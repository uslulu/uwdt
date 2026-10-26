package backend

// Подписка VPN (как в Happ, v2rayN): https-ссылка, по которой отдаётся список
// серверов vless://… — обычно в base64. Служебные строки (#profile-title,
// happ://routing/…) понимаем частично: название берём, правила Happ не нужны —
// у UWDT свои исключения и «Россия напрямую».

import (
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// SubServer — один сервер из подписки.
type SubServer struct {
	Name      string `json:"name"`
	Link      string `json:"link"`      // исходная ссылка vless://…
	Transport string `json:"transport"` // tcp, xhttp, grpc, ws
	Security  string `json:"security"`  // reality, tls, none
	Host      string `json:"host"`
	Port      int    `json:"port"`
}

// Subscription — разобранная подписка.
type Subscription struct {
	URL     string      `json:"url"`
	Title   string      `json:"title"`
	Servers []SubServer `json:"servers"`
	Skipped int         `json:"skipped"` // строки с протоколами, которые UWDT пока не умеет
}

// FetchSubscription скачивает и разбирает подписку.
func FetchSubscription(rawURL string) (*Subscription, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("нужна ссылка https://… на подписку")
	}
	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "UWDT/"+Version)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("подписка не скачалась: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("сервер подписки ответил %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	sub := ParseSubscription(string(body))
	sub.URL = u.String()
	if sub.Title == "" {
		sub.Title = u.Host
	}
	if len(sub.Servers) == 0 {
		return nil, fmt.Errorf("в подписке нет серверов VLESS")
	}
	return sub, nil
}

// ParseSubscription разбирает тело подписки: base64 или открытый текст.
func ParseSubscription(body string) *Subscription {
	text := strings.TrimSpace(body)
	if dec, ok := decodeB64(text); ok && strings.Contains(dec, "://") {
		text = dec
	}
	sub := &Subscription{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case strings.HasPrefix(line, "#profile-title:"):
			sub.Title = profileTitle(strings.TrimSpace(strings.TrimPrefix(line, "#profile-title:")))
		case strings.HasPrefix(line, "#"), strings.HasPrefix(line, "happ://"):
			// служебные строки Happ и комментарии
		case strings.HasPrefix(line, "vless://"):
			if s, err := ParseVLESS(line); err == nil {
				sub.Servers = append(sub.Servers, s.Server())
			} else {
				sub.Skipped++
			}
		case strings.Contains(line, "://"):
			sub.Skipped++
		}
	}
	return sub
}

// «base64:…» в #profile-title — так некоторые панели передают кириллицу
func profileTitle(v string) string {
	if b, ok := strings.CutPrefix(v, "base64:"); ok {
		if dec, ok := decodeB64(b); ok {
			return strings.TrimSpace(dec)
		}
	}
	return v
}

func decodeB64(s string) (string, bool) {
	s = strings.Join(strings.Fields(s), "")
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return string(b), true
		}
	}
	return "", false
}

// VLESS — разобранная ссылка vless://uuid@host:port?params#name.
type VLESS struct {
	ID     string
	Host   string
	Port   int
	Name   string
	Params url.Values
}

func ParseVLESS(link string) (*VLESS, error) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || u.Scheme != "vless" {
		return nil, fmt.Errorf("не ссылка vless://")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port <= 0 || port > 65535 || u.Hostname() == "" || u.User.Username() == "" {
		return nil, fmt.Errorf("в ссылке vless нет адреса, порта или ID")
	}
	v := &VLESS{ID: u.User.Username(), Host: u.Hostname(), Port: port, Params: u.Query(), Name: u.Fragment}
	if v.Name == "" {
		v.Name = net2str(v.Host, port)
	}
	return v, nil
}

func net2str(host string, port int) string { return net.JoinHostPort(host, strconv.Itoa(port)) }

func (v *VLESS) param(k, def string) string {
	if s := v.Params.Get(k); s != "" {
		return s
	}
	return def
}

func (v *VLESS) Server() SubServer {
	return SubServer{
		Name:      v.Name,
		Link:      v.raw(),
		Transport: v.param("type", "tcp"),
		Security:  v.param("security", "none"),
		Host:      v.Host,
		Port:      v.Port,
	}
}

func (v *VLESS) raw() string {
	u := url.URL{Scheme: "vless", User: url.User(v.ID), Host: net2str(v.Host, v.Port), RawQuery: v.Params.Encode(), Fragment: v.Name}
	return u.String()
}
