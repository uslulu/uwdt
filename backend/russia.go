package backend

// «Россия напрямую»: все IPv4-сети, которые RIPE числит за Россией, идут мимо
// туннеля. В бинарь вшит снимок списка; раз в неделю приложение тянет
// свежий с RIPEstat и кладёт рядом с настройками.

import (
	"bufio"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed ru_ipv4.txt
var embeddedRuNets string

const (
	ruNetsURL        = "https://stat.ripe.net/data/country-resource-list/data.json?resource=RU&v4_format=prefix"
	ruNetsMaxAge     = 7 * 24 * time.Hour
	ruNetsMinEntries = 5000 // меньше — значит ответ битый, не доверяем
)

var ruNetsMu sync.Mutex

func ruNetsCachePath() string { return filepath.Join(configDir(), "ru_ipv4.txt") }

func parseNetList(text string) []string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if c, ok := validExcludeNet(line); ok {
			out = append(out, c)
		}
	}
	return out
}

func validExcludeNet(c string) (string, bool) {
	ip, n, err := net.ParseCIDR(c)
	if err != nil || ip.To4() == nil {
		return "", false
	}
	if ones, _ := n.Mask.Size(); ones < 8 {
		return "", false
	}
	return n.String(), true
}

// russiaNets — актуальный список: кэш с диска, если он есть и цел, иначе вшитый снимок.
// Если кэш старше недели — в фоне запускается обновление.
func russiaNets(logf wgLogFunc) []string {
	ruNetsMu.Lock()
	defer ruNetsMu.Unlock()
	path := ruNetsCachePath()
	if data, err := os.ReadFile(path); err == nil {
		if nets := parseNetList(string(data)); len(nets) >= ruNetsMinEntries {
			if st, err := os.Stat(path); err == nil && time.Since(st.ModTime()) > ruNetsMaxAge {
				go refreshRussiaNets(logf)
			}
			return nets
		}
	}
	go refreshRussiaNets(logf)
	return parseNetList(embeddedRuNets)
}

var ruRefreshOnce sync.Mutex

func refreshRussiaNets(logf wgLogFunc) {
	if !ruRefreshOnce.TryLock() {
		return
	}
	defer ruRefreshOnce.Unlock()
	nets, err := fetchRussiaNets()
	if err != nil {
		if logf != nil {
			logf("Список российских сетей не обновлён: " + err.Error())
		}
		return
	}
	body := fmt.Sprintf("# RIPE country-resource-list RU, %s, %d сетей\n%s\n",
		time.Now().Format("2006-01-02"), len(nets), strings.Join(nets, "\n"))
	ruNetsMu.Lock()
	err = atomicWrite(ruNetsCachePath(), []byte(body))
	ruNetsMu.Unlock()
	if err == nil && logf != nil {
		logf(fmt.Sprintf("Список российских сетей обновлён: %d", len(nets)))
	}
}

func fetchRussiaNets() ([]string, error) {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(ruNetsURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("RIPE ответил %d", resp.StatusCode)
	}
	var data struct {
		Data struct {
			Resources struct {
				IPv4 []string `json:"ipv4"`
			} `json:"resources"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	nets := aggregateNets(data.Data.Resources.IPv4)
	if len(nets) < ruNetsMinEntries {
		return nil, fmt.Errorf("подозрительно короткий список (%d)", len(nets))
	}
	return nets, nil
}

// aggregateNets сливает пересекающиеся и соседние сети в минимальный набор CIDR.
func aggregateNets(prefixes []string) []string {
	type span struct{ lo, hi uint64 }
	var spans []span
	for _, p := range prefixes {
		_, n, err := net.ParseCIDR(strings.TrimSpace(p))
		if err != nil || n.IP.To4() == nil {
			continue
		}
		lo := uint64(binary.BigEndian.Uint32(n.IP.To4()))
		ones, _ := n.Mask.Size()
		spans = append(spans, span{lo, lo + (1 << (32 - ones)) - 1})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].lo < spans[j].lo })
	var merged []span
	for _, s := range spans {
		if k := len(merged); k > 0 && s.lo <= merged[k-1].hi+1 {
			if s.hi > merged[k-1].hi {
				merged[k-1].hi = s.hi
			}
			continue
		}
		merged = append(merged, s)
	}
	var out []string
	for _, s := range merged {
		for lo := s.lo; lo <= s.hi; {
			// самый крупный блок, выровненный по lo и не выходящий за hi
			size := uint64(1)
			for size < 1<<32 && lo%(size*2) == 0 && lo+size*2-1 <= s.hi {
				size *= 2
			}
			bits := 0
			for (uint64(1) << bits) < size {
				bits++
			}
			ip := make(net.IP, 4)
			binary.BigEndian.PutUint32(ip, uint32(lo))
			out = append(out, fmt.Sprintf("%s/%d", ip, 32-bits))
			lo += size
		}
	}
	return out
}

// russiaNetsDate — дата актуального списка из его заголовка ("# RIPE ..., 2026-10-07, ...").
func russiaNetsDate() string {
	text := embeddedRuNets
	if data, err := os.ReadFile(ruNetsCachePath()); err == nil && len(parseNetList(string(data))) >= ruNetsMinEntries {
		text = string(data)
	}
	first, _, _ := strings.Cut(text, "\n")
	parts := strings.Split(first, ",")
	if len(parts) >= 2 {
		if t, err := time.Parse("2006-01-02", strings.TrimSpace(parts[1])); err == nil {
			return t.Format("02.01.2006")
		}
	}
	return ""
}

// Экспорт для тестов
func AggregateNets(p []string) []string { return aggregateNets(p) }
func EmbeddedRussiaNets() []string      { return parseNetList(embeddedRuNets) }
