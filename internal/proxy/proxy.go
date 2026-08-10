// Package proxy implements the Registry V2 read-only data plane.
// Host routing, upstream token exchange, streaming forward. See .ai/01_DESIGN.md §4.
package proxy

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/alex_wuyh/easy-docker-proxy/internal/acl"
	"github.com/alex_wuyh/easy-docker-proxy/internal/auth/upstream"
	"github.com/alex_wuyh/easy-docker-proxy/internal/config"
	"github.com/alex_wuyh/easy-docker-proxy/internal/metrics"
	"github.com/alex_wuyh/easy-docker-proxy/internal/ratelimit"
	"github.com/alex_wuyh/easy-docker-proxy/internal/record"
)

// Emitter receives pull metadata without blocking (M2). nil is a no-op.
type Emitter interface {
	Emit(record.Event)
}

// PullAuthenticator verifies docker-login credentials for the data plane.
// Independent of web console users.
type PullAuthenticator interface {
	AuthenticatePull(ctx context.Context, username, password string) (string, error)
}

// Path patterns for Docker Registry HTTP API V2.
var (
	manifestRe  = regexp.MustCompile(`^/v2/(.+)/manifests/([^/]+)$`)
	blobRe      = regexp.MustCompile(`^/v2/(.+)/blobs/([^/]+)$`)
	tagsRe      = regexp.MustCompile(`^/v2/(.+)/tags/list$`)
	referrersRe = regexp.MustCompile(`^/v2/(.+)/referrers/([^/]+)$`)
)

// pathRoute maps a repository path prefix (e.g. ghcr.io) to an upstream.
type pathRoute struct {
	prefix string
	reg    *config.RegistryConfig
}

// Proxy is the registry reverse proxy.
type Proxy struct {
	mu         sync.RWMutex
	cfg        *config.Config
	hostIndex  map[string]*config.RegistryConfig
	pathRoutes []pathRoute // longest-prefix first
	defaultReg *config.RegistryConfig
	acl        *acl.Matcher
	limiter    *ratelimit.Limiter
	trusted    []*net.IPNet

	clients   map[string]*http.Client
	clientMux sync.Mutex

	tokens *upstream.Cache

	// recorder is optional; set via SetEmitter (async pull events, M2).
	recorder Emitter
	// metrics is optional Prometheus collector (M4).
	metrics *metrics.Collector
	// pullAuth verifies client Basic credentials (optional).
	pullAuth PullAuthenticator
}

// New creates a Proxy from cfg.
func New(cfg *config.Config) *Proxy {
	p := &Proxy{
		clients: make(map[string]*http.Client),
		tokens:  upstream.NewCache(),
	}
	p.apply(cfg)
	return p
}

// SetEmitter attaches an async event recorder (typically *record.Queue).
func (p *Proxy) SetEmitter(e Emitter) {
	p.mu.Lock()
	p.recorder = e
	p.mu.Unlock()
}

// SetMetrics attaches a metrics collector (optional).
func (p *Proxy) SetMetrics(m *metrics.Collector) {
	p.mu.Lock()
	p.metrics = m
	p.mu.Unlock()
}

// SetPullAuthenticator attaches pull-user verification for data-plane Basic auth.
func (p *Proxy) SetPullAuthenticator(a PullAuthenticator) {
	p.mu.Lock()
	p.pullAuth = a
	p.mu.Unlock()
}

// Reload swaps configuration without dropping in-flight requests.
func (p *Proxy) Reload(cfg *config.Config) {
	cp := config.Clone(cfg)
	p.mu.Lock()
	p.applyLocked(cp)
	p.mu.Unlock()
	p.tokens.Clear()
	// Drop HTTP clients so TLS/insecure settings take effect.
	p.clientMux.Lock()
	p.clients = make(map[string]*http.Client)
	p.clientMux.Unlock()
	log.Printf("proxy reloaded (%d registries)", len(cp.Registries))
}

func (p *Proxy) apply(cfg *config.Config) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.applyLocked(cfg)
}

func (p *Proxy) applyLocked(cfg *config.Config) {
	idx, paths, def := buildRoutes(cfg)
	p.cfg = cfg
	p.hostIndex = idx
	p.pathRoutes = paths
	p.defaultReg = def
	p.acl = acl.Build(&cfg.AccessControl)
	if p.limiter == nil {
		p.limiter = ratelimit.New(cfg.RateLimit)
	} else {
		p.limiter.Update(cfg.RateLimit)
	}
	p.trusted = parseTrusted(cfg.TrustedProxies)
}

