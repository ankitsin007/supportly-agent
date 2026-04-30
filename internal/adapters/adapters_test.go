package adapters

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGet_KnownLanguagesReturnNonEmpty(t *testing.T) {
	for _, lang := range []string{"python", "node", "java"} {
		t.Run(lang, func(t *testing.T) {
			snippet, ok := Get(lang)
			if !ok {
				t.Fatalf("Get(%q) ok=false", lang)
			}
			if !strings.Contains(snippet, "127.0.0.1:4318") {
				t.Errorf("snippet for %s should reference the agent's OTLP endpoint", lang)
			}
		})
	}
}

func TestGet_UnknownReturnsNotOK(t *testing.T) {
	if _, ok := Get("cobol"); ok {
		t.Error("Get(\"cobol\") should be ok=false")
	}
}

func TestList_SortedAndComplete(t *testing.T) {
	got := List()
	want := []string{"java", "node", "python"}
	if len(got) != len(want) {
		t.Fatalf("List() len = %d want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("List()[%d] = %q want %q (sort order broken)", i, got[i], w)
		}
	}
}

func TestEmbeddedMatchesDisk(t *testing.T) {
	// Guard against drift: the markdown a docs reader sees on GitHub
	// must equal what the binary embeds. If a future PR edits one
	// without the other, this test fires.
	repoRoot := findRepoRoot(t)
	for _, lang := range []string{"python", "node", "java"} {
		t.Run(lang, func(t *testing.T) {
			diskPath := filepath.Join(repoRoot, "adapters", lang+".md")
			disk, err := os.ReadFile(diskPath)
			if err != nil {
				t.Fatalf("read %s: %v", diskPath, err)
			}
			embedded, _ := Get(lang)
			if string(disk) != embedded {
				t.Errorf("adapters/%s.md drifted from internal/adapters/embedded/%s.md — re-copy or run `make adapters-sync`", lang, lang)
			}
		})
	}
}

// findRepoRoot walks up from the test file's directory until it finds
// a `go.mod`. Lets the test work regardless of where pytest/go test
// is invoked from.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.mod walking up from %s", wd)
		}
		dir = parent
	}
}
