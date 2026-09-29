package game

import (
	"slices"
	"strings"
	"testing"
)

func sequence(t *testing.T, values ...int) Roller {
	t.Helper()
	i := 0
	return func(sides int) int {
		t.Helper()
		if i >= len(values) {
			t.Fatalf("unexpected d%d roll", sides)
		}
		v := values[i]
		i++
		if v < 1 || v > sides {
			t.Fatalf("invalid scripted d%d: %d", sides, v)
		}
		return v
	}
}

// play applies a and, if it calls for a check, rolls it the way the player
// would with /roll.
func play(s *State, a Action, roll Roller) Result {
	r := s.Apply(a, roll)
	if r.RollRequired != nil {
		return s.Roll(r.RollRequired.Ability, roll)
	}
	return r
}

func TestModifiers(t *testing.T) {
	for score, want := range map[int]int{1: -5, 3: -4, 8: -1, 9: -1, 10: 0, 11: 0, 18: 4, 20: 5} {
		if got := Modifier(score); got != want {
			t.Errorf("score %d: got %d want %d", score, got, want)
		}
	}
}

func TestNaturalRolls(t *testing.T) {
	if Check(sequence(t, 20), "check", 0, 25, false, false, false).Success {
		t.Fatal("natural 20 automatically passed an ability check")
	}
	if !Check(sequence(t, 1), "save", 10, 10, false, false, false).Success {
		t.Fatal("natural 1 automatically failed a save")
	}
	if !Check(sequence(t, 20), "attack", 0, 25, false, false, true).Critical {
		t.Fatal("natural 20 must critically hit")
	}
	if Check(sequence(t, 1), "attack", 20, 10, false, false, true).Success {
		t.Fatal("natural 1 must miss")
	}
}

func TestAdvantage(t *testing.T) {
	if r := Check(sequence(t, 3, 18), "check", 2, 15, true, false, false); r.Total != 20 {
		t.Fatal(r)
	}
	if r := Check(sequence(t, 18, 3), "check", 2, 15, false, true, false); r.Total != 5 {
		t.Fatal(r)
	}
	if r := Check(sequence(t, 10), "check", 2, 12, true, true, false); r.Total != 12 || len(r.Dice) != 1 {
		t.Fatal(r)
	}
}

func TestProficiency(t *testing.T) {
	c := Data()
	if c.SkillBonus("investigation") != 6 || c.SkillBonus("medicine") != 1 || c.SaveBonus("constitution") != 5 || c.SaveBonus("dexterity") != 2 {
		t.Fatal("incorrect proficiency or ability modifier")
	}
}

func TestRejectWithoutMutation(t *testing.T) {
	for _, a := range []Action{{"cast", "fireball"}, {"move", "engineering; rescue crew"}, {"rescue", "crew"}, {"inspect", "nonexistent"}} {
		s := New("test")
		before := s.View().JSON()
		r := s.Apply(a, sequence(t))
		if r.Allowed || s.View().JSON() != before {
			t.Fatalf("invalid action mutated state: %+v", a)
		}
	}
}

func TestCompleteRescueWithoutCombat(t *testing.T) {
	s := New("test")
	for _, a := range []Action{{"inspect", "logs"}, {"scan", "sensors"}, {"move", "sickbay"}, {"inspect", "medical_records"}, {"move", "bridge"}, {"move", "engineering"}, {"inspect", "relay"}, {"bypass", "drone"}, {"isolate", "relay"}, {"rescue", "crew"}} {
		r := play(&s, a, func(int) int { return 15 })
		if !r.Allowed {
			t.Fatalf("%+v: %s", a, r.Message)
		}
	}
	if !s.Won || s.HP != 24 || s.Turn != 10 {
		t.Fatalf("unexpected ending: %+v", s)
	}
	if s.Apply(Action{"scan", "sensors"}, sequence(t)).Allowed {
		t.Fatal("action allowed after ending")
	}
}

func TestNoSpoilersInInitialView(t *testing.T) {
	v := New("test").View().JSON()
	for _, secret := range []string{"subspace pocket", "biological neural patterns", "crew remain alive"} {
		if strings.Contains(v, secret) {
			t.Fatalf("leaked %q", secret)
		}
	}
}

