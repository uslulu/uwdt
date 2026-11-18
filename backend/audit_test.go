package backend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	core "wg-turn-client"

	"github.com/zalando/go-keyring"
)

func TestProfileLegacyRenameAndLockedVault(t *testing.T) {
	s := NewTestStore(t)
	legacy := filepath.Join(s.baseDir, "servers", "Дом.json")
	const source = `{"peer":"192.0.2.1","device_id":"old-device","password":"synthetic-secret"}`
	if err := os.WriteFile(legacy, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	keyring.MockInitWithError(errors.New("locked"))
	if err := s.writeProfile("Дом", "Дом!", ProfileData{Password: "synthetic-secret"}); err == nil {
		t.Fatal("saved with locked vault")
	}
	data, _ := os.ReadFile(legacy)
	if string(data) != source {
		t.Fatal("legacy copy changed after failed migration")
	}
	keyring.MockInit()
	p, err := s.LoadProfile("Дом")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Дом!", "дом!"} {
		if err = s.writeProfile(map[string]string{"Дом!": "Дом", "дом!": "Дом!"}[name], name, *p); err != nil {
			t.Fatal(err)
		}
		got, err := s.LoadProfile(name)
		if err != nil || got.DeviceID != "old-device" || got.Password != "synthetic-secret" {
			t.Fatalf("rename %q failed: %v", name, err)
		}
	}
	data, _ = os.ReadFile(legacy)
	if strings.Contains(string(data), "synthetic-secret") {
		t.Fatal("secret in profile")
	}
	if err := s.SaveProfile("Другой", ProfileData{}); err != nil {
		t.Fatal(err)
	}
	if err := s.writeProfile("дом!", "другой", *p); err == nil {
		t.Fatal("overwrote another profile")
	}
	if err := s.DeleteProfile("отсутствует"); err == nil {
		t.Fatal("missing delete reported success")
	}
}
func TestConcurrentSettingsUpdate(t *testing.T) {
	s := NewTestStore(t)
	var wg sync.WaitGroup
	for _, change := range []func(*AppSettings){func(s *AppSettings) { s.TurnTCP = true }, func(s *AppSettings) { s.RussiaDirect = true }} {
		wg.Go(func() {
			if err := s.updateSettings(change); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	got := s.LoadSettings()
	if !got.TurnTCP || !got.RussiaDirect {
		t.Fatal(got)
	}
}
func TestFlatAndOverlappingRuleSources(t *testing.T) {
	const value = "192.0.2.7/32"
	sources := []ExcludeEntry{{Value: value, Enabled: true}, {Source: "A", Value: value, Enabled: true}, {Source: "B", Value: value, Enabled: true}}
	for len(sources) > 0 {
		got, err := ValidateExcludes(sources)
		if err != nil || !slices.Equal(got, sources) {
			t.Fatalf("lost source: %v %v", got, err)
		}
		if resolved := resolveExcludes(got, nil, true); !slices.Equal(resolved, []string{value}) {
			t.Fatal(resolved)
		}
		sources = sources[:len(sources)-1]
	}
}
func TestAddressInspectionReadOnly(t *testing.T) {
	a := &App{store: NewTestStore(t)}
	if err := a.store.SaveSettings(AppSettings{Excludes: []ExcludeEntry{{Value: "127.0.0.0/8", Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	rows, err := a.InspectAddress("127.0.0.1")
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	row := rows[0]
	if !slices.Contains(row.Rules, "127.0.0.0/8") || row.Route == "" && row.Error == "" {
		t.Fatal(row)
	}
	if a.RuleStatus() != "При следующем подключении" {
		t.Fatal(a.RuleStatus())
	}
	t.Logf("OS observation: %s %s", row.Route, row.Error)
}
func BenchmarkRussianSnapshot(b *testing.B) {
	b.Setenv("HOME", b.TempDir())
	expected := parseNetList(embeddedRuNets)
	got, _ := russiaSnapshot()
	if !slices.Equal(got, expected) {
		b.Fatal("prefix set changed")
	}
	b.Run("parse", func(b *testing.B) {
		for b.Loop() {
			parseNetList(embeddedRuNets)
		}
	})
	b.Run("cached", func(b *testing.B) {
		for b.Loop() {
			russiaSnapshot()
		}
	})
}
func BenchmarkSessionLog(b *testing.B) {
	// Same serialized records and fsync cadence for the old and new paths.
	for _, mode := range []string{"synchronous", "queued"} {
		b.Run(mode, func(b *testing.B) {
			s := &Store{baseDir: b.TempDir()}
			os.Mkdir(filepath.Join(s.baseDir, "logs"), 0700)
			message := strings.Repeat("x", 120)
			latency := make([]int64, 2000)
			if mode == "synchronous" {
				file, err := os.CreateTemp(s.baseDir, "baseline")
				if err != nil {
					b.Fatal(err)
				}
				defer file.Close()
				for b.Loop() {
					for i := 1; i <= 2000; i++ {
						start := time.Now()
						fmt.Fprintf(file, "[%s] [%s] %s\n", time.Now().Format("15:04:05"), "INFO", message)
						if i%50 == 0 {
							file.Sync()
						}
						latency[i-1] = time.Since(start).Nanoseconds()
					}
				}
			} else {
				file := s.OpenLogFile()
				defer file.Close()
				for b.Loop() {
					for i := 0; i < 2000; i++ {
						start := time.Now()
						file.Write("INFO", message)
						latency[i] = time.Since(start).Nanoseconds()
					}
				}
			}
			slices.Sort(latency)
			b.ReportMetric(float64(latency[1899]), "p95-ns")
			b.ReportMetric(float64(latency[1979]), "p99-ns")
		})
	}
}

func TestProfileFailuresPreserveData(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires POSIX directory permissions")
	}
	s := NewTestStore(t)
	original := ProfileData{PeerAddr: "192.0.2.1", DeviceID: "stable"}
	if err := s.SaveProfile("Дом", original); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(s.baseDir, "servers")
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0700)
	if err := s.writeProfile("Дом", "Дом!", ProfileData{PeerAddr: "192.0.2.2"}); err == nil {
		t.Fatal("failed write reported success")
	}
	if err := s.DeleteProfile("Дом"); err == nil {
		t.Fatal("failed delete reported success")
	}
	if got, err := s.LoadProfile("Дом"); err != nil || got.DeviceID != "stable" || got.PeerAddr != original.PeerAddr {
		t.Fatal(got, err)
	}
}
func TestProtectedCatalogRoundTrip(t *testing.T) {
	keyring.MockInit()
	a := &App{store: NewTestStore(t)}
	const data = `[{"link":"vless://synthetic","subUrl":"https://example.invalid/synthetic"}]`
	if err := a.SaveCatalog(data); err != nil {
		t.Fatal(err)
	}
	saved, err := a.LoadCatalog()
	if err != nil || saved != data {
		t.Fatal(err)
	}
	path := filepath.Join(a.store.baseDir, "catalog.json")
	disk, _ := os.ReadFile(path)
	if strings.Contains(string(disk), "synthetic") {
		t.Fatal("secret on disk")
	}
	keyring.MockInitWithError(errors.New("locked"))
	if err := a.SaveCatalog("[]"); err == nil {
		t.Fatal("wrote without keychain")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(disk, after) {
		t.Fatal("catalog changed")
	}
}

func TestDisconnectThenXrayWaitsForOldTURNConsumer(t *testing.T) {
	store := NewTestStore(t)
	connected := make(chan struct{})
	bridge := NewBridge(context.Background(), store, func(event string, args ...any) {
		if event == "state_changed" && args[0] == "connected" {
			close(connected)
		}
	})
	bridge.running = true
	bridge.session = 1
	oldDone := make(chan struct{})
	bridge.sessionWorkDone = oldDone
	bridge.applyXray = func(string, []string, wgLogFunc) error { return nil }
	disconnected := make(chan struct{})
	go func() { bridge.Disconnect(); close(disconnected) }()
	deadline := time.After(2 * time.Second)
	for {
		bridge.mu.Lock()
		stopping := bridge.stopping
		bridge.mu.Unlock()
		if stopping {
			break
		}
		select {
		case <-deadline:
			t.Fatal("disconnect did not start")
		default:
			runtime.Gosched()
		}
	}
	started := make(chan error, 1)
	go func() {
		started <- bridge.ConnectXray("vless://11111111-2222-3333-4444-555555555555@192.0.2.1:443?type=tcp", "synthetic")
	}()
	events := make(chan core.Event, 1)
	events <- core.Event{Type: core.EventState, Status: "connected"}
	close(events)
	bridge.forwardEvents(context.Background(), events, 1)
	close(oldDone)
	<-disconnected
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	select {
	case <-connected:
	case <-time.After(time.Second):
		t.Fatal("Xray did not connect")
	}
	if err := bridge.Disconnect(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsPartialBatchCanBeDisabled(t *testing.T) {
	const user, builtin, gw = "192.0.2.1/32", "198.51.100.1/32", "192.0.2.254"
	w := WG{physGW: gw, builtinWindows: map[string]bool{builtin: true}, ownedWindows: map[windowsRoute]bool{
		{prefix: user, gateway: gw, iface: 1}: true, {prefix: builtin, gateway: gw, iface: 1}: true,
	}}
	add, remove := w.planWindowsUserRoutes(nil)
	if len(add) != 0 || !slices.Equal(remove, []string{user}) {
		t.Fatal(add, remove)
	}
}
