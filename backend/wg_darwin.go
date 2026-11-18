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
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"

	"log"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
)

// wgHelperMsg — JSON-строка протокола helper→app (одна на строку).
// Сообщение с непустым Name несёт fd интерфейса в OOB (SCM_RIGHTS).
type wgHelperMsg struct {
	ID    string `json:"id,omitempty"`
	Log   string `json:"log,omitempty"`
	Error string `json:"error,omitempty"`
	Name  string `json:"name,omitempty"`
}

// ═══════════════════════════════════════════════════
// APP SIDE
// ═══════════════════════════════════════════════════

func (w *WG) applyDarwin(confText string, turnIPs []string, logf wgLogFunc) error {
	if err := w.teardownDarwin(); err != nil {
		return err
	}

	addr, mtuStr, allowedIPs, wgConf := parseWGConfig(confText)
	if addr == "" {
		return fmt.Errorf("Address not found in wg config")
	}
	mtu := 1300
	if mtuStr != "" {
		fmt.Sscanf(mtuStr, "%d", &mtu)
	}

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

	h, err := startTunHelperDarwin(addr, mtu, turnIPs, tunnels, logf)
	if err != nil {
		return err
	}
	tunFile, utunName, hconn := h.file, h.name, h.conn

	tunDev, err := tun.CreateTUNFromFile(tunFile, 0) // mtu уже выставлен helper'ом
	if err != nil {
		tunFile.Close()
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

	// Меняем системный DNS только для полного туннеля: при split tunnel
	// публичные DNS могут оказаться за пределами AllowedIPs.
	fullLow, fullHigh := false, false
	for _, cidr := range tunnels {
		fullLow = fullLow || cidr == "0.0.0.0/1"
		fullHigh = fullHigh || cidr == "128.0.0.0/1"
	}
	if fullLow && fullHigh {
		if err := configureTunnelDNSDarwin(h, logf); err != nil {
			dev.Close()
			hconn.Close()
			return err
		}
	}

	// Захватываем stateMu только на секцию присвоения состояния: osascript
	// выше может висеть минутами (диалог пароля) — нельзя блокировать Teardown
	w.stateMu.Lock()
	w.activeTun = tunDev
	w.activeDevice = dev
	session := w.setDarwinStateLocked(h, tunnels, logf)
	w.stateMu.Unlock()

	logf(fmt.Sprintf("Туннель %s поднят, маршруты: %v", utunName, tunnels))
	// Свежие адреса доменов, сети AS и «Россия» — в фоне, через helper
	go w.refreshExcludesDarwinSession(logf, session)
	return nil
}

// xrayTunAddr — адрес нашего конца utun в режиме Xray: сам Xray его не использует,
// но интерфейсу macOS нужен IPv4, чтобы на него можно было повесить маршруты
const xrayTunAddr = "10.255.255.1/32"

func (w *WG) applyXrayDarwin(link string, serverIPs []string, logf wgLogFunc) error {
	if err := w.teardownDarwin(); err != nil {
		return err
	}

	tunnels := []string{"0.0.0.0/1", "128.0.0.0/1"}
	h, err := startTunHelperDarwin(xrayTunAddr, xrayMTU, serverIPs, tunnels, logf)
	if err != nil {
		return err
	}

	logf("Запуск Xray…")
	x, err := startXray(link, h.file.Fd(), xrayTunName(), xrayMTU, logf)
	if err != nil {
		h.file.Close()
		h.conn.Close()
		return err
	}

	if err := configureTunnelDNSDarwin(h, logf); err != nil {
		x.Close()
		h.file.Close()
		h.conn.Close()
		return err
	}

	w.stateMu.Lock()
	w.activeXray = x
	w.activeTunFile = h.file
	session := w.setDarwinStateLocked(h, tunnels, logf)
	w.stateMu.Unlock()

	logf(fmt.Sprintf("Туннель %s (Xray) поднят, маршруты: %v", h.name, tunnels))
	go w.refreshExcludesDarwinSession(logf, session)
	return nil
}

type helperRequest struct {
	ID     string   `json:"id"`
	Op     string   `json:"op"`
	Values []string `json:"values,omitempty"`
}

func helperCall(conn *net.UnixConn, op string, values []string, logf wgLogFunc) error {
	return helperCallWithin(conn, op, values, logf, 20*time.Second)
}
func helperCallWithin(conn *net.UnixConn, op string, values []string, logf wgLogFunc, timeout time.Duration) error {
	req := helperRequest{ID: uuid.NewString(), Op: op, Values: values}
	conn.SetDeadline(time.Now().Add(timeout))
	defer conn.SetDeadline(time.Time{})
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return err
	}
	dec := json.NewDecoder(conn)
	for {
		var reply wgHelperMsg
		if err := dec.Decode(&reply); err != nil {
			return fmt.Errorf("helper: %w", err)
		}
		if reply.Log != "" && logf != nil {
			logf(reply.Log)
		}
		if reply.ID != req.ID {
			continue
		}
		if reply.Error != "" {
			return errors.New(reply.Error)
		}
		return nil
	}
}
func configureTunnelDNSDarwin(h *tunHelper, logf wgLogFunc) error {
	if dnsByOtherVPN(h.name, logf) {
		logf("DNS обслуживает другой VPN")
		return nil
	}
	return helperCall(h.conn, "dns", tunnelDNSDefault, logf)
}

