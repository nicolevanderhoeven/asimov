package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// The free run: the k6 tests against the game's mock game master (--llm mock)
// and a local mock judge standing in for Claude behind ANTHROPIC_API_URL. It
// needs no API key and no network beyond localhost. See go-game/README.md.

const (
	// A port of its own, so a real game server on 8080 can't answer a mock run.
	mockBaseURL          = "http://localhost:8081"
	defaultMockJudgeAddr = "127.0.0.1:8099"
	mockJudgeLog         = "logs/mockjudge.log"
)

// mockSettings is env for a mock run, with the URL the game server defaults
// to, the flags that make it play the mock game master, and the address of
// the mock judge. MOCK_SEED and MOCK_FLAWS choose the flaws it plants;
// MOCK_TELEMETRY=1 sends its telemetry to Grafana, from .env.
func mockSettings(env environ) (environ, string, []string, string) {
	addr, _ := env.Get("MOCK_JUDGE_ADDR")
	if addr == "" {
		addr = defaultMockJudgeAddr
	}
	// The key is only ever shown to the local mock judge. The mock player and
	// judges know the classic adventure, so a scenario in .env must not apply.
	env = env.Set("ANTHROPIC_API_KEY", "mock").
		Set("ANTHROPIC_API_URL", "http://"+addr+"/v1/messages").
		Set("ASIMOV_LLM", "mock").
		Set("ASIMOV_SCENARIO", "classic")
	if v, _ := env.Get("MOCK_SEED"); v != "" {
		env = env.Set("ASIMOV_MOCK_SEED", v)
	}
	if v, _ := env.Get("MOCK_FLAWS"); v != "" {
		env = env.Set("ASIMOV_MOCK_FLAWS", v)
	}
	flags := []string{"--llm", "mock"}
	if v, _ := env.Get("MOCK_TELEMETRY"); v != "1" {
		flags = append(flags, "--no-telemetry")
	}
	return env, mockBaseURL, flags, addr
}

// startMockJudge builds and starts the mock judge on addr, unless one is
// already answering there, in which case the returned server is nil.
func (a *app) startMockJudge(env environ, addr string, sig <-chan os.Signal) (*server, error) {
	if judgeUp(addr) {
		return nil, nil
	}
	if err := os.MkdirAll("logs", 0o755); err != nil {
		return nil, err
	}
	fmt.Fprintf(a.stdout, "Starting the mock judge on %s (output in %s)...\n", addr, mockJudgeLog)
	path, err := a.build(env, "mockjudge", "the mock judge")
	if err != nil {
		return nil, err
	}
	s, err := spawn(env, path, mockJudgeLog, "--addr", addr)
	if err != nil {
		return nil, err
	}
	for i := 0; i < 60; i++ {
		if judgeUp(addr) {
			return s, nil
		}
		select {
		case <-s.done:
			s.log.Close()
			return nil, fmt.Errorf("The mock judge exited; see %s:\n%s", mockJudgeLog, tail(mockJudgeLog, 5))
		case <-sig:
			s.stop()
			return nil, exitError{130}
		case <-time.After(500 * time.Millisecond):
		}
	}
	s.stop()
	return nil, fmt.Errorf("The mock judge didn't start within 30s; see %s.", mockJudgeLog)
}

