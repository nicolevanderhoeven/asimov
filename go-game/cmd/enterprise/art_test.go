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
	for _, set := range scenes {
		s := game.New("test")
		set(&s)
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
