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
func physLUID(gw netip.Addr) (winipcfg.LUID, error) {
	rows, err := winipcfg.GetIPForwardTable2(winipcfg.AddressFamily(windows.AF_INET))
	if err != nil {
		return 0, err
	}
	for i := range rows {
		r := &rows[i]
		if r.DestinationPrefix.Prefix().Bits() == 0 && r.NextHop.Addr() == gw {
			return r.InterfaceLUID, nil
		}
	}
	return 0, fmt.Errorf("интерфейс шлюза %s не найден", gw)
}

func bulkRoutes(add bool, gw string, cidrs []string) (int, error) {
	g, err := netip.ParseAddr(gw)
	if err != nil {
		return 0, err
	}
	if !add {
		return deleteRoutes(g, cidrs)
	}
	luid, err := physLUID(g)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			continue
		}
		if add {
			err = luid.AddRoute(p.Masked(), g, 5)
			if errors.Is(err, windows.ERROR_OBJECT_ALREADY_EXISTS) {
				err = nil
			}
		} else {
			err = luid.DeleteRoute(p.Masked(), g)
		}
		if err == nil {
			n++
		}
	}
	return n, nil
}

// deleteRoutes снимает маршруты через шлюз gw по таблице маршрутов: интерфейс
// искать не нужно (после смены сети его default route может уже не быть), и
// не нужен route.exe на каждую из тысяч сетей — это минуты «Отключения…».
func deleteRoutes(gw netip.Addr, cidrs []string) (int, error) {
	want := make(map[netip.Prefix]bool, len(cidrs))
	for _, c := range cidrs {
		if p, err := netip.ParsePrefix(c); err == nil {
			want[p.Masked()] = true
		}
	}
	rows, err := winipcfg.GetIPForwardTable2(winipcfg.AddressFamily(windows.AF_INET))
	if err != nil {
		return 0, err
	}
	n := 0
	for i := range rows {
		r := &rows[i]
		if r.NextHop.Addr() == gw && want[r.DestinationPrefix.Prefix().Masked()] {
			if r.Delete() == nil {
				n++
			}
		}
	}
	return n, nil
}
