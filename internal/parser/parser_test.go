package parser

import "testing"

func TestLayered_FirstNonNilWins(t *testing.T) {
	pipeline := &Layered{
		Parsers: []Parser{
			JSON{},
			Fallback{},
		},
	}

	// JSON line — should match JSON layer, not fall through to Fallback.
	jsonLine := `{"level":"error","message":"db down"}`
	env := pipeline.Parse(rawLine(jsonLine), "p")
	if env == nil {
		t.Fatal("expected envelope")
	}
	if env.Tags["parser"] != "json" {
		t.Errorf("parser tag = %v, want json", env.Tags["parser"])
	}

	// Plain text with ERROR — JSON returns nil, Fallback catches.
	plainLine := "2026-04-26 ERROR boom"
	env = pipeline.Parse(rawLine(plainLine), "p")
	if env == nil {
		t.Fatal("expected fallback match")
	}
	if env.Tags["parser"] != "fallback" {
		t.Errorf("parser tag = %v, want fallback", env.Tags["parser"])
	}

	// Pure noise — no parser matches.
	noise := "INFO healthcheck"
	if env := pipeline.Parse(rawLine(noise), "p"); env != nil {
		t.Errorf("expected nil, got %+v", env)
	}
}

func TestLayered_MergesSourceTags(t *testing.T) {
	pipeline := &Layered{Parsers: []Parser{Fallback{}}}
	env := pipeline.Parse(rawLine("ERROR x"), "p")
	if env == nil {
		t.Fatal("expected envelope")
	}
	if env.Tags["log_source"] != "file" {
		t.Errorf("log_source tag = %v", env.Tags["log_source"])
	}
	if env.Tags["file_path"] != "/var/log/test.log" {
		t.Errorf("file_path tag = %v", env.Tags["file_path"])
	}
}
