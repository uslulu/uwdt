package backend

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// currentBundle — путь к .app, из которого запущены.
func currentBundle() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	idx := strings.Index(exe, ".app/Contents/MacOS/")
	if idx < 0 {
		return "", fmt.Errorf("приложение запущено не из .app — обновите вручную")
	}
	return exe[:idx+len(".app")], nil
}

// installUpdate распаковывает новую сборку и подменяет текущий .app.
func installUpdate(zipPath string) (func() error, error) {
	app, err := currentBundle()
	if err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "uwdt-update-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if out, err := exec.Command("ditto", "-x", "-k", zipPath, tmp).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("распаковка: %v %s", err, strings.TrimSpace(string(out)))
	}
	matches, _ := filepath.Glob(filepath.Join(tmp, "*.app"))
	if len(matches) != 1 {
		return nil, fmt.Errorf("в архиве нет приложения")
	}
	newApp := matches[0]
	if out, err := exec.Command("codesign", "-v", newApp).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("подпись новой сборки не сходится: %s", strings.TrimSpace(string(out)))
	}

	old := app + ".old-update"
	os.RemoveAll(old)
	if err := os.Rename(app, old); err != nil {
		return nil, fmt.Errorf("нет прав заменить %s: %w", app, err)
	}
	if out, err := exec.Command("ditto", newApp, app).CombinedOutput(); err != nil {
		os.RemoveAll(app)
		os.Rename(old, app) // откат
		return nil, fmt.Errorf("установка: %v %s", err, strings.TrimSpace(string(out)))
	}
	os.RemoveAll(old)

	return func() error {
		return exec.Command("/bin/sh", "-c", "sleep 1; /usr/bin/open -n "+shellQuote(app)).Start()
	}, nil
}

// cleanupOldUpdate — остаток прерванного обновления.
func cleanupOldUpdate() {
	if app, err := currentBundle(); err == nil {
		os.RemoveAll(app + ".old-update")
	}
}
