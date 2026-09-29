package main

import (
	"fmt"
	"strings"

	"github.com/nicolevanderhoeven/asimov/go-game/internal/game"
)

// Terminal presentation for the REPL only: the HTTP API returns the same
// game.View as JSON and never sees any of this.

const width = 64

const shipArt = `
          ______________________________
         <______________________________>          ______________________
                    \       /                     /_____________________/
                     \     /_________________________/   /
                      \                                 /
                       '-------------------------------'`

var sceneArt = map[string]string{
	"bridge": `
      .    *        .    [ MAIN VIEWER ]    .        *    .
         *     .        *       .       *        .     *
      ____________________________________________________
     |   [ops]           ___________           [conn]    |
     |    |__|          /  captain  \           |__|     |
     |_________________/_____________\___________________|`,
	"sickbay": `
       _____________                 _____________
      | /\_/\__/\_  |               | ___________ |
      |_____________|               |_____________|
      [=============]               [=============]
       ||         ||    biobeds      ||         ||`,
	"engineering": `
                        |  ||  |
                   _____|  ||  |_____
        [relay]==>|     |  ||  |     |
                  |~~~~~|  ||  |~~~~~|    warp core
                  |_____|  ||  |_____|
                        |__||__|`,
	"combat": `
                 _______
                / () () \      zzzt!
               |   ___   |===----- - -  *
                \_______/
               ___|___|___   security drone`,
	"rescued": `
            .  *  .      *    .   *      .  *  .
          *   | |   .  | |   *   | |  .   | |   *
           .  |*|  *   |*|   .   |*|   *  |*|  .
              | |      | |       | |      | |
          ==========  CREW RECOVERED  ==========`,
	"disabled": `
                  _______________________
                 |  POSITRONIC  OFFLINE  |
                 |   . . . . . . . . .   |
                 |_______________________|`,
}

// sceneKey names the art for the player's current scene; a change of key is
// what triggers redrawing it.
func sceneKey(s game.State) string {
	switch {
	case s.Won:
		return "rescued"
	case s.HP <= 0:
		return "disabled"
	case s.Combat:
		return "combat"
	}
	return s.Location
}

func banner() string {
	title := strings.ToUpper(game.Title)
	return fmt.Sprintf("%s\n\n%s\n%s\n%s\n%s",
		shipArt,
		rule('='),
		center(title),
		center("2014 5e subset with Star Trek adaptations"),
		rule('='))
}

func rule(c rune) string { return strings.Repeat(string(c), width) }

func center(s string) string {
	pad := (width - len(s)) / 2
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat(" ", pad) + s
}

// heading is a rule with a label set into it: "-- Turn 3 -------...".
func heading(label string) string {
	s := "-- " + label + " "
	if n := width - len(s); n > 0 {
		s += strings.Repeat("-", n)
	}
	return s
}

func hpBar(hp, max int) string {
	const cells = 12
	filled := 0
	if max > 0 && hp > 0 {
		filled = (hp*cells + max - 1) / max
	}
	return fmt.Sprintf("[%s%s] %d/%d", strings.Repeat("#", filled), strings.Repeat(".", cells-filled), hp, max)
}

// statusLine is the one-line summary shown after every turn.
func statusLine(v game.View) string {
	s := fmt.Sprintf("%s  |  HP %s  |  %s", strings.ToUpper(v.Location), hpBar(v.HP, v.Character.MaxHP), v.Status)
	if v.Combat {
		s += fmt.Sprintf("  |  drone HP %d", v.DroneHP)
	}
	return s
}

// wrap breaks text into lines of at most width-indent columns, each
// prefixed with indent.
func wrap(text, indent string) string {
	var lines []string
	line := indent
	for _, word := range strings.Fields(text) {
		if len(line) > len(indent) && len(line)+1+len(word) > width {
			lines = append(lines, line)
			line = indent
		}
		if len(line) > len(indent) {
			line += " "
		}
		line += word
	}
	return strings.Join(append(lines, line), "\n")
}
