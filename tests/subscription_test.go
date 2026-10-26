package backend_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"pwdtt/backend"
)

// Ключи и адреса вымышленные; набор транспортов — как в реальной подписке
const (
	linkVision = "vless://11111111-2222-3333-4444-555555555555@203.0.113.10:443?type=tcp&security=reality&flow=xtls-rprx-vision&fp=firefox&encryption=none&sni=www.example.com&pbk=bmXOC-gsYBnUzqfQ7J6Hv0Tg8ApEMTx3jJbW6bBV4lA&sid=6ba85179e30d4fc2#%F0%9F%87%A9%F0%9F%87%AA%20%D0%93%D0%B5%D1%80%D0%BC%D0%B0%D0%BD%D0%B8%D1%8F%20-%20Vision"
	linkXHTTP  = "vless://11111111-2222-3333-4444-555555555555@203.0.113.10:443?type=xhttp&security=reality&fp=firefox&encryption=none&mode=auto&path=%2Fxh&sni=www.example.com&pbk=bmXOC-gsYBnUzqfQ7J6Hv0Tg8ApEMTx3jJbW6bBV4lA&sid=6ba85179e30d4fc2#xHTTP"
	linkGRPC   = "vless://11111111-2222-3333-4444-555555555555@vpn.example.net:443?type=grpc&security=reality&fp=firefox&encryption=none&mode=gun&serviceName=grpc&sni=www.example.com&pbk=bmXOC-gsYBnUzqfQ7J6Hv0Tg8ApEMTx3jJbW6bBV4lA&sid=6ba85179e30d4fc2#gRPC"
)

func TestParseSubscription_Base64WithHappLines(t *testing.T) {
	body := "happ://routing/onadd/eyJOYW1lIjoiWCJ9\n#profile-title: uslulu VPN\n" + linkVision + "\n" + linkXHTTP + "\n" + linkGRPC + "\nvmess://abc\n"
	sub := backend.ParseSubscription(base64.StdEncoding.EncodeToString([]byte(body)))
	if sub.Title != "uslulu VPN" {
		t.Errorf("Title = %q", sub.Title)
	}
	if len(sub.Servers) != 3 || sub.Skipped != 1 {
		t.Fatalf("серверов %d, пропущено %d; want 3 и 1", len(sub.Servers), sub.Skipped)
	}
	if s := sub.Servers[0]; s.Name != "🇩🇪 Германия - Vision" || s.Transport != "tcp" || s.Security != "reality" || s.Port != 443 {
		t.Errorf("первый сервер разобран неверно: %+v", s)
	}
	if !strings.HasPrefix(sub.Servers[2].Link, "vless://") || sub.Servers[2].Host != "vpn.example.net" {
		t.Errorf("третий сервер: %+v", sub.Servers[2])
	}
}

func TestParseSubscription_Base64Title(t *testing.T) {
	title := base64.StdEncoding.EncodeToString([]byte("Мой VPN"))
	sub := backend.ParseSubscription("#profile-title: base64:" + title + "\n" + linkVision)
	if sub.Title != "Мой VPN" || len(sub.Servers) != 1 {
		t.Errorf("Title = %q, серверов %d", sub.Title, len(sub.Servers))
	}
}

// Конфиг должен приниматься самим Xray для всех трёх транспортов
func TestCheckXrayLink(t *testing.T) {
	for _, l := range []string{linkVision, linkXHTTP, linkGRPC} {
		if err := backend.CheckXrayLink(l); err != nil {
			t.Errorf("%s: %v", l[strings.Index(l, "#"):], err)
		}
	}
	if err := backend.CheckXrayLink("vless://id@host:443?type=kcp"); err == nil {
		t.Error("неподдерживаемый транспорт должен давать ошибку")
	}
}
