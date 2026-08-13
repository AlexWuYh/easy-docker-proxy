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
		Server:            ServerConfig{Listen: ":5000", AdminListen: "127.0.0.1:5001"},
		LogLevel:          "normal",
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

func TestPathPrefixesOnlyValid(t *testing.T) {
	en := true
	off := false
	cfg := &Config{
		Server:            ServerConfig{Listen: ":5000", AdminListen: "127.0.0.1:5001"},
		LogLevel:          "normal",
		Default:           "dockerhub",
		UpstreamAllowlist: UpstreamAllowlist{Enabled: &off},
		Registries: []RegistryConfig{
			{
				Name: "dockerhub", PathPrefixes: []string{"docker.io"},
				Upstream: "https://registry-1.docker.io",
				Auth:     AuthConfig{Type: AuthToken}, Enabled: &en,
			},
			{
				Name: "ghcr", PathPrefixes: []string{"ghcr.io"},
				Upstream: "https://ghcr.io",
				Auth:     AuthConfig{Type: AuthToken}, Enabled: &en,
			},
		},
	}
	Normalize(cfg)
	cfg.UpstreamAllowlist.Enabled = &off
	if err := Validate(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Registries[0].PathPrefixes[0] != "docker.io" {
		t.Fatalf("normalize prefix %q", cfg.Registries[0].PathPrefixes[0])
	}
}

func TestDuplicatePathPrefixRejected(t *testing.T) {
	en := true
	off := false
	cfg := &Config{
		Server:            ServerConfig{Listen: ":5000", AdminListen: "127.0.0.1:5001"},
		LogLevel:          "normal",
		UpstreamAllowlist: UpstreamAllowlist{Enabled: &off},
		Registries: []RegistryConfig{
			{Name: "a", PathPrefixes: []string{"ghcr.io"}, Upstream: "https://ghcr.io", Auth: AuthConfig{Type: AuthAnonymous}, Enabled: &en},
			{Name: "b", PathPrefixes: []string{"ghcr.io"}, Upstream: "https://ghcr.io", Auth: AuthConfig{Type: AuthAnonymous}, Enabled: &en},
		},
	}
	Normalize(cfg)
	cfg.UpstreamAllowlist.Enabled = &off
	if err := Validate(cfg); err == nil {
		t.Fatal("expected duplicate path_prefix error")
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
		Server:            ServerConfig{Listen: ":5000", AdminListen: "127.0.0.1:5001"},
		LogLevel:          "normal",
		AccessControl:     AccessControl{Mode: ACLModeWhitelist},
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

func TestValidateRejectsUnknownAccessControlMode(t *testing.T) {
	en := true
	off := false
	cfg := &Config{
		Server:            ServerConfig{Listen: ":5000", AdminListen: "127.0.0.1:5001"},
		LogLevel:          "normal",
		AccessControl:     AccessControl{Mode: "white-list"},
		UpstreamAllowlist: UpstreamAllowlist{Enabled: &off},
		Registries: []RegistryConfig{{
			Name: "x", Hosts: []string{"h"}, Upstream: "https://example.com",
			Auth: AuthConfig{Type: AuthAnonymous}, Enabled: &en,
		}},
	}
	Normalize(cfg)
	if cfg.AccessControl.Mode != "white-list" {
		t.Fatalf("Normalize must not coerce unknown ACL mode, got %q", cfg.AccessControl.Mode)
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected invalid access_control.mode rejected")
	}
}

func TestValidateRejectsUnknownPullAuthMode(t *testing.T) {
	en := true
	off := false
	cfg := &Config{
		Server:            ServerConfig{Listen: ":5000", AdminListen: "127.0.0.1:5001"},
		LogLevel:          "normal",
		PullAuth:          PullAuthConfig{Mode: "require"},
		UpstreamAllowlist: UpstreamAllowlist{Enabled: &off},
		Registries: []RegistryConfig{{
			Name: "x", Hosts: []string{"h"}, Upstream: "https://example.com",
			Auth: AuthConfig{Type: AuthAnonymous}, Enabled: &en,
		}},
	}
	Normalize(cfg)
	if cfg.PullAuth.Mode != "require" {
		t.Fatalf("Normalize must not coerce unknown pull_auth.mode, got %q", cfg.PullAuth.Mode)
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected invalid pull_auth.mode rejected")
	}
}

func TestLoadDocker(t *testing.T) {
	path := filepath.Join("..", "..", "configs", "config.docker.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("docker config not found: %v", err)
	}
	t.Setenv("PROXY_ADMIN_TOKEN", "test-admin-token-for-load")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.AdminListen != "0.0.0.0:5001" {
		t.Fatalf("admin_listen %q", cfg.Server.AdminListen)
	}
	if len(cfg.TrustedProxies) != 1 || cfg.TrustedProxies[0] != "127.0.0.1/32" {
		t.Fatalf("trusted_proxies %#v", cfg.TrustedProxies)
	}
}

func TestCloneCopiesPathPrefixes(t *testing.T) {
	en := true
	cfg := &Config{
		Registries: []RegistryConfig{{
			Name: "ghcr", PathPrefixes: []string{"ghcr.io"},
			Hosts: []string{"reg.example.com"}, Enabled: &en,
		}},
	}
	cp := Clone(cfg)
	cp.Registries[0].PathPrefixes[0] = "mutated"
	if cfg.Registries[0].PathPrefixes[0] != "ghcr.io" {
		t.Fatal("Clone must not alias PathPrefixes")
	}
}

func TestCheckTokenRealm(t *testing.T) {
	off := false
	on := true
	cfg := &Config{
		AllowInsecureUpstream: true,
		UpstreamAllowlist:     UpstreamAllowlist{Enabled: &off},
	}
	if err := CheckTokenRealm(cfg, "http://127.0.0.1:9/token", "http://127.0.0.1:8"); err != nil {
		t.Fatalf("same-host http realm with AllowInsecureUpstream: %v", err)
	}

	strict := &Config{UpstreamAllowlist: UpstreamAllowlist{Enabled: &on}}
	if err := CheckTokenRealm(strict, "https://auth.docker.io/token", "https://registry-1.docker.io"); err != nil {
		t.Fatalf("default allowlist should include auth.docker.io: %v", err)
	}
	if err := CheckTokenRealm(strict, "https://evil.example/token", "https://ghcr.io"); err == nil {
		t.Fatal("expected allowlist rejection")
	}
	if err := CheckTokenRealm(strict, "http://auth.docker.io/token", "https://registry-1.docker.io"); err == nil {
		t.Fatal("expected http realm rejected without AllowInsecureUpstream")
	}
	if err := CheckTokenRealm(strict, "https://ghcr.io/token", "https://ghcr.io"); err != nil {
		t.Fatalf("same-host as upstream: %v", err)
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
