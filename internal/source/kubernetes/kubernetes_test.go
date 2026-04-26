package kubernetes

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ankitsin007/supportly-agent/internal/source"
)

func TestParsePodDir(t *testing.T) {
	cases := []struct {
		dir      string
		wantNS   string
		wantPod  string
		wantUID  string
		wantDepl string
		ok       bool
	}{
		{
			"default_checkout-svc-7d9f4b8c-xq2k4_abcd-1234",
			"default", "checkout-svc-7d9f4b8c-xq2k4", "abcd-1234", "checkout-svc",
			true,
		},
		{
			// Pod with underscores in the name (legal): everything between
			// first and last underscore is the pod-name.
			"my-ns_some_pod_name_uid123",
			"my-ns", "some_pod_name", "uid123", "some_pod_name",
			true,
		},
		{
			// Statefulset pods: name like "redis-0" — no hash suffix.
			"db_redis-0_uid456",
			"db", "redis-0", "uid456", "redis-0",
			true,
		},
		{
			"malformed",
			"", "", "", "", false,
		},
	}
	for _, c := range cases {
		got, ok := parsePodDir(c.dir)
		if ok != c.ok {
			t.Errorf("parsePodDir(%q) ok = %v, want %v", c.dir, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if got.Namespace != c.wantNS || got.PodName != c.wantPod || got.PodUID != c.wantUID {
			t.Errorf("parsePodDir(%q) = %+v", c.dir, got)
		}
		if got.Deployment != c.wantDepl {
			t.Errorf("deployment for %q = %q, want %q", c.dir, got.Deployment, c.wantDepl)
		}
	}
}

func TestSource_TailsExistingPod(t *testing.T) {
	root := t.TempDir()
	podDir := filepath.Join(root, "default_checkout-svc-7d9f4b8c-xq2k4_uid001")
	containerDir := filepath.Join(podDir, "checkout")
	if err := os.MkdirAll(containerDir, 0755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(containerDir, "0.log")
	fh, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	fh.Close()

	s := New(nil)
	s.Root = root
	out := make(chan source.RawLog, 10)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = s.Start(ctx, out)
	}()

	// Wait for the tail to seek to end.
	time.Sleep(700 * time.Millisecond)
	if err := os.WriteFile(logPath, []byte("ERROR something blew up\n"), 0644); err != nil {
		t.Fatal(err)
	}

	select {
	case rl := <-out:
		if rl.Tags["k8s_namespace"] != "default" {
			t.Errorf("namespace = %q", rl.Tags["k8s_namespace"])
		}
		if rl.Tags["k8s_deployment"] != "checkout-svc" {
			t.Errorf("deployment = %q", rl.Tags["k8s_deployment"])
		}
		if rl.Tags["container_name"] != "checkout" {
			t.Errorf("container = %q", rl.Tags["container_name"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no log line received")
	}

	cancel()
	wg.Wait()
}

func TestSource_SkipsExcludedNamespace(t *testing.T) {
	root := t.TempDir()
	podDir := filepath.Join(root, "kube-system_etcd-master_uid999")
	if err := os.MkdirAll(filepath.Join(podDir, "etcd"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(podDir, "etcd", "0.log"), []byte("noise\n"), 0644); err != nil {
		t.Fatal(err)
	}

	s := New(nil) // defaults exclude kube-system
	s.Root = root
	out := make(chan source.RawLog, 5)
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	go func() { _ = s.Start(ctx, out) }()

	select {
	case rl := <-out:
		t.Errorf("expected no events from kube-system, got %+v", rl)
	case <-time.After(500 * time.Millisecond):
		// good, nothing should have been emitted
	}
}

func TestIsLikelyHash(t *testing.T) {
	cases := map[string]bool{
		"7d9f4b8c": true,
		"xq2k4":    true,
		"abc":      false, // too short
		"redis-0":  false, // contains hyphen
		"":         false,
		"REDIS":    false, // uppercase
	}
	for s, want := range cases {
		if got := isLikelyHash(s); got != want {
			t.Errorf("isLikelyHash(%q) = %v, want %v", s, got, want)
		}
	}
}
