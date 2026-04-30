// Package adapters embeds the per-language OTel auto-instrument
// snippets at compile time so the `supportly-agent adapters <lang>`
// CLI works on air-gapped hosts without docs/ being shipped alongside
// the binary.
//
// Source of truth lives in adapters/*.md at the repo root. The tests
// in adapters_test.go assert that the embedded copy matches what's
// on disk so a doc-only edit can't drift away from the embedded
// version silently.
package adapters

import (
	_ "embed"
	"sort"
)

//go:embed embedded/python.md
var pythonAdapter string

//go:embed embedded/node.md
var nodeAdapter string

//go:embed embedded/java.md
var javaAdapter string

// adapters is the dispatcher table. Adding a new language = drop a
// .md into adapters/embedded/ and add a row here.
var adapters = map[string]string{
	"python": pythonAdapter,
	"node":   nodeAdapter,
	"java":   javaAdapter,
}

// Get returns the adapter snippet for `lang`. ok=false when the
// language isn't bundled.
func Get(lang string) (snippet string, ok bool) {
	snippet, ok = adapters[lang]
	return
}

// List returns the supported language names in sorted order.
func List() []string {
	out := make([]string, 0, len(adapters))
	for k := range adapters {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
