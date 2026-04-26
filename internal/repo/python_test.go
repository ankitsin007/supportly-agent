package repo

import (
	"strings"
	"testing"
)

func TestExtractPythonChunks_TopLevelFunction(t *testing.T) {
	src := `def hello(name):
    return f"hi {name}"

def goodbye(name):
    return f"bye {name}"
`
	chunks := ExtractPythonChunks(src, "greet.py")
	if len(chunks) != 2 {
		t.Fatalf("want 2 chunks, got %d", len(chunks))
	}
	if chunks[0].Name != "hello" || chunks[1].Name != "goodbye" {
		t.Errorf("names = %s, %s", chunks[0].Name, chunks[1].Name)
	}
	if chunks[0].Kind != "function" {
		t.Errorf("kind = %q", chunks[0].Kind)
	}
	if !strings.Contains(chunks[0].Content, "hi {name}") {
		t.Errorf("content missing body: %q", chunks[0].Content)
	}
}

func TestExtractPythonChunks_Methods(t *testing.T) {
	src := `class Greeter:
    def greet(self, name):
        return hello(name)

    def shout(self, name):
        return hello(name).upper()

    @classmethod
    def factory(cls):
        return cls()
`
	chunks := ExtractPythonChunks(src, "greeter.py")
	names := chunkNames(chunks)
	if !contains(names, "greet") || !contains(names, "shout") || !contains(names, "factory") {
		t.Errorf("missing methods, got %v", names)
	}
	for _, c := range chunks {
		if c.Kind != "method" {
			t.Errorf("expected method kind for %s, got %s", c.Name, c.Kind)
		}
	}
}

func TestExtractPythonChunks_AsyncFunction(t *testing.T) {
	src := `async def fetch(url):
    return await client.get(url)
`
	chunks := ExtractPythonChunks(src, "client.py")
	if len(chunks) != 1 || chunks[0].Name != "fetch" {
		t.Fatalf("got %v", chunks)
	}
}

func TestExtractPythonChunks_FunctionEndsAtNextDef(t *testing.T) {
	src := `def first():
    return 1


def second():
    return 2
`
	chunks := ExtractPythonChunks(src, "x.py")
	if len(chunks) != 2 {
		t.Fatalf("want 2 chunks, got %d", len(chunks))
	}
	// 'first' should NOT include the body of 'second'.
	if strings.Contains(chunks[0].Content, "return 2") {
		t.Errorf("first leaked into second: %q", chunks[0].Content)
	}
}

func TestExtractPythonChunks_NestedFunctionsBothCaptured(t *testing.T) {
	src := `def outer():
    def inner():
        return 1
    return inner
`
	chunks := ExtractPythonChunks(src, "x.py")
	names := chunkNames(chunks)
	if !contains(names, "outer") || !contains(names, "inner") {
		t.Errorf("expected outer + inner, got %v", names)
	}
}

func TestExtractPythonChunks_EmptyInput(t *testing.T) {
	if got := ExtractPythonChunks("", "x.py"); got != nil {
		t.Errorf("expected nil for empty source, got %v", got)
	}
}

func TestExtractPythonChunks_HashChunkStable(t *testing.T) {
	src := `def hello():
    return 1
`
	a := ExtractPythonChunks(src, "x.py")[0]
	b := ExtractPythonChunks(src, "x.py")[0]
	if a.ContentHash != b.ContentHash {
		t.Error("ContentHash should be stable across calls")
	}
}

func TestExtractPythonChunks_HashChunkPathSensitive(t *testing.T) {
	src := `def hello():
    return 1
`
	a := ExtractPythonChunks(src, "a.py")[0].ContentHash
	b := ExtractPythonChunks(src, "b.py")[0].ContentHash
	if a == b {
		t.Error("ContentHash should differ when file path differs")
	}
}

func TestHashChunk_Direct(t *testing.T) {
	h1 := HashChunk("x.py", "def f(): pass")
	h2 := HashChunk("x.py", "def f(): pass")
	h3 := HashChunk("x.py", "def f(): return 1")
	if h1 != h2 {
		t.Error("same input should produce same hash")
	}
	if h1 == h3 {
		t.Error("different content should produce different hash")
	}
	if len(h1) != 64 {
		t.Errorf("expected sha256 hex length 64, got %d", len(h1))
	}
}

// --- helpers ---

func chunkNames(cs []Chunk) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
