package backend_test

// Живая проверка серверов подписки: Xray с SOCKS-входом вместо TUN, запрос
// через него. Запускается только с UWDT_LIVE_SUB=<путь к файлу подписки>.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
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

func TestPingXrayLive(t *testing.T) {
	path := os.Getenv("UWDT_LIVE_SUB")
	if path == "" {
		t.Skip("UWDT_LIVE_SUB не задан")
	}
	body, _ := os.ReadFile(path)
	sub := backend.ParseSubscription(string(body))
	var links []string
	for _, s := range sub.Servers {
		links = append(links, s.Link)
	}
	// 192.0.2.1 — адрес из документационной сети, не отвечает никогда
	const dead = "vless://11111111-2222-3333-4444-555555555555@192.0.2.1:443?type=tcp&security=reality&sni=a.com&pbk=bmXOC-gsYBnUzqfQ7J6Hv0Tg8ApEMTx3jJbW6bBV4lA#dead"
	res := backend.PingXray(append(links, dead))
	for _, s := range sub.Servers {
		if res[s.Link] <= 0 {
			t.Errorf("%s: нет ответа", s.Name)
		}
		t.Logf("%-28s %d мс", s.Name, res[s.Link])
	}
	if v := res[dead]; v != -1 {
		t.Errorf("недоступный сервер: %d, want -1", v)
	}
}

// Правило про торренты срабатывает: в тесте его выход заменён на прямой (direct),
// а цель — эхо-сервер на 127.0.0.1, до которого сервер подписки дотянуться не
// может. BitTorrent-рукопожатие возвращается эхом — значит, правило его поймало;
// обычный трафик к тому же адресу уходит на сервер и эха нет. (Проверять сам
// blackhole через сервер нельзя: панели обычно режут торренты и на сервере.)
func TestXrayLiveTorrentRuleMatches(t *testing.T) {
	path := os.Getenv("UWDT_LIVE_SUB")
	if path == "" {
		t.Skip("UWDT_LIVE_SUB не задан")
	}
	body, _ := os.ReadFile(path)
	sub := backend.ParseSubscription(string(body))
	js, err := backend.BuildXrayConfig(sub.Servers[0].Link, 1400)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	_ = json.Unmarshal(js, &cfg)
	in := cfg["inbounds"].([]any)[0].(map[string]any)
	rule := cfg["routing"].(map[string]any)["rules"].([]any)[0].(map[string]any)
	if rule["outboundTag"] != "block" {
		t.Fatalf("правило торрентов ведёт в %v, want block", rule["outboundTag"])
	}
	rule["outboundTag"] = "direct"

	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	echoPort := echo.Addr().(*net.TCPAddr).Port

	port := 21180
	cfg["inbounds"] = []any{map[string]any{"listen": "127.0.0.1", "port": port, "protocol": "socks", "settings": map[string]any{"udp": false}, "sniffing": in["sniffing"]}}
	js, _ = json.Marshal(cfg)
	c, err := serial.LoadJSONConfig(bytes.NewReader(js))
	if err != nil {
		t.Fatal(err)
	}
	inst, err := core.New(c)
	if err == nil {
		err = inst.Start()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close()

	send := func(payload []byte) []byte {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(6 * time.Second))
		conn.Write([]byte{5, 1, 0})
		io.ReadFull(conn, make([]byte, 2))
		conn.Write([]byte{5, 1, 0, 1, 127, 0, 0, 1, byte(echoPort >> 8), byte(echoPort)})
		io.ReadFull(conn, make([]byte, 10))
		conn.Write(payload)
		buf := make([]byte, 128)
		n, _ := io.ReadAtLeast(conn, buf, len(payload))
		return buf[:n]
	}
	bt := append([]byte("\x13BitTorrent protocol"), make([]byte, 48)...)
	if got := send(bt); !bytes.Equal(got, bt) {
		t.Errorf("правило не поймало BitTorrent: эхо %d байт из %d", len(got), len(bt))
	}
	if got := send([]byte("hello uwdt")); len(got) != 0 {
		t.Errorf("обычный трафик ушёл мимо сервера подписки: %q", got)
	}
}
