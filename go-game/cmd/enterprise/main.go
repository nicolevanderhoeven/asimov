package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/grafana/agento11y/go/agento11y"
	"github.com/grafana/ai-sdk/middleware/agentobservability"
	"github.com/grafana/ai-sdk/providers/anthropic"
	"github.com/joho/godotenv"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/game"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/gm"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
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
	flag.Parse()
	if err := godotenv.Load(*env); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read environment file: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	s := game.New(agentobservability.NewGenerationID())
	if *resume {
		var err error
		s, err = game.Load(*save)
		if err != nil {
			return err
		}
	} else if _, err := os.Stat(*save); err == nil {
		return fmt.Errorf("save already exists; use --resume or choose a new --save path")
	}
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
	ctx = agento11y.WithConversationID(ctx, s.ConversationID)
	ctx = agento11y.WithConversationTitle(ctx, game.Title)
	if err := game.Save(*save, s); err != nil {
		return err
	}
	fmt.Printf("\n%s\n2014 5e subset with Star Trek adaptations. Type /help for commands.\n\n", game.Title)
	if !*resume {
		fmt.Println(game.Opening)
	}
	show(s)
	// Read input in a goroutine so Ctrl-C also shuts down exporters while idle.
	lines := make(chan string)
	inputErrors := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 4096), 64*1024)
		defer close(lines)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
		inputErrors <- scanner.Err()
	}()
	for {
		fmt.Print("\nData > ")
		var input string
		select {
		case <-ctx.Done():
			return nil
		case line, ok := <-lines:
			if !ok {
				return <-inputErrors
			}
			input = strings.TrimSpace(line)
		}
		if input == "" {
			continue
		}
		switch input {
		case "quit", "exit", "/quit":
			return nil
		case "/help":
			fmt.Println("Type an action naturally, or /do KIND TARGET from /actions.\n/actions lists supported actions; /status shows state; /sheet shows Data's sheet; /quit exits.\nState autosaves after every resolved action. Use --resume next time.")
			continue
		case "/status", "/actions":
			show(s)
			continue
		case "/sheet":
			b, _ := json.MarshalIndent(game.Data(), "", "  ")
			fmt.Println(string(b))
			continue
		}
		if strings.HasPrefix(input, "/") && !strings.HasPrefix(input, "/do ") {
			fmt.Println("Unknown command. Type /help.")
			continue
		}
		if s.Won || s.HP <= 0 {
			fmt.Println("This adventure has ended. Use a new --save path to start again.")
			continue
		}
		turnCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		turnCtx, span := otel.Tracer(telemetry.Service).Start(turnCtx, "game.turn")
		span.SetAttributes(attribute.String("gen_ai.conversation.id", s.ConversationID), attribute.Int("game.turn", s.Turn+1))
		var result game.Result
		var err error
		if strings.HasPrefix(input, "/do ") {
			parts := strings.Fields(input)
			if len(parts) != 3 {
				fmt.Println("Usage: /do KIND TARGET")
				span.End()
				cancel()
				continue
			}
			result = g.Execute(turnCtx, &s, game.Action{Kind: parts[1], Target: parts[2]}, agentobservability.NewGenerationID())
		} else if *offline {
			fmt.Println("Offline mode requires an exact /do command from /actions.")
			span.End()
			cancel()
			continue
		} else {
			result, err = g.Resolve(turnCtx, &s, input)
		}
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "action resolution failed")
			fmt.Fprintln(os.Stderr, "Action failed; state unchanged:", err)
			span.End()
			cancel()
			continue
		}
		// Persist authoritative state before optional narration, so an interrupted
		// stream or provider outage cannot undo or repeat a completed action.
		if err = game.Save(*save, s); err != nil {
			span.End()
			cancel()
			return fmt.Errorf("save resolved turn: %w", err)
		}
		fmt.Println("\n[Engine]", result.Message)
		for _, r := range result.Rolls {
			fmt.Printf("  %s: %v %+d = %d", r.Label, r.Dice, r.Modifier, r.Total)
			if r.Target > 0 {
				fmt.Printf(" vs %d; success=%t", r.Target, r.Success)
			}
			fmt.Println()
		}
		if result.Damage > 0 {
			fmt.Printf("  Data takes %d damage.\n", result.Damage)
		}
		if !*offline {
			fmt.Print("\nGM: ")
			if err = g.Narrate(turnCtx, input, result, os.Stdout); err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, "narration failed")
				fmt.Fprintln(os.Stderr, "\nNarration interrupted; the engine result above is saved:", err)
			}
			fmt.Println()
		}
		logger.InfoContext(turnCtx, "player turn complete", "turn", s.Turn, "allowed", result.Allowed, "won", s.Won)
		span.End()
		cancel()
		fmt.Printf("[%s | HP %d/%d | %s]\n", s.Location, s.HP, game.Data().MaxHP, s.View().Status)
	}
}

func show(s game.State) {
	v := s.View()
	fmt.Printf("\n%s\nLocation: %s | HP: %d/%d | %s\n", v.Description, v.Location, v.HP, v.Character.MaxHP, v.Status)
	for _, clue := range v.Discovered {
		fmt.Println("Evidence:", clue)
	}
	for _, a := range v.Actions {
		fmt.Printf("  /do %s %s — %s\n", a.Kind, a.Target, a.Description)
	}
}
