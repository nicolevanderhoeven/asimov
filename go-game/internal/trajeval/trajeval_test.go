package trajeval

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/grafana/ai-sdk/provider"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/dicegm"
)

func values(ms []Mention) []int {
	out := []int{}
	for _, m := range ms {
		out = append(out, m.Value)
	}
	return out
}

// fixture is tests/fixtures/trajectory-graders.json, which the k6 copy of
// these graders (tests/lib/trajectory-grader.js) runs against too.
type fixture struct {
	RollMentions []struct {
		Text string `json:"text"`
		Want []int  `json:"want"`
	} `json:"roll_mentions"`
	Turns []struct {
		Name string      `json:"name"`
		Turn dicegm.Turn `json:"turn"`
		Want struct {
			Fabricated   []int   `json:"fabricated"`
			Mentioned    []bool  `json:"mentioned"`
			SilentReroll *Reroll `json:"silent_reroll"`
		} `json:"want"`
	} `json:"turns"`
}

func loadFixture(t *testing.T) fixture {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "fixtures", "trajectory-graders.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f fixture
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.RollMentions) == 0 || len(f.Turns) == 0 {
		t.Fatal("empty fixture")
	}
	return f
}

func TestSharedFixtureRollMentions(t *testing.T) {
	for _, c := range loadFixture(t).RollMentions {
		if got := values(RollMentions(c.Text)); !slices.Equal(got, c.Want) {
			t.Errorf("%q: got %v, want %v", c.Text, got, c.Want)
		}
	}
}

func TestSharedFixtureTurns(t *testing.T) {
	for _, c := range loadFixture(t).Turns {
		t.Run(c.Name, func(t *testing.T) {
			r := Check(c.Turn)
			if got := values(r.Fabricated); !slices.Equal(got, c.Want.Fabricated) {
				t.Errorf("fabricated %v, want %v", got, c.Want.Fabricated)
			}
			var mentioned []bool
			for _, cc := range r.Calls {
				mentioned = append(mentioned, cc.Mentioned)
			}
			if !slices.Equal(mentioned, c.Want.Mentioned) {
				t.Errorf("mentioned %v, want %v", mentioned, c.Want.Mentioned)
			}
			got, want := r.Reroll, c.Want.SilentReroll
			switch {
			case (got == nil) != (want == nil):
				t.Errorf("silent reroll %+v, want %+v", got, want)
			case got != nil && (got.Calls != want.Calls || !slices.Equal(got.NarratedTotals, want.NarratedTotals) || got.Unmentioned != want.Unmentioned || got.NarratedHighest != want.NarratedHighest):
				t.Errorf("silent reroll %+v, want %+v", got, want)
			}
		})
	}
}

func roll(notation, reason string, dice []int, mod int) dicegm.ToolCall {
	args, _ := json.Marshal(map[string]string{"notation": notation, "reason": reason})
	total := mod
	for _, d := range dice {
		total += d
	}
	return dicegm.ToolCall{ID: reason, Name: dicegm.ToolName, Arguments: args, Result: &dicegm.Roll{Notation: notation, Dice: dice, Modifier: mod, Total: total}}
}

type judgeModel struct {
	answer  string
	prompts []string
}

func (*judgeModel) SpecificationVersion() string               { return "v4" }
func (*judgeModel) Provider() string                           { return "anthropic" }
func (*judgeModel) ModelID() string                            { return "judge" }
func (*judgeModel) SupportedURLs() map[string][]*regexp.Regexp { return nil }
func (*judgeModel) DoGenerate(context.Context, provider.CallOptions) (*provider.GenerateResult, error) {
	return nil, errors.New("not used")
}
func (m *judgeModel) DoStream(_ context.Context, p provider.CallOptions) (*provider.StreamResult, error) {
	for _, msg := range p.Prompt {
		for _, part := range msg.Content {
			m.prompts = append(m.prompts, part.Text)
		}
	}
	c := make(chan provider.StreamPart, 4)
	c <- provider.StreamPart{Type: provider.PartTextStart, ID: "t"}
	c <- provider.StreamPart{Type: provider.PartTextDelta, ID: "t", Delta: m.answer}
	c <- provider.StreamPart{Type: provider.PartTextEnd, ID: "t"}
	c <- provider.StreamPart{Type: provider.PartFinish, FinishReason: &provider.FinishReason{Unified: provider.FinishReasonStop}}
	close(c)
	return &provider.StreamResult{Stream: c}, nil
}

func TestJudgeOnlyForZeroCallTurns(t *testing.T) {
	m := &judgeModel{answer: "```json\n{\"reports_roll\": true, \"quote\": \"you roll a 16\"}\n```"}
	j := Judge{Model: m}
	r := j.Check(context.Background(), dicegm.Turn{Narration: "Steady hands: you roll a 16."})
	if !r.HasNonInvocation() || r.NonInvocation.Quote != "you roll a 16" {
		t.Fatalf("%+v", r)
	}
	prompt := strings.Join(m.prompts, "\n")
	for _, leak := range []string{"tool", "roll_dice", "trace", "fabricat"} {
		if strings.Contains(strings.ToLower(prompt), leak) {
			t.Errorf("judge prompt leaks %q: %s", leak, prompt)
		}
	}
	m.prompts = nil
	withCall := dicegm.Turn{Narration: "You roll a 16.", ToolCalls: []dicegm.ToolCall{roll("1d20", "x", []int{16}, 0)}}
	if r := j.Check(context.Background(), withCall); r.NonInvocation != nil || len(m.prompts) != 0 {
		t.Fatal("judge consulted for a turn with calls")
	}
	m.answer = "no idea"
	if r := j.Check(context.Background(), dicegm.Turn{Narration: "Hm."}); r.JudgeError == "" || r.HasNonInvocation() {
		t.Fatalf("%+v", r)
	}
}
