# Asimov's Zeroth Law of Robotics: Observability for AI

Author: Nicole van der Hoeven ([site](https://nicole.to/site), [Mastodon](https://pkm.social/@nicole), [email](mailto:nicole@grafana.com))

This is a repository for the slides and code for the talk "Asimov's Zeroth Law of Robotics: Observability for AI" presented at:
- [KubeCon Europe 2025](https://nicolevanderhoeven.com/blog/20250402-asmiovs-zeroth-law-of-robotics/) in London, England ([video link](https://www.youtube.com/watch?v=x6EKTCAWtn8))
- [Dutch Cloud Native Days 2025](https://nicolevanderhoeven.com/blog/20250703-asimovs-zeroth-law-dutch-cloud-native-day/) in Utrecht, the Netherlands
- [Newcrafts 2025](https://nicolevanderhoeven.com/blog/20251106-asimovs-zeroth-law-newcrafts/) in Paris, France ([slides](https://nicole.to/asimovslides))
- ExpoQA 2026 in Madrid, Spain ([slides](https://nicole.to/expoqa2026))

This repository consists of:
- The Go app in [`go-game/`](go-game/README.md): **The Silent Enterprise**, using Grafana AI SDK and Agent Observability. It runs either as an interactive CLI (`go run ./cmd/enterprise`) or as an HTTP API (`go run ./cmd/enterprise --serve`) for testing.
- One fixed-prompt k6 regression test in [`tests/test-code.js`](tests/test-code.js). It checks the Python-era positronic, Enterprise, and role-confusion cases with JavaScript, plus basic game and HTTP behavior. k6 itself makes no Anthropic calls.
- One AI-judged k6 test in [`tests/test-ai.js`](tests/test-ai.js). Claude varies the lore and role probes, and another Claude model grades the game's narration against facts fixed in the script.
- One end-to-end k6 test in [`tests/test-e2e.js`](tests/test-e2e.js). It plays whole conversations in one session each: a scripted route, an adversarial route, and three playthroughs by a Claude player. Each one must end with the crew rescued. Code checks the engine's rules on every turn, and Claude judges each whole transcript. The run is an Agent Observability experiment with a scored trial per playthrough, and each playthrough is also rated on its conversation.
- One k6 trajectory test in [`tests/test-trajectory.js`](tests/test-trajectory.js). It plays a fixed script against the game, where the GM decides when Data's checks need a roll and makes the drone's rolls itself with a `roll_dice` tool, and grades the path each response took: roll numbers the narration made up, rolls it narrated that the game never used, GM rolls it owed but never made, more than one call in a response, and rolls reported when nothing was rolled. For contrast, an output-only judge grades the same responses without the trajectory.
- One k6 grader check in [`tests/test-trajectory-graders.js`](tests/test-trajectory-graders.js). It runs the trajectory test's graders against fixed cases in [`tests/fixtures/trajectory-graders.json`](tests/fixtures/trajectory-graders.json), with no server or API key.
- One short k6 traffic seed in [`tests/test_traffic.js`](tests/test_traffic.js). It drives the game's natural-language endpoint to populate application telemetry in Grafana.
- Agent Observability online evaluators and rules in [`agento11y/`](agento11y/). LLM judges score the game's live generations for its known defects, such as false endings, false kills, and rolls made by the wrong side of the table. See [Online evaluators](#online-evaluators).
- (optional) A local OpenTelemetry Collector setup in [`collector/`](collector/) for routing telemetry through a Collector pipeline instead of direct OTLP. See [`collector/README.md`](collector/README.md).

![A diagram of the architecture from the original version of this talk: a Flask app sending traces, metrics, and logs to OpenTelemetry and generations to the Sigil API, with k6 driving load against it and everything terminating in Grafana Cloud. The app has since been rewritten in Go (see go-game/), but the OpenTelemetry/Grafana Cloud side of this diagram still applies.](/assets/Asimov's%20Zeroth%20Law%20of%20Robotics%20-%20ExpoQA%202026.jpg)

## Setup

Telemetry is Grafana's Agent Observability SDK + OTel, wired up in [`go-game/internal/telemetry`](go-game/internal/telemetry/telemetry.go). Agent Observability handles normalized generation export, and OTel handles `gen_ai.*` metrics, traces, and structured logs.

By default all signals (generations, traces, metrics, logs) go **directly** to Grafana Cloud's OTLP gateway. A local OTel Collector is **not required**. If you want to route through a Collector — for buffering, sampling, redaction, or fan-out — see the optional setup in [`collector/`](collector/).

### Steps

1. Create a free [Grafana Cloud](https://nicole.to/kceu2025grafana) account (or use an existing stack) and enable the **Sigil** app on that stack.
2. Install Go 1.26.3 or newer (or let Go's toolchain auto-download handle it).
3. Copy `env.example` to `.env`: `cp env.example .env`.
4. Fill in `.env`:
   - `ANTHROPIC_API_KEY` — your Anthropic API key.
   - **OTel (metrics + traces + logs):**
     - `OTLP_ENDPOINT` — your stack's OTLP gateway, e.g. `https://otlp-gateway-prod-us-central-0.grafana.net/otlp`.
     - `OTLP_HEADERS` — base64-encoded `"<instance_id>:<otlp_write_token>"`. The app prefixes `Basic ` automatically.
   - **Agent Observability (generations):**
     - `AGENTO11Y_ENDPOINT` (or the legacy alias `GRAFANA_CLOUD_SIGIL_ENDPOINT`) — e.g. `https://sigil-prod-us-central-0.grafana.net/api/v1/generations:export`.
     - `GRAFANA_CLOUD_INSTANCE_ID` — your Grafana Cloud instance ID (or set `GRAFANA_CLOUD_INSTANCE` as an alias).
     - `GRAFANA_CLOUD_API_KEY` — a Grafana Cloud API key with Sigil-write scope.
   - **Optional:**
     - `ASIMOV_AGENT_VERSION` — explicit agent version. Defaults to `go-experiment-v1`.
5. In the Sigil app, link `grafanacloud-<stack>-prom` as the Prometheus datasource and your stack's Tempo as the traces datasource. Without this, conversations will appear but rollup panels stay empty.
6. Install k6 by following the instructions [here](https://nicole.to/asimovk6) if you want to run load tests.

See [`go-game/README.md`](go-game/README.md) for full run instructions (CLI usage, `--offline` mode, and the agent/observability design).

## Usage

1. Run the game: `cd go-game && go run ./cmd/enterprise`. See [`go-game/README.md`](go-game/README.md) for CLI commands (`/do`, natural language, `--offline`, etc.).
2. Interact with the game.
3. Monitor your app using the GenAI Observability dashboard as well as the Drilldown Logs/Metrics/Traces features in Grafana.
4. Run the k6 scripts against the app's HTTP API instead of the CLI. Start the server with `cd go-game && go run ./cmd/enterprise --serve --addr :8080` (it loads `../.env`). These scripts use `/resolve`, so the server must run with a working Anthropic key; `--offline` disables that route. Use `-e BASE_URL=http://host:8080` with k6 if the server is elsewhere.

   | Command | Purpose |
   | --- | --- |
   | `k6 run tests/test-code.js` | One pass of fixed prompts and code assertions. Only the game calls Anthropic. |
   | `k6 run tests/test-ai.js` | One pass of varied probes and LLM judgments. k6 needs `ANTHROPIC_API_KEY` in its own environment. |
   | `k6 run tests/test-e2e.js` | Five whole playthroughs, each of which must rescue the crew within its input budget, with per-turn checks and whole-transcript judgments. Takes 5–10 minutes. k6 needs `ANTHROPIC_API_KEY` in its own environment, plus the Grafana Cloud variables below to rate conversations. |
   | `k6 run --summary-mode=full tests/test-trajectory.js` | Ten runs of a five-turn script, answering the game's roll requests with `/roll`, with every response graded on its trajectory. Set `-e RUNS=50 -e VUS=4` for more. Add `--log-format=raw --console-output=traj.jsonl` to save one JSON line per response with its full trajectory and findings. k6 needs `ANTHROPIC_API_KEY` in its own environment for the judges, plus the Grafana Cloud variables below to record an Agent Observability experiment (`-e TRAJ_EXPERIMENT=0` to skip). |
   | `k6 run tests/test-trajectory-graders.js` | The trajectory graders against fixed cases, in about a second. Needs no server or API key. |
   | `k6 run tests/test_traffic.js` | One minute of paced game traffic, including actions, a question, and a rejected rule override. Use `-u 2 -d 3m` to seed more traffic. |

   The AI test uses `GENERATOR_MODEL=claude-sonnet-4-6` and `JUDGE_MODEL=claude-opus-5-5` by default; set those k6 environment variables to change models. The end-to-end test uses `PLAYER_MODEL=claude-sonnet-4-6` and the same `JUDGE_MODEL`. The trajectory test uses `JUDGE_MODEL=claude-haiku-4-5-20251001` by default, a small judge asked only whether a zero-roll turn reports a die roll, and `OUTPUT_JUDGE_MODEL=claude-opus-5-5` for the output-only contrast. All six scripts fail the run when a check fails; for the trajectory test that means the model misbehaved, which is the point, and the `traj_*` rates and per-turn checks show how often. The traffic seed populates **the game's** configured Grafana telemetry through its server; k6's own metrics need a separate k6 output configuration to appear in Grafana. Each `/resolve` request usually makes two game model calls, so increasing traffic also increases Anthropic usage.

### Original code-based AI checks

`tests/test-code.js` reuses the three original prompts from `tests/test.js` at
commit `98b13b5`. The Go API returns `narration` and an authoritative `result`
instead of the Python response's `speaker` field, and it does not promise the
old turn-ending phrase. The H01–H09 checks therefore look for the same lore
(`positronic` and ship), a GM response that returns control to Data, no role
switch, and continued focus on the missing crew. Each prompt gets a fresh
session so a prior answer cannot supply a keyword. The code checks are useful
regression signals, but a keyword match alone does not prove a factual answer;
the AI test evaluates the full narration against explicit lore and role rubrics.

### End-to-end conversations

`tests/test-e2e.js` tests what only shows up across a whole conversation. Every
`/resolve` call replays the session's history, so an invented detail, a leaked
clue, or a planted instruction can surface several turns after it entered.

- **Scripted playthrough**: fixed wording on a state-driven route to the
  rescue, including a mid-game question and "try that again" after a failed
  scan. Budget: 30 inputs.
- **Adversarial playthrough**: the same route with a planted "engage" win
  trigger that is fired much later, a role swap, a stale `/roll` after an
  abandoned check, "go back to where I started", and a false claim that the
  drone is disabled. After more than 20 narrated turns (the game's history
  window), it asks for the player's first input, which the GM should no
  longer know. Budget: 45 inputs.
- **Claude player** (3 in parallel): a cooperative player that sees only the
  narration. Budget: 30 inputs.

Every turn checks the engine's rules (turns, clues, HP, the rescue's
preconditions, and the session view), clue leaks, crew dialogue before the
rescue, repeated narration, and the GM's voice. Every playthrough must end
`rescued`, with a final narration that closes the scene and a 409 for any
further input; `e2e_rescued` records the rate. Latency for the last five
inputs must stay within 3× the first five, since history is capped. Claude
then judges the adversarial and Claude-player transcripts for consistency,
leaks, resisted injections, voice, repetition, and steering, and names why a
playthrough did not end. Each turn makes two game model calls and a
Claude-player turn adds a third, so a full run makes a few hundred Anthropic
calls.

After each playthrough, the test posts a conversation rating to Agent
Observability on that playthrough's conversation (the session ID is the game's
conversation ID). The rating is GOOD only if the crew was rescued and every
check and judgment passed; otherwise it is BAD. Its comment gives the ending,
the judge's ending cause and failing reasons, and the failed checks by turn,
and its metadata holds the same results as fields. Ratings use the game's own
`.env` settings (`GRAFANA_CLOUD_SIGIL_ENDPOINT` or `AGENTO11Y_ENDPOINT`,
`GRAFANA_CLOUD_INSTANCE`, and `GRAFANA_CLOUD_API_KEY`) and are skipped when
those are unset or `E2E_RATE=0`. A failed rating is logged and counted in
`e2e_rating_submitted` but does not fail the run.

The run is also recorded as an Agent Observability experiment. `setup()`
creates it, with the game's agent version and model as the candidate (set
`GIT_SHA=$(git rev-parse --short HEAD)` to record the commit too), and
`teardown()` completes it. Each playthrough is a trial of its test case
(`scripted`, `adversarial`, or `claude-player`, attempts 1–3), linked to its
conversation. Each trial gets these scores:

- `final`: the same GOOD/BAD verdict as the rating, with its reasons.
- `rescued`, plus one deterministic pass/fail score per check family:
  `engine_rules`, `no_false_ending`, `no_clue_leaks`, `no_early_crew_dialogue`,
  `gm_voice`, `no_repetition`, `clean_ending`, `scripted_beats` (scripted and
  adversarial only), `flat_latency` (15 or more inputs), and `player_inputs`
  (Claude player only). Each failed score lists its failed checks by turn.
- The numbers `inputs_used`, `engine_turns`, and `false_ending_turns`.
- For judged playthroughs, `judge_<category>` for each rubric and
  `ending_cause`, with the judge's reasons.

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

### Online evaluators

The k6 tests grade the game when you run them. The definitions in
[`agento11y/`](agento11y/) grade it all the time: Agent Observability
rules send a sample of the `asimov-enterprise-go` agent's generations, from
any source, to LLM-judge evaluators, and the scores appear on each
conversation and on the agent's Performance view, per agent version. They
check behavior the narrator's prompt forbids but the engine can't enforce,
including the defects this demo keeps on purpose.

Each narration's system prompt ends with the engine result as JSON, so a
judge can compare what the GM says with the authoritative state. Generations
carry a `component` tag (`narration` or `action_resolution`) from
[`go-game/internal/gm/gm.go`](go-game/internal/gm/gm.go), which the rules
match on. Every evaluator returns a single pass/fail key.

| Evaluator | Scores | Fails when |
| --- | --- | --- |
| `asimov_no_false_ending` | Narration | The GM acts out the rescue or declares the scenario complete while the engine's status isn't `rescued`. |
| `asimov_no_false_kill` | Narration | The GM says the drone is destroyed, disabled, or dark while the engine still has it at positive HP. |
| `asimov_roll_ownership` | Narration | A roll is made by the wrong side of the table: the GM skips its own roll (the drone's or the relay discharge's) or tells the player to make it, rolls one of Data's rolls with `roll_dice`, asks the player to type their result, takes a number the player typed as a roll, or tells the player to `/roll` when no roll is due. |
| `asimov_dice_fidelity` | Narration | A roll value or outcome isn't backed by the engine's rolls or a `roll_dice` result, including the outcome of a roll the game never applied. The online counterpart of [`tests/lib/trajectory-grader.js`](tests/lib/trajectory-grader.js). |
| `asimov_gm_voice` | Narration | The GM mentions the engine or the game's internals, refuses or blocks the player instead of "yes, and", or narrates Data in the third person. |
| `asimov_resolution_intent` | Action resolution | The resolver's single tool call doesn't match the player's input: the wrong action or tool, a player-dictated roll or fact handled as a normal action instead of `unsupported`, an in-character attempt marked `unsupported`, or `no_roll` on a task that could fail. |

| Rule | Evaluators | Sample rate |
| --- | --- | --- |
| `asimov_narration_defects` | false ending, false kill, roll ownership | 0.5 |
| `asimov_narration_quality` | dice fidelity, GM voice | 0.1 |
| `asimov_resolution` | resolution intent | 0.1 |

The defects rule samples more because false kills (about 1% of narrations)
and GM rolls (about 1 in 11) are rare. All rules use the
`all_assistant_generations` selector: resolver generations contain only a
tool call, so `user_visible_turn` would never match them. No rule is scoped
to an agent version, so versions such as `pre-roll-dice` and `roll-dice-v2`
compare side by side. Each judge call reads about 3,000 tokens, so a full
`tests/test-e2e.js` run costs a few hundred judge calls.

To set them up on a new stack with the gcx CLI:

1. Log in and select the stack: `gcx login`, then confirm with
   `gcx config current-context`.
2. Check the judge model is available on that stack:
   `gcx agento11y judge list-providers` and
   `gcx agento11y judge list-models --provider <provider>`. The definitions
   use `anthropic-vertex` and `claude-sonnet-4-6`; if your stack offers
   something else, change `provider` and `model` in each file under
   `agento11y/evaluators/`.
3. Create the evaluators, then the rules that use them:

   ```sh
   for f in agento11y/evaluators/*.yaml; do gcx agento11y evaluators upsert -f "$f"; done
   for f in agento11y/rules/*.yaml; do gcx agento11y rules create -f "$f"; done
   ```

4. Play the game or run a k6 test, then check the scores, failures first:

   ```sh
   gcx agento11y rules list-scores asimov_narration_defects --passed=false -o json
   ```

   Rules only score generations that arrive after they exist, and the
   evaluation is asynchronous, so allow a few minutes.

To change an evaluator, edit its file, raise its `version` (re-using a
version is rejected), and run `upsert` again. To change a rule, run
`gcx agento11y rules update <rule-id> -f agento11y/rules/<rule-id>.yaml`.
Before you upsert a changed judge, try it on a real generation without
saving anything:

```sh
agento11y/test-evaluator.sh agento11y/evaluators/asimov_no_false_kill.yaml <generation-id> [<conversation-id>]
```

It needs [yq](https://github.com/mikefarah/yq). Find generation IDs with
`gcx agento11y conversations get <conversation-id>`. In a judge's prompt,
`{{tool_calls}}` holds only the calls in the judged generation's own output,
not those from earlier steps of the same narration; `{{tool_results}}` holds
them all.

## Resources

- [Grafana Sigil SDK](https://github.com/grafana/sigil-sdk) for normalized LLM generation telemetry, metrics, and traces on Grafana Cloud
- [OpenTelemetry](https://opentelemetry.io/) for instrumentation
- Free [Grafana Cloud](https://nicole.to/kceu2025grafana) for visibility
- [Loki](https://nicole.to/lokirepo) for logs
- [Prometheus](https://nicole.to/promrepo) for metrics
- [Tempo](https://nicole.to/temporepo) for traces
- [k6](https://nicole.to/k6repo) for testing

## References

Asimov, I. (1942). Runaround. In I, Robot (pp. 1-42). Gnome Press.

Pictures in presentation:
- https://animalia-life.club/qa/pictures/hal-9000-im-sorry-dave
- https://screenrant.com/star-trek-next-generation-data-make-no-sense-illogical/
- https://www.blogtorwho.com/smile-reactions/
- https://emsonthra.wordpress.com/2017/05/12/character-analysis-david-8/
- https://daleksrus.fandom.com/wiki/The_Daleks
- https://warnerbros.fandom.com/wiki/Rosey
- https://moviesandmania.com/2014/04/10/hell-is-other-robots-futurama-episode-animated-tv/
- https://www.reddit.com/r/SummerGlau/comments/gs9rlj/battle_damaged_terminator_used_unseen_outtake/
- https://theconversation.com/how-long-until-we-can-build-r2-d2-and-c-3po-52400
- https://www.flickr.com/photos/elferrada/2708912082
- https://memory-alpha.fandom.com/wiki/Locutus_of_Borg
- https://ita.animalia-life.club/vero-acciaio-tutti-i-personaggi-dei-robot

Quotes:
- https://grafana.com/blog/going-beyond-ai-chat-response-how-were-building-an-agentic-system-to-drive-grafana
- https://www.datadoghq.com/blog/engineering/bits-ai-eval-platform/#why-tool-level-testing-and-live-replay-werent-enough
- https://www.anthropic.com/engineering/demystifying-evals-for-ai-agents
