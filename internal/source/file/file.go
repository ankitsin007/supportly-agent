// Package file implements a file-tailing Source backed by fsnotify.
//
// Behavior:
//   - On Start, seeks to the END of the file (only NEW lines are emitted)
//   - On rotation (file truncated or replaced), reopens and resumes
//   - Each line becomes one RawLog
//   - Multi-line traceback recombination happens in the parser layer,
//     not here, so this stays simple.
//
// Limitations (M1):
//   - One file per FileSource. Glob expansion happens at config-load time
//     (caller constructs N FileSources for N matched paths).
//   - No log-rotation race fix beyond reopen-on-truncate. logrotate's
//     copytruncate mode is supported; rename+create mode TBD in M1.1.
package file

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/ankitsin007/supportly-agent/internal/source"
)

// FileSource tails a single file.
type FileSource struct {
	path string

	// Counters for Health() — atomic so Health can be called concurrently.
	emitted atomic.Uint64
	dropped atomic.Uint64

	mu        sync.Mutex
	lastErr   string
	lastErrAt time.Time
	stopped   bool
}

// New returns an unstarted FileSource for the given path.
func New(path string) *FileSource {
	return &FileSource{path: path}
}

// Name returns "file:<path>" for logs and metrics.
func (f *FileSource) Name() string {
	return "file:" + f.path
}

// Start tails the file until ctx is done or an unrecoverable error occurs.
func (f *FileSource) Start(ctx context.Context, out chan<- source.RawLog) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("fsnotify.NewWatcher: %w", err)
	}
	defer watcher.Close()

	if err := watcher.Add(f.path); err != nil {
		return fmt.Errorf("watch %s: %w", f.path, err)
	}

	fh, err := os.Open(f.path)
	if err != nil {
		return fmt.Errorf("open %s: %w", f.path, err)
	}
	defer fh.Close()

	// Seek to end — we only emit NEW lines.
	if _, err := fh.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("seek %s: %w", f.path, err)
	}

	reader := bufio.NewReader(fh)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case ev, ok := <-watcher.Events:
			if !ok {
				return fmt.Errorf("watcher channel closed")
			}
			if ev.Op&fsnotify.Write == fsnotify.Write {
				f.drainReader(ctx, reader, out)
			}
			if ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
				// File rotated — reopen and reset reader.
				newFh, newReader, err := f.reopen(watcher)
				if err != nil {
					f.recordErr(err)
					return err
				}
				fh.Close()
				fh = newFh
				reader = newReader
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return fmt.Errorf("watcher errors channel closed")
			}
			f.recordErr(err)

		case <-time.After(1 * time.Second):
			// Periodic poll catches missed events on some filesystems.
			f.drainReader(ctx, reader, out)
		}
	}
}

func (f *FileSource) reopen(watcher *fsnotify.Watcher) (*os.File, *bufio.Reader, error) {
	// Wait briefly for the new file to appear (logrotate is racy).
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(f.path); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := watcher.Add(f.path); err != nil {
		return nil, nil, fmt.Errorf("re-watch %s: %w", f.path, err)
	}
	fh, err := os.Open(f.path)
	if err != nil {
		return nil, nil, fmt.Errorf("reopen %s: %w", f.path, err)
	}
	// On reopen we start at the BEGINNING — the old file's tail is gone,
	// the new file may already have content.
	return fh, bufio.NewReader(fh), nil
}

func (f *FileSource) drainReader(ctx context.Context, reader *bufio.Reader, out chan<- source.RawLog) {
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			rl := source.RawLog{
				Source:    "file",
				Timestamp: time.Now().UTC(),
				Line:      line,
				Tags:      map[string]string{"file_path": f.path},
			}
			// Non-blocking send — drop and increment if consumer is slow.
			select {
			case out <- rl:
				f.emitted.Add(1)
			case <-ctx.Done():
				return
			default:
				f.dropped.Add(1)
			}
		}
		if err == io.EOF {
			return
		}
		if err != nil {
			f.recordErr(err)
			return
		}
	}
}

func (f *FileSource) recordErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastErr = err.Error()
	f.lastErrAt = time.Now().UTC()
}

// Stop is currently a no-op; ctx cancellation drives shutdown. Provided
// for interface conformance.
func (f *FileSource) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = true
	return nil
}

// Health returns the current snapshot.
func (f *FileSource) Health() source.Health {
	f.mu.Lock()
	defer f.mu.Unlock()
	return source.Health{
		Healthy:      f.lastErr == "",
		LinesEmitted: f.emitted.Load(),
		LinesDropped: f.dropped.Load(),
		LastError:    f.lastErr,
		LastErrorAt:  f.lastErrAt,
	}
}
