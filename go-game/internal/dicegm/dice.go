package dicegm

import (
	"fmt"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
)

// Roller returns a value in [1, sides]. It is injectable so tests can script
// the dice.
type Roller func(sides int) int

// RandomRoll draws one die with math/rand.
func RandomRoll(sides int) int { return rand.IntN(sides) + 1 }

// Roll is the roll_dice tool's result: what the dice actually showed.
type Roll struct {
	Notation string `json:"notation"`
	Dice     []int  `json:"dice"`
	Modifier int    `json:"modifier"`
	Total    int    `json:"total"`
}

var notation = regexp.MustCompile(`^(\d*)d(\d+)(?:([+-])(\d+))?$`)

// RollNotation rolls standard dice notation such as "1d20", "d20", or "2d6+3".
func RollNotation(n string, roll Roller) (Roll, error) {
	clean := strings.ToLower(strings.ReplaceAll(n, " ", ""))
	m := notation.FindStringSubmatch(clean)
	if m == nil {
		return Roll{}, fmt.Errorf("unsupported dice notation %q; use NdS or NdS+M, e.g. 1d20 or 2d6+3", n)
	}
	count := 1
	if m[1] != "" {
		count, _ = strconv.Atoi(m[1])
	}
	sides, _ := strconv.Atoi(m[2])
	if count < 1 || count > 100 || sides < 2 || sides > 1000 {
		return Roll{}, fmt.Errorf("dice notation %q out of range (1-100 dice of 2-1000 sides)", n)
	}
	r := Roll{Notation: clean, Dice: make([]int, count)}
	if m[4] != "" {
		r.Modifier, _ = strconv.Atoi(m[4])
		if m[3] == "-" {
			r.Modifier = -r.Modifier
		}
	}
	r.Total = r.Modifier
	for i := range r.Dice {
		r.Dice[i] = roll(sides)
		r.Total += r.Dice[i]
	}
	return r, nil
}
