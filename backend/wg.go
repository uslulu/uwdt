package backend

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
)

// WG — управление WireGuard интерфейсом.
var wg = &WG{}

const wgIface = "pwdtt-wg"

// Статический GUID для Wintun задан в wg_windows_init.go (windows-only),
// чтобы не плодить адаптеры (pwdtt-wg 2, pwdtt-wg 3) при крашах приложения.

// ═══════════════════════════════════════════════════
// PUBLIC API
// ═══════════════════════════════════════════════════

type windowsRoute struct {
	prefix, gateway string
	iface           uint64
}
type WG struct {
	cancelLookup    context.CancelFunc
	linuxOwned      bool
	linuxGateway    string
	ruleMu          sync.Mutex
	ruleStatus      string
	builtinWindows  map[string]bool
	windowsRevision uint64
	activeRoutes    []string
	activeRoutesMu  sync.Mutex

	// stateMu сериализует Apply/Teardown и защищает поля ниже:
	// Apply вызывается из горутины forwardEvents, Teardown — из потока
	// Wails-биндинга (Disconnect), одновременный доступ = гонка.
	stateMu sync.Mutex

	// Windows/macOS userspace-specific
	activeDevice  *device.Device
	activeTun     tun.Device
	ownedWindows  map[windowsRoute]bool
	windowsTunnel map[string]bool
	physGW        string // physical gateway saved during apply for restore on teardown

	// macOS-specific: соединение с привилегированным helper-процессом
	helperConn *net.UnixConn
	tunName    string

	// Режим Xray: запущенный движок и fd интерфейса (закрываются при отключении).
	activeXray    io.Closer
	activeTunFile *os.File

	// Пользовательские исключения, применённые сейчас (CIDR), и остановка их обновления
	activeUserExcludes []string
	refreshStop        chan struct{}
	darwinSession      uint64 // protected by stateMu
	darwinRefresh      uint64 // protected by stateMu
}

type wgLogFunc func(msg string)

