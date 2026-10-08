package backend

// Маршруты через routing socket (PF_ROUTE) вместо запуска `route` на каждый:
// исключение «Россия напрямую» — это ~9 тысяч подсетей, по процессу на
// каждую выходило бы около минуты. Работает только от root (в helper'е).

import (
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"syscall"

	"golang.org/x/net/route"
)

type rtSock struct {
	mu  sync.Mutex
	fd  int
	seq int
}

func openRtSock() (*rtSock, error) {
	fd, err := syscall.Socket(syscall.AF_ROUTE, syscall.SOCK_RAW, syscall.AF_UNSPEC)
	if err != nil {
		return nil, fmt.Errorf("routing socket: %w", err)
	}
	// Нам не нужны уведомления о чужих изменениях — не копим их в буфере
	_ = syscall.Shutdown(fd, syscall.SHUT_RD)
	return &rtSock{fd: fd}, nil
}

func (s *rtSock) Close() { syscall.Close(s.fd) }

func (s *rtSock) send(typ int, dst *net.IPNet, gw net.IP) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	ones, _ := dst.Mask.Size()
	flags := syscall.RTF_UP | syscall.RTF_STATIC
	addrs := make([]route.Addr, syscall.RTAX_NETMASK+1)
	var ip4 [4]byte
	copy(ip4[:], dst.IP.To4())
	addrs[syscall.RTAX_DST] = &route.Inet4Addr{IP: ip4}
	if gw != nil {
		var g4 [4]byte
		copy(g4[:], gw.To4())
		addrs[syscall.RTAX_GATEWAY] = &route.Inet4Addr{IP: g4}
		flags |= syscall.RTF_GATEWAY
	}
	if ones == 32 {
		flags |= syscall.RTF_HOST
		addrs = addrs[:syscall.RTAX_GATEWAY+1]
	} else {
		var m4 [4]byte
		copy(m4[:], dst.Mask)
		addrs[syscall.RTAX_NETMASK] = &route.Inet4Addr{IP: m4}
	}
	msg := route.RouteMessage{
		Version: syscall.RTM_VERSION,
		Type:    typ,
		Flags:   flags,
		ID:      uintptr(os.Getpid()),
		Seq:     s.seq,
		Addrs:   addrs,
	}
	b, err := msg.Marshal()
	if err != nil {
		return err
	}
	_, err = syscall.Write(s.fd, b)
	return err
}

// Add ставит маршрут на сеть через шлюз; syscall.EEXIST — маршрут на эту сеть уже есть.
func (s *rtSock) Add(cidr string, gw net.IP) error {
	_, n, err := net.ParseCIDR(cidr)
	if err != nil {
		return err
	}
	return s.send(syscall.RTM_ADD, n, gw)
}

// Change переписывает шлюз существующего маршрута.
func (s *rtSock) Change(cidr string, gw net.IP) error {
	_, n, err := net.ParseCIDR(cidr)
	if err != nil {
		return err
	}
	return s.send(syscall.RTM_CHANGE, n, gw)
}

func (s *rtSock) Delete(cidr string) error {
	_, n, err := net.ParseCIDR(cidr)
	if err != nil {
		return err
	}
	err = s.send(syscall.RTM_DELETE, n, nil)
	if errors.Is(err, syscall.ESRCH) {
		return nil // и так нет
	}
	return err
}

type rtEntry struct {
	gw    string
	iface string
}

// gatewayRoutes — снимок таблицы: CIDR → шлюз и интерфейс для маршрутов через шлюз (IPv4).
func gatewayRoutes() (map[string]rtEntry, error) {
	rib, err := route.FetchRIB(syscall.AF_INET, route.RIBTypeRoute, 0)
	if err != nil {
		return nil, err
	}
	msgs, err := route.ParseRIB(route.RIBTypeRoute, rib)
	if err != nil {
		return nil, err
	}
	out := make(map[string]rtEntry, len(msgs))
	names := map[int]string{}
	for _, m := range msgs {
		rm, ok := m.(*route.RouteMessage)
		if !ok || rm.Flags&syscall.RTF_GATEWAY == 0 || len(rm.Addrs) <= syscall.RTAX_GATEWAY {
			continue
		}
		dst, ok1 := rm.Addrs[syscall.RTAX_DST].(*route.Inet4Addr)
		gw, ok2 := rm.Addrs[syscall.RTAX_GATEWAY].(*route.Inet4Addr)
		if !ok1 || !ok2 {
			continue
		}
		ones := 32
		if rm.Flags&syscall.RTF_HOST == 0 {
			ones = 0
			if len(rm.Addrs) > syscall.RTAX_NETMASK {
				if mk, ok := rm.Addrs[syscall.RTAX_NETMASK].(*route.Inet4Addr); ok {
					ones, _ = net.IPMask(mk.IP[:]).Size()
				}
			}
		}
		n := net.IPNet{IP: net.IP(dst.IP[:]).Mask(net.CIDRMask(ones, 32)), Mask: net.CIDRMask(ones, 32)}
		name, ok := names[rm.Index]
		if !ok {
			if ifc, err := net.InterfaceByIndex(rm.Index); err == nil {
				name = ifc.Name
			}
			names[rm.Index] = name
		}
		out[n.String()] = rtEntry{gw: net.IP(gw.IP[:]).String(), iface: name}
	}
	return out, nil
}
