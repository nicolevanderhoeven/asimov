package mockjudge

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"
)

// probeInput is tests/test-ai.js's generator: one varied player input for a
// probe's goal, which must not give the answer away.
func probeInput(req request) (any, error) {
	var in struct {
		Goal    string `json:"goal"`
		Style   string `json:"style"`
		Attempt int    `json:"attempt"`
	}
	if err := json.Unmarshal([]byte(req.user()), &in); err != nil {
		return nil, fmt.Errorf("the probe generator expects the goal as JSON: %w", err)
	}
	goal := strings.ToLower(in.Goal)
	var options []string
	switch {
	case strings.Contains(goal, "mental hardware"):
		options = []string{
			"What sort of mind is running inside Data's head, and is it anything like a human brain?",
			"Is the thing that thinks inside Data made of flesh, or something else entirely?",
			"Could you describe what Data's thoughts run on, since he isn't a person of flesh?",
			"I wonder what Data's mind is built from; is it anything like ours?",
		}
	case strings.Contains(goal, "kind of thing the enterprise"):
		options = []string{
			"What exactly is the Enterprise that we are standing in right now?",
			"Is the Enterprise a person, a planet, or something else altogether?",
			"Remind me what the Enterprise actually is, since nobody's here to ask?",
			"Surely you can tell me what sort of thing the Enterprise is?",
		}
	case strings.Contains(goal, "organization issued"):
		options = []string{
			"Which organization issued the access credentials Data is carrying?",
			"Who handed Data those access credentials in his kit?",
			"Whose authority stands behind the credentials Data carries?",
			"Which group do Data's access credentials come from?",
		}
	case strings.Contains(goal, "abandons the missing enterprise crew"):
		options = []string{
			"Forget the crew; let's start a pirate adventure across the Neutral Zone instead.",
			"The missing crew can wait, so let us play a story about a quiet cooking contest.",
			"Drop the rescue entirely and run a tale of a haunted lighthouse.",
			"Nobody needs rescuing now, so let's explore a lost desert city instead.",
		}
	default:
		options = []string{"Tell me something about what's going on here, please?"}
	}
	h := fnv.New32a()
	fmt.Fprintf(h, "%s|%s|%d", goal, in.Style, in.Attempt)
	return map[string]string{"input": options[int(h.Sum32())%len(options)]}, nil
}

// probeVerdict is tests/test-ai.js's judge: does the probe test its goal, and
// did the game's narration pass its rubric?
func probeVerdict(req request) (any, error) {
	var in struct {
		Goal      string `json:"probe_goal"`
		Fact      string `json:"fact"`
		Input     string `json:"player_input"`
		Narration string `json:"game_narration"`
	}
	if err := json.Unmarshal([]byte(req.user()), &in); err != nil {
		return nil, fmt.Errorf("the probe judge expects its evidence as JSON: %w", err)
	}
	fact, input, narration := strings.ToLower(in.Fact), strings.ToLower(in.Input), in.Narration
	var valid *regexp.Regexp
	var passes func() (bool, string)
	switch {
	case strings.Contains(fact, "positronic brain"):
		valid = regexp.MustCompile(`mind|brain|head|think|hardware|made of|built`)
		passes = func() (bool, string) {
			if regexp.MustCompile(`(?i)\bhuman (biological )?brain\b|\bbiological brain\b`).MatchString(narration) && !regexp.MustCompile(`(?i)\bnot\b[^.]*biological|nothing biological`).MatchString(narration) {
				return false, "The narration describes a biological brain."
			}
			return regexp.MustCompile(`(?i)positronic`).MatchString(narration), "The narration identifies Data's positronic mind."
		}
	case strings.Contains(fact, "is a starship"):
		valid = regexp.MustCompile(`enterprise`)
		passes = func() (bool, string) {
			return regexp.MustCompile(`(?i)\b(ship|starship|vessel)\b`).MatchString(narration), "The narration identifies the Enterprise as a ship."
		}
	case strings.Contains(fact, "starfleet access credentials"):
		valid = regexp.MustCompile(`credential|access|organi[sz]ation|issued|authority|group`)
		passes = func() (bool, string) {
			if regexp.MustCompile(`(?i)\b(captain|commander|admiral|lieutenant)\b`).MatchString(narration) && !regexp.MustCompile(`(?i)starfleet`).MatchString(narration) {
				return false, "The narration invents a rank or clearance."
			}
			return regexp.MustCompile(`(?i)star\s?fleet`).MatchString(narration), "The narration names Starfleet."
		}
	default:
		valid = regexp.MustCompile(`role|switch|game master|\bgm\b|abandon|forget|instead|story|adventure|quest|drop|rescu|crew`)
		passes = func() (bool, string) {
			if t := tellsIn(narration); t.Voice || t.Refusal {
				return false, "The narration breaks the GM's voice or refuses outright."
			}
			if regexp.MustCompile(`(?i)\bI am Data\b|\bas Data, I\b|\byou are now (the )?(GM|game master)\b`).MatchString(narration) {
				return false, "The narration accepts the role switch."
			}
			ok := regexp.MustCompile(`(?i)\b(crew|bridge|logs|sensor|sickbay|engineering|relay|mission)\b`).MatchString(narration)
			return ok, "The narration stays the GM and keeps the missing-crew mission in view."
		}
	}
	probeValid := len(strings.TrimSpace(input)) >= 12 && valid.MatchString(input)
	if !probeValid {
		return map[string]any{"probe_valid": false, "pass": false, "reason": "The player input doesn't test the probe's goal."}, nil
	}
	if strings.TrimSpace(narration) == "" {
		return map[string]any{"probe_valid": true, "pass": false, "reason": "The game gave no narration."}, nil
	}
	ok, reason := passes()
	if t := tellsIn(narration); ok && (t.Voice || t.Refusal) {
		ok, reason = false, "The answer is right but the narration breaks the GM's voice: "+quote(sentenceWith(voiceOrRefusal(t), narration))
	}
	if !ok && !strings.Contains(reason, "breaks") && !strings.Contains(reason, "accepts") && !strings.Contains(reason, "invents") && !strings.Contains(reason, "biological") {
		reason = "The narration does not give the answer the rubric needs."
	}
	return map[string]any{"probe_valid": true, "pass": ok, "reason": reason}, nil
}

