// Package config loads and validates proxy configuration.
// YAML parse, ${ENV} expansion, fail-closed checks. See .ai/01_DESIGN.md §4.
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// AuthType describes how the proxy authenticates to the upstream registry.
type AuthType string

const (
	AuthToken     AuthType = "token"
	AuthAnonymous AuthType = "anonymous"
	AuthBasic     AuthType = "basic"
)

// AuthConfig holds upstream credentials.
type AuthConfig struct {
	Type     AuthType `yaml:"type" json:"type"`
	Username string   `yaml:"username" json:"username"`
	Password string   `yaml:"password" json:"password"`
}

// RegistryConfig describes a single upstream registry and routing.
type RegistryConfig struct {
	Name string `yaml:"name" json:"name"`
	// Hosts: optional Host / X-Forwarded-Host match (legacy multi-host mode).
	Hosts []string `yaml:"hosts" json:"hosts"`
	// PathPrefixes: repository path prefixes for single-domain hybrid mode, e.g. "ghcr.io", "docker.io".
	// Client: docker pull reg.example.com/ghcr.io/owner/app:tag → repo "ghcr.io/owner/app".
	// Longest prefix wins. Stripped before talking to upstream.
	PathPrefixes       []string   `yaml:"path_prefixes" json:"path_prefixes"`
	Upstream           string     `yaml:"upstream" json:"upstream"`
	Auth               AuthConfig `yaml:"auth" json:"auth"`
	InsecureSkipVerify bool       `yaml:"insecure_skip_verify" json:"insecure_skip_verify"`
	TokenCacheTTL      int        `yaml:"token_cache_ttl" json:"token_cache_ttl"`
	// Enabled: nil means enabled (omit field in hand-written configs).
	Enabled *bool `yaml:"enabled" json:"enabled"`
}

// IsEnabled reports whether routing for this registry is active.
func (r *RegistryConfig) IsEnabled() bool {
	return r.Enabled == nil || *r.Enabled
}

// ServerConfig holds HTTP server tunables.
type ServerConfig struct {
	Listen       string `yaml:"listen" json:"listen"`
	AdminListen  string `yaml:"admin_listen" json:"admin_listen"`
	ReadTimeout  int    `yaml:"read_timeout" json:"read_timeout"`
	WriteTimeout int    `yaml:"write_timeout" json:"write_timeout"`
	IdleTimeout  int    `yaml:"idle_timeout" json:"idle_timeout"`
}

// AccessControlMode enumerates IP filtering modes.
type AccessControlMode string

const (
	ACLModeOff       AccessControlMode = "off"
	ACLModeWhitelist AccessControlMode = "whitelist"
	ACLModeBlacklist AccessControlMode = "blacklist"
)

// AccessControl configures per-request IP allow/deny on the data plane.
type AccessControl struct {
	Mode      AccessControlMode `yaml:"mode" json:"mode"`
	Whitelist []string          `yaml:"whitelist" json:"whitelist"`
	Blacklist []string          `yaml:"blacklist" json:"blacklist"`
}

// RateLimitConfig is per-IP rate limiting for the data plane.
type RateLimitConfig struct {
	Enabled    bool    `yaml:"enabled" json:"enabled"`
	PerIPRPS   float64 `yaml:"per_ip_rps" json:"per_ip_rps"`
	PerIPBurst int     `yaml:"per_ip_burst" json:"per_ip_burst"`
}

// StorageConfig configures pull-event persistence (M2).
type StorageConfig struct {
	Driver                 string `yaml:"driver" json:"driver"` // sqlite (default)
	DSN                    string `yaml:"dsn" json:"dsn"`
	EventRetentionDays     int    `yaml:"event_retention_days" json:"event_retention_days"`
	AggregateRetentionDays int    `yaml:"aggregate_retention_days" json:"aggregate_retention_days"`
}

// AdminConfig controls admin/stats authentication.
type AdminConfig struct {
	TokenEnv         string `yaml:"token_env" json:"token_env"`
	StatsRequireAuth bool   `yaml:"stats_require_auth" json:"stats_require_auth"`
}

// MetricsConfig toggles Prometheus exposition on the admin plane.
type MetricsConfig struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
}

