#!/usr/bin/env bash
# Runs a k6 script with the settings from .env, against the game server at
# BASE_URL (default http://localhost:8080). If nothing is listening there, it
# builds and starts a server for the run and stops it afterwards; the
# server's output goes to logs/server.log.
#
#   scripts/k6.sh tests/test-code.js
#   scripts/k6.sh --summary-mode=full -e RUNS=50 tests/test-trajectory.js
#
# With K6_CLOUD=1 (in the shell or .env), the test still runs here but streams
# its results to Grafana Cloud k6; run k6 cloud login once first.
#
# Every test except tests/test-trajectory-graders.js spends Anthropic credits,
# and tests/test-e2e.js with E2E_DURATION keeps spending for that long:
# roughly $40-60 per 30 minutes.
#
# With MOCK=1 nothing is spent and no API key is needed: the game server runs
# its mock game master (--llm mock) and a local mock judge answers the k6
# scripts' Claude calls (see tests/README.md). MOCK_SEED and MOCK_FLAWS choose
# which flaws the mock plants; MOCK_TELEMETRY=1 also sends its telemetry to
# Grafana, from .env.
set -euo pipefail
cd "$(dirname "$0")/.."

if ! command -v k6 >/dev/null; then
  echo "k6 is not installed: https://grafana.com/docs/k6/latest/set-up/install-k6/" >&2
  exit 1
fi

# k6 reads its settings from its own environment, not from .env.
if [ -f .env ]; then
  set -a
  # shellcheck disable=SC1091
  . ./.env
  set +a
fi

MOCK=${MOCK:-}
if [ "$MOCK" = 1 ]; then
  # A port of its own, so a real game server on 8080 can't answer a mock run,
  # and a key that is never sent anywhere but the local mock judge.
  BASE_URL=${BASE_URL:-http://localhost:8081}
  MOCK_JUDGE_ADDR=${MOCK_JUDGE_ADDR:-127.0.0.1:8099}
  export ANTHROPIC_API_KEY=mock
  export ANTHROPIC_API_URL="http://$MOCK_JUDGE_ADDR/v1/messages"
  export ASIMOV_LLM=mock
  [ -z "${MOCK_SEED:-}" ] || export ASIMOV_MOCK_SEED=$MOCK_SEED
  [ -z "${MOCK_FLAWS:-}" ] || export ASIMOV_MOCK_FLAWS=$MOCK_FLAWS
else
  BASE_URL=${BASE_URL:-http://localhost:8080}
fi
export BASE_URL

# Any HTTP answer, even a 404, means a server is up.
up() { curl -s -o /dev/null --max-time 2 "$BASE_URL/session/ping"; }

pids=()
cleanup() {
  for pid in "${pids[@]:-}"; do
    [ -z "$pid" ] || { kill "$pid" 2>/dev/null; wait "$pid" 2>/dev/null || true; }
  done
}
trap cleanup EXIT

if [ "$MOCK" = 1 ]; then
  echo "Starting the mock judge on $MOCK_JUDGE_ADDR (output in logs/mockjudge.log)..."
  mkdir -p logs
  (cd go-game && go build -o mockjudge ./cmd/mockjudge)
  (cd go-game && exec ./mockjudge --addr "$MOCK_JUDGE_ADDR") >logs/mockjudge.log 2>&1 &
  pids+=($!)
  for _ in $(seq 1 30); do
    curl -s -o /dev/null --max-time 2 "http://$MOCK_JUDGE_ADDR/healthz" && break
    sleep 0.3
  done
  curl -s -o /dev/null --max-time 2 "http://$MOCK_JUDGE_ADDR/healthz" || { echo "The mock judge didn't start; see logs/mockjudge.log." >&2; exit 1; }
fi

if ! up; then
  case "$BASE_URL" in
    http://localhost:8080 | http://127.0.0.1:8080 | http://localhost:8081 | http://127.0.0.1:8081) ;;
    *)
      echo "No game server is answering at $BASE_URL." >&2
      exit 1
      ;;
  esac
  echo "Starting the game server for this run (output in logs/server.log)..."
  mkdir -p logs
  (cd go-game && go build -o enterprise ./cmd/enterprise)
  port=${BASE_URL##*:}
  game_flags=(--serve --addr ":$port")
  if [ "$MOCK" = 1 ]; then
    game_flags+=(--llm mock)
    [ "${MOCK_TELEMETRY:-}" = 1 ] || game_flags+=(--no-telemetry)
  fi
  (cd go-game && exec ./enterprise "${game_flags[@]}") >logs/server.log 2>&1 &
  server=$!
  pids+=("$server")
  for _ in $(seq 1 30); do
    up && break
    if ! kill -0 "$server" 2>/dev/null; then
      echo "The game server exited; see logs/server.log:" >&2
      tail -n 5 logs/server.log >&2
      exit 1
    fi
    sleep 1
  done
  up || { echo "The game server didn't start within 30s; see logs/server.log." >&2; exit 1; }
fi

if [ "${K6_CLOUD:-}" = 1 ]; then
  # Local execution reaches the game on localhost. Without the archive upload,
  # the script and the environment it reads (API keys) stay on this machine.
  k6 cloud run --local-execution --no-archive-upload --include-system-env-vars "$@"
else
  k6 run "$@"
fi