func judgeUp(addr string) bool {
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get("http://" + addr + "/healthz")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// mockSuite is every test the free run plays, in order.
var mockSuite = []string{
	"tests/test-trajectory-graders.js",
	"tests/test-code.js",
	"tests/test-ai.js",
	"tests/test-trajectory.js",
	"tests/test-e2e.js",
}

// cleanMustPass are the tests a game master with no planted flaws must pass.
var cleanMustPass = map[string]bool{
	"tests/test-trajectory-graders.js": true,
	"tests/test-code.js":               true,
	"tests/test-ai.js":                 true,
	"tests/test-trajectory.js":         true,
	"tests/test-e2e.js":                true,
}

const (
	k6ThresholdsCrossed = 99 // k6's exit code when a test's thresholds fail
	graders             = "tests/test-trajectory-graders.js"
)

// k6Mock runs the free suite (or the scripts it is given) and says which
// tests caught flaws. A k6 test that crosses its thresholds exits 99: with
// flaws planted that is the point, so the run fails only if a test could not
// run, if flaws were planted and nothing caught any, or if a clean game
// master failed a test that must pass.
//
// Settings may be given as arguments, such as MOCK_FLAWS=off or MOCK_SEED=7,
// or in the environment; an argument wins.
func (a *app) k6Mock(args []string) error {
	var scripts []string
	for _, arg := range args {
		name, value, isSetting := strings.Cut(arg, "=")
		switch {
		case isSetting && (name == "MOCK_FLAWS" || name == "MOCK_SEED" || name == "MOCK_TELEMETRY" || name == "MOCK_JUDGE_ADDR"):
			a.override = a.override.Set(name, value)
		case isSetting:
			return fmt.Errorf("k6-mock takes MOCK_FLAWS=..., MOCK_SEED=..., MOCK_TELEMETRY=..., and test scripts, not %q", arg)
		default:
			scripts = append(scripts, arg)
		}
	}
	full := len(scripts) == 0
	if full {
		scripts = mockSuite
	}
	if err := needK6(); err != nil {
		return err
	}
	flaws, _ := append(append(environ{}, a.env...), a.override...).Get("MOCK_FLAWS")
	if flaws == "" {
		flaws = "default"
	}

	var results []mockResult
	for _, script := range scripts {
		fmt.Fprintf(a.stdout, "\n=== %s (mock, flaws: %s) ===\n", script, flaws)
		var err error
		if script == graders {
			// It grades fixed cases: no game server, no judge.
			err = a.child(".", a.env, "k6", "run", script)
		} else {
			err = a.k6With([]string{script}, true)
		}
		results = append(results, mockResult{script, codeOf(err), err})
	}

	lines, verdict := mockOutcome(results, mockClean(flaws), full)
	fmt.Fprintf(a.stdout, "\n=== Mock run (flaws: %s) ===\n", flaws)
	for _, l := range lines {
		fmt.Fprintln(a.stdout, "  "+l)
	}
	fmt.Fprintln(a.stdout)
	if verdict != nil {
		return verdict
	}
	if mockClean(flaws) {
		fmt.Fprintln(a.stdout, "OK: no planted flaws, and the tests that must pass did.")
	} else {
		fmt.Fprintln(a.stdout, "OK: the tests caught the planted flaws ('caught' above is the point: the thresholds crossed).")
	}
	return nil
}

func mockClean(flaws string) bool {
	f := strings.ToLower(strings.TrimSpace(flaws))
	return f == "off" || f == "none"
}

// codeOf is the exit code behind a run's error: 0 for none, and -1 for an
// error that is not a child's exit (k6 missing, the server not starting).
func codeOf(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(exitError); ok {
		return ee.code
	}
	return -1
}

// mockResult is how one test of the free run ended.
type mockResult struct {
	script string
	code   int
	err    error
}

// mockOutcome is the report lines for results, and why the run failed, if it
// did. full is whether the whole suite ran, which is what "nothing caught
// anything" is judged on.
func mockOutcome(results []mockResult, clean, full bool) ([]string, error) {
	var lines []string
	crashed, caught, unexpected := 0, 0, 0
	for _, r := range results {
		switch r.code {
		case 0:
			lines = append(lines, "clean    "+r.script)
		case k6ThresholdsCrossed:
			lines = append(lines, "caught   "+r.script)
			if r.script != graders {
				caught++
			}
			if clean && cleanMustPass[r.script] {
				unexpected++
			}
		default:
			crashed++
			why := fmt.Sprintf("exit %d", r.code)
			if r.err != nil && r.code == -1 {
				why = r.err.Error()
			}
			lines = append(lines, fmt.Sprintf("CRASHED  %s (%s)", r.script, why))
		}
	}
	switch {
	case crashed > 0:
		return lines, fmt.Errorf("FAIL: %d test(s) could not run.", crashed)
	case clean && unexpected > 0:
		return lines, fmt.Errorf("FAIL: a game master with no planted flaws failed %d test(s) that must pass.", unexpected)
	case !clean && full && caught == 0:
		return lines, fmt.Errorf("FAIL: flaws were planted but no test caught any; try another MOCK_SEED.")
	}
	return lines, nil
}
