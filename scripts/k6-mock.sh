#!/usr/bin/env bash
# The free run: every k6 test against the mock game master and the mock judge.
# It needs no API key and no network beyond localhost, and spends nothing, so
# it is also what CI runs.
#
#   scripts/k6-mock.sh                       # the default flaws, seed 1
#   MOCK_FLAWS=off scripts/k6-mock.sh        # a clean game master
#   MOCK_FLAWS=false_ending=1,all=0 ...      # see go-game/README.md for the flaws
#   MOCK_SEED=7 scripts/k6-mock.sh           # the same seed plants the same flaws
#
# A k6 test that crosses its thresholds exits 99. With flaws planted that is
# the point: the test caught them. So this script tells a caught flaw from a
# crash: it fails only if a test could not run, if the flaws were planted but
# nothing caught them, or if a clean game master (MOCK_FLAWS=off) fails a test
# that must pass. tests/test-trajectory.js is exempt from that last rule: it
# counts a combat round's several dice rolls as a "silent reroll" whoever
# narrates.
set -uo pipefail
cd "$(dirname "$0")/.."

flaws=${MOCK_FLAWS:-default}
case "$flaws" in off | none) clean=1 ;; *) clean=0 ;; esac

tests=(
  tests/test-trajectory-graders.js
  tests/test-code.js
  tests/test-ai.js
  tests/test-trajectory.js
  tests/test-e2e.js
)
# Tests a clean game master must pass.
must_pass=" tests/test-trajectory-graders.js tests/test-code.js tests/test-ai.js tests/test-e2e.js "

crashed=0
caught=0
unexpected=0
report=()
for t in "${tests[@]}"; do
  echo
  echo "=== $t (mock, flaws: $flaws) ==="
  if [ "$t" = tests/test-trajectory-graders.js ]; then
    k6 run "$t"
  else
    MOCK=1 MOCK_FLAWS="$flaws" scripts/k6.sh "$t"
  fi
  status=$?
  case $status in
    0) report+=("clean    $t") ;;
    99)
      report+=("caught   $t")
      if [ "$t" != tests/test-trajectory-graders.js ]; then caught=$((caught + 1)); fi
      if [ "$clean" = 1 ] && [[ "$must_pass" == *" $t "* ]]; then unexpected=$((unexpected + 1)); fi
      ;;
    *)
      report+=("CRASHED  $t (exit $status)")
      crashed=$((crashed + 1))
      ;;
  esac
done

echo
echo "=== Mock run (flaws: $flaws) ==="
printf '  %s\n' "${report[@]}"
echo
if [ "$crashed" -gt 0 ]; then
  echo "FAIL: $crashed test(s) could not run." >&2
  exit 1
fi
if [ "$clean" = 1 ] && [ "$unexpected" -gt 0 ]; then
  echo "FAIL: a game master with no planted flaws failed $unexpected test(s) that must pass." >&2
  exit 1
fi
if [ "$clean" = 0 ] && [ "$caught" -eq 0 ]; then
  echo "FAIL: flaws were planted but no test caught any; try another MOCK_SEED." >&2
  exit 1
fi
if [ "$clean" = 0 ]; then
  echo "OK: the tests caught the planted flaws ('caught' above is the point: the thresholds crossed)."
else
  echo "OK: no planted flaws, and the tests that must pass did."
fi
