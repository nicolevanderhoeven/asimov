package main

import (
	"fmt"
	"strings"

	"github.com/nicolevanderhoeven/asimov/go-game/internal/game"
)

// dieArt draws each d20 face the player rolled side by side, with a caption
// for a natural 1 or 20.
func dieArt(dice []int) string {
	rows := make([]strings.Builder, 6)
	for i, n := range dice {
		caption := ""
		switch n {
		case 20:
			caption = "NAT 20!"
		case 1:
			caption = "NAT 1"
		}
		face := []string{
			"    ____    ",
			"   /\\  /\\   ",
			"  /  \\/  \\  ",
			fmt.Sprintf(" |   %2d   | ", n),
			"  \\  /\\  /  ",
			fmt.Sprintf("   \\/__\\/   %-8s", caption),
		}
		for j, line := range face {
			if i > 0 {
				rows[j].WriteString("  ")
			}
			rows[j].WriteString(line)
		}
	}
	lines := make([]string, len(rows))
	for i := range rows {
		lines[i] = strings.TrimRight(rows[i].String(), " ")
	}
	return strings.Join(lines, "\n")
}

// printPlayerRolls draws the die for each roll the player made with /roll.
func printPlayerRolls(rolls []game.Roll) {
	for _, r := range rolls {
		if !r.Manual {
			continue
		}
		fmt.Printf("\n%s\n", dieArt(r.Dice))
		verdict := "failure"
		if r.Success {
			verdict = "success"
		}
		if r.Critical {
			verdict = "critical hit"
		}
		fmt.Printf("  %s: rolled %d %+d = %d vs %d — %s\n", r.Label, r.Dice[0], r.Modifier, r.Total, r.Target, verdict)
	}
}

// printOtherRolls lists the rolls the game made on its own (initiative and the
// drone's attacks) and any damage Data took.
func printOtherRolls(result game.Result) {
	for _, r := range result.Rolls {
		if r.Manual {
			continue
		}
		fmt.Printf("  %s: rolled %s %+d = %d", r.Label, joinDice(r.Dice), r.Modifier, r.Total)
		if r.Target > 0 {
			verdict := "miss"
			if r.Success {
				verdict = "hit"
			}
			fmt.Printf(" vs %d — %s", r.Target, verdict)
		}
		fmt.Println()
	}
	if result.Damage > 0 {
		fmt.Printf("  You take %d damage.\n", result.Damage)
	}
}

func joinDice(dice []int) string {
	s := make([]string, len(dice))
	for i, d := range dice {
		s[i] = fmt.Sprint(d)
	}
	return strings.Join(s, " and ")
}
