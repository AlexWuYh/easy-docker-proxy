package proxy

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alex_wuyh/easy-docker-proxy/internal/config"
)

type fakePullAuth struct {
	user string
	pass string
}

func (f *fakePullAuth) AuthenticatePull(_ context.Context, u, p string) (string, error) {
	if u == f.user && p == f.pass {
		return u, nil
	}
	return "", errors.New("bad")
}

func withBasic(r *http.Request, user, pass string) {
	tok := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
	r.Header.Set("Authorization", "Basic "+tok)
}

func TestPullAuthOff(t *testing.T) {
	cfg := testConfig()
	cfg.PullAuth.Mode = config.PullAuthOff
	p := New(cfg)
	p.SetPullAuthenticator(&fakePullAuth{user: "u", pass: "password1"})

	req := httptest.NewRequest(http.MethodGet, "http://hub.example.com/v2/", nil)
	req.Host = "hub.example.com"
	req.RemoteAddr = "127.0.0.1:1"
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("off mode should allow anonymous: %d", rr.Code)
	}
}

func TestPullAuthRequired(t *testing.T) {
	cfg := testConfig()
	cfg.PullAuth.Mode = config.PullAuthRequired
	cfg.PullAuth.Realm = "test-realm"
	p := New(cfg)
	p.SetPullAuthenticator(&fakePullAuth{user: "puller", pass: "password1"})

	// no creds
	req := httptest.NewRequest(http.MethodGet, "http://hub.example.com/v2/", nil)
	req.Host = "hub.example.com"
	req.RemoteAddr = "127.0.0.1:1"
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("required without creds: %d", rr.Code)
	}
	if got := rr.Header().Get("WWW-Authenticate"); got == "" || got != `Basic realm="test-realm"` {
		t.Fatalf("WWW-Authenticate %q", got)
	}

	// good creds
	req2 := httptest.NewRequest(http.MethodGet, "http://hub.example.com/v2/", nil)
	req2.Host = "hub.example.com"
	req2.RemoteAddr = "127.0.0.1:1"
	withBasic(req2, "puller", "password1")
	rr2 := httptest.NewRecorder()
	p.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("required with good creds: %d", rr2.Code)
	}

	// bad creds
	req3 := httptest.NewRequest(http.MethodGet, "http://hub.example.com/v2/", nil)
	req3.Host = "hub.example.com"
	req3.RemoteAddr = "127.0.0.1:1"
	withBasic(req3, "puller", "wrongpass")
	rr3 := httptest.NewRecorder()
	p.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusUnauthorized {
		t.Fatalf("required with bad creds: %d", rr3.Code)
	}
}

func TestPullAuthOptional(t *testing.T) {
	cfg := testConfig()
	cfg.PullAuth.Mode = config.PullAuthOptional
	p := New(cfg)
	p.SetPullAuthenticator(&fakePullAuth{user: "puller", pass: "password1"})

	// anonymous OK
	req := httptest.NewRequest(http.MethodGet, "http://hub.example.com/v2/", nil)
	req.Host = "hub.example.com"
	req.RemoteAddr = "127.0.0.1:1"
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("optional anonymous: %d", rr.Code)
	}

	// bad basic rejected
	req2 := httptest.NewRequest(http.MethodGet, "http://hub.example.com/v2/", nil)
	req2.Host = "hub.example.com"
	req2.RemoteAddr = "127.0.0.1:1"
	withBasic(req2, "puller", "nope")
	rr2 := httptest.NewRecorder()
	p.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusUnauthorized {
		t.Fatalf("optional bad creds: %d", rr2.Code)
	}

	// non-Basic Authorization is not treated as anonymous
	req3 := httptest.NewRequest(http.MethodGet, "http://hub.example.com/v2/", nil)
	req3.Host = "hub.example.com"
	req3.RemoteAddr = "127.0.0.1:1"
	req3.Header.Set("Authorization", "Bearer not-a-docker-login")
	rr3 := httptest.NewRecorder()
	p.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusUnauthorized {
		t.Fatalf("optional Bearer must 401: %d", rr3.Code)
	}
}
