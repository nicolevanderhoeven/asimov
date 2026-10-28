package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestMockSettingsPointEverythingAtTheMocks(t *testing.T) {
	env, url, flags, addr := mockSettings(environ{
		"ANTHROPIC_API_KEY=sk-real", "ASIMOV_SCENARIO=generated", "MOCK_SEED=7", "MOCK_FLAWS=all=0.3",
	})
	for key, want := range map[string]string{
		"ANTHROPIC_API_KEY": "mock", "ASIMOV_LLM": "mock", "ASIMOV_SCENARIO": "classic",
		"ANTHROPIC_API_URL": "http://127.0.0.1:8099/v1/messages", "ASIMOV_MOCK_SEED": "7", "ASIMOV_MOCK_FLAWS": "all=0.3",
	} {
		if got, _ := env.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if url != "http://localhost:8081" || addr != "127.0.0.1:8099" {
		t.Errorf("url %q, judge %q", url, addr)
	}
	if want := []string{"--llm", "mock", "--no-telemetry"}; !reflect.DeepEqual(flags, want) {
		t.Errorf("flags = %v, want %v", flags, want)
	}
	if !startable(url) {
		t.Error("a mock run starts its own server on its own port")
	}
}

func TestMockSettingsTelemetryAndAddress(t *testing.T) {
	env, _, flags, addr := mockSettings(environ{"MOCK_TELEMETRY=1", "MOCK_JUDGE_ADDR=127.0.0.1:9000"})
	if want := []string{"--llm", "mock"}; !reflect.DeepEqual(flags, want) {
		t.Errorf("with telemetry on, flags = %v, want %v", flags, want)
	}
	if got, _ := env.Get("ANTHROPIC_API_URL"); addr != "127.0.0.1:9000" || got != "http://127.0.0.1:9000/v1/messages" {
		t.Errorf("addr %q, url %q", addr, got)
	}
	if _, ok := env.Get("ASIMOV_MOCK_SEED"); ok {
		t.Error("an unset seed should leave the game's default")
	}
}

func TestStartableOnlyOnThisMachine(t *testing.T) {
	for url, want := range map[string]bool{
		"http://localhost:8080": true, "http://127.0.0.1:8081/": true, "http://localhost:8081": true,
		"http://example.com:8080": false, "http://localhost:9999": false,
	} {
		if got := startable(url); got != want {
			t.Errorf("startable(%q) = %v, want %v", url, got, want)
		}
	}
}

func results(codes ...int) []mockResult {
	var out []mockResult
	for i, c := range codes {
		out = append(out, mockResult{script: mockSuite[i], code: c})
	}
	return out
}

func TestMockOutcomeWithFlawsPlanted(t *testing.T) {
	// graders, code, ai, trajectory, e2e
	lines, err := mockOutcome(results(0, 0, 0, 99, 99), false, true)
	if err != nil || !strings.HasPrefix(lines[3], "caught") || !strings.HasPrefix(lines[0], "clean") {
		t.Errorf("caught flaws are the point: %v %v", lines, err)
	}
	if _, err := mockOutcome(results(0, 0, 0, 0, 0), false, true); err == nil || !strings.Contains(err.Error(), "no test caught") {
		t.Errorf("planted flaws that nothing caught must fail: %v", err)
	}
	if _, err := mockOutcome(results(0, 0, 0, 0, 0), false, false); err != nil {
		t.Errorf("a subset of scripts can't be judged on catching: %v", err)
	}
	// The graders crossing their thresholds is not a flaw caught.
	if _, err := mockOutcome(results(99, 0, 0, 0, 0), false, true); err == nil {
		t.Error("only the game's tests count as catching")
	}
	lines, err = mockOutcome(results(0, 107, 0, 99, 99), false, true)
	if err == nil || !strings.Contains(strings.Join(lines, "\n"), "CRASHED  tests/test-code.js (exit 107)") {
		t.Errorf("a crash fails the run: %v %v", lines, err)
	}
}

func TestMockOutcomeWithNoFlawsPlanted(t *testing.T) {
	if _, err := mockOutcome(results(0, 0, 0, 0, 0), true, true); err != nil {
		t.Error("a clean run catching nothing is fine:", err)
	}
	for i := range mockSuite {
		codes := []int{0, 0, 0, 0, 0}
		codes[i] = 99
		_, err := mockOutcome(results(codes...), true, true)
		if (err != nil) != cleanMustPass[mockSuite[i]] {
			t.Errorf("%s crossing its thresholds with no flaws planted: err = %v", mockSuite[i], err)
		}
	}
}

func TestMockCleanAndExitCodes(t *testing.T) {
	for in, want := range map[string]bool{"off": true, " None ": true, "default": false, "all=0": false, "": false} {
		if got := mockClean(in); got != want {
			t.Errorf("mockClean(%q) = %v", in, got)
		}
	}
	if codeOf(nil) != 0 || codeOf(exitError{99}) != 99 || codeOf(bytes.ErrTooLarge) != -1 {
		t.Error("codeOf should read a child's exit code, and -1 for other errors")
	}
}

func TestK6MockRejectsUnknownSettings(t *testing.T) {
	a := &app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	if err := a.k6Mock([]string{"MOCK_FLAWS=off", "E2E_DURATION=1h"}); err == nil || !strings.Contains(err.Error(), "E2E_DURATION=1h") {
		t.Errorf("want an error naming the argument, got %v", err)
	}
}
