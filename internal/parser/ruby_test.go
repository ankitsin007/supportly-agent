package parser

import "testing"

func TestRuby_StandardError(t *testing.T) {
	trace := "/app/lib/foo.rb:42:in `bar': could not connect (ConnectionError)\n" +
		"\tfrom /app/lib/baz.rb:18:in `init'\n" +
		"\tfrom /app/main.rb:5:in `<main>'"

	env := Ruby{}.Parse(rawLine(trace), "p")
	if env == nil {
		t.Fatal("nil")
	}
	if env.Platform != "ruby" {
		t.Errorf("platform = %q", env.Platform)
	}
	if env.Exception.Type != "ConnectionError" {
		t.Errorf("type = %q", env.Exception.Type)
	}
	if env.Exception.Value != "could not connect" {
		t.Errorf("value = %q", env.Exception.Value)
	}
	frames := env.Exception.Stacktrace.Frames
	if len(frames) != 3 {
		t.Fatalf("frames = %d", len(frames))
	}
	if frames[0].Filename != "/app/lib/foo.rb" || frames[0].Lineno != 42 || frames[0].Function != "bar" {
		t.Errorf("frame0 = %+v", frames[0])
	}
	if frames[2].Function != "<main>" {
		t.Errorf("frame2 function = %q", frames[2].Function)
	}
}

func TestRuby_NamespacedError(t *testing.T) {
	trace := "/app/x.rb:10:in `foo': bad input (ActiveRecord::RecordNotFound)\n" +
		"\tfrom /app/y.rb:5:in `bar'"
	env := Ruby{}.Parse(rawLine(trace), "p")
	if env == nil {
		t.Fatal("nil")
	}
	if env.Exception.Type != "ActiveRecord::RecordNotFound" {
		t.Errorf("type = %q", env.Exception.Type)
	}
}
