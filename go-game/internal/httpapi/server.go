package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/nicolevanderhoeven/asimov/go-game/internal/gm"
)

// Server adapts gm.GM and a per-session Store to HTTP. offline mirrors
// cmd/enterprise's --offline flag: when true, gm.Model is nil and /resolve
// (and a narrated /roll), which need an LLM, are refused up front.
type Server struct {
	gm      *gm.GM
	store   *Store
	logger  *slog.Logger
	offline bool
}

func NewServer(g *gm.GM, store *Store, logger *slog.Logger, offline bool) *Server {
	return &Server{gm: g, store: store, logger: logger, offline: offline}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /session", s.handleCreateSession)
	mux.HandleFunc("GET /session/{id}", s.handleGetSession)
	mux.HandleFunc("POST /session/{id}/actions", s.handleAction)
	mux.HandleFunc("POST /session/{id}/resolve", s.handleResolve)
	mux.HandleFunc("POST /session/{id}/roll", s.handleRoll)
	mux.HandleFunc("POST /session/{id}/improvise", s.handleImprovise)
	mux.HandleFunc("POST /dm", s.handleCreateDM)
	mux.HandleFunc("POST /dm/{id}/turns", s.handleDMTurn)
	return mux
}
