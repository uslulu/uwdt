package backend

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
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

	var b strings.Builder
	fmt.Fprintf(&b, "UWDT %s · %s/%s · %s\n", Version, runtime.GOOS, runtime.GOARCH, time.Now().Format("2006-01-02 15:04:05"))
	s := a.store.LoadSettings()
	fmt.Fprintf(&b, "Обфускация: %s · TCP: %v · Россия напрямую: %v · исключений: %d\n", s.ObfsMode, s.TurnTCP, s.RussiaDirect, len(s.Excludes))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "\n===== %s =====\n", filepath.Base(f))
		b.Write(data)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
