package backend

// Пинг серверов подписки — реальная задержка запроса через сервер (как
// «Real delay» в Happ): временный Xray с SOCKS-входом, запрос generate_204.
// Туннель при этом не трогается.

import (
	"bytes"
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
	out := make(map[string]int, len(links))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, pingParallel)
	for _, l := range links {
		wg.Add(1)
		go func(l string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ms := -1
			if d, err := pingXrayOne(l); err == nil {
				ms = int(d.Milliseconds())
			}
			mu.Lock()
			out[l] = ms
			mu.Unlock()
		}(l)
	}
	wg.Wait()
	return out
}

func pingXrayOne(link string) (time.Duration, error) {
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
	resp, err := client.Get(pingURL)
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
