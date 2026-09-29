package game

import (
	"crypto/rand"
	"math/big"
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

type Roll struct {
	Label    string `json:"label"`
	Dice     []int  `json:"dice"`
	Modifier int    `json:"modifier"`
	Total    int    `json:"total"`
	Target   int    `json:"target"`
	Success  bool   `json:"success"`
	Critical bool   `json:"critical,omitempty"`
	// Manual marks the roll the player made with /roll, as opposed to one the
	// engine made for the drone or for initiative.
	Manual bool `json:"manual,omitempty"`
}

func Check(roll Roller, label string, bonus, target int, advantage, disadvantage, attack bool) Roll {
	die := roll(20)
	r := Roll{Label: label, Dice: []int{die}, Modifier: bonus, Target: target}
	if advantage != disadvantage {
		other := roll(20)
		r.Dice = append(r.Dice, other)
		if (advantage && other > die) || (disadvantage && other < die) {
			die = other
		}
	}
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
		Scores:            map[string]int{"strength": 18, "dexterity": 14, "constitution": 16, "intelligence": 18, "wisdom": 12, "charisma": 10},
		Skills:            map[string]string{"athletics": "strength", "investigation": "intelligence", "arcana": "intelligence", "medicine": "wisdom", "perception": "wisdom"},
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

func (c Character) SaveBonus(ability string) int {
	b := Modifier(c.Scores[ability])
	for _, p := range c.SaveProficiencies {
		if p == ability {
			return b + c.ProficiencyBonus
		}
	}
	return b
}
