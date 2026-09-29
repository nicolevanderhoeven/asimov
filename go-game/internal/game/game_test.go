package game

import (
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
	for _, a := range []Action{{"cast", "fireball"}, {"move", "engineering; rescue crew"}, {"rescue", "crew"}, {"attack", "drone"}, {"inspect", "medical_records"}} {
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
	play(&s,Action{"scan", "sensors"}, sequence(t, 1))
	if s.Clues["frequency"] {
		t.Fatal("failed check revealed frequency")
	}
	play(&s,Action{"scan", "sensors"}, sequence(t, 10))
	if !s.Clues["frequency"] {
		t.Fatal("retry did not reveal frequency")
	}
}

func TestCombatAndCriticalDamage(t *testing.T) {
	s := New("test")
	s.Location = "engineering"
	// Player wins initiative, critically hits, and deals 6+4+2 damage.
	r := play(&s,Action{"attack", "drone"}, sequence(t, 15, 1, 20, 6, 4))
	if s.DroneHP != 0 || s.Combat || s.HP != 24 || len(r.Rolls) != 3 {
		t.Fatalf("bad critical or retaliation after defeat: %+v %+v", s, r)
	}
}

func TestDroneActsFirstAndCanDisableData(t *testing.T) {
	s := New("test")
	s.Location = "engineering"
	s.HP = 1
	r := play(&s,Action{"attack", "drone"}, sequence(t, 1, 20, 20, 4, 4))
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
	play(&s,Action{"bypass", "drone"}, sequence(t, 1, 15, 1))
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
	r := play(&s,Action{"isolate", "relay"}, sequence(t, 1, 6))
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
