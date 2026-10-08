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
