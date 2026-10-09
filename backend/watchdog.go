package backend

// Сторож сессии: после сна мака и при пропаже всех каналов туннель не должен
// забирать трафик в пустоту. Каналов нет дольше deadAfter — маршруты туннеля
// снимаются (интернет напрямую), ядро забывает протухшие креды и баны TURN и
// поднимает каналы заново; каналы вернулись — маршруты туннеля возвращаются.

import (
	"fmt"
	"time"

	core "wg-turn-client"
)

const (
	watchTick     = 5 * time.Second
	deadAfter     = 45 * time.Second
	sleepGap      = 30 * time.Second // разрыв настенных часов между тиками — мак спал
	degradedReset = 2 * time.Minute  // пока связи нет, сбрасываем состояние ядра не чаще
)

type sessionHealth struct {
	tunnelUp     bool
	degraded     bool
	active       int32
	lastActiveAt time.Time
	lastResetAt  time.Time
}

func wallNow() time.Time { return time.Now().Round(0) } // без монотонных часов: они во сне стоят

func (b *Bridge) noteStats(active int32) {
	b.mu.Lock()
	b.health.active = active
	if active > 0 {
		b.health.lastActiveAt = wallNow()
	}
	b.mu.Unlock()
}

func (b *Bridge) noteTunnelUp() {
	b.mu.Lock()
	b.health.tunnelUp = true
	b.health.lastActiveAt = wallNow()
	b.mu.Unlock()
}

func (b *Bridge) watchdog(sessID uint64) {
	t := time.NewTicker(watchTick)
	defer t.Stop()
	last := wallNow()
	for range t.C {
		now := wallNow()
		gap := now.Sub(last)
		last = now

		b.mu.Lock()
		if b.session != sessID || !b.running {
			b.mu.Unlock()
			return
		}
		h := &b.health
		if gap > sleepGap {
			// Проснулись: даём сети и каналам время, ядро — с чистого листа
			h.lastActiveAt = now
			h.lastResetAt = now
			b.mu.Unlock()
			core.ResetSessionState()
			b.log(fmt.Sprintf("[СТОРОЖ] Мак спал %s — обновляю доступ к серверам ВК", gap.Round(time.Minute)))
			continue
		}
		up, deg, act := h.tunnelUp, h.degraded, h.active
		silent := now.Sub(h.lastActiveAt)
		needReset := deg && now.Sub(h.lastResetAt) > degradedReset
		b.mu.Unlock()

		switch {
		case up && !deg && act == 0 && silent > deadAfter:
			if err := wg.SetTunnelRoutes(false); err != nil {
				b.log("[СТОРОЖ] Не удалось снять маршруты туннеля: " + err.Error())
				continue
			}
			core.ResetSessionState()
			b.mu.Lock()
			h.degraded = true
			h.lastResetAt = now
			b.mu.Unlock()
			b.onEvent("degraded", true)
			b.log(fmt.Sprintf("[СТОРОЖ] Нет каналов к серверу %s — интернет пока напрямую, восстанавливаю", silent.Round(time.Second)))
		case deg && act > 0:
			if err := wg.SetTunnelRoutes(true); err != nil {
				b.log("[СТОРОЖ] Не удалось вернуть маршруты туннеля: " + err.Error())
				continue
			}
			b.mu.Lock()
			h.degraded = false
			b.mu.Unlock()
			b.onEvent("degraded", false)
			b.log("[СТОРОЖ] Каналы вернулись — трафик снова через туннель")
		case needReset:
			core.ResetSessionState()
			b.mu.Lock()
			h.lastResetAt = now
			b.mu.Unlock()
		}
	}
}

// log — сообщение и в интерфейс, и в файл сессии.
func (b *Bridge) log(msg string) {
	b.onEvent("log", "INFO", msg)
	b.mu.Lock()
	if b.logFile != nil {
		b.logFile.Write("INFO", msg)
	}
	b.mu.Unlock()
}
