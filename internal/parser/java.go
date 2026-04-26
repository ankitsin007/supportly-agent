// Package parser — Java traceback parser.
//
// Recognizes the JVM's Throwable.printStackTrace format:
//
//	java.lang.NullPointerException: Cannot invoke "String.length()" ...
//	    at com.example.Foo.bar(Foo.java:42)
//	    at com.example.Foo.main(Foo.java:18)
//	    ... 5 more
//	Caused by: java.io.IOException: disk full
//	    at com.example.Bar.write(Bar.java:99)
//
// We capture the OUTERMOST exception (first line) and its frames. "Caused by"
// chains are summarized as a single Frame containing the cause type/message
// for now; richer chain support can land in a follow-up.
package parser

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/ankitsin007/supportly-agent/internal/envelope"
	"github.com/ankitsin007/supportly-agent/internal/source"
)

var (
	// javaExceptionStart: 'java.lang.NullPointerException: msg' OR
	// 'com.app.MyException' (no message) OR
	// 'Exception in thread "main" java.lang.X: msg'
	javaExceptionStart = regexp.MustCompile(
		`^(?:Exception in thread "[^"]+"\s+)?([\w$\.]+(?:Exception|Error|Throwable))(?::\s*(.*))?$`,
	)

	// javaFrame: '\tat com.app.Class.method(File.java:42)'
	// Also handles native methods, unknown source, and module/class-loader prefixes.
	javaFrame = regexp.MustCompile(
		`^\s+at\s+(?:[\w./@$\-]+/)?([\w$\.]+)\(([^:)]*)(?::(\d+))?\)$`,
	)
)

// Java recognizes JVM exception traces.
type Java struct{}

// Name implements Parser.
func (Java) Name() string { return "java" }

// Parse implements Parser.
func (Java) Parse(raw source.RawLog, projectID string) *envelope.Envelope {
	lines := strings.Split(raw.Line, "\n")
	startIdx := -1
	var excType, excValue string

	for i, ln := range lines {
		if m := javaExceptionStart.FindStringSubmatch(ln); m != nil {
			// Require at least one frame line right after.
			if i+1 < len(lines) && javaFrame.MatchString(lines[i+1]) {
				excType = m[1]
				if len(m) > 2 {
					excValue = m[2]
				}
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
		if m := javaFrame.FindStringSubmatch(line); m != nil {
			f := envelope.Frame{
				Function: m[1],
				Filename: m[2],
			}
			if len(m) > 3 && m[3] != "" {
				if n, err := strconv.Atoi(m[3]); err == nil {
					f.Lineno = n
				}
			}
			frames = append(frames, f)
			continue
		}
		// Allow "... N more" filler and "Caused by:" continuation; stop on
		// anything else.
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "...") || strings.HasPrefix(t, "Caused by:") || strings.HasPrefix(t, "Suppressed:") {
			continue
		}
		if t == "" {
			continue
		}
		break
	}

	env := envelope.New(projectID, "java")
	env.Level = "error"
	env.Message = excType
	if excValue != "" {
		env.Message = excType + ": " + excValue
	}
	env.Exception = &envelope.Exception{
		Type:       excType,
		Value:      excValue,
		Stacktrace: &envelope.Stacktrace{Frames: frames},
	}
	return env
}
