// Package acl provides IP allow/deny matching for the data plane.
// See .ai/01_DESIGN.md §4 and .ai/03_SECURITY.md.
package acl

import (
	"net"
	"strings"

	"github.com/alex_wuyh/easy-docker-proxy/internal/config"
)

// Matcher holds compiled IP rules for whitelist/blacklist modes.
type Matcher struct {
	Mode      config.AccessControlMode
	Whitelist []*net.IPNet
	Blacklist []*net.IPNet
	Invalid   []string
}

// Build compiles AccessControl into a Matcher.
// Unknown/empty mode yields a disabled matcher (allow all).
func Build(ac *config.AccessControl) *Matcher {
	if ac == nil {
		return &Matcher{Mode: config.ACLModeOff}
	}
	switch ac.Mode {
	case config.ACLModeWhitelist, config.ACLModeBlacklist:
		// ok
	case config.ACLModeOff, "":
		return &Matcher{Mode: config.ACLModeOff}
	default:
		return &Matcher{Mode: config.ACLModeOff}
	}
	w, wi := parseIPRules(ac.Whitelist)
	b, bi := parseIPRules(ac.Blacklist)
	return &Matcher{
		Mode:      ac.Mode,
		Whitelist: w,
		Blacklist: b,
		Invalid:   append(append([]string{}, wi...), bi...),
	}
}

func parseIPRules(entries []string) ([]*net.IPNet, []string) {
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

// Allows reports whether clientIP may access the registry data plane.
func (m *Matcher) Allows(ipStr string) bool {
	if m == nil || m.Mode == config.ACLModeOff || m.Mode == "" {
		return true
	}
	raw := net.ParseIP(ipStr)
	if raw == nil {
		// Unknown IP: whitelist denies (fail-closed); blacklist allows.
		return m.Mode != config.ACLModeWhitelist
	}
	ip := raw
	if v4 := raw.To4(); v4 != nil {
		ip = v4
	}
	switch m.Mode {
	case config.ACLModeWhitelist:
		for _, n := range m.Whitelist {
			if n.Contains(ip) {
				return true
			}
		}
		return false
	case config.ACLModeBlacklist:
		for _, n := range m.Blacklist {
			if n.Contains(ip) {
				return false
			}
		}
		return true
	default:
		return true
	}
}
