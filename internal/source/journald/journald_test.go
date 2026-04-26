package journald

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ankitsin007/supportly-agent/internal/source"
)

// fakeJournalctl writes a tiny shell script that prints fixture JSON lines
// then sleeps. We point Source.JournalctlPath at it to avoid needing real
// systemd in CI.
func fakeJournalctl(t *testing.T, lines []string) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "journalctl.sh")
	body := "#!/bin/sh\n"
	for _, l := range lines {
		body += "echo '" + strings.ReplaceAll(l, "'", "'\\''") + "'\n"
	}
	body += "sleep 5\n"
	if err := os.WriteFile(script, []byte(body), 0755); err != nil {
		t.Fatal(err)
	}
	return script
}

func TestSource_EmitsParsedEntries(t *testing.T) {
	fake := fakeJournalctl(t, []string{
		`{"_SYSTEMD_UNIT":"nginx.service","MESSAGE":"upstream timed out","_HOSTNAME":"box01","PRIORITY":"3"}`,
		`{"_SYSTEMD_UNIT":"systemd-logind.service","MESSAGE":"noise"}`, // excluded
		`{"_SYSTEMD_UNIT":"app.service","MESSAGE":"ERROR connection refused","PRIORITY":"3"}`,
	})

	s := New(nil)
	s.JournalctlPath = fake

	out := make(chan source.RawLog, 10)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = s.Start(ctx, out) }()

	got := drain(out, 2, 4*time.Second)
	if len(got) != 2 {
		t.Fatalf("expected 2 emitted entries, got %d: %+v", len(got), got)
	}
	if got[0].Tags["systemd_unit"] != "nginx.service" {
		t.Errorf("unit = %q", got[0].Tags["systemd_unit"])
	}
	if got[0].Tags["hostname"] != "box01" {
		t.Errorf("hostname = %q", got[0].Tags["hostname"])
	}
	if got[0].Line != "upstream timed out" {
		t.Errorf("line = %q", got[0].Line)
	}
	if got[1].Tags["systemd_unit"] != "app.service" {
		t.Errorf("second unit = %q", got[1].Tags["systemd_unit"])
	}
}

func TestSource_SkipsBinaryPayloads(t *testing.T) {
	// Real journald sometimes emits MESSAGE as an array of bytes for binary
	// content. Make sure we just skip those rather than crashing.
	fake := fakeJournalctl(t, []string{
		`{"_SYSTEMD_UNIT":"app.service","MESSAGE":[1,2,3]}`,
		`{"_SYSTEMD_UNIT":"app.service","MESSAGE":"ok line"}`,
	})

	s := New(nil)
	s.JournalctlPath = fake
	out := make(chan source.RawLog, 5)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = s.Start(ctx, out) }()

	got := drain(out, 1, 4*time.Second)
	if len(got) != 1 || got[0].Line != "ok line" {
		t.Fatalf("expected 1 'ok line' entry, got %+v", got)
	}
}

func TestParseRealtimeUS(t *testing.T) {
	t1, ok := parseRealtimeUS("1714128000000000")
	if !ok || t1.Unix() != 1714128000 {
		t.Errorf("parseRealtimeUS got %v ok=%v", t1, ok)
	}
	if _, ok := parseRealtimeUS("not-a-number"); ok {
		t.Errorf("expected false for non-numeric")
	}
}

func TestIsExcluded(t *testing.T) {
	cases := map[string]bool{
		"systemd-logind.service":   true,
		"systemd-resolved.service": true,
		"dbus.service":             true,
		"polkit.service":           true,
		"nginx.service":            false,
		"app.service":              false,
		"":                         false,
	}
	for unit, want := range cases {
		if got := isExcluded(unit); got != want {
			t.Errorf("isExcluded(%q) = %v, want %v", unit, got, want)
		}
	}
}

// drain waits up to timeout for n items from the channel.
func drain(ch <-chan source.RawLog, n int, timeout time.Duration) []source.RawLog {
	var got []source.RawLog
	deadline := time.After(timeout)
	for len(got) < n {
		select {
		case x := <-ch:
			got = append(got, x)
		case <-deadline:
			return got
		}
	}
	return got
}
