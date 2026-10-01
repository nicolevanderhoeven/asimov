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

// printRolls shows result's rolls: a die face for each d20 the player rolled
// with /roll, and a line for every other roll, the GM's included.
func printRolls(result game.Result) {
	for _, r := range result.Rolls {
		switch {
		case r.Automatic:
			fmt.Printf("  %s: no roll needed — the GM rules it a success\n", r.Label)
			continue
		case r.Skipped:
			fmt.Printf("  %s: not rolled, so it doesn't happen\n", r.Label)
			continue
		case r.By == game.ByPlayer && r.Target > 0:
			fmt.Printf("\n%s\n", dieArt(r.Dice))
		}
		who := "GM rolls"
		if r.By == game.ByPlayer {
			who = "you roll"
		}
		fmt.Printf("  %s: %s %s: %s %+d = %d", r.Label, who, r.Notation, joinDice(r.Dice), r.Modifier, r.Total)
		if r.Target > 0 {
			verdict := "failure"
			if r.Success {
				verdict = "success"
			}
			if r.Critical {
				verdict = "critical hit"
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
