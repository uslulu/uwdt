package backend

// Пользовательские исключения из туннеля: подсети, адреса и домены,
// которые идут мимо туннеля через физический шлюз (например корпоративный
// VPN или российские сервисы). Хранятся в config.json, применяются при
// подключении и на лету — без переподключения и без повторного пароля.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ExcludeEntry — одна строка списка исключений.
type ExcludeEntry struct {
	Value   string `json:"value"`   // 10.0.0.0/8, 1.2.3.4 или meet.example.ru
	Enabled bool   `json:"enabled"` // выключенная строка хранится, но не применяется
}

const excludeRefreshInterval = 10 * time.Minute

var domainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// Номер автономной системы — все сети компании (AS32590 — Valve: Steam, Dota 2)
var asnRe = regexp.MustCompile(`^as\s*([0-9]{1,10})$`)

// NormalizeExclude приводит ввод пользователя к канонической форме.
// Принимает IPv4, CIDR, домен, номер AS, а также вставленный URL (берётся хост).
// kind: "cidr", "domain" или "asn".
func NormalizeExclude(raw string) (kind, value string, err error) {
	v := strings.TrimSpace(strings.ToLower(raw))
	if v == "" {
		return "", "", fmt.Errorf("пустая строка")
	}
	if m := asnRe.FindStringSubmatch(v); m != nil {
		n, err := strconv.ParseUint(m[1], 10, 32)
		if err != nil || n == 0 {
			return "", "", fmt.Errorf("неверный номер AS: %s", raw)
		}
		return "asn", fmt.Sprintf("AS%d", n), nil
	}
	if strings.Contains(v, "://") {
		if u, perr := url.Parse(v); perr == nil && u.Hostname() != "" {
			v = u.Hostname()
		}
	}
	// "host/path" без схемы: путь отбрасываем, а "/N" у адреса — это маска
	if left, _, found := strings.Cut(v, "/"); found && net.ParseIP(left) == nil {
		v = left
	}
	v = strings.TrimSuffix(v, ".")

	if strings.Contains(v, "/") {
		ip, ipnet, perr := net.ParseCIDR(v)
		if perr != nil || ip.To4() == nil {
			return "", "", fmt.Errorf("неверная подсеть: %s", raw)
		}
		ones, _ := ipnet.Mask.Size()
		if ones < 8 {
			return "", "", fmt.Errorf("слишком широкая подсеть /%d — минимум /8", ones)
		}
		return "cidr", ipnet.String(), nil
	}
	if ip := net.ParseIP(v); ip != nil {
		if ip.To4() == nil {
			return "", "", fmt.Errorf("IPv6 пока не поддерживается: %s", raw)
		}
		return "cidr", ip.To4().String() + "/32", nil
	}
	if domainRe.MatchString(v) {
		return "domain", v, nil
	}
	return "", "", fmt.Errorf("не похоже на адрес, подсеть или домен: %s", raw)
}

// ValidateExcludes нормализует список, отбрасывает дубликаты и возвращает
// первую ошибку (строка с ошибкой в список не попадает).
func ValidateExcludes(entries []ExcludeEntry) ([]ExcludeEntry, error) {
	var out []ExcludeEntry
	seen := map[string]bool{}
	var firstErr error
	for _, e := range entries {
		_, v, err := NormalizeExclude(e.Value)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, ExcludeEntry{Value: v, Enabled: e.Enabled})
	}
	return out, firstErr
}

// resolveExcludes превращает включённые записи в список CIDR.
// fast=true — без сети: домены и AS берутся из кэша (для подключения, чтобы
// медленный DNS не задерживал туннель); fast=false — свежий запрос, домены
// параллельно и с запасными DNS-серверами.
func resolveExcludes(entries []ExcludeEntry, logf wgLogFunc, fast bool) []string {
	set := map[string]bool{}
	var domains []string
	for _, e := range entries {
		if !e.Enabled {
			continue
		}
		kind, v, err := NormalizeExclude(e.Value)
		if err != nil {
			continue
		}
		switch kind {
		case "cidr":
			set[v] = true
		case "asn":
			var nets []string
			if fast {
				nets = asnNetsCached(v)
			} else {
				nets = asnNets(v, logf)
			}
			for _, c := range nets {
				set[c] = true
			}
		case "domain":
			domains = append(domains, v)
		}
	}
	for _, ips := range resolveDomains(domains, logf, fast) {
		for _, ip := range ips {
			set[ip+"/32"] = true
		}
	}
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// ── домены: кэш последних адресов + параллельный резолв с запасными DNS ──

var (
	dnsCacheMu     sync.Mutex
	dnsCache       map[string][]string // домен → последние известные IPv4
	dnsCacheLoaded bool
	logOnceMu      sync.Mutex
	logOnceSeen    = map[string]time.Time{}
)

// Запасные DNS: оба уже идут мимо туннеля (77.88/18 и 1.1.1/24 во встроенных исключениях)
var fallbackDNS = []string{"77.88.8.8:53", "1.1.1.1:53"}

func dnsCachePath() string { return filepath.Join(configDir(), "dns_cache.json") }

func loadDNSCacheLocked() {
	if dnsCacheLoaded {
		return
	}
	dnsCacheLoaded = true
	dnsCache = map[string][]string{}
	if data, err := os.ReadFile(dnsCachePath()); err == nil {
		_ = json.Unmarshal(data, &dnsCache)
	}
}

// logOnce пишет сообщение не чаще раза в полчаса для одного ключа.
func logOnce(logf wgLogFunc, key, msg string) {
	if logf == nil {
		return
	}
	logOnceMu.Lock()
	last, seen := logOnceSeen[key]
	if seen && time.Since(last) < 30*time.Minute {
		logOnceMu.Unlock()
		return
	}
	logOnceSeen[key] = time.Now()
	logOnceMu.Unlock()
	logf(msg)
}

func lookupIPv4(ctx context.Context, r *net.Resolver, host string) []string {
	addrs, err := r.LookupIPAddr(ctx, host)
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		if ip4 := a.IP.To4(); ip4 != nil {
			out = append(out, ip4.String())
		}
	}
	return out
}

