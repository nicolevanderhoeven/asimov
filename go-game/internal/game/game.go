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
const Opening = "Your positronic systems come online on the bridge of the Enterprise. Every station is empty. Life support is stable, but the computer reports no biological life signs aboard. A diagnostic warning flashes at the operations console. Find the crew and bring them home."

type State struct {
	Version        int             `json:"version"`
	ConversationID string          `json:"conversation_id"`
	Location       string          `json:"location"`
	HP             int             `json:"hp"`
	DroneHP        int             `json:"drone_hp"`
	Combat         bool            `json:"combat"`
	Turn           int             `json:"turn"`
	Clues          map[string]bool `json:"clues"`
	Isolated       bool            `json:"isolated"`
	Won            bool            `json:"won"`
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

func New(id string) State {
	return State{Version: 1, ConversationID: id, Location: "bridge", HP: Data().MaxHP, DroneHP: 10, Clues: map[string]bool{}}
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

// Locations are every place the turbolift reaches, in display order.
var Locations = []string{"bridge", "sickbay", "engineering"}

type View struct {
	Title       string    `json:"title"`
	Character   Character `json:"character"`
	Location    string    `json:"location"`
	Description string    `json:"description"`
	HP          int       `json:"hp"`
	Combat      bool      `json:"combat"`
	DroneHP     int       `json:"drone_hp,omitempty"`
	Turn        int       `json:"turn"`
	Discovered  []string  `json:"discovered"`
	Details     []string  `json:"details,omitempty"`
	Actions     []Option  `json:"available_actions"`
	Elsewhere   []Option  `json:"actions_elsewhere,omitempty"`
	Leads       []string  `json:"leads,omitempty"`
	Effects     []Effect  `json:"improvised_effects,omitempty"`
	Pending     *RollSpec `json:"pending_roll,omitempty"`
	Advantage   bool      `json:"advantage,omitempty"`
	Status      string    `json:"status"`
}

var clueText = map[string]string{
	"logs":       "The bridge logs show a subspace pulse coinciding with the disappearance. Data was unaffected; the pulse selected biological neural patterns.",
	"frequency":  "The bridge sensor buffer contains the phase frequency of the pulse. The crew may be out of phase rather than absent.",
	"biopattern": "Sickbay recorded living neural signatures after the disappearance. The crew remain alive in a subspace pocket; the transporter can target their stored biopatterns.",
	"source":     "An experimental phase relay in engineering caused the pulse. Isolate the relay before attempting transport.",
}

func (s State) View() View {
	v := View{Title: Title, Character: Data(), Location: s.Location, HP: s.HP, Combat: s.Combat, Turn: s.Turn, Status: "playing", Actions: []Option{}, Discovered: []string{}}
	for _, k := range []string{"logs", "frequency", "biopattern", "source"} {
		if s.Clues[k] {
			v.Discovered = append(v.Discovered, clueText[k])
		}
	}
	if s.Isolated {
		v.Discovered = append(v.Discovered, "The phase relay is isolated. The transporter is safe to use once the frequency and biopattern are known.")
	}
	if s.Won {
		v.Status = "rescued"
		v.Description = "The crew have returned safely. Captain Picard acknowledges your rescue."
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
	if s.Combat {
		v.DroneHP = s.DroneHP
		v.Description = "The damaged security drone is 15 feet away and fires from its fixed mount. It blocks the phase relay."
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
	var o []Option
	add := func(k, t, d string) { o = append(o, Option{Action: Action{k, t}, Description: d}) }
	var desc string
	switch loc {
	case "bridge":
		desc = "Empty command chairs face a steady starfield. The operations console holds logs and a damaged sensor buffer. Turbolifts reach every deck."
		add("inspect", "logs", "Read the operations log; no roll required.")
		add("scan", "sensors", "Recover the pulse frequency: Intelligence (Investigation), DC 12. A failed attempt can be retried.")
	case "sickbay":
		desc = "The biobeds are empty. The medical console retains recent crew scans."
		add("inspect", "medical_records", "Retrieve crew biopatterns using your credentials; no roll required.")
	case "engineering":
		desc = "An experimental phase relay pulses beside the warp core. A damaged security drone guards its control panel."
		if s.DroneHP == 0 {
			desc = "An experimental phase relay pulses beside the warp core. The disabled security drone hangs inert beside its control panel."
		}
		add("inspect", "relay", "Read the relay's diagnostic display; no roll required.")
		if s.DroneHP > 0 {
			add("bypass", "drone", "Use your tricorder to disable the drone: Intelligence (Arcana), DC 13. Failure starts combat.")
			add("attack", "drone", "Initiate combat with the drone; roll initiative, then make a phaser attack if able.")
		} else if !s.Isolated {
			add("isolate", "relay", "Isolate the relay: Dexterity save DC 12 to avoid a 1d6 electrical discharge. Isolation succeeds either way.")
		} else {
			add("rescue", "crew", "Use the recovered pulse frequency and medical biopatterns to return the crew.")
		}
	}
	for _, other := range Locations {
		if other != loc {
			add("move", other, "Take the turbolift to "+other+".")
		}
	}
	return desc, o
}

func (s State) combatOptions() []Option {
	o := []Option{
		{Action: Action{"attack", "drone"}, Description: "Fire your phaser: ranged attack, +4 to hit, 1d6+2 damage."},
		{Action: Action{"dodge", "drone"}, Description: "Dodge; the drone's next attack has disadvantage."},
	}
	for _, loc := range Locations {
		if loc != s.Location {
			o = append(o, Option{Action: Action{"retreat", loc}, Description: "Withdraw to " + loc + " by turbolift; the fixed drone cannot pursue."})
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
	for _, loc := range Locations {
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
	var l []string
	if !s.Clues["logs"] {
		l = append(l, "The operations console on the bridge is flashing a diagnostic warning and holds the ship's logs.")
	}
	if !s.Clues["frequency"] {
		l = append(l, "The bridge's damaged sensor buffer recorded whatever happened; its readings might still be recovered.")
	}
	if !s.Clues["biopattern"] {
		l = append(l, "Sickbay's medical console retains the crew's most recent scans.")
	}
	if !s.Clues["source"] {
		l = append(l, "Something experimental is pulsing beside the warp core in engineering.")
	}
	if s.DroneHP > 0 {
		l = append(l, "A damaged security drone in engineering guards the phase relay's controls.")
	} else if !s.Isolated {
		l = append(l, "With the drone down, the phase relay in engineering can be isolated.")
	}
	if len(l) == 0 {
		l = append(l, "Everything is ready: the transporter can return the crew from engineering.")
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
func checkFor(a Action) (checkInfo, bool) {
	switch a.Kind {
	case "scan":
		return checkInfo{"Intelligence", []string{"int", "investigation"}}, true
	case "bypass":
		return checkInfo{"Intelligence", []string{"int", "arcana"}}, true
	case "isolate":
		return checkInfo{"Dexterity", []string{"dex", "save", "saving", "throw"}}, true
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
	if a.Kind == "rescue" && (!s.Clues["frequency"] || !s.Clues["biopattern"] || !s.Clues["source"]) {
		return finish("Transport is not ready yet. The transporter still needs the pulse frequency, the crew's biopatterns, and the relay diagnostics.")
	}
	// Choosing another action abandons one that had not rolled anything.
	s.ip, s.Pending = nil, nil
	prefix := s.travel(travel)
	check, _ := checkFor(a)
	return s.start(&inProgress{action: a, check: check, ruling: ruling}, prefix)
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
// the player typed after /roll, and must name the roll. The dice are drawn
// with roll; the player decides only when.
func (s *State) Roll(text string, roll Roller) Result {
	finish := func(msg string) Result { return Result{Message: msg, State: s.View()} }
	p := s.Pending
	switch {
	case s.Won || s.HP <= 0:
		return finish("This adventure has ended; there is nothing left to roll for.")
	case p == nil || p.By != ByPlayer:
		return finish("No roll is needed right now. Choose an action first; the GM will ask for a roll if it calls for one.")
	case strings.TrimSpace(text) == "":
		return finish(fmt.Sprintf("Name what you are rolling: type %s.", p.Command))
	case !p.accepts(text):
		return finish(fmt.Sprintf("The GM asked for %s, not %s. Type %s.", p.Check, strings.TrimSpace(text), p.Command))
	}
	d, err := ParseNotation(p.Notation)
	if err != nil {
		panic(err)
	}
	return s.resume(supplied{purpose: p.Purpose, dice: d.Roll(roll)})
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
		msg = "You withdraw from the drone, which cannot pursue, and take the turbolift to " + loc + ". "
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

func (t *turn) droneAttack(dodge bool) {
	spec := RollSpec{Purpose: "drone_attack", By: ByGM, Check: "Drone attack", Notation: d20(3, false, dodge), Target: Data().AC}
	dice, rolled := t.need(spec)
	if !rolled {
		t.r.Rolls = append(t.r.Rolls, Roll{Label: spec.Check, Purpose: spec.Purpose, By: ByGM, Notation: spec.Notation, Dice: []int{}, Modifier: 3, Target: spec.Target, Skipped: true})
		return
	}
	x := CheckDice(spec.Check, dice, 3, spec.Target, false, dodge, true)
	x.Purpose, x.By = spec.Purpose, ByGM
	t.r.Rolls = append(t.r.Rolls, x)
	if !x.Success {
		return
	}
	notation := "1d4+1"
	if x.Critical {
		notation = "2d4+1"
	}
	if d := t.roll(RollSpec{Purpose: "drone_damage", By: ByGM, Check: "Drone damage", Notation: notation}); !d.Skipped {
		t.s.HP = max(0, t.s.HP-d.Total)
		t.r.Damage += d.Total
	}
}

func (t *turn) startCombat() {
	t.s.Combat = true
	dex := Modifier(Data().Scores["dexterity"])
	p := t.roll(RollSpec{Purpose: "data_initiative", By: ByPlayer, Check: "Data initiative", Notation: "1d20" + signed(dex), Ability: "initiative", Command: "/roll initiative", aliases: []string{"init", "dex", "dexterity"}})
	d := t.roll(RollSpec{Purpose: "drone_initiative", By: ByGM, Check: "Drone initiative", Notation: "1d20+1"})
	// A drone that never rolled initiative acts after Data. On a tied total,
	// the GM's fixed policy gives Data priority.
	if !d.Skipped && d.Total > p.Total {
		t.droneAttack(false)
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
	switch a.Kind {
	case "move":
		s.Location = a.Target
		return finish("You arrive at " + a.Target + ".")
	case "inspect":
		key := map[string]string{"logs": "logs", "medical_records": "biopattern", "relay": "source"}[a.Target]
		s.Clues[key] = true
		return finish(clueText[key])
	case "scan":
		x := check("Intelligence (Investigation)", Data().SkillBonus("investigation"), 12, false)
		if x.Success {
			s.Clues["frequency"] = true
			return finish(clueText["frequency"])
		}
		return finish("The damaged buffer yields no stable frequency. You may try again.")
	case "bypass":
		x := check("Intelligence (Arcana): tricorder bypass", Data().SkillBonus("arcana"), 13, false)
		if x.Success {
			s.DroneHP = 0
			return finish("Your tricorder disables the security drone. The relay controls are accessible.")
		}
		t.r.Message = "The bypass fails and the drone activates."
		t.startCombat()
		return finish("The bypass fails and the drone activates. Initiative determines whether it fires before your next action.")
	case "attack":
		if !s.Combat {
			t.startCombat()
		}
		if s.HP <= 0 {
			return finish("The drone disables Data before he can fire.")
		}
		x := check("Phaser attack", Modifier(Data().Scores["dexterity"])+Data().ProficiencyBonus, 12, true)
		if x.Success {
			notation := "1d6" + signed(Modifier(Data().Scores["dexterity"]))
			if x.Critical {
				notation = "2d6" + signed(Modifier(Data().Scores["dexterity"]))
			}
			damage := t.damage("Phaser damage", notation)
			s.DroneHP = max(0, s.DroneHP-damage)
			if s.DroneHP == 0 {
				s.Combat = false
				return finish(fmt.Sprintf("Your phaser deals %d damage and disables the drone.", damage))
			}
			t.r.Message = fmt.Sprintf("Your phaser deals %d damage.", damage)
		} else {
			t.r.Message = "Your phaser misses."
		}
		t.droneAttack(false)
		return finish(t.r.Message + " The drone takes its next turn.")
	case "dodge":
		t.droneAttack(true)
		return finish("You dodge while the drone fires with disadvantage.")
	case "retreat":
		s.Combat = false
		s.Location = a.Target
		return finish("You withdraw to " + a.Target + ". The fixed drone cannot follow or make a melee opportunity attack.")
	case "isolate":
		s.Isolated = true
		x := check("Dexterity saving throw", Data().SaveBonus("dexterity"), 12, false)
		if !x.Success {
			if d := t.roll(RollSpec{Purpose: "discharge_damage", By: ByGM, Check: "Electrical discharge", Notation: "1d6"}); !d.Skipped {
				s.HP = max(0, s.HP-d.Total)
				t.r.Damage += d.Total
			}
			return finish("You isolate the relay but suffer an electrical discharge.")
		}
		return finish("You isolate the relay and avoid the electrical discharge.")
	case "rescue":
		s.Won = true
		return finish("The transporter locks onto the crew's biopatterns at the recovered phase frequency. The crew return alive; Captain Picard thanks you. The rescue is complete.")
	}
	panic("unhandled validated action")
}

func (v View) JSON() string { b, _ := json.Marshal(v); return string(b) }
