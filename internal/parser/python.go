// Package parser — Python traceback parser.
//
// Recognizes the canonical CPython traceback shape:
//
//	Traceback (most recent call last):
//	  File "/app/views.py", line 47, in create_order
//	    customer = Customer.objects.get(id=customer_id)
//	  File "/usr/local/lib/python3.11/site-packages/django/db/models/manager.py", line 87, in manager_method
//	    return getattr(self.get_queryset(), name)(*args, **kwargs)
//	django.core.exceptions.ObjectDoesNotExist: Customer matching query does not exist.
//
// Caller is responsible for grouping multi-line input via Recombiner with
// PythonContinuation. This parser receives the full block as raw.Line.
package parser

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/ankitsin007/supportly-agent/internal/envelope"
	"github.com/ankitsin007/supportly-agent/internal/source"
)

var (
	// pythonStartLine is the marker that says "next lines are a traceback."
	pythonStartLine = "Traceback (most recent call last):"

	// pythonFrameRegex matches: '  File "/path", line 42, in funcname'
	pythonFrameRegex = regexp.MustCompile(`^\s*File "([^"]+)", line (\d+), in (.+)$`)

	// pythonExceptionRegex matches the LAST line: 'ExcType: message' or
	// 'pkg.module.ExcType: message'. Strategy: any dotted Python identifier
	// followed by ": ". Modules are typically lowercase, the class capitalized,
	// e.g. 'django.core.exceptions.ObjectDoesNotExist'. The "Traceback..."
	// marker already gates parsing, so we only run this after seeing it —
	// false positives on plain log lines are bounded.
	pythonExceptionRegex = regexp.MustCompile(`^([A-Za-z_][\w]*(?:\.[A-Za-z_]\w*)*):\s*(.*)$`)
)

// Python recognizes Python tracebacks.
type Python struct{}

// Name implements Parser.
func (Python) Name() string { return "python" }

// Parse implements Parser.
func (Python) Parse(raw source.RawLog, projectID string) *envelope.Envelope {
	lines := strings.Split(raw.Line, "\n")
	startIdx := -1
	for i, ln := range lines {
		if strings.Contains(ln, pythonStartLine) {
			startIdx = i
			break
		}
	}
	if startIdx == -1 {
		return nil
	}

	frames := make([]envelope.Frame, 0, 8)
	excType, excValue := "", ""

	// Python traceback frames come in pairs: a "File ..., line ..., in ..."
	// line followed by an indented context line.
	for i := startIdx + 1; i < len(lines); i++ {
		line := lines[i]
		if m := pythonFrameRegex.FindStringSubmatch(line); m != nil {
			lineno, _ := strconv.Atoi(m[2])
			f := envelope.Frame{
				Filename: m[1],
				Function: m[3],
				Lineno:   lineno,
			}
			// Next line, if it exists and is more-indented, is the source line.
			if i+1 < len(lines) {
				next := lines[i+1]
				if strings.HasPrefix(next, "    ") &&
					!pythonFrameRegex.MatchString(next) {
					f.ContextLine = strings.TrimSpace(next)
					i++ // consume the source line
				}
			}
			frames = append(frames, f)
			continue
		}
		// Not a frame — try the final exception line.
		if m := pythonExceptionRegex.FindStringSubmatch(line); m != nil {
			excType = m[1]
			excValue = m[2]
			break
		}
	}

	if excType == "" && len(frames) == 0 {
		// Marker matched but couldn't parse anything useful.
		// Fall through; let Fallback layer ship it as a message.
		return nil
	}

	env := envelope.New(projectID, "python")
	env.Level = "error"
	env.Message = strings.TrimSpace(lines[0])
	if excType != "" {
		env.Message = excType + ": " + excValue
	}
	env.Exception = &envelope.Exception{
		Type:       excType,
		Value:      excValue,
		Stacktrace: &envelope.Stacktrace{Frames: frames},
	}
	return env
}
