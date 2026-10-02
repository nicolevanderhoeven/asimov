package game

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
)

// A Scenario is everything that differs from one adventure to the next: the
// rooms, the evidence and where it is, the encounter guarding the cause, the
// hazard of shutting the cause down, and the rescue. The shape is the same in
// every scenario, so one engine runs them all: gather the evidence, get past
// the encounter, secure the cause, then bring the crew home. Classic is the
// original hand-written adventure; Generate builds others from modules.
type Scenario struct {
	// ID is "silent-enterprise" for the classic adventure, or "generated-SEED".
	ID   string `json:"id"`
	Seed uint64 `json:"seed,omitempty"`
	// Modules names the module chosen for each part of a generated scenario.
	Modules   map[string]string `json:"modules,omitempty"`
	Opening   string            `json:"opening"`
	Start     string            `json:"start"`
	Locations []string          `json:"locations"`
	Rooms     map[string]Room   `json:"rooms"`
	// Clues are the evidence, in the order the view lists it.
	Clues     []Clue    `json:"clues"`
	Encounter Encounter `json:"encounter"`
	Fix       Fix       `json:"fix"`
	Rescue    Rescue    `json:"rescue"`
	// Computer is the ship's computer's answer, a detail everywhere.
	Computer string `json:"computer"`
	// Prompt holds the scenario's examples in the GM's prompts.
	Prompt Prompt `json:"prompt"`
	// Summary is a spoiler summary of the solution, for test judges only.
	Summary string `json:"summary"`
}

// A Room is one place the turbolift reaches.
type Room struct {
	// Description is the scene. In the encounter's room, the encounter's
	// active or cleared sentence follows it.
	Description string   `json:"description"`
	Details     []string `json:"details"`
}

// A CheckSpec is a d20 roll Data makes against a DC: an ability check, with a
// skill if Skill is set, or a saving throw if Save is set.
type CheckSpec struct {
	// Label is how the check is named, such as "Intelligence (Investigation)".
	Label   string `json:"label"`
	Ability string `json:"ability"`
	Skill   string `json:"skill,omitempty"`
	Save    bool   `json:"save,omitempty"`
	DC      int    `json:"dc"`
}

func (c CheckSpec) bonus() int {
	d := Data()
	switch {
	case c.Save:
		return d.SaveBonus(c.Ability)
	case c.Skill != "":
		return d.SkillBonus(c.Skill)
	}
	return Modifier(d.Scores[c.Ability])
}

// info is how the player names the check with /roll: the ability, or its
// abbreviation, the skill, or the words of a saving throw.
func (c CheckSpec) info() checkInfo {
	aliases := []string{abbreviation(c.Ability)}
	switch {
	case c.Save:
		aliases = append(aliases, "save", "saving", "throw")
	case c.Skill != "":
		aliases = append(aliases, strings.Split(c.Skill, "_")...)
	}
	return checkInfo{title(c.Ability), aliases}
}

func abbreviation(ability string) string {
	for abbr, full := range abilityAbbreviations {
		if full == ability {
			return abbr
		}
	}
	return ability
}

// A Clue is one piece of evidence, found with an action at its location: an
// inspect with no roll, or a scan with a check that can be retried.
type Clue struct {
	Key      string     `json:"key"`
	Text     string     `json:"text"`
	Location string     `json:"location"`
	Action   Action     `json:"action"`
	Option   string     `json:"option"`
	Check    *CheckSpec `json:"check,omitempty"`
	// Retry is the message when the check fails.
	Retry string `json:"retry,omitempty"`
	// Lead points to the clue while it is undiscovered.
	Lead string `json:"lead"`
	// Required clues must all be discovered before the rescue.
	Required bool `json:"required"`
	// Effect is the improvised way to the clue, while it is undiscovered, and
	// EffectRetry the message when that attempt fails.
	Effect      *Effect `json:"effect,omitempty"`
	EffectRetry string  `json:"effect_retry,omitempty"`
}

// Encounter kinds.
const (
	Combat    = "combat"
	Challenge = "challenge"
)

