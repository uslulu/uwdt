package backend

// macOS: создание utun требует root, поэтому туннель поднимается в два процесса:
//
//  1. Приложение (без прав) слушает unix-сокет и через osascript
//     ("with administrator privileges" — системный диалог пароля) запускает
//     этот же бинарник в режиме `--wg-helper` от root.
//  2. Helper (root) создаёт utun, назначает адрес/MTU, прописывает маршруты
//     и передаёт fd интерфейса приложению через SCM_RIGHTS. Дальше helper
//     висит на сокете: команда "down" или обрыв соединения (краш приложения)
//     → снимает exclude-маршруты и выходит.
//  3. Приложение оборачивает полученный fd в tun.Device и крутит userspace
//     wireguard-go без прав. Ключи helper'у не передаются.
//
// Туннельные маршруты (-interface utunN) умирают вместе с utun, когда
// приложение закрывает fd — явно их снимать не нужно.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
)

// wgHelperMsg — JSON-строка протокола helper→app (одна на строку).
// Сообщение с непустым Name несёт fd интерфейса в OOB (SCM_RIGHTS).
type wgHelperMsg struct {
	Log   string `json:"log,omitempty"`
	Error string `json:"error,omitempty"`
	Name  string `json:"name,omitempty"`
}

// ═══════════════════════════════════════════════════
// APP SIDE
// ═══════════════════════════════════════════════════

