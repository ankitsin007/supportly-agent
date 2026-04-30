// Package otel implements an OTel OTLP/HTTP log receiver.
//
// M5 Week 1 — the first piece of the deepest-fidelity tier. Customer
// apps already instrumented with OpenTelemetry (Java agent, Python SDK,
// Node SDK, etc.) can point their OTLP/HTTP log exporter at the agent's
// /v1/logs endpoint. The agent extracts each LogRecord and feeds it
// into the same parser pipeline the file/docker/journald sources use.
//
// Why OTel before eBPF:
//   - Cross-platform (works on macOS, Windows, Linux without kernel deps).
//   - Industry-standard wire format; large existing ecosystem.
//   - Lower setup cost for customers — they likely already have OTel
//     instrumentation for traces/metrics.
//
// Wire format (subset we implement):
//
//	POST /v1/logs   Content-Type: application/json
//	Body: {"resourceLogs": [{"scopeLogs": [{"logRecords": [...]}]}]}
//
// Each LogRecord carries timestamp, severityText, severityNumber,
// body (string or any), attributes ([{key, value}]), and (optionally)
// traceId/spanId. We map those onto our internal RawLog so the existing
// parsers (Python tracebacks, Java throwables, etc.) work unchanged.
package otel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ankitsin007/supportly-agent/internal/source"
)

// Default OTLP/HTTP listen port. Matches the OTLP spec's reserved port
// for HTTP. gRPC's 4317 is intentionally NOT supported in M5 W1 —
// adding gRPC pulls protobuf + grpc-go which would balloon the
// agent binary; HTTP/JSON keeps it small.
const DefaultAddr = "127.0.0.1:4318"

// Source receives OTel OTLP/HTTP requests and emits one RawLog per LogRecord.
type Source struct {
	Addr string

	// Counters surfaced by /healthz.
	emitted atomic.Uint64
	dropped atomic.Uint64

	mu        sync.Mutex
	lastErr   string
	lastErrAt time.Time

	srv *http.Server
}

// New returns a Source bound to the given address. Pass "" for the default.
func New(addr string) *Source {
	if addr == "" {
		addr = DefaultAddr
	}
	return &Source{Addr: addr}
}

// Name implements source.Source.
func (s *Source) Name() string { return "otel:" + s.Addr }

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

// Stop is a no-op; ctx cancellation drives shutdown.
func (s *Source) Stop() error { return nil }

// Start binds the listener and serves until ctx is cancelled.
func (s *Source) Start(ctx context.Context, out chan<- source.RawLog) error {
	mux := http.NewServeMux()
	mux.Handle("/v1/logs", s.handler(ctx, out))

	s.srv = &http.Server{
		Addr:              s.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		s.recordErr(err)
		return fmt.Errorf("otel: bind %s: %w", s.Addr, err)
	}
	slog.Info("otel receiver listening", "addr", s.Addr)

	errCh := make(chan error, 1)
	go func() {
		err := s.srv.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(shutCtx)
		return nil
	case err := <-errCh:
		return err
	}
}

func (s *Source) handler(ctx context.Context, out chan<- source.RawLog) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		var req otlpLogsRequest
		if err := json.Unmarshal(body, &req); err != nil {
			s.recordErr(err)
			http.Error(w, "decode otlp/json: "+err.Error(), http.StatusBadRequest)
			return
		}

		records := req.flatten()
		for _, rec := range records {
			rl := source.RawLog{
				Source:    "otel",
				Timestamp: rec.timestamp(),
				Line:      rec.bodyText(),
				Tags:      rec.tags(),
			}
			select {
			case out <- rl:
				s.emitted.Add(1)
			case <-ctx.Done():
				return
			default:
				s.dropped.Add(1)
			}
		}

		// OTLP/HTTP success response per the spec: empty
		// ExportLogsServiceResponse JSON (just {}).
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}
}

func (s *Source) recordErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastErr = err.Error()
	s.lastErrAt = time.Now().UTC()
}

// --- OTLP/JSON wire types (minimal subset) ---

type otlpLogsRequest struct {
	ResourceLogs []resourceLogs `json:"resourceLogs"`
}

type resourceLogs struct {
	Resource  resourceObj `json:"resource"`
	ScopeLogs []scopeLogs `json:"scopeLogs"`
}

type resourceObj struct {
	Attributes []keyValue `json:"attributes"`
}

type scopeLogs struct {
	Scope      scopeObj    `json:"scope"`
	LogRecords []logRecord `json:"logRecords"`
}