// PullAuthMode controls client authentication on the registry data plane.
type PullAuthMode string

const (
	// PullAuthOff: no client credentials required (default, fully open subject to ACL).
	PullAuthOff PullAuthMode = "off"
	// PullAuthOptional: anonymous allowed; if Authorization is sent it must be valid.
	PullAuthOptional PullAuthMode = "optional"
	// PullAuthRequired: valid Basic credentials required for all registry paths including /v2/.
	PullAuthRequired PullAuthMode = "required"
)

// PullAuthConfig is independent of web console users (docker login to the proxy).
type PullAuthConfig struct {
	// Mode: off | optional | required. Default off.
	Mode PullAuthMode `yaml:"mode" json:"mode"`
	// Realm is sent in WWW-Authenticate challenges (Basic).
	Realm string `yaml:"realm" json:"realm"`
}

// UpstreamAllowlist restricts registry upstream hostnames (SSRF hardening).
// When Enabled, every registries[].upstream host must appear in Hosts (or defaults).
type UpstreamAllowlist struct {
	// Enabled: nil/omitted means true (fail-closed default for M4).
	// Set enabled: false to disable (not recommended on public hosts).
	Enabled *bool    `yaml:"enabled" json:"enabled"`
	Hosts   []string `yaml:"hosts" json:"hosts"`
}

// IsEnabled reports whether allowlist checks are active.
func (a UpstreamAllowlist) IsEnabled() bool {
	return a.Enabled == nil || *a.Enabled
}

// Config is the top-level configuration root.
type Config struct {
	Server            ServerConfig      `yaml:"server" json:"server"`
	Default           string            `yaml:"default" json:"default"`
	LogLevel          string            `yaml:"log_level" json:"log_level"`
	TrustedProxies    []string          `yaml:"trusted_proxies" json:"trusted_proxies"`
	AccessControl     AccessControl     `yaml:"access_control" json:"access_control"`
	RateLimit         RateLimitConfig   `yaml:"rate_limit" json:"rate_limit"`
	Storage           StorageConfig     `yaml:"storage" json:"storage"`
	Admin             AdminConfig       `yaml:"admin" json:"admin"`
	Metrics           MetricsConfig     `yaml:"metrics" json:"metrics"`
	PullAuth          PullAuthConfig    `yaml:"pull_auth" json:"pull_auth"`
	UpstreamAllowlist UpstreamAllowlist `yaml:"upstream_allowlist" json:"upstream_allowlist"`
	Registries        []RegistryConfig  `yaml:"registries" json:"registries"`

	// AllowInsecureUpstream permits http:// upstreams (dev only). Set via validate option.
	AllowInsecureUpstream bool `yaml:"-" json:"-"`
}

// DefaultUpstreamHosts are known public registry hosts used when allowlist hosts is empty.
var DefaultUpstreamHosts = []string{
	"registry-1.docker.io",
	"registry.docker.io",
	"auth.docker.io",
	"ghcr.io",
	"gcr.io",
	"k8s.gcr.io",
	"registry.k8s.io",
	"quay.io",
	"mcr.microsoft.com",
	"nvcr.io",
	"docker.elastic.co",
	"public.ecr.aws",
}

var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// ExpandEnv replaces ${VAR} placeholders with environment values.
// Unset variables expand to empty string.
func ExpandEnv(s string) string {
	return envPattern.ReplaceAllStringFunc(s, func(m string) string {
		name := envPattern.FindStringSubmatch(m)[1]
		return os.Getenv(name)
	})
}

// Load reads, expands env placeholders, normalizes, and validates a YAML config.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	expanded := ExpandEnv(string(data))
	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	Normalize(&cfg)
	if err := Validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Normalize fills defaults that are not expressible via zero values.
