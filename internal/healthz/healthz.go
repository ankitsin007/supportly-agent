// Package healthz exposes a tiny localhost-only HTTP endpoint at
// 127.0.0.1:9876/healthz that install.sh polls to confirm the agent is
// alive after install. Also returns aggregated source health for
// debugging.
//
// Loopback-only by design (127.0.0.1, not 0.0.0.0) — there's no auth on
// /healthz because there's no remote attack surface.
package healthz

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"time"

	"github.com/ankitsin007/supportly-agent/internal/source"
)

const Addr = "127.0.0.1:9876"

// Snapshot is what /healthz returns.
type Snapshot struct {
	Status  string              `json:"status"`
	Version string              `json:"version,omitempty"`
	Sources []SourceHealthEntry `json:"sources,omitempty"`
}

type SourceHealthEntry struct {
	Name         string `json:"name"`
	Healthy      bool   `json:"healthy"`
	LinesEmitted uint64 `json:"lines_emitted"`
	LinesDropped uint64 `json:"lines_dropped"`
	LastError    string `json:"last_error,omitempty"`
}

// Server holds the running httptest-style server.
type Server struct {
	srv     *http.Server
	sources []source.Source
	version string
}

// New returns a stopped server. Call Start to bind. Always supplies the
// current set of sources for status reporting.
func New(version string, sources []source.Source) *Server {
	return &Server{version: version, sources: sources}
}

// Start binds 127.0.0.1:9876 and serves until ctx is done. Returns the
// listen error if bind fails (port already in use, etc.); silently
// returns nil on graceful shutdown.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handle)

	s.srv = &http.Server{
		Addr:              Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ln, err := net.Listen("tcp", Addr)
	if err != nil {
		return err
	}

	errCh := make(chan error, 1)
	go func() {
		if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(shutCtx)
		return nil
	case err := <-errCh:
		return err
	}
}

func (s *Server) handle(w http.ResponseWriter, _ *http.Request) {
	snap := Snapshot{
		Status:  "ok",
		Version: s.version,
	}
	for _, src := range s.sources {
		h := src.Health()
		entry := SourceHealthEntry{
			Name:         src.Name(),
			Healthy:      h.Healthy,
			LinesEmitted: h.LinesEmitted,
			LinesDropped: h.LinesDropped,
		}
		if !h.Healthy {
			entry.LastError = h.LastError
			snap.Status = "degraded"
		}
		snap.Sources = append(snap.Sources, entry)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snap)
}
