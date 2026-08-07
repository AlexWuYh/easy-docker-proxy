package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alex_wuyh/easy-docker-proxy/internal/config"
)

func testConfig() *config.Config {
	en := true
	cfg := &config.Config{
		Server: config.ServerConfig{
			Listen:      ":5000",
			AdminListen: "127.0.0.1:5001",
		},
		Default:  "dockerhub",
		LogLevel: "quiet",
		TrustedProxies: []string{
			"127.0.0.1/32",
			"10.0.0.0/8",
		},
		AccessControl: config.AccessControl{Mode: config.ACLModeOff},
		RateLimit:     config.RateLimitConfig{Enabled: false},
		Registries: []config.RegistryConfig{
			{
				Name:     "dockerhub",
				Hosts:    []string{"hub.example.com"},
				Upstream: "https://registry-1.docker.io",
				Auth:     config.AuthConfig{Type: config.AuthToken},
				Enabled:  &en,
			},
			{
				Name:     "ghcr",
				Hosts:    []string{"ghcr.example.com"},
				Upstream: "https://ghcr.io",
				Auth:     config.AuthConfig{Type: config.AuthToken},
				Enabled:  &en,
			},
		},
	}
	config.Normalize(cfg)
	return cfg
}

func TestResolveRegistryByHost(t *testing.T) {
	p := New(testConfig())
	req := httptest.NewRequest(http.MethodGet, "http://hub.example.com/v2/", nil)
	req.Host = "hub.example.com"
	reg := p.ResolveRegistry(req)
	if reg == nil || reg.Name != "dockerhub" {
		t.Fatalf("got %+v", reg)
	}

	req2 := httptest.NewRequest(http.MethodGet, "http://ghcr.example.com/v2/", nil)
	req2.Host = "ghcr.example.com"
	reg2 := p.ResolveRegistry(req2)
	if reg2 == nil || reg2.Name != "ghcr" {
		t.Fatalf("got %+v", reg2)
	}
}

func TestResolveRegistryDefault(t *testing.T) {
	p := New(testConfig())
	req := httptest.NewRequest(http.MethodGet, "http://unknown.example.com/v2/", nil)
	req.Host = "unknown.example.com"
	reg := p.ResolveRegistry(req)
	if reg == nil || reg.Name != "dockerhub" {
		t.Fatalf("expected default dockerhub, got %+v", reg)
	}
}

func TestResolveRegistryNoDefaultFallback(t *testing.T) {
	cfg := testConfig()
	cfg.Default = ""
	p := New(cfg)
	req := httptest.NewRequest(http.MethodGet, "http://unknown.example.com/v2/library/nginx/manifests/latest", nil)
	req.Host = "unknown.example.com"
	if reg := p.ResolveRegistry(req); reg != nil {
		t.Fatalf("expected nil without default, got %+v", reg)
	}
	// Matched host still works.
	req2 := httptest.NewRequest(http.MethodGet, "http://hub.example.com/v2/", nil)
	req2.Host = "hub.example.com"
	if reg := p.ResolveRegistry(req2); reg == nil || reg.Name != "dockerhub" {
		t.Fatalf("matched host: %+v", reg)
	}
	// ServeHTTP returns 404 for unknown host.
	req3 := httptest.NewRequest(http.MethodGet, "http://unknown.example.com/v2/library/nginx/manifests/latest", nil)
	req3.Host = "unknown.example.com"
	req3.RemoteAddr = "127.0.0.1:1"
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req3)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
}

func TestResolveRegistryXForwardedHostTrusted(t *testing.T) {
	p := New(testConfig())
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/v2/", nil)
	req.Host = "127.0.0.1"
	req.RemoteAddr = "10.0.0.5:12345"
	req.Header.Set("X-Forwarded-Host", "ghcr.example.com")
	reg := p.ResolveRegistry(req)
	if reg == nil || reg.Name != "ghcr" {
		t.Fatalf("expected ghcr via XFH, got %+v", reg)
	}
}

func TestResolveRegistryXForwardedHostUntrusted(t *testing.T) {
	p := New(testConfig())
	req := httptest.NewRequest(http.MethodGet, "http://evil.com/v2/", nil)
	req.Host = "evil.com"
	req.RemoteAddr = "8.8.8.8:99"
	req.Header.Set("X-Forwarded-Host", "ghcr.example.com")
	reg := p.ResolveRegistry(req)
	// Untrusted peer: ignore XFH, fall back to default.
	if reg == nil || reg.Name != "dockerhub" {
		t.Fatalf("expected default when XFH untrusted, got %+v", reg)
	}
}

func TestClientIPTrustedXFF(t *testing.T) {
	p := New(testConfig())
	req := httptest.NewRequest(http.MethodGet, "http://hub.example.com/v2/", nil)
	req.RemoteAddr = "10.0.0.1:1"
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	if ip := p.ClientIP(req); ip != "203.0.113.9" {
		t.Fatalf("got %q", ip)
	}
}

func TestClientIPUntrustedIgnoresXFF(t *testing.T) {
	p := New(testConfig())
	req := httptest.NewRequest(http.MethodGet, "http://hub.example.com/v2/", nil)
	req.RemoteAddr = "198.51.100.1:1"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	if ip := p.ClientIP(req); ip != "198.51.100.1" {
		t.Fatalf("got %q", ip)
	}
}

