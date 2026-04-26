// Package kubernetes tails container logs from a kubelet's local pod log
// directory. Designed to run as a DaemonSet — one agent pod per node, each
// reading only the logs of pods scheduled on that node.
//
// Strategy:
//   - Watch the pod log root (default /var/log/pods) for new directories
//     appearing as kubelet creates them.
//   - For each pod, watch its container subdirectories for the active
//     log file (kubelet writes 0.log; rotates to 0.log.YYYYMMDD-N).
//   - Tail each active log file via the same fsnotify-backed primitive
//     the file source uses.
//
// Pod metadata enrichment (namespace, deployment, app label) is intentionally
// extracted from the directory name rather than the k8s API in M1 — this
// avoids the agent needing in-cluster credentials and keeps the surface
// small. The directory format kubelet uses is:
//
//	/var/log/pods/<namespace>_<pod-name>_<pod-uid>/<container>/<rotation>.log
//
// The pod-name itself encodes the deployment via standard k8s naming
// (deployment-replicaset-pod), so we extract that too.
//
// Adding a real k8s API client (with watch on Pods + Endpoints) is a
// follow-up — guarded behind a `--k8s-api-enrichment` flag once requested.
package kubernetes

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/ankitsin007/supportly-agent/internal/source"
)

const defaultPodLogRoot = "/var/log/pods"

// Source tails kubelet pod logs.
type Source struct {
	// Root overrides the pod log directory. Tests use this to point at
	// a temp dir laid out like kubelet's.
	Root string

	// ExcludeNamespaces blocks specific namespaces (e.g. "kube-system").
	ExcludeNamespaces map[string]bool

	emitted atomic.Uint64
	dropped atomic.Uint64

	mu        sync.Mutex
	lastErr   string
	lastErrAt time.Time

	tailing map[string]context.CancelFunc // pod-dir → cancel
}

// New returns a Source. Pass nil ExcludeNamespaces to use defaults
// (kube-system + kube-public + kube-node-lease).
func New(excludeNamespaces []string) *Source {
	excl := map[string]bool{
		"kube-system":     true,
		"kube-public":     true,
		"kube-node-lease": true,
	}
	for _, n := range excludeNamespaces {
		excl[n] = true
	}
	return &Source{
		Root:              defaultPodLogRoot,
		ExcludeNamespaces: excl,
		tailing:           make(map[string]context.CancelFunc),
	}
}

// Name implements source.Source.
func (s *Source) Name() string { return "kubernetes:" + s.Root }

// Health implements source.Source.
func (s *Source) Health() source.Health {
	s.mu.Lock()
	defer s.mu.Unlock()
	return source.Health{
		Healthy:      s.lastErr == "",
		LinesEmitted: s.emitted.Load(),
		LinesDropped: s.dropped.Load(),
		LastError:    s.lastErr,
		LastErrorAt:  s.lastErrAt,
	}
}

// Stop is a no-op; ctx cancellation drives shutdown.
func (s *Source) Stop() error { return nil }

// Start scans existing pod directories and watches for new ones.
func (s *Source) Start(ctx context.Context, out chan<- source.RawLog) error {
	if _, err := os.Stat(s.Root); err != nil {
		s.recordErr(err)
		return fmt.Errorf("pod log root %s not accessible: %w", s.Root, err)
	}

	// Tail any pods that already exist.
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		s.recordErr(err)
		return fmt.Errorf("read %s: %w", s.Root, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			s.maybeFollowPod(ctx, filepath.Join(s.Root, e.Name()), out)
		}
	}

	// Watch for new pod directories appearing.
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("fsnotify: %w", err)
	}
	defer watcher.Close()
	if err := watcher.Add(s.Root); err != nil {
		return fmt.Errorf("watch root: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-watcher.Events:
			if !ok {
				return fmt.Errorf("watcher closed")
			}
			if ev.Op&fsnotify.Create == fsnotify.Create {
				if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
					s.maybeFollowPod(ctx, ev.Name, out)
				}
			}
		case err := <-watcher.Errors:
			s.recordErr(err)
		}
	}
}

// PodMeta is what we extract from the kubelet directory name.
type PodMeta struct {
	Namespace  string
	PodName    string
	PodUID     string
	Deployment string // best-effort from pod-name shape
}

