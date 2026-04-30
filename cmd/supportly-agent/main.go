// Command supportly-agent ships exception events from log files and
// container streams to Supportly.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/ankitsin007/supportly-agent/internal/adapters"
	"github.com/ankitsin007/supportly-agent/internal/buffer"
	"github.com/ankitsin007/supportly-agent/internal/config"
	"github.com/ankitsin007/supportly-agent/internal/envelope"
	"github.com/ankitsin007/supportly-agent/internal/healthz"
	"github.com/ankitsin007/supportly-agent/internal/parser"
	"github.com/ankitsin007/supportly-agent/internal/ratelimit"
	"github.com/ankitsin007/supportly-agent/internal/redact"
	"github.com/ankitsin007/supportly-agent/internal/sink"
	"github.com/ankitsin007/supportly-agent/internal/source"
	"github.com/ankitsin007/supportly-agent/internal/source/docker"
	"github.com/ankitsin007/supportly-agent/internal/source/ebpf"
	"github.com/ankitsin007/supportly-agent/internal/source/file"
	"github.com/ankitsin007/supportly-agent/internal/source/journald"
	"github.com/ankitsin007/supportly-agent/internal/source/kubernetes"
	"github.com/ankitsin007/supportly-agent/internal/source/otel"
	"github.com/ankitsin007/supportly-agent/internal/tlsconfig"
)

var (
	configPath = flag.String("config", "", "Path to YAML config (overrides /etc/supportly/agent.yaml)")
	logLevel   = flag.String("log-level", "info", "Log level: debug, info, warn, error")
)

const version = "1.2.0"

