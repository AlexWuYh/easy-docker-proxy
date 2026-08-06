package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandEnv(t *testing.T) {
	t.Setenv("FOO_TEST_VAR", "bar")
	got := ExpandEnv("user=${FOO_TEST_VAR}")
	if got != "user=bar" {
		t.Fatalf("got %q", got)
	}
}

func TestLoadExample(t *testing.T) {
	// Relative to module root when tests run from package dir.
	path := filepath.Join("..", "..", "configs", "config.example.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Skip("example config not found:", path)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Registries) < 2 {
		t.Fatalf("expected >=2 registries, got %d", len(cfg.Registries))
	}
	if cfg.Server.Listen != ":5000" {
		t.Fatalf("listen %q", cfg.Server.Listen)
	}
	if cfg.Default != "dockerhub" {
		t.Fatalf("default %q", cfg.Default)
	}
}

func TestValidateRejectsHTTPUpstream(t *testing.T) {
	en := true
	off := false
	cfg := &Config{
		Server:  ServerConfig{Listen: ":5000", AdminListen: "127.0.0.1:5001"},
		LogLevel: "normal",
		UpstreamAllowlist: UpstreamAllowlist{Enabled: &off},
		Registries: []RegistryConfig{{
			Name:     "x",
			Hosts:    []string{"x.example.com"},
			Upstream: "http://example.com",
			Auth:     AuthConfig{Type: AuthToken},
			Enabled:  &en,
		}},
	}
	Normalize(cfg)
	// Normalize re-enables allowlist when Enabled was set false - actually we set &off so stays false
	cfg.UpstreamAllowlist.Enabled = &off
	if err := Validate(cfg); err == nil {
		t.Fatal("expected http upstream rejected")
	}
	cfg.AllowInsecureUpstream = true
	if err := Validate(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestUpstreamAllowlist(t *testing.T) {
	en := true
	cfg := &Config{
		Server:   ServerConfig{Listen: ":5000", AdminListen: "127.0.0.1:5001"},
		LogLevel: "normal",
		Registries: []RegistryConfig{{
			Name: "evil", Hosts: []string{"e"}, Upstream: "https://evil.example",
			Auth: AuthConfig{Type: AuthAnonymous}, Enabled: &en,
		}},
	}
	Normalize(cfg)
	if err := Validate(cfg); err == nil {
		t.Fatal("expected allowlist rejection")
	}
	off := false
	cfg.UpstreamAllowlist.Enabled = &off
	if err := Validate(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestValidateWhitelistEmpty(t *testing.T) {
	en := true
	off := false
	cfg := &Config{
		Server:   ServerConfig{Listen: ":5000", AdminListen: "127.0.0.1:5001"},
		LogLevel: "normal",
		AccessControl: AccessControl{Mode: ACLModeWhitelist},
		UpstreamAllowlist: UpstreamAllowlist{Enabled: &off},
		Registries: []RegistryConfig{{
			Name: "x", Hosts: []string{"h"}, Upstream: "https://example.com",
			Auth: AuthConfig{Type: AuthAnonymous}, Enabled: &en,
		}},
	}
	Normalize(cfg)
	cfg.UpstreamAllowlist.Enabled = &off
	if err := Validate(cfg); err == nil {
		t.Fatal("expected empty whitelist rejected")
	}
}

func TestMaskedCopy(t *testing.T) {
	en := true
	cfg := &Config{
		Registries: []RegistryConfig{{
			Name: "x", Hosts: []string{"h"}, Upstream: "https://example.com",
			Auth: AuthConfig{Type: AuthToken, Password: "secret"}, Enabled: &en,
		}},
	}
	m := MaskedCopy(cfg)
	if m.Registries[0].Auth.Password != "********" {
		t.Fatalf("password not masked: %q", m.Registries[0].Auth.Password)
	}
	if cfg.Registries[0].Auth.Password != "secret" {
		t.Fatal("original mutated")
	}
}
