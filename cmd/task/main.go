// Command task runs the project's shortcuts (play, serve, doctor, unit, and
// the k6 tests) with nothing but Go installed, so they work the same on
// Windows, macOS, and Linux. The Makefile is a thin wrapper around it.
//
//	go run ./cmd/task            # list the targets
//	go run ./cmd/task unit
//	go run ./cmd/task k6-code
//	go run ./cmd/task k6 --summary-mode=full -e RUNS=50 tests/test-trajectory.js
//
// Run it from anywhere inside the repository. Every target reads its settings
// from .env (copy env.example to start).
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"
)

// defaultE2EDuration is how long the test target keeps starting e2e
// playthroughs. Each 30 minutes costs roughly $40-60 in Anthropic calls.
const defaultE2EDuration = "30m"

// defaultServeAddr is where the serve target listens unless SERVE_ADDR says
// otherwise.
const defaultServeAddr = "localhost:8080"

// target is one shortcut. Extra command-line arguments are passed on to what
// the target runs (the game, or k6), except for test, which takes NAME=value
// settings, and unit, smoke, and doctor, which take none.
type target struct {
	name     string
	help     string
	needsEnv bool // stops with a hint until .env exists
	run      func(a *app, args []string) error
}

// app is what a target needs to run: the repository root as the working
// directory, and the shell's environment.
type app struct {
	env      environ
	override environ // settings from the command line, which win over .env
	stdout   io.Writer
	stderr   io.Writer
}

// exitError ends the program with a child's exit code, without a message of
// its own: the child has already said what went wrong.
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

func targets() []target {
	return []target{
		{"play-offline", "Play with no API key, LLM, or telemetry (exact /do commands only)", false,
			func(a *app, args []string) error { return a.game(args, "--offline") }},
		{"play-local", "Play with the AI GM but without sending telemetry (needs ANTHROPIC_API_KEY)", true,
			func(a *app, args []string) error { return a.game(args, "--no-telemetry") }},
		{"play", "Play with the AI GM, sending telemetry to Grafana Cloud", true,
			func(a *app, args []string) error { return a.game(args) }},
		{"serve", "Run the HTTP API on localhost:8080 for the k6 tests (SERVE_ADDR=:8080 exposes it)", true,
			func(a *app, args []string) error {
				// The API is unauthenticated and spends your Anthropic key,
				// so it stays on this machine unless SERVE_ADDR says otherwise.
				addr, _ := a.env.Get("SERVE_ADDR")
				if addr == "" {
					addr = defaultServeAddr
				}
				return a.game(args, "--serve", "--telemetry-if-configured", "--addr", addr)
			}},
		{"doctor", "Check .env and send one test span, metric, log, and generation to Grafana Cloud", true,
			func(a *app, _ []string) error { return a.child("go-game", a.env, "go", "run", "./cmd/doctor") }},
		{"test", "The full e2e eval for E2E_DURATION (default 30m): costs about 40-60 USD per 30 min", true,
			(*app).test},
		{"unit", "Run the Go unit tests (no credentials needed)", false,
			func(a *app, _ []string) error { return a.unit() }},
		{"smoke", "Play a scripted offline game to check the install works (no credentials needed)", false,
			func(a *app, _ []string) error { return a.smoke() }},
		{"k6-graders", "Trajectory graders against fixed cases: about 1s, no server or API calls", false,
			func(a *app, args []string) error {
				if err := needK6(); err != nil {
					return err
				}
				return a.child(".", a.env, "k6", append([]string{"run", "tests/test-trajectory-graders.js"}, args...)...)
			}},
		{"k6-code", "Fixed prompts with code checks: under 1 min, ~10 model calls", true,
			k6Target("tests/test-code.js")},
		{"k6-ai", "Varied probes judged by Claude: 1-2 min, ~20 model calls", true,
			k6Target("tests/test-ai.js")},
		{"k6-trajectory", "Dice trajectory evals: a few min, ~200 model calls", true,
			k6Target("tests/test-trajectory.js", "--summary-mode=full")},
		{"k6-e2e", "Five whole playthroughs: 5-10 min, a few hundred model calls", true,
			k6Target("tests/test-e2e.js")},
		{"k6", "Run any k6 script with .env, starting the game server if needed (arguments go to k6)", false,
			func(a *app, args []string) error { return a.k6(args) }},
	}
}

func k6Target(script string, flags ...string) func(*app, []string) error {
	return func(a *app, args []string) error {
		return a.k6(append(append(flags, script), args...))
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		help(stdout)
		return 0
	}
	var t *target
	for _, c := range targets() {
		if c.name == args[0] {
			c := c
			t = &c
		}
	}
	if t == nil {
		fmt.Fprintf(stderr, "Unknown target %q. Run without arguments to list the targets.\n", args[0])
		return 2
	}
	if err := chdirRoot(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	a := &app{env: environ(os.Environ()), stdout: stdout, stderr: stderr}
	if t.needsEnv {
		if err := ensureEnv(stdout, grafanaBacked(t.name)); err != nil {
			return report(stderr, err)
		}
	}
	return report(stderr, t.run(a, args[1:]))
}

func report(stderr io.Writer, err error) int {
	var ee exitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ee):
		return ee.code
	default:
		fmt.Fprintln(stderr, err)
		return 1
	}
}

func help(w io.Writer) {
	color := useColor()
	fmt.Fprintln(w, "Usage: go run ./cmd/task <target> [arguments]   (or make <target>)")
	fmt.Fprintln(w)
	for _, t := range targets() {
		name := fmt.Sprintf("%-16s", t.name)
		if color {
			name = "\033[36m" + name + "\033[0m"
		}
		fmt.Fprintf(w, "  %s %s\n", name, t.help)
	}
}

// useColor reports whether to colour the help: only on a terminal, and on
// Windows only in Windows Terminal, since the older console prints escape
// codes literally.
func useColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stdout.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	return !isWindows || os.Getenv("WT_SESSION") != ""
}

// test is the long e2e eval: the k6 e2e test, started afresh for
// E2E_DURATION. Settings may be given as arguments, such as E2E_DURATION=10m
// or K6_CLOUD=1, or in the environment; an argument wins.
func (a *app) test(args []string) error {
	duration, _ := a.env.Get("E2E_DURATION")
	if duration == "" {
		duration = defaultE2EDuration
	}
	cloud := ""
	for _, arg := range args {
		name, value, _ := strings.Cut(arg, "=")
		switch name {
		case "E2E_DURATION":
			if value != "" {
				duration = value
			}
		case "K6_CLOUD":
			cloud = value
		default:
			return fmt.Errorf("test takes E2E_DURATION=... and K6_CLOUD=..., not %q", arg)
		}
	}
	if cloud != "" {
		a.override = a.override.Set("K6_CLOUD", cloud)
	}
	fmt.Fprintf(a.stdout, "test plays e2e playthroughs for %s, plus up to 15 min to finish.\n", duration)
	fmt.Fprintln(a.stdout, "It costs real Anthropic credits: roughly $40-60 per 30 minutes. Ctrl-C now to stop.")
	time.Sleep(5 * time.Second)
	return a.k6([]string{"-e", "E2E_DURATION=" + duration, "tests/test-e2e.js"})
}

// interrupts returns a channel that receives Ctrl-C, so the parent can wait
// for a child (which gets the same Ctrl-C from the terminal) and clean up
// after it instead of dying first.
func interrupts() (chan os.Signal, func()) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	return c, func() { signal.Stop(c) }
}
