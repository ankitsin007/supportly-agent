package parser

import (
	"testing"
	"time"

	"github.com/ankitsin007/supportly-agent/internal/source"
)

func rawLine(line string) source.RawLog {
	return source.RawLog{
		Source:    "file",
		Timestamp: time.Now().UTC(),
		Line:      line,
		Tags:      map[string]string{"file_path": "/var/log/test.log"},
	}
}

func TestJSON_NestedException(t *testing.T) {
	line := `{"level":"error","message":"db down","exception":{"type":"OperationalError","value":"could not connect","stacktrace":{"frames":[{"filename":"db.py","function":"connect","lineno":42}]}},"timestamp":"2026-04-26T12:00:00Z","environment":"prod","release":"v1.2.3"}`
	env := JSON{}.Parse(rawLine(line), "proj-123")
	if env == nil {
		t.Fatal("expected envelope, got nil")
	}
	if env.ProjectID != "proj-123" {
		t.Errorf("project_id = %q", env.ProjectID)
	}
	if env.Level != "error" {
		t.Errorf("level = %q", env.Level)
	}
	if env.Message != "db down" {
		t.Errorf("message = %q", env.Message)
	}
	if env.Environment != "prod" {
		t.Errorf("environment = %q", env.Environment)
	}
	if env.Release != "v1.2.3" {
		t.Errorf("release = %q", env.Release)
	}
	if env.Exception == nil {
		t.Fatal("expected exception, got nil")
	}
	if env.Exception.Type != "OperationalError" {
		t.Errorf("exception.type = %q", env.Exception.Type)
	}
	if env.Exception.Value != "could not connect" {
		t.Errorf("exception.value = %q", env.Exception.Value)
	}
	if env.Exception.Stacktrace == nil || len(env.Exception.Stacktrace.Frames) != 1 {
		t.Fatalf("expected 1 frame, got %+v", env.Exception.Stacktrace)
	}
	f := env.Exception.Stacktrace.Frames[0]
	if f.Filename != "db.py" || f.Function != "connect" || f.Lineno != 42 {
		t.Errorf("frame = %+v", f)
	}
}

func TestJSON_FlatErrorShape(t *testing.T) {
	// Common Node/Express logger shape — error+stack at top level.
	line := `{"level":"error","msg":"request failed","error":"ENOENT: no such file","logger":"express"}`
	env := JSON{}.Parse(rawLine(line), "p")
	if env == nil {
		t.Fatal("expected envelope")
	}
	if env.Message != "request failed" {
		t.Errorf("message = %q", env.Message)
	}
	if env.Exception == nil || env.Exception.Value != "ENOENT: no such file" {
		t.Errorf("exception = %+v", env.Exception)
	}
	if env.Platform != "node" {
		t.Errorf("platform inference failed: got %q, want node", env.Platform)
	}
}

func TestJSON_GoogleCloudShape(t *testing.T) {
	line := `{"severity":"ERROR","message":"timeout","timestamp":"2026-04-26T12:00:00Z"}`
	env := JSON{}.Parse(rawLine(line), "p")
	if env == nil {
		t.Fatal("expected envelope")
	}
	if env.Level != "error" {
		t.Errorf("level = %q (want error)", env.Level)
	}
	if env.Message != "timeout" {
		t.Errorf("message = %q", env.Message)
	}
}

func TestJSON_SkipsInfoLevel(t *testing.T) {
	// We only ship errors in M1 — INFO must not produce an envelope.
	line := `{"level":"info","message":"healthcheck ok"}`
	env := JSON{}.Parse(rawLine(line), "p")
	if env != nil {
		t.Errorf("expected nil for INFO line, got envelope %+v", env)
	}
}

func TestJSON_RejectsNonJSON(t *testing.T) {
	for _, line := range []string{
		"plain text log line",
		"2026-04-26 ERROR something happened",
		"",
		"{ not valid json",
		`["array", "not", "object"]`,
	} {
		if env := (JSON{}).Parse(rawLine(line), "p"); env != nil {
			t.Errorf("expected nil for %q, got %+v", line, env)
		}
	}
}

func TestJSON_PrefersOwnTimestamp(t *testing.T) {
	line := `{"level":"error","message":"x","timestamp":"2026-01-15T10:30:45Z"}`
	env := JSON{}.Parse(rawLine(line), "p")
	if env == nil {
		t.Fatal("expected envelope")
	}
	want := time.Date(2026, 1, 15, 10, 30, 45, 0, time.UTC)
	if !env.Timestamp.Equal(want) {
		t.Errorf("timestamp = %v, want %v", env.Timestamp, want)
	}
}
