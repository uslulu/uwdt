package backend

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	core "wg-turn-client"
)

// Bridge — мост между App.go и ядром.
type Bridge struct {
	applyXray       func(string, []string, wgLogFunc) error
	ctx             context.Context
	store           *Store
	onEvent         func(name string, args ...any)
	lifecycle       sync.Mutex
	mu              sync.Mutex
	stopping        bool
	sessionWorkDone <-chan struct{}
	cancelSession   context.CancelFunc
	core            *core.Core
	running         bool
	logFile         *LogFile // полный лог сессии
	stage           stageTracker
	health          sessionHealth
	session         uint64 // номер сессии; stale forwardEvents не должен трогать новую
	xray            bool   // текущая сессия — сервер из подписки (Xray), без ядра VK TURN
}

func NewBridge(ctx context.Context, store *Store, onEvent func(string, ...any)) *Bridge {
	return &Bridge{applyXray: wg.ApplyXray, ctx: ctx, store: store, onEvent: onEvent}
}

// Connect подключается к серверу.
func (b *Bridge) Connect(params ConnectParams) error {
	b.lifecycle.Lock()
	defer b.lifecycle.Unlock()

	b.mu.Lock()
	if b.running {
		b.mu.Unlock()
		log.Printf("[BRIDGE] Connect rejected: already running")
		return fmt.Errorf("already running")
	}
	// Сразу помечаем как running чтобы заблокировать параллельные вызовы
	b.running = true
	b.session++
	b.stage = stageTracker{}
	b.health = sessionHealth{}
	sessID := b.session
	b.mu.Unlock()

	// resetRunning сбрасывает флаг при любой ошибке до запуска forwardEvents
	resetRunning := func() {
		b.mu.Lock()
		b.running = false
		b.mu.Unlock()
	}

	hashes := params.Hashes
	if len(hashes) == 0 {
		resetRunning()
		return fmt.Errorf("нет хешей VK")
	}

	workers := params.Workers
	if workers <= 0 {
		workers = 24
	}

	deviceID := params.DeviceID
	if deviceID == "" {
		deviceID = b.store.DeviceID()
	}

	cfg := core.Config{
		PeerAddr:    params.PeerAddr,
		Password:    params.Password,
		Hashes:      hashes,
		DeviceID:    deviceID,
		Workers:     workers,
		CaptchaMode: params.CaptchaMode,
		ObfsMode:    params.ObfsMode,
		Fingerprint: params.Fingerprint,
		TurnTCP:     params.TurnTCP,
	}

	// Новое подключение — с чистого листа: без прошлых банов TURN и протухших кредов
	core.ResetSessionState()
	c := core.New(cfg)

	b.mu.Lock()
	b.core = c
	b.logFile = b.store.OpenLogFile()
	b.mu.Unlock()

	events, err := c.Start()
	if err != nil {
		log.Printf("[BRIDGE] core start failed: %v", err)
		c.Stop() // на всякий случай гасим контекст ядра
		b.mu.Lock()
		b.core = nil
		file := b.logFile
		b.logFile = nil
		b.running = false
		b.mu.Unlock()
		file.Close()
		return fmt.Errorf("core start: %w", err)
	}

	done := make(chan struct{})
	b.sessionWorkDone = done
	ctx, cancel := context.WithCancel(b.ctx)
	b.cancelSession = cancel
	eventsDone := make(chan struct{})
	go func() {
		defer close(eventsDone)
		defer cancel()
		b.forwardEvents(ctx, events, sessID)
	}()
	go func() {
		defer close(done)
		b.watchdog(sessID, eventsDone)
		<-eventsDone
	}()
	return nil
}

func (b *Bridge) Disconnect() error {
	b.lifecycle.Lock()
	defer b.lifecycle.Unlock()

	b.mu.Lock()
	c, xray, sess := b.core, b.xray, b.session
	b.stopping = true
	b.health = sessionHealth{}
	b.mu.Unlock()
	b.onEvent("degraded", false)
	if b.cancelSession != nil {
		b.cancelSession()
		b.cancelSession = nil
	}

	if c != nil {
		c.Stop()
	}
	// Завершаем обработчики сессии до снятия туннеля: они ещё могут применять конфиг.
	if b.sessionWorkDone != nil {
		<-b.sessionWorkDone
		b.sessionWorkDone = nil
	}
	cleanupErr := wg.Teardown()
	if xray {
		b.finishXray(sess)
	}

	b.mu.Lock()
	b.stopping = false
	b.mu.Unlock()
	if cleanupErr != nil {
		wg.setRuleStatus("Ошибка восстановления сети: " + cleanupErr.Error())
	}
	return cleanupErr
}

