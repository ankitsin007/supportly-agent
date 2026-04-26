package parser

import (
	"strings"
	"testing"
)

func TestPython_DjangoTraceback(t *testing.T) {
	trace := `Traceback (most recent call last):
  File "/app/views.py", line 47, in create_order
    customer = Customer.objects.get(id=customer_id)
  File "/usr/local/lib/python3.11/site-packages/django/db/models/manager.py", line 87, in manager_method
    return getattr(self.get_queryset(), name)(*args, **kwargs)
django.core.exceptions.ObjectDoesNotExist: Customer matching query does not exist.`

	env := Python{}.Parse(rawLine(trace), "p")
	if env == nil {
		t.Fatal("expected envelope, got nil")
	}
	if env.Platform != "python" {
		t.Errorf("platform = %q", env.Platform)
	}
	if env.Exception == nil {
		t.Fatal("no exception")
	}
	if env.Exception.Type != "django.core.exceptions.ObjectDoesNotExist" {
		t.Errorf("type = %q", env.Exception.Type)
	}
	if !strings.Contains(env.Exception.Value, "Customer matching query does not exist") {
		t.Errorf("value = %q", env.Exception.Value)
	}
	if env.Exception.Stacktrace == nil || len(env.Exception.Stacktrace.Frames) != 2 {
		t.Fatalf("expected 2 frames, got %+v", env.Exception.Stacktrace)
	}
	f0 := env.Exception.Stacktrace.Frames[0]
	if f0.Filename != "/app/views.py" || f0.Lineno != 47 || f0.Function != "create_order" {
		t.Errorf("frame0 = %+v", f0)
	}
	if !strings.Contains(f0.ContextLine, "Customer.objects.get") {
		t.Errorf("context line = %q", f0.ContextLine)
	}
}

func TestPython_SimpleTraceback(t *testing.T) {
	trace := `Traceback (most recent call last):
  File "x.py", line 1, in <module>
    raise RuntimeError("boom")
RuntimeError: boom`

	env := Python{}.Parse(rawLine(trace), "p")
	if env == nil {
		t.Fatal("nil")
	}
	if env.Exception.Type != "RuntimeError" {
		t.Errorf("type = %q", env.Exception.Type)
	}
}

func TestPython_RejectsNonPython(t *testing.T) {
	cases := []string{
		`{"level":"error","message":"x"}`,
		"plain log line",
		"java.lang.NPE: bad",
	}
	for _, c := range cases {
		if env := (Python{}).Parse(rawLine(c), "p"); env != nil {
			t.Errorf("expected nil for %q, got %+v", c, env)
		}
	}
}
