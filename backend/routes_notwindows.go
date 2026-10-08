//go:build !windows

package backend

import "fmt"

func bulkRoutes(_ bool, _ string, _ []string) (int, error) {
	return 0, fmt.Errorf("только для Windows")
}
