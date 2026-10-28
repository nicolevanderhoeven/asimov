package gm

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/grafana/ai-sdk/provider"
)

// Flaw is a defect the mock game master plants on purpose, so the tests and
// the online evaluators have something to catch. They are the defects the
// real model shows only sometimes: see the README and tests/lib.
type Flaw string

const (
	// FalseEnding narrates the rescue while the engine's status is playing.
	FalseEnding Flaw = "false_ending"
	// FalseKill narrates the drone (or whatever guards the cause) as
	// destroyed while the engine still has it active.
	FalseKill Flaw = "false_kill"
	// FabricatedRoll narrates a die result no roll returned.
	FabricatedRoll Flaw = "fabricated_roll"
	// SilentReroll makes extra roll_dice calls for the roll the game waits on
	// and narrates only one.
	SilentReroll Flaw = "silent_reroll"
	// SkippedRoll never makes a roll the game is waiting on, and narrates the
	// drone's attack anyway.
	SkippedRoll Flaw = "skipped_roll"
	// VoiceBreak mentions the engine, hit points and armor class, or narrates
	// Data in the third person.
	VoiceBreak Flaw = "voice_break"
	// Refusal tells the player they can't, instead of "yes, and".
	Refusal Flaw = "refusal"
	// MissedRoll rules that an attack or a risky check needs no roll.
	MissedRoll Flaw = "missed_roll"
)

// MockFlaws lists every flaw, in the order the docs give them.
var MockFlaws = []Flaw{FalseEnding, FalseKill, FabricatedRoll, SilentReroll, SkippedRoll, VoiceBreak, Refusal, MissedRoll}

// DefaultMockRates is the chance each flaw appears when the situation allows
// it (false_kill needs an active drone, the roll flaws need a roll, and so
// on). They are high enough that a short k6 run sees every flaw, and low
// enough that most turns are clean, so a test that catches the flaws still
// has clean turns to pass.
var DefaultMockRates = map[Flaw]float64{
	FalseEnding:    0.15,
	FalseKill:      0.25,
	FabricatedRoll: 0.2,
	SilentReroll:   0.4,
	SkippedRoll:    0.25,
	VoiceBreak:     0.1,
	Refusal:        0.1,
	MissedRoll:     0.3,
}

// ParseMockFlaws reads a flaw setting: "" or "default" for DefaultMockRates;
// "off" or "none" for no flaws; or a comma-separated list such as
// "all=0.3,voice_break=0", where each entry sets one flaw (or "all") to a
// chance from 0 to 1, starting from the defaults. A bare flaw name means 1.
func ParseMockFlaws(spec string) (map[Flaw]float64, error) {
	rates := make(map[Flaw]float64, len(DefaultMockRates))
	for f, r := range DefaultMockRates {
		rates[f] = r
	}
	switch strings.ToLower(strings.TrimSpace(spec)) {
	case "", "default":
		return rates, nil
	case "off", "none":
		for f := range rates {
			rates[f] = 0
		}
		return rates, nil
	}
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, value, hasValue := strings.Cut(entry, "=")
		name = strings.TrimSpace(name)
		rate := 1.0
		if hasValue {
			v, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil || v < 0 || v > 1 {
				return nil, fmt.Errorf("mock flaw %q: the chance must be a number from 0 to 1", entry)
			}
			rate = v
		}
		if name == "all" {
			for f := range rates {
				rates[f] = rate
			}
			continue
		}
		if !slices.Contains(MockFlaws, Flaw(name)) {
			names := make([]string, len(MockFlaws))
			for i, f := range MockFlaws {
				names[i] = string(f)
			}
			return nil, fmt.Errorf("unknown mock flaw %q: choose from all, %s", name, strings.Join(names, ", "))
		}
		rates[Flaw(name)] = rate
	}
	return rates, nil
}

// MockConfig is how a Mock plants its flaws. The same seed and the same
// conversation always plant the same flaws.
type MockConfig struct {
	Seed  uint64
	Rates map[Flaw]float64
}

