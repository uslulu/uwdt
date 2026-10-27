//go:build !windows

package backend

import "fmt"

func (w *WG) applyXrayWindows(link string, serverIPs []string, logf wgLogFunc) error {
	return fmt.Errorf("windows not supported on this platform")
}
