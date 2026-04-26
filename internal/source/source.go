// Package source defines the Source interface every log adapter implements.
//
// Sources produce RawLog values into a shared channel. The agent core fans
// them into the parser pipeline. Future eBPF / OTel sources will populate
// the optional Spans field; M1 sources always leave it nil.
package source

import (
	"context"
	"time"
)

// RawLog is the universal currency between sources and parsers.
// One RawLog per discrete log line (or one per multi-line traceback when
// the source already knows how to recombine them).
type RawLog struct {
	// Source identifies which adapter produced this — populated by the
	// adapter, used by parsers to select strategy and by the envelope
	// builder for the `tags.log_source` field.
	Source string

	// Timestamp is the time the log line was emitted (not received).
	// Falls back to time.Now() if the source can't extract it.
	Timestamp time.Time

	// Line is the raw text content. For multi-line tracebacks that the
	// source has already grouped (e.g., docker recombiner), this contains
	// the entire group with embedded newlines.
	Line string

	// Tags carries source-specific metadata: container_name, k8s_namespace,
	// systemd_unit, file_path. Merged into the envelope's tags map.
	Tags map[string]string

	// Spans is reserved for M5 (eBPF / OTel auto-instrument). Always nil
	// from M1 sources. Adding the field now means the parser pipeline
	// won't need a breaking change when M5 lands.
	Spans []Span
}

// Span is an opaque placeholder for M5. Defined here so the wire format
// is stable across milestones; concrete shape comes with M5.
type Span struct{}

// Source is the contract every log adapter implements.
//
// Lifecycle:
//
//	src := file.New(...)
//	out := make(chan RawLog, 1024)
//	go src.Start(ctx, out)
//	... agent consumes from out ...
//	src.Stop()
//
// Sources MUST exit Start when ctx is cancelled. They MUST NOT block on
// channel sends — drop with a metric increment if the consumer is slow.
type Source interface {
	// Start begins emitting RawLogs to the out channel. Returns when ctx
	// is done or an unrecoverable error occurs.
	Start(ctx context.Context, out chan<- RawLog) error

	// Stop releases any resources (file handles, sockets, goroutines).
	// Idempotent. Should be safe to call after Start has already returned.
	Stop() error

	// Name returns a human-readable identifier for logs and metrics
	// (e.g., "file:/var/log/myapp.log", "docker:my-container").
	Name() string

	// Health reports the current state for the agent's heartbeat metrics.
	Health() Health
}

// Health is the observability snapshot the agent ships to Supportly
// every 60s as `parse_failures_total`, `events_buffered`, etc.
type Health struct {
	Healthy      bool
	LinesEmitted uint64
	LinesDropped uint64
	LastError    string
	LastErrorAt  time.Time
}
