package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
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
	if err := a.child(".", a.env, "docker", "compose", "-f", localStackCompose, "up", "-d", "--wait"); err != nil {
		return err
	}
	port, _ := a.env.Get("ASIMOV_GRAFANA_PORT")
	if port == "" {
		port = "3000"
	}
	fmt.Fprintf(a.stdout, "Grafana: http://localhost:%s/d/asimov-local (no login).\n", port)
	fmt.Fprintln(a.stdout, "Now play with: make play-offline-observed (no API key) or make play-local-observed (AI game master).")
	return nil
}

func (a *app) localStackDown() error {
	if err := needDocker(); err != nil {
		return err
	}
	return a.child(".", a.env, "docker", "compose", "-f", localStackCompose, "down")
}
