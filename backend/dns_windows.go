package backend

// DNS туннеля на Windows. Без этого Windows спрашивает DNS провайдера (или
// роутер, который его пересылает), а тот подменяет ответы для заблокированных
// доменов адресом-заглушкой: Discord не открывается, у claude.ai — чужой
// сертификат. Кроме DNS на интерфейсе туннеля нужно правило NRPT «.»: иначе
// «умный» резолвер Windows спрашивает все интерфейсы разом и берёт самый быстрый
// ответ — от провайдера.

import (
	"net/netip"
	"strings"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

const nrptComment = "UWDT"

func powershell(script string) error {
	return runCmdWindows("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
}

func setTunnelDNSWindows(dev tun.Device, servers []string, logf wgLogFunc) {
	nt, ok := dev.(*tun.NativeTun)
	if !ok {
		return
	}
	luid := winipcfg.LUID(nt.LUID())

	var addrs []netip.Addr
	quoted := make([]string, 0, len(servers))
	for _, s := range servers {
		if a, err := netip.ParseAddr(s); err == nil {
			addrs = append(addrs, a)
			quoted = append(quoted, "'"+s+"'")
		}
	}
	if err := luid.SetDNS(windows.AF_INET, addrs, nil); err != nil {
		logf("DNS на интерфейсе туннеля: " + err.Error())
	}
	// Сервер может прислать DNS из встроенных исключений (1.1.1.1 ∈ 1.1.1.0/24):
	// тогда запросы шли бы мимо туннеля, а провайдер подменял бы ответы.
	// Маршрут /32 в туннель точнее исключения и перебивает его; уходит вместе с адаптером
	for _, a := range addrs {
		if err := luid.AddRoute(netip.PrefixFrom(a, 32), netip.IPv4Unspecified(), 0); err != nil &&
			err != windows.ERROR_OBJECT_ALREADY_EXISTS {
			logf("Маршрут DNS " + a.String() + " в туннель: " + err.Error())
		}
	}

	clearNRPT()
	if err := powershell("Add-DnsClientNrptRule -Namespace '.' -NameServers " + strings.Join(quoted, ",") +
		" -Comment '" + nrptComment + "'"); err != nil {
		logf("Правило NRPT для DNS: " + err.Error())
	}

	// IPv6 туннель не везёт: без этих маршрутов сайты с AAAA-записью шли бы
	// мимо туннеля. Пакеты сюда WireGuard молча отбрасывает — браузер уходит на IPv4.
	for _, p := range []string{"::/1", "8000::/1"} {
		if err := luid.AddRoute(netip.MustParsePrefix(p), netip.IPv6Unspecified(), 0); err != nil &&
			err != windows.ERROR_OBJECT_ALREADY_EXISTS {
			logf("Блокировка IPv6 мимо туннеля (" + p + "): " + err.Error())
		}
	}

	_ = runCmdWindows("ipconfig", "/flushdns") // подменённые ответы из кэша
	logf("DNS через туннель: " + strings.Join(servers, ", "))
}

func clearNRPT() {
	_ = powershell("Get-DnsClientNrptRule | Where-Object Comment -eq '" + nrptComment + "' | Remove-DnsClientNrptRule -Force")
}

// clearTunnelDNSWindows убирает правило NRPT: без туннеля DNS снова системный.
// Вызывается и при запуске — на случай, если прошлый сеанс оборвался аварийно.
func clearTunnelDNSWindows() {
	clearNRPT()
	_ = runCmdWindows("ipconfig", "/flushdns")
}
