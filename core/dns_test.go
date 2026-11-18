package core

import (
	"net"
	"testing"
)

func TestCoreStartKeepsDefaultResolver(t *testing.T) {
	original := net.DefaultResolver
	c := New(Config{})
	if _, err := c.Start(); err == nil {
		t.Fatal("expected invalid config to fail")
	}
	if net.DefaultResolver != original {
		net.DefaultResolver = original
		t.Fatal("Core.Start replaced the process DNS resolver")
	}
}
