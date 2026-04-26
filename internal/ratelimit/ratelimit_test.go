package ratelimit

import (
	"testing"
	"time"
)

func TestBucket_AllowsBurst(t *testing.T) {
	b := New(10, 5) // 10/sec sustained, 5 burst
	for i := 0; i < 5; i++ {
		if !b.Allow() {
			t.Errorf("burst[%d] denied", i)
		}
	}
	if b.Allow() {
		t.Error("6th immediate call should be denied")
	}
}

func TestBucket_Refills(t *testing.T) {
	b := New(100, 1) // 100/sec, burst 1 → 10ms per token
	if !b.Allow() {
		t.Fatal("first allow should succeed")
	}
	if b.Allow() {
		t.Fatal("immediate second allow should fail")
	}
	time.Sleep(15 * time.Millisecond)
	if !b.Allow() {
		t.Error("after refill window, allow should succeed")
	}
}

func TestBucket_Stats(t *testing.T) {
	b := New(2, 2)
	for i := 0; i < 5; i++ {
		b.Allow()
	}
	a, d := b.Stats()
	if a != 2 || d != 3 {
		t.Errorf("stats = (%d, %d), want (2, 3)", a, d)
	}
}

func TestBucket_SampleRate(t *testing.T) {
	b := New(0, 1)
	b.Allow() // succeed
	for i := 0; i < 9; i++ {
		b.Allow() // fail
	}
	r := b.SampleRate()
	if r < 9 || r > 11 {
		t.Errorf("sample rate = %d, want ~10", r)
	}
}
