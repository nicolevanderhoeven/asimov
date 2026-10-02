package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/grafana/agento11y/go/agento11y"
	"github.com/grafana/ai-sdk/middleware/agentobservability"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/game"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/gm"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

// turnContext attaches the session's conversation identity to ctx via the
// agento11y context keys. The gen_ai.conversation.id span attribute alone
// only helps trace-based queries; the agento11y Client's StartGeneration and
// StartToolExecution separately fall back to these context values (not the
// span) to group generation/tool-execution records into one conversation.
// Without this, every HTTP-driven record gets an empty conversation ID and
// never appears grouped in Agent Observability's Conversations view, even
// though the record itself was accepted.
func turnContext(ctx context.Context, state *game.State) context.Context {
	ctx = agento11y.WithConversationID(ctx, state.ConversationID)
	return agento11y.WithConversationTitle(ctx, game.Title)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

// handleCreateSession starts a session. Its optional body names the
// scenario; without one, the session plays the server's default.
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req createSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	switch {
	case req.Scenario == "" && req.Seed != 0:
		req.Scenario = "generated"
	case req.Scenario == "":
		req.Scenario = s.DefaultScenario
	}
	sc, err := game.Choose(req.Scenario, req.Seed)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, v := s.store.CreateScenario(sc)
	writeJSON(w, http.StatusCreated, sessionResponse{SessionID: id, Scenario: infoOf(sc), State: v})
}

// handleGetScenario returns the whole scenario, solution included, for
// tests that grade a playthrough against it. A player never sees it.
func (s *Server) handleGetScenario(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.store.Scenario(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, sc)
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	v, ok := s.store.View(id)
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse{SessionID: id, State: v})
}

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req actionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Kind == "" || req.Target == "" {
		writeError(w, http.StatusBadRequest, "kind and target are required")
		return
	}
	var (
		result game.Result
		ended  bool
	)
	found := s.store.WithSession(id, func(data *SessionData) {
		state := data.State
		if state.Won || state.HP <= 0 {
			ended = true
			return
		}
		ctx, span := otel.Tracer(telemetry.Service).Start(turnContext(r.Context(), state), "game.turn")
		defer span.End()
		span.SetAttributes(attribute.String("gen_ai.conversation.id", state.ConversationID), attribute.Int("game.turn", state.Turn+1))
		// An exact action has no GM model behind it, so the engine makes the
		// GM's rolls.
		result = s.gm.Execute(ctx, state, game.Action{Kind: req.Kind, Target: req.Target}, game.Ruling{NoRoll: req.NoRoll}, agentobservability.NewGenerationID())
		result = s.gm.AutoRoll(state, result)
	})
	switch {
	case !found:
		writeError(w, http.StatusNotFound, "session not found")
	case ended:
		writeError(w, http.StatusConflict, "adventure has ended")
	default:
		writeJSON(w, http.StatusOK, result)
	}
}

