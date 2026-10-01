#!/bin/sh
# usage: agento11y/test-evaluator.sh <evaluator.yaml> <generation_id> [conversation_id]
# Tests a draft evaluator against a real generation without persisting it.
set -e
req=$(mktemp).yaml
yq '{"kind": .kind, "config": .config, "output_keys": .output_keys, "generation_id": "'"$2"'"}' "$1" > "$req"
GCX_AGENT_MODE=true gcx agento11y evaluators test -f "$req" ${3:+--conversation-id "$3"} -o json