func (w *WG) applyDarwin(confText string, turnIPs []string, logf wgLogFunc) error {
	w.teardownDarwin()

	addr, mtuStr, allowedIPs, wgConf := parseWGConfig(confText)
	if addr == "" {
		return fmt.Errorf("Address not found in wg config")
	}
	mtu := 1300
	if mtuStr != "" {
		fmt.Sscanf(mtuStr, "%d", &mtu)
	}

	// Exclude-маршруты — через физический gateway, мимо туннеля
	var excludes []string
	for _, ip := range turnIPs {
		excludes = append(excludes, ip+"/32")
	}
	excludes = append(excludes, vkExcludeCIDRs...)
	for _, dns := range localDNSServers() {
		// DNS, который уже ходит через другой VPN (Cisco и т.п.), не трогаем:
		// иначе запросы к нему уйдут мимо того VPN и начнут таймаутить
		if iface := routeIfaceDarwin(dns); strings.HasPrefix(iface, "utun") {
			logf(fmt.Sprintf("DNS %s обслуживает %s — оставляю ему", dns, iface))
			continue
		}
		excludes = append(excludes, dns+"/32")
	}
	userExcludes := currentUserExcludes(logf)
	if len(userExcludes) > 0 {
		logf(fmt.Sprintf("Пользовательских исключений: %d", len(userExcludes)))
	}
	excludes = append(excludes, userExcludes...)

	// Туннельные маршруты: полный дефолт заменяем на split-default,
	// чтобы не трогать физический default route
	var tunnels []string
	for _, cidr := range allowedIPs {
		if cidr == "0.0.0.0/0" {
			tunnels = append(tunnels, "0.0.0.0/1", "128.0.0.0/1")
		} else {
			tunnels = append(tunnels, cidr)
		}
	}

	// Сокет, на который выйдет helper
	sockDir, err := os.MkdirTemp("", "pwdtt-wg-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(sockDir)
	sock := filepath.Join(sockDir, "h.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		ln.Close()
		return err
	}

	shellCmd := fmt.Sprintf("%s --wg-helper -sock %s -addr %s -mtu %d -exclude %s -tunnel %s >/dev/null 2>&1 &",
		shellQuote(exe), shellQuote(sock), shellQuote(addr), mtu,
		shellQuote(strings.Join(excludes, ",")), shellQuote(strings.Join(tunnels, ",")))
	osa := fmt.Sprintf("do shell script %q with administrator privileges", shellCmd)

	logf("Запрос прав администратора для настройки туннеля…")
	if out, err := exec.Command("osascript", "-e", osa).CombinedOutput(); err != nil {
		ln.Close()
		return fmt.Errorf("права администратора не получены: %w — %s", err, strings.TrimSpace(string(out)))
	}

	ln.SetDeadline(time.Now().Add(30 * time.Second))
	hconn, err := ln.AcceptUnix()
	ln.Close()
	if err != nil {
		return fmt.Errorf("helper не вышел на связь: %w", err)
	}

	tunFile, utunName, err := recvTunFD(hconn, logf)
	if err != nil {
		hconn.Close()
		return err
	}
	hconn.SetReadDeadline(time.Time{})
	logf(fmt.Sprintf("Интерфейс %s получен от helper", utunName))

	tunDev, err := tun.CreateTUNFromFile(tunFile, 0) // mtu уже выставлен helper'ом
	if err != nil {
		hconn.Close()
		return fmt.Errorf("wrap TUN: %w", err)
	}

	logger := &device.Logger{
		Verbosef: func(format string, args ...interface{}) {},
		Errorf:   func(format string, args ...interface{}) { logf(fmt.Sprintf(format, args...)) },
	}
	dev := device.NewDevice(tunDev, conn.NewDefaultBind(), logger)

	logf("Применение WireGuard конфига (uapi)")
	if err := dev.IpcSetOperation(strings.NewReader(uapiConf(wgConf))); err != nil {
		dev.Close()
		hconn.Close()
		return fmt.Errorf("IpcSet: %w", err)
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		hconn.Close()
		return fmt.Errorf("device up: %w", err)
	}

	// Захватываем stateMu только на секцию присвоения состояния: osascript
	// выше может висеть минутами (диалог пароля) — нельзя блокировать Teardown
	w.stateMu.Lock()
	w.activeTun = tunDev
	w.activeDevice = dev
	w.helperConn = hconn
	w.activeExcludeRoutes = excludes
	w.activeUserExcludes = userExcludes
	w.refreshStop = make(chan struct{})
	go w.excludeRefreshLoop(w.refreshStop, logf)
	w.activeRoutesMu.Lock()
	w.activeRoutes = tunnels
	w.activeRoutesMu.Unlock()
	w.stateMu.Unlock()

	logf(fmt.Sprintf("Туннель %s поднят, маршруты: %v", utunName, tunnels))
	if russiaDirectEnabled() {
		go w.RefreshExcludes(logf) // ~9 тыс. сетей — уже через helper, не через командную строку
	}
	return nil
}

func (w *WG) teardownDarwin() {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()

	if w.refreshStop != nil {
		close(w.refreshStop)
		w.refreshStop = nil
	}
	w.activeUserExcludes = nil

	// Останавливаем движок и закрываем fd — utun и его маршруты исчезают сами
	if w.activeDevice != nil {
		w.activeDevice.Close()
		w.activeDevice = nil
	}
	if w.activeTun != nil {
		_ = w.activeTun.Close()
		w.activeTun = nil
	}

	// Просим helper снять exclude-маршруты (без пароля) и ждём его выхода
	if w.helperConn != nil {
		_, _ = w.helperConn.Write([]byte("down\n"))
		_ = w.helperConn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, _ = io.Copy(io.Discard, w.helperConn)
		w.helperConn.Close()
		w.helperConn = nil
	}

	w.activeRoutesMu.Lock()
	w.activeRoutes = nil
	w.activeRoutesMu.Unlock()
	w.activeExcludeRoutes = nil
}

// refreshExcludesDarwin досылает helper'у разницу между применённым и
// текущим списком исключений. Пароль не нужен: helper уже работает от root.
func (w *WG) refreshExcludesDarwin(logf wgLogFunc) {
	next := currentAllExcludes(logf) // DNS может тормозить — резолвим без блокировки

	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	if w.helperConn == nil {
		return // туннель не поднят — список применится при подключении
	}

	// Встроенные исключения (VK, TURN, DNS) не трогаем, даже если пользователь
	// продублировал их в своём списке, а потом удалил
	builtin := map[string]bool{}
	user := map[string]bool{}
	for _, c := range w.activeUserExcludes {
		user[c] = true
	}
	for _, c := range w.activeExcludeRoutes {
		if !user[c] {
			builtin[c] = true
		}
	}

	add, del := diffCIDRs(w.activeUserExcludes, next)
	var cmds strings.Builder
	for _, c := range del {
		if !builtin[c] {
			cmds.WriteString("del " + c + "\n")
		}
	}
	for _, c := range add {
		if !builtin[c] {
			cmds.WriteString("add " + c + "\n")
		}
	}
	if cmds.Len() == 0 {
		return
	}
	if _, err := w.helperConn.Write([]byte(cmds.String())); err != nil {
		logf(fmt.Sprintf("Не удалось применить исключения: %v", err))
		return
	}
	w.activeUserExcludes = next
	logf(fmt.Sprintf("Исключения обновлены: +%d −%d", len(add), len(del)))
}

// routeGetDarwin — что macOS выберет для адреса: назначение, маска, шлюз, интерфейс.
func routeGetDarwin(args ...string) (dst, mask, gw, iface string) {
	out, err := exec.Command("route", append([]string{"-n", "get"}, args...)...).Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		switch f[0] {
		case "destination:":
			dst = f[1]
		case "mask:":
			mask = f[1]
		case "gateway:":
			gw = f[1]
		case "interface:":
			iface = f[1]
		}
	}
	return
}

