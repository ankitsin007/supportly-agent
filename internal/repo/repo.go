// Package repo implements the agent's "self-host repo source" — clones
// a customer-configured git repo inside their VPC, walks files, extracts
// function-grain chunks, and ships them to Supportly's ingest endpoint
// for indexing. Source code never leaves the customer's network beyond
// the shipped chunk text + metadata (which the customer's Supportly
// org already has access to via the issue-event payload).
//
// This is M2's zero-egress alternative to the GitHub-App path. For
// customers who can't or won't grant Supportly direct GitHub access,
// they install the agent inside their VPC, point it at a local git
// remote (or a private GitHub URL with credentials), and the agent
// does the cloning + extraction itself.
//
// Embedding stays server-side: the OpenAI API key lives in Supportly,
// not on the customer's host. The agent ships raw chunks; Supportly's
// indexer worker embeds them on receipt.
//
// Lifecycle:
//  1. Config declares a repo entry (URL, branch, indexer interval).
//  2. On agent startup, RepoSource.Start spawns a ticker.
//  3. Each tick: shallow-clone, walk, extract, post chunks in batches
//     to /api/v1/ingest-agents/repo-chunks.
//  4. Mark the repo "ready" via a final POST with sha + chunk_count.
package repo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Config is the per-repo entry from agent.yaml.
type Config struct {
	// Identifier the customer assigns. Echoed back to Supportly so the
	// dashboard can show "indexed by agent X". Must be stable across
	// runs because Supportly de-dups by (project_id, name).
	Name string `yaml:"name"`

	// Git URL to clone. HTTPS or SSH; the agent uses whatever git can
	// resolve from $HOME/.ssh + $HOME/.git-credentials on the host.
	URL string `yaml:"url"`

	// Branch to index. Default "main".
	Branch string `yaml:"branch"`

	// How often to re-index. Default 1 hour. Webhook-driven re-index
	// isn't supported in M2.4; that's a follow-up.
	IntervalSeconds int `yaml:"interval_seconds"`

	// File-extension allow-list. Empty = all supported (.py).
	IncludeExtensions []string `yaml:"include_extensions"`
}

// Source ships chunks to Supportly's ingest endpoint.
type Source struct {
	Cfg        Config
	IngestURL  string // e.g. https://ingest.supportly.io/api/v1/ingest-agents/repo-chunks
	AgentToken string // X-Agent-Token bearer

	// Stats for /healthz.
	cloned        atomic.Uint64
	chunksShipped atomic.Uint64
	chunksDropped atomic.Uint64

	mu          sync.Mutex
	lastErr     string
	lastErrAt   time.Time
	lastIndexed time.Time
}

// New returns a Source with sensible defaults filled in.
func New(cfg Config, ingestURL, agentToken string) *Source {
	if cfg.Branch == "" {
		cfg.Branch = "main"
	}
	if cfg.IntervalSeconds <= 0 {
		cfg.IntervalSeconds = 3600
	}
	return &Source{Cfg: cfg, IngestURL: ingestURL, AgentToken: agentToken}
}

// Name implements a sentinel "log source"-style identifier for logs/metrics.
func (s *Source) Name() string { return "repo:" + s.Cfg.Name }

