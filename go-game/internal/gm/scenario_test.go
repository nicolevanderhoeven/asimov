package gm

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/nicolevanderhoeven/asimov/go-game/internal/game"
)

// generated finds a generated scenario with the given modules.
func generated(t *testing.T, want map[string]string) *game.Scenario {
	t.Helper()
	for seed := range uint64(5000) {
		sc := game.Generate(seed)
		if !slices.ContainsFunc(sortedKeys(want), func(k string) bool { return sc.Modules[k] != want[k] }) {
			return sc
		}
	}
	t.Fatalf("no seed builds %v", want)
	return nil
}

func sortedKeys(m map[string]string) []string {
	var k []string
	for x := range m {
		k = append(k, x)
	}
	slices.Sort(k)
	return k
}

func TestPromptsUseTheScenariosExamples(t *testing.T) {
	sc := generated(t, map[string]string{"records": "security", "reading": "astrometrics", "encounter": "exocomp"})
	r, n := resolvePromptFor(sc), narratePromptFor(sc)
	for _, want := range []string{sc.Prompt.Elsewhere, sc.Prompt.LongShot, `"` + sc.Prompt.Approach + `"`} {
		if !strings.Contains(r, want) {
			t.Errorf("resolve prompt is missing %q", want)
		}
	}
	for _, want := range []string{sc.Prompt.NotReady, "You make " + sc.Prompt.GMRolls + " with the roll_dice tool"} {
		if !strings.Contains(n, want) {
			t.Errorf("narrate prompt is missing %q", want)
		}
	}
	for _, classic := range []string{"medical_records", "sensor buffer", "the drone's rolls", "relay"} {
		if strings.Contains(r+n, classic) {
			t.Errorf("prompts still mention the classic %q", classic)
		}
	}
}

func TestRollDiceOffersTheScenariosPurposes(t *testing.T) {
	sc := generated(t, map[string]string{"encounter": "containment_field", "hazard": "radiation"})
	var raw struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(rollSchemaFor(sc).JSON(), &raw); err != nil {
		t.Fatal(err)
	}
	if got := raw.Properties["purpose"].Enum; !slices.Equal(got, []string{"field_damage", "radiation_damage"}) {
		t.Fatalf("purposes %v", got)
	}
	if rollSchemaFor(game.Classic()).JSON() == nil || string(rollSchemaFor(game.Classic()).JSON()) != string(rollDiceSchema.JSON()) {
		t.Fatal("the classic schema must be the declared one")
	}
}

func TestNarratorRollsAChallengeHazard(t *testing.T) {
	sc := generated(t, map[string]string{"encounter": "containment_field"})
	m := &fakeModel{rolls: []string{`{"notation":"1d4","reason":"feedback","purpose":"field_damage"}`}}
	g := newGM(m)
	g.Roll = func(int) int { return 3 }
	s := game.NewScenario("test", sc)
	s.Location = sc.Encounter.Location
	s.Apply(game.Action{Kind: "bypass", Target: "power_feed"}, game.Ruling{})
	r := s.Roll("dexterity", func(int) int { return 1 })
	if r.GMRollRequired == nil || r.GMRollRequired.Purpose != "field_damage" {
		t.Fatalf("%+v", r)
	}
	var out strings.Builder
	n, err := g.Narrate(context.Background(), &s, nil, "/roll dexterity", r, &out)
	if err != nil || len(n.Rolls) != 1 || n.Rolls[0].AppliedTo != "field_damage" || s.HP != 21 {
		t.Fatalf("%+v HP %d %v", n.Rolls, s.HP, err)
	}
	if b, _ := json.Marshal(m.params.Prompt[len(m.params.Prompt)-1]); !strings.Contains(string(b), `"encounter"`) || strings.Contains(string(b), "drone_hp") {
		t.Fatalf("the GM should hear the encounter, not drone_hp: %s", b)
	}
}

func TestContextInfoTagsTheScenario(t *testing.T) {
	sc := game.Generate(7)
	ctx := withScenario(context.WithValue(context.Background(), componentKey{}, "narration"), sc)
	tags := contextInfo(ctx, "v1").Tags
	if tags["scenario"] != "generated" || tags["scenario_variant"] != sc.Variant() || tags["scenario_seed"] != "7" || tags["component"] != "narration" {
		t.Fatalf("%+v", tags)
	}
	classic := contextInfo(withScenario(context.WithValue(context.Background(), componentKey{}, "narration"), game.Classic()), "v1").Tags
	if len(classic) != 2 || classic["scenario"] != "silent-enterprise" {
		t.Fatalf("classic tags must be unchanged: %+v", classic)
	}
}
