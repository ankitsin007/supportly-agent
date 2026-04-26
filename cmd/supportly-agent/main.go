// Command supportly-agent ships exception events from log files and
// container streams to Supportly.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/ankitsin007/supportly-agent/internal/config"
	"github.com/ankitsin007/supportly-agent/internal/parser"
	"github.com/ankitsin007/supportly-agent/internal/ratelimit"
	"github.com/ankitsin007/supportly-agent/internal/redact"
	"github.com/ankitsin007/supportly-agent/internal/sink"
	"github.com/ankitsin007/supportly-agent/internal/source"
	"github.com/ankitsin007/supportly-agent/internal/source/docker"
	"github.com/ankitsin007/supportly-agent/internal/source/file"
)

var (
	configPath = flag.String("config", "", "Path to YAML config (overrides /etc/supportly/agent.yaml)")
	logLevel   = flag.String("log-level", "info", "Log level: debug, info, warn, error")
)

const version = "0.2.0"

func main() {
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

	httpSink := sink.New(cfg.APIEndpoint, cfg.APIKey)

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
			slog.Warn("ingest send failed", "err", err)
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
// per path; for unknown sources we just use the source name.
func streamKey(raw source.RawLog) string {
	if v := raw.Tags["container_name"]; v != "" {
		return "docker:" + v
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
