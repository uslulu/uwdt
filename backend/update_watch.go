package backend

// Проверка обновлений живёт в Go, а не в таймере страницы: UWDT почти всё
// время свёрнут в трей, и macOS для скрытого окна замораживает таймеры JS —
// приложение, запущенное до выхода релиза, так его и не видело.

import (
	"log"
	"time"
)

const (
	updateEvery    = time.Hour
	updateTick     = 10 * time.Minute // во сне тикер стоит: после пробуждения проверка не ждёт час
	updateRemind   = 24 * time.Hour   // закрытое окно обновления показываем снова через сутки
	updateFirstGap = 15 * time.Second // при запуске — когда сеть и туннель успеют подняться
)

func (a *App) watchUpdates() {
	time.Sleep(updateFirstGap)
	var last, shownAt time.Time
	var shown string
	check := func() {
		last = wallNow()
		info, err := CheckUpdate(Version)
		if err != nil {
			log.Printf("[UPDATE] Проверка не удалась: %v", err)
			return
		}
		if !info.Available {
			return
		}
		if info.Version == shown && wallNow().Sub(shownAt) < updateRemind {
			return
		}
		shown, shownAt = info.Version, wallNow()
		a.onBridgeEvent("update_available", info)
	}
	check()
	t := time.NewTicker(updateTick)
	defer t.Stop()
	for range t.C {
		if wallNow().Sub(last) >= updateEvery {
			check()
		}
	}
}
