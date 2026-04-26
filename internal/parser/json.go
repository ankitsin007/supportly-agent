// Package parser — JSON layer.
//
// Most modern apps emit structured JSON logs. If the line is valid JSON
// AND has a recognizable error shape, we ship as-is — highest fidelity
// because the app already did the parsing work.
//
// Supported shapes (best-effort):
//   - {"level":"ERROR","message":"...","exception":{"type":"...","value":"...","stacktrace":{"frames":[...]}}}
//   - {"level":"error","msg":"...","error":"...","stack":"..."}
//   - {"severity":"ERROR","message":"..."}  (Google Cloud Logging shape)
//
// Anything not matching one of the above falls through to the next layer.
package parser

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/ankitsin007/supportly-agent/internal/envelope"
	"github.com/ankitsin007/supportly-agent/internal/source"
)

// JSON parses structured log lines.
type JSON struct{}

// Name implements Parser.
func (JSON) Name() string { return "json" }

// Parse implements Parser.
func (JSON) Parse(raw source.RawLog, projectID string) *envelope.Envelope {
	line := strings.TrimSpace(raw.Line)
	if !strings.HasPrefix(line, "{") {
		return nil
	}

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(line), &data); err != nil {
		return nil
	}

	level := normaliseLevel(firstString(data, "level", "severity"))
	if level == "" {
		return nil
	}
	if !isErrorish(level) {
		// Skip INFO/DEBUG/WARN. We only ship errors in M1 — non-error
		// log volume is huge and we'd torch the user's quota.
		return nil
	}

	env := envelope.New(projectID, "")
	env.Level = level
	env.Message = firstString(data, "message", "msg", "log")

	// Timestamp: prefer the log's own timestamp if present and parseable.
	if ts := firstString(data, "timestamp", "ts", "time"); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			env.Timestamp = t.UTC()
		}
	}

	// Exception: nested {"exception": {...}} or flat top-level keys.
	if exc := extractException(data); exc != nil {
		env.Exception = exc
	}

	// Platform inference: best-effort from logger name or a "platform" key.
	env.Platform = inferPlatform(data)

	// Environment, release, server_name if present.
	env.Environment = firstString(data, "environment", "env")
	env.Release = firstString(data, "release", "version", "app_version")
	env.ServerName = firstString(data, "server_name", "hostname", "host")

	return env
}

// firstString returns the first non-empty string value among the given keys.
func firstString(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func normaliseLevel(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "fatal", "critical", "crit":
		return "fatal"
	case "error", "err":
		return "error"
	case "warn", "warning":
		return "warning"
	case "info", "notice":
		return "info"
	case "debug":
		return "debug"
	default:
		return strings.ToLower(s)
	}
}

func isErrorish(level string) bool {
	return level == "fatal" || level == "error"
}

func extractException(data map[string]interface{}) *envelope.Exception {
	if raw, ok := data["exception"].(map[string]interface{}); ok {
		exc := &envelope.Exception{
			Type:  firstString(raw, "type", "class"),
			Value: firstString(raw, "value", "message"),
		}
		if st, ok := raw["stacktrace"].(map[string]interface{}); ok {
			exc.Stacktrace = extractFrames(st)
		}
		if exc.Type != "" || exc.Value != "" {
			return exc
		}
	}
	// Flat shape: top-level "error" + "stack".
	if errStr := firstString(data, "error", "err"); errStr != "" {
		return &envelope.Exception{
			Type:  "Error",
			Value: errStr,
		}
	}
	return nil
}

func extractFrames(st map[string]interface{}) *envelope.Stacktrace {
	rawFrames, ok := st["frames"].([]interface{})
	if !ok {
		return nil
	}
	frames := make([]envelope.Frame, 0, len(rawFrames))
	for _, rf := range rawFrames {
		fm, ok := rf.(map[string]interface{})
		if !ok {
			continue
		}
		f := envelope.Frame{
			Filename:    firstString(fm, "filename", "file"),
			Function:    firstString(fm, "function", "func", "method"),
			ContextLine: firstString(fm, "context_line", "line", "code"),
		}
		if ln, ok := fm["lineno"].(float64); ok {
			f.Lineno = int(ln)
		}
		frames = append(frames, f)
	}
	return &envelope.Stacktrace{Frames: frames}
}

func inferPlatform(data map[string]interface{}) string {
	if p := firstString(data, "platform", "language"); p != "" {
		return p
	}
	if logger := firstString(data, "logger", "logger_name"); logger != "" {
		switch {
		case strings.Contains(logger, "django") || strings.Contains(logger, "fastapi") || strings.Contains(logger, "flask"):
			return "python"
		case strings.Contains(logger, "express") || strings.Contains(logger, "node"):
			return "node"
		}
	}
	return "unknown"
}
