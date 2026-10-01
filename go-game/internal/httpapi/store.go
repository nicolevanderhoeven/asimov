// Package httpapi exposes the game engine over HTTP, one isolated session
// per client, so concurrent callers (e.g. k6 virtual users) never interleave
// turns into the same game.State the way a single shared session would.
package httpapi

import (
	"context"
	"sync"
	"time"

	"github.com/grafana/ai-sdk/middleware/agentobservability"
	"github.com/grafana/ai-sdk/provider"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/game"
)

type session struct {
	mu         sync.Mutex
	state      game.State
	history    []provider.Message
	lastAccess time.Time
}

// SessionData is the mutable view of one session handed to a WithSession
// callback. State may be read and mutated directly through the pointer, as
// before; History should be reassigned (typically via gm.AppendTurn) rather
// than mutated in place — the Store copies whatever History holds when the
// callback returns back into the session for the next call.
type SessionData struct {
	State   *game.State
	History []provider.Message
}

// Store is an in-memory, per-session game.State registry, safe for
// concurrent use by multiple HTTP handlers. Sessions idle longer than ttl
// are reclaimed by Sweep. This is intentionally simple: a demo app has no
// need for persistence or a distributed store.
type Store struct {
	mu       sync.RWMutex
	sessions map[string]*session
	ttl      time.Duration
}

func NewStore(ttl time.Duration) *Store {
	return &Store{sessions: make(map[string]*session), ttl: ttl}
}

// Create starts a new session with a fresh game.State, keyed by its own
// ConversationID (which doubles as the session id, so telemetry keys on the
// same identifier whether the turn came from the REPL or the HTTP API).
func (st *Store) Create() (string, game.View) {
	id := agentobservability.NewGenerationID()
	s := &session{state: game.New(id), lastAccess: time.Now()}
	st.mu.Lock()
	st.sessions[id] = s
	st.mu.Unlock()
	return id, s.state.View()
}

// View reports the current View for id, or ok=false if the session is
// unknown or has expired.
func (st *Store) View(id string) (game.View, bool) {
	st.mu.RLock()
	s, ok := st.sessions[id]
	st.mu.RUnlock()
	if !ok {
		return game.View{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastAccess = time.Now()
	return s.state.View(), true
}

// WithSession locks the named session for the duration of fn, which may
// mutate the *game.State it's given and reassign History, and refreshes the
// session's last-access time. It reports whether the session existed.
// Callers must not retain the SessionData or its State pointer past fn's
// return.
func (st *Store) WithSession(id string, fn func(*SessionData)) bool {
	st.mu.RLock()
	s, ok := st.sessions[id]
	st.mu.RUnlock()
	if !ok {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastAccess = time.Now()
	data := &SessionData{State: &s.state, History: s.history}
	fn(data)
	s.history = data.History
	return true
}

// Sweep deletes sessions idle longer than the store's ttl, once per
// interval, until ctx is done. Run it in its own goroutine at server
// startup.
func (st *Store) Sweep(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-st.ttl)
			st.mu.Lock()
			for id, s := range st.sessions {
				s.mu.Lock()
				expired := s.lastAccess.Before(cutoff)
				s.mu.Unlock()
				if expired {
					delete(st.sessions, id)
				}
			}
			st.mu.Unlock()
		}
	}
}
