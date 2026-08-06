// Package config loads and validates proxy configuration.
// Full implementation: YAML parse, ${ENV} expansion, fail-closed checks.
// See .ai/01_DESIGN.md §4.
package config

// Config is the top-level configuration root (scaffold stub).
type Config struct {
	// Populated in M1.
}
