package httpapi

import "github.com/nicolevanderhoeven/asimov/go-game/internal/game"

type sessionResponse struct {
	SessionID string    `json:"session_id"`
	State     game.View `json:"state"`
}

type actionRequest struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
}

type resolveRequest struct {
	Input string `json:"input"`
}

type resolveResponse struct {
	Result         game.Result `json:"result"`
	Narration      string      `json:"narration"`
	NarrationError string      `json:"narration_error,omitempty"`
}

type errorResponse struct {
	Error string `json:"error"`
}
