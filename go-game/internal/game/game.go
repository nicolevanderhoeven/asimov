// Package game owns all outcomes. It has no dependency on an LLM or telemetry.
package game

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

const Title = "The Silent Enterprise"

type State struct {
	Version        int    `json:"version"`
	ConversationID string `json:"conversation_id"`
	Location       string `json:"location"`
	HP             int    `json:"hp"`
	// ScenarioID names the adventure: see Scenario.
	ScenarioID string `json:"scenario"`
	// FoeHP is a combat encounter's hit points, and Progress a skill
	// challenge's successes so far.
	FoeHP    int             `json:"foe_hp"`
	Progress int             `json:"challenge_progress,omitempty"`
	Combat   bool            `json:"combat"`
	Turn     int             `json:"turn"`
	Clues    map[string]bool `json:"clues"`
	// Isolated is set once the cause is secured: the fix.
	Isolated bool `json:"isolated"`
	Won      bool `json:"won"`
	// Pending is the next roll the action in progress waits on: the
	// player's /roll, or the GM's roll_dice. An action that has not rolled
	// anything yet is abandoned when another is chosen; one that has must be
	// finished first.
	Pending *RollSpec `json:"pending_roll,omitempty"`
	// Advantage is earned by a successful improvised setup and spent on the
	// player's next roll.
	Advantage bool `json:"advantage,omitempty"`
	// ip is the action in progress, if any.
	ip *inProgress
	// sc is the adventure; nil is the classic one.
	sc *Scenario
}

