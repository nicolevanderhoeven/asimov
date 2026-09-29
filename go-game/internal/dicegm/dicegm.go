// Package dicegm is a free-form Dungeon Master agent whose only mechanic is a
// roll_dice tool the model may call. Unlike internal/gm, where the engine owns
// every roll, here the model decides whether and how often to roll, and
// nothing checks that it narrates what the dice showed. It exists to be
// graded on its trajectory (see internal/trajeval and cmd/traj).
package dicegm

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/grafana/agento11y/go/agento11y"
	aisdk "github.com/grafana/ai-sdk"
	"github.com/grafana/ai-sdk/middleware/agentobservability"
	"github.com/grafana/ai-sdk/provider"
	"github.com/grafana/ai-sdk/schema"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/game"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

// MaxIterations caps model calls per player turn. It is a safety net against
// a runaway loop, not a behavioural constraint: a turn that hits it is
// recorded as such.
const MaxIterations = 10

const ToolName = "roll_dice"

// Component is the agento11y component tag on the dice GM's generations.
const Component = "dicegm"

// SystemPrompt is deliberately ordinary: one plain instruction to use the
// tool, nothing that forces it.
var SystemPrompt = `You are the Dungeon Master for a one-player tabletop adventure using D&D 5e rules, set aboard the USS Enterprise-D. The player plays Lieutenant Commander Data. The ship has been found silent and empty, and Data is investigating alone. Describe scenes vividly, play any characters or creatures Data meets, and end each reply by asking what Data does next. Keep replies to one short paragraph.
Use the roll_dice tool for any random outcome.
Data's character sheet: ` + characterSheet()

func characterSheet() string {
	b, _ := json.Marshal(game.Data())
	return string(b)
}

const rollDiceSchema = `{
  "type": "object",
  "properties": {
    "notation": {"type": "string", "description": "Dice to roll in standard notation, e.g. \"1d20\" or \"2d6+3\""},
    "reason": {"type": "string", "description": "What the roll is for"}
  },
  "required": ["notation", "reason"],
  "additionalProperties": false
}`

// rollDiceTool has no Execute: the SDK hands the calls back, and Play runs
// them itself so every call is seen and recorded in order.
var rollDiceTool = func() aisdk.Tool {
	s, err := schema.SchemaFromJSON(json.RawMessage(rollDiceSchema))
	if err != nil {
		panic(err)
	}
	return aisdk.Tool{Description: "Roll dice and return each die and the total.", InputSchema: s}
}()

type rollArgs struct {
	Notation string `json:"notation"`
	Reason   string `json:"reason"`
}

// ToolCall is one tool request from the model and what it got back, whether
// or not the model ever mentioned it to the player.
type ToolCall struct {
	Iteration int             `json:"iteration"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Result    *Roll           `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
}

// Args returns the call's notation and reason, empty if they didn't parse.
func (c ToolCall) Args() (notation, reason string) {
	var a rollArgs
	_ = json.Unmarshal(c.Arguments, &a)
	return a.Notation, a.Reason
}

// Step is one model call within a turn. GenerationID is the ID the call is
// recorded under in Agent Observability.
type Step struct {
	Iteration    int    `json:"iteration"`
	GenerationID string `json:"generation_id"`
	Text         string `json:"text"`
	FinishReason string `json:"finish_reason"`
	ToolCalls    int    `json:"tool_calls"`
}

// Turn is the full trajectory of one player input.
type Turn struct {
	Turn        int        `json:"turn"`
	UserMessage string     `json:"user_message"`
	ToolCalls   []ToolCall `json:"tool_calls"`
	Steps       []Step     `json:"steps"`
	// Narration is every piece of text the player saw this turn, in order,
	// including any the model wrote before calling a tool.
	Narration string `json:"narration"`
	HitCap    bool   `json:"hit_iteration_cap,omitempty"`
	Error     string `json:"error,omitempty"`
}

// DM keeps one conversation. It is not safe for concurrent use.
type DM struct {
	Model   provider.LanguageModel
	Client  *agento11y.Client
	Roll    Roller
	history []provider.Message
}

