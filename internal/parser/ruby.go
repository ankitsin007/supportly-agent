// Package parser — Ruby traceback parser.
//
// Modern Ruby (>= 2.5) prints exceptions as:
//
//	/app/lib/foo.rb:42:in `bar': could not connect (ConnectionError)
//	  from /app/lib/baz.rb:18:in `init'
//	  from /app/main.rb:5:in `<main>'
//
// Older Ruby uses tabs instead of two-space indents; we accept both.
package parser

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/ankitsin007/supportly-agent/internal/envelope"
	"github.com/ankitsin007/supportly-agent/internal/source"
)

var (
	// rubyHeader: '/app/x.rb:42:in `foo': msg (ExcClass)' — first line.
	rubyHeader = regexp.MustCompile("^([^:]+):(\\d+):in `([^']+)':\\s*(.*?)\\s*\\(([\\w:]+)\\)$")

	// rubyFrame: '\tfrom /app/x.rb:42:in `foo'' or '  from ...'
	rubyFrame = regexp.MustCompile("^\\s+from\\s+([^:]+):(\\d+):in `([^']+)'$")
)

// Ruby recognizes Ruby exceptions.
type Ruby struct{}

// Name implements Parser.
func (Ruby) Name() string { return "ruby" }

// Parse implements Parser.
func (Ruby) Parse(raw source.RawLog, projectID string) *envelope.Envelope {
	lines := strings.Split(raw.Line, "\n")
	startIdx := -1
	var excType, excValue string
	var firstFrame envelope.Frame

	for i, ln := range lines {
		if m := rubyHeader.FindStringSubmatch(ln); m != nil {
			lineno, _ := strconv.Atoi(m[2])
			firstFrame = envelope.Frame{
				Filename: m[1],
				Lineno:   lineno,
				Function: m[3],
			}
			excValue = m[4]
			excType = m[5]
			startIdx = i
			break
		}
	}
	if startIdx == -1 {
		return nil
	}

	frames := []envelope.Frame{firstFrame}
	for i := startIdx + 1; i < len(lines); i++ {
		line := lines[i]
		m := rubyFrame.FindStringSubmatch(line)
		if m == nil {
			break
		}
		lineno, _ := strconv.Atoi(m[2])
		frames = append(frames, envelope.Frame{
			Filename: m[1],
			Lineno:   lineno,
			Function: m[3],
		})
	}

	env := envelope.New(projectID, "ruby")
	env.Level = "error"
	env.Message = excType + ": " + excValue
	env.Exception = &envelope.Exception{
		Type:       excType,
		Value:      excValue,
		Stacktrace: &envelope.Stacktrace{Frames: frames},
	}
	return env
}