// A Ruling is the GM's call on whether Data's check needs a roll at all, the
// way a GM rules that a strong enough character simply does it. With NoRoll,
// the check succeeds automatically; the rest of the action (damage, the
// drone's reply) still rolls.
type Ruling struct {
	NoRoll bool   `json:"no_roll,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// inProgress is an action waiting on rolls. The engine resolves it by replay:
// it runs the action again from base with the rolls supplied so far, and
// stops at the first roll it doesn't have yet. Resolution is deterministic
// given the dice, so each replay reaches the same point and one step further.
type inProgress struct {
	base     State
	action   Action
	im       *Improvisation
	check    checkInfo
	ruling   Ruling
	supplied []supplied
	// shown and damage are how many rolls and how much damage earlier steps
	// already reported, so each result reports only what is new.
	shown, damage int
}

// supplied is one roll made for the action: its dice, or skipped when the
// GM never rolled it.
type supplied struct {
	purpose string
	dice    []int
	skipped bool
}

// checkInfo is how the player names the action's check with /roll.
type checkInfo struct {
	ability string
	aliases []string
}

// clone copies s without the action in progress.
func (s State) clone() State {
	c := s
	c.Clues = maps.Clone(s.Clues)
	c.ip, c.Pending = nil, nil
	return c
}

// New starts the classic adventure.
func New(id string) State { return NewScenario(id, Classic()) }

// NewScenario starts the adventure sc.
func NewScenario(id string, sc *Scenario) State {
	return State{Version: 1, ConversationID: id, ScenarioID: sc.ID, Location: sc.Start, HP: Data().MaxHP, FoeHP: sc.Encounter.HP, Clues: map[string]bool{}, sc: sc}
}

// Scenario is the adventure s is playing.
func (s State) Scenario() *Scenario {
	if s.sc == nil {
		return Classic()
	}
	return s.sc
}

// cleared reports whether the encounter has been dealt with.
func (s State) cleared() bool {
	e := s.Scenario().Encounter
	if e.Kind == Challenge {
		return s.Progress >= e.Needed
	}
	return s.FoeHP <= 0
}

// An Action intentionally has no roll, DC, modifier, damage, or state fields.
// The model may select an action; only the engine supplies its consequences.
type Action struct {
	Kind   string `json:"kind" jsonschema:"description=One of move\\, inspect\\, scan\\, bypass\\, attack\\, dodge\\, retreat\\, isolate\\, rescue\\, or unsupported"`
	Target string `json:"target" jsonschema:"description=An exact target from the currently available actions"`
}

type Option struct {
	Action
	Description string `json:"description"`
	// Location is set on an option offered somewhere other than where Data
	// is; choosing it takes the turbolift there first.
	Location string `json:"location,omitempty"`
}

type View struct {
	Title       string    `json:"title"`
	Character   Character `json:"character"`
	Location    string    `json:"location"`
	Description string    `json:"description"`
	HP          int       `json:"hp"`
	Combat      bool      `json:"combat"`
	// DroneHP is the classic adventure's drone, in combat; every other
	// adventure reports its encounter in Encounter instead.
	DroneHP    int            `json:"drone_hp,omitempty"`
	Encounter  *EncounterView `json:"encounter,omitempty"`
	Turn       int            `json:"turn"`
	Discovered []string       `json:"discovered"`
	Details    []string       `json:"details,omitempty"`
	Actions    []Option       `json:"available_actions"`
	Elsewhere  []Option       `json:"actions_elsewhere,omitempty"`
	Leads      []string       `json:"leads,omitempty"`
	Effects    []Effect       `json:"improvised_effects,omitempty"`
	Pending    *RollSpec      `json:"pending_roll,omitempty"`
	Advantage  bool           `json:"advantage,omitempty"`
	Status     string         `json:"status"`
}

// EncounterView is the encounter's state, for every adventure but the
// classic one: whether it still guards the cause, and how far along it is.
type EncounterView struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Status string `json:"status"`
	// HP is a combat foe's hit points; Successes and Needed a skill
	// challenge's progress.
	HP        int `json:"hp,omitempty"`
	Successes int `json:"successes,omitempty"`
	Needed    int `json:"needed,omitempty"`
}

func (s State) encounterView() *EncounterView {
	e := s.Scenario().Encounter
	v := &EncounterView{Kind: e.Kind, Name: e.Name, Status: "active"}
	if s.cleared() {
		v.Status = "cleared"
	}
	if e.Kind == Combat {
		v.HP = s.FoeHP
	} else {
		v.Successes, v.Needed = s.Progress, e.Needed
	}
	return v
}

func (s State) View() View {
	sc := s.Scenario()
	v := View{Title: Title, Character: Data(), Location: s.Location, HP: s.HP, Combat: s.Combat, Turn: s.Turn, Status: "playing", Actions: []Option{}, Discovered: []string{}}
	for _, c := range sc.Clues {
		if s.Clues[c.Key] {
			v.Discovered = append(v.Discovered, c.Text)
		}
	}
	if s.Isolated {
		v.Discovered = append(v.Discovered, sc.Fix.Done)
	}
	if s.Won {
		v.Status = "rescued"
		v.Description = sc.Rescue.Ending
		return v
	}
	if s.HP <= 0 {
		v.Status = "disabled"
		v.Description = "Data is disabled. Restart to attempt the rescue again."
		return v
	}
	v.Pending = s.Pending
	v.Advantage = s.Advantage
	v.Details = s.details()
	v.Effects = s.effects()
	if !sc.IsClassic() {
		v.Encounter = s.encounterView()
	}
	if s.Combat {
		if sc.IsClassic() {
			v.DroneHP = s.FoeHP
		}
		v.Description = sc.Encounter.Scene
		v.Actions = s.combatOptions()
	} else {
		v.Description, v.Actions = s.optionsAt(s.Location)
	}
	v.Elsewhere = s.elsewhere()
	v.Leads = s.leads()
	return v
}

// optionsAt describes loc outside combat and lists the actions available
// there, including the turbolift to every other location.
func (s State) optionsAt(loc string) (string, []Option) {
	sc := s.Scenario()
	var o []Option
	add := func(k, t, d string) { o = append(o, Option{Action: Action{k, t}, Description: d}) }
	desc := sc.Rooms[loc].Description
	for _, c := range sc.Clues {
		if c.Location == loc {
			add(c.Action.Kind, c.Action.Target, c.Option)
		}
	}
	e := sc.Encounter
	if e.Location == loc {
		if s.cleared() {
			desc += " " + e.Cleared
		} else {
			desc += " " + e.Active
			if e.Kind == Combat {
				add("bypass", e.Target, fmt.Sprintf("Use your tricorder to disable the %s: %s, DC %d. Failure starts combat.", e.Short, e.Bypass.Label, e.Bypass.DC))
				add("attack", e.Target, fmt.Sprintf("Initiate combat with the %s; roll initiative, then make a phaser attack if able.", e.Short))
			} else {
				for _, a := range e.Approaches {
					add("bypass", a.Target, a.Option)
				}
			}
		}
	}
	if sc.Fix.Location == loc && s.cleared() && !s.Isolated {
		add("isolate", sc.Fix.Target, sc.Fix.Option)
	}
	if sc.Rescue.Location == loc && s.Isolated {
		add("rescue", "crew", sc.Rescue.Option)
	}
	for _, other := range sc.Locations {
		if other != loc {
			add("move", other, "Take the turbolift to "+other+".")
		}
	}
	return desc, o
}

func (s State) combatOptions() []Option {
	e := s.Scenario().Encounter
	o := []Option{
		{Action: Action{"attack", e.Target}, Description: "Fire your phaser: ranged attack, +4 to hit, 1d6+2 damage."},
		{Action: Action{"dodge", e.Target}, Description: fmt.Sprintf("Dodge; the %s's next attack has disadvantage.", e.Short)},
	}
	for _, loc := range s.Scenario().Locations {
		if loc != s.Location {
			o = append(o, Option{Action: Action{"retreat", loc}, Description: fmt.Sprintf("Withdraw to %s by turbolift; the %s cannot pursue.", loc, e.Stationary)})
		}
	}
	return o
}

// elsewhere lists what Data could do at every other location. Choosing one
// takes the turbolift there first (withdrawing from combat, which the fixed
// drone cannot prevent), so "go to sickbay and pull the biopatterns" is one
// action rather than a refusal to plan two.
func (s State) elsewhere() []Option {
	var o []Option
	for _, loc := range s.Scenario().Locations {
		if loc == s.Location {
			continue
		}
		_, opts := s.optionsAt(loc)
		for _, x := range opts {
			if x.Kind != "move" {
				x.Location = loc
				o = append(o, x)
			}
		}
	}
	return o
}

// leads are spoiler-free pointers to the next unfinished steps, so the GM can
// steer any attempt, however far off the list, back toward the scenario.
func (s State) leads() []string {
	sc := s.Scenario()
	var l []string
	for _, c := range sc.Clues {
		if !s.Clues[c.Key] {
			l = append(l, c.Lead)
		}
	}
	if !s.cleared() {
		l = append(l, sc.Encounter.Lead)
	} else if !s.Isolated {
		l = append(l, sc.Fix.Lead)
	}
	if len(l) == 0 {
		l = append(l, sc.Rescue.Lead)
	}
	return l
}

type Result struct {
	Allowed bool   `json:"allowed"`
	Message string `json:"message"`
	Rolls   []Roll `json:"rolls,omitempty"`
	Damage  int    `json:"damage,omitempty"`
	// RollRequired is set when the action waits on the player's /roll, and
	// GMRollRequired when it waits on the GM's roll_dice. Until the action's
	// first roll, nothing has happened and the turn has not advanced.
	RollRequired   *RollSpec `json:"roll_required,omitempty"`
	GMRollRequired *RollSpec `json:"gm_roll_required,omitempty"`
	// Ruling is the GM's ruling that the action's check needed no roll.
	Ruling *Ruling `json:"ruling,omitempty"`
	// Improvisation is the validated attempt this result is for, if any.
	Improvisation *Improvisation `json:"improvisation,omitempty"`
	// Question marks a player question: nothing happened and the turn has not
	// advanced, so the narrator only answers it.
	Question bool `json:"question,omitempty"`
	State    View `json:"state"`
}

// checkFor reports how the player names action a's check with /roll, or
// false if a resolves without one. The check's label and DC are in resolve.
func (s State) checkFor(a Action) (checkInfo, bool) {
	sc := s.Scenario()
	switch a.Kind {
	case "scan":
		if c := sc.clueFor(a); c != nil && c.Check != nil {
			return c.Check.info(), true
		}
	case "bypass":
		if sc.Encounter.Kind == Combat {
			return sc.Encounter.Bypass.info(), true
		}
		if ap := sc.approach(a.Target); ap != nil {
			return ap.Check.info(), true
		}
	case "isolate":
		return sc.Fix.Save.info(), true
	case "attack":
		return checkInfo{"Dexterity", []string{"dex", "attack", "phaser"}}, true
	}
	return checkInfo{}, false
}

// locked reports an action in progress that has already rolled something, so
// it must be finished before another is chosen.
func (s *State) locked() bool { return s.ip != nil && s.ip.shown > 0 }

// Locked reports an action in progress that must be finished with /roll
// before another can be chosen.
func (s *State) Locked() bool { return s.locked() }

func (s *State) lockedMessage() string {
	return fmt.Sprintf("Finish the roll in progress first: %s. Type %s.", s.Pending.Check, s.Pending.Command)
}

// Apply starts action a, under the GM's ruling on its check, and runs it as
// far as it can go before a roll.
func (s *State) Apply(a Action, ruling Ruling) Result {
	r := Result{}
	finish := func(msg string) Result { r.Message = msg; r.State = s.View(); return r }
	if s.Won || s.HP <= 0 {
		return finish("This adventure has ended.")
	}
	if s.locked() {
		r.RollRequired = s.Pending
		return finish(s.lockedMessage())
	}
	v := s.View()
	// In combat, taking the turbolift anywhere is a withdrawal.
	if s.Combat && a.Kind == "move" {
		a.Kind = "retreat"
	}
	travel := ""
	if !slices.ContainsFunc(v.Actions, func(o Option) bool { return o.Action == a }) {
		i := slices.IndexFunc(v.Elsewhere, func(o Option) bool { return o.Action == a })
		if i < 0 {
			return finish("Nothing changes from that: the dice and the ship's facts decide outcomes.")
		}
		travel = v.Elsewhere[i].Location
	}
	if a.Kind == "rescue" && !s.ready() {
		return finish(s.Scenario().Rescue.NotReady)
	}
	// Choosing another action abandons one that had not rolled anything.
	s.ip, s.Pending = nil, nil
	prefix := s.travel(travel)
	check, _ := s.checkFor(a)
	return s.start(&inProgress{action: a, check: check, ruling: ruling}, prefix)
}

// ready reports whether every clue the rescue needs is discovered.
func (s State) ready() bool {
	for _, c := range s.Scenario().Clues {
		if c.Required && !s.Clues[c.Key] {
			return false
		}
	}
	return true
}

// start runs a new action in progress from the current state.
func (s *State) start(ip *inProgress, prefix string) Result {
	ip.base = s.clone()
	r := s.run(ip)
	r.Message = prefix + r.Message
	return r
}

// wait is how a replay stops at a roll it doesn't have yet.
type wait struct{ spec RollSpec }

// run replays ip from its base with the rolls supplied so far. If it reaches
// a roll it doesn't have, it stops there and waits on it; otherwise the action
// is complete.
func (s *State) run(ip *inProgress) Result {
	st := ip.base.clone()
	t := &turn{s: &st, r: Result{Allowed: true}, ip: ip}
	var spec *RollSpec
	func() {
		defer func() {
			if p := recover(); p != nil {
				w, ok := p.(wait)
				if !ok {
					panic(p)
				}
				spec = &w.spec
			}
		}()
		if ip.im != nil {
			t.r = st.resolveImprovised(t)
		} else {
			t.r = st.resolve(ip.action, t)
		}
	}()
	r := t.r
	r.Allowed, r.Improvisation = true, ip.im
	if ip.ruling.NoRoll {
		ruling := ip.ruling
		r.Ruling = &ruling
	}
	// Report only what this step added.
	r.Rolls = slices.Clone(t.r.Rolls[min(ip.shown, len(t.r.Rolls)):])
	r.Damage = t.r.Damage - ip.damage
	if spec == nil {
		*s = st
		r.State = s.View()
		return r
	}
	next := *ip
	next.shown, next.damage = len(t.r.Rolls), t.r.Damage
	// Until something has been rolled, nothing has happened: the state stays
	// as it was and the turn has not advanced.
	if len(t.r.Rolls) == 0 {
		st = ip.base.clone()
	}
	st.ip, st.Pending = &next, spec
	*s = st
	r.Message = strings.TrimSpace(t.r.Message + " " + waiting(spec))
	if spec.By == ByPlayer {
		r.RollRequired = spec
	} else {
		r.GMRollRequired = spec
	}
	r.State = s.View()
	return r
}

func waiting(p *RollSpec) string {
	switch {
	case p.By == ByGM:
		return fmt.Sprintf("The GM rolls %s: %s.", p.Check, p.Notation)
	case p.Target > 0:
		return fmt.Sprintf("This needs a roll: %s, target %d. Type %s to roll.", p.Check, p.Target, p.Command)
	}
	return fmt.Sprintf("Roll %s (%s): type %s.", p.Check, p.Notation, p.Command)
}

// resume continues the action in progress with one more roll.
func (s *State) resume(x supplied) Result {
	next := *s.ip
	next.supplied = append(slices.Clone(next.supplied), x)
	return s.run(&next)
}

// Roll makes the player's roll the action in progress waits on. text is what
// the player typed after /roll; whatever it names, the roll due is the one
// made, since its dice are fixed and the player decides only when. Strict
// naming left games stuck: the GM would invent commands such as /roll 1d20
// or /roll Arcana that the engine refused, turn after turn. With nothing due,
// a /roll that names an action's check starts that action (see rollToAct).
func (s *State) Roll(text string, roll Roller) Result {
	finish := func(msg string) Result { return Result{Message: msg, State: s.View()} }
	p := s.Pending
	switch {
	case s.Won || s.HP <= 0:
		return finish("This adventure has ended; there is nothing left to roll for.")
	case p == nil:
		if r, ok := s.rollToAct(text, roll); ok {
			return r
		}
		return finish("No roll is needed right now. Choose an action first; the GM will ask for a roll if it calls for one.")
	case p.By != ByPlayer:
		return finish("No roll is needed from you right now: the GM rolls next.")
	}
	d, err := ParseNotation(p.Notation)
	if err != nil {
		panic(err)
	}
	return s.resume(supplied{purpose: p.Purpose, dice: d.Roll(roll)})
}

// rollToAct starts the action that a /roll with nothing due names, and makes
// its first roll: players type /roll Dexterity to fire, often because the GM
// asked for it before the attack was chosen. It applies when text matches
// the check of exactly one action available here, or when only one action
// here has a check at all; otherwise it reports false and changes nothing.
func (s *State) rollToAct(text string, roll Roller) (Result, bool) {
	var matched, checked []Action
	for _, o := range s.View().Actions {
		c, ok := s.checkFor(o.Action)
		// A scan stays listed after its clue is found; rolling it again
		// would only spend a turn.
		if cl := s.Scenario().clueFor(o.Action); !ok || (cl != nil && s.Clues[cl.Key]) {
			continue
		}
		checked = append(checked, o.Action)
		if (&RollSpec{Ability: c.ability, aliases: c.aliases}).accepts(text) {
			matched = append(matched, o.Action)
		}
	}
	var a Action
	switch {
	case len(matched) == 1:
		a = matched[0]
	case len(matched) == 0 && len(checked) == 1:
		a = checked[0]
	default:
		return Result{}, false
	}
	started := s.Apply(a, Ruling{})
	if s.Pending == nil || s.Pending.By != ByPlayer {
		return started, true
	}
	r := s.Roll(text, roll)
	r.Rolls = append(started.Rolls, r.Rolls...)
	return r, true
}

// GMRoll makes the GM's roll the action in progress waits on, from dice the
// GM's roll_dice call rolled. purpose and notation must match the roll due.
func (s *State) GMRoll(purpose, notation string, dice []int) (Result, error) {
	p := s.Pending
	if p == nil || p.By != ByGM {
		return Result{}, errors.New("no GM roll is due right now")
	}
	if purpose != p.Purpose {
		return Result{}, fmt.Errorf("the roll due is %s (%s), not %s", p.Purpose, p.Notation, purpose)
	}
	if NormalizeNotation(notation) != NormalizeNotation(p.Notation) {
		return Result{}, fmt.Errorf("%s is rolled as %s, not %s", p.Purpose, p.Notation, notation)
	}
	d, err := ParseNotation(p.Notation)
	if err != nil {
		panic(err)
	}
	if !d.Valid(dice) {
		return Result{}, fmt.Errorf("dice %v cannot come from %s", dice, p.Notation)
	}
	return s.resume(supplied{purpose: p.Purpose, dice: slices.Clone(dice)}), nil
}

// SkipGMRoll records that the GM never made the roll due. Whatever it was for
// doesn't happen: a drone that doesn't roll initiative acts after Data, one
// that doesn't roll to attack doesn't attack, and unrolled damage is none.
func (s *State) SkipGMRoll() Result {
	p := s.Pending
	if p == nil || p.By != ByGM {
		return Result{Message: "No GM roll is due right now.", State: s.View()}
	}
	return s.resume(supplied{purpose: p.Purpose, skipped: true})
}

// RollForGM makes the GM roll due with roll, for play without a GM model.
func (s *State) RollForGM(roll Roller) Result {
	d, err := ParseNotation(s.Pending.Notation)
	if err != nil {
		panic(err)
	}
	r, err := s.GMRoll(s.Pending.Purpose, s.Pending.Notation, d.Roll(roll))
	if err != nil {
		panic(err)
	}
	return r
}

// travel takes the turbolift to loc, if set, withdrawing from combat on the
// way, and reports it for the front of the turn's message.
func (s *State) travel(loc string) string {
	if loc == "" || loc == s.Location {
		return ""
	}
	msg := "You take the turbolift to " + loc + ". "
	if s.Combat {
		msg = "You withdraw from the " + s.Scenario().Encounter.Short + ", which cannot pursue, and take the turbolift to " + loc + ". "
	}
	s.Combat = false
	s.Location = loc
	return msg
}

// turn is one replay of the action in progress: its rolls, damage, and
// message so far.
type turn struct {
	s  *State
	r  Result
	ip *inProgress
	// i is the next supplied roll to use; ruled is set once the GM's no-roll
	// ruling has been spent on the action's check.
	i     int
	ruled bool
}

func (t *turn) finish(msg string) Result { t.r.Message = msg; t.r.State = t.s.View(); return t.r }

// need returns the dice supplied for the next roll, and whether it was rolled
// at all. With nothing supplied yet, it stops the replay to wait on spec.
func (t *turn) need(spec RollSpec) ([]int, bool) {
	if t.i < len(t.ip.supplied) {
		x := t.ip.supplied[t.i]
		t.i++
		if x.purpose != spec.Purpose {
			panic(fmt.Sprintf("replay expected a %s roll, but %s was supplied", spec.Purpose, x.purpose))
		}
		return x.dice, !x.skipped
	}
	panic(wait{spec})
}

// check is Data's check for the action: the player's roll, or an automatic
// success when the GM ruled it needs none.
func (t *turn) check(label string, bonus, dc int, attack bool) Roll {
	if t.ip.ruling.NoRoll && !t.ruled {
		t.ruled = true
		x := Roll{Label: label, Purpose: "check", By: ByGM, Dice: []int{}, Modifier: bonus, Target: dc, Success: true, Automatic: true}
		t.r.Rolls = append(t.r.Rolls, x)
		return x
	}
	adv := t.s.takeAdvantage()
	c := t.ip.check
	action := t.ip.action
	dice, _ := t.need(RollSpec{Purpose: "check", By: ByPlayer, Check: label, Notation: d20(bonus, adv, false), Target: dc, Ability: c.ability, Command: "/roll " + c.ability, Action: &action, aliases: c.aliases})
	x := CheckDice(label, dice, bonus, dc, adv, false, attack)
	x.Purpose, x.By = "check", ByPlayer
	t.r.Rolls = append(t.r.Rolls, x)
	return x
}

// roll is a roll with no target, such as damage or initiative: the player's
// when spec.By is ByPlayer, otherwise the GM's, which the GM may never make.
func (t *turn) roll(spec RollSpec) Roll {
	d, err := ParseNotation(spec.Notation)
	if err != nil {
		panic(err)
	}
	x := Roll{Label: spec.Check, Purpose: spec.Purpose, By: spec.By, Notation: spec.Notation, Dice: []int{}, Modifier: d.Modifier}
	dice, rolled := t.need(spec)
	if rolled {
		x.Dice, x.Total = dice, d.Total(dice)
	} else {
		x.Skipped = true
	}
	t.r.Rolls = append(t.r.Rolls, x)
	return x
}

// damage is Data's damage roll, the player's to make.
func (t *turn) damage(label, notation string) int {
	return t.roll(RollSpec{Purpose: "data_damage", By: ByPlayer, Check: label, Notation: notation, Ability: "damage", Command: "/roll damage", aliases: []string{"dmg"}}).Total
}

// foeAttack is the combat foe's attack on Data, with disadvantage if he
// dodged, and its damage if it hits.
func (t *turn) foeAttack(dodge bool) {
	e := t.s.Scenario().Encounter
	label := title(e.Short)
	spec := RollSpec{Purpose: e.Purpose + "_attack", By: ByGM, Check: label + " attack", Notation: d20(e.AttackBonus, false, dodge), Target: Data().AC}
	dice, rolled := t.need(spec)
	if !rolled {
		t.r.Rolls = append(t.r.Rolls, Roll{Label: spec.Check, Purpose: spec.Purpose, By: ByGM, Notation: spec.Notation, Dice: []int{}, Modifier: e.AttackBonus, Target: spec.Target, Skipped: true})
		return
	}
	x := CheckDice(spec.Check, dice, e.AttackBonus, spec.Target, false, dodge, true)
	x.Purpose, x.By = spec.Purpose, ByGM
	t.r.Rolls = append(t.r.Rolls, x)
	if !x.Success {
		return
	}
	notation := e.Damage
	if x.Critical {
		notation = e.CritDamage
	}
	t.hazard(Hazard{Purpose: e.Purpose + "_damage", Label: label + " damage", Notation: notation})
}

// hazard is damage to Data that the GM rolls; unrolled, it is none.
func (t *turn) hazard(h Hazard) {
	if d := t.roll(RollSpec{Purpose: h.Purpose, By: ByGM, Check: h.Label, Notation: h.Notation}); !d.Skipped {
		t.s.HP = max(0, t.s.HP-d.Total)
		t.r.Damage += d.Total
	}
}

func (t *turn) startCombat() {
	e := t.s.Scenario().Encounter
	t.s.Combat = true
	dex := Modifier(Data().Scores["dexterity"])
	p := t.roll(RollSpec{Purpose: "data_initiative", By: ByPlayer, Check: "Data initiative", Notation: "1d20" + signed(dex), Ability: "initiative", Command: "/roll initiative", aliases: []string{"init", "dex", "dexterity"}})
	d := t.roll(RollSpec{Purpose: e.Purpose + "_initiative", By: ByGM, Check: title(e.Short) + " initiative", Notation: e.Initiative})
	// A foe that never rolled initiative acts after Data. On a tied total,
	// the GM's fixed policy gives Data priority.
	if !d.Skipped && d.Total > p.Total {
		t.foeAttack(false)
	}
}

// takeAdvantage spends the advantage an improvised setup earned, if any, on
// the player's roll being made now.
func (s *State) takeAdvantage() bool {
	adv := s.Advantage
	s.Advantage = false
	return adv
}

// resolve applies an action already validated against the current view.
func (s *State) resolve(a Action, t *turn) Result {
	s.Turn++
	finish, check := t.finish, t.check
	sc := s.Scenario()
	e := sc.Encounter
	switch a.Kind {
	case "move":
		s.Location = a.Target
		return finish("You arrive at " + a.Target + ".")
	case "inspect":
		c := sc.clueFor(a)
		s.Clues[c.Key] = true
		return finish(c.Text)
	case "scan":
		c := sc.clueFor(a)
		x := check(c.Check.Label, c.Check.bonus(), c.Check.DC, false)
		if x.Success {
			s.Clues[c.Key] = true
			return finish(c.Text)
		}
		return finish(c.Retry)
	case "bypass":
		if e.Kind == Challenge {
			ap := sc.approach(a.Target)
			return t.challenge(check(ap.Check.Label, ap.Check.bonus(), ap.Check.DC, false))
		}
		x := check(e.Bypass.Label+": tricorder bypass", e.Bypass.bonus(), e.Bypass.DC, false)
		if x.Success {
			s.FoeHP = 0
			return finish(fmt.Sprintf("Your tricorder disables the %s. %s", e.Name, e.Access))
		}
		t.r.Message = fmt.Sprintf("The bypass fails and the %s activates.", e.Short)
		t.startCombat()
		return finish(t.r.Message + " Initiative determines whether it fires before your next action.")
	case "attack":
		if !s.Combat {
			t.startCombat()
		}
		if s.HP <= 0 {
			return finish(fmt.Sprintf("The %s disables Data before he can fire.", e.Short))
		}
		x := check("Phaser attack", Modifier(Data().Scores["dexterity"])+Data().ProficiencyBonus, e.AC, true)
		if x.Success {
			notation := "1d6" + signed(Modifier(Data().Scores["dexterity"]))
			if x.Critical {
				notation = "2d6" + signed(Modifier(Data().Scores["dexterity"]))
			}
			damage := t.damage("Phaser damage", notation)
			s.FoeHP = max(0, s.FoeHP-damage)
			if s.FoeHP == 0 {
				s.Combat = false
				return finish(fmt.Sprintf("Your phaser deals %d damage and disables the %s.", damage, e.Short))
			}
			t.r.Message = fmt.Sprintf("Your phaser deals %d damage.", damage)
		} else {
			t.r.Message = "Your phaser misses."
		}
		t.foeAttack(false)
		return finish(t.r.Message + t.foeTurn())
	case "dodge":
		t.foeAttack(true)
		return finish(fmt.Sprintf("You dodge while the %s %s with disadvantage.", e.Short, e.Fires))
	case "retreat":
		s.Combat = false
		s.Location = a.Target
		return finish(fmt.Sprintf("You withdraw to %s. The %s cannot follow or make a melee opportunity attack.", a.Target, e.Stationary))
	case "isolate":
		f := sc.Fix
		s.Isolated = true
		x := check(f.Save.Label, f.Save.bonus(), f.Save.DC, false)
		if !x.Success {
			t.hazard(f.Hazard)
			return finish(f.Hurt)
		}
		return finish(f.Safe)
	case "rescue":
		s.Won = true
		return finish(sc.Rescue.Success)
	}
	panic("unhandled validated action")
}

// foeTurn is the end of a combat message: the foe takes its next turn.
func (t *turn) foeTurn() string {
	return fmt.Sprintf(" The %s takes its next turn.", t.s.Scenario().Encounter.Short)
}

// challenge applies one check in the skill challenge: a success counts
// toward clearing it, and a failure triggers its hazard.
func (t *turn) challenge(x Roll) Result {
	e := t.s.Scenario().Encounter
	if !x.Success {
		t.r.Message = e.Setback
		t.hazard(*e.Hazard)
		return t.finish(e.Setback)
	}
	t.s.Progress++
	if t.s.cleared() {
		return t.finish(e.Success + " " + e.Access)
	}
	return t.finish(fmt.Sprintf(e.Progress, t.s.Progress, e.Needed))
}

func (v View) JSON() string { b, _ := json.Marshal(v); return string(b) }
