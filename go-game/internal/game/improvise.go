package game

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// An Improvisation is the model's reading of a creative attempt that no listed
// action covers. Like Action, it has no DC, modifier, or outcome: the engine
// validates it against the current effect menu, sets the DC, and decides what
// success or failure does.
type Improvisation struct {
	Approach   string `json:"approach" jsonschema:"description=What Data attempts in one short sentence\\, in the player's terms"`
	Ability    string `json:"ability" jsonschema:"description=The ability the approach relies on: strength\\, dexterity\\, constitution\\, intelligence\\, wisdom\\, or charisma"`
	Skill      string `json:"skill,omitempty" jsonschema:"description=Optional 5e skill that fits the approach\\, such as athletics or sleight_of_hand; empty for a plain ability check"`
	Difficulty string `json:"difficulty" jsonschema:"description=easy\\, medium\\, or hard: how hard the approach itself is for a capable android"`
	Effect     string `json:"effect" jsonschema:"description=The id of the improvised_effects entry the attempt could achieve"`
}

// An Effect is one outcome an improvisation may aim for in the current state.
// The menu is authored per state, so a clever approach can find another way to
// an objective but never skip one: rescuing the crew is never on it.
type Effect struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	MinDC       int    `json:"min_dc,omitempty"`
	OnFailure   string `json:"on_failure,omitempty"`
	// Location is set on an effect only reachable somewhere else; aiming for
	// it takes the turbolift there first.
	Location string `json:"location,omitempty"`
}

// difficultyDC maps the model's rating of an approach to a DC. An effect's
// MinDC can raise it, never lower it.
var difficultyDC = map[string]int{"easy": 10, "medium": 15, "hard": 20}

// DifficultyDC is the DC for a difficulty tier, before any effect minimum.
func DifficultyDC(tier string) int { return difficultyDC[tier] }

var abilityAbbreviations = map[string]string{"str": "strength", "dex": "dexterity", "con": "constitution", "int": "intelligence", "wis": "wisdom", "cha": "charisma"}

// maxApproach bounds the model-supplied approach text, which is echoed into
// check labels, prompts, and telemetry.
const maxApproach = 160

func (s State) effects() []Effect {
	if s.Won || s.HP <= 0 {
		return nil
	}
	e := s.effectsAt(s.Location, s.Combat)
	for _, loc := range Locations {
		if loc != s.Location {
			for _, x := range s.effectsAt(loc, false) {
				x.Location = loc
				e = append(e, x)
			}
		}
	}
	if !s.Advantage {
		e = append(e, Effect{ID: "gain_advantage", Description: "Set up a later attempt: your next roll (a check, save, or phaser attack) has advantage.", MinDC: 10, OnFailure: "no advantage; in combat the drone still fires"})
	}
	return append(e, Effect{ID: "flavor", Description: "Anything else: an attempt with no mechanical effect, from sitting in the captain's chair to a long shot the scenario can't support yet. No roll; the GM narrates it and points to a way forward."})
}

// effectsAt lists the location-bound effects at loc, as they would be with
// Data there and, if combat is set, fighting the drone.
func (s State) effectsAt(loc string, combat bool) []Effect {
	switch {
	case combat:
		return []Effect{{ID: "damage_drone", Description: "Damage the drone by some means other than your phaser: 1d6 damage on a success.", MinDC: 15, OnFailure: "no damage; the drone fires either way unless disabled"}}
	case loc == "bridge" && !s.Clues["frequency"]:
		return []Effect{{ID: "recover_frequency", Description: "Recover the pulse frequency from the damaged sensor buffer by another method.", MinDC: 15, OnFailure: "nothing is recovered; you may try again"}}
	case loc == "engineering" && s.DroneHP > 0:
		return []Effect{{ID: "disable_drone", Description: "Disable the security drone without a fight.", MinDC: 20, OnFailure: "the drone activates and combat starts"}}
	}
	return nil
}

// details are authored, spoiler-free facts about the current scene that the
// GM may use to describe flavor actions and answer questions.
func (s State) details() []string {
	computer := "The ship's computer answers queries anywhere aboard, but reports no biological life signs and cannot say where the crew went."
	if s.Combat {
		return []string{
			"The drone is a fixed-mount security unit with a cracked casing. It tracks Data and fires short bursts.",
			"The phase relay's control panel is behind the drone.",
			computer,
		}
	}
	switch s.Location {
	case "bridge":
		return []string{
			"The captain's chair, conn, and ops stations are unoccupied. Every console is still powered.",
			"The main viewscreen shows the ship holding position.",
			"A turbolift at the rear of the bridge reaches every deck.",
			computer,
		}
	case "sickbay":
		return []string{
			"The biobeds are made up and unoccupied. A tray of instruments sits untouched.",
			"The door to the chief medical officer's office is open, and her desk console is on.",
			computer,
		}
	case "engineering":
		d := []string{
			"The warp core pulses steadily, and main power reads nominal.",
			"The phase relay is a temporary installation, cabled into a power conduit beside the core.",
			"Tools lie where the engineering crew set them down.",
		}
		if s.DroneHP > 0 {
			d = append(d, "A fixed-mount security drone with a cracked casing guards the relay's control panel. It has not fired.")
		} else {
			d = append(d, "The security drone hangs inert on its mount.")
		}
		return append(d, computer)
	}
	return []string{computer}
}

