//go:build !darwin

package backend

import "fmt"

func (w *WG) applyDarwin(conf string, turnIPs []string, logf wgLogFunc) error {
	return fmt.Errorf("darwin not supported on this platform")
}

func (w *WG) teardownDarwin() {}

// Заглушка для Linux/Windows
func cleanupStaleExcludeRoutesDarwin(_ wgLogFunc) {}

// На Linux/Windows исключения применяются при подключении
func (w *WG) refreshExcludesDarwin(logf wgLogFunc) {
	logf("Исключения сохранены — применятся при следующем подключении")
}
