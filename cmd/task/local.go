package main

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
)

// localStackCompose is the Compose file of the local Grafana stack.
const localStackCompose = "local-stack/compose.yaml"

// localSettings point the game at the local stack (see local-stack/) and
// away from Grafana Cloud, whatever .env says: the game's .env loader keeps
// variables that are already set, even to nothing. Generations are kept in
// logs/generations.jsonl for agento11y/judge-local.py.
var localSettings = []envVar{
	{"OTLP_ENDPOINT", "http://localhost:4318"},
	{"OTLP_HEADERS", ""},
	{"AGENTO11Y_ENDPOINT", ""},
	{"AGENTO11Y_API_ENDPOINT", ""},
	{"GRAFANA_CLOUD_SIGIL_ENDPOINT", ""},
	{"GRAFANA_CLOUD_INSTANCE_ID", ""},
	{"GRAFANA_CLOUD_INSTANCE", ""},
	{"GRAFANA_CLOUD_API_KEY", ""},
}

// localEnv is e with localSettings applied. The generations file is an
// absolute path, since the game runs from go-game.
func (e environ) localEnv() (environ, error) {
	file, err := filepath.Abs(filepath.Join("logs", "generations.jsonl"))
	if err != nil {
		return e, err
	}
	for _, v := range localSettings {
		e = e.Set(v.key, v.value)
	}
	return e.Set("ASIMOV_GENERATIONS_FILE", file), nil
}

// localGame runs the game from source against the local stack.
func (a *app) localGame(extra []string, flags ...string) error {
	env, err := a.env.localEnv()
	if err != nil {
		return err
	}
	args := append([]string{"run", "./cmd/enterprise"}, flags...)
	return a.child("go-game", env, "go", append(args, extra...)...)
}

func needDocker() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker is not installed: https://docs.docker.com/get-docker/")
	}
	return nil
}

// localStack starts the local Grafana stack and waits until it is ready.
func (a *app) localStack() error {
	if err := needDocker(); err != nil {
		return err
	}
	port, _ := a.env.Get("ASIMOV_GRAFANA_PORT")
	if port == "" {
		port = "3000"
	}
	// Docker's own error goes to the terminal as before; a copy is kept to
	// recognize a port that is taken.
	var docker bytes.Buffer
	up := *a
	up.stderr = io.MultiWriter(a.stderr, &docker)
	if err := up.child(".", a.env, "docker", "compose", "-f", localStackCompose, "up", "-d", "--wait"); err != nil {
		if hint := portHint(docker.String(), port); hint != "" {
			fmt.Fprintln(a.stderr, hint)
		}
		return err
	}
	fmt.Fprintf(a.stdout, "Grafana: http://localhost:%s/d/asimov-local (no login).\n", port)
	fmt.Fprintln(a.stdout, "Now play with: make play-offline-observed (no API key) or make play-local-observed (AI game master).")
	return nil
}

// portHint explains a failed local-stack start when Docker's output says a
// port is taken, and returns "" for any other failure. grafanaPort is the
// port Grafana was asked to use.
func portHint(dockerOutput, grafanaPort string) string {
	taken := false
	for _, s := range []string{"port is already allocated", "address already in use", "Only one usage of each socket address", "ports are not available"} {
		taken = taken || strings.Contains(dockerOutput, s)
	}
	if !taken {
		return ""
	}
	if strings.Contains(dockerOutput, ":"+grafanaPort+" ") || strings.Contains(dockerOutput, ":"+grafanaPort+":") {
		return fmt.Sprintf("Port %s is in use. Set ASIMOV_GRAFANA_PORT to a free port in your shell and run local-stack again, as in ASIMOV_GRAFANA_PORT=3001 make local-stack (PowerShell: $env:ASIMOV_GRAFANA_PORT=3001, then go run ./cmd/task local-stack).", grafanaPort)
	}
	return "A port the stack needs is in use. Grafana's port can move (set ASIMOV_GRAFANA_PORT in your shell), but OTLP stays on 4317 and 4318: stop whatever holds them, such as the Collector from collector/, and run local-stack again."
}

func (a *app) localStackDown() error {
	if err := needDocker(); err != nil {
		return err
	}
	return a.child(".", a.env, "docker", "compose", "-f", localStackCompose, "down")
}
