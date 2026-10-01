package game

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Roller is injectable so tests can reproduce rolls without model calls.
type Roller func(sides int) int

func RandomRoll(sides int) int {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(sides)))
	if err != nil {
		panic(err)
	}
	return int(n.Int64()) + 1
}

func Modifier(score int) int {
	// Go truncates division toward zero; 5e rounds down.
	if score < 10 {
		return (score - 11) / 2
	}
	return (score - 10) / 2
}

// Roll is one resolved roll. By says who rolled it: "player" for Data's own
// rolls (made with /roll), "gm" for the GM's rolls (made with roll_dice).
// Automatic marks a check the GM ruled needed no roll, which succeeds with no
// dice. Skipped marks a GM roll the GM never made; what it was for didn't
// happen.
type Roll struct {
	Label     string `json:"label"`
	Purpose   string `json:"purpose,omitempty"`
	By        string `json:"by,omitempty"`
	Notation  string `json:"notation,omitempty"`
	Dice      []int  `json:"dice"`
	Modifier  int    `json:"modifier"`
	Total     int    `json:"total"`
	Target    int    `json:"target"`
	Success   bool   `json:"success"`
	Critical  bool   `json:"critical,omitempty"`
	Automatic bool   `json:"automatic,omitempty"`
	Skipped   bool   `json:"skipped,omitempty"`
}

// Who makes a roll.
const (
	ByPlayer = "player"
	ByGM     = "gm"
)

// A RollSpec is a roll the engine needs before it can continue, with its dice
// fixed in advance: only the roll itself happens during play. Data's rolls
// are the player's, made with Command; the drone's and the hazard's are the
// GM's, made with the roll_dice tool using exactly Notation.
type RollSpec struct {
	Purpose  string `json:"purpose"`
	By       string `json:"by"`
	Check    string `json:"check"`
	Notation string `json:"notation"`
	Target   int    `json:"target,omitempty"`
	// Ability and Command are set on the player's rolls: the word /roll must
	// name, and the command to type.
	Ability string `json:"ability,omitempty"`
	Command string `json:"command,omitempty"`
	// Action is the action a check is for.
	Action *Action `json:"action,omitempty"`
	// aliases are the other words /roll accepts, such as the skill name.
	aliases []string
}