// dnsByOtherVPN — какой-то из DNS-серверов системы ходит через другой VPN (utun).
func dnsByOtherVPN(ownTun string, logf wgLogFunc) bool {
	for _, dns := range localDNSServers() {
		if iface := routeIfaceDarwin(dns); strings.HasPrefix(iface, "utun") && iface != ownTun {
			logf(fmt.Sprintf("DNS %s обслуживает %s", dns, iface))
			return true
		}
	}
	return false
}

// tunHelper — интерфейс от root-помощника: fd utun, его имя, связь с помощником
// и применённые им исключения.
type tunHelper struct {
	file         *os.File
	name         string
	conn         *net.UnixConn
	excludes     []string // все исключения (встроенные + пользовательские)
	userExcludes []string
}

// startTunHelperDarwin запрашивает пароль администратора, запускает helper,
// который создаёт utun с адресом addr, ставит исключения (directIPs, VK, DNS,
// пользовательские и «Россия») и маршруты tunnels, и получает fd интерфейса.
// Общий для WireGuard и Xray.
func startTunHelperDarwin(addr string, mtu int, directIPs, tunnels []string, logf wgLogFunc) (*tunHelper, error) {
	// Exclude-маршруты — через физический gateway, мимо туннеля
	var excludes []string
	for _, ip := range directIPs {
		excludes = append(excludes, ip+"/32")
	}
	excludes = append(excludes, vkExcludeCIDRs...)
	for _, dns := range localDNSServers() {
		// Адреса tunnelDNSDefault — не «прежний DNS», а наш же остаток от
		// прошлой сессии: исключать их мимо туннеля нельзя (см. isTunnelDNS)
		if isTunnelDNS(dns) {
			continue
		}
		// DNS, который уже ходит через другой VPN (Cisco и т.п.), не трогаем:
		// иначе запросы к нему уйдут мимо того VPN и начнут таймаутить
		if iface := routeIfaceDarwin(dns); strings.HasPrefix(iface, "utun") {
			logf(fmt.Sprintf("DNS %s обслуживает %s — оставляю ему", dns, iface))
			continue
		}
		excludes = append(excludes, dns+"/32")
	}
	// Пользовательские исключения и «Россия» — из кэша, без DNS; ставятся helper'ом
	// одним пакетом ДО туннельных маршрутов, чтобы их трафик ни на миг не ушёл в туннель
	userExcludes := currentAllExcludesFast(logf)
	if len(userExcludes) > 0 {
		logf(fmt.Sprintf("Исключений из туннеля: %d", len(userExcludes)))
	}

	// Сокет, на который выйдет helper
	sockDir, err := os.MkdirTemp("", "pwdtt-wg-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(sockDir)
	sock := filepath.Join(sockDir, "h.sock")
	exclFile := filepath.Join(sockDir, "exclude.txt")
	if err := os.WriteFile(exclFile, []byte(strings.Join(userExcludes, "\n")), 0o644); err != nil {
		return nil, err
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		return nil, err
	}

	exe, err := os.Executable()
	if err != nil {
		ln.Close()
		return nil, err
	}

	shellCmd := fmt.Sprintf("%s --wg-helper -sock %s -addr %s -mtu %d -exclude %s -exclude-file %s -tunnel %s -diagnostic-ips %s >/dev/null 2>&1 &",
		shellQuote(exe), shellQuote(sock), shellQuote(addr), mtu,
		shellQuote(strings.Join(excludes, ",")), shellQuote(exclFile), shellQuote(strings.Join(tunnels, ",")), shellQuote(strings.Join(directIPs, ",")))
	osa := fmt.Sprintf("do shell script %q with administrator privileges", shellCmd)

	logf("Запрос прав администратора для настройки туннеля…")
	prompt, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(prompt, "osascript", "-e", osa).CombinedOutput(); err != nil {
		ln.Close()
		return nil, fmt.Errorf("права администратора не получены: %w — %s", err, strings.TrimSpace(string(out)))
	}

	ln.SetDeadline(time.Now().Add(30 * time.Second))
	hconn, err := ln.AcceptUnix()
	ln.Close()
	if err != nil {
		return nil, fmt.Errorf("helper не вышел на связь: %w", err)
	}

	tunFile, utunName, err := recvTunFD(hconn, logf)
	if err != nil {
		hconn.Close()
		return nil, err
	}
	hconn.SetReadDeadline(time.Time{})
	logf(fmt.Sprintf("Интерфейс %s получен от helper", utunName))
	return &tunHelper{file: tunFile, name: utunName, conn: hconn, excludes: excludes, userExcludes: userExcludes}, nil
}

// setDarwinStateLocked запоминает поднятый туннель (stateMu уже захвачен).
func (w *WG) setDarwinStateLocked(h *tunHelper, tunnels []string, logf wgLogFunc) uint64 {
	w.setRuleStatus("Применяется")
	w.darwinSession++
	session := w.darwinSession
	w.helperConn = h.conn
	w.tunName = h.name
	w.activeUserExcludes = h.userExcludes
	w.refreshStop = make(chan struct{})
	go excludeRefreshLoop(w.refreshStop, func() { w.refreshExcludesDarwinSession(logf, session) })
	w.activeRoutesMu.Lock()
	w.activeRoutes = tunnels
	w.activeRoutesMu.Unlock()
	return session
}

func (w *WG) helperAlive() bool {
	if !w.stateMu.TryLock() {
		return true
	}
	defer w.stateMu.Unlock()
	if w.helperConn == nil {
		return true
	}
	raw, err := w.helperConn.SyscallConn()
	if err != nil {
		return false
	}
	alive := true
	err = raw.Control(func(fd uintptr) {
		var b [1]byte
		n, _, err := unix.Recvfrom(int(fd), b[:], unix.MSG_PEEK|unix.MSG_DONTWAIT)
		alive = n > 0 || errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR)
	})
	return err == nil && alive
}
func (w *WG) teardownDarwin() error {
	var cleanupErr error
	w.stateMu.Lock()
	defer w.stateMu.Unlock()

	if w.cancelLookup != nil {
		w.cancelLookup()
		w.cancelLookup = nil
	}
	if w.refreshStop != nil {
		close(w.refreshStop)
		w.refreshStop = nil
	}
	w.activeUserExcludes = nil
	w.setRuleStatus("")

	// Останавливаем движок и закрываем fd — utun и его маршруты исчезают сами
	if w.activeXray != nil {
		_ = w.activeXray.Close()
		w.activeXray = nil
	}
	if w.activeTunFile != nil {
		_ = w.activeTunFile.Close()
		w.activeTunFile = nil
	}
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
		cleanupErr = helperCall(w.helperConn, "down", nil, nil)
		w.helperConn.Close()
		w.helperConn = nil
		w.tunName = ""
	}

	w.activeRoutesMu.Lock()
	w.activeRoutes = nil
	w.activeRoutesMu.Unlock()
	return cleanupErr
}

