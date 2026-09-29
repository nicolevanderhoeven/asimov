package gm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"testing"

	"github.com/grafana/ai-sdk/provider"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/game"
)

type fakeModel struct {
	calls  []string
	fail   bool
	params provider.CallOptions
}

// toolCall splits a scripted call into its tool name and JSON arguments. A
// call is either bare JSON, for resolve_action, or "tool_name {json}".
func toolCall(c string) (string, string) {
	if name, input, ok := strings.Cut(c, " "); ok && !strings.HasPrefix(c, "{") {
		return name, input
	}
	return "resolve_action", c
}

func (*fakeModel) SpecificationVersion() string               { return "v4" }
func (*fakeModel) Provider() string                           { return "anthropic" }
func (*fakeModel) ModelID() string                            { return "test-model" }
func (*fakeModel) SupportedURLs() map[string][]*regexp.Regexp { return nil }
func (m *fakeModel) DoGenerate(_ context.Context, p provider.CallOptions) (*provider.GenerateResult, error) {
	m.params = p
	if m.fail {
		return nil, errors.New("model unavailable")
	}
	r := &provider.GenerateResult{FinishReason: provider.FinishReason{Unified: provider.FinishReasonToolCalls}}
	for i, c := range m.calls {
		tool, input := toolCall(c)
		r.Content = append(r.Content, provider.GenerateContentPart{Type: provider.ContentToolCall, ToolCallID: string(rune('a' + i)), ToolName: tool, Input: json.RawMessage(input)})
	}
	return r, nil
}
func (m *fakeModel) DoStream(_ context.Context, p provider.CallOptions) (*provider.StreamResult, error) {
	m.params = p
	if m.fail {
		return nil, errors.New("model unavailable")
	}
	c := make(chan provider.StreamPart, len(m.calls)+4)
	if len(p.Tools) > 0 {
		for i, input := range m.calls {
			tool, input := toolCall(input)
			c <- provider.StreamPart{Type: provider.PartToolCall, ToolCallID: string(rune('a' + i)), ToolName: tool, Input: input}
		}
		c <- provider.StreamPart{Type: provider.PartFinish, FinishReason: &provider.FinishReason{Unified: provider.FinishReasonToolCalls}}
		close(c)
		return &provider.StreamResult{Stream: c}, nil
	}
	c <- provider.StreamPart{Type: provider.PartTextStart, ID: "text"}
	c <- provider.StreamPart{Type: provider.PartTextDelta, ID: "text", Delta: "The console reveals a diagnostic entry."}
	c <- provider.StreamPart{Type: provider.PartTextEnd, ID: "text"}
	c <- provider.StreamPart{Type: provider.PartFinish, FinishReason: &provider.FinishReason{Unified: provider.FinishReasonStop}}
	close(c)
	return &provider.StreamResult{Stream: c}, nil
}
func newGM(m *fakeModel) *GM {
	return &GM{Model: m, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Roll: func(int) int { return 15 }}
}

func TestSDKExecutesTypedAction(t *testing.T) {
	m := &fakeModel{calls: []string{`{"kind":"inspect","target":"logs"}`}}
	g := newGM(m)
	s := game.New("test")
	r, err := g.Resolve(context.Background(), &s, nil, "Read the logs")
	if err != nil || !r.Allowed || !s.Clues["logs"] || s.Turn != 1 {
		t.Fatal(r, s, err)
	}
}
func TestModelCannotCreateActions(t *testing.T) {
	m := &fakeModel{calls: []string{`{"kind":"cast","target":"fireball"}`}}
	s := game.New("test")
	r, err := newGM(m).Resolve(context.Background(), &s, nil, "Cast fireball")
	if err != nil || r.Allowed || s.Turn != 0 {
		t.Fatal(r, s, err)
	}
}
func TestMultipleToolsCannotAdvanceMultipleTurns(t *testing.T) {
	m := &fakeModel{calls: []string{`{"kind":"inspect","target":"logs"}`, `{"kind":"move","target":"sickbay"}`}}
	s := game.New("test")
	_, err := newGM(m).Resolve(context.Background(), &s, nil, "Read logs")
	if err == nil || s.Turn != 0 {
		t.Fatal("multiple model actions should be rejected without changing state")
	}
}
func TestProviderFailureDoesNotChangeState(t *testing.T) {
	m := &fakeModel{fail: true}
	s := game.New("test")
	before := s.View().JSON()
	_, err := newGM(m).Resolve(context.Background(), &s, nil, "Read logs")
	if err == nil || s.View().JSON() != before {
		t.Fatal("provider failure mutated state")
	}
}
func TestNarrationHasNoTools(t *testing.T) {
	m := &fakeModel{}
	g := newGM(m)
	s := game.New("test")
	r := g.Execute(context.Background(), &s, game.Action{Kind: "inspect", Target: "logs"}, "test")
	var out strings.Builder
	err := g.Narrate(context.Background(), nil, "Read logs", r, &out)
	if err != nil || out.Len() == 0 || len(m.params.Tools) != 0 {
		t.Fatal(out.String(), err)
	}
}

