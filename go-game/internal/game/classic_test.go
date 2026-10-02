package game

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

// classicFingerprint is the SHA-256 of 400 random playthroughs of the classic
// adventure, recorded before scenarios existed. The classic game is the
// control arm of the generated-scenario experiment, so it must keep playing
// exactly as it did: every view and result, byte for byte.
const classicFingerprint = "12bea564ba8eb3d742a197d67d287d644ff37f1d0c9ad6e6e7f9865c5d28e49e"

func TestClassicPlaysExactlyAsBefore(t *testing.T) {
	var b strings.Builder
	for walk := range 400 {
		rng := rand.New(rand.NewPCG(uint64(walk), 7))
		roll := func(sides int) int { return rng.IntN(sides) + 1 }
		s := New("golden")
		fmt.Fprintf(&b, "== walk %d\n%s\n", walk, s.View().JSON())
		for step := 0; step < 40 && !s.Won && s.HP > 0; step++ {
			var r Result
			switch {
			case s.Pending != nil && s.Pending.By == ByPlayer:
				r = s.Roll(s.Pending.Ability, roll)
			case s.Pending != nil && s.Pending.By == ByGM:
				if rng.IntN(6) == 0 {
					r = s.SkipGMRoll()
				} else {
					r = s.RollForGM(roll)
				}
			default:
				v := s.View()
				opts := append(append([]Option{}, v.Actions...), v.Elsewhere...)
				n := rng.IntN(len(opts) + len(v.Effects) + 1)
				switch {
				case n < len(opts):
					r = s.Apply(opts[n].Action, Ruling{NoRoll: rng.IntN(5) == 0})
				case n < len(opts)+len(v.Effects):
					e := v.Effects[n-len(opts)]
					abil := []string{"strength", "dexterity", "intelligence", "wisdom"}[rng.IntN(4)]
					diff := []string{"easy", "medium", "hard"}[rng.IntN(3)]
					r = s.Improvise(Improvisation{Approach: "try something clever", Ability: abil, Skill: []string{"", "athletics", "investigation"}[rng.IntN(3)], Difficulty: diff, Effect: e.ID}, Ruling{NoRoll: rng.IntN(5) == 0})
				default:
					r = s.Answer()
				}
			}
			j, _ := json.Marshal(r)
			fmt.Fprintf(&b, "%s\n", j)
		}
	}
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(b.String()))); got != classicFingerprint {
		t.Fatalf("the classic adventure no longer plays as it did (fingerprint %s)", got)
	}
}
