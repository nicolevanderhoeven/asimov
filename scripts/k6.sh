#!/usr/bin/env bash
# Runs a k6 script with the settings from .env, against the game server at
# BASE_URL (default http://localhost:8080). If nothing is listening there, it
# builds and starts a server for the run and stops it afterwards; the
# server's output goes to logs/server.log.
#
#   scripts/k6.sh tests/test-code.js
#   scripts/k6.sh --summary-mode=full -e RUNS=50 tests/test-trajectory.js
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

BASE_URL=${BASE_URL:-http://localhost:8080}
export BASE_URL

# Any HTTP answer, even a 404, means a server is up.
up() { curl -s -o /dev/null --max-time 2 "$BASE_URL/session/ping"; }

if ! up; then
  case "$BASE_URL" in
    http://localhost:8080 | http://127.0.0.1:8080) ;;
    *)
      echo "No game server is answering at $BASE_URL." >&2
      exit 1
      ;;
  esac
  echo "Starting the game server for this run (output in logs/server.log)..."
  mkdir -p logs
  (cd go-game && go build -o enterprise ./cmd/enterprise)
  (cd go-game && exec ./enterprise --serve --addr :8080) >logs/server.log 2>&1 &
  server=$!
  trap 'kill "$server" 2>/dev/null; wait "$server" 2>/dev/null || true' EXIT
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

k6 run "$@"
