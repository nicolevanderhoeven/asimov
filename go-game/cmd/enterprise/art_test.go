package main

import (
	"strings"
	"testing"

	"github.com/nicolevanderhoeven/asimov/go-game/internal/game"
)

func TestEverySceneHasArtThatFits(t *testing.T) {
	scenes := []func(*game.State){
		func(s *game.State) {},
		func(s *game.State) { s.Location = "sickbay" },
		func(s *game.State) { s.Location = "engineering" },
		func(s *game.State) { s.Location, s.Combat = "engineering", true },
		func(s *game.State) { s.Won = true },
		func(s *game.State) { s.HP = 0 },
	}
	states := []game.State{}
	for _, set := range scenes {
		s := game.New("test")
		set(&s)
		states = append(states, s)
	}
	// Every room and foe of a generated scenario falls back to art that fits.
	for seed := range uint64(300) {
		sc := game.Generate(seed)
		for _, loc := range sc.Locations {
			s := game.NewScenario("test", sc)
			s.Location = loc
			states = append(states, s)
		}
		s := game.NewScenario("test", sc)
		s.Location, s.Combat = sc.Encounter.Location, sc.Encounter.Kind == game.Combat
		states = append(states, s)
	}
	for _, s := range states {
		art, ok := sceneArt[sceneKey(s)]
		if !ok {
			t.Fatalf("no art for scene %q", sceneKey(s))
		}
		for _, line := range strings.Split(art+"\n"+banner(), "\n") {
			if len([]rune(line)) > 80 { // a standard terminal width
				t.Errorf("scene %q: line too wide: %q", sceneKey(s), line)
			}
		}
	}
}

func TestWrapRespectsWidth(t *testing.T) {
	for _, line := range strings.Split(wrap(strings.Repeat("word ", 50), "    "), "\n") {
		if len(line) > width || !strings.HasPrefix(line, "    ") {
			t.Fatalf("bad wrapped line: %q", line)
		}
	}
}
