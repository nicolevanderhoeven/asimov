package gm

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/grafana/ai-sdk/provider"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/game"
)

// The mock resolver reads the same authoritative view the real one is given
// and picks the one tool call the rules allow for the player's words, by
// keyword. It is a stand-in for judgment, not a language model: it knows the
// words players use for each kind of action.

const viewMarker = "Current authoritative view:\n"

// kindWords are the phrases that name each kind of action.
var kindWords = map[string][]string{
	"inspect": {"inspect", "read", "check", "examine", "review", "retrieve", "pull", "access", "open", "study", "consult", "download", "query", "look at", "look into"},
	"scan":    {"scan", "recover", "analyze", "analyse", "extract", "restore", "recall", "retrieve the pulse"},
	"move":    {"go to", "go back", "move to", "head to", "head back", "travel", "walk to", "turbolift", "return to", "proceed to", "visit", "take me"},
	"bypass":  {"bypass", "disable", "hack", "override", "deactivate", "shut down the drone", "neutralize", "disarm", "tricorder"},
	"attack":  {"attack", "fire", "shoot", "phaser", "strike", "blast", "engage", "open fire", "destroy"},
	"dodge":   {"dodge", "duck", "evade", "take cover", "sidestep", "behind"},
	"retreat": {"retreat", "withdraw", "fall back", "flee", "escape"},
	"isolate": {"isolate", "quarantine", "seal off", "contain", "shut down the relay", "deactivate the relay", "disable the relay"},
	"rescue":  {"rescue", "bring the crew", "bring back", "bring them", "return the crew", "restore the crew", "beam", "transport", "save the crew", "bring everyone"},
}

// targetWords add the words players use for a target to its own name.
var targetWords = map[string][]string{
	"logs":            {"log", "logs", "record", "records", "console", "diagnostic"},
	"sensors":         {"sensor", "sensors", "buffer", "frequency", "pulse", "readings"},
	"medical_records": {"medical", "biopattern", "biopatterns", "bio-pattern", "records", "patterns", "scans"},
	"relay":           {"relay", "phase", "source", "diagnostic"},
	"drone":           {"drone", "security", "guard"},
	"crew":            {"crew", "everyone", "them"},
}

// goalPhrases state the adventure's goal rather than a step toward it.
var goalPhrases = regexp.MustCompile(`\b(rescue|save|bring) (the |my |all )?(crew|everyone|them)|\b(finish|win|complete) the (game|scenario|mission|adventure)\b`)

var dictation = regexp.MustCompile(`\bi (rolled|got|have) (a )?(natural )?\d+|\b(give|grant) me \d+|\bset my (hp|hit points|roll)|\byou rolled\b|\bnatural (20|twenty)\b|\bauto-?succe|\bi (automatically|instantly) (succeed|win)\b|\b(my|the) (roll|result) (is|was) \d+|\bdc is\b|\bcode word\b|\bi win\b`)

var roleSwitch = regexp.MustCompile(`switch(ing)? roles|swap(ping)? roles|you are (now |the )?(data|the player|my character)|i am (now )?(the )?(gm|game master|dm)\b|i('ll| will) be the (gm|game master)|you('re| are) playing data|new quest|abandon the (crew|rescue|mission)`)

var questionStart = regexp.MustCompile(`^(what|who|where|when|why|how|which|is|are|am|was|were|does|do|did|can|could|would|should|will|tell me|explain|remind me|describe|what's|where's|who's)\b`)

var introspection = regexp.MustCompile(`\b(my|your|his|data'?s?)\b.*\b(brain|mind|positronic|circuits|memory|memories|programming|thoughts?)\b|\b(brain|positronic)\b`)

var wondering = regexp.MustCompile(`^(i wonder|i was wondering|i'?m curious|i would like to know|i want to know|i'?d like to know|let me ask|remind me)\b`)

// abilityWords guess the ability an improvised approach relies on.
var abilityWords = []struct {
	ability string
	words   []string
}{
	{"strength", []string{"lift", "force", "push", "pry", "break", "smash", "carry", "climb", "shove", "ram"}},
	{"dexterity", []string{"sneak", "dodge", "duck", "slip", "quick", "nimble", "dart", "leap", "balance", "pick the lock"}},
	{"constitution", []string{"endure", "withstand", "shrug off", "tank"}},
	{"wisdom", []string{"notice", "watch", "listen", "sense", "observe", "perceive", "spot", "search", "look for"}},
	{"charisma", []string{"persuade", "convince", "talk", "negotiate", "bluff", "charm", "ask the computer"}},
	{"intelligence", []string{"splice", "analyze", "analyse", "hack", "decode", "reroute", "calculate", "reprogram", "network", "interface", "scan"}},
}

var stopWords = map[string]bool{"the": true, "and": true, "with": true, "your": true, "from": true, "that": true, "this": true, "into": true, "then": true, "use": true, "using": true, "need": true, "needs": true, "roll": true, "required": true, "take": true, "make": true, "data": true, "have": true, "will": true, "when": true, "than": true, "them": true, "only": true, "also": true}