// parsePodDir splits "<ns>_<pod>_<uid>" into its parts.
// Returns ok=false if the format doesn't match.
func parsePodDir(dirname string) (PodMeta, bool) {
	parts := strings.Split(dirname, "_")
	if len(parts) < 3 {
		return PodMeta{}, false
	}
	ns := parts[0]
	uid := parts[len(parts)-1]
	pod := strings.Join(parts[1:len(parts)-1], "_")
	meta := PodMeta{Namespace: ns, PodName: pod, PodUID: uid}
	meta.Deployment = deploymentFromPodName(pod)
	return meta, true
}

// deploymentFromPodName strips the trailing -<replicaset-hash>-<pod-hash>
// from k8s deployment pod names. e.g. "checkout-svc-7d9f4b8c-xq2k4" → "checkout-svc".
// Returns the full pod name unchanged if it doesn't look like a deployment pod.
func deploymentFromPodName(pod string) string {
	parts := strings.Split(pod, "-")
	if len(parts) < 3 {
		return pod
	}
	last := parts[len(parts)-1]
	second := parts[len(parts)-2]
	// Last two segments are the pod hash + replicaset hash.
	if isLikelyHash(last) && isLikelyHash(second) {
		return strings.Join(parts[:len(parts)-2], "-")
	}
	return pod
}

func isLikelyHash(s string) bool {
	if len(s) < 4 || len(s) > 10 {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// maybeFollowPod starts goroutines tailing each container in the pod.
func (s *Source) maybeFollowPod(ctx context.Context, podPath string, out chan<- source.RawLog) {
	dirname := filepath.Base(podPath)
	meta, ok := parsePodDir(dirname)
	if !ok {
		slog.Debug("k8s: skipping unrecognized pod dir", "dir", dirname)
		return
	}
	if s.ExcludeNamespaces[meta.Namespace] {
		return
	}

	s.mu.Lock()
	if _, exists := s.tailing[podPath]; exists {
		s.mu.Unlock()
		return
	}
	tailCtx, cancel := context.WithCancel(ctx)
	s.tailing[podPath] = cancel
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.tailing, podPath)
			s.mu.Unlock()
		}()
		if err := s.tailPod(tailCtx, podPath, meta, out); err != nil && tailCtx.Err() == nil {
			slog.Warn("k8s pod tail failed", "pod", meta.PodName, "err", err)
			s.recordErr(err)
		}
	}()
}

// tailPod watches for container subdirs and tails the latest 0.log in each.
func (s *Source) tailPod(ctx context.Context, podPath string, meta PodMeta, out chan<- source.RawLog) error {
	containers, err := os.ReadDir(podPath)
	if err != nil {
		return err
	}
	for _, c := range containers {
		if c.IsDir() {
			go s.tailContainer(ctx, filepath.Join(podPath, c.Name()), c.Name(), meta, out)
		}
	}
	<-ctx.Done()
	return nil
}

// tailContainer follows the active 0.log file in a container's log dir.
func (s *Source) tailContainer(ctx context.Context, containerPath, containerName string, meta PodMeta, out chan<- source.RawLog) {
	logFile := filepath.Join(containerPath, "0.log")
	for ctx.Err() == nil {
		if err := s.tailFile(ctx, logFile, containerName, meta, out); err != nil {
			slog.Debug("k8s tail iteration ended", "file", logFile, "err", err)
		}
		// Brief backoff before reopening (rotation, restart).
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return
		}
	}
}

func (s *Source) tailFile(ctx context.Context, path, container string, meta PodMeta, out chan<- source.RawLog) error {
	fh, err := os.Open(path)
	if err != nil {
		return err
	}
	defer fh.Close()

	// Seek to end so we only ship NEW lines after agent start.
	if _, err := fh.Seek(0, io.SeekEnd); err != nil {
		return err
	}

	reader := bufio.NewReader(fh)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			for {
				line, err := reader.ReadString('\n')
				if len(line) > 0 {
					rl := source.RawLog{
						Source:    "kubernetes",
						Timestamp: time.Now().UTC(),
						Line:      line,
						Tags: map[string]string{
							"k8s_namespace":  meta.Namespace,
							"k8s_pod":        meta.PodName,
							"k8s_pod_uid":    meta.PodUID,
							"k8s_deployment": meta.Deployment,
							"container_name": container,
						},
					}
					select {
					case out <- rl:
						s.emitted.Add(1)
					case <-ctx.Done():
						return nil
					default:
						s.dropped.Add(1)
					}
				}
				if err == io.EOF {
					break
				}
				if err != nil {
					return err
				}
			}
		}
	}
}

func (s *Source) recordErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastErr = err.Error()
	s.lastErrAt = time.Now().UTC()
}
