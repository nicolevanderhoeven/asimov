package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// smoke plays a short scripted game offline, which needs no API key, LLM,
// or network, and checks that the game starts, lists its actions, and quits
// cleanly. It shows that Go, the module downloads, and the game work here.
func (a *app) smoke() error {
	cmd := exec.Command("go", "run", "./cmd/enterprise", "--offline")
	cmd.Dir = "go-game"
	cmd.Env = a.env
	cmd.Stdin = strings.NewReader("/actions\n/status\n/quit\n")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		fmt.Fprint(a.stderr, out.String())
		return fmt.Errorf("the offline game failed: %w", err)
	}
	for _, want := range []string{"THE SILENT ENTERPRISE", "-- Actions --"} {
		if !strings.Contains(out.String(), want) {
			fmt.Fprint(a.stderr, out.String())
			return fmt.Errorf("the offline game's output has no %q", want)
		}
	}
	fmt.Fprintln(a.stdout, "Offline play works: the game started, listed its actions, and quit.")
	return nil
}