func (w *WG) Apply(conf string, turnIPs []string, logf wgLogFunc) error {
	if logf == nil {
		logf = func(msg string) { log.Printf("[WG] %s", msg) }
	}
	switch runtime.GOOS {
	case "linux":
		return w.applyLinux(conf, turnIPs, logf)
	case "windows":
		return w.applyWindows(conf, turnIPs, logf)
	case "darwin":
		return w.applyDarwin(conf, turnIPs, logf)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

// ApplyXray поднимает туннель на движке Xray к серверу из ссылки vless://.
// serverIPs — адреса сервера: они идут мимо туннеля, иначе Xray зациклится сам на себя.
func (w *WG) ApplyXray(link string, serverIPs []string, logf wgLogFunc) error {
	if logf == nil {
		logf = func(msg string) { log.Printf("[XRAY] %s", msg) }
	}
	switch runtime.GOOS {
	case "darwin":
		return w.applyXrayDarwin(link, serverIPs, logf)
	case "windows":
		return w.applyXrayWindows(link, serverIPs, logf)
	default:
		return fmt.Errorf("подписки на %s не поддерживаются", runtime.GOOS)
	}
}

// XrayTraffic — трафик сервера подписки с начала сессии; ok=false, если Xray не запущен.
func (w *WG) XrayTraffic() (up, down int64, ok bool) {
	w.stateMu.Lock()
	x, _ := w.activeXray.(*xrayInstance)
	w.stateMu.Unlock()
	if x == nil {
		return 0, 0, false
	}
	up, down = x.Traffic()
	return up, down, true
}

// RefreshExcludes применяет текущий список исключений к поднятому туннелю
// (после правки списка или для обновления адресов доменов).
func (w *WG) beginLookup() (context.Context, context.CancelFunc) {
	if w.cancelLookup != nil {
		w.cancelLookup()
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.cancelLookup = cancel
	return ctx, cancel
}
func (w *WG) setRuleStatus(status string) {
	w.ruleMu.Lock()
	defer w.ruleMu.Unlock()
	w.ruleStatus = status
}
func (w *WG) refreshTask(logf wgLogFunc) func() {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	if logf == nil {
		logf = func(msg string) { log.Print(msg) }
	}
	session, stop := w.darwinSession, w.refreshStop
	if w.helperConn != nil || stop != nil {
		w.setRuleStatus("Применяется")
	}
	return func() {
		switch runtime.GOOS {
		case "darwin":
			w.refreshExcludesDarwinSession(logf, session)
		case "windows":
			w.refreshWindowsSession(logf, stop)
		case "linux":
			w.setRuleStatus("При следующем подключении · Россия напрямую на Linux не поддерживается")
		}
	}
}
func (w *WG) RefreshExcludes(logf wgLogFunc) { w.refreshTask(logf)() }

// SetTunnelRoutes снимает (false) или возвращает (true) маршруты туннеля, не
// разрывая его: если каналы к серверу пропали, интернет идёт напрямую, а не в
// пустой туннель.
func (w *WG) SetTunnelRoutes(on bool) error {
	switch runtime.GOOS {
	case "darwin":
		return w.setTunnelRoutesDarwin(on)
	case "windows":
		return w.setTunnelRoutesWindows(on)
	}
	return fmt.Errorf("изменение маршрутов активного туннеля не поддерживается на %s", runtime.GOOS)
}

func (w *WG) setTunnelRoutesWindows(on bool) error {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	if w.activeDevice == nil {
		return fmt.Errorf("туннель не поднят")
	}
	op := "delete"
	if on {
		op = "add"
	}
	var errs []error
	for _, cidr := range w.activeRoutes {
		if w.windowsTunnel[cidr] == on {
			continue
		}
		if err := runCmdWindows("netsh", "interface", "ip", op, "route", cidr, wgIface); err != nil {
			errs = append(errs, err)
		} else {
			w.windowsTunnel[cidr] = on
		}
	}
	return errors.Join(errs...)

}

func (w *WG) Teardown() error {
	switch runtime.GOOS {
	case "linux":
		return w.teardownLinux()
	case "windows":
		return w.teardownWindows()
	case "darwin":
		return w.teardownDarwin()
	}
	return nil
}

// ═══════════════════════════════════════════════════
// COMMON — парсинг конфига
// ═══════════════════════════════════════════════════

var wgQuickOnlyFields = map[string]bool{
	"address": true, "dns": true, "mtu": true,
	"preup": true, "postup": true, "predown": true, "postdown": true,
	"saveconfig": true,
}

var vkExcludeCIDRs = []string{
	"87.240.128.0/18", "87.240.192.0/19", "90.156.0.0/16",
	"93.186.224.0/21", "95.142.192.0/21", "95.163.0.0/16",
	"95.213.0.0/18", "155.212.192.0/20", "185.16.28.0/22",
	"194.67.64.0/18", "195.82.146.0/23", "213.180.193.0/24",
	"77.88.0.0/18", "8.8.8.0/24", "1.1.1.0/24",
}

// tunnelDNSDefault — DNS, если сервер не прислал свой. Оба адреса не входят во
// встроенные исключения (1.1.1.0/24 и 8.8.8.0/24 идут мимо туннеля), значит
// запросы к ним идут через туннель и провайдер их не подменит.
var tunnelDNSDefault = []string{"1.0.0.1", "8.8.4.4"}

// isTunnelDNS — адрес из tunnelDNSDefault. Такие DNS должны ходить ТОЛЬКО через
// туннель: если resolv.conf ещё держит их от прошлой сессии (краш без
// восстановления), исключать их через физический шлюз нельзя — провайдерский
// DPI перехватит запрос и вернёт SERVFAIL для заблокированных доменов.
func isTunnelDNS(s string) bool {
	for _, d := range tunnelDNSDefault {
		if s == d {
			return true
		}
	}
	return false
}

// parseWGDNS — IPv4-адреса из строки DNS = … конфига (имена поиска пропускаем).
func parseWGDNS(conf string) []string {
	var out []string
	for _, line := range strings.Split(conf, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "=", 2)
		if len(parts) != 2 || !strings.EqualFold(strings.TrimSpace(parts[0]), "dns") {
			continue
		}
		for _, v := range strings.Split(parts[1], ",") {
			if ip := net.ParseIP(strings.TrimSpace(v)); ip != nil && ip.To4() != nil {
				out = append(out, ip.String())
			}
		}
	}
	if len(out) == 0 {
		return tunnelDNSDefault
	}
	return out
}

func parseWGConfig(conf string) (addr, mtu string, allowedIPs []string, wgConf string) {
	var out strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(conf))
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) == 2 {
			key := strings.ToLower(strings.TrimSpace(parts[0]))
			val := strings.TrimSpace(parts[1])
			switch key {
			case "address":
				addr = val
				continue
			case "mtu":
				mtu = val
				continue
			case "allowedips":
				for _, cidr := range strings.Split(val, ",") {
					if c := strings.TrimSpace(cidr); c != "" {
						allowedIPs = append(allowedIPs, c)
					}
				}
			default:
				if wgQuickOnlyFields[key] {
					continue
				}
			}
		}
		out.WriteString(line + "\n")
	}
	wgConf = out.String()
	return
}

