package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alex_wuyh/easy-docker-proxy/internal/config"
)

func hybridConfig() *config.Config {
	en := true
	cfg := &config.Config{
		Server:   config.ServerConfig{Listen: ":5000", AdminListen: "127.0.0.1:5001"},
		Default:  "dockerhub",
		LogLevel: "quiet",
		Registries: []config.RegistryConfig{
			{
				Name:         "dockerhub",
				Hosts:        []string{"reg.example.com"},
				PathPrefixes: []string{"docker.io"},
				Upstream:     "https://registry-1.docker.io",
				Auth:         config.AuthConfig{Type: config.AuthAnonymous},
				Enabled:      &en,
			},
			{
				Name:         "ghcr",
				PathPrefixes: []string{"ghcr.io"},
				Upstream:     "https://ghcr.io",
				Auth:         config.AuthConfig{Type: config.AuthAnonymous},
				Enabled:      &en,
			},
			{
				Name:         "quay",
				PathPrefixes: []string{"quay.io"},
				Upstream:     "https://quay.io",
				Auth:         config.AuthConfig{Type: config.AuthAnonymous},
				Enabled:      &en,
			},
			{
				Name:         "gcr",
				PathPrefixes: []string{"gcr.io"},
				Upstream:     "https://gcr.io",
				Auth:         config.AuthConfig{Type: config.AuthAnonymous},
				Enabled:      &en,
			},
			{
				Name:         "k8s",
				PathPrefixes: []string{"k8s.gcr.io", "registry.k8s.io"},
				Upstream:     "https://registry.k8s.io",
				Auth:         config.AuthConfig{Type: config.AuthAnonymous},
				Enabled:      &en,
			},
		},
	}
	off := false
	cfg.UpstreamAllowlist.Enabled = &off
	config.Normalize(cfg)
	cfg.UpstreamAllowlist.Enabled = &off
	return cfg
}

func TestStripRepoPathPrefix(t *testing.T) {
	cases := []struct {
		path, prefix, want string
	}{
		{"/v2/ghcr.io/owner/app/manifests/v1", "ghcr.io", "/v2/owner/app/manifests/v1"},
		{"/v2/docker.io/library/nginx/manifests/latest", "docker.io", "/v2/library/nginx/manifests/latest"},
		{"/v2/library/nginx/manifests/latest", "", "/v2/library/nginx/manifests/latest"},
		{"/v2/library/nginx/manifests/latest", "ghcr.io", "/v2/library/nginx/manifests/latest"},
		{"/v2/ghcr.io/owner/app/blobs/sha256:abc", "ghcr.io", "/v2/owner/app/blobs/sha256:abc"},
	}
	for _, c := range cases {
		got := stripRepoPathPrefix(c.path, c.prefix)
		if got != c.want {
			t.Fatalf("strip(%q,%q)=%q want %q", c.path, c.prefix, got, c.want)
		}
	}
}

func TestResolveRoutePathPrefix(t *testing.T) {
	p := New(hybridConfig())

	// No prefix → default dockerhub
	req := httptest.NewRequest(http.MethodGet, "http://reg.example.com/v2/library/nginx/manifests/latest", nil)
	req.Host = "reg.example.com"
	hit := p.resolveRoute(req)
	if hit.reg == nil || hit.reg.Name != "dockerhub" || hit.stripPrefix != "" {
		t.Fatalf("no prefix: %+v reg=%v", hit, hit.reg)
	}

	// ghcr.io prefix
	req2 := httptest.NewRequest(http.MethodGet, "http://reg.example.com/v2/ghcr.io/owner/app/manifests/tag", nil)
	req2.Host = "reg.example.com"
	hit2 := p.resolveRoute(req2)
	if hit2.reg == nil || hit2.reg.Name != "ghcr" || hit2.stripPrefix != "ghcr.io" {
		t.Fatalf("ghcr: %+v", hit2)
	}

	// docker.io prefix → dockerhub + strip
	req3 := httptest.NewRequest(http.MethodGet, "http://reg.example.com/v2/docker.io/library/nginx/manifests/latest", nil)
	req3.Host = "reg.example.com"
	hit3 := p.resolveRoute(req3)
	if hit3.reg == nil || hit3.reg.Name != "dockerhub" || hit3.stripPrefix != "docker.io" {
		t.Fatalf("docker.io prefix: %+v", hit3)
	}
}

