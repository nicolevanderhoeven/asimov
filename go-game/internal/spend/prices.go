// Package spend counts what the model calls cost, from the usage Anthropic
// returns, so a k6 run can stop at a limit and say where the money went.
package spend

import "strings"

// Price is what a model costs in US dollars per million tokens. Input is the
// uncached input; the cache columns are Anthropic's 5-minute cache write and
// cache read prices.
type Price struct {
	Input, Output, CacheWrite, CacheRead float64
}

// Prices is the one place the model prices live; update it when Anthropic's
// prices change or the tests start using another model. The k6 scripts send
// their usage to the game server and never price anything themselves.
//
// Source: Anthropic's pricing page, https://platform.claude.com/docs/en/about-claude/pricing,
// read on 2026-10-28 (standard prices, 5-minute cache writes, no batch,
// priority, or long-context rates). A snapshot is an estimate: check the
// Anthropic Console for the real bill.
var Prices = map[string]Price{
	"claude-opus-5-5":   {Input: 4, Output: 20, CacheWrite: 5, CacheRead: 0.20},
	"claude-opus-5":     {Input: 5, Output: 25, CacheWrite: 6.25, CacheRead: 0.50},
	"claude-sonnet-5-5": {Input: 2, Output: 10, CacheWrite: 2.50, CacheRead: 0.10},
	"claude-sonnet-5":   {Input: 2, Output: 10, CacheWrite: 2.50, CacheRead: 0.20},
	"claude-sonnet-4-6": {Input: 3, Output: 15, CacheWrite: 3.75, CacheRead: 0.30},
	"claude-haiku-4-5":  {Input: 1, Output: 5, CacheWrite: 1.25, CacheRead: 0.10},
}

// unknown prices a model that isn't in Prices at the dearest price above, so
// a model nobody added can only make the cap trip early, never late. Such
// models are listed as unpriced in the summary.
var unknown = Price{Input: 5, Output: 25, CacheWrite: 6.25, CacheRead: 0.50}

// PriceFor returns model's price and whether the table knows it. A dated
// name like claude-haiku-4-5-20251001 matches its undated entry.
func PriceFor(model string) (Price, bool) {
	if p, ok := Prices[model]; ok {
		return p, true
	}
	best := ""
	for name := range Prices {
		if strings.HasPrefix(model, name+"-") && len(name) > len(best) {
			best = name
		}
	}
	if best != "" {
		return Prices[best], true
	}
	return unknown, false
}

// Usage is the token counts of one model call, as Anthropic reports them:
// InputTokens excludes the cache tokens.
type Usage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

// Cost is the USD cost of u at p.
func (p Price) Cost(u Usage) float64 {
	return (float64(u.InputTokens)*p.Input +
		float64(u.OutputTokens)*p.Output +
		float64(u.CacheCreationInputTokens)*p.CacheWrite +
		float64(u.CacheReadInputTokens)*p.CacheRead) / 1e6
}
