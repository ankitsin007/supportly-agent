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

	Sources []SourceConfig `yaml:"sources"`
}

// SourceConfig is a discriminated union by Type. M1 supports type=file.
// Paths is only used when Type == "file".
type SourceConfig struct {
	Type    string   `yaml:"type"`
	Paths   []string `yaml:"paths,omitempty"`
	Enabled bool     `yaml:"enabled"`
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
	return nil
}
