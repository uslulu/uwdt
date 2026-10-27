package backend

// DNS через туннель на macOS в режиме подписки. Иначе адреса сайтов мак
// спрашивает у роутера, то есть у провайдера, а тот подменяет ответы для
// заблокированных доменов. Пока туннель поднят, root-помощник ставит основной
// сети DNS туннеля (как «networksetup -setdnsservers») и возвращает прежний
// при отключении — и при падении приложения, когда рвётся связь с помощником.

import (
	"bufio"
	"fmt"
	"net"
	"regexp"
	"strings"
)

// dnsOverride — подменённый DNS сетевой службы (helper, root).
type dnsOverride struct {
	service string
	saved   []string // прежние серверы; пусто — DNS выдавал DHCP
	active  bool
}

var serviceDeviceRe = regexp.MustCompile(`^\(Hardware Port: .*, Device: (\S+)\)$`)

// serviceForDevice — имя сетевой службы («Wi-Fi») по устройству («en0»).
func serviceForDevice(dev string) (string, error) {
	out, err := cmdOutputDarwin("networksetup", "-listnetworkserviceorder")
	if err != nil {
		return "", err
	}
	return parseServiceForDevice(out, dev)
}

func parseServiceForDevice(out, dev string) (string, error) {
	var name string
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "(") && !strings.HasPrefix(line, "(Hardware Port:") {
			if i := strings.Index(line, ") "); i > 0 {
				name = strings.TrimPrefix(line[i+2:], "*")
			}
			continue
		}
		if m := serviceDeviceRe.FindStringSubmatch(line); m != nil && m[1] == dev && name != "" {
			return name, nil
		}
	}
	return "", fmt.Errorf("нет сетевой службы для %s", dev)
}

func (d *dnsOverride) set(servers []string) error {
	if d.active {
		return nil
	}
	dev := defaultIfaceDarwin()
	if !strings.HasPrefix(dev, "en") {
		return fmt.Errorf("основная сеть %q — не физическая, DNS не трогаю", dev)
	}
	svc, err := serviceForDevice(dev)
	if err != nil {
		return err
	}
	out, err := cmdOutputDarwin("networksetup", "-getdnsservers", svc)
	if err != nil {
		return err
	}
	var saved []string
	for _, f := range strings.Fields(out) {
		if net.ParseIP(f) != nil {
			saved = append(saved, f)
		}
	}
	if err := runCmdDarwin("networksetup", append([]string{"-setdnsservers", svc}, servers...)...); err != nil {
		return err
	}
	d.service, d.saved, d.active = svc, saved, true
	flushDNSCacheDarwin()
	return nil
}

func (d *dnsOverride) restore() {
	if !d.active {
		return
	}
	args := []string{"-setdnsservers", d.service}
	if len(d.saved) == 0 {
		args = append(args, "empty")
	} else {
		args = append(args, d.saved...)
	}
	_ = runCmdDarwin("networksetup", args...)
	d.active = false
	flushDNSCacheDarwin()
}

func flushDNSCacheDarwin() {
	_ = runCmdDarwin("dscacheutil", "-flushcache")
	_ = runCmdDarwin("killall", "-HUP", "mDNSResponder")
}

// defaultIfaceDarwin — интерфейс маршрута по умолчанию (en0, utun3…).
func defaultIfaceDarwin() string {
	out, err := cmdOutputDarwin("route", "-n", "get", "default")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "interface:"); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
