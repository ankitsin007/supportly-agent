// Package repo — Python function/method extractor.
//
// The agent doesn't have access to Python's stdlib AST, so we do the same
// work the backend's services/repo_indexer.py does, but with regex +
// indentation tracking. Function bodies extend until we see a line whose
// indent ≤ the def line's indent (and that line is non-blank, non-comment).
//
// This matches what Python's ast.FunctionDef yields for end_lineno,
// modulo edge cases with weird indentation. Edge cases produce slightly
// larger or smaller chunks; the diagnostic agent's frame-lookup tolerates
// off-by-a-few since it picks the chunk whose [start, end] CONTAINS the
// stack-frame line.
package repo

import (
	"regexp"
	"strings"
)

// `def name(...)` or `async def name(...)`. Captures the indent and name.
var pyDefRE = regexp.MustCompile(
	`^(?P<indent>[ \t]*)(?:async\s+)?def\s+(?P<name>[A-Za-z_]\w*)\s*\(`,
)

// ExtractPythonChunks scans a Python source string and emits one chunk
// per top-level function or class method.
//
// Returns [] on empty input. Never errors — best-effort, like the
// rest of the agent's extractor pipeline.
func ExtractPythonChunks(source, filePath string) []Chunk {
	if source == "" {
		return nil
	}
	lines := strings.Split(source, "\n")

	var chunks []Chunk
	for i, line := range lines {
		m := pyDefRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		indent := indentOf(m[1])
		name := m[2]
		startLine := i + 1
		endLine := findFunctionEnd(lines, i, indent)
		content := strings.Join(lines[startLine-1:endLine], "\n")

		// Heuristic for "method": first arg in the def is `self` or `cls`.
		// We need to compare against the keyword's actual length (`self`
		// is 4, `cls` is 3) — the original off-by-one missed `def f(cls)`.
		kind := "function"
		paren := strings.Index(line, "(")
		if paren != -1 {
			rest := line[paren+1:]
			for _, kw := range []string{"self", "cls"} {
				if !strings.HasPrefix(rest, kw) {
					continue
				}
				// Either the keyword IS the entire remaining text (no
				// closing paren on this line) or the next char terminates
				// the param.
				if len(rest) == len(kw) {
					kind = "method"
					break
				}
				next := rest[len(kw)]
				if next == ',' || next == ')' || next == ' ' || next == '\t' || next == ':' {
					kind = "method"
				}
				break
			}
		}

		chunks = append(chunks, Chunk{
			FilePath:    filePath,
			StartLine:   startLine,
			EndLine:     endLine,
			Kind:        kind,
			Name:        name,
			Language:    "python",
			Content:     content,
			ContentHash: HashChunk(filePath, content),
		})
	}
	return chunks
}

// indentOf returns the column count for a leading-whitespace string.
// Tabs count as 1 (matches Python's "no mixed indents" convention; if
// the source uses tabs everywhere the comparisons still work because
// they're consistent).
func indentOf(s string) int {
	n := 0
	for _, c := range s {
		if c == ' ' || c == '\t' {
			n++
		} else {
			break
		}
	}
	return n
}

// findFunctionEnd walks forward from the def line and returns the
// 1-based line number of the function's last line.
//
// Algorithm: the function ends at the line BEFORE the next non-blank,
// non-comment line whose indent is ≤ defIndent. If no such line
// exists, the function extends to EOF.
func findFunctionEnd(lines []string, defLineIdx, defIndent int) int {
	for j := defLineIdx + 1; j < len(lines); j++ {
		ln := lines[j]
		stripped := strings.TrimLeft(ln, " \t")
		if stripped == "" {
			continue
		}
		if strings.HasPrefix(stripped, "#") {
			continue
		}
		if indentOf(ln) <= defIndent {
			return j // 1-based: index j is line j+1, so end is line j (i.e. j-1+1)
		}
	}
	return len(lines)
}
