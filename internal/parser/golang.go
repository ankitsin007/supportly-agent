// Package parser — Go panic parser.
//
// Recognizes the runtime panic format:
//
//	panic: runtime error: index out of range [5] with length 3
//
//	goroutine 1 [running]:
//	main.processOrders(0xc00009a000)
//	        /app/main.go:42 +0x123
//	main.main()
//	        /app/main.go:18 +0x52
//	exit status 2
package parser

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/ankitsin007/supportly-agent/internal/envelope"
	"github.com/ankitsin007/supportly-agent/internal/source"
)

var (
	// goPanicStart: 'panic: ...' or 'fatal error: ...'
	goPanicStart = regexp.MustCompile(`^(panic|fatal error):\s*(.+)$`)

	// goFuncLine: 'pkg/path.FuncName(args)' or 'pkg.FuncName(args)'
	goFuncLine = regexp.MustCompile(`^([\w./\-]+\.[\w\-]+)\(.*\)$`)

	// goFileLine: '\t/path/to/file.go:42 +0x1a3'
	goFileLine = regexp.MustCompile(`^\s+([^:\s]+\.go):(\d+)(?:\s+.*)?$`)
)

// Go recognizes Go runtime panics.
type Go struct{}

// Name implements Parser.
func (Go) Name() string { return "go" }

// Parse implements Parser.
func (Go) Parse(raw source.RawLog, projectID string) *envelope.Envelope {
	lines := strings.Split(raw.Line, "\n")
	startIdx := -1
	var panicValue, panicType string

	for i, ln := range lines {
		if m := goPanicStart.FindStringSubmatch(ln); m != nil {
			panicType = "PanicError"
			if m[1] == "fatal error" {
				panicType = "FatalError"
			}
			panicValue = m[2]
			startIdx = i
			break
		}
	}
	if startIdx == -1 {
		return nil
	}

	frames := make([]envelope.Frame, 0, 8)
	// Walk lines after the panic header looking for func+file pairs.
	for i := startIdx + 1; i < len(lines); i++ {
		line := lines[i]
		if m := goFuncLine.FindStringSubmatch(line); m != nil {
			f := envelope.Frame{Function: m[1]}
			// Next line should be the file:line.
			if i+1 < len(lines) {
				if fm := goFileLine.FindStringSubmatch(lines[i+1]); fm != nil {
					f.Filename = fm[1]
					if n, err := strconv.Atoi(fm[2]); err == nil {
						f.Lineno = n
					}
					i++ // consume the file line
				}
			}
			frames = append(frames, f)
		}
	}

	env := envelope.New(projectID, "go")
	env.Level = "error"
	env.Message = "panic: " + panicValue
	env.Exception = &envelope.Exception{
		Type:       panicType,
		Value:      panicValue,
		Stacktrace: &envelope.Stacktrace{Frames: frames},
	}
	return env
}
