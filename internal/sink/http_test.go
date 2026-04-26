package sink

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ankitsin007/supportly-agent/internal/envelope"
)

func TestHTTP_Send_Success(t *testing.T) {
	var received atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-API-Key"); got != "secret" {
			t.Errorf("X-API-Key = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		received.Add(1)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"abc"}`))
	}))
	defer srv.Close()

	h := New(srv.URL, "secret")
	env := envelope.New("proj-1", "python")
	env.Message = "test"

	if err := h.Send(context.Background(), env); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if received.Load() != 1 {
		t.Errorf("server received %d requests, want 1", received.Load())
	}
}

func TestHTTP_Send_RetriesOn5xx(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	h := New(srv.URL, "k")
	h.BaseBackoff = 10 * time.Millisecond
	h.MaxBackoff = 50 * time.Millisecond

	if err := h.Send(context.Background(), envelope.New("p", "x")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if attempts.Load() != 3 {
		t.Errorf("attempts = %d, want 3", attempts.Load())
	}
}

func TestHTTP_Send_DoesNotRetry4xx(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	h := New(srv.URL, "k")
	h.BaseBackoff = 1 * time.Millisecond

	err := h.Send(context.Background(), envelope.New("p", "x"))
	if err == nil {
		t.Fatal("expected error on 400, got nil")
	}
	if attempts.Load() != 1 {
		t.Errorf("attempts = %d, want 1 (4xx must not retry)", attempts.Load())
	}
}

func TestHTTP_Send_RespectsContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	h := New(srv.URL, "k")
	h.BaseBackoff = 100 * time.Millisecond
	h.MaxRetries = 100

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := h.Send(ctx, envelope.New("p", "x"))
	if err == nil {
		t.Fatal("expected ctx-related error")
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Errorf("did not respect context cancellation; took %v", time.Since(start))
	}
}