// refreshExcludesDarwin досылает helper'у разницу между применённым и
// текущим списком исключений. Пароль не нужен: helper уже работает от root.
func (w *WG) refreshExcludesDarwinSession(logf wgLogFunc, session uint64) {
	w.stateMu.Lock()
	if w.helperConn == nil || w.darwinSession != session {
		w.stateMu.Unlock()
		return
	}
	w.setRuleStatus("Применяется")
	w.darwinRefresh++
	refresh := w.darwinRefresh
	ctx, cancel := w.beginLookup()
	defer cancel()
	w.stateMu.Unlock()

	next, unresolved := allExcludes(ctx, logf, false)

	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	if w.helperConn == nil || w.darwinSession != session || w.darwinRefresh != refresh {
		return
	}

	err := helperCall(w.helperConn, "rules", next, logf)
	if err != nil {
		w.setRuleStatus("Ошибка применения: " + err.Error())
		return
	}
	if len(unresolved) > 0 {
		w.setRuleStatus("Нет IPv4-сетей для " + strings.Join(unresolved, ", "))
		return
	}
	w.activeUserExcludes = next
	w.setRuleStatus("Действует")
}

// setTunnelRoutesDarwin снимает или возвращает маршруты туннеля через helper (без пароля).
func (w *WG) setTunnelRoutesDarwin(on bool) error {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	if w.helperConn == nil {
		return fmt.Errorf("туннель не поднят")
	}
	cmd := "tunoff"
	if on {
		cmd = "tunon"
	}
	return helperCall(w.helperConn, cmd, nil, nil)
}