// An Encounter guards the cause, and must be dealt with before the cause can
// be secured. A combat encounter is a foe Data bypasses or fights; a
// challenge is a skill challenge: enough successful checks, from any of its
// approaches, with each failure costing Data damage.
type Encounter struct {
	Kind string `json:"kind"`
	// Name is the full name ("security drone"), Short the name used in
	// sentences ("drone"). Target is the action target.
	Name     string `json:"name"`
	Short    string `json:"short"`
	Target   string `json:"target"`
	Location string `json:"location"`
	// Active and Cleared follow the room's description; ActiveDetail and
	// ClearedDetail are the room's last detail before the computer's.
	Active        string `json:"active"`
	Cleared       string `json:"cleared"`
	ActiveDetail  string `json:"active_detail"`
	ClearedDetail string `json:"cleared_detail"`
	// Lead points to the encounter while it is active.
	Lead string `json:"lead"`
	// Access is what clearing the encounter opens up, such as "The relay
	// controls are accessible."
	Access string `json:"access"`

	// Combat: the foe's statistics and how it fights.
	HP          int    `json:"hp,omitempty"`
	AC          int    `json:"ac,omitempty"`
	AttackBonus int    `json:"attack_bonus,omitempty"`
	Initiative  string `json:"initiative,omitempty"`
	Damage      string `json:"damage,omitempty"`
	CritDamage  string `json:"crit_damage,omitempty"`
	// Purpose prefixes the foe's roll purposes: drone_initiative and so on.
	Purpose string `json:"purpose,omitempty"`
	// Fires is what the foe does when it attacks ("fires"), and Stationary
	// names it as unable to pursue ("fixed drone").
	Fires      string `json:"fires,omitempty"`
	Stationary string `json:"stationary,omitempty"`
	// Bypass is the tricorder check that disables the foe without a fight.
	Bypass *CheckSpec `json:"bypass,omitempty"`
	// Scene and SceneDetails replace the room's while in combat.
	Scene        string   `json:"scene,omitempty"`
	SceneDetails []string `json:"scene_details,omitempty"`

	// Challenge: how many successes it needs, the ways to make them, and the
	// hazard each failure triggers, which the GM rolls.
	Needed     int        `json:"needed,omitempty"`
	Approaches []Approach `json:"approaches,omitempty"`
	Hazard     *Hazard    `json:"hazard,omitempty"`
	// Progress is the message for a success short of clearing it, with the
	// successes and the number needed; Success the message for the last one;
	// Setback the message for a failure.
	Progress string `json:"progress,omitempty"`
	Success  string `json:"success,omitempty"`
	Setback  string `json:"setback,omitempty"`
	// Advance is the improvised effect that counts as a success.
	Advance *Effect `json:"advance,omitempty"`
}

// An Approach is one way to make progress in a skill challenge.
type Approach struct {
	Target string    `json:"target"`
	Option string    `json:"option"`
	Check  CheckSpec `json:"check"`
}

// A Hazard is damage the GM rolls when something goes wrong.
type Hazard struct {
	// Purpose is the roll's purpose, such as discharge_damage.
	Purpose  string `json:"purpose"`
	Label    string `json:"label"`
	Notation string `json:"notation"`
}

// The Fix secures the cause once the encounter is cleared. It always
// succeeds; its saving throw only decides whether Data takes the hazard's
// damage.
type Fix struct {
	Target   string    `json:"target"`
	Location string    `json:"location"`
	Option   string    `json:"option"`
	Save     CheckSpec `json:"save"`
	Hazard   Hazard    `json:"hazard"`
	// Hurt and Safe are the messages for a failed and a successful save.
	Hurt string `json:"hurt"`
	Safe string `json:"safe"`
	// Done is the evidence once the cause is secured.
	Done string `json:"done"`
	// Lead points to the fix once the encounter is cleared.
	Lead string `json:"lead"`
}

// The Rescue ends the adventure, once every required clue is discovered and
// the cause is secured.
type Rescue struct {
	Location string `json:"location"`
	Option   string `json:"option"`
	// NotReady is the message when evidence is still missing.
	NotReady string `json:"not_ready"`
	Success  string `json:"success"`
	// Ending is the scene once the crew are back.
	Ending string `json:"ending"`
	// Lead is the last lead, once everything is ready.
	Lead string `json:"lead"`
}

