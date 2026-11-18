package backend

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	wails "github.com/wailsapp/wails/v2/pkg/runtime"
)

// SaveLogs сохраняет полный лог последних сессий (со всеми служебными строками,
// которых нет на экране) в файл, выбранный пользователем. Пароли и ключи в лог
// не пишутся. Возвращает путь или "" если пользователь отменил.
func (a *App) SaveLogs() (string, error) {
	dir := filepath.Join(configDir(), "logs")
	files, _ := filepath.Glob(filepath.Join(dir, "*.log"))
	if len(files) == 0 {
		return "", fmt.Errorf("логов пока нет")
	}
	sort.Strings(files) // имена — дата и время, по возрастанию
	if len(files) > 3 {
		files = files[len(files)-3:] // три последние сессии
	}

	path, err := wails.SaveFileDialog(a.ctx, wails.SaveDialogOptions{
		Title:           "Сохранить лог UWDT",
		DefaultFilename: "uwdt-log-" + time.Now().Format("2006-01-02_15-04") + ".txt",
		Filters:         []wails.FileFilter{{DisplayName: "Текст", Pattern: "*.txt"}},
	})
	if err != nil || path == "" {
		return "", err
	}

	if err := a.bridge.flushLog(); err != nil {
		return "", err
	}
	out, err := os.CreateTemp(filepath.Dir(path), ".uwdt-log-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(out.Name())
	defer out.Close()
	if _, err = fmt.Fprintf(out, "UWDT %s · %s/%s\n", Version, runtime.GOOS, runtime.GOARCH); err != nil {
		return "", err
	}
	settings := a.store.LoadSettings()
	if _, err = fmt.Fprintf(out, "Обфускация: %s · TCP: %v · Россия напрямую: %v · исключений: %d\n", settings.ObfsMode, settings.TurnTCP, settings.RussiaDirect, len(settings.Excludes)); err != nil {
		return "", err
	}
	for _, path := range files {
		for _, part := range []string{path + ".previous", path} {
			file, err := os.Open(part)
			if errors.Is(err, os.ErrNotExist) && part != path {
				continue
			}
			if err != nil {
				return "", err
			}
			_, err = fmt.Fprintf(out, "\n===== %s =====\n", filepath.Base(part))
			if err == nil {
				_, err = io.Copy(out, file)
			}
			file.Close()
			if err != nil {
				return "", err
			}
		}
	}
	if err = out.Close(); err != nil {
		return "", err
	}
	if err = os.Rename(out.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}