// logRoutesDarwin просит helper сохранить маршруты текущей сессии.
func (w *WG) logRoutesDarwin() {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	if w.helperConn != nil {
		_ = helperCall(w.helperConn, "diag", nil, func(msg string) { log.Print(msg) })
	}
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
	defer func() {
		if pendingFD >= 0 {
			_ = syscall.Close(pendingFD)
		}
	}()
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
				file := os.NewFile(uintptr(pendingFD), msg.Name)
				pendingFD = -1
				return file, msg.Name, nil
			}
		}

		n, oobn, flags, _, readErr := hconn.ReadMsgUnix(buf, oob)
		control := oob[:oobn]
		for len(control) >= unix.CmsgLen(0) {
			header, data, remaining, err := unix.ParseOneSocketControlMessage(control)
			if err != nil {
				return nil, "", fmt.Errorf("чтение дескриптора от helper: %w", err)
			}
			control = remaining
			if header.Level != unix.SOL_SOCKET || header.Type != unix.SCM_RIGHTS {
				continue
			}
			fds, err := unix.ParseUnixRights(&unix.SocketControlMessage{Header: header, Data: data})
			if err != nil {
				return nil, "", fmt.Errorf("чтение дескриптора от helper: %w", err)
			}
			for _, fd := range fds {
				if pendingFD >= 0 {
					_ = syscall.Close(fd)
					continue
				}
				pendingFD = fd
				syscall.CloseOnExec(fd)
			}
		}
		if readErr != nil {
			return nil, "", fmt.Errorf("чтение от helper: %w", readErr)
		}
		if flags&unix.MSG_CTRUNC != 0 {
			return nil, "", fmt.Errorf("неполное сообщение с дескриптором от helper")
		}
		acc = append(acc, buf[:n]...)
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// physicalGatewayDarwin — шлюз default route, только если он на физическом
// интерфейсе (en*, bridge*); если default сейчас ведёт в чужой туннель — "".
func physicalGatewayDarwin() (string, string) {
	_, _, gw, iface := routeGetDarwin("default")
	if net.ParseIP(gw) == nil || !(strings.HasPrefix(iface, "en") || strings.HasPrefix(iface, "bridge")) {
		return "", ""
	}
	return gw, iface
}

