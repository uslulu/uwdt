//go:build !darwin && !windows

package backend

import "fmt"

func installUpdate(_ string) (func() error, error) {
	return nil, fmt.Errorf("автообновление на этой системе не поддерживается — скачайте релиз вручную")
}

func cleanupOldUpdate() {}
