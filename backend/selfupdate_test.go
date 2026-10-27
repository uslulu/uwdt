package backend

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestDownloadVerified(t *testing.T) {
	data := make([]byte, 3<<20+12345) // не делится на число частей
	_, _ = rand.Read(data)
	sum := sha256.Sum256(data)
	want := hex.EncodeToString(sum[:])

	withRange := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "u.zip", time.Time{}, bytes.NewReader(data))
	}))
	defer withRange.Close()
	noRange := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data)
	}))
	defer noRange.Close()

	for name, url := range map[string]string{"частями": withRange.URL, "одним потоком": noRange.URL} {
		last := 0
		path, err := downloadVerified(url, want, func(p int) { last = p })
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, _ := os.ReadFile(path)
		os.Remove(path)
		if !bytes.Equal(got, data) {
			t.Errorf("%s: файл не совпал", name)
		}
		if last != 100 {
			t.Errorf("%s: прогресс закончился на %d%%", name, last)
		}
	}

	if _, err := downloadVerified(withRange.URL, hex.EncodeToString(make([]byte, 32)), func(int) {}); err == nil {
		t.Error("при неверной сумме файл не должен ставиться")
	}
}

// Живая проверка скорости с GitHub: UWDT_LIVE_UPDATE_URL и UWDT_LIVE_UPDATE_SHA
func TestDownloadVerifiedLive(t *testing.T) {
	url, sha := os.Getenv("UWDT_LIVE_UPDATE_URL"), os.Getenv("UWDT_LIVE_UPDATE_SHA")
	if url == "" {
		t.Skip("UWDT_LIVE_UPDATE_URL не задан")
	}
	start := time.Now()
	path, err := downloadVerified(url, sha, func(int) {})
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(path)
	t.Logf("скачано и проверено за %s", time.Since(start).Round(time.Second))
}
