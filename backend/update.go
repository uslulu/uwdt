package backend

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"runtime"
	"strings"
	"time"
)

// UpdateRepo — откуда UWDT берёт обновления (релизы GitHub).
const UpdateRepo = "uslulu/uwdt"

// UpdateInfo — информация о доступном обновлении.
type UpdateInfo struct {
	Available bool   `json:"available"` // есть ли обновление
	Version   string `json:"version"`   // версия обновления
	URL       string `json:"url"`       // ссылка на скачивание сборки под эту систему
	Body      string `json:"body"`      // описание изменений (changelog)
	Page      string `json:"page"`      // страница релиза
	SHA256    string `json:"-"`         // контрольная сумма сборки от GitHub
}

type ghRelease struct {
	TagName string `json:"tag_name"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		BrowserDownloadURL string `json:"browser_download_url"`
		Name               string `json:"name"`
		Digest             string `json:"digest"` // "sha256:…"
	} `json:"assets"`
}

// assetMarker — какой файл релиза подходит этой системе.
func assetMarker() string {
	switch runtime.GOOS {
	case "darwin":
		return "macos-" + runtime.GOARCH
	case "windows":
		return "windows-" + runtime.GOARCH
	}
	return runtime.GOOS + "-" + runtime.GOARCH
}

// CheckUpdate проверяет наличие обновлений на GitHub (UpdateRepo).
// Возвращает ошибку если сеть недоступна или API недоступен.
func CheckUpdate(currentVersion string) (*UpdateInfo, error) {
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/" + UpdateRepo + "/releases/latest")
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("github api status: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	var release ghRelease
	if err := json.Unmarshal(body, &release); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	latest := strings.TrimPrefix(release.TagName, "v")
	log.Printf("[UPDATE] Release: %s, Current: %s, isNewer: %v", latest, currentVersion, isNewer(latest, currentVersion))
	if !isNewer(latest, currentVersion) {
		return &UpdateInfo{Available: false}, nil
	}

	info := &UpdateInfo{Available: true, Version: latest, Body: release.Body, Page: release.HTMLURL}
	marker := assetMarker()
	for _, a := range release.Assets {
		if strings.Contains(strings.ToLower(a.Name), marker) && strings.HasSuffix(strings.ToLower(a.Name), ".zip") {
			info.URL = a.BrowserDownloadURL
			info.SHA256 = strings.TrimPrefix(strings.ToLower(a.Digest), "sha256:")
			break
		}
	}
	return info, nil
}

func isNewer(a, b string) bool {
	av := parseVersion(a)
	bv := parseVersion(b)
	for i := 0; i < 3; i++ {
		if av[i] > bv[i] {
			return true
		}
		if av[i] < bv[i] {
			return false
		}
	}
	return false
}

func parseVersion(v string) [3]int {
	var result [3]int
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	for i, p := range parts {
		fmt.Sscanf(p, "%d", &result[i])
	}
	return result
}
