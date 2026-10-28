# Shortcuts for the common tasks. Run `make` to list them.
# This is a thin wrapper: every target runs the Go task runner in cmd/task,
# which does the work the same way on macOS, Linux, and Windows. Without make
# (on Windows, say), run it directly: go run ./cmd/task <target>
# Every target reads settings from .env (copy env.example to start).

TASK := go run ./cmd/task

# Personal overrides, not committed: copy local.mk.example to local.mk.
-include local.mk
# How long make test keeps starting e2e playthroughs. Each 30 minutes costs
# roughly $40-60 in Anthropic calls; a command-line value wins over local.mk,
# as in make test E2E_DURATION=10m. The default is in the task runner.
E2E_DURATION ?=
# K6_CLOUD=1 streams k6's results to Grafana Cloud k6 (after k6 cloud login).
K6_CLOUD ?=
export E2E_DURATION K6_CLOUD
# Extra arguments for the game or for k6, as in
# make k6 ARGS="-e RUNS=50 tests/test-trajectory.js"
ARGS ?=

# The address make serve listens on, localhost:8080 by default. The API is
# unauthenticated and spends your Anthropic key, so it stays on this machine
# unless you pass SERVE_ADDR=:8080.
SERVE_ADDR ?=
export SERVE_ADDR

.DEFAULT_GOAL := help

# The targets and their descriptions are listed by the runner: make help.
TARGETS := play-offline play-local play serve doctor test unit smoke k6-graders k6-code k6-ai k6-trajectory k6-e2e k6-mock k6

.PHONY: help $(TARGETS)
help $(TARGETS):
	@$(TASK) $@ $(ARGS)
