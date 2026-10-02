package httpapi

import (
	"github.com/nicolevanderhoeven/asimov/go-game/internal/game"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/gm"
)

type sessionResponse struct {
	SessionID string        `json:"session_id"`
	Scenario  *scenarioInfo `json:"scenario,omitempty"`
	State     game.View     `json:"state"`
}

// createSessionRequest optionally names the scenario: "classic" or
// "generated", with a seed to replay a generated one.
type createSessionRequest struct {
	Scenario string `json:"scenario,omitempty"`
	Seed     uint64 `json:"seed,omitempty"`
}

// scenarioInfo is what a player may know about the scenario: which one it
// is, and the opening they read before their first input.
type scenarioInfo struct {
	ID      string `json:"id"`
	Mode    string `json:"mode"`
	Variant string `json:"variant"`
	Seed    uint64 `json:"seed,omitempty"`
	Opening string `json:"opening"`
}

func infoOf(sc *game.Scenario) *scenarioInfo {
	return &scenarioInfo{ID: sc.ID, Mode: sc.Mode(), Variant: sc.Variant(), Seed: sc.Seed, Opening: sc.Opening}
}

// actionRequest is an exact action. NoRoll rules its check an automatic
// success, as the GM may.
type actionRequest struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	NoRoll bool   `json:"no_roll,omitempty"`
}

// improviseRequest is an exact improvisation, with the same optional ruling.
type improviseRequest struct {
	game.Improvisation
	NoRoll bool `json:"no_roll,omitempty"`
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

// resolveResponse is a narrated turn: the result after the GM's rolls, the
// narration, and every roll_dice call the GM made, in order.
type resolveResponse struct {
	Result         game.Result   `json:"result"`
	Narration      string        `json:"narration,omitempty"`
	NarrationError string        `json:"narration_error,omitempty"`
	GMRolls        []gm.RollCall `json:"gm_rolls,omitempty"`
}

type errorResponse struct {
	Error string `json:"error"`
}
