// Package ebpf implements an exception-capturing source backed by
// kernel-level uprobes. Linux-only (kernel ≥ 5.4 required for the
// uprobe.Open API in cilium/ebpf, ≥ 4.18 with adjustments). The
// real attach code lives in ebpf_linux.go behind a build tag; this
// file contains the platform-portable types + the New() entry-point
// that callers (config dispatch in main.go) use unconditionally.
//
// Why this exists at all on non-Linux: the agent's binary is built
// for darwin and linux from the same source tree (see .goreleaser.yaml).
// A pure-Linux source would break cross-compilation. The build-tag
// split below resolves it: ebpf_linux.go provides the real impl on
// Linux; ebpf_other.go provides a stub everywhere else that fails
// Start() with a clear "unsupported platform" error.
//
// Uprobe targets (M5 Week 3 ships the framework; per-language probes
// land in subsequent PRs each on their own Linux box):
//   - Python:  PyErr_Display + _PyErr_Display in libpython3.so
//   - JVM:     JVMTI ExceptionEvent (alternative: bytecode rewriting)
//   - Node:    v8::Isolate::SetCaptureStackTraceForUncaughtExceptions
//   - Go:      runtime.gopanic (binary-agnostic; great for self-host)
//   - Ruby:    rb_exc_raise in libruby.so
//
// Each target maps onto our internal RawLog so the existing parsers
// pick them up without per-language plumbing.
package ebpf

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ankitsin007/supportly-agent/internal/source"
)

// Config carries per-target tuning.
type Config struct {
	// Targets is the list of exec paths or library paths to attach
	// uprobes to. Examples:
	//   "/usr/bin/python3.11"
	//   "/usr/lib/x86_64-linux-gnu/libruby-3.0.so.3.0"
	//   "/proc/<pid>/exe"  (attach to a specific running process)
	// The Linux impl resolves each path's symbol table and attaches
	// the per-language uprobe set defined in ebpf_linux.go.
	Targets []string

	// Languages restricts which uprobe sets to attach. Default = all
	// supported. Useful for partial deployments where only one runtime
	// is in use.
	Languages []string
}

// Source is the platform-portable shell. The actual attach machinery
// lives in:
//   - ebpf_linux.go     (build tag: linux) — real cilium/ebpf code
//   - ebpf_other.go     (build tag: !linux) — stub returning ErrUnsupported
type Source struct {
	cfg Config

	emitted atomic.Uint64
	dropped atomic.Uint64

	mu        sync.Mutex
	lastErr   string
	lastErrAt time.Time

	// Filled by impl.start() on Linux. Nil on other platforms.
	impl ebpfImpl
}

// ebpfImpl is the platform-specific extension point. ebpf_linux.go
// provides a struct that implements it; ebpf_other.go provides a
// no-op type that returns ErrUnsupported.
type ebpfImpl interface {
	start(s *Source, out chan<- source.RawLog) error
	stop() error
}

// ErrUnsupported is returned by Start() on non-Linux platforms.
// Distinct error type so main.go can demote it to a warning instead
// of failing the agent's whole startup.
var ErrUnsupported = fmt.Errorf("ebpf: unsupported on %s/%s — Linux only", runtime.GOOS, runtime.GOARCH)

// New constructs a Source. Callers pass the same struct on every
// platform; Start() is what platform-checks.
func New(cfg Config) *Source {
	return &Source{cfg: cfg}
}

// Name implements source.Source.
func (s *Source) Name() string { return "ebpf" }

// Health implements source.Source.
func (s *Source) Health() source.Health {
	s.mu.Lock()
	defer s.mu.Unlock()
	return source.Health{
		Healthy:      s.lastErr == "",
		LinesEmitted: s.emitted.Load(),
		LinesDropped: s.dropped.Load(),
		LastError:    s.lastErr,
		LastErrorAt:  s.lastErrAt,
	}
}

// Stop implements source.Source.
func (s *Source) Stop() error {
	if s.impl == nil {
		return nil
	}
	return s.impl.stop()
}

func (s *Source) recordErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastErr = err.Error()
	s.lastErrAt = time.Now().UTC()
}