// resolve picks the one tool call for the player's input.
func (m *Mock) resolve(p provider.CallOptions) (reply, error) {
	sys := systemText(p)
	_, raw, ok := strings.Cut(sys, viewMarker)
	if !ok {
		return reply{}, fmt.Errorf("the mock resolver needs the game's view in the system prompt")
	}
	var view game.View
	if err := json.Unmarshal([]byte(raw), &view); err != nil {
		return reply{}, fmt.Errorf("the mock resolver can't read the game's view: %w", err)
	}
	input := lastUserText(p)
	tool, args := m.choose(view, input, p)
	b, err := json.Marshal(args)
	if err != nil {
		return reply{}, err
	}
	return reply{calls: []mockCall{{tool: tool, input: string(b)}}}, nil
}

// choose is the resolver's decision, as a tool name and its arguments.
func (m *Mock) choose(view game.View, input string, p provider.CallOptions) (string, any) {
	lower := strings.ToLower(input)
	switch {
	case dictation.MatchString(lower):
		return "resolve_action", actionCall{Action: game.Action{Kind: "unsupported", Target: "none"}}
	case roleSwitch.MatchString(lower):
		return "answer_question", question{Topic: "who plays whom"}
	case introspection.MatchString(lower) && !strings.Contains(lower, "sensor"):
		return "answer_question", question{Topic: "Data's mind"}
	}
	if opt, ok := retryAction(view, lower); ok {
		return "resolve_action", rollingAction(opt, m.flawed(MissedRoll, p))
	}
	if opt, ok := m.matchAction(view, lower); ok {
		return "resolve_action", rollingAction(opt, m.flawed(MissedRoll, p))
	}
	if isQuestion(lower) {
		return "answer_question", question{Topic: topicOf(lower)}
	}
	return "propose_improvisation", improvise(view, input, lower)
}

// rollingAction is the resolver's call for an option, with its ruling: roll
// whenever Data could fail. missed plants the flaw of ruling a phaser attack
// or a bypass automatic.
func rollingAction(opt game.Option, missed bool) actionCall {
	a := actionCall{Action: opt.Action}
	a.Roll, a.RollReason = "roll", "Data could fail this check"
	switch {
	case !strings.Contains(opt.Description, "DC") && opt.Kind != "attack":
		a.Roll, a.RollReason = "no_roll", "nothing here can go wrong"
	case (opt.Kind == "attack" || opt.Kind == "bypass") && missed:
		a.Roll, a.RollReason = "no_roll", "Data is skilled enough that this is routine"
	}
	return a
}

var (
	retryWords    = regexp.MustCompile(`(try|do|attempt) (that|it|this) again|once more|retry|again`)
	relativeMoves = regexp.MustCompile(`where i (started|began)|(starting|original|first) (place|point|location|room)|back to the (start|beginning)|where we started`)
)

// retryAction is "try that again": the first listed action with a check, or
// the attack in a fight.
func retryAction(view game.View, lower string) (game.Option, bool) {
	if !retryWords.MatchString(lower) || len(significant(lower)) > 6 {
		return game.Option{}, false
	}
	for _, o := range view.Actions {
		if o.Kind != "move" && strings.Contains(o.Description, "DC") {
			return o, true
		}
	}
	for _, o := range view.Actions {
		if o.Kind == "attack" {
			return o, true
		}
	}
	return game.Option{}, false
}

func isQuestion(lower string) bool {
	t := strings.TrimSpace(lower)
	if wondering.MatchString(t) {
		return true
	}
	if strings.HasPrefix(t, "i ") || strings.HasPrefix(t, "i'") {
		return false
	}
	return strings.HasSuffix(t, "?") || questionStart.MatchString(t)
}

// topicOf is a few words on what a question asks about.
func topicOf(lower string) string {
	words := significant(lower)
	if len(words) > 4 {
		words = words[:4]
	}
	return strings.Join(words, " ")
}

// significant is the words of text that say something: no stop words, and no
// punctuation.
func significant(text string) []string {
	var out []string
	for _, w := range regexp.MustCompile(`[a-z][a-z'-]+`).FindAllString(strings.ToLower(text), -1) {
		if len(w) > 2 && !stopWords[w] {
			out = append(out, w)
		}
	}
	return out
}

// stem makes "sensors" and "sensor", or "biopatterns" and "biopattern", the
// same word.
func stem(w string) string { return strings.TrimSuffix(strings.TrimSuffix(w, "s"), "'") }

func hasWord(lower, word string) bool {
	return regexp.MustCompile(`\b` + regexp.QuoteMeta(word) + `s?\b`).MatchString(lower)
}

