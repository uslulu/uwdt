package backend

// Движок Xray для серверов из подписки (VLESS + Reality, xHTTP, gRPC).
// Xray работает внутри приложения и читает пакеты прямо из нашего TUN:
// интерфейс создаёт тот же root-помощник, что и для WireGuard, и отдаёт его
// дескриптор, а Xray берёт его из переменной xray.tun.fd. Маршруты (туннель,
// исключения, «Россия напрямую») остаются нашими.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	xlog "github.com/xtls/xray-core/common/log"
	core "github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/inbound"
	"github.com/xtls/xray-core/features/stats"
	"github.com/xtls/xray-core/infra/conf/serial"
	_ "github.com/xtls/xray-core/main/distro/all"
	"github.com/xtls/xray-core/proxy"
)

const (
	xrayTunFdEnv = "xray.tun.fd"
	xrayMTU      = 1400
)

// xrayStartMu: дескриптор передаётся через переменную окружения процесса —
// два запуска одновременно перепутали бы интерфейсы
var xrayStartMu sync.Mutex

// xrayTunName — имя TUN-входа Xray. На macOS имя не используется (интерфейс
// приходит через xray.tun.fd), на Windows Xray создаёт адаптер Wintun с этим именем.
func xrayTunName() string {
	if runtime.GOOS == "windows" {
		return "uwdt-xray"
	}
	return "utun99"
}

// BuildXrayConfig — JSON-конфиг Xray: вход из TUN, выход — сервер из ссылки vless://.
func BuildXrayConfig(link string, mtu int) ([]byte, error) {
	return buildXrayConfig(link, mtu, xrayTunName())
}

// tunName "" — без входа TUN: для проверки ссылки, Xray при этом не создаёт интерфейс
func buildXrayConfig(link string, mtu int, tunName string) ([]byte, error) {
	v, err := ParseVLESS(link)
	if err != nil {
		return nil, err
	}
	stream, err := v.streamSettings()
	if err != nil {
		return nil, err
	}
	user := map[string]any{
		"address":    v.Host,
		"port":       v.Port,
		"id":         v.ID,
		"encryption": v.param("encryption", "none"),
	}
	if flow := v.Params.Get("flow"); flow != "" {
		user["flow"] = flow
	}
	cfg := map[string]any{
		"log":      map[string]any{"loglevel": "warning", "access": "none"},
		"inbounds": []any{},
		"outbounds": []any{
			map[string]any{"tag": "proxy", "protocol": "vless", "settings": user, "streamSettings": stream},
			map[string]any{"tag": "direct", "protocol": "freedom"},
			map[string]any{"tag": "block", "protocol": "blackhole"},
		},
		// Торренты на сервер подписки не пускаем: раздачи отслеживают, и жалобы
		// правообладателей уходят хостеру сервера — он его отключит
		"routing": map[string]any{"rules": []any{
			map[string]any{"type": "field", "protocol": []string{"bittorrent"}, "outboundTag": "block"},
		}},
		// Счётчики трафика сервера — для карточки скорости
		"stats":  map[string]any{},
		"policy": map[string]any{"system": map[string]any{"statsOutboundUplink": true, "statsOutboundDownlink": true}},
	}
	if tunName != "" {
		cfg["inbounds"] = []any{map[string]any{
			"tag":      "tun",
			"port":     0,
			"protocol": "tun",
			"settings": map[string]any{"name": tunName, "MTU": mtu},
			// Домен из TLS/HTTP — Xray видит, куда идёт соединение, а не только IP
			"sniffing": map[string]any{"enabled": true, "destOverride": []string{"http", "tls", "quic", "bittorrent"}, "routeOnly": true},
		}}
	}
	return json.MarshalIndent(cfg, "", "  ")
}