func main() {
	// Subcommand dispatch: `supportly-agent adapters [lang]` short-
	// circuits the daemon path and prints the embedded snippet to
	// stdout. Done before flag.Parse so the existing daemon flag set
	// doesn't reject `adapters` as an unknown flag.
	if len(os.Args) >= 2 && os.Args[1] == "adapters" {
		os.Exit(runAdaptersCmd(os.Args[2:]))
	}

	flag.Parse()
	setupLogging(*logLevel)
	slog.Info("supportly-agent starting", "version", version)

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("config load failed", "err", err)
		os.Exit(1)
	}
	slog.Info("config loaded",
		"project_id", cfg.ProjectID,
		"endpoint", cfg.APIEndpoint,
		"sources", len(cfg.Sources),
		"redaction", cfg.Redaction.Enabled,
		"per_source_eps", cfg.RateLimits.PerSourceEPS,
	)

	// Parser pipeline. Order matters: highest-confidence first, fallback last.
	pipeline := &parser.Layered{
		Parsers: []parser.Parser{
			parser.JSON{},
			parser.Python{},
			parser.Java{},
			parser.Go{},
			parser.Node{},
			parser.Ruby{},
			parser.Fallback{},
		},
	}

	tlsCfg, err := tlsconfig.Build(tlsconfig.Options{
		CABundlePath:   cfg.TLS.CABundlePath,
		CertPin:        cfg.TLS.CertPin,
		ClientCertFile: cfg.TLS.ClientCertFile,
		ClientKeyFile:  cfg.TLS.ClientKeyFile,
		SkipVerify:     cfg.TLS.SkipVerify,
		Acknowledged:   cfg.TLS.Acknowledged,
	})
	if err != nil {
		slog.Error("tls config invalid", "err", err)
		os.Exit(1)
	}
	if cfg.TLS.SkipVerify {
		slog.Warn("TLS verification DISABLED — agent is operating in INSECURE mode",
			"reason", "--tls-skip-verify is set")
	}

	httpSink := sink.NewWithTLS(cfg.APIEndpoint, cfg.APIKey, tlsCfg)

	// On-disk buffer for offline survival. nil = disabled.
	var diskBuf *buffer.Buffer
	if cfg.Buffer.Enabled {
		maxBytes := int64(cfg.Buffer.MaxDiskMB) * 1024 * 1024
		var bufErr error
		diskBuf, bufErr = buffer.New(cfg.Buffer.Path, maxBytes)
		if bufErr != nil {
			slog.Warn("disk buffer disabled — could not open path",
				"path", cfg.Buffer.Path, "err", bufErr)
			diskBuf = nil
		} else {
			slog.Info("disk buffer ready", "path", cfg.Buffer.Path,
				"max_mb", cfg.Buffer.MaxDiskMB, "queued", diskBuf.Len())
		}
	}

	// PII redactor — wraps the sink so envelopes are scrubbed at the boundary.
	var redactor *redact.Redactor
	if cfg.Redaction.Enabled {
		redactor = redact.New(cfg.Redaction.Patterns, cfg.Redaction.Custom)
	}

	// Rate limiter — global for now; per-source granularity is a follow-up.
	limiter := ratelimit.New(cfg.RateLimits.PerSourceEPS, cfg.RateLimits.Burst)

	// Construct sources.
	var sources []source.Source
	for _, sc := range cfg.Sources {
		if !sc.Enabled {
			continue
		}
		switch sc.Type {
		case "file":
			for _, p := range sc.Paths {
				sources = append(sources, file.New(p))
			}
		case "docker":
			if sc.Socket != "" {
				sources = append(sources, docker.NewWithSocket(sc.Socket, sc.ExcludeContainers))
			} else {
				sources = append(sources, docker.New(sc.ExcludeContainers))
			}
		case "journald":
			sources = append(sources, journald.New(sc.Units))
		case "kubernetes":
			ks := kubernetes.New(sc.ExcludeNamespaces)
			if sc.PodLogRoot != "" {
				ks.Root = sc.PodLogRoot
			}
			sources = append(sources, ks)
		case "otel":
			// M5 Week 1: OTel OTLP/HTTP receiver. Customer apps with
			// existing OpenTelemetry instrumentation can point their
			// log exporter at the agent without any other changes.
			sources = append(sources, otel.New(sc.OTLPAddr))
		case "ebpf":
			// M5 Week 3: eBPF uprobe receiver. Linux-only — the source's
			// Start() returns ErrUnsupported on macOS/Windows; the
			// consumer goroutine logs that as a warning and the rest
			// of the agent keeps running.
			sources = append(sources, ebpf.New(ebpf.Config{
				Targets:   sc.EBPFTargets,
				Languages: sc.EBPFLanguages,
			}))
		default:
			slog.Warn("unknown source type — skipping", "type", sc.Type)
		}
	}
	if len(sources) == 0 {
		slog.Error("no sources configured — exiting")
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	rawCh := make(chan source.RawLog, 1024)
	var wg sync.WaitGroup

	// Start the local /healthz server. Bind failure is logged but
	// not fatal — the agent itself can still ship events even if the
	// healthz port is taken.
	hzSrv := healthz.New(version, sources)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := hzSrv.Start(ctx); err != nil {
			slog.Warn("healthz server failed to start", "err", err)
		}
	}()

	for _, s := range sources {
		s := s
		wg.Add(1)
		go func() {
			defer wg.Done()
			slog.Info("source starting", "name", s.Name())
			if err := s.Start(ctx, rawCh); err != nil && err != context.Canceled {
				slog.Error("source exited with error", "name", s.Name(), "err", err)
			}
		}()
	}

	// Per-stream recombiners: tracebacks span many lines, so we buffer
	// continuation lines until a non-continuation arrives. Keyed by
	// (source, sub-stream) where sub-stream comes from the source's tags
	// (container_name for docker, file_path for file).
	recombiners := make(map[string]*parser.Recombiner)
	var rcMu sync.Mutex
	getRecombiner := func(key string) *parser.Recombiner {
		rcMu.Lock()
		defer rcMu.Unlock()
		if r, ok := recombiners[key]; ok {
			return r
		}
		r := &parser.Recombiner{
			IsContinuation: parser.UniversalContinuation,
			FlushAfter:     500 * time.Millisecond,
		}
		recombiners[key] = r
		return r
	}

	process := func(raw source.RawLog) {
		if redactor != nil {
			raw.Line = redactor.Redact(raw.Line)
		}
		env := pipeline.Parse(raw, cfg.ProjectID)
		if env == nil {
			return
		}
		if !limiter.Allow() {
			slog.Debug("rate-limited — dropping envelope")
			return
		}
		if err := httpSink.Send(ctx, env); err != nil {
			if diskBuf != nil {
				if seq, bufErr := diskBuf.Enqueue(env); bufErr != nil {
					slog.Warn("ingest send failed AND disk buffer rejected envelope",
						"send_err", err, "buf_err", bufErr)
				} else {
					slog.Debug("buffered envelope for retry", "seq", seq, "send_err", err)
				}
			} else {
				slog.Warn("ingest send failed (buffer disabled, dropping)", "err", err)
			}
		} else {
			slog.Debug("envelope shipped",
				"event_id", env.EventID,
				"level", env.Level,
				"parser", env.Tags["parser"],
			)
		}
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		flushTicker := time.NewTicker(250 * time.Millisecond)
		defer flushTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				return

			case <-flushTicker.C:
				// Periodic flush: emit any buffered groups whose last line
				// was received more than FlushAfter ago.
				rcMu.Lock()
				for k, r := range recombiners {
					if combined := r.Flush(source.RawLog{Source: k}); combined != nil {
						go process(*combined)
					}
				}
				rcMu.Unlock()

			case raw, ok := <-rawCh:
				if !ok {
					return
				}
				key := streamKey(raw)
				r := getRecombiner(key)
				if combined := r.Feed(raw); combined != nil {
					process(*combined)
				}
			}
		}
	}()

	// Replay goroutine — periodically tries to ship buffered envelopes.
	// Stops on first error (most likely the network is still down) and
	// waits for the next tick.
	if diskBuf != nil {
		interval := time.Duration(cfg.Buffer.ReplayIntervalSeconds) * time.Second
		if interval <= 0 {
			interval = 30 * time.Second
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if diskBuf.Len() == 0 {
						continue
					}
					n, err := diskBuf.Drain(func(env *envelope.Envelope) error {
						return httpSink.Send(ctx, env)
					})
					if n > 0 {
						slog.Info("replayed buffered envelopes", "shipped", n,
							"remaining", diskBuf.Len())
					}
					if err != nil {
						slog.Debug("replay paused on error",
							"err", err, "remaining", diskBuf.Len())
					}
				}
			}
		}()
	}

	<-ctx.Done()
	slog.Info("shutdown signal received — draining")
	for _, s := range sources {
		_ = s.Stop()
	}
	wg.Wait()

	allowed, dropped := limiter.Stats()
	slog.Info("supportly-agent stopped cleanly",
		"events_shipped", allowed,
		"events_rate_limited", dropped,
	)
}