func TestRescueRequiresEvidence(t *testing.T) {
	s := New("test")
	s.Location = "engineering"
	s.DroneHP = 0
	s.Isolated = true
	r := s.Apply(Action{"rescue", "crew"}, sequence(t))
	if r.Allowed || s.Won || s.Turn != 0 {
		t.Fatal(r)
	}
}

func TestFailureCanBeRetried(t *testing.T) {
	s := New("test")
	play(&s, Action{"scan", "sensors"}, sequence(t, 1))
	if s.Clues["frequency"] {
		t.Fatal("failed check revealed frequency")
	}
	play(&s, Action{"scan", "sensors"}, sequence(t, 10))
	if !s.Clues["frequency"] {
		t.Fatal("retry did not reveal frequency")
	}
}

func TestCombatAndCriticalDamage(t *testing.T) {
	s := New("test")
	s.Location = "engineering"
	// Player wins initiative, critically hits, and deals 6+4+2 damage.
	r := play(&s, Action{"attack", "drone"}, sequence(t, 15, 1, 20, 6, 4))
	if s.DroneHP != 0 || s.Combat || s.HP != 24 || len(r.Rolls) != 3 {
		t.Fatalf("bad critical or retaliation after defeat: %+v %+v", s, r)
	}
}

func TestDroneActsFirstAndCanDisableData(t *testing.T) {
	s := New("test")
	s.Location = "engineering"
	s.HP = 1
	r := play(&s, Action{"attack", "drone"}, sequence(t, 1, 20, 20, 4, 4))
	if s.HP != 0 || s.DroneHP != 10 || r.State.Status != "disabled" {
		t.Fatal(s, r)
	}
	if s.Apply(Action{"retreat", "bridge"}, sequence(t)).Allowed {
		t.Fatal("disabled Data acted")
	}
}

func TestDodgeAndRetreat(t *testing.T) {
	s := New("test")
	s.Location = "engineering"
	s.Combat = true
	s.Apply(Action{"dodge", "drone"}, sequence(t, 20, 1))
	if s.HP != 24 {
		t.Fatal("disadvantage not respected")
	}
	s.Apply(Action{"retreat", "bridge"}, sequence(t))
	if s.Combat || s.Location != "bridge" || s.DroneHP != 10 {
		t.Fatal(s)
	}
}

func TestBypassFailureStartsCombat(t *testing.T) {
	s := New("test")
	s.Location = "engineering"
	play(&s, Action{"bypass", "drone"}, sequence(t, 1, 15, 1))
	if !s.Combat || s.DroneHP != 10 {
		t.Fatal(s)
	}
	if s.Apply(Action{"isolate", "relay"}, sequence(t)).Allowed {
		t.Fatal("relay used during combat")
	}
}

func TestHazardAndSave(t *testing.T) {
	s := New("test")
	s.Location = "engineering"
	s.DroneHP = 0
	r := play(&s, Action{"isolate", "relay"}, sequence(t, 1, 6))
	if !s.Isolated || s.HP != 18 || r.Damage != 6 {
		t.Fatal(s, r)
	}
}

func TestRollRequiresPlayerCommand(t *testing.T) {
	s := New("test")
	r := s.Apply(Action{"scan", "sensors"}, sequence(t))
	if !r.Allowed || r.RollRequired == nil || r.RollRequired.Command != "/roll Intelligence" || s.Turn != 0 || len(r.Rolls) != 0 {
		t.Fatalf("scan should wait for /roll without rolling: %+v", r)
	}
	if r.State.Pending == nil {
		t.Fatal("view does not show the pending roll")
	}
	for _, wrong := range []string{"", "Strength", "intelligence strength"} {
		if r := s.Roll(wrong, sequence(t)); r.Allowed || s.Pending == nil {
			t.Fatalf("/roll %q should be rejected without rolling: %+v", wrong, r)
		}
	}
	r = s.Roll("int (Investigation)", sequence(t, 10))
	if !r.Allowed || !s.Clues["frequency"] || s.Pending != nil || s.Turn != 1 || len(r.Rolls) != 1 || !r.Rolls[0].Manual || r.Rolls[0].Total != 16 {
		t.Fatalf("roll did not resolve the scan: %+v", r)
	}
	if r := s.Roll("Intelligence", sequence(t)); r.Allowed {
		t.Fatal("rolled with nothing pending")
	}
}