// ConnectXray подключает к серверу из подписки (ссылка vless://) через Xray.
// Ядро VK TURN при этом не запускается; маршруты и исключения — те же.
func (b *Bridge) ConnectXray(link, name string) error {
	v, err := ParseVLESS(link)
	if err != nil {
		return err
	}
	b.lifecycle.Lock()
	defer b.lifecycle.Unlock()

	b.mu.Lock()
	if b.running {
		b.mu.Unlock()
		return fmt.Errorf("already running")
	}
	b.running = true
	b.xray = true
	b.session++
	b.stage = stageTracker{}
	b.health = sessionHealth{}
	sessID := b.session
	b.logFile = b.store.OpenLogFile()
	b.mu.Unlock()

	b.onEvent("state_changed", "connecting")
	logf := func(msg string) {
		b.mu.Lock()
		if b.session != sessID || b.stopping {
			b.mu.Unlock()
			return
		}
		file := b.logFile
		stage, moved := b.stage.feed(msg)
		b.mu.Unlock()
		b.onEvent("log", "INFO", "[XRAY] "+msg)
		file.Write("INFO", "[XRAY] "+msg)
		if moved {
			b.onEvent("stage", stage)
		}
	}

	ctx, cancel := context.WithCancel(b.ctx)
	b.cancelSession = cancel
	done := make(chan struct{})
	b.sessionWorkDone = done
	go func() {
		defer close(done)
		defer cancel()
		logf(fmt.Sprintf("Сервер из подписки: %s (%s, %s)", name, v.param("type", "tcp"), v.param("security", "none")))
		ips := resolveServerIPv4(v.Host)
		if len(ips) == 0 {
			b.onEvent("error", "[XRAY] Не удалось узнать адрес сервера "+v.Host)
			b.finishXray(sessID)
			return
		}
		b.mu.Lock()
		stopping := b.stopping
		b.mu.Unlock()
		if stopping {
			return
		}
		err := b.applyXray(link, ips, logf)

		b.mu.Lock()
		stopping = b.stopping
		b.mu.Unlock()
		switch {
		case err != nil:
			b.onEvent("error", fmt.Sprintf("[XRAY] Ошибка: %v", err))
			b.finishXray(sessID)
		case stopping:
			// Disconnect снимет туннель после завершения этой горутины.
		default:
			logf("Туннель активен")
			b.onEvent("state_changed", "connected")
			b.xrayStats(ctx, sessID)
		}
	}()
	return nil
}

// xrayStats раз в 3 с шлёт трафик сервера подписки в карточку скорости (как ядро VK TURN).
func (b *Bridge) xrayStats(ctx context.Context, sessID uint64) {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		b.mu.Lock()
		current := b.session == sessID && b.xray && b.running && !b.stopping
		b.mu.Unlock()
		if !current {
			return
		}
		up, down, ok := wg.XrayTraffic()
		if !ok {
			return
		}
		b.onEvent("stats", map[string]any{"active": 1, "bytes_up": up, "bytes_down": down})
	}
}

// finishXray закрывает сессию Xray, если она всё ещё текущая.
func (b *Bridge) finishXray(sessID uint64) {
	b.mu.Lock()
	if b.session != sessID || !b.xray || !b.running {
		b.mu.Unlock()
		return
	}
	b.running = false
	b.xray = false
	file := b.logFile
	b.logFile = nil
	b.mu.Unlock()
	file.Close()
	b.onEvent("state_changed", "disconnected")
}

// resolveServerIPv4 — IPv4-адреса сервера (сам адрес, если это уже IP).
func resolveServerIPv4(host string) []string {
	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() != nil {
			return []string{ip.String()}
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		if a.IP.To4() != nil {
			out = append(out, a.IP.String())
		}
	}
	return out
}

func (b *Bridge) IsRunning() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.running
}

func (b *Bridge) SendCaptchaResult(token string) {
	b.mu.Lock()
	c := b.core
	b.mu.Unlock()
	if c != nil {
		c.SolveCaptcha(token)
	}
}

