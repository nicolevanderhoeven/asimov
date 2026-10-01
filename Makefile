# Shortcuts for the common tasks. Run `make` to list them.
# Every target reads settings from .env (copy env.example to start).

GAME := cd go-game && go run ./cmd/enterprise
K6 := scripts/k6.sh

.DEFAULT_GOAL := help

.PHONY: help
help: ## List the targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-z0-9-]+:.*## / {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.env:
	@cp env.example .env
	@echo "Created .env from env.example. Fill it in (see docs/grafana-cloud-setup.md), then run make doctor."
	@exit 1

## Play

.PHONY: play-offline
play-offline: ## Play with no API key, LLM, or telemetry (exact /do commands only)
	$(GAME) --offline

.PHONY: play-local
play-local: .env ## Play with the AI GM but without sending telemetry (needs ANTHROPIC_API_KEY)
	$(GAME) --no-telemetry

.PHONY: play
play: .env ## Play with the AI GM, sending telemetry to Grafana Cloud
	$(GAME)

.PHONY: serve
serve: .env ## Run the HTTP API on :8080 for the k6 tests
	$(GAME) --serve --addr :8080

## Check

.PHONY: doctor
doctor: .env ## Check .env and send one test span, metric, log, and generation to Grafana Cloud
	cd go-game && go run ./cmd/doctor

.PHONY: test
test: ## Run the Go unit tests (no credentials needed)
	cd go-game && go vet ./... && go test -race ./...

## k6 tests (each starts the game server if it isn't running)

.PHONY: k6-graders
k6-graders: ## Trajectory graders against fixed cases: about 1s, no server or API calls
	k6 run tests/test-trajectory-graders.js

.PHONY: k6-code
k6-code: .env ## Fixed prompts with code checks: under 1 min, ~10 model calls
	$(K6) tests/test-code.js

.PHONY: k6-ai
k6-ai: .env ## Varied probes judged by Claude: 1-2 min, ~20 model calls
	$(K6) tests/test-ai.js

.PHONY: k6-traffic
k6-traffic: .env ## One minute of game traffic to populate Grafana: a few dozen model calls
	$(K6) tests/test_traffic.js

.PHONY: k6-trajectory
k6-trajectory: .env ## Dice trajectory evals: a few min, ~200 model calls
	$(K6) --summary-mode=full tests/test-trajectory.js

.PHONY: k6-e2e
k6-e2e: .env ## Five whole playthroughs: 5-10 min, a few hundred model calls
	$(K6) tests/test-e2e.js