func (v *VLESS) streamSettings() (map[string]any, error) {
	network := v.param("type", "tcp")
	s := map[string]any{"network": network}
	switch network {
	case "tcp", "raw":
		s["network"] = "tcp"
	case "grpc":
		s["grpcSettings"] = map[string]any{
			"serviceName": v.Params.Get("serviceName"),
			"authority":   v.Params.Get("authority"),
			"multiMode":   v.Params.Get("mode") == "multi",
		}
	case "xhttp", "splithttp":
		s["network"] = "xhttp"
		x := map[string]any{"path": v.param("path", "/"), "host": v.Params.Get("host"), "mode": v.param("mode", "auto")}
		if extra := v.Params.Get("extra"); extra != "" && json.Valid([]byte(extra)) {
			x["extra"] = json.RawMessage(extra)
		}
		s["xhttpSettings"] = x
	case "ws":
		s["wsSettings"] = map[string]any{"path": v.param("path", "/"), "host": v.Params.Get("host")}
	default:
		return nil, fmt.Errorf("транспорт %q пока не поддерживается", network)
	}

	switch sec := v.param("security", "none"); sec {
	case "reality":
		s["security"] = "reality"
		s["realitySettings"] = map[string]any{
			"serverName":  v.Params.Get("sni"),
			"fingerprint": v.param("fp", "chrome"),
			"publicKey":   v.Params.Get("pbk"),
			"shortId":     v.Params.Get("sid"),
			"spiderX":     v.Params.Get("spx"),
		}
	case "tls":
		s["security"] = "tls"
		t := map[string]any{"serverName": v.param("sni", v.Host), "fingerprint": v.param("fp", "chrome")}
		if alpn := v.Params.Get("alpn"); alpn != "" {
			t["alpn"] = strings.Split(alpn, ",")
		}
		s["tlsSettings"] = t
	case "none", "":
	default:
		return nil, fmt.Errorf("защита %q пока не поддерживается", sec)
	}
	return s, nil
}

// xrayInstance — запущенный Xray; Close останавливает его (дескриптор TUN закрывает вызывающий).
type xrayInstance struct {
	inst  *core.Instance
	tunIn any // TUN-вход (*tun.Handler): его стек и интерфейс закрываем сами
}

// Остановленные экземпляры держим до конца работы приложения. Xray оборачивает
// переданный fd в свой os.File и не закрывает его (fd наш), а финализатор этого
// File при сборке мусора закрыл бы номер fd ещё раз — когда он уже может
// принадлежать другому файлу. Экземпляр после Close занимает немного, а
// переподключаются нечасто.
var (
	retiredMu   sync.Mutex
	retiredXray []*core.Instance
)

func (x *xrayInstance) Close() error {
	err := x.inst.Close()
	// xray-core v1.260327.0: у TUN-входа нет Close, а inst.Close закрывает
	// только workers и mux — адаптер, его маршруты и стек gVisor оставались
	// жить, трафик шёл в «зомби», следующий запуск зависал на занятом адаптере
	if x.tunIn != nil {
		if cerr := closeTunInbound(x.tunIn); cerr != nil && err == nil {
			err = cerr
		}
	}
	setXrayLog(discardLog{}) // записи остановленного Xray не должны попадать в чужую сессию
	retiredMu.Lock()
	retiredXray = append(retiredXray, x.inst)
	retiredMu.Unlock()
	return err
}

// Traffic — байты, переданные на сервер и полученные от него с начала сессии.
func (x *xrayInstance) Traffic() (up, down int64) {
	m, ok := x.inst.GetFeature(stats.ManagerType()).(stats.Manager)
	if !ok {
		return 0, 0
	}
	if c := m.GetCounter("outbound>>>proxy>>>traffic>>>uplink"); c != nil {
		up = c.Value()
	}
	if c := m.GetCounter("outbound>>>proxy>>>traffic>>>downlink"); c != nil {
		down = c.Value()
	}
	return up, down
}

// tunInbound — TUN-вход Xray (*tun.Handler) по тегу «tun».
func tunInbound(inst *core.Instance) any {
	m, ok := inst.GetFeature(inbound.ManagerType()).(inbound.Manager)
	if !ok {
		return nil
	}
	h, err := m.GetHandler(context.Background(), "tun")
	if err != nil {
		return nil
	}
	if gi, ok := h.(proxy.GetInbound); ok {
		return gi.GetInbound()
	}
	return nil
}

