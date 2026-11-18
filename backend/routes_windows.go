package backend

// Пачка маршрутов через IP Helper API (CreateIpForwardEntry2): «Россия
// напрямую» — ~9 тысяч сетей, `route add` на каждую заняла бы минуты.

import (
	"errors"
	"fmt"
	"net/netip"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// physLUID — интерфейс, через который идёт default route на физический шлюз.
func physLUID(gw netip.Addr, rows []winipcfg.MibIPforwardRow2) (winipcfg.LUID, error) {
	for i := range rows {
		r := &rows[i]
		if r.DestinationPrefix.Prefix().Bits() == 0 && r.NextHop.Addr() == gw {
			return r.InterfaceLUID, nil
		}
	}
	return 0, fmt.Errorf("интерфейс шлюза %s не найден", gw)
}

// Called with stateMu held. The ledger contains only successful creates.
func (w *WG) bulkRoutes(add bool, gw string, cidrs []string) (int, error) {
	g, err := netip.ParseAddr(gw)
	if err != nil {
		return 0, err
	}
	if w.ownedWindows == nil {
		w.ownedWindows = map[windowsRoute]bool{}
	}
	if len(cidrs) == 0 {
		return 0, nil
	}
	if !add {
		wanted := map[string]bool{}
		for _, s := range cidrs {
			wanted[s] = true
		}
		var failures []error
		count := 0
		for route := range w.ownedWindows {
			if route.gateway != gw || !wanted[route.prefix] {
				continue
			}
			if err := w.releaseWindowsRoute(route); err != nil {
				failures = append(failures, err)
			} else {
				count++
			}
		}
		return count, errors.Join(failures...)
	}
	rows, err := winipcfg.GetIPForwardTable2(winipcfg.AddressFamily(windows.AF_INET))
	if err != nil {
		return 0, err
	}
	luid, err := physLUID(g, rows)
	if err != nil {
		return 0, err
	}
	var failures []error
	// Index the RIB once for the entire batch. Preserve other VPN routes.
	present, foreign := map[netip.Prefix]bool{}, map[netip.Prefix]winipcfg.LUID{}
	for i := range rows {
		row := &rows[i]
		prefix := row.DestinationPrefix.Prefix().Masked()
		if row.InterfaceLUID == luid && row.NextHop.Addr() == g {
			present[prefix] = true
		} else {
			foreign[prefix] = row.InterfaceLUID
		}
	}
	n := 0
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		p = p.Masked()
		if other, exists := foreign[p]; exists {
			failures = append(failures, fmt.Errorf("%s: сохранён маршрут другого интерфейса или шлюза (LUID %d)", p, other))
			continue
		}
		if present[p] {
			continue
		}
		key := windowsRoute{prefix: p.String(), gateway: gw, iface: uint64(luid)}
		err = luid.AddRoute(p, g, 5)
		if errors.Is(err, windows.ERROR_OBJECT_ALREADY_EXISTS) {
			continue
		}
		if err == nil {
			w.ownedWindows[key] = true
		}

		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", c, err))
		} else {
			n++
		}
	}
	return n, errors.Join(failures...)
}

func (w *WG) releaseWindowsRoute(key windowsRoute) error {
	luid := winipcfg.LUID(key.iface)
	p := netip.MustParsePrefix(key.prefix)
	gw := netip.MustParseAddr(key.gateway)
	row, err := luid.Route(p, gw)
	if err == nil && row.Metric == 5 {
		err = row.Delete()
	}
	if err == nil || errors.Is(err, windows.ERROR_NOT_FOUND) {
		delete(w.ownedWindows, key)
		return nil
	}
	return fmt.Errorf("%s через %s: %w", p, gw, err)
}