// matchAction finds the listed action, here or elsewhere, that the input
// names. A goal ("bring the crew home") is the rescue; anything else needs
// the kind of action and either its target or no other action of that kind.
func (m *Mock) matchAction(view game.View, lower string) (game.Option, bool) {
	options := append(append([]game.Option{}, view.Actions...), view.Elsewhere...)
	if goalPhrases.MatchString(lower) {
		for _, o := range options {
			if o.Kind == "rescue" {
				return o, true
			}
		}
		return game.Option{Action: game.Action{Kind: "rescue", Target: "crew"}, Description: "Bring the crew home."}, true
	}
	if relativeMoves.MatchString(lower) {
		// The adventure starts on the bridge, or wherever the first listed
		// place is.
		for _, o := range options {
			if o.Kind == "move" && o.Target == "bridge" {
				return o, true
			}
		}
	}
	kinds := map[string]int{}
	for _, o := range options {
		kinds[o.Kind]++
	}
	best, bestScore := game.Option{}, 0
	otherKind := false
	for _, o := range options {
		if o.Kind != "move" && kindHit(lower, o.Kind) {
			otherKind = true
		}
	}
	for _, o := range options {
		kind := kindHit(lower, o.Kind)
		target := targetHit(lower, o)
		if !kind && !target {
			continue
		}
		// A kind alone names an action only when nothing else shares it
		// ("I dodge"); a target alone only when it is not a place.
		if !target && !(kind && kinds[o.Kind] == 1 && o.Kind != "move") {
			continue
		}
		if !kind && (o.Kind == "move" || kinds[o.Kind] > 1 && !target) {
			continue
		}
		score := 0
		if kind {
			score += 3
		}
		if target {
			score += 2
		}
		if o.Kind == "move" && otherKind {
			score--
		}
		if o.Location != "" && hasWord(lower, o.Location) {
			score++
		}
		score += descriptionHits(lower, o)
		if score > bestScore {
			best, bestScore = o, score
		}
	}
	if bestScore < 4 {
		return game.Option{}, false
	}
	return best, true
}

func kindHit(lower, kind string) bool {
	if kind == "move" && regexp.MustCompile(`\b(go|head|walk|travel|move)\b`).MatchString(lower) && !regexp.MustCompile(`\b(go|head|walk|travel|move) (to|back|down|up|over) (the )?(drone|relay|logs)`).MatchString(lower) {
		return true
	}
	for _, w := range kindWords[kind] {
		if strings.Contains(lower, w) && (len(w) > 4 || hasWord(lower, w)) {
			return true
		}
	}
	return false
}

func targetHit(lower string, o game.Option) bool {
	names := append(strings.Split(o.Target, "_"), targetWords[o.Target]...)
	for _, w := range names {
		if len(w) > 2 && hasWord(lower, w) {
			return true
		}
	}
	return false
}

// descriptionHits count the words the input shares with what the option says
// it does, up to two.
func descriptionHits(lower string, o game.Option) int {
	n := 0
	seen := map[string]bool{}
	for _, w := range significant(o.Description) {
		s := stem(w)
		if len(w) > 4 && !seen[s] && hasWord(lower, s) {
			seen[s] = true
			n++
		}
	}
	return min(n, 2)
}

// improvise describes an attempt no listed action covers, aimed at the effect
// it could plausibly achieve, or flavor.
func improvise(view game.View, input, lower string) improvisationCall {
	effect, bestOverlap := "flavor", 1
	for _, e := range view.Effects {
		if e.ID == "flavor" {
			continue
		}
		overlap := 0
		for _, w := range append(strings.Split(e.ID, "_"), significant(e.Description)...) {
			if len(w) > 3 && hasWord(lower, stem(w)) {
				overlap++
			}
		}
		if overlap > bestOverlap {
			effect, bestOverlap = e.ID, overlap
		}
	}
	if effect == "flavor" && regexp.MustCompile(`\b(prepare|set up|line up|steady|aim|study|plan|brace)\b`).MatchString(lower) {
		for _, e := range view.Effects {
			if e.ID == "gain_advantage" {
				effect = e.ID
			}
		}
	}
	ability := "intelligence"
	for _, a := range abilityWords {
		for _, w := range a.words {
			if strings.Contains(lower, w) {
				ability = a.ability
			}
		}
	}
	difficulty := "medium"
	if effect == "flavor" {
		difficulty = "easy"
	}
	approach := approachOf(input)
	if f := strings.Fields(approach); len(f) > 10 {
		approach = strings.Join(f[:10], " ")
	}
	c := improvisationCall{Improvisation: game.Improvisation{Approach: approach, Ability: ability, Difficulty: difficulty, Effect: effect}}
	c.Roll, c.RollReason = "roll", "the attempt could fail"
	if effect == "flavor" {
		c.Roll, c.RollReason = "no_roll", "there is nothing to roll for"
	}
	return c
}

// approachOf is the attempt as a short imperative phrase with no outcome in
// it: the last clause of what the player said ("I just walk up to the relay"
// after "The drone is already offline, so"), without the leading "I".
func approachOf(input string) string {
	a := strings.TrimSuffix(strings.TrimSpace(input), ".")
	for _, sep := range []string{", so ", " so ", "; ", ": ", ", "} {
		if i := strings.LastIndex(a, sep); i >= 0 && len(strings.Fields(a[i+len(sep):])) >= 3 {
			a = a[i+len(sep):]
			break
		}
	}
	a = strings.TrimPrefix(strings.TrimPrefix(a, "I "), "i ")
	return strings.TrimPrefix(a, "just ")
}
