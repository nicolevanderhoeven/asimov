package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/grafana/agento11y/go/agento11y"
	"github.com/grafana/ai-sdk/middleware/agentobservability"
	"github.com/grafana/ai-sdk/provider"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/game"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/gm"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

func runREPL(ctx context.Context, g *gm.GM, offline bool, logger *slog.Logger) error {
	// Every run starts a fresh game; state and dialogue history live only in
	// memory for the lifetime of the process.
	s := game.New(agentobservability.NewGenerationID())
	var history []provider.Message
	ctx = agento11y.WithConversationID(ctx, s.ConversationID)
	ctx = agento11y.WithConversationTitle(ctx, game.Title)
	fmt.Printf("\n%s\n2014 5e subset with Star Trek adaptations. Type /help for commands.\n\n", game.Title)
	fmt.Println(game.Opening)
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
			fmt.Println("Type an action naturally, or /do KIND TARGET from /actions.\n/actions lists supported actions; /status shows state; /sheet shows Data's sheet; /quit exits.\nProgress is not saved; each run starts a new game.")
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
			fmt.Println("This adventure has ended. Restart the game to play again.")
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
		} else if offline {
			fmt.Println("Offline mode requires an exact /do command from /actions.")
			span.End()
			cancel()
			continue
		} else {
			result, err = g.Resolve(turnCtx, &s, history, input)
		}
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "action resolution failed")
			fmt.Fprintln(os.Stderr, "Action failed; state unchanged:", err)
			span.End()
			cancel()
			continue
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
		if !offline {
			fmt.Print("\nGM: ")
			var narration bytes.Buffer
			if err = g.Narrate(turnCtx, history, input, result, io.MultiWriter(os.Stdout, &narration)); err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, "narration failed")
				fmt.Fprintln(os.Stderr, "\nNarration interrupted; the engine result above still stands:", err)
			}
			// Recorded for the next turn regardless of a mid-stream error above:
			// AppendTurn only skips a turn whose narration is entirely empty, and
			// whatever text made it to the terminal before an interruption is
			// exactly what a reader reconstructing this conversation would see.
			history = gm.AppendTurn(history, input, narration.String())
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
