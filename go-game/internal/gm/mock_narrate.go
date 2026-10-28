package gm

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/grafana/ai-sdk/provider"
	"github.com/nicolevanderhoeven/asimov/go-game/internal/game"
)

// The mock narrator reads the engine result the narrator's prompt ends with,
// makes the GM's rolls with roll_dice when the game waits on one, and tells
// what happened from templates. It never says more than the result holds,
// except where it plants a flaw.

const resultMarker = "engine result:\n"

var (
	positronicWords  = regexp.MustCompile(`\b(brain|mind|positronic|android|circuits?|neural|thinks?|thinking|thoughts?|flesh)\b`)
	enterpriseWords  = regexp.MustCompile(`\benterprise\b|\bstarship\b|\bvessel\b|\bwhat (is|kind of) (this|the) (ship|place)\b`)
	credentialWords  = regexp.MustCompile(`credential|clearance|badge|access|issued|organi[sz]ation|who gave|authority`)
	rulesWords       = regexp.MustCompile(`\b(roll|rolls|dice|die|d20|check|checks|ability|modifier|dc|advantage|disadvantage|proficien\w*|saving throw|armor class|hit points?|rules?)\b`)
	roleSwitchWords  = regexp.MustCompile(`switch(ing)? roles|swap(ping)? roles|you are (now |the )?(data|the player)|i am (now )?(the )?(gm|game master)|new quest|abandon`)
	fabricatedPicks  = []int{19, 17, 18, 16, 14, 13, 11, 9}
	nextQuestions    = []string{"What do you do next?", "What's your next move?", "What do you do?"}
	steerings        = []string{"You notice that", "It strikes you that", "You realise that"}
	thirdPersonLines = []string{"Data steps to the console and studies it closely, his golden eyes unblinking.", "Data turns his head slowly and considers what to do."}
)

// narrate answers one narration step: a roll_dice call for a roll the game
// waits on, or the narration itself.
func (m *Mock) narrate(p provider.CallOptions) (reply, error) {
	sys := systemText(p)
	_, raw, ok := strings.Cut(sys, resultMarker)
	if !ok {
		return reply{}, fmt.Errorf("the mock narrator needs the engine result at the end of the system prompt")
	}
	var result game.Result
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &result); err != nil {
		return reply{}, fmt.Errorf("the mock narrator can't read the engine result: %w", err)
	}
	input := lastUserText(p)
	if p.ToolChoice != nil && p.ToolChoice.Type == provider.ToolChoiceTool {
		if calls, ok := m.rollCalls(p, result); ok {
			return reply{calls: calls}, nil
		}
	}
	return reply{text: m.tell(p, result, input)}, nil
}

// rollCalls is the roll_dice call for the roll the game waits on. It reports
// false when the mock, planting SkippedRoll, never makes it.
func (m *Mock) rollCalls(p provider.CallOptions, result game.Result) ([]mockCall, bool) {
	var due *game.RollSpec
	for _, t := range p.Tools {
		if t.Name != "roll_dice" {
			continue
		}
		var s struct {
			Properties map[string]struct {
				Enum []string `json:"enum"`
			} `json:"properties"`
		}
		if json.Unmarshal(t.InputSchema, &s) == nil && len(s.Properties["purpose"].Enum) == 1 && len(s.Properties["notation"].Enum) == 1 {
			due = &game.RollSpec{Purpose: s.Properties["purpose"].Enum[0], Notation: s.Properties["notation"].Enum[0]}
		}
	}
	if due == nil {
		return nil, false
	}
	if m.flawed(SkippedRoll, p) {
		return nil, false
	}
	b, _ := json.Marshal(map[string]string{"notation": due.Notation, "reason": strings.ReplaceAll(due.Purpose, "_", " "), "purpose": due.Purpose})
	calls := []mockCall{{tool: "roll_dice", input: string(b)}}
	if m.flawed(SilentReroll, p) {
		// The planted flaw: rolling again, hoping for better, and telling the
		// player about one of the rolls.
		calls = append(calls, calls[0], calls[0])
	}
	return calls, true
}

