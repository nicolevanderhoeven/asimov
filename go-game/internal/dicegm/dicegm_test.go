package dicegm

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/grafana/agento11y/go/agento11y"
	"github.com/grafana/ai-sdk/middleware/agentobservability"
	"github.com/grafana/ai-sdk/provider"
)

// reply is one scripted model response: optional text, then tool calls given
// as raw JSON arguments to roll_dice.
type reply struct {
	text  string
	calls []string
}

type scriptedModel struct {
	replies  []reply
	requests []provider.CallOptions
	tags     []map[string]string
	ids      []string
	parents  [][]string
}

func (*scriptedModel) SpecificationVersion() string               { return "v4" }
func (*scriptedModel) Provider() string                           { return "anthropic" }
func (*scriptedModel) ModelID() string                            { return "test-model" }
func (*scriptedModel) SupportedURLs() map[string][]*regexp.Regexp { return nil }
func (m *scriptedModel) DoGenerate(context.Context, provider.CallOptions) (*provider.GenerateResult, error) {
	return nil, errors.New("not used")
}
func (m *scriptedModel) DoStream(ctx context.Context, p provider.CallOptions) (*provider.StreamResult, error) {
	m.requests = append(m.requests, p)
	m.tags = append(m.tags, agento11y.TagsFromContext(ctx))
	m.ids = append(m.ids, agentobservability.GenerationIDFromContext(ctx))
	m.parents = append(m.parents, agentobservability.ParentGenerationIDsFromContext(ctx))
	if len(m.replies) == 0 {
		return nil, errors.New("script exhausted")
	}
	r := m.replies[0]
	if len(m.replies) > 1 {
		m.replies = m.replies[1:]
	}
	c := make(chan provider.StreamPart, len(r.calls)+4)
	if r.text != "" {
		c <- provider.StreamPart{Type: provider.PartTextStart, ID: "t"}
		c <- provider.StreamPart{Type: provider.PartTextDelta, ID: "t", Delta: r.text}
		c <- provider.StreamPart{Type: provider.PartTextEnd, ID: "t"}
	}
	finish := provider.FinishReasonStop
	for i, input := range r.calls {
		c <- provider.StreamPart{Type: provider.PartToolCall, ToolCallID: "call" + string(rune('a'+i)) + string(rune('0'+len(m.requests))), ToolName: ToolName, Input: input}
		finish = provider.FinishReasonToolCalls
	}
	c <- provider.StreamPart{Type: provider.PartFinish, FinishReason: &provider.FinishReason{Unified: finish}}
	close(c)
	return &provider.StreamResult{Stream: c}, nil
}

func fixed(values ...int) Roller {
	i := 0
	return func(int) int {
		v := values[i%len(values)]
		i++
		return v
	}
}

func countParts(msgs []provider.Message, typ provider.ContentPartType) int {
	n := 0
	for _, m := range msgs {
		for _, p := range m.Content {
			if p.Type == typ {
				n++
			}
		}
	}
	return n
}

func TestRollNotation(t *testing.T) {
	r, err := RollNotation("2d6+3", fixed(4, 5))
	if err != nil || r.Total != 12 || len(r.Dice) != 2 || r.Modifier != 3 {
		t.Fatal(r, err)
	}
	if r, err := RollNotation("d20-1", fixed(1)); err != nil || r.Total != 0 {
		t.Fatal(r, err)
	}
	for _, bad := range []string{"", "d", "1d1", "0d6", "banana", "1d20+"} {
		if _, err := RollNotation(bad, fixed(1)); err == nil {
			t.Error("accepted", bad)
		}
	}
}

func TestRandomRollInRange(t *testing.T) {
	for range 1000 {
		if n := RandomRoll(20); n < 1 || n > 20 {
			t.Fatal(n)
		}
	}
}

