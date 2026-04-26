package buffer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ankitsin007/supportly-agent/internal/envelope"
)

func mkEnv(msg string) *envelope.Envelope {
	e := envelope.New("p", "test")
	e.Message = msg
	return e
}

func TestBuffer_EnqueueDrainFIFO(t *testing.T) {
	b, err := New(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}

	for _, m := range []string{"first", "second", "third"} {
		if _, err := b.Enqueue(mkEnv(m)); err != nil {
			t.Fatalf("enqueue %s: %v", m, err)
		}
	}
	if got := b.Len(); got != 3 {
		t.Errorf("len = %d, want 3", got)
	}

	var seen []string
	n, err := b.Drain(func(env *envelope.Envelope) error {
		seen = append(seen, env.Message)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("drained = %d, want 3", n)
	}
	if got := strings.Join(seen, ","); got != "first,second,third" {
		t.Errorf("FIFO order broken: %s", got)
	}
	if b.Len() != 0 {
		t.Errorf("buffer not empty after drain")
	}
}

func TestBuffer_DrainStopsOnError(t *testing.T) {
	b, _ := New(t.TempDir(), 1<<20)
	for _, m := range []string{"a", "b", "c"} {
		_, _ = b.Enqueue(mkEnv(m))
	}
	calls := 0
	stopAt := errors.New("simulated network failure")
	n, err := b.Drain(func(env *envelope.Envelope) error {
		calls++
		if env.Message == "b" {
			return stopAt
		}
		return nil
	})
	if !errors.Is(err, stopAt) {
		t.Errorf("expected stopAt error, got %v", err)
	}
	// We delivered 'a' (drained=1), then 'b' failed.
	if n != 1 {
		t.Errorf("drained = %d, want 1", n)
	}
	if b.Len() != 2 {
		t.Errorf("expected 2 left, got %d", b.Len())
	}
	if calls != 2 {
		t.Errorf("callback called %d times, want 2", calls)
	}
}

func TestBuffer_EvictsOldestWhenAtCap(t *testing.T) {
	dir := t.TempDir()
	// One envelope marshals to ~150 bytes; cap at 500 fits ~3 entries.
	b, _ := New(dir, 500)
	for i := 0; i < 10; i++ {
		_, err := b.Enqueue(mkEnv(strings.Repeat("x", 50)))
		if err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	if b.Bytes() > 500 {
		t.Errorf("bytes = %d > cap 500", b.Bytes())
	}
	if b.Len() == 0 {
		t.Errorf("eviction was too aggressive")
	}
	if b.Len() >= 10 {
		t.Errorf("expected eviction, got %d entries", b.Len())
	}
}

func TestBuffer_PersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	b1, _ := New(dir, 1<<20)
	for _, m := range []string{"x", "y"} {
		_, _ = b1.Enqueue(mkEnv(m))
	}

	// Reopen.
	b2, err := New(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if b2.Len() != 2 {
		t.Errorf("reopen lost entries: got %d, want 2", b2.Len())
	}
	// Add one more — sequence should not collide.
	if _, err := b2.Enqueue(mkEnv("z")); err != nil {
		t.Errorf("post-reopen enqueue: %v", err)
	}
	if b2.Len() != 3 {
		t.Errorf("got %d entries after add", b2.Len())
	}
}

func TestBuffer_SkipsCorruptEntries(t *testing.T) {
	dir := t.TempDir()
	b, _ := New(dir, 1<<20)
	_, _ = b.Enqueue(mkEnv("good"))
	// Drop a corrupt file that looks like one of ours.
	corrupt := filepath.Join(dir, "00000000000000000099.json")
	if err := os.WriteFile(corrupt, []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _ = b.Enqueue(mkEnv("good2"))

	var seen []string
	_, _ = b.Drain(func(e *envelope.Envelope) error {
		seen = append(seen, e.Message)
		return nil
	})
	// Corrupt entry must have been skipped without blocking the drain.
	if got := strings.Join(seen, ","); got != "good,good2" {
		t.Errorf("got %q, want good,good2 (corrupt entry should be silently dropped)", got)
	}
}

func TestBuffer_IgnoresUnrecognizedFiles(t *testing.T) {
	dir := t.TempDir()
	// Pre-place a non-queue file.
	_ = os.WriteFile(filepath.Join(dir, "README.txt"), []byte("hi"), 0o644)
	b, err := New(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if b.Len() != 0 {
		t.Errorf("non-queue file leaked into Len()")
	}
}

func TestBuffer_InfoReportsState(t *testing.T) {
	b, _ := New(t.TempDir(), 5000)
	_, _ = b.Enqueue(mkEnv("hello"))
	info := b.Info()
	if info.Entries != 1 {
		t.Errorf("entries = %d", info.Entries)
	}
	if info.Bytes <= 0 {
		t.Errorf("bytes = %d", info.Bytes)
	}
	if info.Cap != 5000 {
		t.Errorf("cap = %d", info.Cap)
	}
}
