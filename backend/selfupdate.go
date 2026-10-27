package backend

// Автообновление: скачать сборку из релиза GitHub, сверить SHA-256 с тем, что
// GitHub отдаёт для файла, заменить себя и перезапуститься. Без совпадения
// суммы ничего не ставится.

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	wails "github.com/wailsapp/wails/v2/pkg/runtime"
)

const maxUpdateSize = 200 << 20

// Через туннель VK TURN одно TCP-соединение тянет медленно: его пакеты идут
// по многим каналам и приходят не по порядку. Поэтому сборку качаем частями
// параллельно (Range): 14 МБ — ~35 с вместо 4–10 минут одним потоком.
const (
	updateParts     = 16
	updatePartTries = 3
)

// progress — общий счётчик скачанного для полосы прогресса.
type progress struct {
	mu          sync.Mutex
	total, done int64
	last        int
	emit        func(int)
}

func (p *progress) add(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done += int64(n)
	if p.total > 0 {
		if pct := int(p.done * 100 / p.total); pct != p.last {
			p.last = pct
			p.emit(pct)
		}
	}
}

// downloadVerified качает файл во временный и проверяет SHA-256.
func downloadVerified(url, wantSHA string, emit func(int)) (string, error) {
	if len(wantSHA) != 64 {
		return "", fmt.Errorf("у файла обновления нет контрольной суммы — не ставлю")
	}
	// Без HTTP/2: иначе все части идут одним мультиплексированным соединением,
	// и параллельности, ради которой их режем, не получается
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ForceAttemptHTTP2 = false
	tr.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	tr.TLSClientConfig = &tls.Config{NextProtos: []string{"http/1.1"}} // иначе сервер выберет h2 по ALPN
	tr.MaxIdleConnsPerHost = updateParts
	defer tr.CloseIdleConnections()
	client := &http.Client{Timeout: 10 * time.Minute, Transport: tr}
	f, err := os.CreateTemp("", "uwdt-update-*.zip")
	if err != nil {
		return "", err
	}
	name := f.Name()
	err = downloadParallel(client, url, f, emit)
	if err == nil {
		_, err = f.Seek(0, io.SeekStart)
	}
	h := sha256.New()
	if err == nil {
		_, err = io.Copy(h, f)
	}
	f.Close()
	if err != nil {
		os.Remove(name)
		return "", fmt.Errorf("скачивание: %w", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, wantSHA) {
		os.Remove(name)
		return "", fmt.Errorf("контрольная сумма не совпала — файл повреждён или подменён")
	}
	return name, nil
}

// downloadParallel: узнаёт размер (первым байтом с Range) и качает частями;
// если сервер не умеет Range — одним потоком.
func downloadParallel(client *http.Client, url string, f *os.File, emit func(int)) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", "bytes=0-0")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	size := rangeTotal(resp.Header.Get("Content-Range"))
	if resp.StatusCode != http.StatusPartialContent || size <= 0 || size > maxUpdateSize {
		return downloadSingle(client, url, f, emit)
	}
	// Ссылка релиза ведёт редиректом на CDN — дальше качаем с него напрямую
	target := resp.Request.URL.String()

	pr := &progress{total: size, emit: emit}
	part := (size + updateParts - 1) / updateParts
	errs := make(chan error, updateParts)
	var wg sync.WaitGroup
	for from := int64(0); from < size; from += part {
		to := min(from+part, size) - 1
		wg.Add(1)
		go func(from, to int64) {
			defer wg.Done()
			var err error
			for try := 0; try < updatePartTries; try++ {
				if err = downloadRange(client, target, f, from, to, pr); err == nil {
					return
				}
			}
			errs <- err
		}(from, to)
	}
	wg.Wait()
	close(errs)
	return <-errs // nil, если все части скачались
}

// downloadRange качает байты [from, to] и пишет их в файл на своё место.
func downloadRange(client *http.Client, url string, f *os.File, from, to int64, pr *progress) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", from, to))
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("HTTP %d на часть %d-%d", resp.StatusCode, from, to)
	}
	buf := make([]byte, 64<<10)
	off := from
	for off <= to {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if int64(n) > to-off+1 {
				n = int(to - off + 1)
			}
			if _, werr := f.WriteAt(buf[:n], off); werr != nil {
				return werr
			}
			off += int64(n)
			pr.add(n)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			pr.add(-int(off - from)) // часть начнётся заново — прогресс назад
			return rerr
		}
	}
	if off != to+1 {
		pr.add(-int(off - from))
		return fmt.Errorf("часть %d-%d оборвалась", from, to)
	}
	return nil
}

func downloadSingle(client *http.Client, url string, f *os.File, emit func(int)) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	pr := &progress{total: resp.ContentLength, emit: emit}
	_, err = io.Copy(io.MultiWriter(f, progressFunc(pr.add)), io.LimitReader(resp.Body, maxUpdateSize))
	return err
}

type progressFunc func(int)

func (p progressFunc) Write(b []byte) (int, error) { p(len(b)); return len(b), nil }

// rangeTotal — полный размер из «Content-Range: bytes 0-0/14395617».
func rangeTotal(h string) int64 {
	i := strings.LastIndex(h, "/")
	if i < 0 {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(h[i+1:]), 10, 64)
	if err != nil {
		return 0
	}
	return n
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
