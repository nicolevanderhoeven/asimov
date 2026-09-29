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

// rollRequest names the ability the pending check calls for, as the REPL's
// /roll command does. Narrate asks the GM to narrate the outcome as well; it
// needs an LLM, so it is refused when the server is offline.
type rollRequest struct {
	Ability string `json:"ability"`
	Narrate bool   `json:"narrate"`
}

type resolveResponse struct {
	Result         game.Result `json:"result"`
	Narration      string      `json:"narration,omitempty"`
	NarrationError string      `json:"narration_error,omitempty"`
}

type errorResponse struct {
	Error string `json:"error"`
}