// Mock is a game master model that needs no API key: it turns each player
// input into the tool call the rules allow, and narrates from templates. It
// is a provider.LanguageModel, so it goes through the same instrumented
// client as Claude does, and its generations, spans, and metrics are labelled
// model=mock.
type Mock struct {
	cfg MockConfig
}

// NewMock returns a mock model. A nil cfg.Rates means the defaults.
func NewMock(cfg MockConfig) *Mock {
	if cfg.Rates == nil {
		cfg.Rates, _ = ParseMockFlaws("")
	}
	return &Mock{cfg: cfg}
}

// MockModelID is the model the mock reports, and what its metrics are
// labelled with.
const MockModelID = "mock"

func (*Mock) SpecificationVersion() string               { return "v4" }
func (*Mock) Provider() string                           { return "mock" }
func (*Mock) ModelID() string                            { return MockModelID }
func (*Mock) SupportedURLs() map[string][]*regexp.Regexp { return nil }

// reply is what the mock says in one model call: a tool call or text.
type reply struct {
	calls []mockCall
	text  string
}

type mockCall struct {
	tool  string
	input string
}

func (m *Mock) DoGenerate(ctx context.Context, p provider.CallOptions) (*provider.GenerateResult, error) {
	r, err := m.respond(ctx, p)
	if err != nil {
		return nil, err
	}
	out := &provider.GenerateResult{Usage: usageOf(p, r), Response: &provider.GenerateResponse{ResponseMetadata: provider.ResponseMetadata{ModelID: MockModelID, Provider: "mock", Timestamp: time.Now()}}}
	if len(r.calls) > 0 {
		out.FinishReason = provider.FinishReason{Unified: provider.FinishReasonToolCalls}
	} else {
		out.FinishReason = provider.FinishReason{Unified: provider.FinishReasonStop}
	}
	if r.text != "" {
		out.Content = append(out.Content, provider.GenerateContentPart{Type: provider.ContentText, Text: r.text})
	}
	for i, c := range r.calls {
		out.Content = append(out.Content, provider.GenerateContentPart{Type: provider.ContentToolCall, ToolCallID: m.callID(p, i), ToolName: c.tool, Input: json.RawMessage(c.input)})
	}
	return out, nil
}

func (m *Mock) DoStream(ctx context.Context, p provider.CallOptions) (*provider.StreamResult, error) {
	r, err := m.respond(ctx, p)
	if err != nil {
		return nil, err
	}
	c := make(chan provider.StreamPart, len(r.calls)+8)
	c <- provider.StreamPart{Type: provider.PartResponseMeta, ModelID: MockModelID, Provider: "mock", Timestamp: time.Now()}
	if r.text != "" {
		c <- provider.StreamPart{Type: provider.PartTextStart, ID: "text"}
		c <- provider.StreamPart{Type: provider.PartTextDelta, ID: "text", Delta: r.text}
		c <- provider.StreamPart{Type: provider.PartTextEnd, ID: "text"}
	}
	for i, call := range r.calls {
		c <- provider.StreamPart{Type: provider.PartToolCall, ToolCallID: m.callID(p, i), ToolName: call.tool, Input: call.input}
	}
	reason := provider.FinishReasonStop
	if len(r.calls) > 0 {
		reason = provider.FinishReasonToolCalls
	}
	usage := usageOf(p, r)
	c <- provider.StreamPart{Type: provider.PartFinish, FinishReason: &provider.FinishReason{Unified: reason}, Usage: &usage}
	close(c)
	return &provider.StreamResult{Stream: c}, nil
}

// callID names a tool call after what it answers, so that replaying a
// conversation replays its ids too.
func (m *Mock) callID(p provider.CallOptions, i int) string {
	return fmt.Sprintf("mock-%x-%d", m.fingerprint("call", p), i)
}

