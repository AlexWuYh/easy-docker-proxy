package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/alex_wuyh/easy-docker-proxy/internal/config"
	"github.com/alex_wuyh/easy-docker-proxy/internal/record"
)

type captureEmitter struct {
	mu     sync.Mutex
	events []record.Event
}

func (c *captureEmitter) Emit(e record.Event) {
	c.mu.Lock()
	c.events = append(c.events, e)
	c.mu.Unlock()
}

func (c *captureEmitter) snapshot() []record.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]record.Event, len(c.events))
	copy(out, c.events)
	return out
}

func TestEmitManifestAndBlob(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/library/hello/manifests/latest":
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"schemaVersion":2}`)
		case r.URL.Path == "/v2/library/hello/blobs/sha256:dead":
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "layer-bytes")
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	en := true
	cfg := &config.Config{
		Server:   config.ServerConfig{Listen: ":0", AdminListen: "127.0.0.1:0"},
		LogLevel: "quiet",
		Default:  "local",
		Registries: []config.RegistryConfig{{
			Name: "local", Hosts: []string{"local.example.com"},
			Upstream: upstream.URL, Auth: config.AuthConfig{Type: config.AuthAnonymous}, Enabled: &en,
		}},
	}
	config.Normalize(cfg)
	cfg.AllowInsecureUpstream = true

	p := New(cfg)
	em := &captureEmitter{}
	p.SetEmitter(em)

	// manifest
	req := httptest.NewRequest(http.MethodGet, "http://local.example.com/v2/library/hello/manifests/latest", nil)
	req.Host = "local.example.com"
	req.RemoteAddr = "192.0.2.10:1"
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("manifest status %d", rr.Code)
	}

	// blob
	req2 := httptest.NewRequest(http.MethodGet, "http://local.example.com/v2/library/hello/blobs/sha256:dead", nil)
	req2.Host = "local.example.com"
	req2.RemoteAddr = "192.0.2.10:1"
	rr2 := httptest.NewRecorder()
	p.ServeHTTP(rr2, req2)
	if rr2.Code != 200 {
		t.Fatalf("blob status %d", rr2.Code)
	}

	// tags should not emit by default
	req3 := httptest.NewRequest(http.MethodGet, "http://local.example.com/v2/library/hello/tags/list", nil)
	req3.Host = "local.example.com"
	req3.RemoteAddr = "192.0.2.10:1"
	// upstream 404 is fine
	p.ServeHTTP(httptest.NewRecorder(), req3)

	deadline := time.Now().Add(time.Second)
	var evs []record.Event
	for time.Now().Before(deadline) {
		evs = em.snapshot()
		if len(evs) >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(evs) != 2 {
		t.Fatalf("expected 2 events (manifest+blob), got %d: %+v", len(evs), evs)
	}
	var sawManifest, sawBlob bool
	for _, e := range evs {
		if e.ClientIP != "192.0.2.10" {
			t.Fatalf("ip %q", e.ClientIP)
		}
		if e.Registry != "local" {
			t.Fatalf("registry %q", e.Registry)
		}
		switch e.EventType {
		case record.EventManifest:
			sawManifest = true
			if e.Repository != "library/hello" || e.Reference != "latest" {
				t.Fatalf("manifest meta %+v", e)
			}
			if !e.IsPull() {
				t.Fatal("expected IsPull")
			}
		case record.EventBlob:
			sawBlob = true
			if e.Bytes != int64(len("layer-bytes")) {
				t.Fatalf("blob bytes %d", e.Bytes)
			}
		default:
			t.Fatalf("unexpected type %s", e.EventType)
		}
	}
	if !sawManifest || !sawBlob {
		t.Fatal("missing event types")
	}
}

func TestClassifyPath(t *testing.T) {
	et, repo, ref, ok := classifyPath("/v2/foo/bar/manifests/v1")
	if !ok || et != record.EventManifest || repo != "foo/bar" || ref != "v1" {
		t.Fatalf("%v %q %q %v", et, repo, ref, ok)
	}
}
