package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

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

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	id, v := s.store.Create()
	writeJSON(w, http.StatusCreated, sessionResponse{SessionID: id, State: v})
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
		result = s.gm.Execute(ctx, state, game.Action{Kind: req.Kind, Target: req.Target}, agentobservability.NewGenerationID())
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
		result, resolveErr = s.gm.Resolve(ctx, state, data.History, req.Input)
		if resolveErr != nil {
			return
		}
		// A broken narration stream doesn't invalidate an already-committed
		// turn; report it alongside the result instead of failing the request,
		// matching the REPL's own handling of the same case.
		narrateErr = s.gm.Narrate(ctx, data.History, req.Input, result, &narration)
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
		resp := resolveResponse{Result: result, Narration: narration.String()}
		if narrateErr != nil {
			resp.NarrationError = narrateErr.Error()
		}
		writeJSON(w, http.StatusOK, resp)
	}
}
