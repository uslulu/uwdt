package backend

// Движок Xray для серверов из подписки (VLESS + Reality, xHTTP, gRPC).
// Xray работает внутри приложения и читает пакеты прямо из нашего TUN:
// интерфейс создаёт тот же root-помощник, что и для WireGuard, и отдаёт его
// дескриптор, а Xray берёт его из переменной xray.tun.fd. Маршруты (туннель,
// исключения, «Россия напрямую») остаются нашими.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	xlog "github.com/xtls/xray-core/common/log"
	core "github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf/serial"
	_ "github.com/xtls/xray-core/main/distro/all"
)

const xrayTunFdEnv = "xray.tun.fd"

// xrayStartMu: дескриптор передаётся через переменную окружения процесса —
// два запуска одновременно перепутали бы интерфейсы
var xrayStartMu sync.Mutex

// BuildXrayConfig — JSON-конфиг Xray: вход из TUN, выход — сервер из ссылки vless://.
func BuildXrayConfig(link string, mtu int) ([]byte, error) { return buildXrayConfig(link, mtu, true) }

// withTun=false — без входа TUN: для проверки ссылки, Xray при этом не создаёт интерфейс
func buildXrayConfig(link string, mtu int, withTun bool) ([]byte, error) {
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
		},
	}
	if withTun {
		cfg["inbounds"] = []any{map[string]any{
			"tag":      "tun",
			"port":     0,
			"protocol": "tun",
			"settings": map[string]any{"name": "utun99", "MTU": mtu}, // имя не используется: интерфейс приходит через xray.tun.fd
			// Домен из TLS/HTTP — Xray видит, куда идёт соединение, а не только IP
			"sniffing": map[string]any{"enabled": true, "destOverride": []string{"http", "tls", "quic"}, "routeOnly": true},
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
type xrayInstance struct{ inst *core.Instance }

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
	retiredMu.Lock()
	retiredXray = append(retiredXray, x.inst)
	retiredMu.Unlock()
	return err
}

// startXray запускает Xray на уже созданном TUN (fd) с сервером из ссылки.
func startXray(link string, tunFd uintptr, mtu int, logf wgLogFunc) (*xrayInstance, error) {
	js, err := BuildXrayConfig(link, mtu)
	if err != nil {
		return nil, err
	}
	cfg, err := serial.LoadJSONConfig(bytes.NewReader(js))
	if err != nil {
		return nil, fmt.Errorf("конфиг Xray: %w", err)
	}

	xrayStartMu.Lock()
	defer xrayStartMu.Unlock()
	os.Setenv(xrayTunFdEnv, strconv.Itoa(int(tunFd)))
	defer os.Unsetenv(xrayTunFdEnv)

	inst, err := core.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("Xray: %w", err)
	}
	if err := inst.Start(); err != nil {
		(&xrayInstance{inst}).Close()
		return nil, fmt.Errorf("запуск Xray: %w", err)
	}
	// Предупреждения и ошибки Xray — в журнал сессии (обработчик ставим после
	// Start: модуль логов Xray при старте заменяет свой)
	xlog.RegisterHandler(xrayLogHandler{logf})
	return &xrayInstance{inst}, nil
}

type xrayLogHandler struct{ logf wgLogFunc }

func (h xrayLogHandler) Handle(msg xlog.Message) {
	if m, ok := msg.(*xlog.GeneralMessage); ok && m.Severity > xlog.Severity_Warning {
		return // info и debug не пишем
	}
	h.logf("[XRAY] " + msg.String())
}

// CheckXrayLink проверяет, что из ссылки собирается рабочий конфиг Xray.
func CheckXrayLink(link string) error {
	js, err := buildXrayConfig(link, 1400, false)
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