func cmdOutputDarwin(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %v: %w — %s", name, args, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
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
	exclFile := fs.String("exclude-file", "", "файл с исключениями пользователя (CIDR построчно)")
	diagnosticIPs := fs.String("diagnostic-ips", "", "IP серверов для диагностики маршрутов")
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

	gw, iface := physicalGatewayDarwin()
	send(wgHelperMsg{Log: fmt.Sprintf("Default gateway: %s", gw)}, -1)

	snapshotRoutes := func(stage string) {
		_, _, defaultGW, defaultIf := routeGetDarwin("default")
		send(wgHelperMsg{Log: fmt.Sprintf("[МАРШРУТЫ] %s: default gateway=%q interface=%q, DNS=%v", stage, defaultGW, defaultIf, localDNSServers())}, -1)
		for _, ip := range splitCSV(*diagnosticIPs) {
			if net.ParseIP(ip) == nil {
				continue
			}
			dst, mask, gateway, iface := routeGetDarwin(ip)
			if iface == "" {
				send(wgHelperMsg{Log: fmt.Sprintf("[МАРШРУТЫ] %s: %s — не удалось определить маршрут", stage, ip)}, -1)
				continue
			}
			var sourceIP string
			if strings.HasPrefix(iface, "en") {
				out, _ := cmdOutputDarwin("ipconfig", "getifaddr", iface)
				sourceIP = strings.TrimSpace(out)
			}
			send(wgHelperMsg{Log: fmt.Sprintf("[МАРШРУТЫ] %s: %s destination=%q mask=%q gateway=%q interface=%q interface_ip=%q", stage, ip, dst, mask, gateway, iface, sourceIP)}, -1)
		}
	}
	snapshotRoutes("до установки маршрутов")

	// Exclude-маршруты через физический gateway — ДО туннельных,
	// чтобы трафик к turn/VK не успел уйти в туннель
	rt, err := openRtSock()
	if err != nil {
		fail("%v", err)
	}
	defer rt.Close()
	g := &helperGuard{tunName: name, foreign: map[string]int{}, rt: rt, gw: gw, iface: iface, ours: map[string]rtEntry{}}
	builtin := map[string]bool{}
	defer g.cleanup()
	if rows, err := g.readTable(); err == nil {
		g.learnForeign(rows)
	}
	for _, c := range splitCSV(*excludes) {
		if err := g.apply("add", c); err != nil {
			send(wgHelperMsg{Error: fmt.Sprintf("%s: %v", c, err)}, -1)
			return
		}
		builtin[c] = true
	}
	user := map[string]bool{}
	reconcile := func(values []string) error {
		want := map[string]bool{}
		var failures []error
		g.mu.Lock()
		defer g.mu.Unlock()
		var err error
		g.snapshot, err = g.readTable()
		if err != nil {
			return err
		}
		for _, c := range values {
			want[c] = true
			if builtin[c] {
				continue
			}
			if err := g.applyLocked("add", c); err != nil {
				failures = append(failures, fmt.Errorf("%s: %w", c, err))
			} else {
				user[c] = true
			}
		}
		for c := range user {
			if !want[c] {
				if err := g.applyLocked("del", c); err != nil {
					failures = append(failures, fmt.Errorf("%s: %w", c, err))
				} else {
					delete(user, c)
				}
			}
		}
		return errors.Join(failures...)
	}
	if *exclFile != "" {
		data, err := os.ReadFile(*exclFile)
		if err != nil {
			send(wgHelperMsg{Error: err.Error()}, -1)
			return
		}
		if err = reconcile(strings.Fields(string(data))); err != nil {
			send(wgHelperMsg{Log: err.Error()}, -1)
		}
	}
	active := map[string]bool{}
	toggleTunnel := func(on bool) error {
		var failures []error
		for _, cidr := range splitCSV(*tunnels) {
			if active[cidr] == on {
				continue
			}
			op := "delete"
			if on {
				op = "add"
			}
			if err := runCmdDarwin("route", "-q", "-n", op, "-net", cidr, "-interface", name); err != nil {
				failures = append(failures, err)
			} else {
				active[cidr] = on
			}
		}
		return errors.Join(failures...)
	}
	if err := toggleTunnel(true); err != nil {
		send(wgHelperMsg{Error: err.Error()}, -1)
		return
	}

	snapshotRoutes("после установки маршрутов")

	// Передаём fd интерфейса приложению
	send(wgHelperMsg{Name: name}, int(dev.File().Fd()))

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { defer close(done); g.watch(stop) }()

	// Команды приложения: "add CIDR" / "del CIDR" — правка исключений на лету,
	// "dns A,B" — DNS туннеля для основной сети (режим подписки),
	// "down" или обрыв соединения (краш приложения) → уборка
	var dns dnsOverride
	var requestedDNS []string
	var downID string
	defer dns.restore()
	reader := bufio.NewReader(hconn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil || strings.TrimSpace(line) == "down" {
			break
		}
		var req helperRequest
		if json.Unmarshal([]byte(line), &req) != nil {
			break
		}
		if req.Op == "down" {
			downID = req.ID
			break
		}
		switch req.Op {
		case "dns":
			requestedDNS = req.Values
			err = dns.set(req.Values)
		case "rules":
			err = reconcile(req.Values)
		case "diag":
			snapshotRoutes("проверка маршрутов")
		case "tunoff":
			err = errors.Join(toggleTunnel(false), dns.restore())
		case "tunon":
			err = toggleTunnel(true)
			if err == nil && len(requestedDNS) > 0 {
				err = dns.set(requestedDNS)
			}
		default:
			err = fmt.Errorf("неизвестная команда")
		}
		reply := wgHelperMsg{ID: req.ID}
		if err != nil {
			reply.Error = err.Error()
		}
		send(reply, -1)

	}
	close(stop)
	<-done
	cleanupErr := errors.Join(g.cleanup(), dns.restore())
	if downID != "" {
		reply := wgHelperMsg{ID: downID}
		if cleanupErr != nil {
			reply.Error = cleanupErr.Error()
		}
		send(reply, -1)
	}
	runtime.KeepAlive(dev)
}

