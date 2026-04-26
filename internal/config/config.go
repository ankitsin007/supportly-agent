// Package config loads the agent's runtime configuration from a YAML file
// (path overridable via flag) plus environment variable overrides.
//
// Precedence (highest first):
//  1. Environment variables (SUPPORTLY_*)
//  2. YAML file (/etc/supportly/agent.yaml or --config flag)
//  3. Built-in defaults
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the full agent configuration tree.
type Config struct {
	ProjectID   string `yaml:"project_id"`
	APIEndpoint string `yaml:"api_endpoint"`
	APIKey      string `yaml:"api_key"`

	Sources    []SourceConfig  `yaml:"sources"`
	Redaction  RedactionConfig `yaml:"redaction"`
	RateLimits RateLimitConfig `yaml:"rate_limits"`
	TLS        TLSConfig       `yaml:"tls"`
	Buffer     BufferConfig    `yaml:"buffer"`
}

// TLSConfig mirrors tlsconfig.Options. See docs §10.6 for the tier model.
type TLSConfig struct {
	CABundlePath   string `yaml:"ca_bundle"`
	CertPin        string `yaml:"cert_pin"`
	ClientCertFile string `yaml:"client_cert"`
	ClientKeyFile  string `yaml:"client_key"`
	SkipVerify     bool   `yaml:"skip_verify"`
	Acknowledged   bool   `yaml:"i_understand_this_is_insecure"`
}

// SourceConfig is a discriminated union by Type.
// Type values: "file", "docker", "journald", "kubernetes".
type SourceConfig struct {
	Type    string `yaml:"type"`
	Enabled bool   `yaml:"enabled"`

	// type=file
	Paths []string `yaml:"paths,omitempty"`

	// type=docker
	Socket            string   `yaml:"socket,omitempty"`
	ExcludeContainers []string `yaml:"exclude_containers,omitempty"`

	// type=journald
	Units []string `yaml:"units,omitempty"`

	// type=kubernetes
	PodLogRoot        string   `yaml:"pod_log_root,omitempty"`
	ExcludeNamespaces []string `yaml:"exclude_namespaces,omitempty"`
}

// RedactionConfig controls PII stripping before envelopes leave the host.
type RedactionConfig struct {
	Enabled  bool     `yaml:"enabled"`
	Patterns []string `yaml:"patterns,omitempty"` // names from redact builtin set
	Custom   []string `yaml:"custom,omitempty"`   // arbitrary regexes
}

// RateLimitConfig caps the per-source event rate.
type RateLimitConfig struct {
	PerSourceEPS int `yaml:"per_source_eps"`
	Burst        int `yaml:"burst"`
}

// BufferConfig controls the on-disk envelope buffer used as fallback when
// the network is down or Supportly returns 5xx.
type BufferConfig struct {
	// Enabled defaults to true. Set false to disable buffering entirely
	// (failed events are dropped immediately).
	Enabled bool `yaml:"enabled"`

	// Path is where the buffer files live. Default
	// /var/lib/supportly/agent/queue (must be writable).
	Path string `yaml:"path"`

	// MaxDiskMB caps total disk usage. Oldest entries are evicted when
	// a new write would exceed it. Default 500.
	MaxDiskMB int `yaml:"max_disk_mb"`

	// ReplayIntervalSeconds — how often to retry buffered entries.
	// Default 30.
	ReplayIntervalSeconds int `yaml:"replay_interval_seconds"`
}

// Load reads a YAML file (if path != "") and applies env-var overrides.
// Returns a fully-populated Config or an error if required fields are missing.
func Load(path string) (*Config, error) {
	cfg := defaults()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config %s: %w", path, err)
		}
	}

	applyEnvOverrides(cfg)

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func defaults() *Config {
	return &Config{
		APIEndpoint: "https://ingest.supportly.io",
		Redaction: RedactionConfig{
			Enabled: true,
			// Empty Patterns list => all builtin patterns active.
		},
		RateLimits: RateLimitConfig{
			PerSourceEPS: 100,
			Burst:        500,
		},
		Buffer: BufferConfig{
			Enabled:               true,
			Path:                  "/var/lib/supportly/agent/queue",
			MaxDiskMB:             500,
			ReplayIntervalSeconds: 30,
		},
	}
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("SUPPORTLY_PROJECT_ID"); v != "" {
		cfg.ProjectID = v
	}
	if v := os.Getenv("SUPPORTLY_API_ENDPOINT"); v != "" {
		cfg.APIEndpoint = v
	}
	if v := os.Getenv("SUPPORTLY_API_KEY"); v != "" {
		cfg.APIKey = v
	}
}

func (c *Config) validate() error {
	if c.ProjectID == "" {
		return fmt.Errorf("project_id is required (set in YAML or SUPPORTLY_PROJECT_ID)")
	}
	if c.APIKey == "" {
		return fmt.Errorf("api_key is required (set in YAML or SUPPORTLY_API_KEY)")
	}
	if c.APIEndpoint == "" {
		return fmt.Errorf("api_endpoint must not be empty")
	}
	if c.RateLimits.PerSourceEPS < 0 || c.RateLimits.Burst < 0 {
		return fmt.Errorf("rate_limits values must be non-negative")
	}
	return nil
}
