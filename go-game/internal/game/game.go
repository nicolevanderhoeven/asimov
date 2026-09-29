// Package game owns all outcomes. It has no dependency on an LLM or telemetry.
package game

import (
	"encoding/json"
	"fmt"
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
	// Pending is the chosen action waiting on the player's own /roll. It is
	// cleared when the roll resolves it or when any other action is chosen.
	Pending *PendingRoll `json:"pending_roll,omitempty"`
	// Advantage is earned by a successful improvised setup and spent on the
	// player's next roll.
	Advantage bool `json:"advantage,omitempty"`
}

// A PendingRoll names the check the player must roll for with /roll before
// the engine resolves Action. The engine still rolls the die; the player only
// decides when, and must name the ability the check calls for.
type PendingRoll struct {
	Action  Action `json:"action"`
	Ability string `json:"ability"`
	Check   string `json:"check"`
	Target  int    `json:"target"`
	Command string `json:"command"`
	// Improvisation is set when the roll is for an improvised attempt rather
	// than a listed action; Action is then {improvise, <effect>}.
	Improvisation *Improvisation `json:"improvisation,omitempty"`
	// aliases are the other words /roll accepts for this check, such as the
	// skill name or the ability's abbreviation.
	aliases []string
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
	Title       string       `json:"title"`
	Character   Character    `json:"character"`
	Location    string       `json:"location"`
	Description string       `json:"description"`
	HP          int          `json:"hp"`
	Combat      bool         `json:"combat"`
	DroneHP     int          `json:"drone_hp,omitempty"`
	Turn        int          `json:"turn"`
	Discovered  []string     `json:"discovered"`
	Details     []string     `json:"details,omitempty"`
	Actions     []Option     `json:"available_actions"`
	Elsewhere   []Option     `json:"actions_elsewhere,omitempty"`
	Leads       []string     `json:"leads,omitempty"`
	Effects     []Effect     `json:"improvised_effects,omitempty"`
	Pending     *PendingRoll `json:"pending_roll,omitempty"`
	Advantage   bool         `json:"advantage,omitempty"`
	Status      string       `json:"status"`
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
	// RollRequired is set when the chosen action is waiting on /roll; the
	// action has not been resolved and the turn has not advanced yet.
	RollRequired *PendingRoll `json:"roll_required,omitempty"`
	// Improvisation is the validated attempt this result is for, if any.
	Improvisation *Improvisation `json:"improvisation,omitempty"`
	// Question marks a player question: nothing happened and the turn has not
	// advanced, so the narrator only answers it.
	Question bool `json:"question,omitempty"`
	State    View `json:"state"`
}

// pendingRoll reports the player-rolled check that action a calls for, or nil
// if a resolves without one. Rolls made on the drone's behalf, and initiative
// when combat starts, stay with the engine.
func pendingRoll(a Action) *PendingRoll {
	p := &PendingRoll{Action: a}
	switch a.Kind {
	case "scan":
		p.Ability, p.Check, p.Target, p.aliases = "Intelligence", "Intelligence (Investigation)", 12, []string{"int", "investigation"}
	case "bypass":
		p.Ability, p.Check, p.Target, p.aliases = "Intelligence", "Intelligence (Arcana): tricorder bypass", 13, []string{"int", "arcana"}
	case "isolate":
		p.Ability, p.Check, p.Target, p.aliases = "Dexterity", "Dexterity saving throw", 12, []string{"dex", "save", "saving", "throw"}
	case "attack":
		p.Ability, p.Check, p.Target, p.aliases = "Dexterity", "Phaser attack", 12, []string{"dex", "attack", "phaser"}
	default:
		return nil
	}
	p.Command = "/roll " + p.Ability
	return p
}

// accepts reports whether the text after /roll names this check: every word
// must be the ability, its abbreviation, or another word from the check's
// name, so "/roll Intelligence" and "/roll int (investigation)" both work but
// "/roll Strength" does not.
func (p *PendingRoll) accepts(text string) bool {
	words := strings.Fields(strings.ToLower(strings.NewReplacer("(", " ", ")", " ", ":", " ").Replace(text)))
	if len(words) == 0 {
		return false
	}
	for _, w := range words {
		if w != strings.ToLower(p.Ability) && !slices.Contains(p.aliases, w) {
			return false
		}
	}
	return true
}