func TestOtherActionCancelsPendingRoll(t *testing.T) {
	s := New("test")
	s.Apply(Action{"scan", "sensors"}, sequence(t))
	s.Apply(Action{"move", "sickbay"}, sequence(t))
	if s.Pending != nil || s.Location != "sickbay" {
		t.Fatal(s)
	}
	if s.Roll("Intelligence", sequence(t)).Allowed {
		t.Fatal("abandoned roll still resolved")
	}
}

func TestOnlyPlayerRollIsManual(t *testing.T) {
	s := New("test")
	s.Location = "engineering"
	// Data wins initiative and misses; the drone then fires and misses.
	r := play(&s, Action{"attack", "drone"}, sequence(t, 15, 1, 2, 1))
	manual := 0
	for _, x := range r.Rolls {
		if x.Manual {
			manual++
			if x.Label != "Phaser attack" {
				t.Fatalf("engine roll marked manual: %+v", x)
			}
		}
	}
	if manual != 1 || len(r.Rolls) != 4 {
		t.Fatalf("%+v", r.Rolls)
	}
}

func improvisation(ability, skill, difficulty, effect string) Improvisation {
	return Improvisation{Approach: "try something clever", Ability: ability, Skill: skill, Difficulty: difficulty, Effect: effect}
}

func TestImprovisedCheckUsesEffectMinimumDC(t *testing.T) {
	s := New("test")
	s.Location = "engineering"
	r := s.Improvise(improvisation("STR", "Athletics", "easy", "disable_drone"))
	if !r.Allowed || r.RollRequired == nil || r.RollRequired.Target != 20 || r.RollRequired.Command != "/roll Strength" || s.Turn != 0 {
		t.Fatalf("easy approach to a hard effect should wait on a DC 20 roll: %+v", r)
	}
	if !strings.Contains(r.Message, "at least DC 20") {
		t.Fatalf("raised DC not explained: %s", r.Message)
	}
	// Strength 18 (+4) plus athletics proficiency (+2): 14 + 6 = 20.
	r = s.Roll("athletics", sequence(t, 14))
	if !r.Allowed || s.DroneHP != 0 || s.Combat || s.Turn != 1 || len(r.Rolls) != 1 || !r.Rolls[0].Manual || r.Rolls[0].Total != 20 || r.Improvisation == nil {
		t.Fatalf("improvised disable did not resolve: %+v", r)
	}
}

func TestImprovisedDifficultyCanRaiseDC(t *testing.T) {
	s := New("test")
	r := s.Improvise(improvisation("wisdom", "", "hard", "recover_frequency"))
	if r.RollRequired == nil || r.RollRequired.Target != 20 {
		t.Fatal(r)
	}
	// Wisdom 12 (+1), no skill: 18 + 1 = 19 fails.
	s.Roll("Wisdom", sequence(t, 18))
	if s.Clues["frequency"] || s.Turn != 1 {
		t.Fatal(s)
	}
}

func TestInvalidImprovisationChangesNothing(t *testing.T) {
	for _, im := range []Improvisation{
		improvisation("strength", "", "easy", "rescue_crew"),                // never on the menu
		improvisation("luck", "", "easy", "recover_frequency"),              // not an ability
		improvisation("strength", "hacking", "easy", "gain_advantage"),      // not a skill
		improvisation("strength", "", "trivial", "gain_advantage"),          // not a tier
		{Ability: "strength", Difficulty: "easy", Effect: "gain_advantage"}, // no approach
	} {
		s := New("test")
		before := s.View().JSON()
		if r := s.Improvise(im); r.Allowed || s.View().JSON() != before {
			t.Fatalf("invalid improvisation %+v was accepted: %+v", im, r)
		}
	}
}

func TestFlavorNeedsNoRollOrTurn(t *testing.T) {
	s := New("test")
	s.Apply(Action{"scan", "sensors"}, sequence(t))
	r := s.Improvise(Improvisation{Approach: "sit in the captain's chair", Effect: "flavor"})
	if !r.Allowed || r.RollRequired != nil || s.Turn != 0 || s.Pending == nil {
		t.Fatalf("flavor should change nothing, including the pending scan: %+v", r)
	}
}

