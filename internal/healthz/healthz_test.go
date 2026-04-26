package healthz

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/ankitsin007/supportly-agent/internal/source"
)

// stubSource implements source.Source for the health snapshot test.
type stubSource struct {
	name    string
	healthy bool
	emitted uint64
	lastErr string
}

func (s *stubSource) Name() string                                          { return s.name }
func (s *stubSource) Start(_ context.Context, _ chan<- source.RawLog) error { return nil }
func (s *stubSource) Stop() error                                           { return nil }
func (s *stubSource) Health() source.Health {
	return source.Health{
		Healthy:      s.healthy,
		LinesEmitted: s.emitted,
		LastError:    s.lastErr,
	}
}

func TestServer_HealthyWhenAllSourcesHealthy(t *testing.T) {
	srcs := []source.Source{
		&stubSource{name: "file:/var/log/a", healthy: true, emitted: 100},
		&stubSource{name: "docker:web", healthy: true, emitted: 50},
	}
	s := New("0.5.0", srcs)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- s.Start(ctx) }()

	waitForListen(t)

	resp, err := http.Get("http://" + Addr + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var snap Snapshot
	if err := json.Unmarshal(body, &snap); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	if snap.Status != "ok" {
		t.Errorf("status = %q, want ok", snap.Status)
	}
	if snap.Version != "0.5.0" {
		t.Errorf("version = %q", snap.Version)
	}
	if len(snap.Sources) != 2 {
		t.Errorf("sources len = %d", len(snap.Sources))
	}

	cancel()
	<-done
}

func TestServer_DegradedWhenAnySourceUnhealthy(t *testing.T) {
	srcs := []source.Source{
		&stubSource{name: "file:/a", healthy: true},
		&stubSource{name: "docker:bad", healthy: false, lastErr: "permission denied"},
	}
	s := New("0.5.0", srcs)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Start(ctx) }()

	waitForListen(t)

	resp, err := http.Get("http://" + Addr + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var snap Snapshot
	_ = json.Unmarshal(body, &snap)
	if snap.Status != "degraded" {
		t.Errorf("status = %q, want degraded", snap.Status)
	}

	cancel()
	<-done
}

func TestServer_BindFailsIfPortBusy(t *testing.T) {
	// Bind first instance.
	srcs := []source.Source{}
	s1 := New("a", srcs)
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	done1 := make(chan error, 1)
	go func() { done1 <- s1.Start(ctx1) }()
	waitForListen(t)

	// Second bind should error.
	s2 := New("b", srcs)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	if err := s2.Start(ctx2); err == nil {
		t.Errorf("expected bind error on duplicate listen")
	}
}

func waitForListen(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get("http://" + Addr + "/healthz"); err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server did not listen within deadline")
}