// forwardEvents читает канал событий ядра и пробрасывает в Wails.
// sessID — поколение сессии, на которую запущен этот горутин: если к моменту
// его завершения уже начата новая сессия (быстрый Disconnect → Connect),
// устаревший горутин не имеет права трогать состояние Bridge.
func (b *Bridge) forwardEvents(parent context.Context, events <-chan core.Event, sessID uint64) {
	ctx, cancel := context.WithCancel(parent)
	var captcha sync.WaitGroup
	defer func() { cancel(); captcha.Wait() }()
	var routeSnapshotAt time.Time
	var applyErr error
	for ev := range events {
		b.mu.Lock()
		stale := b.session != sessID || b.stopping
		b.mu.Unlock()
		if stale {
			continue // только осушаем канал, без побочных эффектов
		}

		switch ev.Type {
		case core.EventState:
			b.onEvent("state_changed", ev.Status)

		case core.EventLog:
			if time.Since(routeSnapshotAt) >= time.Minute &&
				(strings.Contains(ev.Message, "network is unreachable") || strings.Contains(ev.Message, "can't assign requested address")) {
				wg.logRoutesDarwin()
				routeSnapshotAt = time.Now()
			}
			// Пишем ВСЁ в файл (включая отфильтрованное для UI)
			b.mu.Lock()
			file := b.logFile
			stage, moved := b.stage.feed(ev.Message)
			b.mu.Unlock()
			file.Write(ev.Level, ev.Message)
			if moved {
				b.onEvent("stage", stage)
			}
			// В UI — только отфильтрованное
			if ev.Level != "SKIP" {
				b.onEvent("log", ev.Level, ev.Message)
			}

		case core.EventStats:
			b.noteStats(ev.Active)
			b.onEvent("stats", map[string]any{
				"active":     ev.Active,
				"bytes_up":   ev.BytesUp,
				"bytes_down": ev.BytesDown,
			})

		case core.EventError:
			b.onEvent("error", ev.Message)

		case core.EventEvent:
			switch ev.Name {
			case "wg_config":
				b.onEvent("log", "INFO", "[WG] Применение конфига...")
				wgLogf := func(msg string) {
					b.mu.Lock()
					if b.session != sessID || b.stopping {
						b.mu.Unlock()
						return
					}
					file := b.logFile
					stage, moved := b.stage.feed(msg)
					b.mu.Unlock()
					b.onEvent("log", "INFO", "[WG] "+msg)
					file.Write("INFO", "[WG] "+msg)
					if moved {
						b.onEvent("stage", stage)
					}
				}
				if err := wg.Apply(ev.Data, ev.TurnIPs, wgLogf); err != nil {
					applyErr = err
					b.onEvent("error", fmt.Sprintf("[WG] Ошибка: %v", err))
					b.mu.Lock()
					c := b.core
					b.mu.Unlock()
					if c != nil {
						c.Stop()
					}
				} else {
					b.noteTunnelUp()
					b.onEvent("log", "INFO", "[WG] Конфиг применён, туннель активен")
					b.onEvent("state_changed", "connected")
				}
			case "captcha_required":
				b.onEvent("captcha_required", ev.Data)
				data := ev.Data
				captcha.Go(func() { b.handleCaptchaRequest(ctx, sessID, data) })
			case "ready":
				b.onEvent("log", "INFO", "[ЯДРО] Туннель готов к работе")
			default:
				b.onEvent("event", ev.Name)
			}
		}
	}

	b.mu.Lock()
	cleanup := b.session == sessID && !b.stopping
	b.mu.Unlock()
	if cleanup {
		if err := wg.Teardown(); err != nil {
			b.onEvent("error", err.Error())
			wg.setRuleStatus("Ошибка восстановления сети: " + err.Error())
		} else if applyErr != nil {
			wg.setRuleStatus("Ошибка подключения: " + applyErr.Error())
		}
	}

	b.mu.Lock()
	current := b.session == sessID
	curSession := b.session
	var file *LogFile
	if current {
		b.running = false
		b.core = nil
		file = b.logFile
		b.logFile = nil
	}
	b.mu.Unlock()
	file.Close()

	if !current {
		log.Printf("[BRIDGE] Завершение устаревшей сессии проигнорировано (актуальна #%d)", curSession)
		return
	}

	b.onEvent("state_changed", "disconnected")
	log.Printf("[BRIDGE] Ядро завершилось")
}

func (b *Bridge) flushLog() error {
	b.mu.Lock()
	file := b.logFile
	b.mu.Unlock()
	return file.Flush()
}
