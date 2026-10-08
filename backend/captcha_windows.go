package backend

// Окно ручной капчи ВК (Windows, WebView2). Тот же протокол, что на macOS:
// отдельный процесс `--captcha-window <url>`, в stdout — "TOKEN <t>" или "CANCEL".

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	webview2 "github.com/jchv/go-webview2"
)

// RunCaptchaWindowWindows — точка входа режима --captcha-window (вызывается из main_windows.go).
func RunCaptchaWindowWindows(args []string) {
	if len(args) < 1 {
		os.Exit(2)
	}
	u, err := url.Parse(args[0])
	if err != nil || u.Scheme != "https" || !isVKCaptchaHost(u.Hostname()) {
		fmt.Println("CANCEL")
		os.Exit(2)
	}

	dataPath := ""
	if dir, err := os.UserCacheDir(); err == nil {
		dataPath = filepath.Join(dir, "UWDT", "captcha-webview")
	}
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath:  dataPath,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  "Капча ВКонтакте — UWDT",
			Width:  440,
			Height: 640,
			Center: true,
		},
	})
	if w == nil {
		fmt.Println("CANCEL")
		os.Exit(1)
	}
	defer w.Destroy()

	done := false
	_ = w.Bind("uwdtToken", func(t string) error {
		if !done && t != "" {
			done = true
			fmt.Println("TOKEN " + t)
			w.Terminate()
		}
		return nil
	})
	w.Init(captchaBridgeJS("window.uwdtToken(t)"))
	w.Navigate(u.String())
	w.Run()
	if !done {
		fmt.Println("CANCEL")
	}
}