// helperGuard (root) держит исключения на месте: после смены сети шлюз
// другой, а Cisco и прочие при переподключении переписывают таблицу.
type helperGuard struct {
	mu sync.Mutex
	rt interface {
		Add(string, net.IP) error
		Delete(string, net.IP) error
		Change(string, net.IP) error
	}
	tunName    string
	foreign    map[string]int
	gw, pendGW string
	ours       map[string]rtEntry
	table      func() (map[string]rtEntry, error)
	snapshot   map[string]rtEntry
	iface      string
}

func (g *helperGuard) readTable() (map[string]rtEntry, error) {
	if g.table != nil {
		return g.table()
	}
	return gatewayRoutes()
}
func (g *helperGuard) apply(op, cidr string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.applyLocked(op, cidr)
}
func (g *helperGuard) applyLocked(op, cidr string) error {
	c, ok := validExcludeNet(cidr)
	if !ok {
		return fmt.Errorf("недопустимый префикс %s", cidr)
	}
	if op == "del" {
		if _, ours := g.ours[c]; !ours {
			return nil
		}
		if g.snapshot == nil {
			return fmt.Errorf("таблица маршрутов недоступна")
		}
		return g.remove(c, g.snapshot)
	}
	gw := net.ParseIP(g.gw)
	if gw == nil {
		return fmt.Errorf("нет физического шлюза")
	}
	if e, ok := g.snapshot[c]; ok {
		if e.gw == g.gw && e.iface == g.iface {
			return nil
		}
		return fmt.Errorf("маршрут занят: %s %s", e.gw, e.iface)
	}
	err := g.rt.Add(c, gw)
	if errors.Is(err, syscall.EEXIST) {
		if g.snapshot == nil {
			g.snapshot, err = g.readTable()
			if err != nil {
				return err
			}
		}
		existing := g.snapshot[c]
		if existing.gw == g.gw && existing.iface == g.iface {
			return nil
		}
		return fmt.Errorf("существующий маршрут %s через %s %s", c, existing.gw, existing.iface)
	}
	if err == nil {
		g.ours[c] = rtEntry{gw: g.gw, iface: g.iface}
		if g.snapshot != nil {
			g.snapshot[c] = g.ours[c]
		}
	}
	return err
}
func (g *helperGuard) remove(c string, rows map[string]rtEntry) error {
	if e := g.ours[c]; rows[c] == e {
		if err := g.rt.Delete(c, net.ParseIP(e.gw)); err != nil {
			return err
		}
	}
	delete(g.ours, c)
	return nil
}

