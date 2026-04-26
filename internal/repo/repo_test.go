package repo

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// fakeIngest records every shipBatch call so tests can assert what
// was sent without spinning up the real Supportly stack.
type fakeIngest struct {
	calls atomic.Int32
	last  []byte
}

func (f *fakeIngest) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.last = body
		f.calls.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}
}

func newSource(url string) *Source {
	return New(Config{
		Name: "demo", URL: "https://github.com/example/x.git",
		Branch: "main", IntervalSeconds: 3600,
	}, url, "agt_fake")
}

func TestShipBatch_PostsAuthAndContentType(t *testing.T) {
	var (
		gotAuth, gotCT string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("X-Agent-Token")
		gotCT = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	s := newSource(srv.URL)
	chunks := ExtractPythonChunks("def x(): return 1\n", "x.py")
	if err := s.shipBatch(context.Background(), "abc123", chunks, false); err != nil {
		t.Fatalf("shipBatch: %v", err)
	}
	if gotAuth != "agt_fake" {
		t.Errorf("X-Agent-Token = %q", gotAuth)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q", gotCT)
	}
}

func TestShipBatch_PayloadShape(t *testing.T) {
	f := &fakeIngest{}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	s := newSource(srv.URL)
	chunks := ExtractPythonChunks("def x(): return 1\n", "x.py")
	if err := s.shipBatch(context.Background(), "abc123", chunks, true); err != nil {
		t.Fatalf("shipBatch: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(f.last, &body); err != nil {
		t.Fatalf("decode: %v body=%s", err, f.last)
	}
	if body["repo_name"] != "demo" {
		t.Errorf("repo_name = %v", body["repo_name"])
	}
	if body["source_sha"] != "abc123" {
		t.Errorf("source_sha = %v", body["source_sha"])
	}
	if body["done"] != true {
		t.Errorf("done = %v", body["done"])
	}
	got := body["chunks"].([]any)
	if len(got) != 1 {
		t.Errorf("chunks len = %d", len(got))
	}
}

func TestShipBatch_4xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	s := newSource(srv.URL)
	if err := s.shipBatch(context.Background(), "x", nil, true); err == nil {
		t.Fatal("expected error on 401, got nil")
	}
}

func TestSource_DefaultsApplied(t *testing.T) {
	s := New(Config{Name: "x", URL: "u"}, "http://u", "tok")
	if s.Cfg.Branch != "main" {
		t.Errorf("default branch should be 'main', got %q", s.Cfg.Branch)
	}
	if s.Cfg.IntervalSeconds != 3600 {
		t.Errorf("default interval should be 3600, got %d", s.Cfg.IntervalSeconds)
	}
}

func TestSource_HealthSnapshotInitial(t *testing.T) {
	s := New(Config{Name: "x", URL: "u"}, "http://u", "tok")
	h := s.HealthSnapshot()
	if !h.Healthy {
		t.Errorf("fresh source should be healthy")
	}
	if h.ChunksShipped != 0 {
		t.Errorf("ChunksShipped = %d", h.ChunksShipped)
	}
}

func TestSource_IncludesDefaultsToPython(t *testing.T) {
	s := New(Config{Name: "x", URL: "u"}, "http://u", "tok")
	if !s.includes("foo.py") {
		t.Error("default should include .py")
	}
	if s.includes("foo.go") {
		t.Error("default should NOT include .go (until M2.5)")
	}
}

func TestSource_IncludesRespectsConfigOverride(t *testing.T) {
	s := New(Config{
		Name: "x", URL: "u",
		IncludeExtensions: []string{".go", ".rs"},
	}, "http://u", "tok")
	if !s.includes("a.go") || !s.includes("b.rs") {
		t.Error("explicit list should be honoured")
	}
	if s.includes("a.py") {
		t.Error(".py should NOT match when explicit list excludes it")
	}
}
