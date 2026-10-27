package backend

// Xray на Windows: адаптер Wintun создаёт сам Xray (приложение и так работает
// с правами администратора), а мы, как для WireGuard, ставим на него адрес,
// маршруты, DNS через туннель и исключения через физический шлюз.

import (
	"crypto/md5"
	"fmt"
	"net/netip"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// xrayTunPrefix — адрес нашего конца адаптера Xray (сам Xray его не использует)
const xrayTunPrefix = "10.255.255.1/30"

func (w *WG) applyXrayWindows(link string, serverIPs []string, logf wgLogFunc) error {
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	w.teardownWindowsLocked()

	if err := extractWintun(); err != nil {
		return fmt.Errorf("extract wintun.dll: %w", err)
	}
	gw := defaultGatewayWindows()
	if gw == "" {
		return fmt.Errorf("не найден шлюз физической сети")
	}
	logf("Default gateway: " + gw)

	// Исключения — до маршрутов туннеля: адрес сервера VLESS (иначе Xray
	// зациклится сам на себя), встроенные, пользовательские и «Россия»
	excludes := make([]string, 0, len(serverIPs)+len(vkExcludeCIDRs))
	for _, ip := range serverIPs {
		excludes = append(excludes, ip+"/32")
	}
	excludes = append(excludes, vkExcludeCIDRs...)
	user := currentAllExcludesFast(logf)
	all := append(excludes, user...)
	if _, err := bulkRoutes(true, gw, all); err != nil {
		return fmt.Errorf("исключения из туннеля: %w", err)
	}
	removeExcludes := func() { _, _ = bulkRoutes(false, gw, all) }

	// Своё имя адаптера на каждый запуск: если прошлый почему-то не закрылся,
	// Xray не подхватит его по имени (с занятой сессией запуск зависал)
	name := nextXrayAdapterName()
	logf("Запуск Xray…")
	x, err := startXray(link, 0, name, xrayMTU, logf)
	if err != nil {
		removeExcludes()
		return err
	}
	luid, err := xrayAdapterLUID(name)
	if err == nil {
		err = configureXrayAdapter(luid)
	}
	if err != nil {
		_ = x.Close()
		removeExcludes()
		return fmt.Errorf("адаптер Xray: %w", err)
	}
	setTunnelDNSLUID(luid, tunnelDNSDefault, logf)

	w.activeXray = x
	w.xrayMode = true
	w.physGW = gw
	w.activeExcludeRoutes = all
	w.activeUserExcludes = user
	w.refreshStop = make(chan struct{})
	go w.excludeRefreshLoop(w.refreshStop, logf)

	logf(fmt.Sprintf("Туннель %s (Xray) поднят", name))
	go w.RefreshExcludes(logf)
	return nil
}

var xrayAdapterSeq atomic.Uint32

func nextXrayAdapterName() string {
	return fmt.Sprintf("%s-%d", xrayTunName(), xrayAdapterSeq.Add(1))
}

// xrayAdapterLUID — LUID адаптера, который создал Xray: его GUID — md5 от имени
// (так делает Xray в proxy/tun/tun_windows.go).
func xrayAdapterLUID(name string) (winipcfg.LUID, error) {
	id := md5.Sum([]byte(name))
	guid := (*windows.GUID)(unsafe.Pointer(&id[0]))
	return winipcfg.LUIDFromGUID(guid)
}

// configureXrayAdapter: адрес, низкая метрика, MTU и маршруты 0/1 + 128/1 —
// они точнее физического default route и не требуют трогать его метрику.
func configureXrayAdapter(luid winipcfg.LUID) error {
	if err := luid.SetIPAddressesForFamily(windows.AF_INET, []netip.Prefix{netip.MustParsePrefix(xrayTunPrefix)}); err != nil {
		return fmt.Errorf("адрес: %w", err)
	}
	if row, err := luid.IPInterface(windows.AF_INET); err == nil {
		row.UseAutomaticMetric = false
		row.Metric = 1
		row.NLMTU = xrayMTU
		_ = row.Set()
	}
	for _, p := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
		if err := luid.AddRoute(netip.MustParsePrefix(p), netip.IPv4Unspecified(), 0); err != nil &&
			err != windows.ERROR_OBJECT_ALREADY_EXISTS {
			return fmt.Errorf("маршрут %s: %w", p, err)
		}
	}
	return nil
}