// Health snapshot for /healthz.
type Health struct {
	Healthy       bool      `json:"healthy"`
	ChunksShipped uint64    `json:"chunks_shipped"`
	ChunksDropped uint64    `json:"chunks_dropped"`
	LastIndexed   time.Time `json:"last_indexed,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
}

func (s *Source) HealthSnapshot() Health {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Health{
		Healthy:       s.lastErr == "",
		ChunksShipped: s.chunksShipped.Load(),
		ChunksDropped: s.chunksDropped.Load(),
		LastIndexed:   s.lastIndexed,
		LastError:     s.lastErr,
	}
}

// Start runs the indexing loop until ctx is done.
func (s *Source) Start(ctx context.Context) error {
	slog.Info("repo source starting", "name", s.Cfg.Name, "url", s.Cfg.URL,
		"branch", s.Cfg.Branch, "interval_s", s.Cfg.IntervalSeconds)

	// First pass immediately so post-install verify shows progress fast.
	if err := s.runOnce(ctx); err != nil {
		s.recordErr(err)
	}

	ticker := time.NewTicker(time.Duration(s.Cfg.IntervalSeconds) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := s.runOnce(ctx); err != nil {
				s.recordErr(err)
			}
		}
	}
}

// runOnce performs one full clone + extract + ship cycle.
func (s *Source) runOnce(ctx context.Context) error {
	tmp, err := os.MkdirTemp("", "supagt-repo-*")
	if err != nil {
		return fmt.Errorf("mkdtemp: %w", err)
	}
	defer os.RemoveAll(tmp)

	sha, err := s.clone(ctx, tmp)
	if err != nil {
		return fmt.Errorf("clone: %w", err)
	}
	s.cloned.Add(1)

	chunks, err := s.walkAndExtract(tmp)
	if err != nil {
		return fmt.Errorf("walk: %w", err)
	}

	// Ship in batches so a single 50KB+ payload doesn't stall under
	// rate limits or 5xx storms.
	const batch = 50
	shipped := 0
	for i := 0; i < len(chunks); i += batch {
		end := i + batch
		if end > len(chunks) {
			end = len(chunks)
		}
		if err := s.shipBatch(ctx, sha, chunks[i:end], false); err != nil {
			s.chunksDropped.Add(uint64(len(chunks) - shipped))
			return fmt.Errorf("ship batch [%d:%d]: %w", i, end, err)
		}
		shipped += end - i
		s.chunksShipped.Add(uint64(end - i))
	}

	// Final empty batch with done=true tells Supportly to mark the
	// mirror 'ready' and run the post-index sweep.
	if err := s.shipBatch(ctx, sha, nil, true); err != nil {
		return fmt.Errorf("ship done marker: %w", err)
	}

	s.mu.Lock()
	s.lastIndexed = time.Now().UTC()
	s.lastErr = ""
	s.mu.Unlock()
	slog.Info("repo indexed", "name", s.Cfg.Name, "sha", sha[:8],
		"chunks", len(chunks))
	return nil
}

func (s *Source) clone(ctx context.Context, dest string) (string, error) {
	cmd := exec.CommandContext(
		ctx, "git", "clone", "--depth=1", "--single-branch",
		"--branch", s.Cfg.Branch, s.Cfg.URL, dest,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git clone: %w (output=%s)", err, string(out))
	}
	sha, err := exec.CommandContext(
		ctx, "git", "-C", dest, "rev-parse", "HEAD",
	).Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	return string(bytes.TrimSpace(sha)), nil
}

// Chunk is the wire shape posted to Supportly.
type Chunk struct {
	FilePath    string `json:"file_path"`
	StartLine   int    `json:"start_line"`
	EndLine     int    `json:"end_line"`
	Kind        string `json:"kind"`
	Name        string `json:"name,omitempty"`
	Language    string `json:"language"`
	Content     string `json:"content"`
	ContentHash string `json:"content_hash"`
}

func (s *Source) walkAndExtract(root string) ([]Chunk, error) {
	var chunks []Chunk
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // skip
		}
		if d.IsDir() {
			name := d.Name()
			// Match the backend's SKIP_DIRS list.
			switch name {
			case ".git", "node_modules", "__pycache__", ".venv", "venv",
				".tox", "site-packages", "dist", "build", ".next", ".cache":
				return filepath.SkipDir
			}
			return nil
		}
		// File include-list
		if !s.includes(path) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		// Walk Python files only in M2.4. Multi-language reuses the
		// backend's regex extractors and lands in M2.5.
		if filepath.Ext(rel) == ".py" {
			chunks = append(chunks, ExtractPythonChunks(string(body), rel)...)
		}
		return nil
	})
	return chunks, err
}

func (s *Source) includes(path string) bool {
	if len(s.Cfg.IncludeExtensions) == 0 {
		return filepath.Ext(path) == ".py"
	}
	ext := filepath.Ext(path)
	for _, e := range s.Cfg.IncludeExtensions {
		if e == ext {
			return true
		}
	}
	return false
}

// shipBatch posts a batch of chunks to Supportly. done=true on the
// final empty batch tells the server "this commit is fully shipped".
func (s *Source) shipBatch(
	ctx context.Context, sha string, chunks []Chunk, done bool,
) error {
	body := map[string]any{
		"repo_name":  s.Cfg.Name,
		"repo_url":   s.Cfg.URL,
		"branch":     s.Cfg.Branch,
		"source_sha": sha,
		"chunks":     chunks,
		"done":       done,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, s.IngestURL, bytes.NewReader(data),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Token", s.AgentToken)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	if resp.StatusCode >= 400 {
		return fmt.Errorf("ingest returned %d", resp.StatusCode)
	}
	return nil
}

func (s *Source) recordErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastErr = err.Error()
	s.lastErrAt = time.Now().UTC()
	slog.Warn("repo source error", "name", s.Cfg.Name, "err", err)
}

// HashChunk computes the content+path SHA256 the backend expects.
// Exposed so callers (and tests) don't need to recompute the prefix
// scheme each time.
func HashChunk(filePath, content string) string {
	h := sha256.New()
	h.Write([]byte(filePath))
	h.Write([]byte("\n"))
	h.Write([]byte(content))
	return hex.EncodeToString(h.Sum(nil))
}
