package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_YAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	yaml := `
project_id: yaml-project
api_key: yaml-key
api_endpoint: https://example.com/ingest
sources:
  - type: file
    enabled: true
    paths: ["/var/log/app.log"]
`
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProjectID != "yaml-project" || cfg.APIKey != "yaml-key" {
		t.Errorf("config = %+v", cfg)
	}
	if len(cfg.Sources) != 1 || cfg.Sources[0].Type != "file" {
		t.Errorf("sources = %+v", cfg.Sources)
	}
}

func TestLoad_EnvOverridesYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	_ = os.WriteFile(path, []byte("project_id: from-yaml\napi_key: yaml-key\n"), 0644)

	t.Setenv("SUPPORTLY_PROJECT_ID", "from-env")
	t.Setenv("SUPPORTLY_API_KEY", "env-key")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProjectID != "from-env" {
		t.Errorf("env override failed: project_id = %q", cfg.ProjectID)
	}
	if cfg.APIKey != "env-key" {
		t.Errorf("env override failed: api_key = %q", cfg.APIKey)
	}
}

func TestLoad_MissingProjectID(t *testing.T) {
	// Make sure no inherited env vars confuse this case.
	t.Setenv("SUPPORTLY_PROJECT_ID", "")
	t.Setenv("SUPPORTLY_API_KEY", "k")
	if _, err := Load(""); err == nil {
		t.Error("expected error for missing project_id")
	}
}

func TestLoad_DefaultEndpoint(t *testing.T) {
	t.Setenv("SUPPORTLY_PROJECT_ID", "p")
	t.Setenv("SUPPORTLY_API_KEY", "k")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIEndpoint != "https://ingest.supportly.io" {
		t.Errorf("default endpoint = %q", cfg.APIEndpoint)
	}
}