// ═══════════════════════════════════════════════════
// LINUX — ip/wg команды через sudo
// ═══════════════════════════════════════════════════

func (w *WG) applyLinux(conf string, turnIPs []string, logf wgLogFunc) (result error) {
	w.teardownLinux()

	addr, mtu, allowedIPs, wgConf := parseWGConfig(conf)
	if addr == "" {
		return fmt.Errorf("Address not found in wg config")
	}

	tmp, err := os.CreateTemp("", "wg-turn-*.conf")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.WriteString(wgConf); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	if err := runCmdLinux("ip", "link", "add", wgIface, "type", "wireguard"); err != nil {
		return fmt.Errorf("ip link add: %w", err)
	}
	w.activeRoutesMu.Lock()
	w.linuxOwned = true
	w.activeRoutesMu.Unlock()
	defer func() {
		if result != nil {
			w.teardownLinux()
		}
	}()
	if err := runCmdLinux("wg", "setconf", wgIface, tmpName); err != nil {
		return fmt.Errorf("wg setconf: %w", err)
	}
	_ = runCmdLinux("ip", "addr", "flush", "dev", wgIface)
	if err := runCmdLinux("ip", "addr", "add", addr, "dev", wgIface); err != nil {
		return fmt.Errorf("ip addr add: %w", err)
	}
	if mtu != "" {
		_ = runCmdLinux("ip", "link", "set", wgIface, "mtu", mtu)
	}
	if err := runCmdLinux("ip", "link", "set", wgIface, "up"); err != nil {
		return fmt.Errorf("ip link set up: %w", err)
	}

	var routes []string
	var failures []error
	gw := defaultGatewayLinux()
	if gw == "" {
		return fmt.Errorf("нет физического шлюза")
	}
	w.linuxGateway = gw
	user, missing := resolvedRules(context.Background(), ruleEntries(), logf, false)
	var direct []string
	for _, ip := range append(append([]string{}, turnIPs...), localDNSServers()...) {
		if addr := net.ParseIP(ip); addr != nil && addr.To4() != nil {
			direct = append(direct, addr.String()+"/32")
		}
	}
	direct = append(direct, vkExcludeCIDRs...)
	direct = append(direct, user...)
	var ruleFailures []string
	seen := map[string]bool{}
	for _, cidr := range direct {
		if seen[cidr] {
			continue
		}
		seen[cidr] = true
		if err := runCmdLinux("ip", "route", "add", cidr, "via", gw); err != nil {
			ruleFailures = append(ruleFailures, fmt.Sprintf("%s: %v", cidr, err))
		} else {
			routes = append(routes, cidr)
		}
	}

	for _, p := range allowedIPs {
		prefixes := []string{p}
		if p == "0.0.0.0/0" {
			prefixes = []string{"0.0.0.0/1", "128.0.0.0/1"}
		}
		for _, cidr := range prefixes {
			if err := runCmdLinux("ip", "route", "add", cidr, "dev", wgIface); err != nil {
				failures = append(failures, err)
			} else {
				routes = append(routes, "dev:"+cidr)
			}
		}
	}
	w.activeRoutesMu.Lock()
	w.activeRoutes = routes
	w.activeRoutesMu.Unlock()
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	if russiaDirectEnabled() {
		ruleFailures = append(ruleFailures, "набор российских IPv4-сетей не поддерживается на Linux")
	}
	if len(missing) > 0 {
		ruleFailures = append(ruleFailures, "нет IPv4 для "+strings.Join(missing, ", "))
	}
	if len(ruleFailures) > 0 {
		w.setRuleStatus("Ошибка применения: " + strings.Join(ruleFailures, "\n"))
	} else {
		w.setRuleStatus("Действует · изменения при следующем подключении")
	}

	return nil
}