func buildRoutes(cfg *config.Config) (map[string]*config.RegistryConfig, []pathRoute, *config.RegistryConfig) {
	idx := make(map[string]*config.RegistryConfig)
	var paths []pathRoute
	var def *config.RegistryConfig
	for i := range cfg.Registries {
		r := &cfg.Registries[i]
		if !r.IsEnabled() {
			continue
		}
		for _, h := range r.Hosts {
			idx[strings.ToLower(strings.TrimSpace(h))] = r
		}
		for _, pref := range r.PathPrefixes {
			pref = strings.ToLower(strings.TrimSpace(pref))
			pref = strings.Trim(pref, "/")
			if pref == "" {
				continue
			}
			paths = append(paths, pathRoute{prefix: pref, reg: r})
		}
		// Only an explicit default name becomes the no-prefix / unknown-Host fallback.
		if cfg.Default != "" && r.Name == cfg.Default {
			def = r
		}
	}
	// Longest path prefix first for stable matching.
	for i := 0; i < len(paths); i++ {
		for j := i + 1; j < len(paths); j++ {
			if len(paths[j].prefix) > len(paths[i].prefix) {
				paths[i], paths[j] = paths[j], paths[i]
			}
		}
	}
	return idx, paths, def
}

func parseTrusted(entries []string) []*net.IPNet {
	nets, _ := parseIPNets(entries)
	return nets
}

func parseIPNets(entries []string) ([]*net.IPNet, []string) {
	var nets []*net.IPNet
	var invalid []string
	for _, raw := range entries {
		e := strings.TrimSpace(raw)
		if e == "" {
			continue
		}
		if i := strings.IndexByte(e, '#'); i >= 0 {
			e = strings.TrimSpace(e[:i])
			if e == "" {
				continue
			}
		}
		var n *net.IPNet
		if _, ipnet, err := net.ParseCIDR(e); err == nil {
			n = ipnet
		} else if ip := net.ParseIP(e); ip != nil {
			if v4 := ip.To4(); v4 != nil {
				n = &net.IPNet{IP: v4, Mask: net.CIDRMask(32, 32)}
			} else {
				n = &net.IPNet{IP: ip, Mask: net.CIDRMask(128, 128)}
			}
		} else {
			invalid = append(invalid, raw)
			continue
		}
		nets = append(nets, n)
	}
	return nets, invalid
}

// Config returns a snapshot pointer of the current config (read-only use).
func (p *Proxy) Config() *config.Config {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.cfg
}

// routeHit is the result of hybrid routing (path prefix + Host + default).
type routeHit struct {
	reg         *config.RegistryConfig
	stripPrefix string // repository path prefix to strip before upstream (e.g. ghcr.io)
}

// ResolveRegistry picks an upstream (legacy helper for tests: ignores path rewrite).
// Prefer resolveRoute for full hybrid behaviour.
func (p *Proxy) ResolveRegistry(r *http.Request) *config.RegistryConfig {
	return p.resolveRoute(r).reg
}

// resolveRoute implements single-domain hybrid routing:
//  1. path_prefixes on repository (longest match)
//  2. Host / trusted X-Forwarded-Host
//  3. default registry
func (p *Proxy) resolveRoute(r *http.Request) routeHit {
	p.mu.RLock()
	defer p.mu.RUnlock()

	repo := extractRepo(r.URL.Path)
	if repo != "" {
		for _, pr := range p.pathRoutes {
			if repo == pr.prefix || strings.HasPrefix(repo, pr.prefix+"/") {
				return routeHit{reg: pr.reg, stripPrefix: pr.prefix}
			}
		}
	}

	host := requestHost(r)
	if reg, ok := p.hostIndex[host]; ok {
		return routeHit{reg: reg}
	}
	if p.isTrustedPeerLocked(r) {
		if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
			fh := strings.ToLower(strings.SplitN(fwd, ":", 2)[0])
			if reg, ok := p.hostIndex[fh]; ok {
				return routeHit{reg: reg}
			}
		}
	}
	return routeHit{reg: p.defaultReg}
}

// isTrustedPeerLocked assumes p.mu is held for read (or exclusive).
func (p *Proxy) isTrustedPeerLocked(r *http.Request) bool {
	remote := remoteIP(r)
	return ipInNets(remote, p.trusted)
}

func requestHost(r *http.Request) string {
	return strings.ToLower(strings.SplitN(r.Host, ":", 2)[0])
}

