package spend

import (
	"context"
	"math"
	"sync"
	"testing"

	"github.com/grafana/ai-sdk/provider"
)

func near(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCostCountsEveryTokenKind(t *testing.T) {
	p := Price{Input: 3, Output: 15, CacheWrite: 3.75, CacheRead: 0.30}
	got := p.Cost(Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000, CacheCreationInputTokens: 1_000_000, CacheReadInputTokens: 1_000_000})
	near(t, got, 3+15+3.75+0.30)
}

func TestPriceForMatchesDatedNamesAndFlagsUnknown(t *testing.T) {
	p, ok := PriceFor("claude-haiku-4-5-20251001")
	if !ok || p != Prices["claude-haiku-4-5"] {
		t.Errorf("dated haiku = %v, %v", p, ok)
	}
	// claude-sonnet-5-5 must not fall back to the shorter claude-sonnet-5.
	if p, _ := PriceFor("claude-sonnet-5-5"); p != Prices["claude-sonnet-5-5"] {
		t.Errorf("sonnet 5.5 = %v", p)
	}
	p, ok = PriceFor("claude-future-9")
	if ok {
		t.Error("an unknown model should be flagged unpriced")
	}
	for name, known := range Prices {
		if p.Output < known.Output || p.Input < known.Input {
			t.Errorf("the fallback price is cheaper than %s", name)
		}
	}
}

func TestLedgerSumsBySourceAndModelConcurrently(t *testing.T) {
	l := NewLedger()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.Record("gm", "claude-sonnet-4-6", Usage{InputTokens: 1_000_000})
			l.Record("judge", "claude-opus-5-5", Usage{OutputTokens: 1_000_000})
		}()
	}
	wg.Wait()
	s := l.Snapshot()
	near(t, s.TotalUSD, 50*3+50*20)
	if s.Calls != 100 || len(s.Entries) != 2 {
		t.Fatalf("calls %d, entries %d", s.Calls, len(s.Entries))
	}
	if s.Entries[0].Source != "judge" || s.Entries[0].Calls != 50 {
		t.Errorf("entries should be dearest first: %+v", s.Entries)
	}
}

type fakeModel struct {
	provider.LanguageModel
	usage provider.Usage
}

func (fakeModel) ModelID() string { return "claude-sonnet-4-6" }
func (f fakeModel) DoGenerate(context.Context, provider.CallOptions) (*provider.GenerateResult, error) {
	return &provider.GenerateResult{Usage: f.usage}, nil
}
func (f fakeModel) DoStream(context.Context, provider.CallOptions) (*provider.StreamResult, error) {
	c := make(chan provider.StreamPart, 3)
	c <- provider.StreamPart{Type: provider.PartTextDelta, Delta: "hi"}
	c <- provider.StreamPart{Type: provider.PartFinish, Usage: &f.usage}
	close(c)
	return &provider.StreamResult{Stream: c}, nil
}

func ip(n int) *int { return &n }

func TestMeterRecordsGenerateAndStreamUsage(t *testing.T) {
	usage := provider.Usage{
		InputTokens:  provider.InputTokenUsage{Total: ip(1_000_000), NoCache: ip(400_000), CacheRead: ip(500_000), CacheWrite: ip(100_000)},
		OutputTokens: provider.OutputTokenUsage{Total: ip(100_000)},
	}
	want := 0.4*3 + 0.5*0.30 + 0.1*3.75 + 0.1*15
	l := NewLedger()
	m := Meter(fakeModel{usage: usage}, l, "gm")
	if _, err := m.DoGenerate(context.Background(), provider.CallOptions{}); err != nil {
		t.Fatal(err)
	}
	near(t, l.Snapshot().TotalUSD, want)
	res, err := m.DoStream(context.Background(), provider.CallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	parts := 0
	for range res.Stream {
		parts++
	}
	if parts != 2 {
		t.Errorf("the stream lost parts: %d", parts)
	}
	s := l.Snapshot()
	near(t, s.TotalUSD, 2*want)
	if e := s.Entries[0]; e.Source != "gm" || e.Model != "claude-sonnet-4-6" || e.Calls != 2 || e.CacheReadInputTokens != 1_000_000 {
		t.Errorf("entry %+v", e)
	}
}

func TestFromProviderWithOnlyATotal(t *testing.T) {
	u := FromProvider(provider.Usage{InputTokens: provider.InputTokenUsage{Total: ip(10)}, OutputTokens: provider.OutputTokenUsage{Total: ip(5)}})
	if u.InputTokens != 10 || u.OutputTokens != 5 {
		t.Errorf("%+v", u)
	}
}
