package backend

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPlatformUpdates(t *testing.T) {
	data := `[
 {"tag_name":"v1.9.0","assets":[{"name":"UWDT-macos-arm64.zip","browser_download_url":"https://example/mac","digest":"sha256:abc"}]},
 {"tag_name":"v1.8.8","assets":[{"name":"UWDT-windows-amd64.zip","browser_download_url":"https://example/windows"}]},
 {"tag_name":"v2.0.0","prerelease":true,"assets":[{"name":"UWDT-windows-amd64.zip","browser_download_url":"https://example/test"}]},
 {"tag_name":"v3.0.0-beta","assets":[{"name":"UWDT-windows-amd64.zip","browser_download_url":"https://example/beta"}]}
 ]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, data) }))
	defer srv.Close()
	for _, tc := range []struct {
		marker, current, want string
		available             bool
	}{
		{"macos-arm64", "1.8.6", "1.9.0", true}, {"windows-amd64", "1.8.6", "1.8.8", true}, {"windows-amd64", "1.8.8", "", false},
	} {
		got, err := checkUpdate(srv.Client(), srv.URL, tc.current, tc.marker)
		if err != nil || got.Available != tc.available || got.Version != tc.want {
			t.Fatalf("%s: got %+v, err %v", tc.marker, got, err)
		}
		if got.Available && got.URL == "" {
			t.Fatal("update has no download")
		}
	}
}

func TestPlatformUpdatesPagination(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("page") == "1" {
			fmt.Fprint(w, "["+strings.TrimSuffix(strings.Repeat(`{"tag_name":"v1.9.0","assets":[]},`, 100), ",")+"]")
			return
		}
		fmt.Fprint(w, `[{"tag_name":"v1.8.7","assets":[{"name":"UWDT-windows-amd64.zip","browser_download_url":"https://example/windows"}]}]`)
	}))
	defer srv.Close()
	got, err := checkUpdate(srv.Client(), srv.URL, "1.8.6", "windows-amd64")
	if err != nil || got.Version != "1.8.7" || calls != 2 {
		t.Fatalf("got %+v, calls %d, err %v", got, calls, err)
	}
}