func (w *WG) teardownLinux() error {
	var failures []error
	w.activeRoutesMu.Lock()
	if !w.linuxOwned {
		w.activeRoutesMu.Unlock()
		return nil
	}
	w.linuxOwned = false
	w.setRuleStatus("")
	routes := w.activeRoutes
	w.activeRoutes = nil
	w.activeRoutesMu.Unlock()

	for _, entry := range routes {
		if strings.HasPrefix(entry, "dev:") {
			if err := runCmdLinux("ip", "route", "del", strings.TrimPrefix(entry, "dev:"), "dev", wgIface); err != nil {
				failures = append(failures, err)
			}
		} else {
			if err := runCmdLinux("ip", "route", "del", entry, "via", w.linuxGateway); err != nil {
				failures = append(failures, err)
			}
		}
	}
	if err := runCmdLinux("ip", "link", "del", wgIface); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func runCmdLinux(name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sudo", append([]string{"-n", name}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w — %s", name, args, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func defaultGatewayLinux() string {
	cmd := exec.Command("ip", "route", "show", "default")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(out))
	for i, f := range fields {
		if f == "via" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return ""
}

func localDNSServers() []string {
	data, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return nil
	}
	var result []string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "nameserver" {
			continue
		}
		ip := net.ParseIP(fields[1])
		if ip == nil || ip.IsLoopback() {
			continue
		}
		result = append(result, fields[1])
	}
	return result
}

// ═══════════════════════════════════════════════════
// WINDOWS — wintun + netsh + route
// ═══════════════════════════════════════════════════

var wintunDLLData []byte

// InitWintun устанавливает wintun.dll (вызывается из main_windows.go).
func InitWintun(dll []byte) {
	wintunDLLData = dll
	clearTunnelDNSWindows() // правило DNS от аварийно оборванного сеанса
}