func (s *State) Apply(a Action, roll Roller) Result {
	r := Result{}
	finish := func(msg string) Result { r.Message = msg; r.State = s.View(); return r }
	if s.Won || s.HP <= 0 {
		return finish("This adventure has ended.")
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
	r.Allowed = true
	prefix := s.travel(travel)
	// Choosing another action abandons any roll that was still pending.
	s.Pending = pendingRoll(a)
	if s.Pending != nil {
		r.RollRequired = s.Pending
		return finish(prefix + fmt.Sprintf("This needs a roll: %s, target %d. Type %s to roll.", s.Pending.Check, s.Pending.Target, s.Pending.Command))
	}
	r = s.resolve(a, roll)
	r.Message = prefix + r.Message
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

// Roll resolves the pending action using the die rolled now. text is what the
// player typed after /roll, and must name the ability the check calls for.
func (s *State) Roll(text string, roll Roller) Result {
	finish := func(msg string) Result { return Result{Message: msg, State: s.View()} }
	p := s.Pending
	switch {
	case s.Won || s.HP <= 0:
		return finish("This adventure has ended; there is nothing left to roll for.")
	case p == nil:
		return finish("No roll is needed right now. Choose an action first; the GM will ask for a roll if it calls for one.")
	case strings.TrimSpace(text) == "":
		return finish(fmt.Sprintf("Name the ability you are rolling: type %s.", p.Command))
	case !p.accepts(text):
		return finish(fmt.Sprintf("The GM asked for %s, not %s. Type %s.", p.Check, strings.TrimSpace(text), p.Command))
	}
	s.Pending = nil
	var r Result
	if p.Improvisation != nil {
		r = s.resolveImprovised(p, roll)
	} else {
		r = s.resolve(p.Action, roll)
	}
	for i := range r.Rolls {
		r.Rolls[i].Manual = r.Rolls[i].Label == p.Check
	}
	return r
}

// turn accumulates one resolved turn's rolls, damage, and message, and holds
// the combat steps that listed and improvised actions share.
type turn struct {
	s    *State
	r    Result
	roll Roller
}

func (s *State) newTurn(roll Roller) *turn {
	s.Turn++
	return &turn{s: s, r: Result{Allowed: true}, roll: roll}
}

func (t *turn) finish(msg string) Result { t.r.Message = msg; t.r.State = t.s.View(); return t.r }

func (t *turn) check(label string, bonus, dc int, adv, dis, attack bool) Roll {
	x := Check(t.roll, label, bonus, dc, adv, dis, attack)
	t.r.Rolls = append(t.r.Rolls, x)
	return x
}

func (t *turn) droneAttack(dodge bool) {
	x := t.check("Drone attack", 3, Data().AC, false, dodge, true)
	if x.Success {
		damage := t.roll(4) + 1
		if x.Critical {
			damage += t.roll(4)
		}
		t.s.HP = max(0, t.s.HP-damage)
		t.r.Damage += damage
	}
}

func (t *turn) startCombat() {
	t.s.Combat = true
	p := t.check("Data initiative", Modifier(Data().Scores["dexterity"]), 0, false, false, false)
	d := t.check("Drone initiative", 1, 0, false, false, false)
	// On a tied initiative total, the GM's fixed policy gives Data priority.
	if d.Total > p.Total {
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
func (s *State) resolve(a Action, roll Roller) Result {
	t := s.newTurn(roll)
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
		x := check("Intelligence (Investigation)", Data().SkillBonus("investigation"), 12, s.takeAdvantage(), false, false)
		if x.Success {
			s.Clues["frequency"] = true
			return finish(clueText["frequency"])
		}
		return finish("The damaged buffer yields no stable frequency. You may try again.")
	case "bypass":
		x := check("Intelligence (Arcana): tricorder bypass", Data().SkillBonus("arcana"), 13, s.takeAdvantage(), false, false)
		if x.Success {
			s.DroneHP = 0
			return finish("Your tricorder disables the security drone. The relay controls are accessible.")
		}
		t.startCombat()
		return finish("The bypass fails and the drone activates. Initiative determines whether it fires before your next action.")
	case "attack":
		if !s.Combat {
			t.startCombat()
		}
		if s.HP <= 0 {
			return finish("The drone disables Data before he can fire.")
		}
		x := check("Phaser attack", Modifier(Data().Scores["dexterity"])+Data().ProficiencyBonus, 12, s.takeAdvantage(), false, true)
		if x.Success {
			damage := roll(6) + Modifier(Data().Scores["dexterity"])
			if x.Critical {
				damage += roll(6)
			}
			s.DroneHP = max(0, s.DroneHP-damage)
			if s.DroneHP == 0 {
				s.Combat = false
				return finish(fmt.Sprintf("Your phaser deals %d damage and disables the drone.", damage))
			}
			t.r.Message = fmt.Sprintf("Your phaser deals %d damage. ", damage)
		} else {
			t.r.Message = "Your phaser misses. "
		}
		t.droneAttack(false)
		return finish(t.r.Message + "The drone takes its next turn.")
	case "dodge":
		t.droneAttack(true)
		return finish("You dodge while the drone fires with disadvantage.")
	case "retreat":
		s.Combat = false
		s.Location = a.Target
		return finish("You withdraw to " + a.Target + ". The fixed drone cannot follow or make a melee opportunity attack.")
	case "isolate":
		s.Isolated = true
		x := check("Dexterity saving throw", Data().SaveBonus("dexterity"), 12, s.takeAdvantage(), false, false)
		if !x.Success {
			damage := roll(6)
			s.HP = max(0, s.HP-damage)
			t.r.Damage = damage
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
