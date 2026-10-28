package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseEnv(t *testing.T) {
	in := "\ufeff# a comment\r\n" +
		"\r\n" +
		"PLAIN=value\r\n" +
		"export EXPORTED=yes\n" +
		"SPACED = padded value  \n" +
		"TRAILING=kept # dropped\n" +
		"HASH=no#comment\n" +
		"DOUBLE=\"a # b\\\"c\" # dropped\n" +
		"SINGLE='$not expanded' # dropped\n" +
		"BASE64=YWJj==\n" +
		"EMPTY=\n" +
		"not a variable\n" +
		"1BAD=skipped\n"
	got, err := parseEnv(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := []envVar{
		{"PLAIN", "value"},
		{"EXPORTED", "yes"},
		{"SPACED", "padded value"},
		{"TRAILING", "kept"},
		{"HASH", "no#comment"},
		{"DOUBLE", `a # b"c`},
		{"SINGLE", "$not expanded"},
		{"BASE64", "YWJj=="},
		{"EMPTY", ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseEnv = %q, want %q", got, want)
	}
}

func TestWithDotEnvOverridesShellExceptBlankValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("FROM_BOTH=file\nBLANK=\nONLY_FILE=file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e, err := environ{"FROM_BOTH=shell", "BLANK=shell", "ONLY_SHELL=shell"}.withDotEnv(path)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"FROM_BOTH": "file", "BLANK": "shell", "ONLY_FILE": "file", "ONLY_SHELL": "shell"} {
		if got, _ := e.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestWithDotEnvMissingFile(t *testing.T) {
	e, err := environ{"A=1"}.withDotEnv(filepath.Join(t.TempDir(), "nope"))
	if err != nil || len(e) != 1 {
		t.Errorf("got %v, %v; want the environment unchanged and no error", e, err)
	}
}

func TestEnvironSetDoesNotAliasTheOriginal(t *testing.T) {
	base := make(environ, 1, 10)
	base[0] = "A=1"
	_ = base.Set("B", "2")
	if _, ok := base.Set("C", "3").Get("B"); ok {
		t.Error("a second Set saw the first one's value")
	}
}

// make is a thin wrapper: every runner target must dispatch to the runner.
func TestMakeForwardsEveryTarget(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not on PATH")
	}
	names := []string{"help"}
	for _, tg := range targets() {
		names = append(names, tg.name)
	}
	for _, name := range names {
		cmd := exec.Command("make", "-n", name)
		cmd.Dir = filepath.Join("..", "..")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Errorf("make -n %s: %v\n%s", name, err, out)
			continue
		}
		if want := "go run ./cmd/task " + name; !strings.Contains(string(out), want) {
			t.Errorf("make -n %s printed %q, want it to run %q", name, out, want)
		}
	}
}

func TestHelpListsEveryTarget(t *testing.T) {
	var out bytes.Buffer
	if code := run(nil, &out, &out); code != 0 {
		t.Errorf("exit code %d, want 0", code)
	}
	for _, tg := range targets() {
		if !strings.Contains(out.String(), tg.name) || !strings.Contains(out.String(), tg.help) {
			t.Errorf("help is missing %s", tg.name)
		}
	}
}

func TestUnknownTarget(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"nonsense"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "nonsense") {
		t.Errorf("code %d, stderr %q", code, errOut.String())
	}
}

func TestTestTargetRejectsUnknownSettings(t *testing.T) {
	a := &app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	if err := a.test([]string{"E2E_DURATION=1m", "--bogus"}); err == nil {
		t.Error("want an error for an argument that is not a setting")
	}
}

func TestEnsureEnvCreatesFromExampleThenStops(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	os.WriteFile("env.example", []byte("KEY=\n"), 0o644)
	var out bytes.Buffer
	if err := ensureEnv(&out, false); err != (exitError{1}) {
		t.Errorf("first call = %v, want exit 1", err)
	}
	if got := out.String(); !strings.Contains(got, "ANTHROPIC_API_KEY") || strings.Contains(got, "grafana-cloud-setup") {
		t.Errorf("message for a target that needs only a key = %q", got)
	}
	if b, _ := os.ReadFile(".env"); string(b) != "KEY=\n" {
		t.Errorf(".env = %q", b)
	}
	if err := ensureEnv(&out, false); err != nil {
		t.Errorf("second call = %v, want nil", err)
	}
}

func TestEnsureEnvPointsGrafanaTargetsAtTheGuide(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	os.WriteFile("env.example", []byte("KEY=\n"), 0o644)
	var out bytes.Buffer
	if err := ensureEnv(&out, true); err != (exitError{1}) {
		t.Fatalf("call = %v, want exit 1", err)
	}
	if !strings.Contains(out.String(), "grafana-cloud-setup.md") {
		t.Errorf("message = %q, want it to name the Grafana guide", out.String())
	}
	for _, name := range []string{"play", "doctor"} {
		if !grafanaBacked(name) {
			t.Errorf("%s should need the Grafana settings", name)
		}
	}
	for _, name := range []string{"play-local", "serve", "k6-code", "k6-e2e", "test"} {
		if grafanaBacked(name) {
			t.Errorf("%s should need only an Anthropic key", name)
		}
	}
}

func TestUp(t *testing.T) {
	notFound := httptest.NewServer(http.NotFoundHandler())
	if !up(notFound.URL) {
		t.Error("a 404 should count as up")
	}
	notFound.Close()
	if up(notFound.URL) {
		t.Error("a closed server should not count as up")
	}
}

func TestTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	os.WriteFile(path, []byte("1\r\n2\r\n3\r\n4\r\n"), 0o644)
	if got := tail(path, 2); got != "3\n4" {
		t.Errorf("tail = %q", got)
	}
	if got := tail(path, 10); got != "1\n2\n3\n4" {
		t.Errorf("tail = %q", got)
	}
	if got := tail(filepath.Join(t.TempDir(), "nope"), 3); got != "" {
		t.Errorf("tail of a missing file = %q", got)
	}
}

func TestTestTargetNeedsASpendLimit(t *testing.T) {
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	a := &app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	err := a.test([]string{"E2E_DURATION=1m"})
	if err == nil || !strings.Contains(err.Error(), "K6_MAX_SPEND_USD=60") {
		t.Errorf("test without a limit = %v, want an error that shows how to give one", err)
	}
	// An empty value, as make exports for an unset variable, is no limit.
	a.env = environ{"K6_MAX_SPEND_USD="}
	if err := a.test(nil); err == nil {
		t.Error("an empty K6_MAX_SPEND_USD should not count as a limit")
	}
}
