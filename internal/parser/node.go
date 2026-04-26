// Package parser — Node.js stack trace parser.
//
// Recognizes V8's standard error.stack format:
//
//	TypeError: Cannot read property 'name' of undefined
//	    at Object.<anonymous> (/app/server.js:42:18)
//	    at Module._compile (internal/modules/cjs/loader.js:1063:30)
//	    at /app/lib/foo.js:10:5
package parser

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/ankitsin007/supportly-agent/internal/envelope"
	"github.com/ankitsin007/supportly-agent/internal/source"
)

var (
	// nodeStartRegex: '<TypeName>: <message>' on a line by itself.
	// Constrained to known JS error types to reduce false positives.
	nodeStartRegex = regexp.MustCompile(`^(Error|TypeError|RangeError|ReferenceError|SyntaxError|URIError|EvalError|AssertionError|\w+Error):\s*(.*)$`)

	// nodeFrameWithFunc: '    at funcName (file:line:col)'
	nodeFrameWithFunc = regexp.MustCompile(`^\s+at\s+(\S.*?)\s+\(([^):]+):(\d+):(\d+)\)$`)

	// nodeFrameNoFunc: '    at file:line:col' (anonymous)
	nodeFrameNoFunc = regexp.MustCompile(`^\s+at\s+([^():]+):(\d+):(\d+)$`)
)

// Node recognizes Node.js / V8 stack traces.
type Node struct{}

// Name implements Parser.
func (Node) Name() string { return "node" }

// Parse implements Parser.
func (Node) Parse(raw source.RawLog, projectID string) *envelope.Envelope {
	lines := strings.Split(raw.Line, "\n")
	startIdx := -1
	var excType, excValue string

	for i, ln := range lines {
		if m := nodeStartRegex.FindStringSubmatch(strings.TrimLeft(ln, " \t")); m != nil {
			// Require at least one frame line nearby to avoid matching
			// arbitrary "Error: foo" log lines that aren't real exceptions.
			if i+1 < len(lines) && (nodeFrameWithFunc.MatchString(lines[i+1]) ||
				nodeFrameNoFunc.MatchString(lines[i+1])) {
				excType = m[1]
				excValue = m[2]
				startIdx = i
				break
			}
		}
	}
	if startIdx == -1 {
		return nil
	}

	frames := make([]envelope.Frame, 0, 8)
	for i := startIdx + 1; i < len(lines); i++ {
		line := lines[i]
		if m := nodeFrameWithFunc.FindStringSubmatch(line); m != nil {
			lineno, _ := strconv.Atoi(m[3])
			frames = append(frames, envelope.Frame{
				Function: m[1],
				Filename: m[2],
				Lineno:   lineno,
			})
			continue
		}
		if m := nodeFrameNoFunc.FindStringSubmatch(line); m != nil {
			lineno, _ := strconv.Atoi(m[2])
			frames = append(frames, envelope.Frame{
				Function: "<anonymous>",
				Filename: m[1],
				Lineno:   lineno,
			})
			continue
		}
		// Stop when frames end.
		if !strings.HasPrefix(line, "    at ") && !strings.HasPrefix(line, "\tat ") {
			break
		}
	}

	env := envelope.New(projectID, "node")
	env.Level = "error"
	env.Message = excType + ": " + excValue
	env.Exception = &envelope.Exception{
		Type:       excType,
		Value:      excValue,
		Stacktrace: &envelope.Stacktrace{Frames: frames},
	}
	return env
}
