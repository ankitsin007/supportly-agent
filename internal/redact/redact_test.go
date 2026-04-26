package redact

import (
	"strings"
	"testing"
)

func TestRedact_Email(t *testing.T) {
	r := New(nil, nil)
	got := r.Redact("user alice@example.com tried to log in")
	if strings.Contains(got, "alice@example.com") {
		t.Errorf("email not redacted: %q", got)
	}
	if !strings.Contains(got, "[email]") {
		t.Errorf("no marker: %q", got)
	}
}

func TestRedact_IP(t *testing.T) {
	r := New(nil, nil)
	got := r.Redact("from 192.168.1.42 connection refused")
	if strings.Contains(got, "192.168.1.42") {
		t.Errorf("ip not redacted: %q", got)
	}
}

func TestRedact_JWT(t *testing.T) {
	r := New(nil, nil)
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4ifQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	got := r.Redact("Bearer " + jwt)
	if strings.Contains(got, "eyJhbGc") {
		t.Errorf("jwt not redacted: %q", got)
	}
}

func TestRedact_StripeKey(t *testing.T) {
	r := New(nil, nil)
	// Built at runtime so GitHub's secret scanner doesn't flag the source.
	fakeKey := "sk" + "_live_" + strings.Repeat("a", 24)
	got := r.Redact("key " + fakeKey + " leaked")
	if strings.Contains(got, fakeKey) {
		t.Errorf("stripe key not redacted: %q", got)
	}
}

func TestRedact_Selective(t *testing.T) {
	// Only enable email — IP should pass through.
	r := New([]string{"email"}, nil)
	got := r.Redact("from 10.0.0.1 user a@b.com")
	if !strings.Contains(got, "10.0.0.1") {
		t.Errorf("ip should be intact when not enabled: %q", got)
	}
	if strings.Contains(got, "a@b.com") {
		t.Errorf("email should be redacted: %q", got)
	}
}

func TestRedact_Custom(t *testing.T) {
	r := New([]string{}, []string{`ssn=\d{3}-\d{2}-\d{4}`})
	// Empty enabledNames=[] means "use defaults" via the New() contract,
	// so we instead pass a name that doesn't exist to disable builtins.
	r2 := New([]string{"none"}, []string{`ssn=\d{3}-\d{2}-\d{4}`})
	got := r2.Redact("ssn=123-45-6789")
	if strings.Contains(got, "123-45-6789") {
		t.Errorf("custom not applied: %q", got)
	}
	// Sanity: the first call with empty patterns also redacts.
	if !strings.Contains(r.Redact("ssn=123-45-6789"), "[redacted]") {
		t.Errorf("custom on default-builtin set still redacts")
	}
}