func TestV2Ping(t *testing.T) {
	p := New(testConfig())
	req := httptest.NewRequest(http.MethodGet, "http://hub.example.com/v2/", nil)
	req.Host = "hub.example.com"
	req.RemoteAddr = "127.0.0.1:1"
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if v := rr.Header().Get("Docker-Distribution-Api-Version"); v != "registry/2.0" {
		t.Fatalf("api version %q", v)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	p := New(testConfig())
	req := httptest.NewRequest(http.MethodPost, "http://hub.example.com/v2/lib/manifests/x", nil)
	req.Host = "hub.example.com"
	req.RemoteAddr = "127.0.0.1:1"
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestUnknownPath404(t *testing.T) {
	p := New(testConfig())
	req := httptest.NewRequest(http.MethodGet, "http://hub.example.com/not-registry", nil)
	req.Host = "hub.example.com"
	req.RemoteAddr = "127.0.0.1:1"
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
}

func TestACLBlocks(t *testing.T) {
	cfg := testConfig()
	cfg.AccessControl = config.AccessControl{
		Mode:      config.ACLModeWhitelist,
		Whitelist: []string{"10.0.0.0/8"},
	}
	p := New(cfg)
	req := httptest.NewRequest(http.MethodGet, "http://hub.example.com/v2/", nil)
	req.Host = "hub.example.com"
	req.RemoteAddr = "8.8.8.8:1"
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestRateLimit(t *testing.T) {
	cfg := testConfig()
	cfg.RateLimit = config.RateLimitConfig{Enabled: true, PerIPRPS: 1, PerIPBurst: 1}
	p := New(cfg)
	mk := func() *http.Response {
		req := httptest.NewRequest(http.MethodGet, "http://hub.example.com/v2/", nil)
		req.Host = "hub.example.com"
		req.RemoteAddr = "10.9.9.9:1"
		rr := httptest.NewRecorder()
		p.ServeHTTP(rr, req)
		return rr.Result()
	}
	if mk().StatusCode != http.StatusOK {
		t.Fatal("first should pass")
	}
	if mk().StatusCode != http.StatusTooManyRequests {
		t.Fatal("second should be limited")
	}
}

func TestProxyStreamsUpstream(t *testing.T) {
	// Local upstream mimicking registry.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" && strings.HasPrefix(r.URL.Path, "/v2/") {
			// first request without token would 401 — we serve 200 for simplicity
		}
		if r.URL.Path == "/v2/library/hello/manifests/latest" {
			w.Header().Set("Content-Type", "application/vnd.docker.distribution.manifest.v2+json")
			w.Header().Set("WWW-Authenticate", `Bearer realm="http://example.invalid/token"`)
			_, _ = io.WriteString(w, `{"schemaVersion":2}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	en := true
	cfg := testConfig()
	cfg.AllowInsecureUpstream = true
	cfg.Registries = []config.RegistryConfig{{
		Name:     "local",
		Hosts:    []string{"local.example.com"},
		Upstream: upstream.URL,
		Auth:     config.AuthConfig{Type: config.AuthAnonymous},
		Enabled:  &en,
	}}
	cfg.Default = "local"
	p := New(cfg)

	req := httptest.NewRequest(http.MethodGet, "http://local.example.com/v2/library/hello/manifests/latest", nil)
	req.Host = "local.example.com"
	req.RemoteAddr = "127.0.0.1:1"
	// Client credentials must not be forwarded.
	req.Header.Set("Authorization", "Bearer client-secret")
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("WWW-Authenticate") != "" {
		t.Fatal("WWW-Authenticate must be stripped")
	}
	if !strings.Contains(rr.Body.String(), "schemaVersion") {
		t.Fatalf("body %q", rr.Body.String())
	}
}

func TestReloadUpdatesRoutes(t *testing.T) {
	p := New(testConfig())
	if p.HostIndexLen() != 2 {
		t.Fatalf("hosts %d", p.HostIndexLen())
	}
	en := true
	next := testConfig()
	next.Registries = []config.RegistryConfig{{
		Name: "only", Hosts: []string{"only.example.com"},
		Upstream: "https://example.com", Auth: config.AuthConfig{Type: config.AuthAnonymous}, Enabled: &en,
	}}
	next.Default = "only"
	p.Reload(next)
	req := httptest.NewRequest(http.MethodGet, "http://only.example.com/v2/", nil)
	req.Host = "only.example.com"
	if reg := p.ResolveRegistry(req); reg == nil || reg.Name != "only" {
		t.Fatalf("got %+v", reg)
	}
	if p.HostIndexLen() != 1 {
		t.Fatalf("hosts %d", p.HostIndexLen())
	}
}

func TestIsRegistryPath(t *testing.T) {
	cases := map[string]bool{
		"/v2/library/nginx/manifests/latest": true,
		"/v2/library/nginx/blobs/sha256:abc": true,
		"/v2/library/nginx/tags/list":        true,
		"/v2/foo/bar/referrers/sha256:x":     true,
		"/v2/": false, // handled separately
		"/admin": false,
	}
	for path, want := range cases {
		if got := isRegistryPath(path); got != want {
			t.Errorf("%s: got %v want %v", path, got, want)
		}
	}
}