// ServeHTTP implements the Registry V2 read-only proxy.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ip := p.ClientIP(r)

	p.mu.RLock()
	matcher := p.acl
	limiter := p.limiter
	p.mu.RUnlock()

	if matcher != nil && !matcher.Allows(ip) {
		http.Error(w, "access denied", http.StatusForbidden)
		log.Printf("[ACL] denied %s %s %s", ip, r.Method, r.URL.Path)
		p.observe("", "denied", 0)
		return
	}
	if limiter != nil && !limiter.Allow(ip) {
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		p.observe("", "ratelimit", 0)
		return
	}

	// Method restriction: read-only proxy.
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		// ok
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		p.observe("", "other", 0)
		return
	}

	// Client pull auth (independent of web console users).
	pullUser, ok := p.checkPullAuth(w, r)
	if !ok {
		p.observe("", "denied", 0)
		return
	}

	// API version check — answer locally so docker client proceeds.
	if r.URL.Path == "/v2/" || r.URL.Path == "/v2" {
		w.Header().Set("Docker-Distribution-Api-Version", "registry/2.0")
		w.WriteHeader(http.StatusOK)
		p.observe("", "ok", 0)
		return
	}

	if !isRegistryPath(r.URL.Path) {
		http.NotFound(w, r)
		p.observe("", "other", 0)
		return
	}

	hit := p.resolveRoute(r)
	if hit.reg == nil {
		// Unknown Host / path with no default fallback, or no registries configured.
		http.Error(w, "unknown registry host", http.StatusNotFound)
		p.observe("", "error", 0)
		return
	}

	start := time.Now()
	status, written, errMsg := p.proxyRequest(w, r, hit)
	p.maybeLog(r, hit.reg, start)
	p.emitEvent(r, hit, ip, start, status, written, errMsg, pullUser)
	p.observe(hit.reg.Name, metrics.ClassFromStatus(status), written)
}

// checkPullAuth enforces pull_auth.mode. Returns (username, true) on allow.
func (p *Proxy) checkPullAuth(w http.ResponseWriter, r *http.Request) (string, bool) {
	p.mu.RLock()
	mode := config.PullAuthOff
	realm := "easy-docker-proxy"
	if p.cfg != nil {
		mode = p.cfg.PullAuth.Mode
		if p.cfg.PullAuth.Realm != "" {
			realm = p.cfg.PullAuth.Realm
		}
	}
	authn := p.pullAuth
	p.mu.RUnlock()

	if mode == "" {
		mode = config.PullAuthOff
	}
	if mode == config.PullAuthOff {
		return "", true
	}

	user, pass, hasBasic := parseBasicAuth(r)
	if !hasBasic {
		if mode == config.PullAuthRequired {
			writePullAuthChallenge(w, realm)
			return "", false
		}
		// optional: anonymous OK
		return "", true
	}

	// Credentials present: must validate in optional and required modes.
	if authn == nil {
		log.Printf("[pull-auth] credentials provided but no authenticator configured")
		writePullAuthChallenge(w, realm)
		return "", false
	}
	name, err := authn.AuthenticatePull(r.Context(), user, pass)
	if err != nil {
		writePullAuthChallenge(w, realm)
		return "", false
	}
	return name, true
}

func writePullAuthChallenge(w http.ResponseWriter, realm string) {
	w.Header().Set("Docker-Distribution-Api-Version", "registry/2.0")
	w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`"`)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

func parseBasicAuth(r *http.Request) (user, pass string, ok bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", "", false
	}
	const prefix = "basic "
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", "", false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(h[len(prefix):]))
	if err != nil {
		return "", "", false
	}
	parts := strings.SplitN(string(raw), ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func (p *Proxy) observe(registry, class string, bytes int64) {
	p.mu.RLock()
	m := p.metrics
	p.mu.RUnlock()
	if m == nil {
		return
	}
	m.IncRequest(registry, class)
	m.AddBytes(bytes)
}

func isRegistryPath(path string) bool {
	return manifestRe.MatchString(path) ||
		blobRe.MatchString(path) ||
		tagsRe.MatchString(path) ||
		referrersRe.MatchString(path)
}

func (p *Proxy) maybeLog(r *http.Request, reg *config.RegistryConfig, start time.Time) {
	p.mu.RLock()
	lv := p.cfg.LogLevel
	p.mu.RUnlock()
	switch lv {
	case "quiet":
		return
	case "normal":
		if blobRe.MatchString(r.URL.Path) {
			return
		}
	}
	log.Printf("%s %s host=%s -> %s (%s)", r.Method, r.URL.Path, r.Host, reg.Name, time.Since(start))
}

// ClientIP extracts the client address using trusted_proxies for XFF.
// Exported for tests.
func (p *Proxy) ClientIP(r *http.Request) string {
	remote := remoteIP(r)
	p.mu.RLock()
	trusted := p.trusted
	p.mu.RUnlock()

	if remote != "" && ipInNets(remote, trusted) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// Left-most is original client when chain is appended by proxies.
			parts := strings.Split(xff, ",")
			if len(parts) > 0 {
				if ip := strings.TrimSpace(parts[0]); ip != "" {
					return ip
				}
			}
		}
		if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
			return xri
		}
	}
	return remote
}

