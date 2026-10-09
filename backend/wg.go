package backend

import (
	"bufio"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

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

type WG struct {
	activeRoutes   []string
	activeRoutesMu sync.Mutex

	// stateMu сериализует Apply/Teardown и защищает поля ниже:
	// Apply вызывается из горутины forwardEvents, Teardown — из потока
	// Wails-биндинга (Disconnect), одновременный доступ = гонка.
	stateMu sync.Mutex

	// Windows/macOS userspace-specific
	activeDevice        *device.Device
	activeTun           tun.Device
	activeExcludeRoutes []string
	physGW              string // physical gateway saved during apply for restore on teardown

	// macOS-specific: соединение с привилегированным helper-процессом
	helperConn *net.UnixConn

	// Пользовательские исключения, применённые сейчас (CIDR), и остановка их обновления
	activeUserExcludes []string
	refreshStop        chan struct{}
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

// CleanupStaleExcludeRoutes убирает exclude-маршруты, оставшиеся после краша
// прошлого запуска (когда helper не успел сделать уборку). Вызывать при
// старте приложения; на платформах без такой проблемы — no-op.
func CleanupStaleExcludeRoutes(logf wgLogFunc) {
	if logf == nil {
		logf = func(msg string) { log.Printf("[WG] %s", msg) }
	}
	if runtime.GOOS == "darwin" {
		cleanupStaleExcludeRoutesDarwin(logf)
	}
}

// RefreshExcludes применяет текущий список исключений к поднятому туннелю
// (после правки списка или для обновления адресов доменов).
func (w *WG) RefreshExcludes(logf wgLogFunc) {
	if logf == nil {
		logf = func(msg string) { log.Printf("[WG] %s", msg) }
	}
	switch runtime.GOOS {
	case "darwin":
		w.refreshExcludesDarwin(logf)
	case "windows":
		w.refreshExcludesWindows(logf)
	default:
		logf("Исключения сохранены — применятся при следующем подключении")
	}
}

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
	return nil
}

func (w *WG) setTunnelRoutesWindows(on bool) error {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	if w.activeDevice == nil {
		return fmt.Errorf("туннель не поднят")
	}
	for _, cidr := range w.activeRoutes {
		if on {
			_ = runCmdWindows("netsh", "interface", "ip", "add", "route", cidr, wgIface)
		} else {
			_ = runCmdWindows("netsh", "interface", "ip", "delete", "route", cidr, wgIface)
		}
	}
	return nil
}

func (w *WG) Teardown() {
	switch runtime.GOOS {
	case "linux":
		w.teardownLinux()
	case "windows":
		w.teardownWindows()
	case "darwin":
		w.teardownDarwin()
	}
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

func (w *WG) applyLinux(conf string, turnIPs []string, logf wgLogFunc) error {
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
	os.Chmod(tmpName, 0o644)

	if err := runCmdLinux("ip", "link", "add", wgIface, "type", "wireguard"); err != nil {
		return fmt.Errorf("ip link add: %w", err)
	}
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
	gw := defaultGatewayLinux()
	if gw != "" {
		for _, ip := range turnIPs {
			if runCmdLinux("ip", "route", "add", ip+"/32", "via", gw) == nil {
				routes = append(routes, ip+"/32")
			}
		}
		for _, cidr := range append(append([]string{}, vkExcludeCIDRs...), currentUserExcludesFast(logf)...) {
			if runCmdLinux("ip", "route", "add", cidr, "via", gw) == nil {
				routes = append(routes, cidr)
			}
		}
		for _, dns := range localDNSServers() {
			if runCmdLinux("ip", "route", "add", dns+"/32", "via", gw) == nil {
				routes = append(routes, dns+"/32")
			}
		}
	}
	for _, cidr := range allowedIPs {
		if runCmdLinux("ip", "route", "add", cidr, "dev", wgIface) == nil {
			routes = append(routes, "dev:"+cidr)
		}
	}

	w.activeRoutesMu.Lock()
	w.activeRoutes = routes
	w.activeRoutesMu.Unlock()
	return nil
}

func (w *WG) teardownLinux() {
	w.activeRoutesMu.Lock()
	routes := w.activeRoutes
	w.activeRoutes = nil
	w.activeRoutesMu.Unlock()

	for _, entry := range routes {
		if strings.HasPrefix(entry, "dev:") {
			_ = runCmdLinux("ip", "route", "del", strings.TrimPrefix(entry, "dev:"), "dev", wgIface)
		} else {
			_ = runCmdLinux("ip", "route", "del", entry)
		}
	}
	_ = runCmdLinux("ip", "link", "del", wgIface)
}

func runCmdLinux(name string, args ...string) error {
	cmd := exec.Command("sudo", append([]string{"-n", name}, args...)...)
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
func InitWintun(dll []byte) { wintunDLLData = dll }

func (w *WG) applyWindows(conf string, turnIPs []string, logf wgLogFunc) error {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	w.teardownWindowsLocked()

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
		if host != "" {
			_ = runCmdWindows("netsh", "interface", "ip", "set", "address",
				"name="+wgIface, "source=static", host, mask)
		}
	}

	// Set low metric on WG interface so it wins over physical default route
	_ = runCmdWindows("netsh", "interface", "ip", "set", "interface",
		"interface="+wgIface, "metric=1")

	// Get physical gateway and raise its metric so WG takes priority
	gw := defaultGatewayWindows()
	logf(fmt.Sprintf("Default gateway: %s", gw))
	if gw != "" {
		// Save original metric for restore on teardown
		w.physGW = gw
		_ = runCmdWindows("route", "change", "0.0.0.0", "mask", "0.0.0.0", gw, "metric", "9999")
	}

	// Exclude routes — VK IPs, DNS, etc go via physical gateway (bypass tunnel)
	var excludes []string
	if gw != "" {
		for _, ip := range turnIPs {
			excludes = append(excludes, ip+"/32")
		}
		excludes = append(excludes, vkExcludeCIDRs...)
		for _, cidr := range excludes {
			ip, mask, err := parseCIDR(cidr)
			if err != nil {
				continue
			}
			_ = runCmdWindows("route", "add", ip, "mask", mask, gw)
		}
		// Пользовательские исключения и «Россия» — из кэша, пачкой через API и ДО
		// маршрутов туннеля, чтобы их трафик ни на миг не ушёл в туннель
		user := currentAllExcludesFast(logf)
		if _, err := bulkRoutes(true, gw, user); err != nil && len(user) > 0 {
			logf("Исключения: " + err.Error())
		}
		excludes = append(excludes, user...)
		w.activeUserExcludes = user
		w.refreshStop = make(chan struct{})
		go w.excludeRefreshLoop(w.refreshStop, logf)
	}
	w.activeExcludeRoutes = excludes

	// Add AllowedIPs routes via the WG interface
	var tunnelRoutes []string
	for _, cidr := range allowedIPs {
		if err := runCmdWindows("netsh", "interface", "ip", "add", "route", cidr, wgIface); err != nil {
			logf(fmt.Sprintf("netsh add route %s failed: %v", cidr, err))
		}
		tunnelRoutes = append(tunnelRoutes, cidr)
	}
	w.activeRoutes = tunnelRoutes

	logf(fmt.Sprintf("Туннель %s поднят, AllowedIPs: %v", wgIface, tunnelRoutes))
	go w.RefreshExcludes(logf) // свежие домены, AS и «Россия» — пачкой через API, после подъёма туннеля
	return nil
}

func (w *WG) teardownWindows() {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	w.teardownWindowsLocked()
}

func (w *WG) teardownWindowsLocked() {
	if w.activeDevice == nil && w.activeTun == nil {
		return
	}

	if w.refreshStop != nil {
		close(w.refreshStop)
		w.refreshStop = nil
	}
	w.activeUserExcludes = nil

	if w.physGW != "" && len(w.activeExcludeRoutes) > 0 {
		if _, err := bulkRoutes(false, w.physGW, w.activeExcludeRoutes); err != nil {
			for _, cidr := range w.activeExcludeRoutes {
				if ip, _, _ := parseCIDR(cidr); ip != "" {
					_ = runCmdWindows("route", "delete", ip)
				}
			}
		}
		w.activeExcludeRoutes = nil
	}

	// Restore physical gateway metric (must happen BEFORE closing WG device)
	if w.physGW != "" {
		_ = runCmdWindows("route", "change", "0.0.0.0", "mask", "0.0.0.0", w.physGW, "metric", "35")
		w.physGW = ""
	}

	// Delete exclude routes — пачкой через API (их могут быть тысячи); fallback на route.exe
	if w.physGW == "" {
		for _, cidr := range w.activeExcludeRoutes {
			if ip, _, _ := parseCIDR(cidr); ip != "" {
				_ = runCmdWindows("route", "delete", ip)
			}
		}
	}
	w.activeExcludeRoutes = nil

	// Delete tunnel routes explicitly to prevent Windows routing table corruption
	for _, cidr := range w.activeRoutes {
		_ = runCmdWindows("netsh", "interface", "ip", "delete", "route", cidr, wgIface)
	}
	w.activeRoutes = nil

	// Close WireGuard device
	if w.activeDevice != nil {
		w.activeDevice.Close()
		w.activeDevice = nil
	}

	// Close TUN device (releases wintun adapter, tunnel routes disappear with it)
	if w.activeTun != nil {
		w.activeTun.Close()
		w.activeTun = nil
	}
}

// refreshExcludesWindows применяет правку списка исключений к поднятому туннелю.
// Приложение на Windows запущено с правами администратора — route работает напрямую.
func (w *WG) refreshExcludesWindows(logf wgLogFunc) {
	next := currentAllExcludes(logf)

	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	if w.activeDevice == nil || w.physGW == "" {
		return
	}
	user := map[string]bool{}
	for _, c := range w.activeUserExcludes {
		user[c] = true
	}
	builtin := map[string]bool{}
	for _, c := range w.activeExcludeRoutes {
		if !user[c] {
			builtin[c] = true
		}
	}
	add, del := diffCIDRs(w.activeUserExcludes, next)
	changed := 0
	var toDel, toAdd []string
	for _, c := range del {
		if !builtin[c] {
			toDel = append(toDel, c)
		}
	}
	for _, c := range add {
		if !builtin[c] {
			toAdd = append(toAdd, c)
		}
	}
	if _, err := bulkRoutes(false, w.physGW, toDel); err != nil && len(toDel) > 0 {
		logf("Снятие исключений: " + err.Error())
	}
	if _, err := bulkRoutes(true, w.physGW, toAdd); err != nil && len(toAdd) > 0 {
		logf("Добавление исключений: " + err.Error())
		return
	}
	changed = len(toDel) + len(toAdd)
	if changed == 0 {
		return
	}
	// Держим полный список актуальным — по нему снимаются маршруты при отключении
	var kept []string
	for _, c := range w.activeExcludeRoutes {
		if builtin[c] {
			kept = append(kept, c)
		}
	}
	w.activeExcludeRoutes = append(kept, next...)
	w.activeUserExcludes = next
	logf(fmt.Sprintf("Исключения обновлены: +%d −%d", len(add), len(del)))
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
	cmd := exec.Command(name, args...)
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
