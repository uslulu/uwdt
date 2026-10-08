package backend

// Автообновление: скачать сборку из релиза GitHub, сверить SHA-256 с тем, что
// GitHub отдаёт для файла, заменить себя и перезапуститься. Без совпадения
// суммы ничего не ставится.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	wails "github.com/wailsapp/wails/v2/pkg/runtime"
)

const maxUpdateSize = 200 << 20

type progressWriter struct {
	total, done int64
	last        int
	emit        func(int)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if p.total > 0 {
		if pct := int(p.done * 100 / p.total); pct != p.last {
			p.last = pct
			p.emit(pct)
		}
	}
	return len(b), nil
}

// downloadVerified качает файл во временный и проверяет SHA-256.
func downloadVerified(url, wantSHA string, emit func(int)) (string, error) {
	if len(wantSHA) != 64 {
		return "", fmt.Errorf("у файла обновления нет контрольной суммы — не ставлю")
	}
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("скачивание: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("скачивание: HTTP %d", resp.StatusCode)
	}
	f, err := os.CreateTemp("", "uwdt-update-*.zip")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	pw := &progressWriter{total: resp.ContentLength, emit: emit}
	_, err = io.Copy(io.MultiWriter(f, h, pw), io.LimitReader(resp.Body, maxUpdateSize))
	f.Close()
	if err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("скачивание: %w", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, wantSHA) {
		os.Remove(f.Name())
		return "", fmt.Errorf("контрольная сумма не совпала — файл повреждён или подменён")
	}
	return f.Name(), nil
}

// InstallUpdate ставит доступное обновление и перезапускает приложение.
func (a *App) InstallUpdate() error {
	info, err := CheckUpdate(Version)
	if err != nil {
		return err
	}
	if !info.Available || info.URL == "" {
		return fmt.Errorf("подходящей сборки в релизе нет")
	}
	emit := func(pct int) { a.onBridgeEvent("update_progress", pct) }
	zipPath, err := downloadVerified(info.URL, info.SHA256, emit)
	if err != nil {
		return err
	}
	defer os.Remove(zipPath)

	relaunch, err := installUpdate(zipPath)
	if err != nil {
		return err
	}
	// Туннель снимаем до перезапуска, чтобы маршруты убрал старый процесс
	if a.bridge != nil {
		a.bridge.Disconnect()
		time.Sleep(1500 * time.Millisecond)
	}
	if err := relaunch(); err != nil {
		return fmt.Errorf("обновление установлено, но перезапуск не удался — откройте UWDT вручную: %w", err)
	}
	wails.Quit(a.ctx)
	return nil
}
