// Package parser — multi-line recombiner.
//
// Stack traces span many lines. A naive "one parser call per line" approach
// would produce N envelopes for one logical exception. Recombiner buffers
// lines that match a "continuation" predicate until either:
//   - a non-continuation line arrives (the trace is complete)
//   - the configured timeout elapses (the trace was the last log line)
//
// Recombiner is per-source-stream: callers should hold one Recombiner per
// (source, stream) pair so independent streams don't interleave. The Docker
// source, for example, holds one per container.
package parser

import (
	"strings"
	"sync"
	"time"

	"github.com/ankitsin007/supportly-agent/internal/source"
)

// ContinuationFunc returns true if `next` is a continuation of `prev`
// (i.e., should be appended to the current group rather than starting a new one).
//
// Different traceback shapes need different rules:
//   - Python: continuation lines start with whitespace ("  File ...", "    code")
//     OR are the final exception type line (no leading space, has ":")
//   - Java: continuation starts with "\tat " or "\tCaused by:" or "\t..."
//   - Go: continuation is any line until the next blank line
type ContinuationFunc func(prev, next string) bool

// Recombiner groups consecutive log lines into multi-line records.
type Recombiner struct {
	IsContinuation ContinuationFunc
	FlushAfter     time.Duration // max wait for next line before emitting

	mu       sync.Mutex
	buffer   []string
	lastRecv time.Time
}

// Feed accepts a new RawLog. Returns the completed group's combined text
// (with the previous batch) when a non-continuation line arrives, or nil
// if the line was added to the current group.
//
// Caller MUST also call Flush() periodically (every FlushAfter / 2) to
// emit the final group when no further lines arrive.
func (r *Recombiner) Feed(raw source.RawLog) (combined *source.RawLog) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	line := strings.TrimRight(raw.Line, "\n")

	if len(r.buffer) == 0 {
		r.buffer = append(r.buffer, line)
		r.lastRecv = now
		return nil
	}

	if r.IsContinuation(r.buffer[len(r.buffer)-1], line) {
		r.buffer = append(r.buffer, line)
		r.lastRecv = now
		return nil
	}

	// Non-continuation: emit the prior group, start a new one with this line.
	emitted := r.emitLocked(raw)
	r.buffer = []string{line}
	r.lastRecv = now
	return emitted
}

// Flush emits the current buffer if FlushAfter has elapsed since the last
// line was received. Returns nil if nothing to emit. Call from a ticker
// goroutine in the caller (we don't spawn one here to keep the type passive).
func (r *Recombiner) Flush(template source.RawLog) *source.RawLog {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.buffer) == 0 {
		return nil
	}
	if r.FlushAfter > 0 && time.Since(r.lastRecv) < r.FlushAfter {
		return nil
	}
	return r.emitLocked(template)
}

// emitLocked builds a RawLog with the buffer joined by newlines.
// Caller must hold r.mu.
func (r *Recombiner) emitLocked(template source.RawLog) *source.RawLog {
	if len(r.buffer) == 0 {
		return nil
	}
	combined := strings.Join(r.buffer, "\n")
	out := template
	out.Line = combined
	r.buffer = nil
	return &out
}

// PythonContinuation matches Python's traceback shape:
//
//	Traceback (most recent call last):    <-- start
//	  File "/app/x.py", line 42, in foo   <-- continuation (leading space)
//	    raise RuntimeError("boom")        <-- continuation (leading space)
//	RuntimeError: boom                    <-- continuation (no space, has ": ")
//	(next plain log line)                 <-- NOT a continuation, ends the group
func PythonContinuation(prev, next string) bool {
	if next == "" {
		return false
	}
	// Indented = continuation
	if strings.HasPrefix(next, " ") || strings.HasPrefix(next, "\t") {
		return true
	}
	// Final exception line: e.g. "RuntimeError: foo"
	// Heuristic: looks like `<Word>...: <text>` and prev was an indented frame.
	if strings.Contains(next, ": ") && (strings.HasPrefix(prev, " ") || strings.HasPrefix(prev, "\t")) {
		return true
	}
	return false
}

// JavaContinuation matches:
//
//	java.lang.NullPointerException: ...   <-- start
//	  at com.foo.Bar.method(Bar.java:42)  <-- "\tat " or "  at "
//	  ... 5 more                          <-- "\t..."
//	Caused by: java.io.IOException ...    <-- "Caused by:"
func JavaContinuation(_, next string) bool {
	t := strings.TrimLeft(next, " \t")
	return strings.HasPrefix(t, "at ") ||
		strings.HasPrefix(t, "... ") ||
		strings.HasPrefix(t, "Caused by:") ||
		strings.HasPrefix(t, "Suppressed:")
}

// GoPanicContinuation matches:
//
//	panic: runtime error: index out of range  <-- start
//	                                          <-- blank line
//	goroutine 1 [running]:                    <-- continuation
//	main.foo(...)                             <-- continuation
//	  /app/main.go:42 +0x1a3                  <-- continuation
//	exit status 2                             <-- last line, still part of crash
//
// Go panics end at the next non-stack-trace line. Heuristic: continue until
// we hit a line that doesn't look stack-tracey (indented OR "goroutine" OR
// pkg.Func).
func GoPanicContinuation(_, next string) bool {
	if next == "" {
		return true // blank lines INSIDE a panic block are kept
	}
	if strings.HasPrefix(next, "\t") || strings.HasPrefix(next, " ") {
		return true
	}
	if strings.HasPrefix(next, "goroutine ") || strings.HasPrefix(next, "exit status ") {
		return true
	}
	// Function-ish: contains a dot followed by an open paren.
	if i := strings.Index(next, "."); i > 0 {
		rest := next[i:]
		if strings.Contains(rest, "(") {
			return true
		}
	}
	return false
}

// NodeContinuation matches:
//
//	Error: connection refused           <-- start
//	    at TCPConnectWrap... (net.js:1) <-- continuation (whitespace)
func NodeContinuation(_, next string) bool {
	return strings.HasPrefix(next, "    at ") || strings.HasPrefix(next, "\tat ")
}

// RubyContinuation matches:
//
//	from /app/x.rb:42:in `foo'          <-- continuation
//	/app/x.rb:42:in `bar': error (Class) <-- start
//
// Ruby tracebacks have lines starting with "  from " (modern Ruby) or "\tfrom "
// pointing at the prior frame.
func RubyContinuation(_, next string) bool {
	t := strings.TrimLeft(next, " \t")
	return strings.HasPrefix(t, "from ")
}

// UniversalContinuation returns true if `next` looks like a continuation
// of ANY known traceback shape. Used by the file/docker source recombiner
// when we don't yet know which platform's logs are being read.
//
// The trade-off: more permissive than per-language rules, so two unrelated
// indented lines might get combined. The downstream parser then either
// matches (good) or falls through to the fallback layer (acceptable).
func UniversalContinuation(prev, next string) bool {
	if next == "" {
		// Blank lines INSIDE a Go panic block are continuations; for other
		// languages they're not. Default true and let the timeout-based
		// flush bound the damage.
		return true
	}
	return PythonContinuation(prev, next) ||
		JavaContinuation(prev, next) ||
		GoPanicContinuation(prev, next) ||
		NodeContinuation(prev, next) ||
		RubyContinuation(prev, next)
}
