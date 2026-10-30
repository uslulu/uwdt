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
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Body       string `json:"body"`
	HTMLURL    string `json:"html_url"`
	Assets     []struct {
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

// CheckUpdate ищет самый новый стабильный релиз с архивом для текущей платформы.
func CheckUpdate(currentVersion string) (*UpdateInfo, error) {
	return checkUpdate(&http.Client{Timeout: 8 * time.Second}, "https://api.github.com/repos/"+UpdateRepo+"/releases", currentVersion, assetMarker())
}

func checkUpdate(client *http.Client, releasesURL, currentVersion, marker string) (*UpdateInfo, error) {
	var best *UpdateInfo
	// GitHub возвращает релизы в порядке создания, а не обязательно номера версии.
	// Просматриваем все страницы, чтобы не пропустить релиз для своей платформы.
	for page := 1; ; page++ {
		resp, err := client.Get(fmt.Sprintf("%s?per_page=100&page=%d", releasesURL, page))
		if err != nil {
			return nil, fmt.Errorf("network error: %w", err)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("github api status: %d", resp.StatusCode)
		}
		var releases []ghRelease
		if err := json.Unmarshal(body, &releases); err != nil {
			return nil, fmt.Errorf("parse response: %w", err)
		}
		for _, release := range releases {
			if release.Draft || release.Prerelease {
				continue
			}
			version := strings.TrimPrefix(release.TagName, "v")
			if !validReleaseVersion(version) || !isNewer(version, currentVersion) || (best != nil && !isNewer(version, best.Version)) {
				continue
			}
			for _, a := range release.Assets {
				name := strings.ToLower(a.Name)
				if strings.Contains(name, marker) && strings.HasSuffix(name, ".zip") && a.BrowserDownloadURL != "" {
					best = &UpdateInfo{Available: true, Version: version, Body: release.Body, Page: release.HTMLURL, URL: a.BrowserDownloadURL, SHA256: strings.TrimPrefix(strings.ToLower(a.Digest), "sha256:")}
					break
				}
			}
		}
		if len(releases) < 100 {
			break
		}
	}
	if best == nil {
		return &UpdateInfo{Available: false}, nil
	}
	log.Printf("[UPDATE] Available: %s, Current: %s", best.Version, currentVersion)
	return best, nil
}

// Принимаем только обычные версии x.y.z; тестовые теги не становятся обновлением.
func validReleaseVersion(v string) bool {
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, c := range part {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
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
