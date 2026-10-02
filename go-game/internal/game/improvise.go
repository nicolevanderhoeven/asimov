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
	for _, loc := range s.Scenario().Locations {
		if loc != s.Location {
			for _, x := range s.effectsAt(loc, false) {
				x.Location = loc
				e = append(e, x)
			}
		}
	}
	if !s.Advantage {
		enc := s.Scenario().Encounter
		failure := "no advantage"
		if enc.Kind == Combat {
			failure = fmt.Sprintf("no advantage; in combat the %s still %s", enc.Short, enc.Fires)
		}
		e = append(e, Effect{ID: "gain_advantage", Description: "Set up a later attempt: your next roll (a check, save, or phaser attack) has advantage.", MinDC: 10, OnFailure: failure})
	}
	return append(e, Effect{ID: "flavor", Description: "Anything else: an attempt with no mechanical effect, from sitting in the captain's chair to a long shot the scenario can't support yet. No roll; the GM narrates it and points to a way forward."})
}

// effectsAt lists the location-bound effects at loc, as they would be with
// Data there and, if combat is set, fighting the encounter's foe.
func (s State) effectsAt(loc string, combat bool) []Effect {
	sc := s.Scenario()
	e := sc.Encounter
	if combat {
		return []Effect{{ID: "damage_" + e.Target, Description: fmt.Sprintf("Damage the %s by some means other than your phaser: 1d6 damage on a success.", e.Short), MinDC: 15, OnFailure: fmt.Sprintf("no damage; the %s %s either way unless disabled", e.Short, e.Fires)}}
	}
	var o []Effect
	for _, c := range sc.Clues {
		if c.Location == loc && c.Effect != nil && !s.Clues[c.Key] {
			o = append(o, *c.Effect)
		}
	}
	if e.Location == loc && !s.cleared() {
		if e.Kind == Combat {
			o = append(o, Effect{ID: "disable_" + e.Target, Description: fmt.Sprintf("Disable the %s without a fight.", e.Name), MinDC: 20, OnFailure: fmt.Sprintf("the %s activates and combat starts", e.Short)})
		} else {
			o = append(o, *e.Advance)
		}
	}
	return o
}

// details are authored, spoiler-free facts about the current scene that the
// GM may use to describe flavor actions and answer questions.
func (s State) details() []string {
	sc := s.Scenario()
	e := sc.Encounter
	if s.Combat {
		return append(slices.Clone(e.SceneDetails), sc.Computer)
	}
	room, ok := sc.Rooms[s.Location]
	if !ok {
		return []string{sc.Computer}
	}
	d := slices.Clone(room.Details)
	if e.Location == s.Location {
		if s.cleared() {
			d = append(d, e.ClearedDetail)
		} else {
			d = append(d, e.ActiveDetail)
		}
	}
	return append(d, sc.Computer)
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

// Improvise validates an improvised attempt, under the GM's ruling on its
// check. A flavor attempt resolves at once without a roll or a turn;
// anything else runs like a listed action. An invalid attempt changes
// nothing.
func (s *State) Improvise(im Improvisation, ruling Ruling) Result {
	finish := func(r Result, msg string) Result { r.Message = msg; r.State = s.View(); return r }
	if s.Won || s.HP <= 0 {
		return finish(Result{}, "This adventure has ended.")
	}
	im, effect, err := s.normalize(im)
	if err != nil {
		return finish(Result{}, err.Error())
	}
	if effect.ID == "flavor" {
		return finish(Result{Allowed: true, Improvisation: &im}, "No mechanical effect; the GM narrates the attempt.")
	}
	if s.locked() {
		return finish(Result{RollRequired: s.Pending}, s.lockedMessage())
	}
	s.ip, s.Pending = nil, nil
	prefix := s.travel(effect.Location)
	aliases := []string{}
	for abbr, full := range abilityAbbreviations {
		if full == im.Ability {
			aliases = append(aliases, abbr)
		}
	}
	if im.Skill != "" {
		aliases = append(aliases, strings.Split(im.Skill, "_")...)
	}
	r := s.start(&inProgress{action: Action{Kind: "improvise", Target: effect.ID}, im: &im, check: checkInfo{title(im.Ability), aliases}, ruling: ruling}, prefix)
	if dc := improvisedDC(im, effect); r.RollRequired != nil && r.RollRequired.Purpose == "check" && dc > difficultyDC[im.Difficulty] {
		r.Message += fmt.Sprintf(" The approach itself is %s, but that outcome needs at least DC %d.", im.Difficulty, effect.MinDC)
	}
	return r
}

// improvisedDC is the higher of the approach's difficulty and the effect's
// minimum.
func improvisedDC(im Improvisation, effect Effect) int {
	return max(difficultyDC[im.Difficulty], effect.MinDC)
}

// improvisedCheck labels an improvised check: the ability, the skill if any,
// and the approach.
func improvisedCheck(im Improvisation) string {
	label := title(im.Ability)
	if im.Skill != "" {
		label += " (" + title(im.Skill) + ")"
	}
	return label + ": " + im.Approach
}

// resolveImprovised applies an improvised attempt's effect. The attempt was
// validated when it started, against the same state it replays from.
func (s *State) resolveImprovised(t *turn) Result {
	im := t.ip.im
	i := slices.IndexFunc(s.effects(), func(e Effect) bool { return e.ID == im.Effect })
	effect := s.effects()[i]
	s.Turn++
	t.r.Improvisation = im
	x := t.check(improvisedCheck(*im), Data().CheckBonus(im.Ability, im.Skill), improvisedDC(*im, effect), false)
	sc := s.Scenario()
	enc := sc.Encounter
	for _, c := range sc.Clues {
		if c.Effect != nil && im.Effect == c.Effect.ID {
			if x.Success {
				s.Clues[c.Key] = true
				return t.finish(c.Text)
			}
			return t.finish(c.EffectRetry)
		}
	}
	switch im.Effect {
	case "disable_" + enc.Target:
		if x.Success {
			s.FoeHP = 0
			return t.finish(fmt.Sprintf("The attempt disables the %s. %s", enc.Name, enc.Access))
		}
		t.r.Message = fmt.Sprintf("The attempt fails and the %s activates.", enc.Short)
		t.startCombat()
		return t.finish(t.r.Message + " Initiative determines whether it fires before your next action.")
	case "damage_" + enc.Target:
		msg := "The attempt does no damage."
		if x.Success {
			damage := t.damage("Improvised damage", "1d6")
			s.FoeHP = max(0, s.FoeHP-damage)
			if s.FoeHP == 0 {
				s.Combat = false
				return t.finish(fmt.Sprintf("The attempt deals %d damage and disables the %s.", damage, enc.Short))
			}
			msg = fmt.Sprintf("The attempt deals %d damage.", damage)
		}
		t.r.Message = msg
		t.foeAttack(false)
		return t.finish(msg + t.foeTurn())
	case "gain_advantage":
		msg := "The setup doesn't pay off."
		if x.Success {
			s.Advantage = true
			msg = "The setup works: your next roll has advantage."
		}
		if s.Combat {
			t.r.Message = msg
			t.foeAttack(false)
			msg += t.foeTurn()
		}
		return t.finish(msg)
	}
	if enc.Advance != nil && im.Effect == enc.Advance.ID {
		return t.challenge(x)
	}
	panic("unhandled validated improvisation")
}

// Answer records a player question. Nothing happens and the turn does not
// advance; the GM answers from the view's facts.
func (s *State) Answer() Result {
	return Result{Allowed: true, Question: true, Message: "No action taken. The GM answers from what Data knows.", State: s.View()}
}
