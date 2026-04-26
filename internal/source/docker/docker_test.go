package docker

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ankitsin007/supportly-agent/internal/source"
)

// fakeDockerSocket spins up a Unix socket that responds to the two endpoints
// the source uses (containers/json + containers/<id>/logs). Lets us exercise
// the multiplex demuxer without a real Docker daemon.
//
// We use /tmp + a pid suffix instead of t.TempDir() because macOS limits
// Unix socket paths to 104 bytes; t.TempDir paths can exceed that easily.
func fakeDockerSocket(t *testing.T, handler http.Handler) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "supdkr*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sockPath := filepath.Join(dir, "d.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: handler}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { srv.Close() })
	return sockPath
}

func TestDemux_StripsFramingAndSplitsLines(t *testing.T) {
	// Build two Docker-framed payloads: one "stdout" frame containing two
	// newline-separated lines, then one "stderr" frame with one line.
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		writeFrame(pw, 1, []byte("first line\nsecond line\n"))
		writeFrame(pw, 2, []byte("err line\n"))
	}()

	out := make(chan source.RawLog, 10)
	var emitted, dropped atomic.Uint64
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	if err := demux(ctx, pr, "test-container", out, &emitted, &dropped); err != nil {
		t.Errorf("demux: %v", err)
	}

	want := []string{"first line", "second line", "err line"}
	for _, w := range want {
		select {
		case rl := <-out:
			if rl.Line != w {
				t.Errorf("line = %q, want %q", rl.Line, w)
			}
			if rl.Tags["container_name"] != "test-container" {
				t.Errorf("tag missing: %v", rl.Tags)
			}
		case <-time.After(200 * time.Millisecond):
			t.Fatalf("missing line %q", w)
		}
	}
	if emitted.Load() != 3 {
		t.Errorf("emitted = %d, want 3", emitted.Load())
	}
}

func writeFrame(w io.Writer, stream byte, payload []byte) {
	header := make([]byte, 8)
	header[0] = stream
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	_, _ = w.Write(header)
	_, _ = w.Write(payload)
}

func TestSource_ListContainersHTTPError(t *testing.T) {
	sock := fakeDockerSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("socket not created: %v", err)
	}

	s := NewWithSocket(sock, nil)
	out := make(chan source.RawLog, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := s.Start(ctx, out)
	if err == nil {
		t.Fatal("expected error from Start when Docker returns 500")
	}
	h := s.Health()
	if h.LastError == "" {
		t.Errorf("Health.LastError empty")
	}
}