func title(s string) string {
	words := strings.Fields(strings.ReplaceAll(s, "_", " "))
	for i, w := range words {
		if w != "of" {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// normalize validates im against the character and the current effect menu,
// returning it in canonical form with the effect it aims for.
func (s State) normalize(im Improvisation) (Improvisation, Effect, error) {
	im.Approach = strings.Join(strings.Fields(im.Approach), " ")
	if r := []rune(im.Approach); len(r) > maxApproach {
		im.Approach = string(r[:maxApproach])
	}
	im.Ability = strings.ToLower(strings.TrimSpace(im.Ability))
	if full, ok := abilityAbbreviations[im.Ability]; ok {
		im.Ability = full
	}
	im.Skill = strings.NewReplacer(" ", "_", "-", "_").Replace(strings.ToLower(strings.TrimSpace(im.Skill)))
	if im.Skill == "none" {
		im.Skill = ""
	}
	im.Difficulty = strings.ToLower(strings.TrimSpace(im.Difficulty))
	im.Effect = strings.ToLower(strings.TrimSpace(im.Effect))
	c := Data()
	effects := s.effects()
	i := slices.IndexFunc(effects, func(e Effect) bool { return e.ID == im.Effect })
	switch {
	case im.Approach == "":
		return im, Effect{}, errors.New("An improvised attempt needs an approach: say what Data tries.")
	case i < 0:
		return im, Effect{}, errors.New("That effect isn't one the engine offers right now; nothing changes.")
	case effects[i].ID == "flavor":
		return im, effects[i], nil
	case c.Scores[im.Ability] == 0:
		return im, Effect{}, fmt.Errorf("%q is not an ability; use strength, dexterity, constitution, intelligence, wisdom, or charisma.", im.Ability)
	case im.Skill != "" && c.Skills[im.Skill] == "":
		return im, Effect{}, fmt.Errorf("%q is not a 5e skill.", im.Skill)
	case difficultyDC[im.Difficulty] == 0:
		return im, Effect{}, fmt.Errorf("%q is not a difficulty; use easy, medium, or hard.", im.Difficulty)
	}
	return im, effects[i], nil
}

// Improvise validates an improvised attempt. A flavor attempt resolves at once
// without a roll or a turn; anything else waits on /roll like a listed check.
// An invalid attempt changes nothing.
func (s *State) Improvise(im Improvisation) Result {
	finish := func(r Result, msg string) Result { r.Message = msg; r.State = s.View(); return r }
	if s.Won || s.HP <= 0 {
		return finish(Result{}, "This adventure has ended.")
	}
	im, effect, err := s.normalize(im)
	if err != nil {
		return finish(Result{}, err.Error())
	}
	r := Result{Allowed: true, Improvisation: &im}
	if effect.ID == "flavor" {
		return finish(r, "No mechanical effect; the GM narrates the attempt.")
	}
	prefix := s.travel(effect.Location)
	ability := title(im.Ability)
	label := ability
	aliases := []string{}
	for abbr, full := range abilityAbbreviations {
		if full == im.Ability {
			aliases = append(aliases, abbr)
		}
	}
	if im.Skill != "" {
		label += " (" + title(im.Skill) + ")"
		aliases = append(aliases, strings.Split(im.Skill, "_")...)
	}
	dc := max(difficultyDC[im.Difficulty], effect.MinDC)
	s.Pending = &PendingRoll{
		Action:        Action{Kind: "improvise", Target: effect.ID},
		Ability:       ability,
		Check:         label + ": " + im.Approach,
		Target:        dc,
		Command:       "/roll " + ability,
		Improvisation: &im,
		aliases:       aliases,
	}
	r.RollRequired = s.Pending
	msg := prefix + fmt.Sprintf("This needs a roll: %s, DC %d. Type %s to roll.", label, dc, s.Pending.Command)
	if dc > difficultyDC[im.Difficulty] {
		msg += fmt.Sprintf(" The approach itself is %s, but that outcome needs at least DC %d.", im.Difficulty, effect.MinDC)
	}
	return finish(r, msg)
}

// resolveImprovised applies an improvised attempt's effect once its check is
// rolled. p was validated when it became pending, and any other action since
// would have cleared it.
func (s *State) resolveImprovised(p *PendingRoll, roll Roller) Result {
	im := p.Improvisation
	t := s.newTurn(roll)
	t.r.Improvisation = im
	x := t.check(p.Check, Data().CheckBonus(im.Ability, im.Skill), p.Target, s.takeAdvantage(), false, false)
	switch im.Effect {
	case "recover_frequency":
		if x.Success {
			s.Clues["frequency"] = true
			return t.finish(clueText["frequency"])
		}
		return t.finish("The attempt yields no stable frequency. You may try again.")
	case "disable_drone":
		if x.Success {
			s.DroneHP = 0
			return t.finish("The attempt disables the security drone. The relay controls are accessible.")
		}
		t.startCombat()
		return t.finish("The attempt fails and the drone activates. Initiative determines whether it fires before your next action.")
	case "damage_drone":
		msg := "The attempt does no damage. "
		if x.Success {
			damage := roll(6)
			s.DroneHP = max(0, s.DroneHP-damage)
			if s.DroneHP == 0 {
				s.Combat = false
				return t.finish(fmt.Sprintf("The attempt deals %d damage and disables the drone.", damage))
			}
			msg = fmt.Sprintf("The attempt deals %d damage. ", damage)
		}
		t.droneAttack(false)
		return t.finish(msg + "The drone takes its next turn.")
	case "gain_advantage":
		msg := "The setup doesn't pay off."
		if x.Success {
			s.Advantage = true
			msg = "The setup works: your next roll has advantage."
		}
		if s.Combat {
			t.droneAttack(false)
			msg += " The drone takes its next turn."
		}
		return t.finish(msg)
	}
	panic("unhandled validated improvisation")
}

// Answer records a player question. Nothing happens and the turn does not
// advance; the GM answers from the view's facts.
func (s *State) Answer() Result {
	return Result{Allowed: true, Question: true, Message: "No action taken. The GM answers from what Data knows.", State: s.View()}
}