// Every call is executed and kept, in order, including extra rolls in one
// response and rolls the final narration never mentions.
func TestPlayRecordsEveryCallAndLoops(t *testing.T) {
	m := &scriptedModel{replies: []reply{
		{text: "Let me see.", calls: []string{`{"notation":"1d20","reason":"lockpicking"}`, `{"notation":"1d20","reason":"lockpicking again"}`}},
		{calls: []string{`{"notation":"1d20+4","reason":"one more"}`}},
		{text: "The lock clicks open on a 19."},
	}}
	d := &DM{Model: m, Roll: fixed(7, 12, 15)}
	turn := d.Play(context.Background(), 1, "I pick the lock")
	if turn.Error != "" || turn.HitCap || len(turn.ToolCalls) != 3 || len(turn.Steps) != 3 {
		t.Fatalf("%+v", turn)
	}
	for i, want := range []int{7, 12, 19} {
		if got := turn.ToolCalls[i].Result.Total; got != want {
			t.Errorf("call %d total %d, want %d", i, got, want)
		}
	}
	if turn.ToolCalls[2].Iteration != 2 {
		t.Error("iteration not recorded", turn.ToolCalls[2])
	}
	if turn.Narration != "Let me see.\n\nThe lock clicks open on a 19." {
		t.Errorf("narration %q", turn.Narration)
	}
	// The narration is left exactly as the model wrote it.
	if last := m.requests[2].Prompt; countParts(last, provider.ContentPartTypeToolResult) != 3 || countParts(last, provider.ContentPartTypeToolCall) != 3 {
		t.Error("third request missing tool calls/results")
	}
	if len(m.requests[0].Tools) != 1 || m.requests[0].Tools[0].Name != ToolName {
		t.Error("roll_dice not offered", m.requests[0].Tools)
	}
	if m.requests[0].ToolChoice != nil && m.requests[0].ToolChoice.Type != provider.ToolChoiceAuto {
		t.Error("tool use must not be forced", m.requests[0].ToolChoice)
	}
	// The next turn sees the whole previous trajectory.
	d.Play(context.Background(), 2, "I go in")
	if countParts(m.requests[3].Prompt, provider.ContentPartTypeToolResult) != 3 {
		t.Error("history lost the tool results")
	}
}

func TestPlayWithoutToolCallRollsNothing(t *testing.T) {
	m := &scriptedModel{replies: []reply{{text: "You roll a 17 and the door opens."}}}
	calls := 0
	d := &DM{Model: m, Roll: func(int) int { calls++; return 1 }}
	turn := d.Play(context.Background(), 1, "I pick the lock")
	if len(turn.ToolCalls) != 0 || calls != 0 || len(m.requests) != 1 || !strings.Contains(turn.Narration, "17") {
		t.Fatalf("%+v", turn)
	}
}

func TestPlayRecordsBadNotationAndContinues(t *testing.T) {
	m := &scriptedModel{replies: []reply{{calls: []string{`{"notation":"a lot","reason":"x"}`}}, {text: "Done."}}}
	turn := (&DM{Model: m, Roll: fixed(1)}).Play(context.Background(), 1, "go")
	if len(turn.ToolCalls) != 1 || turn.ToolCalls[0].Error == "" || turn.ToolCalls[0].Result != nil || turn.Narration != "Done." {
		t.Fatalf("%+v", turn)
	}
}

func TestPlayStopsAtIterationCap(t *testing.T) {
	m := &scriptedModel{replies: []reply{{calls: []string{`{"notation":"1d6","reason":"again"}`}}}}
	d := &DM{Model: m, Roll: fixed(3)}
	turn := d.Play(context.Background(), 1, "go")
	if !turn.HitCap || len(m.requests) != MaxIterations || len(turn.ToolCalls) != MaxIterations {
		t.Fatalf("requests %d, %+v", len(m.requests), turn)
	}
	if d.history != nil {
		t.Error("an unfinished turn should not enter the history")
	}
}

func TestPlayTagsAndChainsEveryGeneration(t *testing.T) {
	m := &scriptedModel{replies: []reply{{calls: []string{`{"notation":"1d20","reason":"x"}`}}, {text: "Done."}}}
	turn := (&DM{Model: m, Roll: fixed(1)}).Play(context.Background(), 3, "go")
	if len(m.tags) != 2 {
		t.Fatal(m.tags)
	}
	// Each call is recorded under the ID the trace reports, chained to the
	// call before it.
	if m.ids[0] == "" || m.ids[0] == m.ids[1] || turn.Steps[0].GenerationID != m.ids[0] || turn.Steps[1].GenerationID != m.ids[1] {
		t.Fatalf("ids %v, steps %+v", m.ids, turn.Steps)
	}
	if len(m.parents[0]) != 0 || len(m.parents[1]) != 1 || m.parents[1][0] != m.ids[0] {
		t.Fatalf("parents %v", m.parents)
	}
	for _, tags := range m.tags {
		if tags["component"] != Component || tags["turn"] != "3" || tags["scenario"] != "dice-gm" {
			t.Errorf("tags %v", tags)
		}
	}
}
