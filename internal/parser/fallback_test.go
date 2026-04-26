package parser

import (
	"strings"
	"testing"
)

func TestFallback_MatchesErrorKeywords(t *testing.T) {
	cases := []struct {
		name string
		line string
		want bool
	}{
		{"plain ERROR", "2026-04-26 12:00:00 ERROR connection failed", true},
		{"FATAL", "FATAL: out of memory", true},
		{"PANIC", "panic: runtime error: index out of range", true},
		{"EXCEPTION", "Got Exception during request", true},
		{"TRACEBACK keyword", "Traceback (most recent call last):", true},
		{"info-level line", "INFO request 200 ok", false},
		{"debug-level line", "DEBUG starting worker", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := Fallback{}.Parse(rawLine(tc.line), "p")
			if (env != nil) != tc.want {
				t.Errorf("Parse(%q) → %v, want match=%v", tc.line, env, tc.want)
			}
			if env != nil && env.Tags["raw_line"] == "" {
				t.Errorf("expected raw_line tag, got empty")
			}
		})
	}
}

func TestFallback_TruncatesLongMessages(t *testing.T) {
	long := strings.Repeat("x", 10_000) + " ERROR boom"
	env := Fallback{}.Parse(rawLine(long), "p")
	if env == nil {
		t.Fatal("expected envelope")
	}
	if len(env.Message) > 8000 {
		t.Errorf("message length %d exceeds 8000", len(env.Message))
	}
}

func TestFallback_CustomKeywords(t *testing.T) {
	f := Fallback{Keywords: []string{"BOOM"}}
	if env := f.Parse(rawLine("ERROR not in custom keywords"), "p"); env != nil {
		t.Errorf("expected nil — ERROR shouldn't match when only BOOM is configured")
	}
	if env := f.Parse(rawLine("here comes BOOM"), "p"); env == nil {
		t.Errorf("expected match for BOOM")
	}
}