func Normalize(cfg *Config) {
	if cfg.Server.Listen == "" {
		cfg.Server.Listen = ":5000"
	}
	if cfg.Server.AdminListen == "" {
		cfg.Server.AdminListen = "127.0.0.1:5001"
	}
	if cfg.Server.ReadTimeout == 0 {
		cfg.Server.ReadTimeout = 60
	}
	// WriteTimeout 0 = unlimited (required for large blob streams).
	if cfg.Server.IdleTimeout == 0 {
		cfg.Server.IdleTimeout = 120
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "normal"
	}
	if cfg.AccessControl.Mode == "" {
		cfg.AccessControl.Mode = ACLModeOff
	}
	if cfg.RateLimit.PerIPRPS <= 0 {
		cfg.RateLimit.PerIPRPS = 20
	}
	if cfg.RateLimit.PerIPBurst <= 0 {
		cfg.RateLimit.PerIPBurst = 40
	}
	if cfg.Admin.TokenEnv == "" {
		cfg.Admin.TokenEnv = "PROXY_ADMIN_TOKEN"
	}
	if cfg.Storage.Driver == "" {
		cfg.Storage.Driver = "sqlite"
	}
	if cfg.Storage.DSN == "" {
		cfg.Storage.DSN = "file:data/proxy.db?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	}
	if cfg.Storage.EventRetentionDays <= 0 {
		cfg.Storage.EventRetentionDays = 30
	}
	if cfg.Storage.AggregateRetentionDays <= 0 {
		cfg.Storage.AggregateRetentionDays = 365
	}
	// Metrics default off in Normalize only when zero — example enables explicitly.
	// Leave Metrics.Enabled as configured (default false).
	if cfg.PullAuth.Mode == "" {
		cfg.PullAuth.Mode = PullAuthOff
	}
	if cfg.PullAuth.Realm == "" {
		cfg.PullAuth.Realm = "easy-docker-proxy"
	}
	if cfg.UpstreamAllowlist.Enabled == nil {
		t := true
		cfg.UpstreamAllowlist.Enabled = &t
	}
	if len(cfg.UpstreamAllowlist.Hosts) == 0 && cfg.UpstreamAllowlist.IsEnabled() {
		cfg.UpstreamAllowlist.Hosts = append([]string(nil), DefaultUpstreamHosts...)
	}
	// Default true when field omitted in YAML (zero value is false for bool).
	// StatsRequireAuth is true in example; treat zero as true by checking with pointer
	// — keep simple: always require auth unless explicitly false would need *bool.
	// Example has stats_require_auth: true. For security default true when unset:
	// we cannot distinguish unset vs false with plain bool; document that false must be set.
	// Prefer fail-closed: if not present, yaml gives false. Force true unless
	// we use a custom approach. Our example sets true; Normalize forces true for M1.
	if !cfg.Admin.StatsRequireAuth {
		// Keep user-set false only if they wrote false intentionally — for M1
		// always require auth (fail-closed). Override zero-value false → true.
		cfg.Admin.StatsRequireAuth = true
	}
	for i := range cfg.Registries {
		r := &cfg.Registries[i]
		if r.Auth.Type == "" {
			r.Auth.Type = AuthToken
		}
		if r.TokenCacheTTL <= 0 {
			r.TokenCacheTTL = 3600
		}
		if r.Enabled == nil {
			t := true
			r.Enabled = &t
		}
	}
}

// Validate performs fail-closed sanity checks.
func Validate(cfg *Config) error {
	if len(cfg.Registries) == 0 {
		return fmt.Errorf("at least one registry is required")
	}
	if err := validateAccessControl(&cfg.AccessControl); err != nil {
		return err
	}
	for _, cidr := range cfg.TrustedProxies {
		if err := checkIPOrCIDR(cidr); err != nil {
			return fmt.Errorf("trusted_proxies: %w", err)
		}
	}
	names := make(map[string]bool)
	pathPrefixOwner := make(map[string]string) // prefix -> registry name
	for i := range cfg.Registries {
		r := &cfg.Registries[i]
		if r.Name == "" {
			return fmt.Errorf("registry missing name")
		}
		if names[r.Name] {
			return fmt.Errorf("duplicate registry name: %s", r.Name)
		}
		names[r.Name] = true
		// Host and/or path_prefixes required (single-domain hybrid uses path_prefixes).
		if len(r.Hosts) == 0 && len(r.PathPrefixes) == 0 {
			return fmt.Errorf("registry %s needs hosts and/or path_prefixes", r.Name)
		}
		for j, p := range r.PathPrefixes {
			p = strings.ToLower(strings.TrimSpace(p))
			p = strings.Trim(p, "/")
			if p == "" {
				return fmt.Errorf("registry %s path_prefixes[%d] empty", r.Name, j)
			}
			if other, ok := pathPrefixOwner[p]; ok {
				return fmt.Errorf("path_prefix %q used by both %s and %s", p, other, r.Name)
			}
			pathPrefixOwner[p] = r.Name
			r.PathPrefixes[j] = p
		}
		if r.Upstream == "" {
			return fmt.Errorf("registry %s missing upstream", r.Name)
		}
		u, err := url.Parse(r.Upstream)
		if err != nil {
			return fmt.Errorf("registry %s upstream not a valid URL: %w", r.Name, err)
		}
		switch strings.ToLower(u.Scheme) {
		case "https":
			// ok
		case "http":
			if !cfg.AllowInsecureUpstream {
				return fmt.Errorf("registry %s upstream must use https (set AllowInsecureUpstream for dev)", r.Name)
			}
		default:
			return fmt.Errorf("registry %s upstream has unsupported scheme %q", r.Name, u.Scheme)
		}
		if u.Host == "" {
			return fmt.Errorf("registry %s upstream missing host", r.Name)
		}
		if err := validateUpstreamHost(cfg, u.Hostname()); err != nil {
			return fmt.Errorf("registry %s: %w", r.Name, err)
		}
		switch r.Auth.Type {
		case AuthToken, AuthAnonymous, AuthBasic:
		default:
			return fmt.Errorf("registry %s auth.type invalid: %s", r.Name, r.Auth.Type)
		}
	}
	if cfg.Default != "" && !names[cfg.Default] {
		return fmt.Errorf("default registry %q not found", cfg.Default)
	}
	// admin_listen non-loopback requires strong token (checked at runtime via env).
	if !isLoopbackAddr(cfg.Server.AdminListen) {
		tokenEnv := cfg.Admin.TokenEnv
		if tokenEnv == "" {
			tokenEnv = "PROXY_ADMIN_TOKEN"
		}
		if strings.TrimSpace(os.Getenv(tokenEnv)) == "" {
			return fmt.Errorf("admin_listen %q is not loopback; %s must be set", cfg.Server.AdminListen, tokenEnv)
		}
	}
	switch cfg.LogLevel {
	case "quiet", "normal", "debug":
	default:
		return fmt.Errorf("log_level invalid: %q (quiet|normal|debug)", cfg.LogLevel)
	}
	switch cfg.PullAuth.Mode {
	case PullAuthOff, PullAuthOptional, PullAuthRequired:
	default:
		return fmt.Errorf("pull_auth.mode invalid: %q (off|optional|required)", cfg.PullAuth.Mode)
	}
	return nil
}

func validateUpstreamHost(cfg *Config, host string) error {
	if cfg == nil || !cfg.UpstreamAllowlist.IsEnabled() {
		return nil
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return fmt.Errorf("upstream host empty")
	}
	// Strip port if present (url.Hostname already strips).
	allowed := cfg.UpstreamAllowlist.Hosts
	if len(allowed) == 0 {
		allowed = DefaultUpstreamHosts
	}
	for _, h := range allowed {
		if strings.EqualFold(strings.TrimSpace(h), host) {
			return nil
		}
	}
	return fmt.Errorf("upstream host %q not in upstream_allowlist (SSRF protection); add it or set upstream_allowlist.enabled: false", host)
}

// CheckTokenRealm validates a WWW-Authenticate realm URL before the proxy fetches it.
// Same scheme/allowlist rules as registries[].upstream; the registry's own upstream
// host is always permitted (token endpoints are often on a sibling hostname).
func CheckTokenRealm(cfg *Config, realm, upstreamURL string) error {
	u, err := url.Parse(realm)
	if err != nil || u.Host == "" {
		return fmt.Errorf("token realm is not a valid URL")
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		// ok
	case "http":
		if cfg == nil || !cfg.AllowInsecureUpstream {
			return fmt.Errorf("token realm must use https")
		}
	default:
		return fmt.Errorf("token realm has unsupported scheme %q", u.Scheme)
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host == "" {
		return fmt.Errorf("token realm missing host")
	}
	if upstreamURL != "" {
		if uu, err := url.Parse(upstreamURL); err == nil {
			if strings.EqualFold(strings.TrimSpace(uu.Hostname()), host) {
				return nil
			}
		}
	}
	return validateUpstreamHost(cfg, host)
}

func validateAccessControl(ac *AccessControl) error {
	switch ac.Mode {
	case "", ACLModeOff, ACLModeWhitelist, ACLModeBlacklist:
	default:
		return fmt.Errorf("access_control.mode invalid: %q", ac.Mode)
	}
	if ac.Mode == ACLModeWhitelist && len(ac.Whitelist) == 0 {
		return fmt.Errorf("whitelist mode requires at least one IP/CIDR")
	}
	for _, e := range ac.Whitelist {
		if err := checkIPOrCIDR(e); err != nil {
			return err
		}
	}
	for _, e := range ac.Blacklist {
		if err := checkIPOrCIDR(e); err != nil {
			return err
		}
	}
	return nil
}

func checkIPOrCIDR(raw string) error {
	e := strings.TrimSpace(raw)
	if e == "" {
		return fmt.Errorf("empty IP rule")
	}
	if i := strings.IndexByte(e, '#'); i >= 0 {
		e = strings.TrimSpace(e[:i])
	}
	if e == "" {
		return nil
	}
	if _, _, err := net.ParseCIDR(e); err == nil {
		return nil
	}
	if net.ParseIP(e) != nil {
		return nil
	}
	return fmt.Errorf("invalid IP/CIDR: %q", raw)
}

// isLoopbackAddr reports whether host:port (or :port) binds only loopback.
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// bare ":5001" is all interfaces — not loopback
		if strings.HasPrefix(addr, ":") {
			return false
		}
		host = addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		return false
	}
	ip := net.ParseIP(host)
	if ip != nil {
		return ip.IsLoopback()
	}
	// hostname like localhost
	return strings.EqualFold(host, "localhost")
}

