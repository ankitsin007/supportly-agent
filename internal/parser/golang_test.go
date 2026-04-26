package parser

import "testing"

func TestGo_RuntimePanic(t *testing.T) {
	trace := `panic: runtime error: index out of range [5] with length 3

goroutine 1 [running]:
main.processOrders(0xc00009a000)
	/app/main.go:42 +0x123
main.main()
	/app/main.go:18 +0x52
exit status 2`

	env := Go{}.Parse(rawLine(trace), "p")
	if env == nil {
		t.Fatal("nil")
	}
	if env.Platform != "go" {
		t.Errorf("platform = %q", env.Platform)
	}
	if env.Exception.Type != "PanicError" {
		t.Errorf("type = %q", env.Exception.Type)
	}
	if env.Exception.Value != "runtime error: index out of range [5] with length 3" {
		t.Errorf("value = %q", env.Exception.Value)
	}
	frames := env.Exception.Stacktrace.Frames
	if len(frames) != 2 {
		t.Fatalf("frames = %d (want 2)", len(frames))
	}
	if frames[0].Function != "main.processOrders" || frames[0].Filename != "/app/main.go" || frames[0].Lineno != 42 {
		t.Errorf("frame0 = %+v", frames[0])
	}
}

func TestGo_FatalError(t *testing.T) {
	trace := `fatal error: concurrent map writes

goroutine 5 [running]:
main.handler()
	/app/handler.go:99 +0x1a3`

	env := Go{}.Parse(rawLine(trace), "p")
	if env == nil {
		t.Fatal("nil")
	}
	if env.Exception.Type != "FatalError" {
		t.Errorf("type = %q", env.Exception.Type)
	}
}
