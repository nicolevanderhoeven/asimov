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
	"strconv"
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
	scenario := flag.String("scenario", "classic", "The adventure: classic, or generated (built from random modules). With --serve, the default for sessions that don't choose. Defaults to $ASIMOV_SCENARIO when set")
	seed := flag.Uint64("seed", 0, "Replay the generated scenario with this seed; 0 picks a random one. Not used with --serve")
	flag.Parse()
	if err := godotenv.Load(*env); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read environment file: %w", err)
	}
	// The flag wins; otherwise the environment, which .env may set.
	if v := os.Getenv("ASIMOV_SCENARIO"); v != "" && !flagSet("scenario") {
		*scenario = v
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
	// The fix for the false ending is opt-in; see gm.GM.EndingGuard.
	if on, _ := strconv.ParseBool(os.Getenv("ASIMOV_ENDING_GUARD")); on && !*offline {
		g.EndingGuard = true
		telemetryNote += " Ending guard on."
	}
	// ASIMOV_GM_FIXES turns on every opt-in fix, the ending guard included.
	if on, _ := strconv.ParseBool(os.Getenv("ASIMOV_GM_FIXES")); on && !*offline {
		g.EndingGuard, g.Fixes = true, true
		telemetryNote += " GM fixes on."
	}
	if !*offline {
		model := os.Getenv("ANTHROPIC_MODEL")
		if model == "" {
			model = "claude-sonnet-4-6"
		}
		g.Model = gm.Wrap(anthropic.New(os.Getenv("ANTHROPIC_API_KEY"), model), client, cfg.Version, diag)
	}
	if *serve {
		if _, err := game.Choose(*scenario, 0); err != nil {
			return err
		}
		fmt.Println(telemetryNote)
		return runServe(ctx, &g, *addr, *sessionTTL, *scenario, logger)
	}
	sc, err := game.Choose(*scenario, *seed)
	if err != nil {
		return err
	}
	return runREPL(ctx, &g, sc, *offline, logger, diag, telemetryNote)
}

// flagSet reports whether the named flag was given on the command line.
func flagSet(name string) bool {
	set := false
	flag.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}