// lookupDomain: системный DNS (у Cisco бывает медленным), затем запасные серверы напрямую.
func lookupDomain(host string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	ips := lookupIPv4(ctx, net.DefaultResolver, host)
	cancel()
	if len(ips) > 0 {
		return ips
	}
	for _, ns := range fallbackDNS {
		ns := ns
		r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 2 * time.Second}
			return d.DialContext(ctx, "udp", ns)
		}}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		ips = lookupIPv4(ctx, r, host)
		cancel()
		if len(ips) > 0 {
			return ips
		}
	}
	return nil
}

func resolveDomains(domains []string, logf wgLogFunc, fast bool) map[string][]string {
	out := map[string][]string{}
	if len(domains) == 0 {
		return out
	}
	dnsCacheMu.Lock()
	loadDNSCacheLocked()
	cached := map[string][]string{}
	for _, d := range domains {
		cached[d] = dnsCache[d]
	}
	dnsCacheMu.Unlock()
	if fast {
		for d, ips := range cached {
			if len(ips) > 0 {
				out[d] = ips
			}
		}
		return out
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, d := range domains {
		wg.Add(1)
		go func(d string) {
			defer wg.Done()
			ips := lookupDomain(d)
			mu.Lock()
			defer mu.Unlock()
			if len(ips) == 0 {
				if old := cached[d]; len(old) > 0 {
					out[d] = old // DNS молчит — держим прошлые адреса
				}
				logOnce(logf, "dns:"+d, fmt.Sprintf("Исключение %s: DNS не ответил, использую прошлые адреса (%d)", d, len(cached[d])))
				return
			}
			out[d] = ips
		}(d)
	}
	wg.Wait()

	dnsCacheMu.Lock()
	changed := false
	for d, ips := range out {
		if strings.Join(dnsCache[d], ",") != strings.Join(ips, ",") {
			dnsCache[d] = ips
			changed = true
		}
	}
	if changed {
		if data, err := json.Marshal(dnsCache); err == nil {
			_ = atomicWrite(dnsCachePath(), data)
		}
	}
	dnsCacheMu.Unlock()
	return out
}

// excludeSource — откуда WG берёт актуальный список (задаётся App при старте).
var (
	excludeSourceMu sync.Mutex
	excludeSource   func() []ExcludeEntry
)

func SetExcludeSource(f func() []ExcludeEntry) {
	excludeSourceMu.Lock()
	excludeSource = f
	excludeSourceMu.Unlock()
}

func currentUserExcludes(logf wgLogFunc) []string { return userExcludes(logf, false) }

// currentUserExcludesFast — без сети, из кэша: для момента подключения.
func currentUserExcludesFast(logf wgLogFunc) []string { return userExcludes(logf, true) }

func userExcludes(logf wgLogFunc, fast bool) []string {
	excludeSourceMu.Lock()
	f := excludeSource
	excludeSourceMu.Unlock()
	if f == nil {
		return nil
	}
	return resolveExcludes(f(), logf, fast)
}

// «Россия напрямую» — флаг из настроек (задаётся App при старте)
var (
	russiaSourceMu sync.Mutex
	russiaSource   func() bool
)

func SetRussiaDirectSource(f func() bool) {
	russiaSourceMu.Lock()
	russiaSource = f
	russiaSourceMu.Unlock()
}

func russiaDirectEnabled() bool {
	russiaSourceMu.Lock()
	f := russiaSource
	russiaSourceMu.Unlock()
	return f != nil && f()
}

// currentAllExcludes — пользовательский список плюс российские сети, если включено.
func currentAllExcludes(logf wgLogFunc) []string {
	list := currentUserExcludes(logf)
	if !russiaDirectEnabled() {
		return list
	}
	seen := make(map[string]bool, len(list))
	for _, c := range list {
		seen[c] = true
	}
	for _, c := range russiaNets(logf) {
		if !seen[c] {
			list = append(list, c)
		}
	}
	return list
}

// diffCIDRs возвращает, что добавить и что убрать, чтобы из old получить new.
func diffCIDRs(old, new []string) (add, del []string) {
	o := map[string]bool{}
	for _, c := range old {
		o[c] = true
	}
	n := map[string]bool{}
	for _, c := range new {
		n[c] = true
		if !o[c] {
			add = append(add, c)
		}
	}
	for _, c := range old {
		if !n[c] {
			del = append(del, c)
		}
	}
	return
}

// Экспорт для тестов
func DiffCIDRs(old, new []string) (add, del []string) { return diffCIDRs(old, new) }

// excludeRefreshLoop периодически перерезолвит домены из списка исключений:
// у сервисов адреса меняются, а маршрут привязан к адресу.
func (w *WG) excludeRefreshLoop(stop chan struct{}, logf wgLogFunc) {
	t := time.NewTicker(excludeRefreshInterval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			w.RefreshExcludes(logf)
		}
	}
}