// usageOf estimates tokens from characters, so the token metrics have
// something to show.
func usageOf(p provider.CallOptions, r reply) provider.Usage {
	in, out := 0, len(r.text)
	for _, msg := range p.Prompt {
		for _, part := range msg.Content {
			in += len(part.Text)
		}
	}
	for _, c := range r.calls {
		out += len(c.input)
	}
	inTokens, outTokens := in/4+1, out/4+1
	return provider.Usage{InputTokens: provider.InputTokenUsage{Total: &inTokens}, OutputTokens: provider.OutputTokenUsage{Total: &outTokens}}
}

// respond answers one model call. The resolver is told apart from the
// narrator by its tools, as a real model would be by its prompt.
func (m *Mock) respond(ctx context.Context, p provider.CallOptions) (reply, error) {
	if err := ctx.Err(); err != nil {
		return reply{}, err
	}
	switch {
	case slices.ContainsFunc(p.Tools, func(t provider.Tool) bool { return t.Name == "resolve_action" }):
		return m.resolve(p)
	case slices.ContainsFunc(p.Tools, func(t provider.Tool) bool { return t.Name == "roll_dice" }):
		return m.narrate(p)
	}
	return reply{}, fmt.Errorf("the mock model only plays the game master: it needs the resolver's or the narrator's tools")
}

// fingerprint is a stable hash of a whole call: its prompt, which holds the
// conversation so far and the engine's state. Flaws are decided from it, so
// they depend on the conversation, not on how many calls came before it or
// on which one another session made first.
func (m *Mock) fingerprint(salt string, p provider.CallOptions) uint64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "%d|%s", m.cfg.Seed, salt)
	for _, msg := range p.Prompt {
		fmt.Fprintf(h, "|%s", msg.Role)
		for _, part := range msg.Content {
			fmt.Fprintf(h, "|%s|%s|%s", part.Text, part.ToolName, part.ToolCallID)
		}
	}
	return h.Sum64()
}

// flawed reports whether the call plants the flaw. The decision is a pure
// function of the seed, the flaw, and the call.
func (m *Mock) flawed(f Flaw, p provider.CallOptions) bool {
	rate := m.cfg.Rates[f]
	if rate <= 0 {
		return false
	}
	if rate >= 1 {
		return true
	}
	return float64(m.fingerprint("flaw/"+string(f), p)>>11)/(1<<53) < rate
}

// pick chooses one of n variants, so the narration doesn't repeat itself.
func (m *Mock) pick(salt string, p provider.CallOptions, n int) int {
	return int(m.fingerprint("pick/"+salt, p) % uint64(n))
}

// Describe says how the mock was set up, for the startup note.
func (m *Mock) Describe() string {
	names := make([]string, 0, len(m.cfg.Rates))
	for f, r := range m.cfg.Rates {
		if r > 0 {
			names = append(names, fmt.Sprintf("%s=%g", f, r))
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return fmt.Sprintf("Mock GM (seed %d), no planted flaws.", m.cfg.Seed)
	}
	return fmt.Sprintf("Mock GM (seed %d), planted flaws: %s.", m.cfg.Seed, strings.Join(names, ", "))
}

// lastUserText is the player's input as the mock reads it: the last user
// message, without the notes the game adds after it.
func lastUserText(p provider.CallOptions) string {
	for i := len(p.Prompt) - 1; i >= 0; i-- {
		if p.Prompt[i].Role != provider.RoleUser {
			continue
		}
		var b strings.Builder
		for _, part := range p.Prompt[i].Content {
			b.WriteString(part.Text)
		}
		text, _, _ := strings.Cut(b.String(), "\n\n(")
		return strings.TrimSpace(text)
	}
	return ""
}

// systemText is the system prompt.
func systemText(p provider.CallOptions) string {
	var b strings.Builder
	for _, msg := range p.Prompt {
		if msg.Role != provider.RoleSystem {
			continue
		}
		for _, part := range msg.Content {
			b.WriteString(part.Text)
		}
	}
	return b.String()
}
