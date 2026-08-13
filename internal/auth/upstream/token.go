// Package upstream handles upstream registry authentication (Bearer / Basic).
package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/alex_wuyh/easy-docker-proxy/internal/config"
)

const tokenFetchTimeout = 15 * time.Second

// Cache stores bearer tokens keyed by registry|realm|service|scope,
// plus a last-known token per registry|scope for the first hop (avoid a 401 RTT).
type Cache struct {
	mu    sync.Mutex
	items map[string]entry
	last  map[string]entry // regName|scope
}

type entry struct {
	token     string
	expiresAt time.Time
}

// NewCache creates an empty token cache.
func NewCache() *Cache {
	return &Cache{items: make(map[string]entry), last: make(map[string]entry)}
}

// Clear drops all cached tokens (call on config reload).
func (c *Cache) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.items = make(map[string]entry)
	c.last = make(map[string]entry)
	c.mu.Unlock()
}

func scopeIndexKey(regName, scope string) string {
	return regName + "|" + scope
}

// Peek returns a still-valid token previously fetched for this registry and scope.
// Realm is unknown until a 401; the last successful token for the scope is reused.
func (c *Cache) Peek(regName, scope string) string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.last[scopeIndexKey(regName, scope)]
	if !ok || !time.Now().Before(e.expiresAt) {
		return ""
	}
	return e.token
}

// InvalidateScope drops cached tokens for one registry+scope (call after a 401 with a peeked token).
func (c *Cache) InvalidateScope(regName, scope string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.last, scopeIndexKey(regName, scope))
	suffix := "|" + scope
	prefix := regName + "|"
	for k := range c.items {
		if strings.HasPrefix(k, prefix) && strings.HasSuffix(k, suffix) {
			delete(c.items, k)
		}
	}
}

// GetToken fetches (and caches) a bearer token from the auth realm.
// earlyExpireSeconds is subtracted from TTL (design: 60s; reference uses 30s).
// The realm URL (and redirects) must pass config.CheckTokenRealm.
func (c *Cache) GetToken(ctx context.Context, client *http.Client, realm, service, scope string, reg *config.RegistryConfig, cfg *config.Config) (string, error) {
	if c == nil {
		c = NewCache()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	upstream := ""
	if reg != nil {
		upstream = reg.Upstream
	}
	if err := config.CheckTokenRealm(cfg, realm, upstream); err != nil {
		return "", err
	}
	if reg == nil {
		return "", fmt.Errorf("missing registry")
	}
	key := reg.Name + "|" + realm + "|" + service + "|" + scope

	c.mu.Lock()
	if e, ok := c.items[key]; ok && time.Now().Before(e.expiresAt) {
		tok := e.token
		c.mu.Unlock()
		return tok, nil
	}
	c.mu.Unlock()

	u, err := url.Parse(realm)
	if err != nil {
		return "", err
	}
	q := u.Query()
	if service != "" {
		q.Set("service", service)
	}
	if scope != "" {
		q.Set("scope", scope)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	if reg.Auth.Username != "" || reg.Auth.Password != "" {
		req.SetBasicAuth(reg.Auth.Username, reg.Auth.Password)
	}

	tokenClient := tokenHTTPClient(client, cfg, upstream)
	resp, err := tokenClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tr struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", err
	}
	tok := tr.Token
	if tok == "" {
		tok = tr.AccessToken
	}
	if tok == "" {
		return "", fmt.Errorf("no token in response")
	}

	ttl := tr.ExpiresIn
	if ttl <= 0 {
		ttl = reg.TokenCacheTTL
	}
	if ttl <= 0 {
		ttl = 3600
	}
	// Expire early so we refresh before upstream rejects.
	skew := 60
	if ttl <= skew {
		skew = ttl / 2
		if skew < 1 {
			skew = 1
		}
	}
	expiresAt := time.Now().Add(time.Duration(ttl-skew) * time.Second)
	c.mu.Lock()
	ent := entry{token: tok, expiresAt: expiresAt}
	c.items[key] = ent
	c.last[scopeIndexKey(reg.Name, scope)] = ent
	c.mu.Unlock()
	return tok, nil
}

func tokenHTTPClient(base *http.Client, cfg *config.Config, upstream string) *http.Client {
	var transport http.RoundTripper
	if base != nil {
		transport = base.Transport
	}
	return &http.Client{
		Timeout:   tokenFetchTimeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("token realm: too many redirects")
			}
			if req.URL == nil {
				return fmt.Errorf("token realm: redirect missing URL")
			}
			return config.CheckTokenRealm(cfg, req.URL.String(), upstream)
		},
	}
}

// ParseBearerChallenge parses WWW-Authenticate: Bearer realm="...",service="...",scope="..."
func ParseBearerChallenge(header string) (realm, service, scope string) {
	header = strings.TrimSpace(header)
	if len(header) < 7 || !strings.EqualFold(header[:7], "bearer ") {
		return
	}
	rest := header[7:]
	var key, val strings.Builder
	inQuote := false
	expectVal := false
	commit := func() {
		k := strings.TrimSpace(key.String())
		v := strings.TrimSpace(val.String())
		switch strings.ToLower(k) {
		case "realm":
			realm = v
		case "service":
			service = v
		case "scope":
			scope = v
		}
		key.Reset()
		val.Reset()
		expectVal = false
	}
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if inQuote {
			if c == '"' {
				inQuote = false
			} else {
				val.WriteByte(c)
			}
			continue
		}
		switch c {
		case '"':
			inQuote = true
		case '=':
			expectVal = true
		case ',':
			if expectVal {
				commit()
			} else {
				key.WriteByte(c)
			}
		default:
			if expectVal {
				val.WriteByte(c)
			} else {
				key.WriteByte(c)
			}
		}
	}
	if expectVal {
		commit()
	}
	return
}
