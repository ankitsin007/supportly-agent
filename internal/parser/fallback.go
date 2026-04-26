// Package parser — fallback layer.
//
// Always-matches safety net. If no higher layer recognized the line but it
// contains an error keyword, we ship a minimal envelope so the user at
// least sees the line in their dashboard. Better to over-report than miss.
package parser

import (
	"strings"

	"github.com/ankitsin007/supportly-agent/internal/envelope"
	"github.com/ankitsin007/supportly-agent/internal/source"
)

// Fallback emits a level=error envelope for any line containing an error
// keyword. Add it LAST in the Layered chain.
type Fallback struct {
	// Keywords (case-insensitive) that mark a line as error-worthy.
	// Default: ["ERROR", "FATAL", "PANIC", "EXCEPTION", "TRACEBACK"].
	Keywords []string
}

// Name implements Parser.
func (Fallback) Name() string { return "fallback" }

// Parse implements Parser.
func (f Fallback) Parse(raw source.RawLog, projectID string) *envelope.Envelope {
	keywords := f.Keywords
	if len(keywords) == 0 {
		keywords = []string{"ERROR", "FATAL", "PANIC", "EXCEPTION", "TRACEBACK"}
	}
	upper := strings.ToUpper(raw.Line)
	matched := false
	for _, kw := range keywords {
		if strings.Contains(upper, kw) {
			matched = true
			break
		}
	}
	if !matched {
		return nil
	}

	env := envelope.New(projectID, "unknown")
	env.Level = "error"
	// Trim very long lines — we ship up to 8KB of message context.
	msg := strings.TrimSpace(raw.Line)
	if len(msg) > 8000 {
		msg = msg[:8000]
	}
	env.Message = msg
	env.Tags["raw_line"] = truncate(raw.Line, 200)
	return env
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
