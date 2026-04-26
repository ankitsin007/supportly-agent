// Package journald reads systemd-journal entries by tailing the binary
// journal files directly via `journalctl -o json -f`.
//
// We deliberately AVOID linking against libsystemd (cgo) so the agent stays
// CGO_ENABLED=0 and ships as a single static binary. The trade-off: we need
// the journalctl binary present at runtime. Every distro that ships systemd
// has it, so this is a non-issue in practice.
//
// Each emitted log line carries:
//   - tags.systemd_unit  : the unit that emitted (e.g. "nginx.service")
//   - tags.hostname      : the journald _HOSTNAME field
//   - tags.priority      : syslog priority 0-7 (0=emerg, 7=debug)
//
// The parser pipeline downstream uses these as platform/level hints when
// the message body itself isn't structured.
package journald

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ankitsin007/supportly-agent/internal/source"
)

// Source tails the system journal.
type Source struct {
	// Units is an optional allow-list. Empty = follow all units except
	// the systemd-internal noise we filter in defaultExcludePrefixes.
	Units []string

	// JournalctlPath is the binary to exec; defaults to "journalctl" via PATH.
	JournalctlPath string

	emitted atomic.Uint64
	dropped atomic.Uint64

	mu        sync.Mutex
	lastErr   string
	lastErrAt time.Time
}

// New returns a Source with default settings. Pass non-nil units to filter.
func New(units []string) *Source {
	return &Source{Units: units}
}

// Name implements source.Source.
func (s *Source) Name() string { return "journald" }

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

// Stop is a no-op; ctx cancellation drives shutdown.
func (s *Source) Stop() error { return nil }

// defaultExcludePrefixes filters out internal systemd unit chatter that
// would otherwise drown signal in noise.
var defaultExcludePrefixes = []string{
	"systemd-",
	"dbus.",
	"polkit.",
}

// Start runs `journalctl -o json -f` and emits one RawLog per JSON entry.
// Exits when ctx is done OR journalctl exits unexpectedly.
func (s *Source) Start(ctx context.Context, out chan<- source.RawLog) error {
	bin := s.JournalctlPath
	if bin == "" {
		bin = "journalctl"
	}
	args := []string{"-o", "json", "-f", "--no-pager", "--since", "now"}
	for _, u := range s.Units {
		args = append(args, "-u", u)
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		s.recordErr(err)
		return fmt.Errorf("start journalctl: %w", err)
	}

	// Surface any stderr (auth errors, missing binary) without burying it.
	go func() {
		br := bufio.NewScanner(stderr)
		for br.Scan() {
			slog.Warn("journalctl stderr", "line", br.Text())
		}
	}()

	scan := bufio.NewScanner(stdout)
	// journalctl can emit very long single-entry JSON lines; bump the buffer.
	scan.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scan.Scan() {
		if ctx.Err() != nil {
			break
		}
		s.processEntry(ctx, scan.Bytes(), out)
	}

	if err := scan.Err(); err != nil && ctx.Err() == nil {
		s.recordErr(err)
		return fmt.Errorf("scan journal: %w", err)
	}
	if err := cmd.Wait(); err != nil && ctx.Err() == nil {
		s.recordErr(err)
		return fmt.Errorf("journalctl exited: %w", err)
	}
	return nil
}

func (s *Source) processEntry(ctx context.Context, raw []byte, out chan<- source.RawLog) {
	var e map[string]any
	if err := json.Unmarshal(raw, &e); err != nil {
		// Some journal entries have non-string MESSAGE arrays for binary
		// payloads. Skip rather than fail.
		return
	}
	unit := stringField(e, "_SYSTEMD_UNIT", "UNIT")
	if isExcluded(unit) {
		return
	}
	msg := stringField(e, "MESSAGE")
	if msg == "" {
		return
	}

	rl := source.RawLog{
		Source:    "journald",
		Timestamp: time.Now().UTC(),
		Line:      msg,
		Tags: map[string]string{
			"systemd_unit": unit,
			"hostname":     stringField(e, "_HOSTNAME"),
			"priority":     stringField(e, "PRIORITY"),
		},
	}
	if v := stringField(e, "__REALTIME_TIMESTAMP"); v != "" {
		// journald gives microseconds-since-epoch as a string.
		if t, ok := parseRealtimeUS(v); ok {
			rl.Timestamp = t
		}
	}
	select {
	case out <- rl:
		s.emitted.Add(1)
	case <-ctx.Done():
	default:
		s.dropped.Add(1)
	}
}

func isExcluded(unit string) bool {
	for _, p := range defaultExcludePrefixes {
		if len(unit) >= len(p) && unit[:len(p)] == p {
			return true
		}
	}
	return false
}

func stringField(e map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := e[k].(string); ok {
			return v
		}
	}
	return ""
}

func parseRealtimeUS(us string) (time.Time, bool) {
	// Microseconds since unix epoch as a decimal string.
	var n int64
	for i := 0; i < len(us); i++ {
		c := us[i]
		if c < '0' || c > '9' {
			return time.Time{}, false
		}
		n = n*10 + int64(c-'0')
	}
	return time.Unix(0, n*1000).UTC(), true
}

func (s *Source) recordErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastErr = err.Error()
	s.lastErrAt = time.Now().UTC()
}
