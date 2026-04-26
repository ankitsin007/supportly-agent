// Package docker tails container stdout/stderr from the Docker daemon.
//
// Strategy:
//   - Connect to /var/run/docker.sock via the Docker Engine HTTP API.
//   - Enumerate currently-running containers; subscribe to their log streams.
//   - Subscribe to /events to notice new containers as they start, and to
//     reap goroutines for stopped ones.
//
// We talk to the API directly rather than vendoring the heavy
// github.com/docker/docker SDK (~50 transitive deps). The two endpoints we
// need (containers list and logs follow) have stable shapes we can decode
// with the standard library.
//
// Multiplex framing: the /containers/<id>/logs endpoint returns frames with
// an 8-byte header per chunk: [stream(1), 0,0,0, length(4 BE)]. We strip
// the headers and split on newline.
package docker

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ankitsin007/supportly-agent/internal/source"
)

const (
	// Default Docker socket path. Override via NewWithSocket.
	defaultSocket = "/var/run/docker.sock"
)

// Source tails Docker container logs.
type Source struct {
	socket string
	client *http.Client

	// excludeContainers is a set of container names we skip (e.g. "healthcheck").
	excludeContainers map[string]bool

	emitted atomic.Uint64
	dropped atomic.Uint64

	mu        sync.Mutex
	lastErr   string
	lastErrAt time.Time
	tailing   map[string]context.CancelFunc // container ID → cancel
}

// New returns a Docker source using the default socket.
func New(excludeContainers []string) *Source {
	return NewWithSocket(defaultSocket, excludeContainers)
}

// NewWithSocket allows tests to point at a Unix socket fixture.
func NewWithSocket(socket string, excludeContainers []string) *Source {
	excl := make(map[string]bool, len(excludeContainers))
	for _, c := range excludeContainers {
		excl[c] = true
	}
	return &Source{
		socket:            socket,
		excludeContainers: excl,
		tailing:           make(map[string]context.CancelFunc),
		client: &http.Client{
			Transport: &http.Transport{
				DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", socket)
				},
				// No timeout on the overall request — log streams are long-lived.
			},
		},
	}
}

// Name implements source.Source.
func (s *Source) Name() string { return "docker:" + s.socket }

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

// Stop is a no-op; Start exits when ctx is cancelled.
func (s *Source) Stop() error { return nil }

// Start lists running containers, subscribes to their logs, and watches
// the events stream for new ones. Exits when ctx is done.
func (s *Source) Start(ctx context.Context, out chan<- source.RawLog) error {
	// Enumerate currently-running containers.
	conts, err := s.listContainers(ctx)
	if err != nil {
		s.recordErr(err)
		return fmt.Errorf("list containers: %w", err)
	}
	for _, c := range conts {
		s.maybeFollow(ctx, c, out)
	}

	// Watch /events for container start/die.
	return s.watchEvents(ctx, out)
}

type container struct {
	ID    string
	Names []string
}

func (s *Source) listContainers(ctx context.Context) ([]container, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/containers/json", nil)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("containers list returned %d: %s", resp.StatusCode, body)
	}
	var raw []struct {
		ID    string   `json:"Id"`
		Names []string `json:"Names"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]container, 0, len(raw))
	for _, r := range raw {
		out = append(out, container{ID: r.ID, Names: r.Names})
	}
	return out, nil
}

// maybeFollow starts a goroutine tailing the container's logs unless it's
// in the exclude set or already being tailed.
func (s *Source) maybeFollow(ctx context.Context, c container, out chan<- source.RawLog) {
	name := primaryName(c.Names)
	if s.excludeContainers[name] {
		return
	}
	s.mu.Lock()
	if _, exists := s.tailing[c.ID]; exists {
		s.mu.Unlock()
		return
	}
	tailCtx, cancel := context.WithCancel(ctx)
	s.tailing[c.ID] = cancel
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.tailing, c.ID)
			s.mu.Unlock()
		}()
		if err := s.followLogs(tailCtx, c.ID, name, out); err != nil && tailCtx.Err() == nil {
			slog.Warn("docker tail failed", "container", name, "err", err)
			s.recordErr(err)
		}
	}()
}

// followLogs streams logs for one container until the stream closes or ctx
// is cancelled. Uses follow=1, since=now to avoid re-shipping historical lines.
func (s *Source) followLogs(ctx context.Context, id, name string, out chan<- source.RawLog) error {
	q := url.Values{}
	q.Set("stdout", "1")
	q.Set("stderr", "1")
	q.Set("follow", "1")
	q.Set("since", fmt.Sprintf("%d", time.Now().Unix()))
	q.Set("timestamps", "0")

	uri := "http://docker/containers/" + id + "/logs?" + q.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("logs returned %d: %s", resp.StatusCode, body)
	}

	return demux(ctx, resp.Body, name, out, &s.emitted, &s.dropped)
}

// demux strips Docker's 8-byte multiplex header and emits one RawLog per
// newline-terminated line. Frames may carry partial lines; we buffer.
func demux(ctx context.Context, r io.Reader, container string, out chan<- source.RawLog, emitted, dropped *atomic.Uint64) error {
	br := bufio.NewReader(r)
	var buf []byte
	for {
		header := make([]byte, 8)
		if _, err := io.ReadFull(br, header); err != nil {
			if err == io.EOF || ctx.Err() != nil {
				return nil
			}
			return err
		}
		size := binary.BigEndian.Uint32(header[4:])
		payload := make([]byte, size)
		if _, err := io.ReadFull(br, payload); err != nil {
			return err
		}
		buf = append(buf, payload...)

		for {
			i := indexNewline(buf)
			if i == -1 {
				break
			}
			line := string(buf[:i])
			buf = buf[i+1:]
			rl := source.RawLog{
				Source:    "docker",
				Timestamp: time.Now().UTC(),
				Line:      line,
				Tags:      map[string]string{"container_name": container},
			}
			select {
			case out <- rl:
				emitted.Add(1)
			case <-ctx.Done():
				return nil
			default:
				dropped.Add(1)
			}
		}
	}
}

func indexNewline(b []byte) int {
	for i, c := range b {
		if c == '\n' {
			return i
		}
	}
	return -1
}

// watchEvents subscribes to /events and starts/stops tail goroutines as
// containers come and go.
func (s *Source) watchEvents(ctx context.Context, out chan<- source.RawLog) error {
	q := url.Values{}
	q.Set("filters", `{"type":["container"],"event":["start","die"]}`)
	uri := "http://docker/events?" + q.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	dec := json.NewDecoder(resp.Body)
	for {
		var ev struct {
			Status string `json:"status"`
			ID     string `json:"id"`
			Actor  struct {
				Attributes map[string]string `json:"Attributes"`
			} `json:"Actor"`
		}
		if err := dec.Decode(&ev); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		switch ev.Status {
		case "start":
			s.maybeFollow(ctx, container{
				ID:    ev.ID,
				Names: []string{"/" + ev.Actor.Attributes["name"]},
			}, out)
		case "die":
			s.mu.Lock()
			if cancel, ok := s.tailing[ev.ID]; ok {
				cancel()
			}
			s.mu.Unlock()
		}
	}
}

func primaryName(names []string) string {
	if len(names) == 0 {
		return "unknown"
	}
	return strings.TrimPrefix(names[0], "/")
}

func (s *Source) recordErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastErr = err.Error()
	s.lastErrAt = time.Now().UTC()
}
