package backend

// Исключение по номеру автономной системы: все сети, которые объявляет
// компания (по данным RIPEstat). Кэш на диске, обновление раз в неделю.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const asnMaxAge = 7 * 24 * time.Hour

func asnCachePath(asn string) string { return filepath.Join(configDir(), "asn", asn+".txt") }

// asnNets — сети AS: свежий кэш или запрос к RIPEstat; при ошибке сети — старый кэш.
func asnNets(asn string, logf wgLogFunc) []string {
	path := asnCachePath(asn)
	var cached []string
	if data, err := os.ReadFile(path); err == nil {
		cached = parseNetList(string(data))
		if st, err := os.Stat(path); err == nil && time.Since(st.ModTime()) < asnMaxAge && len(cached) > 0 {
			return cached
		}
	}
	nets, err := fetchASNNets(asn)
	if err != nil {
		if logf != nil {
			logf(fmt.Sprintf("Исключение %s: не удалось получить сети (%v)", asn, err))
		}
		return cached
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = atomicWrite(path, []byte(fmt.Sprintf("# %s, %s\n%s\n", asn, time.Now().Format("2006-01-02"), strings.Join(nets, "\n"))))
	return nets
}

func fetchASNNets(asn string) ([]string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get("https://stat.ripe.net/data/announced-prefixes/data.json?resource=" + asn)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("RIPE ответил %d", resp.StatusCode)
	}
	var data struct {
		Data struct {
			Prefixes []struct {
				Prefix string `json:"prefix"`
			} `json:"prefixes"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	var raw []string
	for _, p := range data.Data.Prefixes {
		if !strings.Contains(p.Prefix, ":") {
			raw = append(raw, p.Prefix)
		}
	}
	nets := aggregateNets(raw)
	var out []string
	for _, c := range nets {
		if v, ok := validExcludeNet(c); ok { // слишком широкие (< /8) не берём
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("у %s нет IPv4-сетей", asn)
	}
	return out, nil
}