func (p *Proxy) isTrustedPeer(r *http.Request) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.isTrustedPeerLocked(r)
}

func remoteIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func ipInNets(ipStr string, nets []*net.IPNet) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func (p *Proxy) proxyRequest(w http.ResponseWriter, r *http.Request, hit routeHit) (status int, written int64, errMsg string) {
	reg := hit.reg
	client := p.getClient(reg)

	target, err := url.Parse(reg.Upstream)
	if err != nil {
		log.Printf("[ERR] bad upstream url %q: %v", reg.Upstream, err)
		http.Error(w, "bad upstream url", http.StatusBadGateway)
		return http.StatusBadGateway, 0, "bad upstream url"
	}
	// Strip path_prefix (e.g. ghcr.io/) so upstream sees its native repository path.
	fwdPath := stripRepoPathPrefix(r.URL.Path, hit.stripPrefix)
	target.Path = singleJoiningSlash(target.Path, fwdPath)
	target.RawQuery = r.URL.RawQuery

	repo := extractRepo(fwdPath)
	scope := ""
	if repo != "" {
		scope = "repository:" + repo + ":pull"
	}

	resp, err := p.doUpstream(client, r, target.String(), "", reg)
	if err != nil {
		log.Printf("[ERR] upstream %s: %v", reg.Name, err)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return http.StatusBadGateway, 0, "upstream error"
	}

	// Token challenge: obtain bearer and retry (not used for anonymous).
	if resp.StatusCode == http.StatusUnauthorized && reg.Auth.Type != config.AuthAnonymous {
		challenge := resp.Header.Get("WWW-Authenticate")
		realm, service, _ := upstream.ParseBearerChallenge(challenge)
		resp.Body.Close()
		if realm == "" {
			http.Error(w, "unauthorized upstream", http.StatusBadGateway)
			return http.StatusBadGateway, 0, "unauthorized upstream"
		}
		token, terr := p.tokens.GetToken(client, realm, service, scope, reg)
		if terr != nil {
			log.Printf("[ERR] token %s scope=%q: %v", reg.Name, scope, terr)
			http.Error(w, "token error", http.StatusBadGateway)
			return http.StatusBadGateway, 0, "token error"
		}
		if token == "" {
			http.Error(w, "unauthorized upstream", http.StatusBadGateway)
			return http.StatusBadGateway, 0, "unauthorized upstream"
		}
		resp2, rerr := p.doUpstream(client, r, target.String(), token, reg)
		if rerr != nil {
			log.Printf("[ERR] upstream %s (retry): %v", reg.Name, rerr)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return http.StatusBadGateway, 0, "upstream error"
		}
		resp = resp2
	}

	defer resp.Body.Close()
	copyHeaders(w.Header(), resp.Header)
	// Client must not contact upstream auth server directly.
	w.Header().Del("WWW-Authenticate")
	w.Header().Set("Docker-Distribution-Api-Version", "registry/2.0")
	w.WriteHeader(resp.StatusCode)
	status = resp.StatusCode

	if r.Method == http.MethodHead {
		return status, 0, ""
	}

	// Stream without buffering whole body to disk/memory.
	buf := make([]byte, 32*1024)
	flusher, _ := w.(http.Flusher)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return status, written, "client write error"
			}
			written += int64(n)
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return status, written, "upstream read error"
		}
	}
	return status, written, ""
}

