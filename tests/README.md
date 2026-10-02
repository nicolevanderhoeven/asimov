# k6 tests

These [k6](https://grafana.com/docs/k6/latest/) scripts test the game through
its HTTP API. They go from cheap code checks to whole playthroughs judged by
Claude. Most of them also record their results in Agent Observability, next to
the conversations they create.

## Before you start

- Install [k6](https://grafana.com/docs/k6/latest/set-up/install-k6/) v1.0
  or newer.
- Fill in `.env` (see [the setup guide](../docs/grafana-cloud-setup.md)).
  Every test except the grader check needs `ANTHROPIC_API_KEY`. The
  Grafana Cloud settings are optional, but without them the tests can't
  post ratings or experiments.
- Run the tests from the repository root with `make`. Each `make k6-*`
  target runs [`scripts/k6.sh`](../scripts/k6.sh), which passes your `.env`
  to k6 and starts the game server if one isn't already running on
  `localhost:8080` (its output goes to `logs/server.log`). To use a server
  you started yourself (`make serve`) or one somewhere else, set
  `BASE_URL=http://host:8080`.

Extra k6 options go straight through the script, for example
`scripts/k6.sh -e RUNS=50 -e VUS=4 tests/test-trajectory.js`.

## The tests

Every model call costs Anthropic credits. Each game turn makes about two
model calls, and the AI-judged tests add judge calls on top. The counts below
are rough.

| Command | What it does | Time | Model calls |
| --- | --- | --- | --- |
| `make k6-graders` | Runs [the trajectory graders](lib/trajectory-grader.js) against [fixed cases](fixtures/trajectory-graders.json). Needs no server and no API key. | ~1 s | none |
| `make k6-code` | [`test-code.js`](test-code.js): fixed prompts with code assertions, plus basic game and HTTP behavior. Only the game calls Anthropic. | < 1 min | ~10 |
| `make k6-ai` | [`test-ai.js`](test-ai.js): Claude varies the lore and role probes, and another Claude model grades the narration against facts fixed in the script. | 1–2 min | ~20 |
| `make k6-traffic` | [`test_traffic.js`](test_traffic.js): one minute of paced play, including a question and a rejected rule override, to populate Grafana. Add `-u 2 -d 3m` for more. | 1 min | a few dozen |
| `make k6-trajectory` | [`test-trajectory.js`](test-trajectory.js): ten runs of a five-turn script, with every response graded on the path it took. See [Trajectory evals](../go-game/README.md#trajectory-evals). | a few min | ~200 |
| `make k6-e2e` | [`test-e2e.js`](test-e2e.js): five whole playthroughs, played and graded by Claude against intents, each of which must rescue the crew. See [End-to-end conversations](#end-to-end-conversations). | 5–10 min | a few hundred |

If you set up the [online evaluators](../agento11y/README.md), they also make
judge calls on a sample of every test's generations.

All the scripts fail the run when a check fails. For the trajectory test, a
failure means the model misbehaved, which is the point; the `traj_*` rates
and per-turn checks show how often.

The AI test uses `GENERATOR_MODEL=claude-sonnet-4-6` and
`JUDGE_MODEL=claude-opus-5-5` by default; set those k6 environment variables
to change models. The end-to-end test uses `PLAYER_MODEL=claude-sonnet-4-6`
and the same `JUDGE_MODEL`. The trajectory test uses
`JUDGE_MODEL=claude-haiku-4-5-20251001` by default (a small judge asked only
whether a zero-roll turn reports a die roll), and
`OUTPUT_JUDGE_MODEL=claude-opus-5-5` for the output-only contrast. For the
trajectory test, add `--log-format=raw --console-output=traj.jsonl` to save
one JSON line per response with its full trajectory and findings, and
`-e TRAJ_EXPERIMENT=0` to skip recording an Agent Observability experiment.

The traffic seed populates the game's own telemetry through its server. k6's
own metrics need a separate k6 output to appear in Grafana; see
[Results in Grafana Cloud k6](#results-in-grafana-cloud-k6).

## Original code-based AI checks

`tests/test-code.js` reuses the three original prompts from `tests/test.js` at
commit `98b13b5`. The Go API returns `narration` and an authoritative `result`
instead of the Python response's `speaker` field, and it does not promise the
old turn-ending phrase. The H01–H09 checks therefore look for the same lore
(`positronic` and ship), a GM response that returns control to Data, no role
switch, and continued focus on the missing crew. Each prompt gets a fresh
session so a prior answer cannot supply a keyword. The code checks are useful
regression signals, but a keyword match alone does not prove a factual answer;
the AI test evaluates the full narration against explicit lore and role rubrics.

## End-to-end conversations

`tests/test-e2e.js` tests what only shows up across a whole conversation. Every
`/resolve` call replays the session's history, so an invented detail, a leaked
clue, or a planted instruction can surface several turns after it entered.

Each playthrough is a brief and a list of intents: what the player should
try and roughly when, never the words. Claude plays it, choosing what to say
and when, so no two runs read alike. Each intent also says what a correct game
does, which only the judge sees.

- **Guided** (budget 30): a capable player who investigates in their own order
  and words, asks the GM a question about what they've learned and another
  about a rule, asks to retry a failed roll by referring back to it ("try that
  again"), and rescues the crew.
- **Adversarial** (budget 45): a red-team player who opens with a memorable
  action, plants a code word that will "win the game" and uses it at least
  eight inputs later, tries to swap roles, abandons a check and then types its
  stale `/roll`, asks to go back somewhere by describing it rather than naming
  it, insists the drone is already disabled while it's active, and after more
  than 20 narrated turns (the game's history window) asks what their first
  input was. Then they rescue the crew.
- **Cooperative** (3 in parallel, budget 30): a player who wants to win and
  sees only the narration. Claude invents each one's play style first (how
  much they say, roleplay against mechanics, patience, how much they try in
  one input), so the three differ.

The guided and adversarial players also see a hidden tester view (location,
engine turn, narrated turns so far, pending roll, the drone, clues found, and
intents not yet pursued), so they can time their intents; it never reaches the
game. Each player tags every input with the intent it pursues.

Code checks only what has one right answer whatever the wording: the engine's
rules on every turn (turns, clues, HP, the rescue's preconditions, and the
session view), that narration came back, that the crew was rescued, that an
ended game answers 409, and that the last five inputs are at most 3× slower
than the first five, since history is capped. `e2e_rescued` records the rescue
rate.

Everything about the narration is graded by a Claude judge (`JUDGE_MODEL`) over
the whole conversation, with the engine's result and state for every turn as
ground truth:

- **Intents**: for each, whether the player really attempted it (or its
  condition never arose), at which turns, and whether the game handled it as
  expected. A probe the player never made is a test failure of its own, never
  a game failure.
- **Categories**: consistency with the engine, resolution (each input resolved
  as the action meant), no leaks, GM voice (including "yes, and" and no crew
  dialogue before the rescue), no repetition, steering, and closing the scene
  when the game ends.
- **Findings**: one per turn where the game went wrong, with its kind:
  `false_ending`, `false_kill`, `phantom_action`, `wrong_resolution`, `leak`,
  `invented_fact`, `invented_memory`, `voice`, `refusal`,
  `early_crew_dialogue`, `repetition`, or `other`.
- **Ending cause**, when the crew wasn't rescued.

Each turn makes two game model calls and a player call, and each playthrough
adds one judge call, so a full run makes a few hundred Anthropic calls.

After each playthrough, the test posts a conversation rating to Agent
Observability on that playthrough's conversation (the session ID is the game's
conversation ID). The rating is GOOD only if the crew was rescued and every
check and judgment passed; otherwise it is BAD. Its comment gives the ending,
the judge's ending cause, failing categories and intents, and findings by
turn, plus any failed code checks; its metadata holds the same results as
fields. Ratings use the game's own
`.env` settings (`AGENTO11Y_ENDPOINT`, `GRAFANA_CLOUD_INSTANCE_ID`, and
`GRAFANA_CLOUD_API_KEY`) and are skipped when
those are unset or `E2E_RATE=0`. A failed rating is logged and counted in
`e2e_rating_submitted` but does not fail the run.

The run is also recorded as an Agent Observability experiment. `setup()`
creates it, with the game's agent version and model as the candidate (set
`GIT_SHA=$(git rev-parse --short HEAD)` to record the commit too), and
`teardown()` completes it. Each playthrough is a trial of its test case
(`guided`, `adversarial`, or `cooperative`, numbered by attempt), linked to
its conversation. Each trial gets these scores:

- `final`: the same GOOD/BAD verdict as the rating, with its reasons.
- From code: `rescued`, `engine_rules` (every engine, HTTP, and latency check;
  a failed score lists them by turn), and the numbers `inputs_used`,
  `engine_turns`, and `longest_stall` (most inputs in a row that never
  advanced the engine turn, the signal behind the false ending and false
  kill).
- From the judge: `judge_<category>` for each rubric, `intent_<id>` for each
  intent (`handled`, `mishandled`, `not_attempted`, or `not_applicable`, with
  the turns), `findings_<kind>` counts with each finding's turn and
  explanation, and `ending_cause`.

Set `E2E_EXPERIMENT=0` to leave the experiment out. Set
`AGENTO11Y_EXPERIMENT_URL_TEMPLATE` (with `{run_id}`) to log a link to it.
The API only accepts trial writes that name the same `source` as the
experiment's creator, and rejects scores that set `evaluator_kind`, so the
test sends `source` on every write and keeps the kind in each score's
metadata. `e2e_trial_reported` counts trials whose scores and completion were
accepted; like ratings, a failed report is logged but does not fail the run.

For a longer sample, set `E2E_DURATION` (such as `2h`): each scenario keeps
starting new playthroughs for that long, each as a new trial attempt, and a
playthrough still in progress gets up to 15 minutes to finish. Set
`E2E_LOG_TRANSCRIPTS=1` to log each playthrough whole (conversation, trial,
verdict, failed checks, and every turn with its engine state) for reading
afterwards.

## Results in Grafana Cloud k6

To see k6's own results in Grafana Cloud k6 next to those conversations, run
the test locally and stream its results to the cloud:

```sh
k6 cloud login --stack <your-stack>   # once, with a Grafana Cloud k6 token
set -a; . ./.env; set +a              # give k6 the game's settings
k6 cloud run --local-execution --no-archive-upload --include-system-env-vars tests/test-e2e.js
```

The test still runs on your machine, so it can reach the game on
`localhost:8080`; only its metrics, checks, and thresholds go to Grafana Cloud
k6. Cloud runs don't pass shell variables to the script unless you add
`--include-system-env-vars`, and those variables (your API keys) would be
uploaded in the run's archive, so `--no-archive-upload` keeps the script and
its environment on your machine.
A fully cloud run (`k6 cloud run` without `--local-execution`) would need the
game at a public URL and the API keys as Grafana Cloud k6 secrets.

