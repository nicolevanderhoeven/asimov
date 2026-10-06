package gm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/grafana/ai-sdk/provider"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/game"
)

type fakeModel struct {
	calls  []string
	fail   bool
	params provider.CallOptions
	// rolls are the roll_dice inputs the narrator calls on its first step, if
	// it may call tools then; after their results come back, it narrates.
	rolls []string
	// choices are the tool choices of every narration step, in order, and
	// firstTools the tools of the first.
	choices    []provider.ToolChoiceType
	firstTools []provider.Tool
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
	c := make(chan provider.StreamPart, len(m.calls)+len(m.rolls)+4)
	narrating := slices.ContainsFunc(p.Tools, func(t provider.Tool) bool { return t.Name == "roll_dice" })
	canRoll := narrating
	if narrating && p.ToolChoice != nil {
		if m.choices == nil {
			m.firstTools = p.Tools
		}
		m.choices = append(m.choices, p.ToolChoice.Type)
		canRoll = p.ToolChoice.Type != provider.ToolChoiceNone
	}
	if canRoll && len(m.rolls) > 0 && p.Prompt[len(p.Prompt)-1].Role != provider.RoleTool {
		for i, input := range m.rolls {
			c <- provider.StreamPart{Type: provider.PartToolCall, ToolCallID: "roll" + string(rune('a'+i)), ToolName: "roll_dice", Input: input}
		}
		c <- provider.StreamPart{Type: provider.PartFinish, FinishReason: &provider.FinishReason{Unified: provider.FinishReasonToolCalls}}
		close(c)
		return &provider.StreamResult{Stream: c}, nil
	}
	if len(p.Tools) > 0 && !narrating {
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
	g := newGM(m)
	var logs strings.Builder
	g.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	r, err := g.Resolve(context.Background(), &s, nil, "Cast fireball")
	if err != nil || r.Allowed || s.Turn != 0 {
		t.Fatal(r, s, err)
	}
	if r.Message == "" || !strings.Contains(logs.String(), "reason=") || !strings.Contains(logs.String(), r.Message) {
		t.Fatalf("a rejection should log its reason %q: %s", r.Message, logs.String())
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
func TestNarrationOffersOnlyRollDice(t *testing.T) {
	m := &fakeModel{}
	g := newGM(m)
	s := game.New("test")
	r := g.Execute(context.Background(), &s, game.Action{Kind: "inspect", Target: "logs"}, game.Ruling{}, "test")
	var out strings.Builder
	n, err := g.Narrate(context.Background(), &s, nil, "Read logs", r, &out)
	if err != nil || out.Len() == 0 || len(m.params.Tools) != 1 || m.params.Tools[0].Name != "roll_dice" || len(n.Rolls) != 0 {
		t.Fatal(out.String(), err, m.params.Tools)
	}
	// No GM roll is due, so roll_dice is declared but can't be called.
	if !slices.Equal(m.choices, []provider.ToolChoiceType{provider.ToolChoiceNone}) {
		t.Fatal("roll_dice must not be callable with no GM roll due", m.choices)
	}
}

func TestNarratorCannotRollWhileThePlayersRollIsDue(t *testing.T) {
	m := &fakeModel{rolls: []string{`{"notation":"1d20+6","reason":"Data reconstructs the sensor buffer"}`}}
	g := newGM(m)
	s := game.New("test")
	r := s.Apply(game.Action{Kind: "scan", Target: "sensors"}, game.Ruling{})
	if r.RollRequired == nil {
		t.Fatal(r)
	}
	var out strings.Builder
	n, err := g.Narrate(context.Background(), &s, nil, "I scan the sensors", r, &out)
	if err != nil || len(n.Rolls) != 0 || out.Len() == 0 || s.Pending == nil || s.Pending.By != game.ByPlayer {
		t.Fatalf("%+v %v %+v", n.Rolls, err, s.Pending)
	}
	if !slices.Equal(m.choices, []provider.ToolChoiceType{provider.ToolChoiceNone}) {
		t.Fatal("roll_dice must not be callable while the player's roll is due", m.choices)
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
	r := g.Execute(context.Background(), &s, game.Action{Kind: "inspect", Target: "logs"}, game.Ruling{}, "test")
	if _, err := g.Narrate(context.Background(), &s, history, "Read the logs", r, &out); err != nil {
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

func TestResolvePassesTheGMsRuling(t *testing.T) {
	m := &fakeModel{calls: []string{`{"kind":"scan","target":"sensors","roll":"no_roll","roll_reason":"an android reads a buffer easily"}`}}
	s := game.New("test")
	r, err := newGM(m).Resolve(context.Background(), &s, nil, "I scan the sensors")
	if err != nil || r.RollRequired != nil || r.Ruling == nil || !s.Clues["frequency"] || s.Turn != 1 {
		t.Fatalf("a no_roll ruling should succeed at once: %+v %v", r, err)
	}
}

func TestResolveWaitsForAnActionInProgress(t *testing.T) {
	m := &fakeModel{calls: []string{`{"kind":"move","target":"sickbay"}`}}
	g := newGM(m)
	s := game.New("test")
	s.Location = "engineering"
	s.Apply(game.Action{Kind: "attack", Target: "drone"}, game.Ruling{})
	g.RollPending(context.Background(), &s, "initiative", "roll")
	m.params = provider.CallOptions{}
	r, err := g.Resolve(context.Background(), &s, nil, "Take me to sickbay")
	if err != nil || r.Allowed || s.Location != "engineering" || m.params.Prompt != nil {
		t.Fatalf("an action in progress should be finished before the model is asked: %+v", r)
	}
}

// dodge puts s mid-combat with the drone's attack roll due.
func dodge(t *testing.T, g *GM) (game.State, game.Result) {
	t.Helper()
	s := game.New("test")
	s.Location, s.Combat = "engineering", true
	r := g.Execute(context.Background(), &s, game.Action{Kind: "dodge", Target: "drone"}, game.Ruling{}, "dodge")
	if r.GMRollRequired == nil || r.GMRollRequired.Notation != "2d20kl1+3" {
		t.Fatal(r)
	}
	return s, r
}

func TestNarratorAppliesTheGMRollDue(t *testing.T) {
	m := &fakeModel{rolls: []string{`{"notation":"2d20kl1+3","reason":"drone fires","purpose":"drone_attack"}`}}
	g := newGM(m)
	g.Roll = func(sides int) int { return min(15, sides) } // 15+3 hits AC 14

	s, r := dodge(t, g)
	var out strings.Builder
	n, err := g.Narrate(context.Background(), &s, nil, "I dodge", r, &out)
	if err != nil || len(n.Rolls) != 1 || n.Rolls[0].AppliedTo != "drone_attack" || n.Rolls[0].Error != "" {
		t.Fatalf("%+v %v", n.Rolls, err)
	}
	// The hit's damage roll was due next and never made, so it is skipped.
	if s.Pending != nil || s.HP != 24 || len(n.Result.Rolls) != 2 || !n.Result.Rolls[1].Skipped || n.Result.Rolls[1].Label != "Drone damage" {
		t.Fatalf("%+v HP %d", n.Result.Rolls, s.HP)
	}
	if b, _ := json.Marshal(m.params.Prompt[len(m.params.Prompt)-1]); !strings.Contains(string(b), "drone_damage") {
		t.Fatalf("the GM was not told the damage roll was next: %+v", m.params.Prompt[len(m.params.Prompt)-1])
	}
	// The damage roll was due after the hit, so the GM was made to roll it.
	if !slices.Equal(m.choices, []provider.ToolChoiceType{provider.ToolChoiceTool, provider.ToolChoiceTool}) {
		t.Fatal(m.choices)
	}
}

func TestNarratorMayRollOnlyTheRollDue(t *testing.T) {
	m := &fakeModel{}
	g := newGM(m)
	s, r := dodge(t, g)
	var out strings.Builder
	if _, err := g.Narrate(context.Background(), &s, nil, "I dodge", r, &out); err != nil {
		t.Fatal(err)
	}
	b := m.firstTools[0].InputSchema
	for _, want := range []string{`"enum":["drone_attack"]`, `"enum":["2d20kl1+3"]`, `"purpose"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("roll_dice should allow only the roll due (%s): %s", want, b)
		}
	}
	if m.choices[0] != provider.ToolChoiceTool {
		t.Fatal("the GM must make the roll due", m.choices)
	}
}

func TestNarratorRollsAHitsDamageInTheSameNarration(t *testing.T) {
	m := &fakeModel{rolls: []string{
		`{"notation":"2d20kl1+3","reason":"drone fires","purpose":"drone_attack"}`,
		`{"notation":"1d4+1","reason":"drone damage","purpose":"drone_damage"}`,
	}}
	g := newGM(m)
	g.Roll = func(sides int) int { return min(15, sides) } // hits, then 4+1 damage
	s, r := dodge(t, g)
	var out strings.Builder
	n, err := g.Narrate(context.Background(), &s, nil, "I dodge", r, &out)
	if err != nil || len(n.Rolls) != 2 || n.Rolls[0].AppliedTo != "drone_attack" || n.Rolls[1].AppliedTo != "drone_damage" {
		t.Fatalf("%+v %v", n.Rolls, err)
	}
	if s.Pending != nil || s.HP != 24-(4+1) {
		t.Fatalf("%+v HP %d", n.Result.Rolls, s.HP)
	}
	// With nothing left due, the GM narrates without roll_dice.
	if !slices.Equal(m.choices, []provider.ToolChoiceType{provider.ToolChoiceTool, provider.ToolChoiceNone}) {
		t.Fatal(m.choices)
	}
}

func TestNarratorRollsAreRecordedEvenWhenNotApplied(t *testing.T) {
	m := &fakeModel{rolls: []string{
		`{"notation":"1d20+3","reason":"drone fires","purpose":"drone_attack"}`,
		`{"notation":"1d100","reason":"is the coffee still warm"}`,
		`{"notation":"lots","reason":"x"}`,
	}}
	g := newGM(m)
	s, r := dodge(t, g)
	var out, logs strings.Builder
	g.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	n, err := g.Narrate(context.Background(), &s, nil, "I dodge", r, &out)
	if err != nil || len(n.Rolls) != 3 {
		t.Fatalf("%+v %v", n.Rolls, err)
	}
	if got := strings.Count(logs.String(), `level=WARN msg="gm roll failed"`); got != 3 {
		t.Fatalf("want the 3 failed rolls logged as warnings, got %d: %s", got, logs.String())
	}
	if n.Rolls[0].AppliedTo != "" || !strings.Contains(n.Rolls[0].Error, "2d20kl1+3") || n.Rolls[0].Result != nil {
		t.Fatalf("a roll with the wrong dice must not apply or roll: %+v", n.Rolls[0])
	}
	if n.Rolls[1].AppliedTo != "" || !strings.Contains(n.Rolls[1].Error, "the roll due is drone_attack (2d20kl1+3)") || n.Rolls[1].Result != nil {
		t.Fatalf("a roll with no purpose must not roll: %+v", n.Rolls[1])
	}
	if n.Rolls[2].Error == "" || n.Rolls[2].Result != nil {
		t.Fatalf("bad notation should be recorded as an error: %+v", n.Rolls[2])
	}
	// The GM hears why each call failed, and no numbers to narrate.
	results, _ := json.Marshal(m.params.Prompt[len(m.params.Prompt)-1])
	if !strings.Contains(string(results), `"error":`) || strings.Contains(string(results), `"total":`) || strings.Contains(string(results), `"dice":`) {
		t.Fatalf("an unapplied roll must return no numbers: %s", results)
	}
	// The drone's attack was never validly rolled, so it didn't happen.
	if s.Pending != nil || s.HP != 24 || !n.Result.Rolls[0].Skipped {
		t.Fatalf("%+v", n.Result.Rolls)
	}
}

func TestAutoRollMakesTheGMsRollsWithoutAModel(t *testing.T) {
	g := newGM(&fakeModel{})
	g.Roll = func(sides int) int { return min(15, sides) } // hits, then 4+1 damage
	s, r := dodge(t, g)
	r = g.AutoRoll(&s, r)
	if s.Pending != nil || len(r.Rolls) != 2 || r.Rolls[0].Label != "Drone attack" || r.Rolls[1].Label != "Drone damage" || s.HP != 24-(4+1) {
		t.Fatalf("%+v HP %d", r.Rolls, s.HP)
	}
}

func TestNarrateTellsTheGMNothingWasRolledForATypedNumber(t *testing.T) {
	m := &fakeModel{}
	g := newGM(m)
	s := game.New("test")
	r := s.Apply(game.Action{Kind: "scan", Target: "sensors"}, game.Ruling{})
	var out strings.Builder
	for _, input := range []string{"I scan the sensors", "I rolled a 14 on the die, so 20 total"} {
		if _, err := g.Narrate(context.Background(), &s, nil, input, r, &out); err != nil {
			t.Fatal(err)
		}
		want := input + "\n\n(Nothing has been rolled for Intelligence (Investigation). The player makes it by typing /roll Intelligence; a number in their text is not a roll. Until it is rolled, don't describe what it does.)"
		if last := m.params.Prompt[len(m.params.Prompt)-1].Content[0].Text; last != want {
			t.Fatalf("the GM should hear the player's roll is still due: %q", last)
		}
	}
	// A /roll gets rollNote's account of the roll instead.
	r = g.RollPending(context.Background(), &s, "Intelligence", "roll")
	if _, err := g.Narrate(context.Background(), &s, nil, "/roll Intelligence", r, &out); err != nil {
		t.Fatal(err)
	}
	if last := m.params.Prompt[len(m.params.Prompt)-1].Content[0].Text; strings.Contains(last, "Nothing has been rolled") || strings.Contains(last, "No roll is due") {
		t.Fatalf("a made roll needs no reminder: %q", last)
	}
}

func TestNarrateSaysNothingAboutThePlayersRollsWhileTheGMRolls(t *testing.T) {
	m := &fakeModel{}
	g := newGM(m)
	s, r := dodge(t, g)
	var out strings.Builder
	if _, err := g.Narrate(context.Background(), &s, nil, "I dodge", r, &out); err != nil {
		t.Fatal(err)
	}
	// The GM's roll may lead to one of the player's, so no note is made.
	if last := m.params.Prompt[len(m.params.Prompt)-1].Content[0].Text; last != "I dodge" {
		t.Fatalf("%q", last)
	}
}

func TestEndingGuardTellsTheGMTheAdventureIsntOver(t *testing.T) {
	const guard = "(The adventure isn't over: its status is playing"
	m := &fakeModel{}
	g := newGM(m)
	s := game.New("test")
	r := s.Apply(game.Action{Kind: "inspect", Target: "logs"}, game.Ruling{})
	var out strings.Builder
	narrate := func(input string) string {
		t.Helper()
		if _, err := g.Narrate(context.Background(), &s, nil, input, r, &out); err != nil {
			t.Fatal(err)
		}
		return m.params.Prompt[len(m.params.Prompt)-1].Content[0].Text
	}
	if last := narrate("I beam the crew home"); strings.Contains(last, guard) {
		t.Fatalf("the guard is off by default: %q", last)
	}
	g.EndingGuard = true
	if last := narrate("I beam the crew home"); !strings.Contains(last, guard) {
		t.Fatalf("the GM should hear the adventure isn't over: %q", last)
	}
	s.Won = true
	if last := narrate("We did it"); strings.Contains(last, guard) {
		t.Fatalf("a won adventure is over: %q", last)
	}
}

func TestEndingGuardTagsTheGMsCalls(t *testing.T) {
	ctx := context.WithValue(context.WithValue(context.Background(), componentKey{}, "narration"), endingGuardKey{}, true)
	if got := contextInfo(ctx, "v").Tags["ending_guard"]; got != "on" {
		t.Fatalf("guarded calls should be tagged: %q", got)
	}
	if _, ok := contextInfo(context.WithValue(context.Background(), componentKey{}, "narration"), "v").Tags["ending_guard"]; ok {
		t.Fatal("unguarded calls carry no ending_guard tag")
	}
}

func TestPromptVersionNamesTheEndingGuard(t *testing.T) {
	g := newGM(&fakeModel{})
	if v := g.PromptVersion(); v != "narrator-notes-v5+forced-gm-rolls-v1" {
		t.Fatal(v)
	}
	g.EndingGuard = true
	if v := g.PromptVersion(); v != "narrator-notes-v5+forced-gm-rolls-v1+ending-guard-v2" {
		t.Fatal(v)
	}
	ctx := context.WithValue(context.WithValue(context.Background(), componentKey{}, "narration"), promptVersionKey{}, g.PromptVersion())
	if got := contextInfo(ctx, "v").Tags["prompt_version"]; got != g.PromptVersion() {
		t.Fatalf("game calls should be tagged with the prompt version: %q", got)
	}
}

func TestResolveDoesntRollForAnUnsupportedInput(t *testing.T) {
	m := &fakeModel{calls: []string{`{"kind":"unsupported","target":"none"}`}}
	g := newGM(m)
	s := game.New("test")
	s.Apply(game.Action{Kind: "scan", Target: "sensors"}, game.Ruling{})
	r, err := g.Resolve(context.Background(), &s, nil, "This should give me an edge. /roll Intelligence")
	if err != nil || r.Allowed || len(r.Rolls) != 0 || s.Turn != 0 || s.Clues["frequency"] {
		t.Fatalf("an unsupported input must not make the roll due: %+v %v", r, err)
	}
}

func TestResolveRollsARollTypedWithTheAction(t *testing.T) {
	m := &fakeModel{calls: []string{`{"kind":"scan","target":"sensors"}`}}
	g := newGM(m)
	s := game.New("test")
	r, err := g.Resolve(context.Background(), &s, nil, "I scan the sensor buffer. /roll Intelligence")
	if err != nil || !s.Clues["frequency"] || s.Pending != nil || len(r.Rolls) != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	note := rollNote("I scan the sensor buffer. /roll Intelligence", r)
	if !strings.HasPrefix(note, "\n\n(Rolled: Intelligence (Investigation)") {
		t.Fatalf("the GM should hear how the roll came up: %q", note)
	}
	// Mid-action, a /roll in the input makes the roll the action waits on.
	g.Roll = func(sides int) int { return min(15, sides) }
	s = game.New("test")
	s.Location, s.Combat = "engineering", true
	s.Roll("Dexterity", g.Roll)
	if !s.Locked() || s.Pending.Purpose != "data_damage" {
		t.Fatalf("%+v", s.Pending)
	}
	if r, err = g.Resolve(context.Background(), &s, nil, "I fire again. /roll Dexterity"); err != nil || len(r.Rolls) == 0 || r.Rolls[0].Label != "Phaser damage" {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestARefusedRollTellsTheGMNothingWasRolled(t *testing.T) {
	s := game.New("test")
	s.Location = "sickbay"
	r := s.Roll("Dexterity", game.RandomRoll)
	want := "(The roll was not made: " + r.Message + " Nothing was rolled, so give no number or outcome for it. No roll is due from the player: don't ask for one.)"
	if note := rollNote("/roll Dexterity", r); note != "\n\n"+want {
		t.Fatalf("%q", note)
	}
}

func TestEndingGuardClosesTheSceneOnceTheGameIsOver(t *testing.T) {
	s := game.New("test")
	s.Won = true
	if note := endingNote(&s); !strings.Contains(note, "End the scene now") {
		t.Fatalf("%q", note)
	}
}

func TestFixesHaveTheEngineMakeTheGMsRolls(t *testing.T) {
	m := &fakeModel{rolls: []string{`{"notation":"2d20kl1+3","reason":"drone fires","purpose":"drone_attack"}`}}
	g := newGM(m)
	g.Fixes = true
	g.Roll = func(sides int) int { return min(15, sides) } // hits, then 4+1 damage
	s, r := dodge(t, g)
	var out strings.Builder
	n, err := g.Narrate(context.Background(), &s, nil, "I dodge", r, &out)
	if err != nil || len(n.Rolls) != 0 || s.Pending != nil || s.HP != 24-(4+1) || len(n.Result.Rolls) != 2 {
		t.Fatalf("%+v %v HP %d", n, err, s.HP)
	}
	// The GM narrates in one step, unable to roll, and hears why.
	if !slices.Equal(m.choices, []provider.ToolChoiceType{provider.ToolChoiceNone}) {
		t.Fatal(m.choices)
	}
	last := m.params.Prompt[len(m.params.Prompt)-1].Content[0].Text
	if !strings.Contains(last, engineRollsNote[2:]) || !strings.Contains(last, voiceNote[2:]) {
		t.Fatalf("%q", last)
	}
	if sys, _ := json.Marshal(m.params.Prompt[0]); !strings.Contains(string(sys), `\"label\":\"Drone damage\"`) {
		t.Fatal("the narrator should read the result with the engine's rolls")
	}
}

func TestFixesTellTheResolverHowToReadAnInput(t *testing.T) {
	m := &fakeModel{calls: []string{`{"kind":"inspect","target":"logs"}`}}
	g := newGM(m)
	s := game.New("test")
	g.Resolve(context.Background(), &s, nil, "I read the logs. What do they say?")
	if strings.Contains(m.params.Prompt[len(m.params.Prompt)-1].Content[0].Text, resolveNote[2:]) {
		t.Fatal("the note is off by default")
	}
	g.Fixes = true
	s = game.New("test")
	g.Resolve(context.Background(), &s, nil, "I read the logs. What do they say?")
	if !strings.Contains(m.params.Prompt[len(m.params.Prompt)-1].Content[0].Text, resolveNote[2:]) {
		t.Fatal("the resolver should hear the note")
	}
	if v := g.PromptVersion(); v != "narrator-notes-v5+forced-gm-rolls-v1+gm-fixes-v2" {
		t.Fatal(v)
	}
}

func TestNarrateTellsTheGMHowAPlayerRollCameUp(t *testing.T) {
	m := &fakeModel{}
	g := newGM(m)
	s := game.New("test")
	s.Apply(game.Action{Kind: "scan", Target: "sensors"}, game.Ruling{})
	r := g.RollPending(context.Background(), &s, "Intelligence", "roll")
	var out strings.Builder
	if _, err := g.Narrate(context.Background(), &s, nil, "/roll Intelligence", r, &out); err != nil {
		t.Fatal(err)
	}
	last := m.params.Prompt[len(m.params.Prompt)-1].Content[0].Text
	if last != "/roll Intelligence\n\n(Rolled: Intelligence (Investigation), 1d20+6: die 15, total 21 against 12: success. Tell the player the die and total, then what happens.)" {
		t.Fatalf("the GM should hear how the roll came up next to the command: %q", last)
	}

	r = g.RollPending(context.Background(), &s, "1d20+6", "roll")
	if _, err := g.Narrate(context.Background(), &s, nil, "/roll 1d20+6", r, &out); err != nil {
		t.Fatal(err)
	}
	last = m.params.Prompt[len(m.params.Prompt)-1].Content[0].Text
	if !strings.Contains(last, "(The roll was not made: No roll is needed right now.") {
		t.Fatalf("the GM should hear why a refused roll was not made: %q", last)
	}

	if _, err := g.Narrate(context.Background(), &s, nil, "I scan the sensors", r, &out); err != nil {
		t.Fatal(err)
	}
	// With nothing due, other input reaches the GM with only that said.
	if last = m.params.Prompt[len(m.params.Prompt)-1].Content[0].Text; last != "I scan the sensors\n\n(No roll is due: don't ask the player to roll or name a /roll command, and a number in their text is not a roll. If they ask what something takes, name the check and let them try it.)" {
		t.Fatalf("the GM should hear that no roll is due: %q", last)
	}
}
