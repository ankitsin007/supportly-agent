// Package ratelimit implements a per-source token bucket.
//
// Default budget per the M1 design doc §9: 100 events/sec sustained,
// burst to 500. Excess events are sampled (1-in-N) so a 50k EPS spike
// becomes ~100 EPS shipped + a `tags.sampled = "1/500"` marker on each
// surviving event.
package ratelimit

import (
	"sync"
	"sync/atomic"
	"time"
)

// Bucket is a thread-safe token bucket.
type Bucket struct {
	rate     float64 // tokens added per second
	capacity float64 // max tokens (burst)

	mu         sync.Mutex
	tokens     float64
	lastRefill time.Time

	// Counters for observability — atomic so the sample-rate computation
	// can read them without holding mu.
	allowed atomic.Uint64
	dropped atomic.Uint64
}

// New returns a bucket that allows `ratePerSec` sustained throughput and
// bursts up to `burst` tokens.
func New(ratePerSec, burst int) *Bucket {
	return &Bucket{
		rate:       float64(ratePerSec),
		capacity:   float64(burst),
		tokens:     float64(burst),
		lastRefill: time.Now(),
	}
}

// Allow returns true if a token was available (and consumed). The caller
// should ship the event if true, sample/drop if false.
func (b *Bucket) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens += elapsed * b.rate
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
	b.lastRefill = now

	if b.tokens >= 1 {
		b.tokens--
		b.allowed.Add(1)
		return true
	}
	b.dropped.Add(1)
	return false
}

// Stats returns counters since process start.
func (b *Bucket) Stats() (allowed, dropped uint64) {
	return b.allowed.Load(), b.dropped.Load()
}

// SampleRate suggests an N for "1-in-N" sampling based on recent drop ratio.
// Returns 1 if no drops (no sampling needed). Used by callers that want to
// emit a sampling marker on the events that DO get through.
func (b *Bucket) SampleRate() uint64 {
	a, d := b.Stats()
	total := a + d
	if total == 0 || a == 0 {
		return 1
	}
	return total / a
}
