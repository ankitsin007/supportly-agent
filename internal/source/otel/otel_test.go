package otel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ankitsin007/supportly-agent/internal/source"
)

// pickPort picks a free TCP port on 127.0.0.1 so parallel tests don't
// collide on the default :4318.
func pickPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func startSource(t *testing.T) (*Source, chan source.RawLog, context.CancelFunc) {
	t.Helper()
	addr := pickPort(t)
	s := New(addr)
	out := make(chan source.RawLog, 32)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = s.Start(ctx, out) }()
	// Wait for the listener to bind.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			c.Close()
			return s, out, cancel
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	t.Fatalf("otel source never bound to %s", addr)
	return nil, nil, cancel
}

func sendOTLP(t *testing.T, addr string, payload string) *http.Response {
	t.Helper()
	resp, err := http.Post(
		"http://"+addr+"/v1/logs",
		"application/json",
		bytes.NewReader([]byte(payload)),
	)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	return resp
}

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

func TestPostsOneLogRecord_EmitsRawLog(t *testing.T) {
	s, out, cancel := startSource(t)
	defer cancel()

	now := time.Now().UnixNano()
	body := fmt.Sprintf(`{
	  "resourceLogs": [{
	    "resource": {"attributes":[{"key":"service.name","value":{"stringValue":"checkout-svc"}}]},
	    "scopeLogs": [{
	      "scope": {"name":"my-logger"},
	      "logRecords": [{
	        "timeUnixNano": "%d",
	        "severityText": "ERROR",
	        "severityNumber": 17,
	        "body": {"stringValue": "TimeoutError: db connection refused"},
	        "traceId": "abc123",
	        "spanId": "def456"
	      }]
	    }]
	  }]
	}`, now)
	resp := sendOTLP(t, s.Addr, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	got := drain(out, 1, 1*time.Second)
	if len(got) != 1 {
		t.Fatalf("expected 1 RawLog, got %d", len(got))
	}
	rl := got[0]
	if rl.Source != "otel" {
		t.Errorf("Source = %q", rl.Source)
	}
	if rl.Line != "TimeoutError: db connection refused" {
		t.Errorf("Line = %q", rl.Line)
	}
	if rl.Tags["otel_severity"] != "ERROR" {
		t.Errorf("severity tag = %q", rl.Tags["otel_severity"])
	}
	if rl.Tags["service_name"] != "checkout-svc" {
		t.Errorf("service_name tag = %q", rl.Tags["service_name"])
	}
	if rl.Tags["trace_id"] != "abc123" || rl.Tags["span_id"] != "def456" {
		t.Errorf("trace/span tags = %v", rl.Tags)
	}
	wantTime := time.Unix(0, now).UTC()
	if !rl.Timestamp.Equal(wantTime) {
		t.Errorf("Timestamp = %v want %v", rl.Timestamp, wantTime)
	}
}

func TestSeverityNumberFallback(t *testing.T) {
	s, out, cancel := startSource(t)
	defer cancel()

	body := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[
	  {"severityNumber": 9, "body":{"stringValue":"info line"}},
	  {"severityNumber": 18, "body":{"stringValue":"err line"}},
	  {"severityNumber": 22, "body":{"stringValue":"fatal line"}}
	]}]}]}`
	sendOTLP(t, s.Addr, body)

	got := drain(out, 3, 1*time.Second)
	if len(got) != 3 {
		t.Fatalf("got %d", len(got))
	}
	want := []string{"INFO", "ERROR", "FATAL"}
	for i, w := range want {
		if got[i].Tags["otel_severity"] != w {
			t.Errorf("record %d severity = %q want %q",
				i, got[i].Tags["otel_severity"], w)
		}
	}
}

func TestRejectsNonPOST(t *testing.T) {
	s, _, cancel := startSource(t)
	defer cancel()
	resp, err := http.Get("http://" + s.Addr + "/v1/logs")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET should be 405, got %d", resp.StatusCode)
	}
}

func TestRejectsInvalidJSON(t *testing.T) {
	s, _, cancel := startSource(t)
	defer cancel()
	resp := sendOTLP(t, s.Addr, "{ not json")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestEmptyBatchReturns200WithoutEmitting(t *testing.T) {
	s, out, cancel := startSource(t)
	defer cancel()
	resp := sendOTLP(t, s.Addr, `{"resourceLogs": []}`)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("empty batch should be 200, got %d", resp.StatusCode)
	}
	// Drain channel briefly; nothing should arrive.
	select {
	case rl := <-out:
		t.Errorf("unexpected RawLog: %+v", rl)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestMultipleResourceLogs(t *testing.T) {
	s, out, cancel := startSource(t)
	defer cancel()

	body := `{"resourceLogs":[
	  {"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"a"}}]},
	   "scopeLogs":[{"logRecords":[{"body":{"stringValue":"line-a"}}]}]},
	  {"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"b"}}]},
	   "scopeLogs":[{"logRecords":[{"body":{"stringValue":"line-b"}}]}]}
	]}`
	sendOTLP(t, s.Addr, body)
	got := drain(out, 2, 1*time.Second)
	if len(got) != 2 {
		t.Fatalf("got %d", len(got))
	}
	services := []string{got[0].Tags["service_name"], got[1].Tags["service_name"]}
	if services[0] == services[1] {
		t.Errorf("expected different service tags, got %v", services)
	}
}

func TestHealthSnapshotCounts(t *testing.T) {
	s, out, cancel := startSource(t)
	defer cancel()

	for i := 0; i < 5; i++ {
		sendOTLP(t, s.Addr,
			`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"x"}}]}]}]}`,
		)
	}
	// Drain so we don't block.
	_ = drain(out, 5, 1*time.Second)

	h := s.Health()
	if h.LinesEmitted != 5 {
		t.Errorf("LinesEmitted = %d want 5", h.LinesEmitted)
	}
	if !h.Healthy {
		t.Errorf("expected healthy=true, got %v", h)
	}
}

// Sanity check that severityNumberToText doesn't blow up for boundary
// values without us standing up a server.
func TestSeverityNumberToText(t *testing.T) {
	cases := map[int]string{
		1: "TRACE", 4: "TRACE",
		5: "DEBUG", 8: "DEBUG",
		9: "INFO", 12: "INFO",
		13: "WARN", 16: "WARN",
		17: "ERROR", 20: "ERROR",
		21: "FATAL", 24: "FATAL",
		0: "TRACE", // out-of-range falls back to TRACE
	}
	for n, want := range cases {
		if got := severityNumberToText(n); got != want {
			t.Errorf("severityNumberToText(%d) = %q want %q", n, got, want)
		}
	}
}

// Ensures the request-decoder treats integer-string bodies (OTLP/JSON
// represents int64 as string) without panicking.
func TestIntValueBodyRoundTrip(t *testing.T) {
	v := anyValue{IntValue: stringPtr("42")}
	if got := v.String(); got != "42" {
		t.Errorf("anyValue.String() = %q want 42", got)
	}
}

func stringPtr(s string) *string { return &s }

// Make sure the dropped counter increments when the consumer can't keep up.
// Done by filling the channel without anyone reading.
func TestDroppedCounterIncrementsWhenChannelFull(t *testing.T) {
	addr := pickPort(t)
	s := New(addr)
	out := make(chan source.RawLog) // capacity 0 → blocks immediately
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Start(ctx, out) }()
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	for i := 0; i < 3; i++ {
		sendOTLP(t, addr,
			`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"x"}}]}]}]}`,
		)
	}
	// Give handler time to attempt sends.
	time.Sleep(100 * time.Millisecond)
	if d := s.Health().LinesDropped; d == 0 {
		t.Errorf("expected dropped > 0, got %d", d)
	}
}

// quiet the linter — the imports below get used elsewhere in the
// suite via stringPtr / strconv usage in fixtures. Touch them so a
// stale-import checker doesn't yell.
var (
	_ = strconv.Itoa
	_ = json.Valid
	_ atomic.Bool
)
