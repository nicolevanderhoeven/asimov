package main

import (
	"context"
	"flag"
	"fmt"
	"io"
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
	resume := flag.Bool("resume", false, "Resume the saved game")
	save := flag.String("save", ".enterprise-save.json", "Local save file")
	env := flag.String("env", "../.env", "Environment file; existing shell variables take precedence")
	serve := flag.Bool("serve", false, "Run an HTTP API server (one isolated session per client) instead of the REPL")
	addr := flag.String("addr", ":8080", "HTTP listen address; only used with --serve")
	sessionTTL := flag.Duration("session-ttl", 30*time.Minute, "Idle session expiry; only used with --serve")
	flag.Parse()
	if *serve && *resume {
		return fmt.Errorf("--resume is not supported with --serve; sessions are created per-request")
	}
	if err := godotenv.Load(*env); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read environment file: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var client *agento11y.Client
	cfg := telemetry.FromEnv()
	if !*offline && os.Getenv("ANTHROPIC_API_KEY") == "" {
		return fmt.Errorf("ANTHROPIC_API_KEY is required (or use --offline)")
	}
	if !*offline && !*noTelemetry {
		r, err := telemetry.Init(ctx, cfg)
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
		fmt.Println("Grafana export configured: generations, traces, metrics, and logs.")
	} else {
		fmt.Println("Grafana telemetry is disabled for this run.")
	}
	g := gm.GM{Client: client, Logger: logger, Roll: game.RandomRoll}
	if !*offline {
		model := os.Getenv("ANTHROPIC_MODEL")
		if model == "" {
			model = "claude-sonnet-4-6"
		}
		g.Model = gm.Wrap(anthropic.New(os.Getenv("ANTHROPIC_API_KEY"), model), client, cfg.Version)
	}
	if *serve {
		return runServe(ctx, &g, *addr, *sessionTTL, logger)
	}
	return runREPL(ctx, &g, *resume, *save, *offline, logger)
}
