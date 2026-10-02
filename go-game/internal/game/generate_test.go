package game

import (
	"encoding/json"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

// everyVariant builds one scenario for every combination of modules.
func everyVariant() []*Scenario {
	var all []*Scenario
	for _, c := range causes {
		for _, rd := range readings {
			for _, rc := range records {
				for _, en := range encounters {
					for _, hz := range hazards {
						all = append(all, assemble(uint64(len(all)), c, rd, rc, en, hz))
					}
				}
			}
		}
	}
	return all
}

func TestEveryVariantIsValid(t *testing.T) {
	all := everyVariant()
	if len(all) != Variants() {
		t.Fatalf("built %d variants, want %d", len(all), Variants())
	}
	seen := map[string]bool{}
	for _, sc := range all {
		if err := sc.Check(); err != nil {
			t.Fatalf("%s: %v", sc.Variant(), err)
		}
		if seen[sc.Variant()] {
			t.Fatalf("variant %s built twice", sc.Variant())
		}
		seen[sc.Variant()] = true
		b, _ := json.Marshal(sc)
		for _, p := range []string{"{device}", "{pattern}", "{noun}", "{room}"} {
			if strings.Contains(string(b), p) {
				t.Fatalf("%s: unreplaced placeholder %s", sc.Variant(), p)
			}
		}
	}
}

func TestClassicIsValid(t *testing.T) {
	if err := Classic().Check(); err != nil {
		t.Fatal(err)
	}
	if got := Classic().GMPurposes(); !slices.Equal(got, []string{"drone_initiative", "drone_attack", "drone_damage", "discharge_damage"}) {
		t.Fatal(got)
	}
}

func TestGenerateIsDeterministic(t *testing.T) {
	a, _ := json.Marshal(Generate(42))
	b, _ := json.Marshal(Generate(42))
	if string(a) != string(b) {
		t.Fatal("the same seed built different scenarios")
	}
	variants := map[string]bool{}
	for seed := range uint64(200) {
		variants[Generate(seed).Variant()] = true
	}
	if len(variants) < 50 {
		t.Fatalf("200 seeds built only %d variants", len(variants))
	}
}

// solve plays sc to the rescue with every die rolled high, following the
// leads the way a player would: the first action listed here or elsewhere
// that isn't done yet.
func solve(t *testing.T, sc *Scenario) State {
	t.Helper()
	s := NewScenario("test", sc)
	high := func(sides int) int { return sides }
	for step := 0; step < 40 && !s.Won; step++ {
		v := s.View()
		opts := append(append([]Option{}, v.Actions...), v.Elsewhere...)
		i := slices.IndexFunc(opts, func(o Option) bool {
			switch o.Kind {
			case "move", "attack", "dodge", "retreat":
				return false
			case "inspect", "scan":
				return !s.Clues[sc.clueFor(o.Action).Key]
			}
			return true
		})
		if i < 0 {
			t.Fatalf("%s: stuck at step %d: %+v", sc.Variant(), step, v)
		}
		if r := play(&s, opts[i].Action, high); !r.Allowed {
			t.Fatalf("%s: %+v refused: %s", sc.Variant(), opts[i].Action, r.Message)
		}
	}
	return s
}

func TestEveryVariantCanBeWon(t *testing.T) {
	for _, sc := range everyVariant() {
		s := solve(t, sc)
		if !s.Won || s.HP != Data().MaxHP {
			t.Fatalf("%s: not won: %+v", sc.Variant(), s)
		}
		if v := s.View(); v.Status != "rescued" || v.Description != sc.Rescue.Ending {
			t.Fatalf("%s: ending view %+v", sc.Variant(), v)
		}
	}
}

func TestGeneratedInitialViewHasNoSpoilers(t *testing.T) {
	for _, sc := range everyVariant() {
		v := NewScenario("test", sc).View().JSON()
		for _, c := range sc.Clues {
			if strings.Contains(v, c.Text) {
				t.Fatalf("%s: initial view leaks clue %s", sc.Variant(), c.Key)
			}
		}
		if strings.Contains(v, sc.Fix.Done) || strings.Contains(v, sc.Rescue.Success) {
			t.Fatalf("%s: initial view leaks the ending", sc.Variant())
		}
	}
}

// Random play with random dice must never break the engine's rules.
func TestGeneratedRandomPlayKeepsTheRules(t *testing.T) {
	for walk := range 600 {
		rng := rand.New(rand.NewPCG(uint64(walk), 11))
		roll := func(sides int) int { return rng.IntN(sides) + 1 }
		sc := Generate(uint64(walk))
		s := NewScenario("test", sc)
		for step := 0; step < 60 && !s.Won && s.HP > 0; step++ {
			switch {
			case s.Pending != nil && s.Pending.By == ByPlayer:
				s.Roll(s.Pending.Ability, roll)
			case s.Pending != nil:
				if rng.IntN(5) == 0 {
					s.SkipGMRoll()
				} else {
					s.RollForGM(roll)
				}
			default:
				v := s.View()
				opts := append(append([]Option{}, v.Actions...), v.Elsewhere...)
				if n := rng.IntN(len(opts) + len(v.Effects)); n < len(opts) {
					s.Apply(opts[n].Action, Ruling{NoRoll: rng.IntN(6) == 0})
				} else {
					s.Improvise(Improvisation{Approach: "try something", Ability: "intelligence", Difficulty: "medium", Effect: v.Effects[n-len(opts)].ID}, Ruling{})
				}
			}
			if s.HP < 0 || s.HP > Data().MaxHP || s.FoeHP < 0 || s.Progress > sc.Encounter.Needed {
				t.Fatalf("%s: broke a rule: %+v", sc.Variant(), s)
			}
			if s.Won && (!s.ready() || !s.Isolated) {
				t.Fatalf("%s: won without the evidence", sc.Variant())
			}
			if s.Pending != nil && s.Pending.By == ByGM && !slices.Contains(sc.GMPurposes(), s.Pending.Purpose) {
				t.Fatalf("%s: GM roll %s isn't one of the scenario's purposes %v", sc.Variant(), s.Pending.Purpose, sc.GMPurposes())
			}
			if v := s.View(); v.Status == "playing" && (v.Encounter == nil || v.DroneHP != 0) {
				t.Fatalf("%s: generated view must report the encounter, not drone_hp", sc.Variant())
			}
		}
	}
}

func challengeScenario(t *testing.T) *Scenario {
	t.Helper()
	i := slices.IndexFunc(encounters, func(e encounterModule) bool { return e.id == "containment_field" })
	return assemble(1, causes[0], readings[0], records[0], encounters[i], hazards[0])
}

func TestSkillChallengeNeedsSuccessesAndHurtsOnFailure(t *testing.T) {
	sc := challengeScenario(t)
	s := NewScenario("test", sc)
	s.Location = sc.Encounter.Location
	if slices.ContainsFunc(s.View().Actions, func(o Option) bool { return o.Kind == "isolate" || o.Kind == "attack" }) {
		t.Fatal("isolate or attack offered before the challenge is cleared")
	}
	// A failure: the player's check fails, then the GM rolls the hazard.
	r := s.Apply(Action{"bypass", "power_feed"}, Ruling{})
	if r.RollRequired == nil || r.RollRequired.Command != "/roll Dexterity" || !r.RollRequired.accepts("dex sleight of hand") {
		t.Fatalf("expected a Dexterity (Sleight of Hand) roll: %+v", r.RollRequired)
	}
	r = s.Roll("Dexterity", sequence(t, 2))
	if r.GMRollRequired == nil || r.GMRollRequired.Purpose != "field_damage" || r.GMRollRequired.Notation != "1d4" {
		t.Fatalf("expected the GM's field_damage roll: %+v", r)
	}
	r = s.RollForGM(sequence(t, 3))
	if s.HP != Data().MaxHP-3 || s.Progress != 0 || s.Combat {
		t.Fatalf("failure: HP %d, progress %d", s.HP, s.Progress)
	}
	// Three successes clear it, from any mix of approaches.
	for i, target := range []string{"field_emitters", "emitter_housing", "field_emitters"} {
		r = play(&s, Action{"bypass", target}, func(int) int { return 20 })
		if s.Progress != i+1 {
			t.Fatalf("success %d: progress %d: %s", i+1, s.Progress, r.Message)
		}
	}
	v := s.View()
	if v.Encounter == nil || v.Encounter.Status != "cleared" || v.Encounter.Successes != 3 {
		t.Fatalf("encounter view %+v", v.Encounter)
	}
	if !slices.ContainsFunc(v.Actions, func(o Option) bool { return o.Action == Action{"isolate", sc.Fix.Target} }) {
		t.Fatalf("fix not offered once cleared: %+v", v.Actions)
	}
	if slices.ContainsFunc(v.Actions, func(o Option) bool { return o.Kind == "bypass" }) {
		t.Fatal("challenge still offered once cleared")
	}
}

func TestSkillChallengeImprovisationCountsAsASuccess(t *testing.T) {
	sc := challengeScenario(t)
	s := NewScenario("test", sc)
	s.Location = sc.Encounter.Location
	im := Improvisation{Approach: "vent the field into the hull", Ability: "intelligence", Difficulty: "easy", Effect: sc.Encounter.Advance.ID}
	r := drive(&s, s.Improvise(im, Ruling{}), func(int) int { return 20 })
	if !r.Allowed || s.Progress != 1 {
		t.Fatalf("progress %d: %s", s.Progress, r.Message)
	}
}

func TestGeneratedCombatUsesTheFoesStatistics(t *testing.T) {
	i := slices.IndexFunc(encounters, func(e encounterModule) bool { return e.id == "exocomp" })
	sc := assemble(1, causes[1], readings[1], records[1], encounters[i], hazards[1])
	s := NewScenario("test", sc)
	s.Location = sc.Encounter.Location
	// Data's initiative 1, the exocomp's 19: it strikes first and hits.
	r := s.Apply(Action{"attack", "exocomp"}, Ruling{})
	r = s.Roll("initiative", sequence(t, 1))
	if r.GMRollRequired == nil || r.GMRollRequired.Purpose != "exocomp_initiative" || r.GMRollRequired.Notation != "1d20+3" {
		t.Fatalf("%+v", r.GMRollRequired)
	}
	r = s.RollForGM(sequence(t, 16))
	if r.GMRollRequired == nil || r.GMRollRequired.Purpose != "exocomp_attack" || r.GMRollRequired.Notation != "1d20+4" {
		t.Fatalf("%+v", r.GMRollRequired)
	}
	r = s.RollForGM(sequence(t, 15))
	if r.GMRollRequired == nil || r.GMRollRequired.Notation != "1d4+2" {
		t.Fatalf("%+v", r.GMRollRequired)
	}
	r = s.RollForGM(sequence(t, 4))
	if s.HP != Data().MaxHP-6 || !s.Combat || s.FoeHP != 8 {
		t.Fatalf("HP %d, foe %d", s.HP, s.FoeHP)
	}
	if v := s.View(); v.Encounter.HP != 8 || v.Encounter.Status != "active" || !strings.Contains(v.Description, "exocomp") {
		t.Fatalf("%+v", v.Encounter)
	}
}
