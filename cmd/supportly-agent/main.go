// Command supportly-agent ships exception events from log files to Supportly.
//
// Week 1 surface area:
//   - Reads YAML config at --config (default /etc/supportly/agent.yaml)
//   - Tails configured files
//   - Parses lines with the JSON parser, falls back to keyword detection
//   - POSTs error envelopes to Supportly's ingest endpoint
//   - Logs to stderr; SIGINT/SIGTERM triggers graceful shutdown
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/ankitsin007/supportly-agent/internal/config"
	"github.com/ankitsin007/supportly-agent/internal/parser"
	"github.com/ankitsin007/supportly-agent/internal/sink"
	"github.com/ankitsin007/supportly-agent/internal/source"
	"github.com/ankitsin007/supportly-agent/internal/source/file"
)

var (
	configPath = flag.String("config", "", "Path to YAML config (overrides /etc/supportly/agent.yaml)")
	logLevel   = flag.String("log-level", "info", "Log level: debug, info, warn, error")
)

const version = "0.1.0"

func main() {
	flag.Parse()
	setupLogging(*logLevel)
	slog.Info("supportly-agent starting", "version", version)

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("config load failed", "err", err)
		os.Exit(1)
	}
	slog.Info("config loaded", "project_id", cfg.ProjectID, "endpoint", cfg.APIEndpoint, "sources", len(cfg.Sources))

	// Build the parser pipeline. JSON first (high confidence), fallback last.
	pipeline := &parser.Layered{
		Parsers: []parser.Parser{
			parser.JSON{},
			parser.Fallback{},
		},
	}

	// Build the sink.
	httpSink := sink.New(cfg.APIEndpoint, cfg.APIKey)

	// Construct sources from config.
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
		default:
			slog.Warn("unknown source type — skipping", "type", sc.Type)
		}
	}
	if len(sources) == 0 {
		slog.Error("no sources configured — exiting")
		os.Exit(1)
	}

	// Wire signal handling for graceful shutdown.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	rawCh := make(chan source.RawLog, 1024)
	var wg sync.WaitGroup

	// Start each source in its own goroutine.
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

	// Consumer goroutine: parse → send.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case raw, ok := <-rawCh:
				if !ok {
					return
				}
				env := pipeline.Parse(raw, cfg.ProjectID)
				if env == nil {
					continue
				}
				if err := httpSink.Send(ctx, env); err != nil {
					slog.Warn("ingest send failed", "err", err)
				} else {
					slog.Debug("envelope shipped", "event_id", env.EventID, "level", env.Level)
				}
			}
		}
	}()

	// Block until ctx done, then wait for goroutines to drain.
	<-ctx.Done()
	slog.Info("shutdown signal received — draining")
	for _, s := range sources {
		_ = s.Stop()
	}
	wg.Wait()
	slog.Info("supportly-agent stopped cleanly")
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
