package ebpf

import (
	"context"
	"errors"
	"testing"

	"github.com/ankitsin007/supportly-agent/internal/source"
)

func TestNew_DoesNotErrorOnAnyPlatform(t *testing.T) {
	// New is constructor-only; preflight + capability checks run in
	// Start. This guards against future refactors that move work
	// into New and would silently fail cross-compilation.
	s := New(Config{Targets: []string{"/usr/bin/python3"}})
	if s == nil {
		t.Fatal("New returned nil")
	}
	if s.Name() != "ebpf" {
		t.Errorf("Name() = %q want ebpf", s.Name())
	}
}

func TestHealth_FreshSourceIsHealthy(t *testing.T) {
	s := New(Config{})
	h := s.Health()
	// Before Start runs, no error has been recorded.
	if !h.Healthy {
		t.Errorf("fresh Source should be healthy, got %+v", h)
	}
	if h.LinesEmitted != 0 || h.LinesDropped != 0 {
		t.Errorf("counters non-zero on fresh source: %+v", h)
	}
}

func TestStop_BeforeStartIsNoOp(t *testing.T) {
	s := New(Config{})
	if err := s.Stop(); err != nil {
		t.Errorf("Stop before Start should be no-op, got: %v", err)
	}
}

func TestStart_RecordsErrorOnPlatformWithoutSupport(t *testing.T) {
	// On macOS / Windows: ErrUnsupported.
	// On Linux as non-root: preflight failure.
	// On Linux as root: "uprobe attach not yet implemented".
	// In every case, Start returns non-nil error AND Health surfaces it.
	s := New(Config{})
	out := make(chan source.RawLog, 1)
	err := s.Start(context.Background(), out)
	if err == nil {
		t.Fatal("Start must error until per-language uprobe attach is implemented")
	}
	h := s.Health()
	if h.Healthy {
		t.Error("Health.Healthy should be false after Start error")
	}
	if h.LastError == "" {
		t.Error("Health.LastError should be populated after Start error")
	}
}

func TestErrUnsupported_HasNonEmptyMessage(t *testing.T) {
	if ErrUnsupported.Error() == "" {
		t.Error("ErrUnsupported should have a non-empty message")
	}
	if !errors.Is(ErrUnsupported, ErrUnsupported) {
		t.Error("ErrUnsupported should match itself via errors.Is")
	}
}