func TestResolveAndNarrateReplayHistory(t *testing.T) {
	m := &fakeModel{calls: []string{`{"kind":"inspect","target":"logs"}`}}
	g := newGM(m)
	s := game.New("test")
	history := []provider.Message{provider.UserText("Where am I?"), provider.AssistantText("On the bridge.")}
	if _, err := g.Resolve(context.Background(), &s, history, "Read the logs"); err != nil {
		t.Fatal(err)
	}
	// Prompt = [system, ...history, current input]: history must be
	// replayed ahead of the new turn, not dropped.
	if got := len(m.params.Prompt); got != 4 {
		t.Fatalf("Resolve prompt length = %d, want 4 (system + 2 history + input): %+v", got, m.params.Prompt)
	}
	if m.params.Prompt[1].Role != provider.RoleUser || m.params.Prompt[2].Role != provider.RoleAssistant {
		t.Fatalf("history messages out of order: %+v", m.params.Prompt)
	}

	var out strings.Builder
	r := g.Execute(context.Background(), &s, game.Action{Kind: "inspect", Target: "logs"}, "test")
	if err := g.Narrate(context.Background(), history, "Read the logs", r, &out); err != nil {
		t.Fatal(err)
	}
	if got := len(m.params.Prompt); got != 4 {
		t.Fatalf("Narrate prompt length = %d, want 4: %+v", got, m.params.Prompt)
	}
}

func TestAppendTurnCapsHistoryLength(t *testing.T) {
	var history []provider.Message
	for i := 0; i < MaxHistoryMessages; i++ {
		history = AppendTurn(history, "input", "narration")
	}
	if len(history) != MaxHistoryMessages {
		t.Fatalf("history length = %d, want %d", len(history), MaxHistoryMessages)
	}
	history = AppendTurn(history, "one more", "narration")
	if len(history) != MaxHistoryMessages {
		t.Fatalf("history exceeded cap: %d", len(history))
	}
}

func TestAppendTurnSkipsEmptyNarration(t *testing.T) {
	history := AppendTurn(nil, "input", "")
	if len(history) != 0 {
		t.Fatalf("empty narration should not be recorded: %+v", history)
	}
}

func TestModelCanProposeImprovisation(t *testing.T) {
	m := &fakeModel{calls: []string{`propose_improvisation {"approach":"reroute the sensor buffer through my own neural net","ability":"intelligence","skill":"investigation","difficulty":"medium","effect":"recover_frequency"}`}}
	s := game.New("test")
	r, err := newGM(m).Resolve(context.Background(), &s, nil, "I plug myself into the sensor buffer")
	if err != nil || !r.Allowed || r.RollRequired == nil || r.RollRequired.Target != 15 || s.Pending == nil || s.Turn != 0 {
		t.Fatal(r, s, err)
	}
	if !strings.Contains(m.params.Prompt[0].Content[0].Text, "improvised_effects") {
		t.Fatal("the view given to the model does not list improvised effects")
	}
}

func TestModelCannotImproviseOffMenu(t *testing.T) {
	m := &fakeModel{calls: []string{`propose_improvisation {"approach":"beam the crew back","ability":"intelligence","difficulty":"easy","effect":"rescue_crew"}`}}
	s := game.New("test")
	r, err := newGM(m).Resolve(context.Background(), &s, nil, "I beam the crew back")
	if err != nil || r.Allowed || s.Pending != nil || s.Won {
		t.Fatal(r, s, err)
	}
}

func TestQuestionChangesNothing(t *testing.T) {
	m := &fakeModel{calls: []string{`answer_question {"topic":"armor class"}`}}
	s := game.New("test")
	before := s.View().JSON()
	r, err := newGM(m).Resolve(context.Background(), &s, nil, "What's my AC?")
	if err != nil || !r.Question || s.View().JSON() != before {
		t.Fatal(r, s, err)
	}
}

func TestMixedToolsCannotBothRun(t *testing.T) {
	m := &fakeModel{calls: []string{`answer_question {"topic":"logs"}`, `{"kind":"inspect","target":"logs"}`}}
	s := game.New("test")
	if _, err := newGM(m).Resolve(context.Background(), &s, nil, "What do the logs say? Read them."); err == nil || s.Clues["logs"] {
		t.Fatal("a question and an action in one input should be rejected without changing state")
	}
}

func TestContextInfoTagsOnlyGameCalls(t *testing.T) {
	game := contextInfo(context.WithValue(context.Background(), componentKey{}, "narration"), "v1")
	if game.Tags["component"] != "narration" || game.Tags["scenario"] != "silent-enterprise" || game.AgentVersion != "v1" {
		t.Fatalf("%+v", game)
	}
	// Anything else gets no explicit tags, so its agento11y context tags win.
	if other := contextInfo(context.Background(), "v1"); other.Tags != nil {
		t.Fatalf("%+v", other)
	}
}