func (w *WG) applyWindows(conf string, turnIPs []string, logf wgLogFunc) (result error) {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	w.teardownWindowsLocked()
	defer func() {
		if result != nil {
			w.teardownWindowsLocked()
		}
	}()

	if err := extractWintun(); err != nil {
		return fmt.Errorf("extract wintun.dll: %w", err)
	}

	addr, mtuStr, allowedIPs, wgConf := parseWGConfig(conf)
	if addr == "" {
		return fmt.Errorf("Address not found in wg config")
	}

	mtu := 1300
	if mtuStr != "" {
		fmt.Sscanf(mtuStr, "%d", &mtu)
	}

	// Create wintun TUN interface
	logf(fmt.Sprintf("Создание TUN интерфейса %q (mtu=%d)", wgIface, mtu))
	tunDev, err := tun.CreateTUN(wgIface, mtu)
	if err != nil {
		return fmt.Errorf("create TUN: %w", err)
	}
	w.activeTun = tunDev

	// Create userspace WireGuard device
	logger := &device.Logger{
		Verbosef: func(format string, args ...interface{}) {},
		Errorf:   func(format string, args ...interface{}) { logf(fmt.Sprintf(format, args...)) },
	}
	dev := device.NewDevice(tunDev, conn.NewDefaultBind(), logger)
	w.activeDevice = dev

	uapi := uapiConf(wgConf)
	logf("Применение WireGuard конфига (uapi)")
	if err := dev.IpcSetOperation(strings.NewReader(uapi)); err != nil {
		return fmt.Errorf("IpcSet: %w", err)
	}

	if err := dev.Up(); err != nil {
		return fmt.Errorf("device up: %w", err)
	}
	logf("WireGuard устройство поднято")

	// Set IP address on the interface
	if err := runCmdWindows("netsh", "interface", "ip", "set", "address",
		"name="+wgIface, "source=static", addr, "none"); err != nil {
		host, mask, _ := parseCIDR(addr)
		if host == "" {
			return err
		}
		if err := runCmdWindows("netsh", "interface", "ip", "set", "address", "name="+wgIface, "source=static", host, mask); err != nil {
			return err
		}
	}

	// Set low metric on WG interface so it wins over physical default route
	if err := runCmdWindows("netsh", "interface", "ip", "set", "interface", "interface="+wgIface, "metric=1"); err != nil {
		return err
	}

	// Split default routes take precedence without modifying the physical metric.
	gw := defaultGatewayWindows()
	if gw == "" {
		return fmt.Errorf("нет физического шлюза")
	}
	w.physGW = gw
	// Exclude routes — VK IPs, DNS, etc go via physical gateway (bypass tunnel)
	var excludes []string
	if gw != "" {
		for _, ip := range turnIPs {
			excludes = append(excludes, ip+"/32")
		}
		excludes = append(excludes, vkExcludeCIDRs...)
		// Пользовательские исключения и «Россия» — из кэша, пачкой через API и ДО
		// маршрутов туннеля, чтобы их трафик ни на миг не ушёл в туннель
		w.builtinWindows = map[string]bool{}
		for _, c := range excludes {
			w.builtinWindows[c] = true
		}
		user := currentAllExcludesFast(logf)
		if _, err := w.bulkRoutes(true, gw, excludes); err != nil {
			w.teardownWindowsLocked()
			return err
		}
		if _, err := w.bulkRoutes(true, gw, user); err != nil {
			logf(err.Error())
			user = nil
		}
		w.activeUserExcludes = user
		w.refreshStop = make(chan struct{})
		stop := w.refreshStop
		go excludeRefreshLoop(stop, func() { w.refreshWindowsSession(logf, stop) })
	}

	// Add AllowedIPs routes via the WG interface
	w.windowsTunnel = map[string]bool{}
	var tunnelRoutes []string
	var routed []string
	for _, p := range allowedIPs {
		if p == "0.0.0.0/0" {
			routed = append(routed, "0.0.0.0/1", "128.0.0.0/1")
		} else {
			routed = append(routed, p)
		}
	}
	for _, cidr := range routed {
		if err := runCmdWindows("netsh", "interface", "ip", "add", "route", cidr, wgIface); err != nil {
			w.activeRoutes = tunnelRoutes
			w.teardownWindowsLocked()
			return fmt.Errorf("маршрут %s: %w", cidr, err)
		}
		w.windowsTunnel[cidr] = true
		tunnelRoutes = append(tunnelRoutes, cidr)
	}
	w.activeRoutes = tunnelRoutes
	if err := setTunnelDNSWindows(tunDev, parseWGDNS(conf), logf); err != nil {
		w.teardownWindowsLocked()
		return err
	}

	logf(fmt.Sprintf("Туннель %s поднят, AllowedIPs: %v", wgIface, tunnelRoutes))
	go w.refreshWindowsSession(logf, w.refreshStop) // свежие домены, AS и «Россия» — пачкой через API, после подъёма туннеля
	return nil
}

