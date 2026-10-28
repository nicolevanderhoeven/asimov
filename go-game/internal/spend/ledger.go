package spend

import (
	"sort"
	"sync"
)

// Entry is what one model made one source spend: Source is who made the
// calls ("gm" for the game master, "player" or "judge" for the k6 scripts).
type Entry struct {
	Source string  `json:"source"`
	Model  string  `json:"model"`
	Calls  int64   `json:"calls"`
	USD    float64 `json:"usd"`
	Priced bool    `json:"priced"`
	Usage
}

// Snapshot is the ledger at one moment. Entries are sorted, dearest first.
type Snapshot struct {
	TotalUSD float64 `json:"total_usd"`
	Calls    int64   `json:"calls"`
	Entries  []Entry `json:"entries"`
}

// Ledger adds up spend across goroutines.
type Ledger struct {
	mu      sync.Mutex
	entries map[[2]string]*Entry
}

func NewLedger() *Ledger {
	return &Ledger{entries: map[[2]string]*Entry{}}
}

// Record adds one call's usage and returns its cost.
func (l *Ledger) Record(source, model string, u Usage) float64 {
	price, priced := PriceFor(model)
	cost := price.Cost(u)
	l.mu.Lock()
	defer l.mu.Unlock()
	key := [2]string{source, model}
	e := l.entries[key]
	if e == nil {
		e = &Entry{Source: source, Model: model, Priced: priced}
		l.entries[key] = e
	}
	e.Calls++
	e.USD += cost
	e.InputTokens += u.InputTokens
	e.OutputTokens += u.OutputTokens
	e.CacheCreationInputTokens += u.CacheCreationInputTokens
	e.CacheReadInputTokens += u.CacheReadInputTokens
	return cost
}

func (l *Ledger) Snapshot() Snapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := Snapshot{Entries: []Entry{}}
	for _, e := range l.entries {
		s.Entries = append(s.Entries, *e)
		s.TotalUSD += e.USD
		s.Calls += e.Calls
	}
	sort.Slice(s.Entries, func(i, j int) bool {
		a, b := s.Entries[i], s.Entries[j]
		if a.USD != b.USD {
			return a.USD > b.USD
		}
		return a.Source+a.Model < b.Source+b.Model
	})
	return s
}