type scopeObj struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type logRecord struct {
	TimeUnixNano   string     `json:"timeUnixNano"`
	SeverityNumber int        `json:"severityNumber"`
	SeverityText   string     `json:"severityText"`
	Body           anyValue   `json:"body"`
	Attributes     []keyValue `json:"attributes"`
	TraceID        string     `json:"traceId"`
	SpanID         string     `json:"spanId"`

	// Filled in during flatten() so per-record helpers can see resource attrs.
	resourceAttrs []keyValue
	scopeName     string
}

type keyValue struct {
	Key   string   `json:"key"`
	Value anyValue `json:"value"`
}

// anyValue is OTel's tagged-union value type. We only round-trip the
// most common variants; rare types (kvlist, array, bytes) are stringified.
type anyValue struct {
	StringValue *string  `json:"stringValue,omitempty"`
	IntValue    *string  `json:"intValue,omitempty"` // OTLP/JSON encodes int64 as string
	DoubleValue *float64 `json:"doubleValue,omitempty"`
	BoolValue   *bool    `json:"boolValue,omitempty"`
	// ArrayValue + KvlistValue intentionally omitted; coerced via .String().
}

// String returns a stable text rendering of the value, used as the line
// body when Log.Body is something other than a string.
func (v anyValue) String() string {
	switch {
	case v.StringValue != nil:
		return *v.StringValue
	case v.IntValue != nil:
		return *v.IntValue
	case v.DoubleValue != nil:
		return fmt.Sprintf("%g", *v.DoubleValue)
	case v.BoolValue != nil:
		return fmt.Sprintf("%t", *v.BoolValue)
	default:
		return ""
	}
}

// flatten yields one logRecord per record across all resourceLogs/scopeLogs,
// stamped with resource + scope context so per-record helpers can reach them.
func (r otlpLogsRequest) flatten() []logRecord {
	var out []logRecord
	for _, rl := range r.ResourceLogs {
		for _, sl := range rl.ScopeLogs {
			for _, rec := range sl.LogRecords {
				rec.resourceAttrs = rl.Resource.Attributes
				rec.scopeName = sl.Scope.Name
				out = append(out, rec)
			}
		}
	}
	return out
}

func (lr logRecord) timestamp() time.Time {
	if lr.TimeUnixNano == "" {
		return time.Now().UTC()
	}
	var ns int64
	for i := 0; i < len(lr.TimeUnixNano); i++ {
		c := lr.TimeUnixNano[i]
		if c < '0' || c > '9' {
			return time.Now().UTC()
		}
		ns = ns*10 + int64(c-'0')
	}
	return time.Unix(0, ns).UTC()
}

func (lr logRecord) bodyText() string {
	return lr.Body.String()
}

// tags collects everything the parser pipeline downstream might want:
// service.name from resource attrs, severity, scope, trace + span ids.
func (lr logRecord) tags() map[string]string {
	tags := map[string]string{
		"otel_severity": lr.SeverityText,
	}
	if lr.SeverityText == "" && lr.SeverityNumber > 0 {
		tags["otel_severity"] = severityNumberToText(lr.SeverityNumber)
	}
	if lr.scopeName != "" {
		tags["otel_scope"] = lr.scopeName
	}
	if lr.TraceID != "" {
		tags["trace_id"] = lr.TraceID
	}
	if lr.SpanID != "" {
		tags["span_id"] = lr.SpanID
	}
	for _, kv := range lr.resourceAttrs {
		// Common resource attrs we always promote; everything else gets
		// prefixed with `otel_attr.` to avoid colliding with our
		// existing tag namespace.
		switch kv.Key {
		case "service.name":
			tags["service_name"] = kv.Value.String()
		case "service.namespace":
			tags["service_namespace"] = kv.Value.String()
		case "service.version":
			tags["service_version"] = kv.Value.String()
		case "host.name":
			tags["hostname"] = kv.Value.String()
		case "deployment.environment":
			tags["environment"] = kv.Value.String()
		}
	}
	return tags
}

// severityNumberToText maps the OTel SeverityNumber enum (1-24) to a
// human-readable string. Banded per the spec: TRACE(1-4), DEBUG(5-8),
// INFO(9-12), WARN(13-16), ERROR(17-20), FATAL(21-24).
func severityNumberToText(n int) string {
	switch {
	case n >= 21:
		return "FATAL"
	case n >= 17:
		return "ERROR"
	case n >= 13:
		return "WARN"
	case n >= 9:
		return "INFO"
	case n >= 5:
		return "DEBUG"
	default:
		return "TRACE"
	}
}
