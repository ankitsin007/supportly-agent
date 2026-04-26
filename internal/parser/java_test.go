package parser

import "testing"

func TestJava_NPE(t *testing.T) {
	trace := `java.lang.NullPointerException: Cannot invoke "String.length()" because "s" is null
	at com.example.Foo.bar(Foo.java:42)
	at com.example.Foo.main(Foo.java:18)`

	env := Java{}.Parse(rawLine(trace), "p")
	if env == nil {
		t.Fatal("nil")
	}
	if env.Platform != "java" {
		t.Errorf("platform = %q", env.Platform)
	}
	if env.Exception.Type != "java.lang.NullPointerException" {
		t.Errorf("type = %q", env.Exception.Type)
	}
	if len(env.Exception.Stacktrace.Frames) != 2 {
		t.Fatalf("frames = %d", len(env.Exception.Stacktrace.Frames))
	}
	f := env.Exception.Stacktrace.Frames[0]
	if f.Function != "com.example.Foo.bar" || f.Filename != "Foo.java" || f.Lineno != 42 {
		t.Errorf("frame0 = %+v", f)
	}
}

func TestJava_ExceptionInThread(t *testing.T) {
	trace := `Exception in thread "main" java.io.IOException: disk full
	at com.example.Bar.write(Bar.java:99)
	at com.example.Bar.main(Bar.java:18)`

	env := Java{}.Parse(rawLine(trace), "p")
	if env == nil {
		t.Fatal("nil")
	}
	if env.Exception.Type != "java.io.IOException" {
		t.Errorf("type = %q", env.Exception.Type)
	}
}

func TestJava_RejectsBareLine(t *testing.T) {
	if env := (Java{}).Parse(rawLine("java.lang.NPE: foo"), "p"); env != nil {
		t.Errorf("expected nil (no frames present)")
	}
}