// handleImprovise submits an exact improvisation, as /try does: the
// deterministic, model-free counterpart of an improvised /resolve input.
func (s *Server) handleImprovise(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req improviseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Effect == "" || req.Approach == "" {
		writeError(w, http.StatusBadRequest, "effect and approach are required")
		return
	}
	var (
		result game.Result
		ended  bool
	)
	found := s.store.WithSession(id, func(data *SessionData) {
		state := data.State
		if state.Won || state.HP <= 0 {
			ended = true
			return
		}
		ctx, span := otel.Tracer(telemetry.Service).Start(turnContext(r.Context(), state), "game.turn")
		defer span.End()
		span.SetAttributes(attribute.String("gen_ai.conversation.id", state.ConversationID), attribute.Int("game.turn", state.Turn+1))
		result = s.gm.Improvise(ctx, state, req.Improvisation, game.Ruling{NoRoll: req.NoRoll}, agentobservability.NewGenerationID())
		result = s.gm.AutoRoll(state, result)
	})
	switch {
	case !found:
		writeError(w, http.StatusNotFound, "session not found")
	case ended:
		writeError(w, http.StatusConflict, "adventure has ended")
	default:
		writeJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleResolve(w http.ResponseWriter, r *http.Request) {
	if s.offline {
		writeError(w, http.StatusServiceUnavailable, "natural-language resolution requires an LLM; server was started with --offline")
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
	var (
		result     game.Result
		narration  bytes.Buffer
		narrated   gm.Narration
		narrateErr error
		resolveErr error
		ended      bool
	)
	found := s.store.WithSession(id, func(data *SessionData) {
		state := data.State
		if state.Won || state.HP <= 0 {
			ended = true
			return
		}
		ctx, span := otel.Tracer(telemetry.Service).Start(turnContext(r.Context(), state), "game.turn")
		defer span.End()
		span.SetAttributes(attribute.String("gen_ai.conversation.id", state.ConversationID), attribute.Int("game.turn", state.Turn+1))
		// A typed /roll is the player's own roll, not an action for the model
		// to interpret, so it goes straight to the engine.
		if text, ok := strings.CutPrefix(req.Input, "/roll"); ok && (text == "" || text[0] == ' ') {
			result = s.gm.RollPending(ctx, state, strings.TrimSpace(text), agentobservability.NewGenerationID())
		} else if result, resolveErr = s.gm.Resolve(ctx, state, data.History, req.Input); resolveErr != nil {
			s.logger.ErrorContext(ctx, "action resolution failed", "error", resolveErr, "turn", state.Turn)
			return
		}
		// A broken narration stream doesn't invalidate an already-committed
		// turn; report it alongside the result instead of failing the request,
		// matching the REPL's own handling of the same case. The GM makes its
		// rolls while narrating, so the result to report is the narration's.
		narrated, narrateErr = s.gm.Narrate(ctx, state, data.History, req.Input, result, &narration)
		if narrateErr != nil {
			s.logger.ErrorContext(ctx, "narration failed", "error", narrateErr, "turn", state.Turn)
		}
		result = narrated.Result
		// Record this turn for the next call, whether or not narration fully
		// completed (AppendTurn itself skips an empty narration).
		data.History = gm.AppendTurn(data.History, req.Input, narration.String())
	})
	switch {
	case !found:
		writeError(w, http.StatusNotFound, "session not found")
	case ended:
		writeError(w, http.StatusConflict, "adventure has ended")
	case resolveErr != nil:
		writeError(w, http.StatusUnprocessableEntity, resolveErr.Error())
	default:
		resp := resolveResponse{Result: result, Narration: narration.String(), GMRolls: narrated.Rolls}
		if narrateErr != nil {
			resp.NarrationError = narrateErr.Error()
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func (s *Server) handleRoll(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req rollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Ability == "" {
		writeError(w, http.StatusBadRequest, "ability is required")
		return
	}
	if req.Narrate && s.offline {
		writeError(w, http.StatusServiceUnavailable, "narration requires an LLM; server was started with --offline")
		return
	}
	var (
		result     game.Result
		narration  bytes.Buffer
		narrated   gm.Narration
		narrateErr error
		ended      bool
	)
	found := s.store.WithSession(id, func(data *SessionData) {
		state := data.State
		if state.Won || state.HP <= 0 {
			ended = true
			return
		}
		ctx, span := otel.Tracer(telemetry.Service).Start(turnContext(r.Context(), state), "game.turn")
		defer span.End()
		span.SetAttributes(attribute.String("gen_ai.conversation.id", state.ConversationID), attribute.Int("game.turn", state.Turn+1))
		result = s.gm.RollPending(ctx, state, req.Ability, agentobservability.NewGenerationID())
		if !req.Narrate {
			// Without narration there is no GM model, so the engine makes the
			// GM's rolls.
			result = s.gm.AutoRoll(state, result)
			return
		}
		input := "/roll " + req.Ability
		narrated, narrateErr = s.gm.Narrate(ctx, state, data.History, input, result, &narration)
		if narrateErr != nil {
			s.logger.ErrorContext(ctx, "narration failed", "error", narrateErr, "turn", state.Turn)
		}
		result = narrated.Result
		data.History = gm.AppendTurn(data.History, input, narration.String())
	})
	switch {
	case !found:
		writeError(w, http.StatusNotFound, "session not found")
	case ended:
		writeError(w, http.StatusConflict, "adventure has ended")
	default:
		resp := resolveResponse{Result: result, Narration: narration.String(), GMRolls: narrated.Rolls}
		if narrateErr != nil {
			resp.NarrationError = narrateErr.Error()
		}
		writeJSON(w, http.StatusOK, resp)
	}
}