// Prompt is the scenario's part of the GM's prompts: examples that name its
// own places and things.
type Prompt struct {
	// Elsewhere is an example of an action elsewhere, as the input and the
	// action it is.
	Elsewhere string `json:"elsewhere"`
	// LongShot is an attempt the scenario can't support yet.
	LongShot string `json:"long_shot"`
	// Approach is an example approach for an improvisation.
	Approach string `json:"approach"`
	// NotReady is an attempt that doesn't get the player what they want yet.
	NotReady string `json:"not_ready"`
	// GMRolls says which rolls are the GM's to make.
	GMRolls string `json:"gm_rolls"`
}

// ClassicID is the ID of the original adventure.
const ClassicID = "silent-enterprise"

// IsClassic reports whether sc is the original adventure, whose view keeps
// exactly the fields it has always had.
func (sc *Scenario) IsClassic() bool { return sc.ID == ClassicID }

// Generated reports whether sc was built from modules.
func (sc *Scenario) Generated() bool { return !sc.IsClassic() }

// Mode is "classic" or "generated".
func (sc *Scenario) Mode() string {
	if sc.IsClassic() {
		return "classic"
	}
	return "generated"
}

// Variant names the modules of a generated scenario, in a fixed order, such
// as "transporter/astrometrics/sickbay/exocomp/radiation".
func (sc *Scenario) Variant() string {
	if sc.IsClassic() {
		return ClassicID
	}
	parts := make([]string, 0, len(moduleOrder))
	for _, m := range moduleOrder {
		parts = append(parts, sc.Modules[m])
	}
	return strings.Join(parts, "/")
}

// GMPurposes are the purposes of every roll the GM makes in this scenario.
func (sc *Scenario) GMPurposes() []string {
	var p []string
	e := sc.Encounter
	if e.Kind == Combat {
		p = append(p, e.Purpose+"_initiative", e.Purpose+"_attack", e.Purpose+"_damage")
	} else if e.Hazard != nil {
		p = append(p, e.Hazard.Purpose)
	}
	if !slices.Contains(p, sc.Fix.Hazard.Purpose) {
		p = append(p, sc.Fix.Hazard.Purpose)
	}
	return p
}

func (sc *Scenario) clue(key string) *Clue {
	i := slices.IndexFunc(sc.Clues, func(c Clue) bool { return c.Key == key })
	if i < 0 {
		return nil
	}
	return &sc.Clues[i]
}

func (sc *Scenario) clueFor(a Action) *Clue {
	i := slices.IndexFunc(sc.Clues, func(c Clue) bool { return c.Action == a })
	if i < 0 {
		return nil
	}
	return &sc.Clues[i]
}

func (sc *Scenario) approach(target string) *Approach {
	i := slices.IndexFunc(sc.Encounter.Approaches, func(a Approach) bool { return a.Target == target })
	if i < 0 {
		return nil
	}
	return &sc.Encounter.Approaches[i]
}

