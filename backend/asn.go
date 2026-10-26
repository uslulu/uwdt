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
	"sync"
	"time"
)

const asnMaxAge = 7 * 24 * time.Hour

func asnCachePath(asn string) string { return filepath.Join(configDir(), "asn", asn+".txt") }

// asnNetsCached — сети AS из кэша без обращения к сети (может быть пусто).
func asnNetsCached(asn string) []string {
	if data, err := os.ReadFile(asnCachePath(asn)); err == nil {
		return parseNetList(string(data))
	}
	return nil
}

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
		logOnce(logf, "asn:"+asn, fmt.Sprintf("Исключение %s: не удалось получить сети (%v)", asn, err))
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

// ── названия компаний для номеров AS (подпись в окне исключений) ──

var (
	asnNamesMu sync.Mutex
	asnNameMap map[string]string // AS32590 → Valve Corporation; загружается с диска один раз
)

func asnNamesPath() string { return filepath.Join(configDir(), "asn", "names.json") }

// ASNames — названия компаний для номеров AS: из кэша, недостающие — из RIPEstat.
// Название не меняется, поэтому кэш вечный; без сети просто вернём что есть.
func ASNames(asns []string) map[string]string {
	asnNamesMu.Lock()
	if asnNameMap == nil {
		asnNameMap = map[string]string{}
		if data, err := os.ReadFile(asnNamesPath()); err == nil {
			_ = json.Unmarshal(data, &asnNameMap)
		}
	}
	var missing []string
	for _, a := range asns {
		if _, ok := asnNameMap[a]; !ok {
			missing = append(missing, a)
		}
	}
	asnNamesMu.Unlock()

	var wg sync.WaitGroup
	var mu sync.Mutex
	fetched := map[string]string{}
	sem := make(chan struct{}, 4)
	for _, a := range missing {
		wg.Add(1)
		go func(a string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if name, err := fetchASNName(a); err == nil && name != "" {
				mu.Lock()
				fetched[a] = name
				mu.Unlock()
			}
		}(a)
	}
	wg.Wait()

	asnNamesMu.Lock()
	defer asnNamesMu.Unlock()
	if len(fetched) > 0 {
		for k, v := range fetched {
			asnNameMap[k] = v
		}
		if data, err := json.Marshal(asnNameMap); err == nil {
			_ = os.MkdirAll(filepath.Dir(asnNamesPath()), 0o755)
			_ = atomicWrite(asnNamesPath(), data)
		}
	}
	out := make(map[string]string, len(asns))
	for _, a := range asns {
		if n, ok := asnNameMap[a]; ok {
			out[a] = n
		}
	}
	return out
}

func fetchASNName(asn string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("https://stat.ripe.net/data/as-overview/data.json?resource=" + asn)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("RIPE ответил %d", resp.StatusCode)
	}
	var data struct {
		Data struct {
			Holder string `json:"holder"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}
	return ASNHolderName(data.Data.Holder), nil
}

// ASNHolderName: «VALVE-CORPORATION - Valve Corporation, US» → «Valve Corporation».
func ASNHolderName(holder string) string {
	h := strings.TrimSpace(holder)
	if i := strings.Index(h, " - "); i >= 0 {
		h = h[i+3:]
	}
	if i := strings.LastIndex(h, ", "); i >= 0 && len(h)-i-2 == 2 {
		h = h[:i] // код страны в конце
	}
	return strings.TrimSpace(h)
}