func (g *helperGuard) watch(stop chan struct{}) {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			gw, iface := physicalGatewayDarwin()
			if gw == "" {
				continue // default сейчас не на физическом интерфейсе (другой VPN перестраивается)
			}
			// Новый шлюз принимаем только со второго замера подряд: во время
			// переподключения Cisco таблица на секунды бывает странной, и
			// переписывать тысячи маршрутов по ней нельзя
			g.mu.Lock()
			if gw != g.gw && gw != g.pendGW {
				g.pendGW = gw
				g.mu.Unlock()
				continue
			}
			g.pendGW = ""
			g.mu.Unlock()

			g.check(gw, iface)
		}
	}
}

// Remember physical host routes while their owner is present. A missing
// route is repaired only after two snapshots select our own tunnel.
func (g *helperGuard) learnForeign(rows map[string]rtEntry) {
	for prefix, e := range rows {
		if strings.HasSuffix(prefix, "/32") && e.gw == g.gw && e.iface == g.iface {
			if _, own := g.ours[prefix]; !own {
				g.foreign[prefix] = 0
			}
		}
	}
}
func snapshotInterface(rows map[string]rtEntry, ip netip.Addr) string {
	for bits := 32; bits >= 0; bits-- {
		if e, ok := rows[netip.PrefixFrom(ip, bits).Masked().String()]; ok {
			return e.iface
		}
	}
	return ""
}

// check возвращает на место наши исключения и чужие host-маршруты,
// если их снесли или они ведут на старый шлюз. Один снимок таблицы на всё.
func (g *helperGuard) check(gw, iface string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	rows, err := g.readTable()
	if err != nil {
		return
	}
	g.gw, g.iface, g.snapshot = gw, iface, rows
	for c, old := range g.ours {
		current, exists := rows[c]
		if exists && current != old {
			delete(g.ours, c)
			continue
		}
		if !exists {
			err = g.rt.Add(c, net.ParseIP(gw))
		} else if old.gw != gw {
			err = g.rt.Change(c, net.ParseIP(gw))
		} else {
			continue
		}
		if err == nil {
			g.ours[c] = rtEntry{gw: gw, iface: iface}
		}
	}
	g.learnForeign(rows)
	for prefix, missed := range g.foreign {
		if _, exists := rows[prefix]; exists {
			g.foreign[prefix] = 0
			continue
		}
		ip := netip.MustParsePrefix(prefix).Addr()
		if snapshotInterface(rows, ip) != g.tunName {
			g.foreign[prefix] = 0
			continue
		}
		g.foreign[prefix] = missed + 1
		if missed >= 1 {
			if err := g.rt.Add(prefix, net.ParseIP(gw)); err == nil {
				g.ours[prefix] = rtEntry{gw: gw, iface: iface}
				g.foreign[prefix] = 0
			}
		}
	}

}
func (g *helperGuard) cleanup() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	rows, err := g.readTable()
	if err != nil {
		return err
	}
	var failures []error
	for c := range g.ours {
		if err := g.remove(c, rows); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
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
