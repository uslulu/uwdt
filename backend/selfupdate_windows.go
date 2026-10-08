package backend

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strings"
)

// installUpdate кладёт новый exe на место текущего. Работающий exe Windows
// удалить не даёт, но переименовать — даёт: старый уходит в .old и убирается
// при следующем запуске.
func installUpdate(zipPath string) (func() error, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("распаковка: %w", err)
	}
	defer zr.Close()
	var entry *zip.File
	for _, f := range zr.File {
		if strings.EqualFold(path.Ext(f.Name), ".exe") && !strings.Contains(f.Name, "/") {
			entry = f
			break
		}
	}
	if entry == nil {
		return nil, fmt.Errorf("в архиве нет exe")
	}

	newPath := exe + ".new"
	src, err := entry.Open()
	if err != nil {
		return nil, err
	}
	dst, err := os.Create(newPath)
	if err != nil {
		src.Close()
		return nil, fmt.Errorf("нет прав записать рядом с %s: %w", exe, err)
	}
	_, err = io.Copy(dst, io.LimitReader(src, maxUpdateSize))
	src.Close()
	dst.Close()
	if err != nil {
		os.Remove(newPath)
		return nil, err
	}

	old := exe + ".old"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		os.Remove(newPath)
		return nil, fmt.Errorf("замена exe: %w", err)
	}
	if err := os.Rename(newPath, exe); err != nil {
		os.Rename(old, exe) // откат
		return nil, fmt.Errorf("замена exe: %w", err)
	}

	return func() error {
		// Пауза, чтобы старый процесс успел выйти и отпустить single-instance lock
		cmd := exec.Command("cmd", "/C", `ping -n 3 127.0.0.1 >nul & start "" "`+exe+`"`)
		hideWindow(cmd)
		return cmd.Start()
	}, nil
}

func cleanupOldUpdate() {
	if exe, err := os.Executable(); err == nil {
		os.Remove(exe + ".old")
	}
}
