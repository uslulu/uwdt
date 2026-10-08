package backend

// Ручная капча ВК: ядро присылает событие captcha_required с адресом
// страницы капчи, мост открывает её в отдельном окне (процесс
// `--captcha-window`), человек решает, окно возвращает success_token,
// мост отдаёт его ядру через SolveCaptcha.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const captchaWindowTimeout = 175 * time.Second // чуть меньше, чем ядро ждёт ответа

var captchaWindowMu sync.Mutex // одно окно за раз — ВК всё равно ставит общую блокировку

func isVKCaptchaHost(host string) bool {
	host = strings.ToLower(host)
	for _, d := range []string{"vk.com", "vk.ru"} {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// captchaBridgeJS — скрипт для страницы капчи: ничего не решает и не нажимает,
// только замечает success_token в ответе API ВК после того, как человек прошёл
// проверку, и передаёт его окну. post — выражение, отправляющее строку t.
func captchaBridgeJS(post string) string {
	return `(function () {
  if (window.__uwdt) return; window.__uwdt = 1;
  var sent = false;
  function scan(text) {
    if (sent || !text) return;
    var m = /"success_token"\s*:\s*"([^"]+)"/.exec(String(text));
    if (m) { sent = true; var t = m[1]; ` + post + `; }
  }
  var of = window.fetch;
  if (of) {
    window.fetch = function () {
      return of.apply(this, arguments).then(function (r) {
        try { if (/captcha/i.test(String(r.url || ''))) r.clone().text().then(scan, function () {}); } catch (e) {}
        return r;
      });
    };
  }
  var oo = XMLHttpRequest.prototype.open, os = XMLHttpRequest.prototype.send;
  XMLHttpRequest.prototype.open = function (m, u) { this.__u = String(u || ''); return oo.apply(this, arguments); };
  XMLHttpRequest.prototype.send = function () {
    var x = this;
    x.addEventListener('load', function () { try { if (/captcha/i.test(x.__u)) scan(x.responseText); } catch (e) {} });
    return os.apply(this, arguments);
  };
  window.addEventListener('message', function (e) { try { scan(typeof e.data === 'string' ? e.data : JSON.stringify(e.data)); } catch (er) {} });
})();`
}

// handleCaptchaRequest вызывается мостом на событие captcha_required.
func (b *Bridge) handleCaptchaRequest(data string) {
	var req struct {
		Mode        string `json:"mode"`
		RedirectURI string `json:"redirect_uri"`
	}
	if json.Unmarshal([]byte(data), &req) != nil || req.RedirectURI == "" {
		return
	}
	if !captchaWindowMu.TryLock() {
		return // окно уже открыто
	}
	defer captchaWindowMu.Unlock()

	b.onEvent("captcha_window", "open")
	b.onEvent("log", "INFO", "[КАПЧА] ВКонтакте просит пройти проверку — решите капчу в открывшемся окне")
	token, err := runCaptchaWindow(req.RedirectURI)
	b.onEvent("captcha_window", "closed")

	b.mu.Lock()
	c := b.core
	b.mu.Unlock()
	if c == nil {
		return
	}
	if err != nil {
		b.onEvent("log", "ERROR", "[КАПЧА] "+err.Error())
		c.SolveCaptcha("error:" + err.Error())
		return
	}
	b.onEvent("log", "INFO", "[КАПЧА] Проверка пройдена, продолжаю подключение")
	c.SolveCaptcha(token)
}

func runCaptchaWindow(pageURL string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), captchaWindowTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "--captcha-window", pageURL)
	hideWindow(cmd)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("окно капчи не открылось: %w", err)
	}
	defer cmd.Wait()

	sc := bufio.NewScanner(out)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "TOKEN "):
			return strings.TrimSpace(strings.TrimPrefix(line, "TOKEN ")), nil
		case line == "CANCEL":
			return "", fmt.Errorf("окно капчи закрыто без решения")
		}
	}
	if ctx.Err() != nil {
		return "", fmt.Errorf("время на капчу вышло")
	}
	return "", fmt.Errorf("окно капчи закрылось без ответа")
}