// emitEvent records manifest/blob metadata asynchronously (never blocks).
// tags/list and referrers are skipped by default (design §5.2).
// Repository uses the stripped upstream path in hybrid path-prefix mode.
func (p *Proxy) emitEvent(r *http.Request, hit routeHit, ip string, start time.Time, status int, written int64, errMsg, pullUser string) {
	fwdPath := stripRepoPathPrefix(r.URL.Path, hit.stripPrefix)
	et, repo, ref, ok := classifyPath(fwdPath)
	if !ok {
		return
	}
	// Default: full manifests + blobs (for traffic); skip tags/referrers.
	if et != record.EventManifest && et != record.EventBlob {
		return
	}

	p.mu.RLock()
	rec := p.recorder
	p.mu.RUnlock()
	if rec == nil {
		return
	}

	// Never put secrets in events (UA only; no Authorization).
	ua := r.Header.Get("User-Agent")
	if len(ua) > 256 {
		ua = ua[:256]
	}

	rec.Emit(record.Event{
		TS:         time.Now().UTC(),
		ClientIP:   ip,
		Registry:   hit.reg.Name,
		Host:       requestHost(r),
		EventType:  et,
		Repository: repo,
		Reference:  ref,
		Method:     r.Method,
		Status:     status,
		Bytes:      written,
		DurationMS: time.Since(start).Milliseconds(),
		UserAgent:  ua,
		PullUser:   pullUser,
		Error:      errMsg,
	})
}

// stripRepoPathPrefix removes a hybrid-mode path prefix from a Registry V2 URL path.
// Example: /v2/ghcr.io/owner/app/manifests/t + "ghcr.io" → /v2/owner/app/manifests/t
func stripRepoPathPrefix(path, prefix string) string {
	if prefix == "" {
		return path
	}
	const head = "/v2/"
	if !strings.HasPrefix(path, head) {
		return path
	}
	rest := path[len(head):]
	pre := strings.ToLower(strings.Trim(prefix, "/"))
	lowerRest := strings.ToLower(rest)
	if lowerRest == pre {
		return head[:len(head)-1] // "/v2"
	}
	if strings.HasPrefix(lowerRest, pre+"/") {
		return head + rest[len(pre)+1:]
	}
	return path
}

// classifyPath returns event type, repository, and reference for a V2 path.
func classifyPath(path string) (et record.EventType, repo, ref string, ok bool) {
	if m := manifestRe.FindStringSubmatch(path); m != nil {
		return record.EventManifest, m[1], m[2], true
	}
	if m := blobRe.FindStringSubmatch(path); m != nil {
		return record.EventBlob, m[1], m[2], true
	}
	if m := tagsRe.FindStringSubmatch(path); m != nil {
		return record.EventTags, m[1], "", true
	}
	if m := referrersRe.FindStringSubmatch(path); m != nil {
		return record.EventReferrers, m[1], m[2], true
	}
	return "", "", "", false
}

func (p *Proxy) doUpstream(client *http.Client, r *http.Request, targetURL, token string, reg *config.RegistryConfig) (*http.Response, error) {
	req, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, nil)
	if err != nil {
		return nil, err
	}
	copyHeaders(req.Header, r.Header)
	// Manage auth ourselves; never forward client credentials.
	req.Header.Del("Authorization")
	removeHopByHop(req.Header)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	} else if reg.Auth.Type == config.AuthBasic && (reg.Auth.Username != "" || reg.Auth.Password != "") {
		req.SetBasicAuth(reg.Auth.Username, reg.Auth.Password)
	}
	return client.Do(req)
}

func (p *Proxy) getClient(reg *config.RegistryConfig) *http.Client {
	p.clientMux.Lock()
	defer p.clientMux.Unlock()
	if c, ok := p.clients[reg.Name]; ok {
		return c
	}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		// Generous for large layers; timeouts controlled by server write_timeout=0.
		IdleConnTimeout: 90 * time.Second,
	}
	if reg.InsecureSkipVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // explicit config opt-in
	}
	c := &http.Client{Transport: transport}
	p.clients[reg.Name] = c
	return c
}

// extractRepo returns the repository name from a Registry V2 path.
func extractRepo(path string) string {
	for _, re := range []*regexp.Regexp{manifestRe, blobRe, tagsRe, referrersRe} {
		if m := re.FindStringSubmatch(path); m != nil {
			return m[1]
		}
	}
	return ""
}

// HostIndexLen returns number of host routes (tests).
func (p *Proxy) HostIndexLen() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.hostIndex)
}

// DefaultRegistryName returns the default registry name (tests).
func (p *Proxy) DefaultRegistryName() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.defaultReg == nil {
		return ""
	}
	return p.defaultReg.Name
}

var hopHeaders = []string{
	"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
	"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

func removeHopByHop(h http.Header) {
	for _, k := range hopHeaders {
		h.Del(k)
	}
	if c := h.Get("Connection"); c != "" {
		for _, f := range strings.Split(c, ",") {
			h.Del(strings.TrimSpace(f))
		}
	}
}

func copyHeaders(dst, src http.Header) {
	for k, vals := range src {
		for _, v := range vals {
			dst.Add(k, v)
		}
	}
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}
