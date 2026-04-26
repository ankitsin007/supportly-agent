// Package sink ships envelopes to Supportly's ingest endpoint.
//
// M1 implementation: synchronous HTTP POST with exponential backoff retry.
// On-disk buffering for offline survival lands in Week 2.
package sink

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/ankitsin007/supportly-agent/internal/envelope"
)

// HTTP posts envelopes to a Supportly ingest URL.
type HTTP struct {
	URL    string // e.g. "http://localhost:8002/api/v1/ingest/events"
	APIKey string // X-API-Key header value
	Client *http.Client

	// MaxRetries before giving up on a single envelope. Default 5.
	MaxRetries int
	// BaseBackoff is the initial retry delay; doubles each attempt up to MaxBackoff.
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
}

// New returns an HTTP sink with sane defaults.
func New(url, apiKey string) *HTTP {
	return &HTTP{
		URL:         url,
		APIKey:      apiKey,
		Client:      &http.Client{Timeout: 10 * time.Second},
		MaxRetries:  5,
		BaseBackoff: 1 * time.Second,
		MaxBackoff:  60 * time.Second,
	}
}

// Send posts one envelope. Retries on 5xx and network errors with backoff.
// 4xx is permanent — logged and dropped (a malformed envelope won't fix
// itself by retrying).
func (h *HTTP) Send(ctx context.Context, env *envelope.Envelope) error {
	body, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}

	backoff := h.BaseBackoff
	var lastErr error

	for attempt := 0; attempt <= h.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > h.MaxBackoff {
				backoff = h.MaxBackoff
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", h.APIKey)
		req.Header.Set("User-Agent", "supportly-agent/0.1.0")

		resp, err := h.Client.Do(req)
		if err != nil {
			lastErr = err
			slog.Warn("ingest network error", "err", err, "attempt", attempt)
			continue
		}

		// Drain & close so the connection can be reused.
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()

		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			return nil
		case resp.StatusCode >= 400 && resp.StatusCode < 500:
			// Permanent: malformed envelope or auth problem. Don't retry.
			return fmt.Errorf("ingest rejected with %d (permanent)", resp.StatusCode)
		default:
			lastErr = fmt.Errorf("ingest returned %d", resp.StatusCode)
			slog.Warn("ingest server error", "status", resp.StatusCode, "attempt", attempt)
		}
	}

	return fmt.Errorf("max retries exhausted: %w", lastErr)
}