func (w *WG) teardownWindows() error {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	return w.teardownWindowsLocked()
}

func (w *WG) teardownWindowsLocked() error {
	var failures []error
	for key := range w.ownedWindows {
		if err := w.releaseWindowsRoute(key); err != nil {
			failures = append(failures, err)
		}
	}
	w.windowsRevision++
	if w.cancelLookup != nil {
		w.cancelLookup()
		w.cancelLookup = nil
	}
	w.setRuleStatus("")
	if w.activeDevice == nil && w.activeTun == nil && w.activeXray == nil {
		return errors.Join(failures...)
	}
	// Каждый шаг — с длительностью в лог: по нему видно, на чём висит «Отключение…»
	step := func(name string, f func()) {
		t := time.Now()
		f()
		if d := time.Since(t); d > 500*time.Millisecond {
			log.Printf("[WG] Отключение: %s — %s", name, d.Round(time.Millisecond))
		}
	}

	step("DNS", func() {
		if err := clearTunnelDNSWindows(); err != nil {
			failures = append(failures, err)
		}
	})

	if w.refreshStop != nil {
		close(w.refreshStop)
		w.refreshStop = nil
	}
	w.activeUserExcludes = nil

	w.physGW = ""
	// Delete tunnel routes explicitly to prevent Windows routing table corruption
	step("маршруты туннеля", func() {
		for _, cidr := range w.activeRoutes {
			if w.windowsTunnel[cidr] {
				if err := runCmdWindows("netsh", "interface", "ip", "delete", "route", cidr, wgIface); err != nil {
					failures = append(failures, err)
				}
			}
		}
	})
	w.activeRoutes = nil

	w.physGW = ""

	// Xray закрывает адаптер — маршруты туннеля исчезают вместе с ним
	if w.activeXray != nil {
		step("Xray", func() { _ = w.activeXray.Close() })
		w.activeXray = nil
	}

	// Close WireGuard device
	if w.activeDevice != nil {
		step("WireGuard", w.activeDevice.Close)
		w.activeDevice = nil
	}

	// Close TUN device (releases wintun adapter, tunnel routes disappear with it)
	if w.activeTun != nil {
		step("адаптер Wintun", func() { w.activeTun.Close() })
		w.activeTun = nil
	}
	return errors.Join(failures...)
}

// refreshExcludesWindows применяет правку списка исключений к поднятому туннелю.
// Приложение на Windows запущено с правами администратора — route работает напрямую.
func (w *WG) refreshWindowsSession(logf wgLogFunc, expected chan struct{}) {
	w.stateMu.Lock()
	if w.refreshStop == nil || w.refreshStop != expected {
		w.stateMu.Unlock()
		return
	}
	session := w.refreshStop
	w.windowsRevision++
	version := w.windowsRevision
	ctx, cancel := w.beginLookup()
	defer cancel()
	w.setRuleStatus("Применяется")
	w.stateMu.Unlock()
	next, unresolved := allExcludes(ctx, logf, false)

	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	if session != w.refreshStop || version != w.windowsRevision {
		return
	}
	toAdd, toDel := w.planWindowsUserRoutes(next)
	_, removeErr := w.bulkRoutes(false, w.physGW, toDel)
	added, addErr := w.bulkRoutes(true, w.physGW, toAdd)
	if err := errors.Join(removeErr, addErr); err != nil {
		w.setRuleStatus("Ошибка применения: " + err.Error())
		return
	}
	if len(unresolved) > 0 {
		w.setRuleStatus("Нет IPv4-сетей для " + strings.Join(unresolved, ", "))
	} else {
		w.setRuleStatus("Действует")
	}
	w.activeUserExcludes = next
	if added+len(toDel) > 0 {
		logf(fmt.Sprintf("Исключения обновлены: +%d −%d", added, len(toDel)))
	}
}