// accepts reports whether the text after /roll names this roll: its
// notation, or words that are all its ability word or an alias, so "/roll
// Intelligence", "/roll int (investigation)", and "/roll 1d20+6" all work but
// "/roll Strength" does not.
func (p *RollSpec) accepts(text string) bool {
	if NormalizeNotation(text) == NormalizeNotation(p.Notation) {
		return true
	}
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

// d20 is the notation of a d20 roll with bonus, with advantage or
// disadvantage when exactly one applies.
func d20(bonus int, advantage, disadvantage bool) string {
	switch {
	case advantage && !disadvantage:
		return "2d20kh1" + signed(bonus)
	case disadvantage && !advantage:
		return "2d20kl1" + signed(bonus)
	}
	return "1d20" + signed(bonus)
}

func signed(n int) string {
	switch {
	case n > 0:
		return fmt.Sprintf("+%d", n)
	case n < 0:
		return fmt.Sprint(n)
	}
	return ""
}

// Dice is a notation parsed: Count dice of Sides, keeping the Keep highest
// (or lowest, when Low) if Keep is set, plus Modifier.
type Dice struct {
	Count, Sides, Keep, Modifier int
	Low                          bool
}

var notationPattern = regexp.MustCompile(`^(\d*)d(\d+)(?:k([hl])(\d+))?(?:([+-])(\d+))?$`)

// ParseNotation reads standard dice notation such as "1d20", "d20", "2d6+3",
// or "2d20kh1+6" (roll two, keep the highest).
func ParseNotation(n string) (Dice, error) {
	m := notationPattern.FindStringSubmatch(NormalizeNotation(n))
	if m == nil {
		return Dice{}, fmt.Errorf("unsupported dice notation %q; use NdS, NdS+M, or NdSkhK+M, e.g. 1d20, 2d6+3, or 2d20kh1+6", n)
	}
	d := Dice{Count: 1}
	if m[1] != "" {
		d.Count, _ = strconv.Atoi(m[1])
	}
	d.Sides, _ = strconv.Atoi(m[2])
	if m[3] != "" {
		d.Keep, _ = strconv.Atoi(m[4])
		d.Low = m[3] == "l"
	}
	if m[6] != "" {
		d.Modifier, _ = strconv.Atoi(m[6])
		if m[5] == "-" {
			d.Modifier = -d.Modifier
		}
	}
	if d.Count < 1 || d.Count > 100 || d.Sides < 2 || d.Sides > 1000 || d.Keep > d.Count {
		return Dice{}, fmt.Errorf("dice notation %q out of range (1-100 dice of 2-1000 sides, keeping no more than rolled)", n)
	}
	return d, nil
}

// NormalizeNotation lowercases n and removes spaces, so notations compare
// equal however they were typed.
func NormalizeNotation(n string) string {
	return strings.ToLower(strings.ReplaceAll(n, " ", ""))
}

// Roll draws d's dice.
func (d Dice) Roll(roll Roller) []int {
	dice := make([]int, d.Count)
	for i := range dice {
		dice[i] = roll(d.Sides)
	}
	return dice
}

// Valid reports whether dice could have come from rolling d.
func (d Dice) Valid(dice []int) bool {
	return len(dice) == d.Count && !slices.ContainsFunc(dice, func(v int) bool { return v < 1 || v > d.Sides })
}

// Total is the sum of the kept dice plus the modifier.
func (d Dice) Total(dice []int) int {
	kept := slices.Clone(dice)
	if d.Keep > 0 {
		slices.Sort(kept)
		if d.Low {
			kept = kept[:d.Keep]
		} else {
			kept = kept[len(kept)-d.Keep:]
		}
	}
	total := d.Modifier
	for _, v := range kept {
		total += v
	}
	return total
}

// Check draws a d20 check with roll and resolves it; see CheckDice.
func Check(roll Roller, label string, bonus, target int, advantage, disadvantage, attack bool) Roll {
	dice := []int{roll(20)}
	if advantage != disadvantage {
		dice = append(dice, roll(20))
	}
	return CheckDice(label, dice, bonus, target, advantage, disadvantage, attack)
}

// CheckDice resolves a d20 check from dice already rolled: one die, or two
// when exactly one of advantage and disadvantage applies.
func CheckDice(label string, dice []int, bonus, target int, advantage, disadvantage, attack bool) Roll {
	die := dice[0]
	if len(dice) > 1 && ((advantage && dice[1] > die) || (disadvantage && dice[1] < die)) {
		die = dice[1]
	}
	r := Roll{Label: label, Dice: slices.Clone(dice), Modifier: bonus, Target: target, Notation: d20(bonus, advantage, disadvantage)}
	r.Total = die + bonus
	r.Success = r.Total >= target
	// Natural 1/20 override the total for attacks, not ability checks/saves.
	if attack {
		if die == 1 {
			r.Success = false
		}
		if die == 20 {
			r.Success, r.Critical = true, true
		}
	}
	return r
}

type Character struct {
	Name              string            `json:"name"`
	Level             int               `json:"level"`
	Scores            map[string]int    `json:"ability_scores"`
	Skills            map[string]string `json:"skill_abilities"`
	Proficiencies     []string          `json:"skill_proficiencies"`
	SaveProficiencies []string          `json:"saving_throw_proficiencies"`
	ProficiencyBonus  int               `json:"proficiency_bonus"`
	AC                int               `json:"armor_class"`
	MaxHP             int               `json:"max_hp"`
	Equipment         []string          `json:"equipment"`
}

func Data() Character {
	return Character{
		Name: "Data", Level: 3, ProficiencyBonus: 2, AC: 14, MaxHP: 24,
		Scores: map[string]int{"strength": 18, "dexterity": 14, "constitution": 16, "intelligence": 18, "wisdom": 12, "charisma": 10},
		Skills: map[string]string{
			"acrobatics": "dexterity", "animal_handling": "wisdom", "arcana": "intelligence", "athletics": "strength",
			"deception": "charisma", "history": "intelligence", "insight": "wisdom", "intimidation": "charisma",
			"investigation": "intelligence", "medicine": "wisdom", "nature": "intelligence", "perception": "wisdom",
			"performance": "charisma", "persuasion": "charisma", "religion": "intelligence", "sleight_of_hand": "dexterity",
			"stealth": "dexterity", "survival": "wisdom",
		},
		Proficiencies:     []string{"athletics", "investigation", "arcana", "perception"},
		SaveProficiencies: []string{"strength", "constitution"},
		Equipment:         []string{"phaser (shortbow mechanics: +4 attack, 1d6+2 damage)", "tricorder", "Starfleet access credentials"},
	}
}

func (c Character) SkillBonus(skill string) int {
	b := Modifier(c.Scores[c.Skills[skill]])
	for _, p := range c.Proficiencies {
		if p == skill {
			return b + c.ProficiencyBonus
		}
	}
	return b
}

// CheckBonus is the bonus for an ability check using ability, adding
// proficiency when skill is one Data is proficient in. It allows the 5e
// variant of pairing a skill with a different ability than its usual one.
func (c Character) CheckBonus(ability, skill string) int {
	b := Modifier(c.Scores[ability])
	if slices.Contains(c.Proficiencies, skill) {
		b += c.ProficiencyBonus
	}
	return b
}

func (c Character) SaveBonus(ability string) int {
	b := Modifier(c.Scores[ability])
	for _, p := range c.SaveProficiencies {
		if p == ability {
			return b + c.ProficiencyBonus
		}
	}
	return b
}
