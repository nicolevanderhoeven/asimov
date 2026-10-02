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

// drive makes every roll r waits on with roll, the player's with /roll and
// the GM's as roll_dice would, and returns the last result with every step's
// rolls and damage.
func drive(s *State, r Result, roll Roller) Result {
	rolls, damage := r.Rolls, r.Damage
	for r.RollRequired != nil || r.GMRollRequired != nil {
		if r.RollRequired != nil {
			r = s.Roll(r.RollRequired.Ability, roll)
		} else {
			r = s.RollForGM(roll)
		}
		rolls, damage = append(rolls, r.Rolls...), damage+r.Damage
	}
	r.Rolls, r.Damage = rolls, damage
	return r
}

// play applies a and makes every roll it needs.
func play(s *State, a Action, roll Roller) Result {
	return drive(s, s.Apply(a, Ruling{}), roll)
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
		r := s.Apply(a, Ruling{})
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
	if s.Apply(Action{"scan", "sensors"}, Ruling{}).Allowed {
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
	s.FoeHP = 0
	s.Isolated = true
	r := s.Apply(Action{"rescue", "crew"}, Ruling{})
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
	if s.FoeHP != 0 || s.Combat || s.HP != 24 || len(r.Rolls) != 4 || r.Rolls[3].Notation != "2d6+2" {
		t.Fatalf("bad critical or retaliation after defeat: %+v %+v", s, r)
	}
}

func TestDroneActsFirstAndCanDisableData(t *testing.T) {
	s := New("test")
	s.Location = "engineering"
	s.HP = 1
	r := play(&s, Action{"attack", "drone"}, sequence(t, 1, 20, 20, 4, 4))
	if s.HP != 0 || s.FoeHP != 10 || r.State.Status != "disabled" {
		t.Fatal(s, r)
	}
	if s.Apply(Action{"retreat", "bridge"}, Ruling{}).Allowed {
		t.Fatal("disabled Data acted")
	}
}

func TestDodgeAndRetreat(t *testing.T) {
	s := New("test")
	s.Location = "engineering"
	s.Combat = true
	play(&s, Action{"dodge", "drone"}, sequence(t, 20, 1))
	if s.HP != 24 {
		t.Fatal("disadvantage not respected")
	}
	s.Apply(Action{"retreat", "bridge"}, Ruling{})
	if s.Combat || s.Location != "bridge" || s.FoeHP != 10 {
		t.Fatal(s)
	}
}

func TestBypassFailureStartsCombat(t *testing.T) {
	s := New("test")
	s.Location = "engineering"
	play(&s, Action{"bypass", "drone"}, sequence(t, 1, 15, 1))
	if !s.Combat || s.FoeHP != 10 {
		t.Fatal(s)
	}
	if s.Apply(Action{"isolate", "relay"}, Ruling{}).Allowed {
		t.Fatal("relay used during combat")
	}
}

func TestHazardAndSave(t *testing.T) {
	s := New("test")
	s.Location = "engineering"
	s.FoeHP = 0
	r := play(&s, Action{"isolate", "relay"}, sequence(t, 1, 6))
	if !s.Isolated || s.HP != 18 || r.Damage != 6 {
		t.Fatal(s, r)
	}
}

func TestRollRequiresPlayerCommand(t *testing.T) {
	s := New("test")
	r := s.Apply(Action{"scan", "sensors"}, Ruling{})
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
	if r := s.Roll("1d20+7", sequence(t)); r.Allowed {
		t.Fatal("/roll accepted notation that isn't the roll due")
	}
	r = s.Roll("int (Investigation)", sequence(t, 10))
	if !r.Allowed || !s.Clues["frequency"] || s.Pending != nil || s.Turn != 1 || len(r.Rolls) != 1 || r.Rolls[0].By != ByPlayer || r.Rolls[0].Total != 16 {
		t.Fatalf("roll did not resolve the scan: %+v", r)
	}
	if r := s.Roll("Intelligence", sequence(t)); r.Allowed {
		t.Fatal("rolled with nothing pending")
	}
}

func TestOtherActionCancelsPendingRoll(t *testing.T) {
	s := New("test")
	s.Apply(Action{"scan", "sensors"}, Ruling{})
	s.Apply(Action{"move", "sickbay"}, Ruling{})
	if s.Pending != nil || s.Location != "sickbay" {
		t.Fatal(s)
	}
	if s.Roll("Intelligence", sequence(t)).Allowed {
		t.Fatal("abandoned roll still resolved")
	}
}

func TestDataRollsAreThePlayersAndDroneRollsTheGMs(t *testing.T) {
	s := New("test")
	s.Location = "engineering"
	// Data wins initiative and misses; the drone then fires and misses.
	r := play(&s, Action{"attack", "drone"}, sequence(t, 15, 1, 2, 1))
	var by []string
	for _, x := range r.Rolls {
		by = append(by, x.Label+":"+x.By)
	}
	want := []string{"Data initiative:player", "Drone initiative:gm", "Phaser attack:player", "Drone attack:gm"}
	if !slices.Equal(by, want) {
		t.Fatalf("got %v, want %v", by, want)
	}
}

func improvisation(ability, skill, difficulty, effect string) Improvisation {
	return Improvisation{Approach: "try something clever", Ability: ability, Skill: skill, Difficulty: difficulty, Effect: effect}
}

func TestImprovisedCheckUsesEffectMinimumDC(t *testing.T) {
	s := New("test")
	s.Location = "engineering"
	r := s.Improvise(improvisation("STR", "Athletics", "easy", "disable_drone"), Ruling{})
	if !r.Allowed || r.RollRequired == nil || r.RollRequired.Target != 20 || r.RollRequired.Command != "/roll Strength" || s.Turn != 0 {
		t.Fatalf("easy approach to a hard effect should wait on a DC 20 roll: %+v", r)
	}
	if !strings.Contains(r.Message, "at least DC 20") {
		t.Fatalf("raised DC not explained: %s", r.Message)
	}
	// Strength 18 (+4) plus athletics proficiency (+2): 14 + 6 = 20.
	r = s.Roll("athletics", sequence(t, 14))
	if !r.Allowed || s.FoeHP != 0 || s.Combat || s.Turn != 1 || len(r.Rolls) != 1 || r.Rolls[0].By != ByPlayer || r.Rolls[0].Total != 20 || r.Improvisation == nil {
		t.Fatalf("improvised disable did not resolve: %+v", r)
	}
}

func TestImprovisedDifficultyCanRaiseDC(t *testing.T) {
	s := New("test")
	r := s.Improvise(improvisation("wisdom", "", "hard", "recover_frequency"), Ruling{})
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
		if r := s.Improvise(im, Ruling{}); r.Allowed || s.View().JSON() != before {
			t.Fatalf("invalid improvisation %+v was accepted: %+v", im, r)
		}
	}
}

