package httpapi

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/grafana/agento11y/go/agento11y"
	"github.com/grafana/ai-sdk/middleware/agentobservability"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/dicegm"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

// The /dm routes serve the free-form dicegm agent, not the game: the model
// owns the dice through roll_dice, and each turn's response is its whole
// trajectory, for tests/test-trajectory.js to grade.

const dmTitle = "Dice GM"

type dmSession struct {
	mu         sync.Mutex
	dm         *dicegm.DM
	turns      int
	lastAccess time.Time
}

// CreateDM starts a new dicegm conversation, keyed by a fresh ID that
// doubles as its conversation ID.
func (st *Store) CreateDM(dm *dicegm.DM) string {
	id := agentobservability.NewGenerationID()
	st.mu.Lock()
	st.dms[id] = &dmSession{dm: dm, lastAccess: time.Now()}
	st.mu.Unlock()
	return id
}

// WithDM locks the named DM session for fn, passing the next turn number,
// and reports whether the session existed.
func (st *Store) WithDM(id string, fn func(dm *dicegm.DM, turn int)) bool {
	st.mu.RLock()
	s, ok := st.dms[id]
	st.mu.RUnlock()
	if !ok {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastAccess = time.Now()
	s.turns++
	fn(s.dm, s.turns)
	return true
}

func (s *Server) handleCreateDM(w http.ResponseWriter, r *http.Request) {
	if s.offline {
		writeError(w, http.StatusServiceUnavailable, "the dice GM requires an LLM; server was started with --offline")
		return
	}
	id := s.store.CreateDM(&dicegm.DM{Model: s.gm.Model, Client: s.gm.Client, Roll: dicegm.RandomRoll})
	writeJSON(w, http.StatusCreated, dmSessionResponse{SessionID: id})
}

// handleDMTurn plays one player turn and returns its trajectory. A model
// failure mid-turn is reported in the turn's error field, alongside whatever
// the trajectory had reached, rather than as an HTTP error.
func (s *Server) handleDMTurn(w http.ResponseWriter, r *http.Request) {
	if s.offline {
		writeError(w, http.StatusServiceUnavailable, "the dice GM requires an LLM; server was started with --offline")
		return
	}
	id := r.PathValue("id")
	var req resolveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Input == "" {
		writeError(w, http.StatusBadRequest, "input is required")
		return
	}
	var turn dicegm.Turn
	found := s.store.WithDM(id, func(dm *dicegm.DM, n int) {
		ctx := agento11y.WithConversationTitle(agento11y.WithConversationID(r.Context(), id), dmTitle)
		ctx, span := otel.Tracer(telemetry.Service).Start(ctx, "dicegm.turn")
		defer span.End()
		turn = dm.Play(ctx, n, req.Input)
		span.SetAttributes(attribute.String("gen_ai.conversation.id", id), attribute.Int("game.turn", n), attribute.Int("dicegm.roll_dice_calls", len(turn.ToolCalls)))
	})
	if !found {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, turn)
}