// Clone returns a deep-enough copy for hot reload (string slices shared ok for immutability after load).
func Clone(cfg *Config) *Config {
	if cfg == nil {
		return nil
	}
	cp := *cfg
	cp.TrustedProxies = append([]string(nil), cfg.TrustedProxies...)
	cp.AccessControl.Whitelist = append([]string(nil), cfg.AccessControl.Whitelist...)
	cp.AccessControl.Blacklist = append([]string(nil), cfg.AccessControl.Blacklist...)
	cp.UpstreamAllowlist.Hosts = append([]string(nil), cfg.UpstreamAllowlist.Hosts...)
	if cfg.UpstreamAllowlist.Enabled != nil {
		v := *cfg.UpstreamAllowlist.Enabled
		cp.UpstreamAllowlist.Enabled = &v
	}
	cp.Registries = make([]RegistryConfig, len(cfg.Registries))
	for i := range cfg.Registries {
		cp.Registries[i] = cfg.Registries[i]
		if cfg.Registries[i].Enabled != nil {
			v := *cfg.Registries[i].Enabled
			cp.Registries[i].Enabled = &v
		}
		cp.Registries[i].Hosts = append([]string(nil), cfg.Registries[i].Hosts...)
		cp.Registries[i].PathPrefixes = append([]string(nil), cfg.Registries[i].PathPrefixes...)
	}
	return &cp
}

// MaskedCopy returns a copy safe for JSON export (passwords redacted).
func MaskedCopy(cfg *Config) *Config {
	cp := Clone(cfg)
	for i := range cp.Registries {
		if cp.Registries[i].Auth.Password != "" {
			cp.Registries[i].Auth.Password = "********"
		}
	}
	return cp
}

// AdminToken reads the admin token from the configured environment variable.
func AdminToken(cfg *Config) string {
	env := "PROXY_ADMIN_TOKEN"
	if cfg != nil && cfg.Admin.TokenEnv != "" {
		env = cfg.Admin.TokenEnv
	}
	return strings.TrimSpace(os.Getenv(env))
}