// Check validates sc: every location has a room, every clue, the
// encounter, the fix, and the rescue are somewhere the turbolift reaches,
// and every action is distinct. A scenario that passes can be won.
func (sc *Scenario) Check() error {
	at := func(loc, what string) error {
		if !slices.Contains(sc.Locations, loc) {
			return fmt.Errorf("%s is at %q, which the turbolift doesn't reach", what, loc)
		}
		return nil
	}
	for _, loc := range sc.Locations {
		if _, ok := sc.Rooms[loc]; !ok {
			return fmt.Errorf("location %q has no room", loc)
		}
	}
	if err := at(sc.Start, "the start"); err != nil {
		return err
	}
	seen := map[Action]bool{}
	add := func(a Action) error {
		if seen[a] {
			return fmt.Errorf("action %+v is defined twice", a)
		}
		seen[a] = true
		return nil
	}
	required := 0
	for _, c := range sc.Clues {
		if err := at(c.Location, "clue "+c.Key); err != nil {
			return err
		}
		if err := add(c.Action); err != nil {
			return err
		}
		if c.Action.Kind == "scan" && c.Check == nil {
			return fmt.Errorf("scan clue %s has no check", c.Key)
		}
		if c.Required {
			required++
		}
	}
	if required == 0 {
		return fmt.Errorf("no clue is required for the rescue")
	}
	e := sc.Encounter
	if err := at(e.Location, "the encounter"); err != nil {
		return err
	}
	switch e.Kind {
	case Combat:
		if e.HP <= 0 || e.Bypass == nil || e.Purpose == "" {
			return fmt.Errorf("combat encounter needs HP, a bypass, and a roll purpose")
		}
		if err := add(Action{"bypass", e.Target}); err != nil {
			return err
		}
	case Challenge:
		if e.Needed <= 0 || len(e.Approaches) == 0 || e.Hazard == nil {
			return fmt.Errorf("skill challenge needs successes, approaches, and a hazard")
		}
		for _, a := range e.Approaches {
			if err := add(Action{"bypass", a.Target}); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unknown encounter kind %q", e.Kind)
	}
	if sc.Fix.Location != e.Location {
		return fmt.Errorf("the fix must be where the encounter guards it")
	}
	return at(sc.Rescue.Location, "the rescue")
}

var classic = &Scenario{
	ID:        ClassicID,
	Opening:   "Your positronic systems come online on the bridge of the Enterprise. Every station is empty. Life support is stable, but the computer reports no biological life signs aboard. A diagnostic warning flashes at the operations console. Find the crew and bring them home.",
	Start:     "bridge",
	Locations: []string{"bridge", "sickbay", "engineering"},
	Rooms: map[string]Room{
		"bridge": {
			Description: "Empty command chairs face a steady starfield. The operations console holds logs and a damaged sensor buffer. Turbolifts reach every deck.",
			Details: []string{
				"The captain's chair, conn, and ops stations are unoccupied. Every console is still powered.",
				"The main viewscreen shows the ship holding position.",
				"A turbolift at the rear of the bridge reaches every deck.",
			},
		},
		"sickbay": {
			Description: "The biobeds are empty. The medical console retains recent crew scans.",
			Details: []string{
				"The biobeds are made up and unoccupied. A tray of instruments sits untouched.",
				"The door to the chief medical officer's office is open, and her desk console is on.",
			},
		},
		"engineering": {
			Description: "An experimental phase relay pulses beside the warp core.",
			Details: []string{
				"The warp core pulses steadily, and main power reads nominal.",
				"The phase relay is a temporary installation, cabled into a power conduit beside the core.",
				"Tools lie where the engineering crew set them down.",
			},
		},
	},
	Clues: []Clue{
		{
			Key: "logs", Location: "bridge", Action: Action{"inspect", "logs"},
			Text:   "The bridge logs show a subspace pulse coinciding with the disappearance. Data was unaffected; the pulse selected biological neural patterns.",
			Option: "Read the operations log; no roll required.",
			Lead:   "The operations console on the bridge is flashing a diagnostic warning and holds the ship's logs.",
		},
		{
			Key: "frequency", Location: "bridge", Action: Action{"scan", "sensors"}, Required: true,
			Text:        "The bridge sensor buffer contains the phase frequency of the pulse. The crew may be out of phase rather than absent.",
			Option:      "Recover the pulse frequency: Intelligence (Investigation), DC 12. A failed attempt can be retried.",
			Check:       &CheckSpec{Label: "Intelligence (Investigation)", Ability: "intelligence", Skill: "investigation", DC: 12},
			Retry:       "The damaged buffer yields no stable frequency. You may try again.",
			Lead:        "The bridge's damaged sensor buffer recorded whatever happened; its readings might still be recovered.",
			Effect:      &Effect{ID: "recover_frequency", Description: "Recover the pulse frequency from the damaged sensor buffer by another method.", MinDC: 15, OnFailure: "nothing is recovered; you may try again"},
			EffectRetry: "The attempt yields no stable frequency. You may try again.",
		},
		{
			Key: "biopattern", Location: "sickbay", Action: Action{"inspect", "medical_records"}, Required: true,
			Text:   "Sickbay recorded living neural signatures after the disappearance. The crew remain alive in a subspace pocket; the transporter can target their stored biopatterns.",
			Option: "Retrieve crew biopatterns using your credentials; no roll required.",
			Lead:   "Sickbay's medical console retains the crew's most recent scans.",
		},
		{
			Key: "source", Location: "engineering", Action: Action{"inspect", "relay"}, Required: true,
			Text:   "An experimental phase relay in engineering caused the pulse. Isolate the relay before attempting transport.",
			Option: "Read the relay's diagnostic display; no roll required.",
			Lead:   "Something experimental is pulsing beside the warp core in engineering.",
		},
	},
	Encounter: Encounter{
		Kind: Combat, Name: "security drone", Short: "drone", Target: "drone", Location: "engineering",
		Active:        "A damaged security drone guards its control panel.",
		Cleared:       "The disabled security drone hangs inert beside its control panel.",
		ActiveDetail:  "A fixed-mount security drone with a cracked casing guards the relay's control panel. It has not fired.",
		ClearedDetail: "The security drone hangs inert on its mount.",
		Lead:          "A damaged security drone in engineering guards the phase relay's controls.",
		Access:        "The relay controls are accessible.",
		HP:            10, AC: 12, AttackBonus: 3, Initiative: "1d20+1", Damage: "1d4+1", CritDamage: "2d4+1",
		Purpose: "drone", Fires: "fires", Stationary: "fixed drone",
		Bypass: &CheckSpec{Label: "Intelligence (Arcana)", Ability: "intelligence", Skill: "arcana", DC: 13},
		Scene:  "The damaged security drone is 15 feet away and fires from its fixed mount. It blocks the phase relay.",
		SceneDetails: []string{
			"The drone is a fixed-mount security unit with a cracked casing. It tracks Data and fires short bursts.",
			"The phase relay's control panel is behind the drone.",
		},
	},
	Fix: Fix{
		Target: "relay", Location: "engineering",
		Option: "Isolate the relay: Dexterity save DC 12 to avoid a 1d6 electrical discharge. Isolation succeeds either way.",
		Save:   CheckSpec{Label: "Dexterity saving throw", Ability: "dexterity", Save: true, DC: 12},
		Hazard: Hazard{Purpose: "discharge_damage", Label: "Electrical discharge", Notation: "1d6"},
		Hurt:   "You isolate the relay but suffer an electrical discharge.",
		Safe:   "You isolate the relay and avoid the electrical discharge.",
		Done:   "The phase relay is isolated. The transporter is safe to use once the frequency and biopattern are known.",
		Lead:   "With the drone down, the phase relay in engineering can be isolated.",
	},
	Rescue: Rescue{
		Location: "engineering",
		Option:   "Use the recovered pulse frequency and medical biopatterns to return the crew.",
		NotReady: "Transport is not ready yet. The transporter still needs the pulse frequency, the crew's biopatterns, and the relay diagnostics.",
		Success:  "The transporter locks onto the crew's biopatterns at the recovered phase frequency. The crew return alive; Captain Picard thanks you. The rescue is complete.",
		Ending:   "The crew have returned safely. Captain Picard acknowledges your rescue.",
		Lead:     "Everything is ready: the transporter can return the crew from engineering.",
	},
	Computer: "The ship's computer answers queries anywhere aboard, but reports no biological life signs and cannot say where the crew went.",
	Prompt: Prompt{
		Elsewhere: `"go to sickbay and pull the biopatterns" is simply inspect medical_records`,
		LongShot:  "beaming the crew back before the transporter is ready",
		Approach:  "splice into the sensor buffer",
		NotReady:  "a transport that isn't ready",
		GMRolls:   "the drone's rolls and the relay's discharge",
	},
	Summary: "The Silent Enterprise: Data must find the missing crew. Clues: logs (bridge log: a subspace pulse took the crew), frequency (bridge sensor scan, DC 12: the crew may be out of phase), biopattern (sickbay records: the crew are alive in a subspace pocket), source (engineering relay display: the experimental phase relay caused the pulse). A security drone guards the relay; a tricorder bypass (DC 13) disables it, and failure starts combat. The relay must then be isolated (Dexterity save DC 12 against a discharge). Rescue needs frequency, biopattern, source, and the isolated relay.",
}

// Classic is the original adventure.
func Classic() *Scenario { return classic }

// Choose is the scenario a mode names: "classic" (or empty) for the original
// adventure, or "generated" for one built from modules, by seed, or by a
// random seed when seed is 0.
func Choose(mode string, seed uint64) (*Scenario, error) {
	switch mode {
	case "", "classic":
		if seed != 0 {
			return nil, errors.New("only a generated scenario takes a seed")
		}
		return Classic(), nil
	case "generated":
		// Random seeds stay below 2^53, so they survive JSON in JavaScript
		// (the k6 tests) exactly and can be replayed from what they report.
		for seed == 0 {
			seed = rand.Uint64N(1 << 53)
		}
		return Generate(seed), nil
	}
	return nil, fmt.Errorf("unknown scenario %q; use classic or generated", mode)
}
