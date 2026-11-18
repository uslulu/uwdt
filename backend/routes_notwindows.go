//go:build !windows

package backend

import "fmt"

func (w *WG) bulkRoutes(_ bool, _ string, _ []string) (int, error) {
	return 0, fmt.Errorf("только для Windows")
}

func (w *WG) releaseWindowsRoute(windowsRoute) error { return nil }