func routeIfaceDarwin(ip string) string {
	_, _, _, iface := routeGetDarwin(ip)
	return iface
}

// recvTunFD читает сообщения helper'а до получения fd интерфейса.
func recvTunFD(hconn *net.UnixConn, logf wgLogFunc) (*os.File, string, error) {
	buf := make([]byte, 4096)
	oob := make([]byte, 128)
	var acc []byte
	pendingFD := -1
	hconn.SetReadDeadline(time.Now().Add(60 * time.Second))

	for {
		for {
			i := bytes.IndexByte(acc, '\n')
			if i < 0 {
				break
			}
			line := acc[:i]
			acc = acc[i+1:]
			var msg wgHelperMsg
			if json.Unmarshal(line, &msg) != nil {
				continue
			}
			if msg.Log != "" {
				logf(msg.Log)
			}
			if msg.Error != "" {
				return nil, "", fmt.Errorf("helper: %s", msg.Error)
			}
			if msg.Name != "" {
				if pendingFD < 0 {
					return nil, "", fmt.Errorf("fd интерфейса %s не получен", msg.Name)
				}
				return os.NewFile(uintptr(pendingFD), msg.Name), msg.Name, nil
			}
		}

		n, oobn, _, _, err := hconn.ReadMsgUnix(buf, oob)
		if err != nil {
			return nil, "", fmt.Errorf("чтение от helper: %w", err)
		}
		if oobn > 0 {
			if scms, err := syscall.ParseSocketControlMessage(oob[:oobn]); err == nil {
				for _, scm := range scms {
					if fds, err := syscall.ParseUnixRights(&scm); err == nil && len(fds) > 0 {
						pendingFD = fds[0]
						syscall.CloseOnExec(fds[0])
					}
				}
			}
		}
		acc = append(acc, buf[:n]...)
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func defaultGatewayDarwin() string {
	out, err := exec.Command("route", "-n", "get", "default").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "gateway:" {
			return fields[1]
		}
	}
	return ""
}

// cleanupStaleExcludeRoutesDarwin — уборка exclude-маршрутов, переживших
// краш приложения вместе с helper'ом: обычные (не -interface) маршруты сами
// не исчезают и после смены сети ведут на мёртвый шлюз, глуша VK/Яндекс
// даже при выключенном туннеле. Права администратора запрашиваются только
// если протухшие маршруты действительно найдены.
func cleanupStaleExcludeRoutesDarwin(logf wgLogFunc) {
	curGW := defaultGatewayDarwin()
	if curGW == "" {
		return
	}

	// Протухший = специфичный маршрут из vkExcludeCIDRs, чей шлюз ≠ текущему
	staleGWs := map[string]bool{}
	for _, cidr := range vkExcludeCIDRs {
		out, err := exec.Command("route", "-n", "get", strings.SplitN(cidr, "/", 2)[0]).Output()
		if err != nil {
			continue
		}
		var dst, gw string
		for _, line := range strings.Split(string(out), "\n") {
			f := strings.Fields(line)
			if len(f) != 2 {
				continue
			}
			switch f[0] {
			case "destination:":
				dst = f[1]
			case "gateway:":
				gw = f[1]
			}
		}
		if dst != "" && dst != "default" && gw != "" && gw != curGW && net.ParseIP(gw) != nil {
			staleGWs[gw] = true
		}
	}
	if len(staleGWs) == 0 {
		return
	}

	// Сносим все маршруты через мёртвые шлюзы — включая /32 на TURN-серверы
	// и DNS, которые заранее не перечислить
	out, err := exec.Command("netstat", "-rn", "-f", "inet").Output()
	if err != nil {
		return
	}
	var dels []string
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && staleGWs[f[1]] {
			dels = append(dels, "route -q -n delete -net "+shellQuote(f[0]))
		}
	}
	if len(dels) == 0 {
		return
	}

	logf(fmt.Sprintf("Найдены маршруты прошлого запуска через мёртвый шлюз (%d шт), удаляю…", len(dels)))
	osa := fmt.Sprintf("do shell script %q with administrator privileges", strings.Join(dels, "; "))
	if out, err := exec.Command("osascript", "-e", osa).CombinedOutput(); err != nil {
		logf(fmt.Sprintf("уборка маршрутов не удалась: %v — %s", err, strings.TrimSpace(string(out))))
		return
	}
	logf("Протухшие маршруты удалены")
}

