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
	"strings"
	"time"

	"github.com/grafana/agento11y/go/agento11y"
	"github.com/grafana/ai-sdk/providers/anthropic"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/dotenv"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/game"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/gm"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/spend"
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
	noTelemetry := flag.Bool("no-telemetry", false, "Explicitly disable all telemetry export")
	offlineTelemetry := flag.Bool("telemetry", false, "With --offline, still send traces, metrics, and logs to a local OTLP_ENDPOINT (nothing leaves this machine)")
	env := flag.String("env", "../.env", "Environment file; existing shell variables take precedence")
	serve := flag.Bool("serve", false, "Run an HTTP API server (one isolated session per client) instead of the REPL")
	autoTelemetry := flag.Bool("telemetry-if-configured", false, "Send telemetry only when Grafana Cloud credentials are set; otherwise act as --no-telemetry")
	addr := flag.String("addr", "localhost:8080", "HTTP listen address, localhost only by default; only used with --serve. The API is unauthenticated and spends your Anthropic key, so expose it (such as :8080) deliberately")
	sessionTTL := flag.Duration("session-ttl", 30*time.Minute, "Idle session expiry; only used with --serve")
	scenario := flag.String("scenario", "classic", "The adventure: classic, or generated (built from random modules). With --serve, the default for sessions that don't choose. Defaults to $ASIMOV_SCENARIO when set")
	seed := flag.Uint64("seed", 0, "Replay the generated scenario with this seed; 0 picks a random one. Not used with --serve")
	llm := flag.String("llm", "anthropic", "The game master's model: anthropic (needs ANTHROPIC_API_KEY), or mock (no key, no network: scripted GM that plants the demo's flaws). Defaults to $ASIMOV_LLM when set")
	mockSeed := flag.Uint64("mock-seed", 1, "With --llm mock, the seed that decides which flaws appear: the same seed and conversation plant the same flaws. Defaults to $ASIMOV_MOCK_SEED when set")
	mockFlaws := flag.String("mock-flaws", "", "With --llm mock, the chance of each planted flaw: default, off, or a list such as all=0.3,voice_break=0 (flaws: "+mockFlawNames()+"). Defaults to $ASIMOV_MOCK_FLAWS when set")
	mockLatency := flag.Duration("mock-latency", 20*time.Millisecond, "With --llm mock, how long each model call takes, so latencies are steady instead of noise")
	flag.Parse()
	if err := dotenv.Load(*env); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read environment file: %w", err)
	}
	// The flag wins; otherwise the environment, which .env may set.
	if v := os.Getenv("ASIMOV_SCENARIO"); v != "" && !flagSet("scenario") {
		*scenario = v
	}
	if v := os.Getenv("ASIMOV_LLM"); v != "" && !flagSet("llm") {
		*llm = v
	}
	if v := os.Getenv("ASIMOV_MOCK_SEED"); v != "" && !flagSet("mock-seed") {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return fmt.Errorf("ASIMOV_MOCK_SEED must be a whole number: %w", err)
		}
		*mockSeed = n
	}
	if v := os.Getenv("ASIMOV_MOCK_FLAWS"); v != "" && !flagSet("mock-flaws") {
		*mockFlaws = v
	}
	if *llm != "anthropic" && *llm != "mock" {
		return fmt.Errorf("--llm must be anthropic or mock, not %q", *llm)
	}
	useMock := *llm == "mock" && !*offline
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Background telemetry diagnostics, and anything a dependency writes via
	// the standard log package, go through diag so the REPL can keep them
	// off its input prompt.
	diag := telemetry.NewDiagnostics(os.Stderr)
	log.SetOutput(diag)
	var client *agento11y.Client
	var generationExport bool
	cfg := telemetry.FromEnv()
	if !*offline && !useMock && os.Getenv("ANTHROPIC_API_KEY") == "" {
		return fmt.Errorf("ANTHROPIC_API_KEY is required (or use --offline)")
	}
	// Partial credentials still reach Validate, which names what's missing.
	if *autoTelemetry && !cfg.Configured() {
		*noTelemetry = true
	}
	if *offline && *offlineTelemetry && !*noTelemetry {
		// Offline play has no model calls to record, and sends nothing off
		// this machine: only OTLP to a receiver on it.
		if !cfg.LocalCollector() {
			return fmt.Errorf("--offline --telemetry needs OTLP_ENDPOINT on localhost (make local-stack starts one)")
		}
		cfg.GenerationEndpoint, cfg.APIEndpoint, cfg.Instance, cfg.Token, cfg.GenerationsFile = "", "", "", "", ""
	}
	if !*noTelemetry && (!*offline || *offlineTelemetry) {
		r, err := telemetry.Init(ctx, cfg, diag)
		if err != nil {
			return err
		}
		client, generationExport = r.Client, r.GenerationExport
		logger = r.Logger
		defer func() {
			shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := r.Shutdown(shutdown); err != nil {
				fmt.Fprintln(os.Stderr, "Telemetry flush failed:", err)
			}
		}()
	}
	g := gm.GM{Client: client, GenerationExport: generationExport, Logger: logger, Roll: game.RandomRoll}
	telemetryNote := describeTelemetry(cfg, client != nil)
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
	// The ledger the GM's model calls are counted in; --serve shows it at /spend.
	ledger := spend.NewLedger()
	if useMock {
		rates, err := gm.ParseMockFlaws(*mockFlaws)
		if err != nil {
			return err
		}
		mock := gm.NewMock(gm.MockConfig{Seed: *mockSeed, Rates: rates, Latency: *mockLatency})
		telemetryNote += " " + mock.Describe()
		g.Model = gm.Wrap(mock, client, cfg.Version, diag)
	} else if !*offline {
		model := os.Getenv("ANTHROPIC_MODEL")
		switch {
		case model != "":
		case g.Fixes:
			// The fixed version follows its notes more closely on a newer
			// model; the default keeps the one its defects were found on.
			model = "claude-sonnet-5-5"
		default:
			model = "claude-sonnet-4-6"
		}
		g.Model = gm.Wrap(spend.Meter(anthropic.New(os.Getenv("ANTHROPIC_API_KEY"), model), ledger, "gm"), client, cfg.Version, diag)
	}
	if *serve {
		if _, err := game.Choose(*scenario, 0); err != nil {
			return err
		}
		fmt.Println(telemetryNote)
		return runServe(ctx, &g, ledger, *addr, *sessionTTL, *scenario, logger)
	}
	sc, err := game.Choose(*scenario, *seed)
	if err != nil {
		return err
	}
	return runREPL(ctx, &g, sc, *offline, logger, diag, telemetryNote)
}

func mockFlawNames() string {
	names := make([]string, len(gm.MockFlaws))
	for i, f := range gm.MockFlaws {
		names[i] = string(f)
	}
	return strings.Join(names, ", ")
}

// describeTelemetry is the one-line summary of where this run's telemetry
// goes.
func describeTelemetry(cfg telemetry.Config, on bool) string {
	switch {
	case !on:
		return "Telemetry is off for this run."
	case cfg.OTLPConfigured() && cfg.GenerationConfigured():
		return "Grafana export on: generations, traces, metrics, and logs."
	case cfg.OTLPConfigured():
		return "Telemetry on: traces, metrics, and logs go to " + strings.TrimRight(cfg.Endpoint, "/") + ". Agent Observability is off."
	}
	return "Agent Observability export on: generations only."
}

// flagSet reports whether the named flag was given on the command line.
func flagSet(name string) bool {
	set := false
	flag.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}
