package backend

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type AddressRoute struct {
	IP    string   `json:"ip"`
	Rules []string `json:"rules"`
	Route string   `json:"route"`
	Error string   `json:"error"`
}

func (a *App) InspectAddress(raw string) ([]AddressRoute, error) {
	host := strings.TrimSpace(raw)
	var ips []netip.Addr
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if ip, err := netip.ParseAddr(host); err == nil {
		ips = []netip.Addr{ip}
	} else {
		kind, value, err := NormalizeExclude(host)
		if err != nil {
			return nil, err
		}
		if kind != "domain" {
			return nil, fmt.Errorf("введите один адрес или домен")
		}
		host = value
		ips, err = net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
	}
	settings := a.store.LoadSettings()
	type match struct {
		label string
		nets  []string
	}
	var rules []match
	for _, e := range settings.Excludes {
		if !e.Enabled {
			continue
		}
		label := e.Value
		if e.Source != "" {
			label = e.Source + " · " + label
		}
		nets := resolveExcludes([]ExcludeEntry{e}, nil, true)
		if e.Value == host {
			for _, ip := range ips {
				if ip.Unmap().Is4() {
					nets = append(nets, ip.Unmap().String()+"/32")
				}
			}
		}
		rules = append(rules, match{label, nets})
	}
	if settings.RussiaDirect {
		nets, _ := russiaSnapshot()
		rules = append(rules, match{"Российские IPv4-сети", nets})
	}
	rows := make([]AddressRoute, 0, len(ips))
	for _, ip := range ips {
		ip = ip.Unmap()
		row := AddressRoute{IP: ip.String(), Rules: []string{}}
		for _, r := range rules {
			for _, c := range r.nets {
				p, err := netip.ParsePrefix(c)
				if err == nil && p.Contains(ip) {
					row.Rules = append(row.Rules, r.label)
					break
				}
			}
		}
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			family := "-inet"
			if ip.Is6() {
				family = "-inet6"
			}
			cmd = exec.CommandContext(ctx, "/sbin/route", "-n", "get", family, ip.String())
		case "linux":
			cmd = exec.CommandContext(ctx, "ip", "route", "get", ip.String())
		case "windows":
			cmd = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "Find-NetRoute -ErrorAction Stop -RemoteIPAddress '"+ip.WithZone("").String()+"' | Format-Table InterfaceAlias,DestinationPrefix,NextHop -AutoSize")
		default:
			row.Error = "На этой платформе проверка маршрута недоступна"
		}
		if cmd != nil {
			out, err := cmd.CombinedOutput()
			if err != nil {
				row.Error = fmt.Sprintf("Маршрут недоступен (%v) %s", err, strings.TrimSpace(string(out)))
			} else {
				row.Route = strings.TrimSpace(string(out))
				if runtime.GOOS == "darwin" {
					var details []string
					for _, line := range strings.Split(row.Route, "\n") {
						k, v, ok := strings.Cut(line, ":")
						if ok && (strings.TrimSpace(k) == "interface" || strings.TrimSpace(k) == "gateway") {
							value := strings.TrimSpace(v)
							if strings.TrimSpace(k) == "interface" {
								wg.stateMu.Lock()
								own := wg.tunName
								wg.stateMu.Unlock()
								switch {
								case value == own && own != "":
									value = "туннель UWDT " + value
								case strings.HasPrefix(value, "utun"):
									value = "другой туннель " + value
								case strings.HasPrefix(value, "en"):
									value = "сетевой интерфейс " + value
								}
							} else {
								value = "шлюз " + value
							}
							details = append(details, value)
						}
					}
					if len(details) > 0 {
						row.Route = strings.Join(details, " · ")
					}
				}
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}
