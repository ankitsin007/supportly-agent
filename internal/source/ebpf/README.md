# eBPF source

Status: **scaffolding only as of M5 Week 3.** Real per-language uprobe
attach is implemented in subsequent PRs once a Linux dev environment
with each runtime installed is available.

## What ships today

- Platform-portable `Source` struct that compiles on every OS the
  agent targets (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64).
- `Start()` short-circuits with `ErrUnsupported` on non-Linux so the
  agent's main loop can demote it to a warning instead of crashing.
- On Linux, `preflight()` runs three cheap checks:
  1. Kernel version ≥ 5.4 (for cilium/ebpf's modern uprobe API).
  2. CAP_BPF / CAP_SYS_ADMIN (currently coarse: root or bust).
  3. `/sys/fs/bpf` mounted (bpffs).
- If preflight passes, the start hook returns a clear "not yet
  implemented" error rather than silently doing nothing.

## What's missing

The per-language uprobe attach code itself. Each target needs:

| Language | Attach point | Notes |
|---|---|---|
| Python | `PyErr_Display` in `libpython3.so` | Walks frame objects to extract traceback |
| JVM | JVMTI `ExceptionEvent` callback | Bytecode rewriting alternative for AOT-compiled apps |
| Node.js | `v8::Isolate::SetCaptureStackTraceForUncaughtExceptions` | Hook fires before stack trace truncation |
| Go | `runtime.gopanic` | Binary-agnostic; great for self-host indexing of the agent itself |
| Ruby | `rb_exc_raise` in `libruby.so` | Walks `rb_thread_t.ec.cfp` for traceback |

Each requires a Linux dev box with the relevant runtime installed to
validate the attach + verify the event payload. They're tracked as
follow-up PRs (M5.1-M5.5).

## Implementation plan

When ready to add real attach code:

1. Add `github.com/cilium/ebpf` to `go.mod`.
2. Create `programs/<lang>.c` for each language's BPF program.
3. Use `bpf2go` to generate the Go binding from the C source.
4. In `ebpf_linux.go`'s `start()`, walk `cfg.Targets` and attach each
   matching program via `link.Uprobe(symbol, prog, opts)`.
5. Read events from a `perf.Reader` or `ringbuf.Reader` and convert
   to `source.RawLog`.

## Why ship this scaffolding now

Two reasons:

1. **Cross-compilation safety.** Without the build-tag split, the
   Linux-only code would break `goreleaser` builds on macOS/Windows.
   Getting the boundary right now means subsequent PRs that add the
   real attach code touch only `ebpf_linux.go` and don't risk breaking
   the cross-compile.
2. **Honest status reporting.** The `Source` interface contract says
   "start, then emit events". A no-op implementation would silently
   show `lines_emitted=0` and look healthy. Failing fast with
   `ErrUnsupported` makes the gap obvious in `/healthz` and the
   agents-list dashboard page.