func runCmdDarwin(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w — %s", name, args, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ═══════════════════════════════════════════════════
// HELPER SIDE (запускается от root: pwdtt --wg-helper …)
// ═══════════════════════════════════════════════════

// RunWGHelperDarwin — точка входа режима --wg-helper (вызывается из main_darwin.go).
func RunWGHelperDarwin(args []string) {
	fs := flag.NewFlagSet("wg-helper", flag.ExitOnError)
	sockPath := fs.String("sock", "", "путь unix-сокета приложения")
	addr := fs.String("addr", "", "адрес интерфейса (CIDR)")
	mtu := fs.Int("mtu", 1300, "MTU")
	excludes := fs.String("exclude", "", "exclude CIDR через запятую (мимо туннеля)")
	tunnels := fs.String("tunnel", "", "туннельные CIDR через запятую")
	fs.Parse(args)

	raddr, err := net.ResolveUnixAddr("unix", *sockPath)
	if err != nil {
		os.Exit(1)
	}
	hconn, err := net.DialUnix("unix", nil, raddr)
	if err != nil {
		os.Exit(1)
	}
	defer hconn.Close()

	send := func(msg wgHelperMsg, fd int) {
		data, _ := json.Marshal(msg)
		data = append(data, '\n')
		if fd >= 0 {
			hconn.WriteMsgUnix(data, syscall.UnixRights(fd), nil)
		} else {
			hconn.Write(data)
		}
	}
	fail := func(format string, a ...any) {
		send(wgHelperMsg{Error: fmt.Sprintf(format, a...)}, -1)
		os.Exit(1)
	}

	if os.Geteuid() != 0 {
		fail("helper запущен не от root (uid=%d)", os.Getuid())
	}
	if *addr == "" {
		fail("не задан -addr")
	}

	dev, err := tun.CreateTUN("utun", *mtu)
	if err != nil {
		fail("create utun: %v", err)
	}
	name, err := dev.Name()
	if err != nil {
		fail("utun name: %v", err)
	}
	send(wgHelperMsg{Log: fmt.Sprintf("Создан интерфейс %s (mtu=%d)", name, *mtu)}, -1)

	host := strings.SplitN(*addr, "/", 2)[0]
	if err := runCmdDarwin("ifconfig", name, "inet", *addr, host, "alias"); err != nil {
		fail("ifconfig: %v", err)
	}
	_ = runCmdDarwin("ifconfig", name, "up")
	send(wgHelperMsg{Log: fmt.Sprintf("IP установлен: %s", *addr)}, -1)

	gw := defaultGatewayDarwin()
	send(wgHelperMsg{Log: fmt.Sprintf("Default gateway: %s", gw)}, -1)

	// Exclude-маршруты через физический gateway — ДО туннельных,
	// чтобы трафик к turn/VK не успел уйти в туннель
	var added []string
	if gw != "" {
		for _, cidr := range splitCSV(*excludes) {
			// Снимаем возможный остаток прошлого запуска (краш без уборки) —
			// иначе add упрётся в старый маршрут с мёртвым шлюзом
			_ = runCmdDarwin("route", "-q", "-n", "delete", "-net", cidr)
			if err := runCmdDarwin("route", "-q", "-n", "add", "-net", cidr, gw); err != nil {
				send(wgHelperMsg{Log: fmt.Sprintf("exclude route %s: %v", cidr, err)}, -1)
			} else {
				added = append(added, cidr)
			}
		}
		send(wgHelperMsg{Log: fmt.Sprintf("Exclude-маршрутов добавлено: %d", len(added))}, -1)
	}

	// Чужие маршруты к отдельным адресам через физический шлюз — обычно это
	// сервер корпоративного VPN (Cisco и т.п.). Запоминаем ДО туннельных
	// маршрутов и дальше следим, чтобы они не уехали в наш туннель.
	rt, err := openRtSock()
	if err != nil {
		fail("%v", err)
	}
	defer rt.Close()
	g := &helperGuard{tunName: name, rt: rt, gw: gw, ours: map[string]bool{}, foreign: map[string]bool{}, restored: map[string]bool{}}
	for _, c := range added {
		g.ours[c] = true
	}
	g.learnForeignHostRoutes(gw)

	for _, cidr := range splitCSV(*tunnels) {
		if err := runCmdDarwin("route", "-q", "-n", "add", "-net", cidr, "-interface", name); err != nil {
			send(wgHelperMsg{Log: fmt.Sprintf("tunnel route %s: %v", cidr, err)}, -1)
		}
	}

	// Передаём fd интерфейса приложению
	send(wgHelperMsg{Name: name}, int(dev.File().Fd()))

	stop := make(chan struct{})
	go g.watch(stop)

	// Команды приложения: "add CIDR" / "del CIDR" — правка исключений на лету,
	// "down" или обрыв соединения (краш приложения) → уборка
	reader := bufio.NewReader(hconn)
	for {
		line, err := reader.ReadString('\n')
		f := strings.Fields(line)
		if len(f) == 1 && f[0] == "down" {
			break
		}
		if len(f) == 2 && (f[0] == "add" || f[0] == "del") {
			g.apply(f[0], f[1])
		}
		if err != nil {
			break
		}
	}
	close(stop)
	g.cleanup()
	runtime.KeepAlive(dev)
}

// helperGuard (root) держит исключения на месте: после смены сети шлюз
// другой, а Cisco и прочие при переподключении переписывают таблицу.
type helperGuard struct {
	mu       sync.Mutex
	tunName  string
	rt       *rtSock
	gw       string             // текущий физический шлюз
	snap     map[string]rtEntry // снимок таблицы маршрутов
	snapAt   time.Time
	ours     map[string]bool // наши exclude-маршруты (CIDR) — снимаем при выходе
	foreign  map[string]bool // чужие host-маршруты через физический шлюз — бережём
	restored map[string]bool // чужие, которые пришлось восстановить нам — снимаем при выходе
}

// snapshot — таблица маршрутов, не старше пары секунд (на пачку из тысяч add — один снимок).
func (g *helperGuard) snapshot() map[string]rtEntry {
	if g.snap == nil || time.Since(g.snapAt) > 2*time.Second {
		if m, err := gatewayRoutes(); err == nil {
			g.snap, g.snapAt = m, time.Now()
		}
	}
	return g.snap
}

func (g *helperGuard) apply(op, cidr string) {
	c, ok := validExcludeNet(cidr)
	if !ok {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	switch op {
	case "add":
		gw := net.ParseIP(g.gw)
		if gw == nil {
			return
		}
		err := g.rt.Add(c, gw)
		if errors.Is(err, syscall.EEXIST) {
			// Маршрут на эту сеть уже есть. Если он ведёт в другой VPN (Cisco
			// прописал свою сеть) — не трогаем, иначе ломаем корпоративный доступ
			if e := g.snapshot()[c]; strings.HasPrefix(e.iface, "utun") {
				return
			}
			err = g.rt.Change(c, gw)
		}
		if err == nil {
			g.ours[c] = true
		}
	case "del":
		if g.ours[c] {
			_ = g.rt.Delete(c)
			delete(g.ours, c)
		}
	}
}

// learnForeignHostRoutes запоминает статические маршруты к отдельным адресам
// через физический шлюз, поставленные не нами (например Cisco к своему серверу).
func (g *helperGuard) learnForeignHostRoutes(gw string) {
	if gw == "" {
		return
	}
	out, err := exec.Command("netstat", "-rn", "-f", "inet").Output()
	if err != nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[1] != gw || !strings.HasPrefix(f[3], "en") {
			continue
		}
		flags := f[2]
		if !strings.Contains(flags, "G") || !strings.Contains(flags, "S") {
			continue
		}
		dst := f[0]
		if strings.HasSuffix(dst, "/32") {
			dst = strings.TrimSuffix(dst, "/32")
		} else if !strings.Contains(flags, "H") {
			continue
		}
		if ip := net.ParseIP(dst); ip == nil || ip.To4() == nil || g.ours[dst+"/32"] {
			continue
		}
		g.foreign[dst] = true
	}
}

func (g *helperGuard) watch(stop chan struct{}) {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			gw := defaultGatewayDarwin()
			if gw == "" {
				continue
			}
			g.learnForeignHostRoutes(gw)
			g.check(gw)
		}
	}
}