// streamKey derives a stable key per logical log stream so each stream gets
// its own recombiner. For Docker we want one per container, for files one
// per path, for journald per systemd unit, for k8s per (pod, container).
func streamKey(raw source.RawLog) string {
	if pod := raw.Tags["k8s_pod"]; pod != "" {
		return "k8s:" + pod + "/" + raw.Tags["container_name"]
	}
	if v := raw.Tags["container_name"]; v != "" {
		return "docker:" + v
	}
	if v := raw.Tags["systemd_unit"]; v != "" {
		return "journald:" + v
	}
	if v := raw.Tags["file_path"]; v != "" {
		return "file:" + v
	}
	return raw.Source
}

func setupLogging(level string) {
	var l slog.Level
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l})
	slog.SetDefault(slog.New(h))
}

// runAdaptersCmd handles `supportly-agent adapters ...`. Returns the
// process exit code so main can call os.Exit on it.
func runAdaptersCmd(args []string) int {
	if len(args) == 0 || args[0] == "list" {
		fmt.Println("Available auto-instrument adapters:")
		for _, lang := range adapters.List() {
			fmt.Printf("  %s\n", lang)
		}
		fmt.Println("\nUsage: supportly-agent adapters <language>")
		return 0
	}
	if args[0] == "-h" || args[0] == "--help" {
		fmt.Println("Usage: supportly-agent adapters [list | <language>]")
		fmt.Println("Languages:", adapters.List())
		return 0
	}
	snippet, ok := adapters.Get(args[0])
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown adapter: %s\n", args[0])
		fmt.Fprintf(os.Stderr, "Available: %v\n", adapters.List())
		return 2
	}
	fmt.Print(snippet)
	return 0
}
