//go:build !windows

package backend

import "golang.zx2c4.com/wireguard/tun"

func setTunnelDNSWindows(tun.Device, []string, wgLogFunc) {}

func clearTunnelDNSWindows() {}