func TestFlavorNeedsNoRollOrTurn(t *testing.T) {
	s := New("test")
	s.Apply(Action{"scan", "sensors"}, Ruling{})
	r := s.Improvise(Improvisation{Approach: "sit in the captain's chair", Effect: "flavor"}, Ruling{})
	if !r.Allowed || r.RollRequired != nil || s.Turn != 0 || s.Pending == nil {
		t.Fatalf("flavor should change nothing, including the pending scan: %+v", r)
	}
}

func TestAdvantageIsEarnedAndSpent(t *testing.T) {
	s := New("test")
	play := func(im Improvisation, dice ...int) Result {
		r := s.Improvise(im, Ruling{})
		return s.Roll(r.RollRequired.Ability, sequence(t, dice...))
	}
	play(improvisation("intelligence", "", "easy", "gain_advantage"), 10)
	if !s.Advantage || slices.ContainsFunc(s.View().Effects, func(e Effect) bool { return e.ID == "gain_advantage" }) {
		t.Fatal("advantage not earned, or still offered while held")
	}
	s.Apply(Action{"scan", "sensors"}, Ruling{})
	r := s.Roll("Intelligence", sequence(t, 2, 12))
	if s.Advantage || len(r.Rolls[0].Dice) != 2 || !s.Clues["frequency"] {
		t.Fatalf("advantage not applied to the next roll: %+v", r)
	}
}

