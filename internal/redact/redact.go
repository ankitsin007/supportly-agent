// Package redact removes obvious PII from log content before envelopes leave
// the customer's network. Mirrors the patterns at
// backend/app/services/pii_redaction.py in the Supportly repo.
//
// What we redact (default):
//   - Email addresses
//   - IPv4 + IPv6 addresses
//   - JWTs (eyJ... base64-shaped tokens with two dots)
//   - Bearer tokens (Authorization: Bearer ...)
//   - API key patterns (sk_*, pk_*, api_key=, X-API-Key headers)
//
// What we DO NOT redact:
//   - Free-form text in error messages (would lose too much signal)
//   - Numeric IDs (rarely PII; needed for fingerprinting)
//
// Customers can extend with their own patterns via Config.Redaction.Custom.
package redact

import "regexp"

// Builtin patterns. Each matches a specific PII shape and replaces with
// a typed marker so the dashboard can show "[email]" instead of garbled text.
var builtin = []struct {
	name    string
	re      *regexp.Regexp
	replace string
}{
	{
		name:    "email",
		re:      regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`),
		replace: "[email]",
	},
	{
		name:    "ipv4",
		re:      regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`),
		replace: "[ip]",
	},
	{
		name:    "ipv6",
		re:      regexp.MustCompile(`\b(?:[0-9a-fA-F]{1,4}:){2,7}[0-9a-fA-F]{1,4}\b`),
		replace: "[ip]",
	},
	{
		name:    "jwt",
		re:      regexp.MustCompile(`eyJ[A-Za-z0-9_\-]+\.eyJ[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+`),
		replace: "[jwt]",
	},
	{
		name:    "bearer",
		re:      regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9_\-\.~+/=]+`),
		replace: "Bearer [token]",
	},
	{
		name:    "stripe_key",
		re:      regexp.MustCompile(`\b(sk|pk|rk)_(test|live)_[A-Za-z0-9]{16,}\b`),
		replace: "[api_key]",
	},
	{
		name:    "generic_api_key",
		re:      regexp.MustCompile(`(?i)(api[_\-]?key["']?\s*[:=]\s*["']?)([A-Za-z0-9_\-]{16,})`),
		replace: "${1}[api_key]",
	},
}

// Redactor applies a fixed pattern set to strings.
type Redactor struct {
	patterns []*regexp.Regexp
	replaces []string
}

// New returns a Redactor configured with the requested builtin patterns
// (by name; empty list = all of them) plus any custom regexes the user
// supplied via config.
func New(enabledNames []string, custom []string) *Redactor {
	enabled := map[string]bool{}
	if len(enabledNames) == 0 {
		for _, p := range builtin {
			enabled[p.name] = true
		}
	} else {
		for _, n := range enabledNames {
			enabled[n] = true
		}
	}
	r := &Redactor{}
	for _, p := range builtin {
		if enabled[p.name] {
			r.patterns = append(r.patterns, p.re)
			r.replaces = append(r.replaces, p.replace)
		}
	}
	for _, c := range custom {
		re, err := regexp.Compile(c)
		if err != nil {
			// Bad regex from config — skip rather than crash. The agent's
			// startup-time validation should catch this earlier.
			continue
		}
		r.patterns = append(r.patterns, re)
		r.replaces = append(r.replaces, "[redacted]")
	}
	return r
}

// Redact applies all patterns to s in order. Returns the cleaned string.
func (r *Redactor) Redact(s string) string {
	for i, p := range r.patterns {
		s = p.ReplaceAllString(s, r.replaces[i])
	}
	return s
}