// Reconcile against the successful creates, including a partially applied batch.
func (w *WG) planWindowsUserRoutes(next []string) (add, remove []string) {
	wanted := map[string]bool{}
	for _, prefix := range next {
		if !wanted[prefix] && !w.builtinWindows[prefix] {
			add = append(add, prefix)
		}
		wanted[prefix] = true
	}
	for key := range w.ownedWindows {
		if key.gateway == w.physGW && !wanted[key.prefix] && !w.builtinWindows[key.prefix] {
			remove = append(remove, key.prefix)
		}
	}
	return
}

func extractWintun() error {
	if wintunDLLData == nil {
		return fmt.Errorf("wintun.dll not embedded")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dst := filepath.Join(filepath.Dir(exe), "wintun.dll")
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	return os.WriteFile(dst, wintunDLLData, 0644)
}

func uapiConf(wgConf string) string {
	var sb strings.Builder
	inPeer := false
	for _, line := range strings.Split(wgConf, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if trimmed == "[Interface]" {
			inPeer = false
			continue
		}
		if trimmed == "[Peer]" {
			if inPeer {
				sb.WriteString("\n")
			}
			inPeer = true
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		val := strings.TrimSpace(parts[1])

		switch key {
		case "privatekey":
			sb.WriteString("private_key=" + toHex(val) + "\n")
		case "listenport":
			sb.WriteString("listen_port=" + val + "\n")
		case "publickey":
			sb.WriteString("public_key=" + toHex(val) + "\n")
		case "presharedkey":
			sb.WriteString("preshared_key=" + toHex(val) + "\n")
		case "endpoint":
			sb.WriteString("endpoint=" + val + "\n")
		case "allowedips":
			for _, cidr := range strings.Split(val, ",") {
				if c := strings.TrimSpace(cidr); c != "" {
					sb.WriteString("allowed_ip=" + c + "\n")
				}
			}
		case "persistentkeepalive":
			sb.WriteString("persistent_keepalive_interval=" + val + "\n")
		}
	}
	sb.WriteString("\n")
	return sb.String()
}

func toHex(b64 string) string {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return b64
	}
	return hex.EncodeToString(raw)
}

func runCmdWindows(name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w — %s", name, args, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func defaultGatewayWindows() string {
	cmd := exec.Command("cmd", "/c", "route print 0.0.0.0")
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "0.0.0.0" && fields[1] == "0.0.0.0" {
			return fields[2]
		}
	}
	return ""
}

func parseCIDR(cidr string) (ip, mask string, err error) {
	parts := strings.SplitN(cidr, "/", 2)
	if len(parts) != 2 {
		return cidr, "255.255.255.255", nil
	}
	ip = parts[0]
	var prefix int
	if _, e := fmt.Sscanf(parts[1], "%d", &prefix); e != nil || prefix < 0 || prefix > 32 {
		return "", "", fmt.Errorf("invalid prefix %q", parts[1])
	}
	var m uint32
	if prefix > 0 {
		m = ^uint32(0) << (32 - prefix)
	}
	mask = fmt.Sprintf("%d.%d.%d.%d", m>>24, (m>>16)&0xff, (m>>8)&0xff, m&0xff)
	return ip, mask, nil
}

// ═══════════════════════════════════════════════════
// Exported wrappers for testing
// ═══════════════════════════════════════════════════

func ParseWGConfig(conf string) (addr, mtu string, allowedIPs []string, wgConf string) {
	return parseWGConfig(conf)
}

func ParseWGDNS(conf string) []string { return parseWGDNS(conf) }

func LocalDNSServers() []string {
	return localDNSServers()
}

func DefaultGatewayLinux() string {
	return defaultGatewayLinux()
}

func UapiConf(wgConf string) string {
	return uapiConf(wgConf)
}

func ToHex(b64 string) string {
	return toHex(b64)
}

func ParseCIDR(cidr string) (ip, mask string, err error) {
	return parseCIDR(cidr)
}
