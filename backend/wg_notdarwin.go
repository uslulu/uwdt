//go:build !darwin

package backend

import "fmt"

func (w *WG) applyDarwin(conf string, turnIPs []string, logf wgLogFunc) error {
	return fmt.Errorf("darwin not supported on this platform")
}

func (w *WG) teardownDarwin() error { return nil }

func (w *WG) refreshExcludesDarwinSession(wgLogFunc, uint64) {}

func (w *WG) setTunnelRoutesDarwin(_ bool) error { return nil }

func (w *WG) applyXrayDarwin(link string, serverIPs []string, logf wgLogFunc) error {
	return fmt.Errorf("darwin not supported on this platform")
}

func (w *WG) logRoutesDarwin() {}

func (w *WG) helperAlive() bool { return true }
