package core

import (
	"strconv"
	"sync/atomic"
	"time"
)

type Stats struct {
	TotalBytesUp      atomic.Int64
	TotalBytesDown    atomic.Int64
	ActiveConnections atomic.Int32
}

func NewStats() *Stats {
	return &Stats{}
}

// RunLoop отправляет статистику каждые 3 секунды.
// onLog вызывается для лог-сообщений, onStats — для статистики.
func (s *Stats) RunLoop(shutdown <-chan struct{},
	onLog func(level, msg string),
	onStats func(rx, tx int64, workers int32)) {

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-shutdown:
			return
		case <-ticker.C:
			active := s.ActiveConnections.Load()
			up := s.TotalBytesUp.Load()
			down := s.TotalBytesDown.Load()
			totalMB := float64(up+down) / (1024.0 * 1024.0)

			if onLog != nil {
				onLog("INFO", "[STATS] Активных: "+itoa(int(active))+", Трафик: "+ftoa(totalMB)+" МБ")
			}
			if onStats != nil {
				onStats(up, down, active)
			}
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

// ftoa — два знака после точки с округлением (раньше 0.08 печаталось как «0.8»,
// а 7.21 — как «7.20», и в [STATS] появлялись ложные скачки трафика)
func ftoa(f float64) string {
	return strconv.FormatFloat(f, 'f', 2, 64)
}
