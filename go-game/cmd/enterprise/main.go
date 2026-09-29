package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"time"

	"github.com/grafana/agento11y/go/agento11y"
	"github.com/grafana/ai-sdk/providers/anthropic"
	"github.com/joho/godotenv"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/game"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/gm"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/telemetry"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func run() error {
	offline := flag.Bool("offline", false, "Play using exact /do commands, without an LLM or network telemetry")
	noTelemetry := flag.Bool("no-telemetry", false, "Explicitly disable Grafana export")
	env := flag.String("env", "../.env", "Environment file; existing shell variables take precedence")
	serve := flag.Bool("serve", false, "Run an HTTP API server (one isolated session per client) instead of the REPL")
	addr := flag.String("addr", ":8080", "HTTP listen address; only used with --serve")
	sessionTTL := flag.Duration("session-ttl", 30*time.Minute, "Idle session expiry; only used with --serve")
	flag.Parse()
	if err := godotenv.Load(*env); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read environment file: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Background telemetry diagnostics, and anything a dependency writes via
	// the standard log package, go through diag so the REPL can keep them
	// off its input prompt.
	diag := telemetry.NewDiagnostics(os.Stderr)
	log.SetOutput(diag)
	var client *agento11y.Client
	cfg := telemetry.FromEnv()
	if !*offline && os.Getenv("ANTHROPIC_API_KEY") == "" {
		return fmt.Errorf("ANTHROPIC_API_KEY is required (or use --offline)")
	}
	if !*offline && !*noTelemetry {
		r, err := telemetry.Init(ctx, cfg, diag)
		if err != nil {
			return err
		}
		client = r.Client
		logger = r.Logger
		defer func() {
			shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := r.Shutdown(shutdown); err != nil {
				fmt.Fprintln(os.Stderr, "Telemetry flush failed:", err)
			}
		}()
	}
	telemetryNote := "Grafana export on: generations, traces, metrics, and logs."
	if client == nil {
		telemetryNote = "Grafana telemetry is off for this run."
	}
	g := gm.GM{Client: client, Logger: logger, Roll: game.RandomRoll}
	if !*offline {
		model := os.Getenv("ANTHROPIC_MODEL")
		if model == "" {
			model = "claude-sonnet-4-6"
		}
		g.Model = gm.Wrap(anthropic.New(os.Getenv("ANTHROPIC_API_KEY"), model), client, cfg.Version, diag)
	}
	if *serve {
		fmt.Println(telemetryNote)
		return runServe(ctx, &g, *addr, *sessionTTL, logger)
	}
	return runREPL(ctx, &g, *offline, logger, diag, telemetryNote)
}