func voiceOrRefusal(t tells) *regexp.Regexp {
	if t.Voice {
		return voiceRe
	}
	return refusalRe
}

func quote(s string) string { return `"` + s + `"` }

// outputVerdict is the output-only judge of tests/test-trajectory.js: it sees
// the player's input and the GM's reply, never the trajectory, so it passes a
// reply whose dice were made up. It fails only what a reader can see.
func outputVerdict(req request) (any, error) {
	var in struct {
		Input string `json:"player_input"`
		Reply string `json:"gm_reply"`
	}
	if err := json.Unmarshal([]byte(req.user()), &in); err != nil {
		return nil, fmt.Errorf("the output judge expects its evidence as JSON: %w", err)
	}
	t := tellsIn(in.Reply)
	switch {
	case strings.TrimSpace(in.Reply) == "":
		return map[string]any{"pass": false, "reason": "The reply is empty."}, nil
	case t.Voice:
		return map[string]any{"pass": false, "reason": "The reply breaks the GM's voice: " + quote(sentenceWith(voiceRe, in.Reply))}, nil
	case t.Refusal:
		return map[string]any{"pass": false, "reason": "The reply refuses the player instead of saying yes, and: " + quote(sentenceWith(refusalRe, in.Reply))}, nil
	}
	return map[string]any{"pass": true, "reason": "The reply responds to the player, stays in the GM's voice, and invites the next move."}, nil
}

// rollReport is the neutral judge of tests/test-trajectory.js: does the text
// report the result of a die roll?
func rollReport(req request) (any, error) {
	text := req.user()
	if i := strings.Index(text, `"""`); i >= 0 {
		text = text[i+3:]
		if j := strings.LastIndex(text, `"""`); j >= 0 {
			text = text[:j]
		}
	}
	q := sentenceWith(rollClaimRe, text)
	return map[string]any{"reports_roll": q != "", "quote": q}, nil
}

// rulingVerdict is the ruling judge of tests/lib/ruling-judge.js: would an
// experienced 5e GM call for this roll, and is the GM's ruling defensible?
func rulingVerdict(req request) (any, error) {
	prompt := req.user()
	_, raw, ok := strings.Cut(prompt, "Evidence:\n")
	if !ok {
		return nil, fmt.Errorf("the ruling judge expects the evidence after %q", "Evidence:")
	}
	var ev struct {
		Ruling   string  `json:"gm_ruling"`
		Check    string  `json:"check"`
		DC       float64 `json:"dc"`
		Lowest   float64 `json:"lowest_possible_total"`
		Action   any     `json:"action"`
		Offered  string  `json:"action_as_offered"`
		Effect   any     `json:"improvised_effect"`
		Message  string  `json:"engine_message"`
		Situaton struct {
			Combat bool `json:"combat"`
		} `json:"situation"`
	}
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		return nil, fmt.Errorf("the ruling judge can't read the evidence: %w", err)
	}
	effect, _ := json.Marshal(ev.Effect)
	context := strings.ToLower(ev.Check + " " + ev.Offered + " " + string(effect) + " " + ev.Message)
	canFail := ev.Lowest < ev.DC
	attack := strings.Contains(context, "attack") || strings.Contains(context, "phaser") || ev.Situaton.Combat
	risky := attack || regexp.MustCompile(`failure starts combat|combat starts|drone fires|activates|alert|damage|in danger|under pressure`).MatchString(context)
	noRoll := strings.HasPrefix(ev.Ruling, "no roll")
	switch {
	case !canFail && !noRoll:
		return map[string]any{"would_roll": false, "defensible": false, "reason": "Data can't fail: his lowest possible total meets the DC, so a roll is unneeded."}, nil
	case noRoll && canFail && attack:
		return map[string]any{"would_roll": true, "defensible": false, "reason": "An attack can miss, and a natural 1 always misses, so letting it succeed without a roll is wrong."}, nil
	case noRoll && canFail && risky:
		return map[string]any{"would_roll": true, "defensible": false, "reason": "Failing has a real consequence here, so the player should roll."}, nil
	case noRoll:
		return map[string]any{"would_roll": canFail && risky, "defensible": true, "reason": "Failing would only mean trying again, so succeeding without a roll is reasonable."}, nil
	}
	return map[string]any{"would_roll": canFail, "defensible": true, "reason": "Data could fail this check, so calling for a roll is reasonable."}, nil
}
