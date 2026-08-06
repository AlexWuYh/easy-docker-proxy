package acl

import (
	"testing"

	"github.com/alex_wuyh/easy-docker-proxy/internal/config"
)

func TestWhitelist(t *testing.T) {
	m := Build(&config.AccessControl{
		Mode:      config.ACLModeWhitelist,
		Whitelist: []string{"10.0.0.0/8", "192.168.1.10"},
	})
	if !m.Allows("10.1.2.3") {
		t.Fatal("expected 10.1.2.3 allowed")
	}
	if !m.Allows("192.168.1.10") {
		t.Fatal("expected exact IP allowed")
	}
	if m.Allows("8.8.8.8") {
		t.Fatal("expected 8.8.8.8 denied")
	}
	if m.Allows("not-an-ip") {
		t.Fatal("unknown IP denied in whitelist")
	}
}

func TestBlacklist(t *testing.T) {
	m := Build(&config.AccessControl{
		Mode:      config.ACLModeBlacklist,
		Blacklist: []string{"203.0.113.0/24"},
	})
	if m.Allows("203.0.113.5") {
		t.Fatal("expected denied")
	}
	if !m.Allows("1.1.1.1") {
		t.Fatal("expected allowed")
	}
	if !m.Allows("garbage") {
		t.Fatal("unknown IP allowed in blacklist mode")
	}
}

func TestOff(t *testing.T) {
	m := Build(&config.AccessControl{Mode: config.ACLModeOff})
	if !m.Allows("1.2.3.4") {
		t.Fatal("off mode allows all")
	}
}
