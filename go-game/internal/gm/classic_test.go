package gm

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/nicolevanderhoeven/asimov/go-game/internal/game"
)

// classicPrompts is the SHA-256 of the classic adventure's resolver and
// narrator prompts and roll_dice schema, recorded before scenarios existed:
// the control arm of the generated-scenario experiment keeps them exactly.
const classicPrompts = "70267c16aaf868fe521543f218c3542031645da89576f8bab730214118de1793"

func TestClassicPromptsAreUnchanged(t *testing.T) {
	j, _ := json.MarshalIndent(rollSchemaFor(game.Classic()), "", " ")
	text := resolvePromptFor(game.Classic()) + "\n----\n" + narratePromptFor(game.Classic()) + "\n----\n" + string(j)
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(text))); got != classicPrompts {
		t.Fatalf("the classic prompts changed (fingerprint %s)", got)
	}
}