func TestPathPrefixUpstreamRewrite(t *testing.T) {
	var seenPath string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"schemaVersion":2}`)
	}))
	defer up.Close()

	en := true
	cfg := hybridConfig()
	for i := range cfg.Registries {
		if cfg.Registries[i].Name == "ghcr" {
			cfg.Registries[i].Upstream = up.URL
			cfg.Registries[i].Auth = config.AuthConfig{Type: config.AuthAnonymous}
			cfg.Registries[i].Enabled = &en
		}
	}
	cfg.AllowInsecureUpstream = true
	p := New(cfg)

	req := httptest.NewRequest(http.MethodGet, "http://reg.example.com/v2/ghcr.io/owner/app/manifests/v1", nil)
	req.Host = "reg.example.com"
	req.RemoteAddr = "127.0.0.1:1"
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	if seenPath != "/v2/owner/app/manifests/v1" {
		t.Fatalf("upstream path %q want /v2/owner/app/manifests/v1", seenPath)
	}
}

func TestDockerIOPrefixUpstreamRewrite(t *testing.T) {
	var seenPath string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer up.Close()

	cfg := hybridConfig()
	for i := range cfg.Registries {
		if cfg.Registries[i].Name == "dockerhub" {
			cfg.Registries[i].Upstream = up.URL
		}
	}
	cfg.AllowInsecureUpstream = true
	p := New(cfg)

	req := httptest.NewRequest(http.MethodGet, "http://reg.example.com/v2/docker.io/library/nginx/manifests/latest", nil)
	req.Host = "reg.example.com"
	req.RemoteAddr = "127.0.0.1:1"
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if seenPath != "/v2/library/nginx/manifests/latest" {
		t.Fatalf("path %q", seenPath)
	}
	if !strings.Contains(rr.Body.String(), "{") {
		t.Fatalf("body %q", rr.Body.String())
	}
}

func TestLongestPathPrefix(t *testing.T) {
	p := New(hybridConfig())
	req := httptest.NewRequest(http.MethodGet, "http://reg.example.com/v2/k8s.gcr.io/pause/manifests/3.9", nil)
	req.Host = "reg.example.com"
	hit := p.resolveRoute(req)
	if hit.reg == nil || hit.reg.Name != "k8s" || hit.stripPrefix != "k8s.gcr.io" {
		t.Fatalf("expected k8s over gcr.io, got %+v", hit)
	}
	req2 := httptest.NewRequest(http.MethodGet, "http://reg.example.com/v2/gcr.io/proj/img/manifests/t", nil)
	req2.Host = "reg.example.com"
	hit2 := p.resolveRoute(req2)
	if hit2.reg == nil || hit2.reg.Name != "gcr" || hit2.stripPrefix != "gcr.io" {
		t.Fatalf("gcr: %+v", hit2)
	}
}

func TestPathPrefixTokenScopeStripped(t *testing.T) {
	var gotScope, gotAuth, seenPath string
	var tokenHits int
	tok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenHits++
		gotScope = r.URL.Query().Get("scope")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"t-ok","expires_in":300}`))
	}))
	defer tok.Close()

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		if r.Header.Get("Authorization") == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+tok.URL+`",service="ghcr.io"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"schemaVersion":2}`))
	}))
	defer up.Close()

	en := true
	cfg := hybridConfig()
	for i := range cfg.Registries {
		if cfg.Registries[i].Name == "ghcr" {
			cfg.Registries[i].Upstream = up.URL
			cfg.Registries[i].Auth = config.AuthConfig{Type: config.AuthToken}
			cfg.Registries[i].Enabled = &en
		}
	}
	cfg.AllowInsecureUpstream = true
	p := New(cfg)
	em := &captureEmitter{}
	p.SetEmitter(em)

	req := httptest.NewRequest(http.MethodGet, "http://reg.example.com/v2/ghcr.io/owner/app/manifests/v1", nil)
	req.Host = "reg.example.com"
	req.RemoteAddr = "127.0.0.1:1"
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	if seenPath != "/v2/owner/app/manifests/v1" {
		t.Fatalf("upstream path %q", seenPath)
	}
	if gotScope != "repository:owner/app:pull" {
		t.Fatalf("scope %q want repository:owner/app:pull (prefix must be stripped)", gotScope)
	}
	if gotAuth != "Bearer t-ok" {
		t.Fatalf("retry Authorization %q", gotAuth)
	}
	if rr.Header().Get("WWW-Authenticate") != "" {
		t.Fatal("WWW-Authenticate must be stripped from the client response")
	}
	evs := em.snapshot()
	if len(evs) != 1 || evs[0].Repository != "owner/app" {
		t.Fatalf("event must record stripped repo, got %+v", evs)
	}
	if tokenHits != 1 {
		t.Fatalf("first request token fetches %d", tokenHits)
	}

	// Second pull for the same repo should reuse the cached token (no extra 401 RTT).
	req2 := httptest.NewRequest(http.MethodGet, "http://reg.example.com/v2/ghcr.io/owner/app/manifests/v2", nil)
	req2.Host = "reg.example.com"
	req2.RemoteAddr = "127.0.0.1:1"
	rr2 := httptest.NewRecorder()
	p.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("second status %d", rr2.Code)
	}
	if tokenHits != 1 {
		t.Fatalf("cached token should skip the realm, fetches=%d", tokenHits)
	}
}
