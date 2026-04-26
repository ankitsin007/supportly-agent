package parser

import (
	"testing"
	"time"

	"github.com/ankitsin007/supportly-agent/internal/source"
)

func TestRecombiner_Python(t *testing.T) {
	r := &Recombiner{
		IsContinuation: PythonContinuation,
		FlushAfter:     50 * time.Millisecond,
	}

	feed := func(s string) *source.RawLog {
		return r.Feed(source.RawLog{Source: "test", Line: s})
	}

	if got := feed("Traceback (most recent call last):"); got != nil {
		t.Errorf("expected buffering, got emit %q", got.Line)
	}
	if got := feed(`  File "x.py", line 1, in foo`); got != nil {
		t.Errorf("expected buffering")
	}
	if got := feed(`    raise RuntimeError("boom")`); got != nil {
		t.Errorf("expected buffering")
	}
	if got := feed("RuntimeError: boom"); got != nil {
		t.Errorf("expected buffering (final exc line)")
	}
	// Next plain line ends the group.
	got := feed("INFO next request started")
	if got == nil {
		t.Fatal("expected emission of buffered traceback")
	}
	want := "Traceback (most recent call last):\n  File \"x.py\", line 1, in foo\n    raise RuntimeError(\"boom\")\nRuntimeError: boom"
	if got.Line != want {
		t.Errorf("combined = %q\nwant     = %q", got.Line, want)
	}
}

func TestRecombiner_FlushAfterTimeout(t *testing.T) {
	r := &Recombiner{
		IsContinuation: PythonContinuation,
		FlushAfter:     20 * time.Millisecond,
	}
	r.Feed(source.RawLog{Source: "t", Line: "Traceback (most recent call last):"})
	r.Feed(source.RawLog{Source: "t", Line: `  File "x.py", line 1, in foo`})

	// Too soon — flush should return nil.
	if got := r.Flush(source.RawLog{Source: "t"}); got != nil {
		t.Errorf("expected nil flush before timeout")
	}
	time.Sleep(30 * time.Millisecond)
	if got := r.Flush(source.RawLog{Source: "t"}); got == nil {
		t.Error("expected flush after timeout")
	}
}

func TestJavaContinuation(t *testing.T) {
	cases := []struct {
		next string
		want bool
	}{
		{"\tat com.foo.Bar.method(Bar.java:42)", true},
		{"  at com.foo.Bar.method(Bar.java:42)", true},
		{"\t... 5 more", true},
		{"Caused by: java.io.IOException", true},
		{"INFO regular line", false},
	}
	for _, c := range cases {
		if got := JavaContinuation("", c.next); got != c.want {
			t.Errorf("JavaContinuation(%q) = %v, want %v", c.next, got, c.want)
		}
	}
}

func TestNodeContinuation(t *testing.T) {
	if !NodeContinuation("", "    at Object.foo (file.js:1:1)") {
		t.Error("expected match")
	}
	if NodeContinuation("", "INFO line") {
		t.Error("expected no match")
	}
}