func TestAdvantageIsEarnedAndSpent(t *testing.T) {
	s := New("test")
	play := func(im Improvisation, dice ...int) Result {
		r := s.Improvise(im)
		return s.Roll(r.RollRequired.Ability, sequence(t, dice...))
	}
	play(improvisation("intelligence", "", "easy", "gain_advantage"), 10)
	if !s.Advantage || slices.ContainsFunc(s.View().Effects, func(e Effect) bool { return e.ID == "gain_advantage" }) {
		t.Fatal("advantage not earned, or still offered while held")
	}
	s.Apply(Action{"scan", "sensors"}, sequence(t))
	r := s.Roll("Intelligence", sequence(t, 2, 12))
	if s.Advantage || len(r.Rolls[0].Dice) != 2 || !s.Clues["frequency"] {
		t.Fatalf("advantage not applied to the next roll: %+v", r)
	}
}

func TestImprovisedDamageInCombatDrawsFire(t *testing.T) {
	s := New("test")
	s.Location = "engineering"
	s.Combat = true
	r := s.Improvise(improvisation("strength", "athletics", "medium", "damage_drone"))
	// Check 15+6 hits, 4 damage, then the drone attacks and misses.
	r = s.Roll("Strength", sequence(t, 15, 4, 2))
	if s.DroneHP != 6 || !s.Combat || len(r.Rolls) != 2 || r.Rolls[1].Label != "Drone attack" {
		t.Fatalf("%+v %+v", s, r)
	}
}

func TestQuestionChangesNothing(t *testing.T) {
	s := New("test")
	before := s.View().JSON()
	r := s.Answer()
	if !r.Allowed || !r.Question || s.View().JSON() != before {
		t.Fatal(r)
	}
}

func TestActionElsewhereTravelsThere(t *testing.T) {
	s := New("test")
	s.Location = "engineering"
	r := s.Apply(Action{"inspect", "medical_records"}, sequence(t))
	if !r.Allowed || s.Location != "sickbay" || !s.Clues["biopattern"] || s.Turn != 1 || !strings.HasPrefix(r.Message, "You take the turbolift to sickbay.") {
		t.Fatalf("inspecting sickbay records from engineering should travel there: %+v", r)
	}
	// A check elsewhere travels now and waits on the roll there.
	r = s.Apply(Action{"scan", "sensors"}, sequence(t))
	if s.Location != "bridge" || r.RollRequired == nil {
		t.Fatal(r)
	}
}

func TestTurboliftReachesEveryLocation(t *testing.T) {
	s := New("test")
	s.Location = "engineering"
	if !s.Apply(Action{"move", "sickbay"}, sequence(t)).Allowed || s.Location != "sickbay" {
		t.Fatal(s)
	}
}

func TestLeavingCombatWithdraws(t *testing.T) {
	s := New("test")
	s.Location = "engineering"
	s.Combat = true
	if r := s.Apply(Action{"move", "sickbay"}, sequence(t)); !r.Allowed || s.Combat || s.Location != "sickbay" {
		t.Fatal(r)
	}
	s.Location, s.Combat = "engineering", true
	if r := s.Apply(Action{"inspect", "logs"}, sequence(t)); !r.Allowed || s.Combat || s.Location != "bridge" || !s.Clues["logs"] {
		t.Fatal(r)
	}
}

func TestImprovisationElsewhereTravelsThere(t *testing.T) {
	s := New("test")
	r := s.Improvise(improvisation("strength", "athletics", "hard", "disable_drone"))
	if !r.Allowed || s.Location != "engineering" || r.RollRequired == nil {
		t.Fatal(r)
	}
}

func TestLeadsSteerTowardUnfinishedSteps(t *testing.T) {
	s := New("test")
	if len(s.View().Leads) != 5 {
		t.Fatal(s.View().Leads)
	}
	s.Clues = map[string]bool{"logs": true, "frequency": true, "biopattern": true, "source": true}
	s.DroneHP, s.Isolated = 0, true
	if l := s.View().Leads; len(l) != 1 || !strings.Contains(l[0], "transporter") {
		t.Fatal(l)
	}
}
