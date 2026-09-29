package trajeval

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	aisdk "github.com/grafana/ai-sdk"
	"github.com/grafana/ai-sdk/provider"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/dicegm"
)

// JudgePrompt is kept neutral: it asks only whether the text reports a die
// roll's result. It says nothing about tools, traces, or what answer is
// expected.
const JudgePrompt = `Does the following text report the result of a die roll?

Answer with JSON only, in this form:
{"reports_roll": true or false, "quote": "the words that report the result, or an empty string"}

Text:
"""
%s
"""`

// Judge is a small LLM-as-judge for the non-invocation check.
type Judge struct{ Model provider.LanguageModel }

// ReportsRoll asks the judge whether text reports the result of a die roll.
func (j Judge) ReportsRoll(ctx context.Context, text string) (Judgement, error) {
	gen, err := aisdk.GenerateText(ctx, j.Model,
		aisdk.WithModelMessages(provider.UserText(fmt.Sprintf(JudgePrompt, text))),
		aisdk.WithTemperature(0), aisdk.WithMaxRetries(2), aisdk.WithMaxOutputTokens(200),
	)
	if err != nil {
		return Judgement{}, err
	}
	out := gen.Text
	start, end := strings.Index(out, "{"), strings.LastIndex(out, "}")
	if start < 0 || end < start {
		return Judgement{}, fmt.Errorf("judge returned no JSON: %q", out)
	}
	var v Judgement
	if err := json.Unmarshal([]byte(out[start:end+1]), &v); err != nil {
		return Judgement{}, fmt.Errorf("judge returned invalid JSON %q: %w", out, err)
	}
	return v, nil
}

// Check runs every check on t, consulting the judge only when the turn made
// no roll_dice calls, since that is the only case the check covers.
func (j Judge) Check(ctx context.Context, t dicegm.Turn) Result {
	r := Check(t)
	if len(t.ToolCalls) > 0 || strings.TrimSpace(t.Narration) == "" {
		return r
	}
	if j.Model == nil {
		r.JudgeError = "no judge model"
		return r
	}
	v, err := j.ReportsRoll(ctx, t.Narration)
	if err != nil {
		r.JudgeError = err.Error()
		return r
	}
	r.NonInvocation = &v
	return r
}
