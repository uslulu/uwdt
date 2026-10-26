package backend_test

// Живая проверка серверов подписки: Xray с SOCKS-входом вместо TUN, запрос
// через него. Запускается только с UWDT_LIVE_SUB=<путь к файлу подписки>.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"pwdtt/backend"

	core "github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf/serial"
)

func TestXrayLiveSubscription(t *testing.T) {
	path := os.Getenv("UWDT_LIVE_SUB")
	if path == "" {
		t.Skip("UWDT_LIVE_SUB не задан")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sub := backend.ParseSubscription(string(body))
	for i, s := range sub.Servers {
		js, err := backend.BuildXrayConfig(s.Link, 1400)
		if err != nil {
			t.Errorf("%s: %v", s.Name, err)
			continue
		}
		var cfg map[string]any
		_ = json.Unmarshal(js, &cfg)
		port := 21080 + i
		cfg["inbounds"] = []any{map[string]any{"listen": "127.0.0.1", "port": port, "protocol": "socks", "settings": map[string]any{"udp": false}}}
		js, _ = json.Marshal(cfg)
		c, err := serial.LoadJSONConfig(bytes.NewReader(js))
		if err != nil {
			t.Errorf("%s: %v", s.Name, err)
			continue
		}
		inst, err := core.New(c)
		if err == nil {
			err = inst.Start()
		}
		if err != nil {
			t.Errorf("%s: %v", s.Name, err)
			continue
		}
		proxy, _ := url.Parse(fmt.Sprintf("socks5://127.0.0.1:%d", port))
		client := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}}
		start := time.Now()
		resp, err := client.Get("https://ifconfig.me/ip")
		if err != nil {
			t.Errorf("%s (%s): %v", s.Name, s.Transport, err)
		} else {
			ip, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			t.Logf("OK %-28s %-6s выход %s за %s", s.Name, s.Transport, bytes.TrimSpace(ip), time.Since(start).Round(time.Millisecond))
		}
		inst.Close()
	}
}
