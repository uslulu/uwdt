package backend

// Пинг серверов подписки — реальная задержка запроса через сервер (как
// «Real delay» в Happ): временный Xray с SOCKS-входом, запрос generate_204.
// Туннель при этом не трогается.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	core "github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf/serial"
)

const (
	pingURL      = "https://www.gstatic.com/generate_204"
	pingTimeout  = 6 * time.Second
	pingParallel = 3
)

// PingXray — задержка до каждого сервера в мс; -1 — сервер не ответил.
func PingXray(links []string) map[string]int {
	var pool pingPool
	return pool.run(context.Background(), links)
}

type pingSample struct {
	delay int
	at    time.Time
}
type pingPool struct {
	mu      sync.Mutex
	cancel  context.CancelFunc
	samples map[string]pingSample
	slots   chan struct{}
}

func (p *pingPool) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cancel != nil {
		p.cancel()
	}
}
func (p *pingPool) run(parent context.Context, links []string) map[string]int {
	p.mu.Lock()
	if p.cancel != nil {
		p.cancel()
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	p.cancel = cancel
	if p.slots == nil {
		p.slots = make(chan struct{}, pingParallel)
	}
	if p.samples == nil {
		p.samples = map[string]pingSample{}
	}
	for k, v := range p.samples {
		if time.Since(v.at) > 2*time.Minute {
			delete(p.samples, k)
		}
	}
	out := map[string]int{}
	var pending []string
	for _, l := range links {
		if _, ok := out[l]; ok {
			continue
		}
		out[l] = -1
		if cached, ok := p.samples[l]; ok {
			out[l] = cached.delay
		} else {
			pending = append(pending, l)
		}
	}
	p.mu.Unlock()
	jobs := make(chan string)
	var wg sync.WaitGroup
	for range min(pingParallel, len(pending)) {
		wg.Go(func() {
			for link := range jobs {
				select {
				case <-ctx.Done():
					return
				case p.slots <- struct{}{}:
				}
				delay, err := pingXrayOne(ctx, link)
				<-p.slots
				if ctx.Err() != nil {
					return
				}
				ms := -1
				if err == nil {
					ms = int(delay.Milliseconds())
				}
				p.mu.Lock()
				out[link] = ms
				p.samples[link] = pingSample{ms, time.Now()}
				p.mu.Unlock()
			}
		})
	}
send:
	for _, l := range pending {
		select {
		case jobs <- l:
		case <-ctx.Done():
			break send
		}
	}
	close(jobs)
	wg.Wait()
	return out
}

func pingXrayOne(ctx context.Context, link string) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	js, err := buildXrayConfig(link, xrayMTU, "")
	if err != nil {
		return 0, err
	}
	port, err := freeLocalPort()
	if err != nil {
		return 0, err
	}
	var cfg map[string]any
	if err := json.Unmarshal(js, &cfg); err != nil {
		return 0, err
	}
	cfg["inbounds"] = []any{map[string]any{"listen": "127.0.0.1", "port": port, "protocol": "socks", "settings": map[string]any{"udp": false}}}
	delete(cfg, "stats")
	delete(cfg, "policy")
	js, _ = json.Marshal(cfg)
	c, err := serial.LoadJSONConfig(bytes.NewReader(js))
	if err != nil {
		return 0, err
	}
	inst, err := core.New(c)
	if err != nil {
		return 0, err
	}
	err = inst.Start()
	restoreXrayLog() // Start временного Xray подменил обработчик логов сессии
	if err != nil {
		inst.Close()
		return 0, err
	}
	defer inst.Close()

	proxy, _ := url.Parse(fmt.Sprintf("socks5://127.0.0.1:%d", port))
	tr := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	client := &http.Client{Timeout: pingTimeout, Transport: tr}
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pingURL, nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return time.Since(start), nil
}

func freeLocalPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
