package backend

import (
	"context"
	"net"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Время ответа в выводе ping: "time=40.1 ms", "time<1ms", "время=32мс"
var rePingTime = regexp.MustCompile(`(?i)(?:time|время)[=<]\s*([\d.,]+)\s*(?:ms|мс)`)

// PingHost меряет задержку до сервера системным ping (ICMP без прав администратора).
// host — "IP" или "IP:порт"; -1 — не ответил.
func (a *App) PingHost(host string) int {
	h := strings.TrimSpace(host)
	if hp, _, err := net.SplitHostPort(h); err == nil {
		h = hp
	}
	if ip := net.ParseIP(h); ip == nil || ip.To4() == nil {
		return -1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "ping", "-n", "1", "-w", "2000", h)
	} else {
		cmd = exec.CommandContext(ctx, "ping", "-c", "1", "-W", "2000", h)
	}
	hideWindow(cmd)
	out, _ := cmd.Output()
	m := rePingTime.FindSubmatch(out)
	if len(m) < 2 {
		return -1
	}
	ms, err := strconv.ParseFloat(strings.ReplaceAll(string(m[1]), ",", "."), 64)
	if err != nil {
		return -1
	}
	if ms < 1 {
		return 1
	}
	return int(ms + 0.5)
}
