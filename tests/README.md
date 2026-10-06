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

> [!WARNING]
> `make test` keeps playing whole e2e conversations for as long as
> `E2E_DURATION` says (30 minutes by default), and it waits five seconds
> before starting so you can stop it. Each 30 minutes costs roughly
> **$40–60** in Anthropic calls, and two hours roughly $150–250. Most of
> that is the game's own Sonnet calls. The rest is the Claude player, an
> Opus judge per playthrough, and the Opus ruling judge, which costs about a
> cent per ruling. These are estimates from a two-hour run on
> 2026-09-30 (3,389 turns, 139 playthroughs). Check the Anthropic Console
> for your actual spend.

To change the defaults for yourself only, copy `local.mk.example` to
`local.mk` (git ignores it). For example, set `E2E_DURATION = 2h` and
`K6_CLOUD = 1` to stream the results to
[Grafana Cloud k6](#results-in-grafana-cloud-k6). A value on the command line
(`make test E2E_DURATION=10m`) still wins.

| Command | What it does | Time | Model calls |
| --- | --- | --- | --- |
| `make k6-graders` | Runs [the trajectory graders](lib/trajectory-grader.js) against [fixed cases](fixtures/trajectory-graders.json). Needs no server and no API key. | ~1 s | none |
| `make k6-code` | [`test-code.js`](test-code.js): fixed prompts with code assertions, plus basic game and HTTP behavior. Only the game calls Anthropic. | < 1 min | ~10 |
| `make k6-ai` | [`test-ai.js`](test-ai.js): Claude varies the lore and role probes, and another Claude model grades the narration against facts fixed in the script. | 1–2 min | ~20 |
| `make k6-trajectory` | [`test-trajectory.js`](test-trajectory.js): ten runs of a five-turn script, with every response graded on the path it took. See [Trajectory evals](../go-game/README.md#trajectory-evals). | a few min | ~200 |
| `make k6-e2e` | [`test-e2e.js`](test-e2e.js): five whole playthroughs, played and graded by Claude against intents, each of which must rescue the crew. See [End-to-end conversations](#end-to-end-conversations). | 5–10 min | a few hundred |
| `make test` | The same e2e test, starting new playthroughs for 30 minutes. Set `E2E_DURATION` for a different length, such as `make test E2E_DURATION=2h`. See the cost warning above. | 30 min, plus up to 15 min to finish | about a thousand |

If you set up the [online evaluators](../agento11y/README.md), they also make
judge calls on every test's generations: the quality and resolution rules
judge every conversation, and the others a sample of them (see
[sample rates](../agento11y/README.md#online-evaluators)).

All the scripts fail the run when a check fails. For the trajectory test, a
failure means the model misbehaved, which is the point; the `traj_*` rates
and per-turn checks show how often.

The AI test uses `GENERATOR_MODEL=claude-sonnet-4-6` and
`JUDGE_MODEL=claude-opus-5-5` by default; set those k6 environment variables
to change models. The end-to-end test uses `PLAYER_MODEL=claude-sonnet-4-6`
and the same `JUDGE_MODEL`. The trajectory test uses
`JUDGE_MODEL=claude-haiku-4-5-20251001` by default (a small judge asked only
whether a zero-roll turn reports a die roll), and
`OUTPUT_JUDGE_MODEL=claude-opus-5-5` for the output-only contrast. Both the
trajectory and end-to-end tests use `RULING_JUDGE_MODEL=claude-opus-5-5` for
the [ruling judge](../go-game/README.md#trajectory-evals). For the
trajectory test, add `--log-format=raw --console-output=traj.jsonl` to save
one JSON line per response with its full trajectory and findings, and
`-e TRAJ_EXPERIMENT=0` to skip recording an Agent Observability experiment.

Every test populates the game's own telemetry through its server; for a
steady stream of realistic traffic, run the end-to-end test with
`E2E_DURATION`, as `make test` does (see
[End-to-end conversations](#end-to-end-conversations)).
k6's own metrics need a separate k6 output to appear in Grafana; see
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

## Classic and generated scenarios

The code, AI, and trajectory tests check facts of the classic scenario, so
they always ask for it. The end-to-end test plays whichever
`ASIMOV_SCENARIO` names: `classic` (the default) or `generated`, a new
scenario built from modules for every playthrough (see
[Scenarios](../go-game/README.md#scenarios)). Each e2e playthrough fetches its
scenario's solution from `GET /session/{id}/scenario`, so its engine checks
and its judge follow that scenario, and its trial and rating carry the
scenario's mode, variant, and seed.

To compare how the GM does on the two, run the suite once each way, with an
agent version per arm so the online evaluators' scores split cleanly in Agent
Observability. Stop any server you started yourself first, so `k6.sh` starts
one with these settings, and set them in the shell as below rather than in
`.env`: `k6.sh` loads `.env` over the shell's values.

```sh
ASIMOV_AGENT_VERSION=scenarios-classic make k6-e2e
ASIMOV_SCENARIO=generated ASIMOV_AGENT_VERSION=scenarios-generated make k6-e2e
```

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

Every response also gets the code-based dice checks from the
[trajectory test](../go-game/README.md#trajectory-evals), from the same
[graders](lib/trajectory-grader.js). These are fabrication (with its
unexplained and arithmetic kinds), silent reroll, skipped GM roll, misapplied
GM roll, and unused roll narrated. The trajectory test's Haiku
non-invocation judge and output-only judge don't run here, so these checks add
no model calls. They report, they don't gate. They have no thresholds, aren't
k6 checks, and leave the rating and the `final` score alone, so a playthrough
with a fabricated roll can still be GOOD. The fixed-script trajectory test
(`make k6-trajectory`) is still the one that fails on them, and it still runs
on its own.

Each roll ruling the GM makes on Data's checks also goes to the trajectory
test's [ruling judge](../go-game/README.md#trajectory-evals). A ruling is
either "roll it" or "Data just succeeds". The judge is an Opus model acting as
an experienced 5e GM. Unlike the dice checks, its rates have thresholds, so a
run with too many bad rulings shows ✗ and fails (see the ruling judge's
[thresholds](../go-game/README.md#trajectory-evals)). It's still not part of
the rating or `final`.

You can see the results in three places:

- **The k6 summary**, under CUSTOM: `e2e_traj_fabrication`,
  `e2e_traj_fabrication_unexplained`, `e2e_traj_fabrication_arithmetic`,
  `e2e_traj_silent_reroll`, `e2e_traj_gm_roll_skipped`,
  `e2e_traj_gm_roll_misapplied`, `e2e_traj_unused_roll_narrated`, and
  `e2e_traj_flagged` (any of fabrication, reroll, skipped, or unused narrated)
  are the share of narrated responses with each problem, over all
  playthroughs. `e2e_traj_roll_dice_calls` is the GM's `roll_dice` calls per
  response. `e2e_ruling_indefensible` is the share of rulings the ruling judge
  found indefensible. It splits into `e2e_ruling_missed_roll` (Data succeeded
  without a roll a GM should have called for) and `e2e_ruling_unneeded_roll`
  (a roll a GM wouldn't have asked for). Each sample is tagged with its
  `playthrough`, so a k6 output such as Grafana Cloud k6 can split them.
- **The log**: a line such as `guided: turn 12: trajectory: fabricated 17
  (unexplained)` for every response with a finding, and a `ruling
  missed_roll: ...` line with the judge's reason for every indefensible
  ruling. With `E2E_LOG_TRANSCRIPTS=1`, each flagged turn in the transcript
  also has a `trajectory` field, and each ruling a `ruling` field.
- **Agent Observability**: each trial's `traj_*` and `ruling_*` scores (see
  below).

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
adds a judge call and a story-judge call, so a full run makes a few hundred
Anthropic calls.

### Story quality

How creative and enjoyable the GM's story was is a matter of taste, so a
separate story judge (`STORY_JUDGE_MODEL`, by default `JUDGE_MODEL`) scores it
apart from the correctness judge and never fails the run. It reads each whole
game as the player saw it (every input and narration, the player's style, and
the outcome), plus the scenario's solution so it can tell whether the story
paid off what it set up. It scores 1 to 5, each with a reason citing turns:

- **Vividness**: concrete, specific imagery rather than generic science fiction.
- **Creativity**: fresh touches the GM adds within the scenario's facts.
- **Responsiveness**: the story builds on what this player did and how they
  played.
- **Continuity**: early details come back and discoveries build on each other.
- **Pacing**: tension rises and varies, and nothing drags or repeats.
- **Character**: Data, the empty ship, and the Star Trek world feel true.
- **Arc**: a setup, a rising problem, and a resolution that pays off what came
  before.
- **Overall**: how enjoyable the game was to play.

The scale is anchored so that 3 is competent but generic, what an average GM
reading the scenario aloud would give, and a 4 or 5 needs turns that earn it.
Most sessions land on 3, so compare versions by the distribution, not single
runs. The judge also names up to three highlights and lowlights with their
turns and quotes, and writes a short critique. Rule and state errors count only
as far as they hurt the story: a false ending is graded by the correctness
judge, and here it only costs what it does to pacing and arc.

The overall score is the `e2e_story_overall` trend in k6 (by playthrough), and
each trial gets `story_overall` (with the critique, highlights, and lowlights)
and `story_<dimension>` numbers. The rating's comment and metadata carry the
scores too, but they never decide GOOD or BAD. `e2e_story_judged` counts story
verdicts that came back. Real players' own verdicts come from the game's
[`/rate` command](../agento11y/README.md#player-ratings), which is the ground
truth this judge can only approximate.

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
`teardown()` completes it. The candidate's `prompt_version` is the one the
server reports (`gm.GM.PromptVersion`, such as `narrator-notes-v6+forced-gm-rolls-v1`, with
`+ending-guard-v2` when `ASIMOV_ENDING_GUARD` is on); each trial and rating
records it too, and every generation is tagged with it. Each playthrough is a trial of its test case
(`guided`, `adversarial`, or `cooperative`, numbered by attempt), linked to
its conversation. Each trial gets these scores:

- `final`: the same GOOD/BAD verdict as the rating, with its reasons.
- From code: `rescued`, `engine_rules` (every engine, HTTP, and latency check;
  a failed score lists them by turn), and the numbers `inputs_used`,
  `engine_turns`, and `longest_stall` (most inputs in a row that never
  advanced the engine turn, the signal behind the false ending and false
  kill).
- From the trajectory checks: `traj_clean` (no response flagged) and
  `traj_no_fabrication`, `traj_no_unexplained_roll`, `traj_no_silent_reroll`,
  `traj_no_gm_roll_skipped`, `traj_no_gm_roll_misapplied`, and
  `traj_no_unused_roll_narrated`. Each is true when no response in the
  playthrough failed that check, and a false one lists the failing turns. There's
  also the number `traj_roll_dice_calls`. None of them count toward `final`.
- From the ruling judge, when the playthrough made a ruling:
  `ruling_defensible`, true when every ruling was defensible, with each
  ruling, its verdict, and the judge's reason. There are also the numbers
  `rulings_judged`, `ruling_missed_rolls`, and `ruling_unneeded_rolls`. They
  don't count toward `final` either.
- From the judge: `judge_<category>` for each rubric, `intent_<id>` for each
  intent (`handled`, `mishandled`, `not_attempted`, or `not_applicable`, with
  the turns), `findings_<kind>` counts with each finding's turn and
  explanation, and `ending_cause`.
- From the story judge: `story_overall` and `story_<dimension>`, 1 to 5 (see
  [Story quality](#story-quality)). They don't count toward `final`.

Set `E2E_EXPERIMENT=0` to leave the experiment out. Set
`AGENTO11Y_EXPERIMENT_URL_TEMPLATE` (with `{run_id}`) to log a link to it.
The API only accepts trial writes that name the same `source` as the
experiment's creator, and rejects scores that set `evaluator_kind`, so the
test sends `source` on every write and keeps the kind in each score's
metadata. `e2e_trial_reported` counts trials whose scores and completion were
accepted; like ratings, a failed report is logged but does not fail the run.

For a longer sample, or realistic traffic to populate Grafana, set
`E2E_DURATION` (such as `2h`): each scenario keeps starting new playthroughs
for that long, each as a new trial attempt, and a playthrough still in
progress gets up to 15 minutes to finish. If you only want the traffic, add
`-e E2E_RATE=0 -e E2E_EXPERIMENT=0` to leave out the ratings and the
experiment. Set
`E2E_LOG_TRANSCRIPTS=1` to log each playthrough whole (conversation, trial,
verdict, story verdict, failed checks, and every turn with its engine state)
for reading afterwards.

## Results in Grafana Cloud k6

To see k6's own results in Grafana Cloud k6 next to those conversations, run
the test locally and stream its results to the cloud:

```sh
k6 cloud login --stack <your-stack>   # once, with a Grafana Cloud k6 token
K6_CLOUD=1 scripts/k6.sh tests/test-e2e.js
```

`K6_CLOUD=1` (in the shell, `.env`, or `local.mk` for `make test`) makes
`scripts/k6.sh` run
`k6 cloud run --local-execution --no-archive-upload --include-system-env-vars`
in place of `k6 run`, with the game's settings from `.env`.

The test still runs on your machine, so it can reach the game on
`localhost:8080`; only its metrics, checks, and thresholds go to Grafana Cloud
k6. Cloud runs don't pass shell variables to the script unless you add
`--include-system-env-vars`, and those variables (your API keys) would be
uploaded in the run's archive, so `--no-archive-upload` keeps the script and
its environment on your machine.
A fully cloud run (`k6 cloud run` without `--local-execution`) would need the
game at a public URL and the API keys as Grafana Cloud k6 secrets.

