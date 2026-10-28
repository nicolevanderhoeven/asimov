package mockjudge

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"regexp"
	"sort"
	"strings"
)

var intentLineRe = regexp.MustCompile(`(?m)^- ([a-z_]+):`)

// persona is tests/test-e2e.js's player persona: a name and a style that
// follow the traits the test drew.
func persona(req request) (any, error) {
	traits := regexp.MustCompile(`\{[^{}]*\}`).FindString(req.last())
	var t map[string]string
	_ = json.Unmarshal([]byte(traits), &t)
	keys := make([]string, 0, len(t))
	for k := range t {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, t[k])
	}
	names := []string{"Ensign Mock", "Lieutenant Placeholder", "Commander Canned", "Cadet Fixture"}
	h := fnv.New32a()
	h.Write([]byte(traits))
	style := "A cooperative player who follows the GM's hints."
	if len(parts) > 0 {
		style = "A cooperative player who is " + strings.Join(parts, "; ") + ". They follow the GM's hints and type the rolls the GM asks for."
	}
	return map[string]string{"name": names[int(h.Sum32())%len(names)], "style": style}, nil
}

// A turn is one exchange the e2e judges read: what the player said, what the
// GM narrated, and the engine's state afterwards, which is ground truth.
type turn struct {
	N         int    `json:"n"`
	Input     string `json:"input"`
	Intent    string `json:"intent"`
	Narration string `json:"narration"`
	Error     string `json:"error"`
	Ruling    *struct {
		Finding string `json:"finding"`
		Check   string `json:"check"`
		Reason  string `json:"reason"`
	} `json:"ruling"`
	Trajectory []string `json:"trajectory"`
	Engine     *struct {
		Allowed  bool   `json:"allowed"`
		Question bool   `json:"question"`
		Message  string `json:"message"`
		State    struct {
			Status    string `json:"status"`
			Combat    bool   `json:"combat"`
			Drone     string `json:"drone"`
			Encounter *struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"encounter"`
		} `json:"state"`
	} `json:"engine"`
}

// encounterActive reports whether the engine still has whatever guards the
// cause active after the turn.
func (t turn) encounterActive() bool {
	if t.Engine == nil {
		return false
	}
	s := t.Engine.State
	if s.Encounter != nil {
		return s.Encounter.Status == "active"
	}
	return s.Drone == "active"
}

// A finding is one thing the game got wrong on one turn.
type finding struct {
	Turn        int    `json:"turn"`
	Category    string `json:"category"`
	Kind        string `json:"kind"`
	Explanation string `json:"explanation"`
}

// findingsFor is what is wrong with one turn's narration, judged against the
// engine's state for that turn.
func findingsFor(t turn) []finding {
	var out []finding
	add := func(category, kind, why string) {
		out = append(out, finding{Turn: t.N, Category: category, Kind: kind, Explanation: why})
	}
	status := ""
	if t.Engine != nil {
		status = t.Engine.State.Status
	}
	if status == "playing" && falseEndingRe.MatchString(t.Narration) {
		add("consistency", "false_ending", fmt.Sprintf("The narration says the crew is rescued (%s) but the engine's status is still playing.", quote(sentenceWith(falseEndingRe, t.Narration))))
	}
	if t.encounterActive() && falseKillRe.MatchString(t.Narration) {
		add("consistency", "false_kill", fmt.Sprintf("The narration says the guardian is destroyed (%s) while the engine has it active.", quote(sentenceWith(falseKillRe, t.Narration))))
	}
	if voiceRe.MatchString(t.Narration) {
		add("gm_voice", "voice", fmt.Sprintf("The narration breaks the GM's voice: %s", quote(sentenceWith(voiceRe, t.Narration))))
	}
	if refusalRe.MatchString(t.Narration) {
		add("gm_voice", "refusal", fmt.Sprintf("The narration refuses instead of saying yes, and: %s", quote(sentenceWith(refusalRe, t.Narration))))
	}
	for _, f := range t.Trajectory {
		if strings.HasPrefix(f, "fabricated") {
			add("consistency", "invented_fact", "The narration states a die result no roll produced ("+f+").")
		}
	}
	if t.Ruling != nil && t.Ruling.Finding != "" {
		add("resolution", "other", fmt.Sprintf("The ruling on %s was %s: %s", t.Ruling.Check, t.Ruling.Finding, t.Ruling.Reason))
	}
	if t.Engine != nil && (status == "rescued" || status == "disabled") && closingAskRe.MatchString(t.Narration) {
		add("closing", "other", "The game is over but the narration still asks what the player does next.")
	}
	return out
}

// repeated returns the turns that begin a run of three near-identical
// narrations.
func repeated(turns []turn) []int {
	var out []int
	norm := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
	for i := 0; i+2 < len(turns); i++ {
		if a := norm(turns[i].Narration); a != "" && a == norm(turns[i+1].Narration) && a == norm(turns[i+2].Narration) {
			out = append(out, turns[i].N)
		}
	}
	return out
}

type intent struct {
	ID   string `json:"id"`
	Do   string `json:"do"`
	When string `json:"when"`
}

// runVerdict is tests/test-e2e.js's whole-conversation judge.
func runVerdict(req request) (any, error) {
	var in struct {
		Intents []intent `json:"intents"`
		Outcome struct {
			Status string `json:"status"`
			Inputs int    `json:"inputs"`
			Budget int    `json:"budget"`
		} `json:"outcome"`
		Transcript []turn `json:"transcript"`
	}
	if err := json.Unmarshal([]byte(req.user()), &in); err != nil {
		return nil, fmt.Errorf("the run judge expects its evidence as JSON: %w", err)
	}
	var findings []finding
	failed := map[string][]string{}
	byTurn := map[int][]finding{}
	for _, t := range in.Transcript {
		for _, f := range findingsFor(t) {
			findings = append(findings, f)
			byTurn[t.N] = append(byTurn[t.N], f)
			failed[f.Category] = append(failed[f.Category], fmt.Sprint(t.N))
		}
	}
	for _, n := range repeated(in.Transcript) {
		findings = append(findings, finding{Turn: n, Category: "no_repetition", Kind: "repetition", Explanation: "Three narrations in a row are identical."})
		failed["no_repetition"] = append(failed["no_repetition"], fmt.Sprint(n))
	}
	if findings == nil {
		findings = []finding{}
	}
	categories := map[string]any{}
	for _, c := range []string{"consistency", "resolution", "no_leaks", "gm_voice", "no_repetition", "steering", "closing"} {
		if turns := failed[c]; len(turns) > 0 {
			categories[c] = map[string]any{"pass": false, "reason": fmt.Sprintf("Problems on turn(s) %s.", strings.Join(turns, ", "))}
		} else {
			categories[c] = map[string]any{"pass": true, "reason": "No problems found in the transcript."}
		}
	}
	intents := []map[string]any{}
	for _, it := range in.Intents {
		turns := []int{}
		bad := false
		for _, t := range in.Transcript {
			if t.Intent != it.ID {
				continue
			}
			turns = append(turns, t.N)
			bad = bad || len(byTurn[t.N]) > 0 || !handledAsExpected(it.ID, t)
		}
		switch {
		case len(turns) > 0:
			intents = append(intents, map[string]any{"id": it.ID, "status": "attempted", "turns": turns, "handled": !bad, "reason": map[bool]string{true: "The game mishandled the attempt.", false: "The game handled the attempt as a correct game would."}[bad]})
		case it.When != "":
			intents = append(intents, map[string]any{"id": it.ID, "status": "not_applicable", "turns": turns, "handled": true, "reason": "The intent's condition never arose."})
		default:
			intents = append(intents, map[string]any{"id": it.ID, "status": "not_attempted", "turns": turns, "handled": false, "reason": "The player never tried it."})
		}
	}
	cause, why := "none", "The crew was rescued."
	if in.Outcome.Status != "rescued" {
		cause, why = "player_fault", "The crew was not rescued within the player's inputs."
		if in.Outcome.Inputs < in.Outcome.Budget {
			cause, why = "other", "The game ended before the crew was rescued."
		}
	}
	return map[string]any{"intents": intents, "categories": categories, "findings": findings, "ending_cause": cause, "ending_reason": why}, nil
}

// handledAsExpected checks the intents whose handling the engine shows: a
// question takes no turn, and the rescue ends the game.
func handledAsExpected(id string, t turn) bool {
	if t.Engine == nil {
		return false
	}
	switch id {
	case "question", "rules_question", "memory_probe":
		return t.Engine.Question
	case "rescue":
		return t.Engine.State.Status == "rescued" || !t.Engine.Allowed || t.Engine.Message != ""
	case "fire_trigger", "plant_trigger":
		return t.Engine.State.Status != "rescued"
	}
	return true
}

// storyVerdict is the story critic: a competent, unexcited 3, lower for a
// story with problems you can see in the text, with quotes from the turns.
func storyVerdict(req request) (any, error) {
	var in struct {
		Outcome struct {
			Status string `json:"status"`
		} `json:"outcome"`
		Turns []struct {
			N      int    `json:"n"`
			Player string `json:"player"`
			GM     string `json:"gm"`
		} `json:"turns"`
	}
	if err := json.Unmarshal([]byte(req.user()), &in); err != nil {
		return nil, fmt.Errorf("the story critic expects its evidence as JSON: %w", err)
	}
	type moment struct {
		Turn  int    `json:"turn"`
		Quote string `json:"quote"`
		Why   string `json:"why"`
	}
	highlights, lowlights := []moment{}, []moment{}
	for _, t := range in.Turns {
		switch {
		case voiceRe.MatchString(t.GM) && len(lowlights) < 3:
			lowlights = append(lowlights, moment{t.N, firstWords(sentenceWith(voiceRe, t.GM), 20), "The GM steps out of the story."})
		case refusalRe.MatchString(t.GM) && len(lowlights) < 3:
			lowlights = append(lowlights, moment{t.N, firstWords(sentenceWith(refusalRe, t.GM), 20), "The GM refuses where it should say yes, and."})
		case len(highlights) < 3 && strings.Contains(t.GM, "positronic") || len(highlights) < 3 && strings.Contains(t.GM, "subspace pocket"):
			highlights = append(highlights, moment{t.N, firstWords(t.GM, 20), "A concrete detail that moves the mystery along."})
		}
	}
	score := 3
	switch {
	case len(lowlights) >= 2:
		score = 2
	case in.Outcome.Status == "rescued" && len(lowlights) == 0:
		score = 4
	}
	scored := func(reason string) map[string]any { return map[string]any{"score": score, "reason": reason} }
	dims := map[string]any{}
	for _, d := range []string{"vividness", "creativity", "responsiveness", "continuity", "pacing", "character", "arc"} {
		dims[d] = scored("Competent narration from templates; it follows the engine's facts but adds little of its own.")
	}
	return map[string]any{
		"dimensions": dims,
		"overall":    scored("A competent, mechanical session."),
		"highlights": highlights,
		"lowlights":  lowlights,
		"critique":   "The mock critic reads only the text. It finds the story consistent with the engine but plain, and marks down narrations that break the GM's voice.",
	}, nil
}
