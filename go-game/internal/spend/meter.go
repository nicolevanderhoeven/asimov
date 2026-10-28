package spend

import (
	"context"

	"github.com/grafana/ai-sdk/provider"
)

// Meter records every call model makes in l under source, from the usage the
// model reports. It passes everything else through unchanged.
func Meter(model provider.LanguageModel, l *Ledger, source string) provider.LanguageModel {
	return &metered{LanguageModel: model, ledger: l, source: source}
}

type metered struct {
	provider.LanguageModel
	ledger *Ledger
	source string
}

// FromProvider converts the SDK's usage, which splits input into its cache
// and uncached parts, to Anthropic's.
func FromProvider(u provider.Usage) Usage {
	n := func(p *int) int64 {
		if p == nil {
			return 0
		}
		return int64(*p)
	}
	out := Usage{
		InputTokens:              n(u.InputTokens.NoCache),
		OutputTokens:             n(u.OutputTokens.Total),
		CacheCreationInputTokens: n(u.InputTokens.CacheWrite),
		CacheReadInputTokens:     n(u.InputTokens.CacheRead),
	}
	if u.InputTokens.NoCache == nil {
		// A provider that only gives a total: price it as uncached.
		out.InputTokens = n(u.InputTokens.Total) - out.CacheCreationInputTokens - out.CacheReadInputTokens
		if out.InputTokens < 0 {
			out.InputTokens = 0
		}
	}
	return out
}

func (m *metered) DoGenerate(ctx context.Context, params provider.CallOptions) (*provider.GenerateResult, error) {
	res, err := m.LanguageModel.DoGenerate(ctx, params)
	if res != nil {
		m.ledger.Record(m.source, m.ModelID(), FromProvider(res.Usage))
	}
	return res, err
}

func (m *metered) DoStream(ctx context.Context, params provider.CallOptions) (*provider.StreamResult, error) {
	res, err := m.LanguageModel.DoStream(ctx, params)
	if err != nil || res == nil {
		return res, err
	}
	in := res.Stream
	out := make(chan provider.StreamPart)
	go func() {
		defer close(out)
		for part := range in {
			if part.Type == provider.PartFinish && part.Usage != nil {
				m.ledger.Record(m.source, m.ModelID(), FromProvider(*part.Usage))
			}
			select {
			case out <- part:
			case <-ctx.Done():
				// The reader is gone; drain so the provider's goroutine ends.
				for range in {
				}
				return
			}
		}
	}()
	wrapped := *res
	wrapped.Stream = out
	return &wrapped, nil
}