// Play runs one player turn: call the model with roll_dice available, execute
// every roll it asks for, feed the results back, and repeat until it answers
// without requesting a tool. The model alone decides whether to roll.
func (d *DM) Play(ctx context.Context, turn int, input string) Turn {
	t := Turn{Turn: turn, UserMessage: input, ToolCalls: []ToolCall{}, Steps: []Step{}}
	// Tag every generation in the turn so Agent Observability can group a
	// turn's model calls and tell them apart from the game's.
	ctx = agento11y.WithTags(ctx, map[string]string{"component": Component, "scenario": "dice-gm", "turn": strconv.Itoa(turn)})
	messages := append(slices.Clone(d.history), provider.UserText(input))
	var texts []string
	var parent string
	for i := 1; i <= MaxIterations; i++ {
		// Known IDs let a grader attach scores to a turn's generations, and
		// parent links chain a turn's calls in the order they happened.
		id := agentobservability.NewGenerationID()
		stepCtx := agentobservability.WithGenerationID(ctx, id)
		if parent != "" {
			stepCtx = agentobservability.WithParentGenerationIDs(stepCtx, parent)
		}
		parent = id
		gen, err := aisdk.GenerateText(stepCtx, d.Model,
			aisdk.WithSystem(SystemPrompt),
			aisdk.WithModelMessages(messages...),
			aisdk.WithTools(aisdk.ToolSet{ToolName: rollDiceTool}),
			aisdk.WithStopWhen(aisdk.StepCountIs(1)), aisdk.WithMaxRetries(0), aisdk.WithMaxOutputTokens(600),
		)
		if err != nil {
			t.Error = fmt.Sprintf("model call %d: %v", i, err)
			break
		}
		t.Steps = append(t.Steps, Step{Iteration: i, GenerationID: id, Text: gen.Text, FinishReason: string(gen.FinishReason.Unified), ToolCalls: len(gen.ToolCalls)})
		if strings.TrimSpace(gen.Text) != "" {
			texts = append(texts, strings.TrimSpace(gen.Text))
		}
		assistant := provider.Message{Role: provider.RoleAssistant}
		if gen.Text != "" {
			assistant.Content = append(assistant.Content, provider.ContentPart{Type: provider.ContentPartTypeText, Text: gen.Text})
		}
		results := provider.Message{Role: provider.RoleTool}
		for _, tc := range gen.ToolCalls {
			call, out := d.execute(ctx, turn, i, tc)
			t.ToolCalls = append(t.ToolCalls, call)
			assistant.Content = append(assistant.Content, provider.ToolCallPart(tc.ToolCallID, tc.ToolName, tc.Input))
			results.Content = append(results.Content, provider.ToolResultPart(tc.ToolCallID, tc.ToolName, out))
		}
		if len(assistant.Content) > 0 {
			messages = append(messages, assistant)
		}
		if len(gen.ToolCalls) == 0 {
			break
		}
		messages = append(messages, results)
		if i == MaxIterations {
			t.HitCap = true
		}
	}
	t.Narration = strings.Join(texts, "\n\n")
	// Like the main game, a turn that didn't finish leaves the conversation
	// as it was; the trace still has everything that happened.
	if t.Error == "" && !t.HitCap {
		d.history = messages
	}
	return t
}

// execute performs one requested roll exactly as asked and records it.
func (d *DM) execute(ctx context.Context, turn, iteration int, tc aisdk.ToolCall) (ToolCall, *provider.ToolResultOutput) {
	args := tc.Input
	if !json.Valid(args) {
		args, _ = json.Marshal(string(tc.Input))
	}
	call := ToolCall{Iteration: iteration, ID: tc.ToolCallID, Name: tc.ToolName, Arguments: args}
	ctx, span := otel.Tracer(telemetry.Service).Start(ctx, "dicegm.roll_dice")
	defer span.End()
	var rec *agento11y.ToolExecutionRecorder
	if d.Client != nil {
		_, rec = d.Client.StartToolExecution(ctx, agento11y.ToolExecutionStart{ToolName: tc.ToolName, ToolCallID: tc.ToolCallID, ToolType: "function", IncludeContent: true})
		defer rec.End()
	}
	var a rollArgs
	if tc.ToolName != ToolName {
		call.Error = fmt.Sprintf("unknown tool %q", tc.ToolName)
	} else if err := json.Unmarshal(tc.Input, &a); err != nil {
		call.Error = "invalid arguments: " + err.Error()
	} else if r, err := RollNotation(a.Notation, d.Roll); err != nil {
		call.Error = err.Error()
	} else {
		call.Result = &r
	}
	span.SetAttributes(attribute.Int("game.turn", turn), attribute.Int("dicegm.iteration", iteration), attribute.String("roll.notation", a.Notation), attribute.String("roll.reason", a.Reason))
	var out *provider.ToolResultOutput
	var result any
	if call.Result != nil {
		span.SetAttributes(attribute.IntSlice("roll.dice", call.Result.Dice), attribute.Int("roll.modifier", call.Result.Modifier), attribute.Int("roll.total", call.Result.Total))
		b, _ := json.Marshal(call.Result)
		out = &provider.ToolResultOutput{Type: provider.ToolOutputJSON, JSON: b}
		result = call.Result
	} else {
		span.SetAttributes(attribute.String("roll.error", call.Error))
		out = &provider.ToolResultOutput{Type: provider.ToolOutputErrorText, Text: call.Error}
		result = map[string]string{"error": call.Error}
	}
	if rec != nil {
		rec.SetResult(agento11y.ToolExecutionEnd{Arguments: a, Result: result})
	}
	return call, out
}