// closeTunInbound закрывает стек gVisor и интерфейс TUN-входа. Поля
// неэкспортируемые (Handler.stack, stackGVisor.tun) — достаём через reflect;
// версия xray-core закреплена в go.mod, имена полей проверяет тест.
func closeTunInbound(h any) error {
	st, err := unexportedField(h, "stack")
	if err != nil {
		return err
	}
	var errs []string
	tunIf, terr := unexportedField(st, "tun")
	if c, ok := st.(io.Closer); ok {
		if err := c.Close(); err != nil {
			errs = append(errs, "стек: "+err.Error())
		}
	}
	if terr != nil {
		errs = append(errs, terr.Error())
	} else if c, ok := tunIf.(io.Closer); ok {
		if err := c.Close(); err != nil {
			errs = append(errs, "интерфейс: "+err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("закрытие TUN Xray: %s", strings.Join(errs, "; "))
	}
	return nil
}

func unexportedField(obj any, name string) (any, error) {
	v := reflect.ValueOf(obj)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return nil, fmt.Errorf("закрытие TUN Xray: %T — не указатель", obj)
	}
	f := v.Elem().FieldByName(name)
	if !f.IsValid() {
		return nil, fmt.Errorf("закрытие TUN Xray: у %T нет поля %s (сменилась версия xray-core?)", obj, name)
	}
	return reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Interface(), nil
}

// startXray запускает Xray с сервером из ссылки. tunFd — уже созданный TUN
// (macOS); 0 — Xray сам создаёт адаптер tunName (Windows).
func startXray(link string, tunFd uintptr, tunName string, mtu int, logf wgLogFunc) (*xrayInstance, error) {
	js, err := buildXrayConfig(link, mtu, tunName)
	if err != nil {
		return nil, err
	}
	cfg, err := serial.LoadJSONConfig(bytes.NewReader(js))
	if err != nil {
		return nil, fmt.Errorf("конфиг Xray: %w", err)
	}

	xrayStartMu.Lock()
	defer xrayStartMu.Unlock()
	if tunFd != 0 {
		os.Setenv(xrayTunFdEnv, strconv.Itoa(int(tunFd)))
		defer os.Unsetenv(xrayTunFdEnv)
	}

	inst, err := core.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("Xray: %w", err)
	}
	x := &xrayInstance{inst: inst, tunIn: tunInbound(inst)}
	if err := inst.Start(); err != nil {
		x.Close()
		return nil, fmt.Errorf("запуск Xray: %w", err)
	}
	// Предупреждения и ошибки Xray — в журнал сессии (обработчик ставим после
	// Start: модуль логов Xray при старте заменяет свой)
	setXrayLog(xrayLogHandler{logf})
	return x, nil
}

// Обработчик логов в Xray один на процесс, и каждый Start (в том числе
// проверки пинга) ставит свой — после них возвращаем обработчик сессии.
var (
	xrayLogMu      sync.Mutex
	xrayLogCurrent xlog.Handler = discardLog{}
)

func setXrayLog(h xlog.Handler) {
	xrayLogMu.Lock()
	xrayLogCurrent = h
	xlog.RegisterHandler(h)
	xrayLogMu.Unlock()
}

func restoreXrayLog() {
	xrayLogMu.Lock()
	xlog.RegisterHandler(xrayLogCurrent)
	xrayLogMu.Unlock()
}

// xrayLogHandler пропускает только предупреждения и ошибки. Записи о каждом
// соединении (AccessMessage) идут сотнями в минуту — их не пишем.
type xrayLogHandler struct{ logf wgLogFunc }

func (h xrayLogHandler) Handle(msg xlog.Message) {
	if m, ok := msg.(*xlog.GeneralMessage); ok && m.Severity <= xlog.Severity_Warning {
		h.logf(msg.String())
	}
}

type discardLog struct{}

func (discardLog) Handle(xlog.Message) {}

// CheckXrayLink проверяет, что из ссылки собирается рабочий конфиг Xray.
func CheckXrayLink(link string) error {
	js, err := buildXrayConfig(link, 1400, "")
	if err != nil {
		return err
	}
	cfg, err := serial.LoadJSONConfig(bytes.NewReader(js))
	if err != nil {
		return err
	}
	_, err = core.New(cfg) // собирает все модули, но ничего не запускает
	return err
}
