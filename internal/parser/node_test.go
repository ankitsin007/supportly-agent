package parser

import "testing"

func TestNode_TypeError(t *testing.T) {
	trace := `TypeError: Cannot read property 'name' of undefined
    at Object.<anonymous> (/app/server.js:42:18)
    at Module._compile (internal/modules/cjs/loader.js:1063:30)
    at /app/lib/foo.js:10:5`

	env := Node{}.Parse(rawLine(trace), "p")
	if env == nil {
		t.Fatal("nil")
	}
	if env.Platform != "node" {
		t.Errorf("platform = %q", env.Platform)
	}
	if env.Exception.Type != "TypeError" {
		t.Errorf("type = %q", env.Exception.Type)
	}
	if env.Exception.Value != "Cannot read property 'name' of undefined" {
		t.Errorf("value = %q", env.Exception.Value)
	}
	if len(env.Exception.Stacktrace.Frames) != 3 {
		t.Fatalf("frames = %d", len(env.Exception.Stacktrace.Frames))
	}
	f := env.Exception.Stacktrace.Frames[0]
	if f.Function != "Object.<anonymous>" || f.Filename != "/app/server.js" || f.Lineno != 42 {
		t.Errorf("frame0 = %+v", f)
	}
	// Anonymous frame (no fn name).
	f2 := env.Exception.Stacktrace.Frames[2]
	if f2.Function != "<anonymous>" {
		t.Errorf("frame2 function = %q", f2.Function)
	}
}

func TestNode_RejectsBareErrorLine(t *testing.T) {
	// "Error: foo" alone (no stack frame after) shouldn't match — too prone
	// to false positives on any random log line that mentions an Error type.
	if env := (Node{}).Parse(rawLine("Error: something went wrong"), "p"); env != nil {
		t.Errorf("expected nil, got %+v", env)
	}
}
