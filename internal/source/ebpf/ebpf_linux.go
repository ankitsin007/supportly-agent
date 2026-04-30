//go:build linux

package ebpf

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/ankitsin007/supportly-agent/internal/source"
)

// linuxImpl is the Linux ebpfImpl. M5 Week 3 ships the platform-check
// + capability-probe scaffolding; per-language uprobe attach code
// (Python PyErr_Display, JVM JVMTI, Go runtime.gopanic, Ruby
// rb_exc_raise, Node V8 stack-trace hook) lands in subsequent PRs
// — each one needs a Linux dev environment with the relevant runtime
// installed to validate the attach + verify the event payload shape.
type linuxImpl struct {
	cancel context.CancelFunc
}

// preflight runs the cheap checks that decide whether eBPF can attach
// at all. Failures here surface as Source.lastErr so /healthz reports
// "ebpf unavailable: <reason>" instead of silently doing nothing.
func preflight() error {
	// 1) Kernel version. cilium/ebpf's uprobe.Open requires ≥ 5.4
	//    for the modern API we'll target. Older kernels need the
	//    bpf() syscall directly + manual kprobe attachment, out of
	//    scope for now.
	if v, err := readKernelMajorMinor(); err != nil {
		return fmt.Errorf("kernel version probe failed: %w", err)
	} else if v < 504 {
		return fmt.Errorf("kernel %d.%d is too old (need ≥ 5.4)", v/100, v%100)
	}

	// 2) Capability check — uprobes need CAP_BPF (kernel ≥ 5.8) or
	//    CAP_SYS_ADMIN. Detection is best-effort; a real attach will
	//    fail with -EPERM if we lack the capability.
	if !hasBPFCapability() {
		return errors.New("missing CAP_BPF / CAP_SYS_ADMIN — run as root or grant the binary the capability")
	}

	// 3) BPF filesystem mounted? (Many uprobe operations write a pin
	//    to /sys/fs/bpf.) cilium/ebpf will create it if missing on
	//    recent kernels; check for the mount point as a hint.
	if _, err := os.Stat("/sys/fs/bpf"); err != nil {
		return errors.New("/sys/fs/bpf not present — bpffs is not mounted")
	}

	return nil
}

// readKernelMajorMinor returns the kernel version as MAJOR*100+MINOR
// (e.g. 6.5 → 605, 5.4 → 504). Reads /proc/sys/kernel/osrelease
// because uname(2) requires cgo.
func readKernelMajorMinor() (int, error) {
	data, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(data))
	// Format like "6.5.0-25-generic" or "5.15.0-119-generic".
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return 0, fmt.Errorf("unexpected osrelease format: %q", s)
	}
	major, err := atoi(parts[0])
	if err != nil {
		return 0, err
	}
	// minor may have non-digits trailing (e.g. "0-25-generic").
	minorStr := parts[1]
	for i, c := range minorStr {
		if c < '0' || c > '9' {
			minorStr = minorStr[:i]
			break
		}
	}
	minor, err := atoi(minorStr)
	if err != nil {
		return 0, err
	}
	return major*100 + minor, nil
}

func atoi(s string) (int, error) {
	n := 0
	if s == "" {
		return 0, fmt.Errorf("empty numeric string")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("non-digit: %q", s)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

// hasBPFCapability is a coarse check — root always passes; non-root
// could still pass via filesystem capabilities (setcap cap_bpf+ep
// /usr/local/bin/supportly-agent), which we don't introspect because
// reading capability sets requires libcap or /proc parsing.
func hasBPFCapability() bool {
	return os.Geteuid() == 0
}

func (i *linuxImpl) start(s *Source, _ chan<- source.RawLog) error {
	if err := preflight(); err != nil {
		s.recordErr(err)
		return fmt.Errorf("ebpf: preflight: %w", err)
	}
	// TODO(M5 Week 4+): attach per-language uprobes here. Targets:
	//   Python  → PyErr_Display in libpython3.so
	//   JVM     → JVMTI ExceptionEvent
	//   Node    → v8::Isolate::SetCaptureStackTraceForUncaughtExceptions
	//   Go      → runtime.gopanic (binary-agnostic; great for self-host)
	//   Ruby    → rb_exc_raise in libruby.so
	//
	// Library choice: github.com/cilium/ebpf for the modern attach
	// API; eBPF program assembly via bpf2go from .c sources kept in
	// internal/source/ebpf/programs/. Per-language program goes into
	// programs/<lang>.c, codegen runs at `make ebpf-build`.
	//
	// Until then, return ErrUnsupported with a useful hint instead
	// of silently emitting zero events.
	err := errors.New("ebpf source preflight passed but uprobe attach is not yet implemented; see internal/source/ebpf/README.md")
	s.recordErr(err)
	return err
}

func (i *linuxImpl) stop() error {
	if i.cancel != nil {
		i.cancel()
	}
	return nil
}

// Start on Linux: instantiate the real impl and call its start hook.
func (s *Source) Start(_ context.Context, out chan<- source.RawLog) error {
	s.impl = &linuxImpl{}
	return s.impl.start(s, out)
}
