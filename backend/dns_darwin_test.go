package backend

import "testing"

func TestParseServiceForDevice(t *testing.T) {
	out := `An asterisk (*) denotes that a network service is disabled.
(1) USB 10/100 LAN
(Hardware Port: USB 10/100 LAN, Device: en7)

(2) Thunderbolt Bridge
(Hardware Port: Thunderbolt Bridge, Device: bridge0)

(3) Wi-Fi
(Hardware Port: Wi-Fi, Device: en0)

(*) Old VPN
(Hardware Port: L2TP, Device: )
`
	if got, err := parseServiceForDevice(out, "en0"); err != nil || got != "Wi-Fi" {
		t.Errorf("en0 → %q, %v; want Wi-Fi", got, err)
	}
	if got, err := parseServiceForDevice(out, "en7"); err != nil || got != "USB 10/100 LAN" {
		t.Errorf("en7 → %q, %v", got, err)
	}
	if _, err := parseServiceForDevice(out, "en9"); err == nil {
		t.Error("для неизвестного устройства нужна ошибка")
	}
}