func TestImprovisedDamageInCombatDrawsFire(t *testing.T) {
	s := New("test")
	s.Location = "engineering"
	s.Combat = true
	r := s.Improvise(improvisation("strength", "athletics", "medium", "damage_drone"), Ruling{})
	// Check 15+6 hits, 4 damage, then the drone attacks and misses.
	r = drive(&s, s.Roll("Strength", sequence(t, 15)), sequence(t, 4, 2))
	if s.FoeHP != 6 || !s.Combat || len(r.Rolls) != 3 || r.Rolls[1].Label != "Improvised damage" || r.Rolls[2].Label != "Drone attack" {
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
	r := s.Apply(Action{"inspect", "medical_records"}, Ruling{})
	if !r.Allowed || s.Location != "sickbay" || !s.Clues["biopattern"] || s.Turn != 1 || !strings.HasPrefix(r.Message, "You take the turbolift to sickbay.") {
		t.Fatalf("inspecting sickbay records from engineering should travel there: %+v", r)
	}
	// A check elsewhere travels now and waits on the roll there.
	r = s.Apply(Action{"scan", "sensors"}, Ruling{})
	if s.Location != "bridge" || r.RollRequired == nil {
		t.Fatal(r)
	}
}

func TestTurboliftReachesEveryLocation(t *testing.T) {
	s := New("test")
	s.Location = "engineering"
	if !s.Apply(Action{"move", "sickbay"}, Ruling{}).Allowed || s.Location != "sickbay" {
		t.Fatal(s)
	}
}

func TestLeavingCombatWithdraws(t *testing.T) {
	s := New("test")
	s.Location = "engineering"
	s.Combat = true
	if r := s.Apply(Action{"move", "sickbay"}, Ruling{}); !r.Allowed || s.Combat || s.Location != "sickbay" {
		t.Fatal(r)
	}
	s.Location, s.Combat = "engineering", true
	if r := s.Apply(Action{"inspect", "logs"}, Ruling{}); !r.Allowed || s.Combat || s.Location != "bridge" || !s.Clues["logs"] {
		t.Fatal(r)
	}
}

func TestImprovisationElsewhereTravelsThere(t *testing.T) {
	s := New("test")
	r := s.Improvise(improvisation("strength", "athletics", "hard", "disable_drone"), Ruling{})
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
	s.FoeHP, s.Isolated = 0, true
	if l := s.View().Leads; len(l) != 1 || !strings.Contains(l[0], "transporter") {
		t.Fatal(l)
	}
}

// pending lists the purposes of the rolls an action waits on, in order, and
// who makes each, rolling each with its value from values.
func pending(t *testing.T, s *State, r Result, values ...int) []string {
	t.Helper()
	var got []string
	roll := sequence(t, values...)
	for r.RollRequired != nil || r.GMRollRequired != nil {
		if p := r.RollRequired; p != nil {
			got = append(got, p.Purpose+":"+p.By)
			r = s.Roll(p.Ability, roll)
		} else {
			p := r.GMRollRequired
			got = append(got, p.Purpose+":"+p.By)
			r = s.RollForGM(roll)
		}
	}
	return got
}

func TestGMCanRuleACheckNeedsNoRoll(t *testing.T) {
	s := New("test")
	r := s.Apply(Action{"scan", "sensors"}, Ruling{NoRoll: true, Reason: "Data's sensors outclass the buffer"})
	if r.RollRequired != nil || !s.Clues["frequency"] || s.Turn != 1 || len(r.Rolls) != 1 || !r.Rolls[0].Automatic || !r.Rolls[0].Success || r.Ruling == nil {
		t.Fatalf("an automatic success should resolve at once: %+v", r)
	}
	// An automatic hit still rolls damage, and the drone still replies.
	s = New("test")
	s.Location, s.Combat = "engineering", true
	got := pending(t, &s, s.Apply(Action{"attack", "drone"}, Ruling{NoRoll: true}), 3, 2)
	if !slices.Equal(got, []string{"data_damage:player", "drone_attack:gm"}) || s.FoeHP != 5 {
		t.Fatalf("got %v, drone HP %d", got, s.FoeHP)
	}
}

func TestInitiativeDecidesWhoActsFirst(t *testing.T) {
	// Data wins: Data's attack roll comes before any drone attack.
	s := New("test")
	s.Location = "engineering"
	got := pending(t, &s, s.Apply(Action{"attack", "drone"}, Ruling{}), 15, 1, 2, 1)
	if !slices.Equal(got, []string{"data_initiative:player", "drone_initiative:gm", "check:player", "drone_attack:gm"}) {
		t.Fatalf("Data first: %v", got)
	}
	// The drone wins: it attacks, and damages Data, before Data rolls to hit.
	s = New("test")
	s.Location = "engineering"
	r := s.Apply(Action{"attack", "drone"}, Ruling{})
	r = s.Roll("initiative", sequence(t, 1))
	r = s.RollForGM(sequence(t, 20))
	if r.GMRollRequired == nil || r.GMRollRequired.Purpose != "drone_attack" {
		t.Fatalf("drone should attack next: %+v", r)
	}
	r = s.RollForGM(sequence(t, 15))
	r = s.RollForGM(sequence(t, 4))
	if s.HP != 19 || r.RollRequired == nil || r.RollRequired.Purpose != "check" || s.Turn != 1 {
		t.Fatalf("the drone's hit should land before Data's attack roll: HP %d, %+v", s.HP, r)
	}
	// With rolls made, the action must be finished before another.
	if r := s.Apply(Action{"dodge", "drone"}, Ruling{}); r.Allowed || s.Pending.Purpose != "check" || r.RollRequired != s.Pending {
		t.Fatalf("an action in progress should be finished first, and say which roll it waits on: %+v", r)
	}
}

func TestGMRollMustMatchTheRollDue(t *testing.T) {
	s := New("test")
	s.Location, s.Combat = "engineering", true
	r := s.Apply(Action{"dodge", "drone"}, Ruling{})
	if p := r.GMRollRequired; p == nil || p.Purpose != "drone_attack" || p.Notation != "2d20kl1+3" || s.Turn != 0 {
		t.Fatalf("dodge should wait on a drone attack with disadvantage: %+v", r)
	}
	before := s.View().JSON()
	for _, bad := range []struct {
		purpose, notation string
		dice              []int
	}{
		{"drone_damage", "2d20kl1+3", []int{5, 6}},
		{"drone_attack", "1d20+3", []int{5}},
		{"drone_attack", "2d20kl1+3", []int{5}},
		{"drone_attack", "2d20kl1+3", []int{5, 21}},
	} {
		if _, err := s.GMRoll(bad.purpose, bad.notation, bad.dice); err == nil || s.View().JSON() != before {
			t.Fatalf("%+v was accepted", bad)
		}
	}
	if _, err := s.GMRoll("drone_attack", "2D20KL1 + 3", []int{19, 2}); err != nil || s.Pending != nil || s.HP != 24 {
		t.Fatalf("a matching roll was refused, or disadvantage ignored: %v, %+v", err, s)
	}
}

func TestSkippedGMRollsDontHappen(t *testing.T) {
	// No drone initiative: Data acts first. No drone attack: no damage.
	s := New("test")
	s.Location = "engineering"
	r := s.Apply(Action{"attack", "drone"}, Ruling{})
	r = s.Roll("initiative", sequence(t, 1))
	r = s.SkipGMRoll()
	if r.RollRequired == nil || r.RollRequired.Purpose != "check" {
		t.Fatalf("Data should act first: %+v", r)
	}
	r = s.Roll("attack", sequence(t, 2))
	r = s.SkipGMRoll()
	if s.HP != 24 || s.Pending != nil || !r.Rolls[0].Skipped || r.Rolls[0].Label != "Drone attack" {
		t.Fatalf("a skipped attack should do nothing: %+v", r)
	}
}

func TestActionWithNothingRolledCanBeAbandoned(t *testing.T) {
	s := New("test")
	s.Location = "engineering"
	s.Apply(Action{"attack", "drone"}, Ruling{})
	if r := s.Roll("1d20 + 2", sequence(t, 5)); r.GMRollRequired == nil {
		t.Fatalf("/roll should accept the roll's own notation: %+v", r)
	}
	s = New("test")
	s.Location = "engineering"
	s.Apply(Action{"attack", "drone"}, Ruling{})
	if r := s.Apply(Action{"inspect", "relay"}, Ruling{}); !r.Allowed || !s.Clues["source"] || s.Combat || s.Pending != nil {
		t.Fatalf("an unrolled action should be abandoned: %+v", r)
	}
}

func TestNotation(t *testing.T) {
	for _, c := range []struct {
		notation string
		dice     []int
		total    int
	}{
		{"1d20+6", []int{9}, 15},
		{"d6", []int{4}, 4},
		{"2d6+2", []int{6, 4}, 12},
		{"2d20kh1+6", []int{3, 18}, 24},
		{"2d20kl1+3", []int{19, 2}, 5},
		{"1d20-1", []int{1}, 0},
	} {
		d, err := ParseNotation(c.notation)
		if err != nil || !d.Valid(c.dice) || d.Total(c.dice) != c.total {
			t.Errorf("%s %v: %+v %v total %d", c.notation, c.dice, d, err, d.Total(c.dice))
		}
	}
	for _, bad := range []string{"", "d", "1d1", "0d6", "banana", "1d20+", "1d20kh2"} {
		if _, err := ParseNotation(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
