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

func runREPL(ctx context.Context, g *gm.GM, offline bool, logger *slog.Logger, diag *telemetry.Diagnostics, telemetryNote string) error {
	// Telemetry diagnostics arrive from background goroutines at any time.
	// Hold them and print them just before each prompt, so they never land on
	// the line where the player is typing.
	diag.Hold()
	defer diag.Release()
	// Every run starts a fresh game; state and dialogue history live only in
	// memory for the lifetime of the process.
	s := game.New(agentobservability.NewGenerationID())
	var history []provider.Message
	ctx = agento11y.WithConversationID(ctx, s.ConversationID)
	ctx = agento11y.WithConversationTitle(ctx, game.Title)
	fmt.Println(banner())
	fmt.Println(center(telemetryNote))
	fmt.Println(center("Type /help for commands."))
	fmt.Printf("\n%s\n", wrap(game.Opening, ""))
	show(s, true)
	scene := sceneKey(s)
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
		diag.Flush()
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
			fmt.Println("Type an action naturally, or /do KIND TARGET from /actions.\nYou can also try anything not on the list, or ask a question; the GM sets a check if it needs one.\nWhen the GM asks for a check, roll it yourself with /roll ABILITY, e.g. /roll Intelligence.\n/try ABILITY[/SKILL] DIFFICULTY EFFECT APPROACH improvises without the model, e.g.\n  /try strength/athletics hard disable_drone rip the drone off its mount\n/actions lists supported actions; /status shows state; /sheet shows Data's sheet; /quit exits.\nProgress is not saved; each run starts a new game.")
			continue
		case "/status", "/actions":
			show(s, false)
			continue
		case "/sheet":
			b, _ := json.MarshalIndent(game.Data(), "", "  ")
			fmt.Println(string(b))
			continue
		}
		isRoll := input == "/roll" || strings.HasPrefix(input, "/roll ")
		isTry := strings.HasPrefix(input, "/try ")
		if strings.HasPrefix(input, "/") && !strings.HasPrefix(input, "/do ") && !isRoll && !isTry {
			fmt.Println("Unknown command. Type /help.")
			continue
		}
		if s.Won || s.HP <= 0 {
			fmt.Println("This adventure has ended. Restart the game to play again.")
			continue
		}
		fmt.Printf("\n%s\n", heading(fmt.Sprintf("Turn %d", s.Turn+1)))
		turnCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		turnCtx, span := otel.Tracer(telemetry.Service).Start(turnCtx, "game.turn")
		span.SetAttributes(attribute.String("gen_ai.conversation.id", s.ConversationID), attribute.Int("game.turn", s.Turn+1))
		var result game.Result
		var err error
		if isTry {
			parts := strings.Fields(input)
			if len(parts) < 5 {
				fmt.Println("Usage: /try ABILITY[/SKILL] DIFFICULTY EFFECT APPROACH")
				span.End()
				cancel()
				continue
			}
			ability, skill, _ := strings.Cut(parts[1], "/")
			im := game.Improvisation{Ability: ability, Skill: skill, Difficulty: parts[2], Effect: parts[3], Approach: strings.Join(parts[4:], " ")}
			result = g.Improvise(turnCtx, &s, im, agentobservability.NewGenerationID())
		} else if isRoll {
			result = g.RollPending(turnCtx, &s, strings.TrimSpace(strings.TrimPrefix(input, "/roll")), agentobservability.NewGenerationID())
		} else if strings.HasPrefix(input, "/do ") {
			parts := strings.Fields(input)
			if len(parts) != 3 {
				fmt.Println("Usage: /do KIND TARGET")
				span.End()
				cancel()
				continue
			}
			result = g.Execute(turnCtx, &s, game.Action{Kind: parts[1], Target: parts[2]}, agentobservability.NewGenerationID())
		} else if offline {
			fmt.Println("Offline mode requires an exact /do, /try, or /roll command; see /help.")
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
		// The player hears one voice, the GM's. Online, the model narrates the
		// engine's result, so its plain message is shown only as a fallback
		// when narration fails; offline, that message is the GM's line.
		printPlayerRolls(result.Rolls)
		printOtherRolls(result)
		if offline {
			fmt.Printf("\n%s\n", wrap("GM: "+result.Message, ""))
		} else {
			fmt.Print("\nGM: ")
			var narration bytes.Buffer
			if err = g.Narrate(turnCtx, history, input, result, io.MultiWriter(os.Stdout, &narration)); err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, "narration failed")
				fmt.Fprintln(os.Stderr, "\nThe GM was interrupted:", err)
				fmt.Printf("\n%s\n", wrap("What happened: "+result.Message, ""))
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
		// Redraw the full scene only when it changes (new room, combat
		// starting or ending, the adventure ending); otherwise one line.
		if next := sceneKey(s); next != scene {
			scene = next
			show(s, true)
		} else {
			fmt.Printf("\n%s\n", statusLine(s.View()))
		}
	}
}

// show prints the current scene, with its art when withArt is set.
func show(s game.State, withArt bool) {
	v := s.View()
	if withArt {
		fmt.Println(sceneArt[sceneKey(s)])
	}
	fmt.Printf("\n%s\n%s\n%s\n\n%s\n", rule('='), statusLine(v), rule('='), wrap(v.Description, ""))
	if len(v.Discovered) > 0 {
		fmt.Printf("\n%s\n", heading("Evidence"))
		for _, clue := range v.Discovered {
			fmt.Println(strings.Replace(wrap(clue, "    "), "    ", "  * ", 1))
		}
	}
	if len(v.Leads) > 0 {
		fmt.Printf("\n%s\n", heading("Leads"))
		for _, lead := range v.Leads {
			fmt.Println(strings.Replace(wrap(lead, "    "), "    ", "  - ", 1))
		}
	}
	if len(v.Actions) > 0 {
		fmt.Printf("\n%s\n", heading("Actions"))
		for _, a := range v.Actions {
			fmt.Printf("  /do %s %s\n%s\n", a.Kind, a.Target, wrap(a.Description, "      "))
		}
	}
	if len(v.Effects) > 0 {
		fmt.Printf("\n%s\n%s\n", heading("Or improvise"), wrap("Describe anything else you try, or ask a question. An attempt can aim for:", "  "))
		for _, e := range v.Effects {
			fmt.Printf("  %s\n%s\n", e.ID, wrap(e.Description, "      "))
		}
	}
	if v.Advantage {
		fmt.Printf("\n%s\n", wrap(">> Your next roll has advantage.", ""))
	}
	if v.Pending != nil {
		fmt.Printf("\n%s\n", wrap(fmt.Sprintf(">> Waiting on your roll: %s vs %d. Type %s.", v.Pending.Check, v.Pending.Target, v.Pending.Command), ""))
	}
}