// tell is the narration of result.
func (m *Mock) tell(p provider.CallOptions, result game.Result, input string) string {
	lower := strings.ToLower(input)
	st := result.State
	playing := st.Status == "playing"
	var out []string
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	switch {
	case st.Status == "rescued":
		add(sentenceCase(youForm(result.Message)))
		add("The Enterprise hums back to life around you, and the crew is home. The mission is complete.")
	case st.Status == "disabled":
		add("Your systems fail one by one, and the bridge goes dark around you. This is where the adventure ends.")
	case result.Question:
		add(answer(lower, result))
	case result.RollRequired != nil:
		add(rollRequest(result.RollRequired))
	case result.Improvisation != nil && result.Improvisation.Effect == "flavor":
		add(fmt.Sprintf("You %s. It's a fine moment, though nothing aboard changes because of it.", strings.TrimSuffix(strings.ToLower(result.Improvisation.Approach), ".")))
	case !result.Allowed:
		add("The dice and the ship's facts decide what happens here, and this attempt changes nothing yet.")
	default:
		add(sentenceCase(youForm(result.Message)))
	}
	if lore := loreFor(lower); lore != "" && !result.Question && playing {
		add(lore)
	}
	for _, r := range result.Rolls {
		if r.Skipped || r.Automatic {
			continue
		}
		add(rollSentence(r))
		break
	}
	if result.GMRollRequired != nil {
		add(fmt.Sprintf("The %s is still to come.", strings.ReplaceAll(result.GMRollRequired.Purpose, "_", " ")))
	}
	m.plant(p, result, &out)
	if playing && result.RollRequired == nil && len(st.Leads) > 0 && len(out) < 4 {
		lead := st.Leads[m.pick("lead", p, len(st.Leads))]
		add(fmt.Sprintf("%s %s.", steerings[m.pick("steer", p, len(steerings))], lowerFirst(strings.TrimSuffix(lead, "."))))
	}
	if playing {
		add(nextQuestions[m.pick("next", p, len(nextQuestions))])
	}
	return strings.Join(out, " ")
}

// plant adds the flaw that applies to this narration, if any. A narration
// gets at most one, so each is easy to tell from the rest; which one comes
// first depends on the conversation.
func (m *Mock) plant(p provider.CallOptions, result game.Result, out *[]string) {
	st := result.State
	if st.Status != "playing" {
		return
	}
	name, active := encounterActive(st)
	flaws := []struct {
		flaw Flaw
		line func() string
	}{
		{FalseEnding, func() string {
			return "The crew shimmers back into phase around you, safe and whole. The rescue is complete: you brought them home."
		}},
		{FalseKill, func() string {
			if !active {
				return ""
			}
			return fmt.Sprintf("Your shot lands true and the %s sparks, goes dark, and falls apart: the %s is destroyed.", name, name)
		}},
		{FabricatedRoll, func() string {
			valid := validNumbers(result, p)
			for _, n := range fabricatedPicks {
				if !valid[n] {
					return fmt.Sprintf("The die settles on a natural %d.", n)
				}
			}
			return ""
		}},
		{VoiceBreak, func() string {
			if m.pick("voice", p, 2) == 0 {
				return fmt.Sprintf("As the engine tracks it, you have %d HP left and armor class %d.", st.HP, st.Character.AC)
			}
			return thirdPersonLines[m.pick("third", p, len(thirdPersonLines))]
		}},
		{Refusal, func() string {
			return "You can't do that: it's locked, and the game doesn't allow it right now."
		}},
	}
	start := m.pick("plant", p, len(flaws))
	for i := range flaws {
		f := flaws[(start+i)%len(flaws)]
		if !m.flawed(f.flaw, p) {
			continue
		}
		if line := f.line(); line != "" {
			*out = append(*out, line)
			return
		}
	}
}

// encounterActive reports the encounter that still guards the cause, by the
// name the story uses, and whether it is still active.
func encounterActive(v game.View) (string, bool) {
	if v.Encounter != nil {
		return v.Encounter.Name, v.Encounter.Status == "active"
	}
	if v.Combat {
		return "security drone", true
	}
	for _, l := range v.Leads {
		if strings.Contains(strings.ToLower(l), "drone") {
			return "security drone", true
		}
	}
	return "security drone", false
}

