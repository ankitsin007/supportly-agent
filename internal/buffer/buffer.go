// Package buffer provides an on-disk FIFO queue for envelopes that the
// HTTP sink couldn't ship (network outage, Supportly 5xx, etc.).
//
// Design choices:
//   - Each envelope is a single file under <dir>/queue/, named by a
//     monotonically-increasing 20-digit zero-padded sequence number so
//     directory listing yields FIFO order. No external library needed
//     (no BoltDB, no SQLite) — keeps the agent dependency surface tiny.
//   - A size cap (default 500 MB per design doc §9) is enforced by
//     evicting the oldest entries when a new write would exceed it.
//   - The buffer is single-process, single-writer; agent crashes mid-
//     write leave a partial file at most, which Read() filters by
//     verifying the JSON parses.
//   - On startup the buffer scans the directory and resumes from the
//     highest existing sequence + 1.
//
// Thread safety: all public methods take the internal mutex. Callers
// can safely Enqueue from one goroutine while another is Drain'ing.
package buffer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"

	"github.com/ankitsin007/supportly-agent/internal/envelope"
)

// Buffer is an on-disk FIFO of Envelopes.
type Buffer struct {
	dir      string
	maxBytes int64

	mu        sync.Mutex
	nextSeq   uint64
	totalSize int64
}

// New opens (or creates) a buffer at dir with the given size cap.
// dir is created if it doesn't exist.
func New(dir string, maxBytes int64) (*Buffer, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	b := &Buffer{dir: dir, maxBytes: maxBytes}
	if err := b.scan(); err != nil {
		return nil, err
	}
	return b, nil
}

// scan walks the directory once on Open to populate nextSeq + totalSize.
func (b *Buffer) scan() error {
	entries, err := os.ReadDir(b.dir)
	if err != nil {
		return fmt.Errorf("read dir: %w", err)
	}
	maxSeq := uint64(0)
	var size int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		seq, ok := parseSeq(e.Name())
		if !ok {
			continue
		}
		if seq > maxSeq {
			maxSeq = seq
		}
		info, err := e.Info()
		if err == nil {
			size += info.Size()
		}
	}
	b.nextSeq = maxSeq + 1
	b.totalSize = size
	return nil
}

// Enqueue persists an envelope. If the write would exceed maxBytes, the
// oldest entries are evicted first. Returns the assigned sequence number.
func (b *Buffer) Enqueue(env *envelope.Envelope) (uint64, error) {
	body, err := json.Marshal(env)
	if err != nil {
		return 0, fmt.Errorf("marshal: %w", err)
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	// Evict until we have headroom for the new entry.
	for b.totalSize+int64(len(body)) > b.maxBytes && b.totalSize > 0 {
		if err := b.evictOldestLocked(); err != nil {
			return 0, fmt.Errorf("evict: %w", err)
		}
	}

	seq := b.nextSeq
	name := fmt.Sprintf("%020d.json", seq)
	path := filepath.Join(b.dir, name)

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return 0, fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("rename: %w", err)
	}
	b.nextSeq++
	b.totalSize += int64(len(body))
	return seq, nil
}

// Drain calls fn on each buffered envelope in FIFO order. If fn returns
// nil the entry is removed; if fn returns an error, drain stops and the
// remaining entries stay buffered. Returns the number of successfully
// drained entries.
func (b *Buffer) Drain(fn func(*envelope.Envelope) error) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	entries, err := b.listLocked()
	if err != nil {
		return 0, err
	}
	drained := 0
	for _, e := range entries {
		path := filepath.Join(b.dir, e.name)
		body, err := os.ReadFile(path)
		if err != nil {
			// Race with eviction or human deletion — skip.
			continue
		}
		var env envelope.Envelope
		if err := json.Unmarshal(body, &env); err != nil {
			// Corrupt entry from a partial write — discard so it doesn't
			// block the queue forever.
			b.removeLocked(path, e.size)
			continue
		}
		if err := fn(&env); err != nil {
			return drained, err
		}
		b.removeLocked(path, e.size)
		drained++
	}
	return drained, nil
}

// Len reports the current entry count. Cheap O(dir-listing).
func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	entries, err := b.listLocked()
	if err != nil {
		return 0
	}
	return len(entries)
}

// Bytes reports the on-disk usage in bytes (cached; updated on Enqueue/evict).
func (b *Buffer) Bytes() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.totalSize
}

// --- internal helpers (caller must hold b.mu) ---

type queueEntry struct {
	name string
	seq  uint64
	size int64
}

func (b *Buffer) listLocked() ([]queueEntry, error) {
	entries, err := os.ReadDir(b.dir)
	if err != nil {
		return nil, err
	}
	out := make([]queueEntry, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		seq, ok := parseSeq(e.Name())
		if !ok {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, queueEntry{name: e.Name(), seq: seq, size: info.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].seq < out[j].seq })
	return out, nil
}

func (b *Buffer) evictOldestLocked() error {
	entries, err := b.listLocked()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return errors.New("nothing to evict")
	}
	oldest := entries[0]
	b.removeLocked(filepath.Join(b.dir, oldest.name), oldest.size)
	return nil
}

func (b *Buffer) removeLocked(path string, size int64) {
	if err := os.Remove(path); err == nil {
		b.totalSize -= size
		if b.totalSize < 0 {
			b.totalSize = 0
		}
	}
}

func parseSeq(name string) (uint64, bool) {
	if len(name) < 25 || name[20:] != ".json" {
		return 0, false
	}
	n, err := strconv.ParseUint(name[:20], 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// CompactInfo wraps the buffer's size + count for the healthz endpoint.
type CompactInfo struct {
	Entries int   `json:"entries"`
	Bytes   int64 `json:"bytes"`
	Cap     int64 `json:"cap"`
}

// Info returns a snapshot for /healthz.
func (b *Buffer) Info() CompactInfo {
	return CompactInfo{
		Entries: b.Len(),
		Bytes:   b.Bytes(),
		Cap:     b.maxBytes,
	}
}

// Stub so callers can treat a nil buffer as "disabled".
var _ io.Closer = (*Buffer)(nil)

// Close is a no-op; files are flushed on each Enqueue. Provided for
// io.Closer conformance so callers can defer it.
func (b *Buffer) Close() error { return nil }
