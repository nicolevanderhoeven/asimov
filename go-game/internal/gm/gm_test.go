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
		r.Content = append(r.Content, provider.GenerateContentPart{Type: provider.ContentToolCall, ToolCallID: string(rune('a' + i)), ToolName: "resolve_action", Input: json.RawMessage(c)})
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
			c <- provider.StreamPart{Type: provider.PartToolCall, ToolCallID: string(rune('a' + i)), ToolName: "resolve_action", Input: input}
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
	r, err := g.Resolve(context.Background(), &s, "Read the logs")
	if err != nil || !r.Allowed || !s.Clues["logs"] || s.Turn != 1 {
		t.Fatal(r, s, err)
	}
}
func TestModelCannotCreateActions(t *testing.T) {
	m := &fakeModel{calls: []string{`{"kind":"cast","target":"fireball"}`}}
	s := game.New("test")
	r, err := newGM(m).Resolve(context.Background(), &s, "Cast fireball")
	if err != nil || r.Allowed || s.Turn != 0 {
		t.Fatal(r, s, err)
	}
}
func TestMultipleToolsCannotAdvanceMultipleTurns(t *testing.T) {
	m := &fakeModel{calls: []string{`{"kind":"inspect","target":"logs"}`, `{"kind":"move","target":"sickbay"}`}}
	s := game.New("test")
	_, err := newGM(m).Resolve(context.Background(), &s, "Read logs")
	if err == nil || s.Turn != 0 {
		t.Fatal("multiple model actions should be rejected without changing state")
	}
}
func TestProviderFailureDoesNotChangeState(t *testing.T) {
	m := &fakeModel{fail: true}
	s := game.New("test")
	before := s.View().JSON()
	_, err := newGM(m).Resolve(context.Background(), &s, "Read logs")
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
	err := g.Narrate(context.Background(), "Read logs", r, &out)
	if err != nil || out.Len() == 0 || len(m.params.Tools) != 0 {
		t.Fatal(out.String(), err)
	}
}