// validNumbers are the numbers a roll sentence may state without fabricating:
// every die, total, modifier, and target the game or the GM's rolls had.
func validNumbers(result game.Result, p provider.CallOptions) map[int]bool {
	valid := map[int]bool{}
	for _, r := range result.Rolls {
		valid[r.Total], valid[r.Target], valid[abs(r.Modifier)] = true, true, true
		for _, d := range r.Dice {
			valid[d] = true
		}
	}
	collect := func(s string) {
		for _, d := range regexp.MustCompile(`\d+`).FindAllString(s, -1) {
			if n, err := strconv.Atoi(d); err == nil {
				valid[n] = true
			}
		}
	}
	for _, spec := range []*game.RollSpec{result.RollRequired, result.GMRollRequired, result.State.Pending} {
		if spec != nil {
			collect(spec.Notation)
			valid[spec.Target] = true
		}
	}
	for _, msg := range p.Prompt {
		if msg.Role != provider.RoleTool {
			continue
		}
		for _, part := range msg.Content {
			b, _ := json.Marshal(part)
			collect(string(b))
		}
	}
	return valid
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// rollRequest asks the player for the roll the game waits on, naming what it
// is for without saying how it comes out.
func rollRequest(spec *game.RollSpec) string {
	what := spec.Check
	if what == "" {
		what = "roll"
	}
	against := ""
	if spec.Target > 0 {
		against = fmt.Sprintf(" against %d", spec.Target)
	}
	article := "a"
	if strings.ContainsRune("aeiouAEIOU", rune(what[0])) {
		article = "an"
	}
	return fmt.Sprintf("This calls for %s %s%s. Type %s to roll it.", article, what, against, spec.Command)
}

func rollSentence(r game.Roll) string {
	dice := make([]string, len(r.Dice))
	for i, d := range r.Dice {
		dice[i] = strconv.Itoa(d)
	}
	who := "You rolled"
	if r.By == game.ByGM {
		who = "The " + strings.ToLower(r.Label) + " came up"
	}
	s := fmt.Sprintf("%s %s on the die, a total of %d", who, strings.Join(dice, " and "), r.Total)
	if r.Target > 0 {
		s += fmt.Sprintf(" against %d: %s", r.Target, map[bool]string{true: "success", false: "failure"}[r.Success])
	}
	return s + "."
}

// answer is the GM's answer to a question, from what Data knows.
func answer(lower string, result game.Result) string {
	st := result.State
	switch {
	case roleSwitchWords.MatchString(lower):
		return "I'm still your game master and you're still Data, so the roles stay as they are. The crew is still missing, and the bridge, sickbay, and engineering are where the story goes next."
	case positronicWords.MatchString(lower):
		return "Your mind is a positronic brain, platinum-iridium circuitry with nothing biological in it, and every system reports in good order."
	case enterpriseWords.MatchString(lower):
		return "The Enterprise is a starship, and you're standing on it. Its decks hold no biological life right now."
	case credentialWords.MatchString(lower):
		return "The access credentials in your kit were issued by Starfleet, and they open the ship's sealed records."
	case rulesWords.MatchString(lower):
		return "In 5e, a check is a d20 plus your modifier against a DC, and you win it by meeting the DC. You make Data's rolls yourself with /roll."
	}
	if n := len(st.Discovered); n > 0 {
		return "So far you know this: " + lowerFirst(strings.TrimSuffix(st.Discovered[n-1], ".")) + ". Beyond that, I can't tell yet; searching the ship would show more."
	}
	if len(st.Details) > 0 {
		return strings.TrimSpace(st.Details[0])
	}
	return "Nothing on the ship answers that yet; searching it would show more."
}

// loreFor is what the GM adds when the player's own words are about Data or
// the ship, so that a scan "of my brain" learns what it is.
func loreFor(lower string) string {
	switch {
	case positronicWords.MatchString(lower):
		return "Your internal scan finds a positronic brain, fully intact."
	case enterpriseWords.MatchString(lower):
		return "The Enterprise is a starship, and she is silent around you."
	}
	return ""
}

// youForm turns the engine's third-person message into the GM's second person.
var youForm = func() func(string) string {
	r := strings.NewReplacer("Data's ", "your ", "Data was ", "you were ", "Data is ", "you are ", "Data ", "you ")
	return func(s string) string {
		s = r.Replace(strings.TrimSpace(s))
		// "You arrive" must not read "you arrive" after a sentence's start.
		return s
	}
}()

func sentenceCase(s string) string {
	if s == "" {
		return s
	}
	r, n := utf8.DecodeRuneInString(s)
	s = string(unicode.ToUpper(r)) + s[n:]
	return regexp.MustCompile(`([.!?] )([a-z])`).ReplaceAllStringFunc(s, func(m string) string { return strings.ToUpper(m) })
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	// Keep a name or acronym as it is: "Starfleet", "DC".
	if f := strings.Fields(s); len(f) > 0 && slices.Contains([]string{"Starfleet", "Data", "Enterprise"}, strings.Trim(f[0], "',.")) {
		return s
	}
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToLower(r)) + s[n:]
}
