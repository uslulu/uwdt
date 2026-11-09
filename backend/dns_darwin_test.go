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

func TestVPNDNSSupplementalPolicy(t *testing.T) {
	policy := `DNS configuration
resolver #1
search domain[0] : corp.example
nameserver[0] : 10.0.0.2
flags : Request A records
resolver #2
domain : corp.example
nameserver[0] : 10.0.0.2
flags : Supplemental, Request A records
DNS configuration (for scoped queries)
resolver #1
nameserver[0] : 10.0.0.3
flags : Scoped, Request A records
`
	if !vpnDNSHasSupplementalZones(policy, []string{"10.0.0.2"}) {
		t.Fatal("specific VPN zone should permit default override")
	}
	if vpnDNSHasSupplementalZones(policy, []string{"10.0.0.2", "10.0.0.3"}) {
		t.Fatal("scoped resolver must not count as suffix coverage")
	}
	if vpnDNSHasSupplementalZones("resolver #1\nsearch domain[0] : corp.example\nnameserver[0] : 10.0.0.2\nflags : Request A records", []string{"10.0.0.2"}) {
		t.Fatal("search domains alone are insufficient")
	}
	if vpnDNSHasSupplementalZones("resolver #1\ndomain : .\nnameserver[0] : 10.0.0.2\nflags : Supplemental", []string{"10.0.0.2"}) {
		t.Fatal("catch-all VPN resolver must remain protected")
	}
}