// check возвращает на место наши исключения и чужие host-маршруты,
// если их снесли или они ведут на старый шлюз. Один снимок таблицы на всё.
func (g *helperGuard) check(gw string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.gw = gw
	g.snap = nil
	snap := g.snapshot()
	gwIP := net.ParseIP(gw)
	for c := range g.ours {
		e, exists := snap[c]
		switch {
		case exists && e.gw == gw:
			continue
		case exists && strings.HasPrefix(e.iface, "utun"):
			continue // сеть забрал другой VPN — его право
		case exists:
			_ = g.rt.Change(c, gwIP) // ведёт на старый шлюз после смены сети
		default:
			_ = g.rt.Add(c, gwIP)
		}
	}
	for ip := range g.foreign {
		if _, _, _, iface := routeGetDarwin(ip); iface != g.tunName {
			continue
		}
		_ = runCmdDarwin("route", "-q", "-n", "delete", "-host", ip)
		if runCmdDarwin("route", "-q", "-n", "add", "-host", ip, gw) == nil {
			g.restored[ip] = true
		}
	}
}

func (g *helperGuard) cleanup() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for c := range g.ours {
		_ = g.rt.Delete(c)
	}
	for ip := range g.restored {
		_ = runCmdDarwin("route", "-q", "-n", "delete", "-host", ip)
	}
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
