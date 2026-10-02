# The Silent Enterprise

A single-player Star Trek adventure: you play Data; an Anthropic-backed Game
Master narrates an original mystery aboard an empty Enterprise. Investigate the
bridge, sickbay, and engineering, discover what happened, and recover the crew.

It uses Grafana's **AI SDK** for model calls and a typed action tool, and the
**Agent Observability Go SDK** for generation and tool recording. It is a first
playable prototype, with a bounded **2014 5e rules subset and explicit Star
Trek homebrew**.

## Run

Requires Go 1.26.3 (Go 1.21 or newer downloads it automatically). Dependencies
are pinned in `go.mod`/`go.sum`, including the AI SDK revision used for this demo.

From this repository:

```sh
cd go-game
go run ./cmd/enterprise
```

By default this reads the repository's `../.env` (copy
[`../env.example`](../env.example) to start). Shell environment variables take
precedence, and `--env PATH` reads a different file.
[`../docs/grafana-cloud-setup.md`](../docs/grafana-cloud-setup.md) explains
where each value comes from, and `go run ./cmd/doctor` checks them.

- `ANTHROPIC_API_KEY`: required for the AI GM.
- `ANTHROPIC_MODEL`: defaults to `claude-sonnet-4-6`.
- `AGENTO11Y_ENDPOINT`: the Agent Observability API URL; the legacy
  `GRAFANA_CLOUD_SIGIL_ENDPOINT` is also accepted.
- `GRAFANA_CLOUD_INSTANCE_ID` and `GRAFANA_CLOUD_API_KEY`: generation export auth.
- `OTLP_ENDPOINT` and `OTLP_HEADERS`: the OTLP gateway and its base64 basic
  auth. A plain-HTTP `OTLP_ENDPOINT` on localhost (a local Collector) needs no
  `OTLP_HEADERS`.
- `ASIMOV_AGENT_VERSION`: optional, defaults to `go-experiment-v1`.
- `ASIMOV_SCENARIO`: optional, `classic` (the default) or `generated`; see
  [Scenarios](#scenarios). The `--scenario` flag overrides it.

Grafana configuration is required by default. To deliberately play without
exporting telemetry, use `--no-telemetry`. To play without an API key, LLM, or any
telemetry network calls, use `--offline` and exact `/do` commands.

```sh
go run ./cmd/enterprise --offline
```

The game does not save progress: every run starts a new adventure from scratch,
and state lives only in memory until you quit.

## Serve

`--serve` runs an HTTP API instead of the REPL, giving each client its own
in-memory session — useful for load testing (see [`../tests/`](../tests/README.md)) since
concurrent clients never interleave turns into the same game state the way a
single shared session would.

```sh
go run ./cmd/enterprise --serve --addr :8080
go run ./cmd/enterprise --offline --serve --addr :8080  # no LLM; /resolve returns 503
```

| Method & path | Purpose |
| --- | --- |
| `POST /session` | Create a new session; returns its id, its `scenario` (id, mode, variant, seed, and the opening), and its initial state. An optional body chooses the scenario: `{"scenario": "classic"}`, `{"scenario": "generated"}` for a random one, or `{"seed": N}` to replay one. Without it, the session plays the server's default (`--scenario`) |
| `GET /session/{id}` | Current state |
| `GET /session/{id}/scenario` | The whole scenario, solution included, for tests that grade a playthrough against it; a player never sees it |
| `POST /session/{id}/actions` | Submit an exact `{"kind","target"}` action, as `/do` does; `"no_roll": true` rules its check an automatic success. The engine makes the GM's rolls |
| `POST /session/{id}/resolve` | Submit natural-language `{"input"}`, as free-text play does; an input of `/roll ...` makes the player's pending roll. The response's `gm_rolls` lists every `roll_dice` call the GM made while narrating |
| `POST /session/{id}/improvise` | Submit an exact improvisation, as `/try` does: `{"approach","ability","skill","difficulty","effect"}`, optionally `"no_roll": true`. The engine makes the GM's rolls |
| `POST /session/{id}/roll` | Make the player's pending roll: `{"ability"}`, plus `"narrate": true` for GM narration (needs an LLM), in which the GM makes its own rolls; without it, the engine does |

Sessions are created per-request and held only in memory. `--session-ttl`
(default `30m`) controls how long an idle session is kept before it's reclaimed.

## Play

Type natural language, such as “Read the operations log” or “Take the turbolift
to sickbay.” One input resolves at most one action. Legal outcomes always come
from the current authoritative state, never from the model's memory of past
turns — the game cannot be talked into an outcome it didn't actually resolve.

Each session also replays its own dialogue history (the player's input and the
GM's narration, up to the last `gm.MaxHistoryMessages` messages) into every
`Resolve`/`Narrate` call, so a session's recorded generations read as one
continuous conversation rather than isolated exchanges — this is what backs
Agent Observability's Conversations view. History lives only in memory (not in
`game.State`, which stays free of any LLM-specific type).

| Command | Purpose |
| --- | --- |
| `/actions` | Show the current supported actions and their mechanics |
| `/do inspect logs` | Execute a supported action directly |
| `/try str/athletics hard disable_drone rip it off its mount` | Improvise directly: `ABILITY[/SKILL] DIFFICULTY EFFECT APPROACH` |
| `/roll Intelligence` | Make the roll the GM just asked for: `/roll` and what it names (`Intelligence`, `initiative`, `damage`) or its notation (`1d20+6`) |
| `/status` | Show location, health, and discovered evidence |
| `/sheet` | Show Data's fixed character sheet |
| `/quit` | Flush telemetry and exit |

Dice work the way they do at a table. The GM decides when a roll is called
for: scanning the sensors, bypassing or attacking the drone, and isolating the
relay have checks, but the GM may rule that Data simply succeeds, the way a GM
waves through a task well within a character's strength or skill. When a check
does need a roll, you make Data's rolls yourself: the check, initiative, and
phaser damage, each with `/roll` (for example `/roll Intelligence`, `/roll
initiative`, or `/roll damage`). The GM makes the drone's rolls and the relay
discharge's, with a `roll_dice` tool while narrating. The engine fixes every
roll's dice in advance (the drone attacks with `1d20+3`, or `2d20kl1+3` when
you dodge), and only the roll itself happens during play; it applies whatever
was actually rolled. Initiative decides who acts first: if the drone wins, it
fires before your attack resolves. A roll the GM never makes doesn't happen:
a drone that doesn't roll initiative acts after you, one that doesn't roll to
attack doesn't attack. Until an action's first roll, the turn hasn't advanced
and choosing a different action abandons it; once something is rolled, finish
the action first. Over HTTP, a response with `roll_required` means the action
is waiting on the player's roll.

The GM plays by the improv rule "yes, and". Nothing you try in character is
refused. The turbolift reaches every location, and an action somewhere else
takes you there first: "go to sickbay and pull the biopatterns" from
engineering is a single turn (and leaving mid-fight withdraws from the drone,
which can't pursue). The view also carries spoiler-free `leads`, pointers to
the next unfinished steps, which the GM uses to steer any attempt back toward
finishing the scenario.

You aren't limited to the listed actions. Describe anything else Data tries
("I splice my positronic net into the sensor buffer", "I rip the drone off its
mount") and the GM treats it as an improvised check. The model reads the
attempt as an ability (and optionally a skill), a difficulty for the approach
itself (easy, medium, or hard: DC 10/15/20), and one effect from a short menu
the engine offers for the current situation. Examples are recovering the
frequency another way, disabling the drone without a fight, damaging it in
combat, or setting up advantage on your next roll. Each effect has a minimum
DC. The engine uses the higher of the two and says when it raised the DC, and
it decides what success and failure do. You then roll with `/roll` as usual,
unless the GM rules the attempt needs no roll.
Anything no effect covers is `flavor`: a harmless action like sitting in the
captain's chair, or a long shot like beaming the crew back before the
transporter is ready. Flavor has no roll, no turn, and no mechanical effect. The
GM plays it out in the story ("you order the transport; the computer can't get a
lock…"), and then offers a way forward from the leads. Improvisation offers
other routes to an objective, never a way to skip one. Only out-of-character
attempts to dictate rolls or rules ("I rolled a 20") are treated as
unsupported, and even then the GM stays in character and suggests something to
try.

You can also just ask a question: about the rules, Data's abilities, the scene,
or what you could try. A question changes nothing and doesn't use a turn. The
GM answers from the state and the scene details, and says when Data doesn't
know something yet.

The player hears one voice, the GM's. Behind it, the game engine's result and
the displayed rolls are authoritative, and the model narrates that result
rather than deciding it. Offline, the engine makes the GM's rolls and its own
message is shown as the GM's line. Online, it's shown only if narration fails.
The model interprets intent, rules whether a check needs a roll, makes the
GM's rolls with `roll_dice`, and narrates; it cannot choose what a roll shows,
or set damage, DCs, inventory, or rescue flags. It can still narrate a
different number than the dice showed, which is what the
[trajectory evals](#trajectory-evals) look for. It receives only discovered
scenario facts. A failed narration
does not undo a resolved action. Model interpretation and prose can still be wrong;
direct `/do` and `/try` commands bypass interpretation for reproducible demonstrations.

## Scenarios

Every game has the same shape: gather the evidence, get past whatever guards
the cause of the disappearance, secure the cause, then bring the crew home.
What fills that shape is a `game.Scenario`, and one engine plays them all.

- **Classic** (the default) is the original adventure: a phase relay in
  engineering, the bridge sensors and sickbay's records, a security drone,
  and an electrical discharge. It plays exactly as it always has; the
  engine's tests check that its views, results, prompts, and tool schemas are
  unchanged.
- **Generated** (`--scenario generated`, or `ASIMOV_SCENARIO=generated`)
  builds a new scenario from one module of each kind, so where to go, what to
  do, and which checks it takes change from game to game:

  | Module | Options |
  | --- | --- |
  | Cause (what took the crew, and where) | phase relay in engineering, pattern buffer in the transporter room, holomatrix on the holodeck, alien artifact in the cargo bay, temporal field from deflector control |
  | Key reading (a retryable check) | bridge sensor buffer (Intelligence (Investigation) DC 12), astrometrics sensor array (Wisdom (Perception) DC 12), computer core memory (Intelligence (Investigation) DC 13) |
  | Crew records (no roll) | sickbay medical records, security office internal sensors |
  | Encounter | a fight with a security drone, a malfunctioning exocomp, or a holographic security officer, each with its own statistics and a tricorder bypass; or a skill challenge, a cascading containment field or a security lockout, that needs three successes from any of three approaches, with the GM rolling damage for each failure |
  | Hazard of securing the cause | electrical discharge (Dexterity save), radiation burst (Constitution save), venting plasma (Dexterity save) |

  That's 450 scenarios. The seed picks them, so the same seed always builds
  the same one: the REPL prints it (replay with `--seed N`), and over HTTP the
  session's `scenario` reports it. Every combination is tested to be valid
  and winnable.

A generated scenario's view carries an `encounter` object (`kind`, `name`,
`status` of `active` or `cleared`, and `hp` or `successes` of `needed`) in
place of the classic view's `drone_hp`, and the GM's prompts and `roll_dice`
purposes name its own places and rolls. Generations are tagged
`scenario=silent-enterprise` for the classic adventure, or
`scenario=generated` with `scenario_variant` (such as
`holodeck/astrometrics/security/containment_field/plasma`) and
`scenario_seed`, so evaluator scores can be compared between them; see
[the evaluators](../agento11y/README.md#comparing-scenarios).

## Rules and adaptations

Implemented mechanics: floor((ability score − 10)/2), proficiency, d20 ability
checks and saves, advantage/disadvantage cancellation, initiative, ranged attacks
against AC, natural 1/20 attack rules, critical damage dice, Dodge, and HP.
Natural 20 is not automatic success on an ability check or saving throw.

Data is a custom level-3-equivalent stat block, not a complete official class:
STR 18, DEX 14, CON 16, INT 18, WIS 12, CHA 10; proficiency +2, AC 14, HP 24.
His phaser uses shortbow attack/damage mechanics (+4, 1d6+2); a critical hit rolls
2d6+2. It has enough charge for this short scenario. Arcana is explicitly adapted
to phase technology; the tricorder doesn't grant arbitrary bonuses. The damaged
drone is a custom fixed ranged enemy (AC 12, HP 10, +3 attack, 1d4+1 damage),
15 feet away. It cannot pursue or make melee opportunity attacks. Tied initiative
goes to Data as a fixed GM ruling. At 0 HP Data is disabled and this demo ends;
that is a scenario rule, not an implementation of 5e death saves.

The adventure defines its checks, legal transitions, and success conditions.
Repeated sensor analysis is allowed without an extra penalty. Failed drone
bypass activates combat. The relay hazard always permits progress but can deal
damage. These are authored encounter rulings, not universal 5e rules.
Improvised checks may pair a skill with a different ability than usual (the 5e
variant rule); proficiency still comes only from Data's own skills.

Not yet implemented: open-ended creative outcomes beyond the authored effect
menu, character creation,
classes, spellcasting, leveling, full movement/range simulation, conditions,
death saves, rests, or additional adventures.

Rules references:
[2014 ability checks](https://www.dndbeyond.com/sources/dnd/basic-rules-2014/using-ability-scores),
[2014 combat](https://www.dndbeyond.com/sources/dnd/basic-rules-2014/combat).

## Agent and observability design

1. A player turn opens a `game.turn` span.
2. AI SDK `GenerateText` makes exactly one typed tool call: `resolve_action`
   (a listed action), `propose_improvisation` (a creative attempt), or
   `answer_question`, with the GM's `roll`/`no_roll` ruling on any check.
3. Go validates it and resolves a candidate state as far as the first roll it
   needs. Multiple tool requests are rejected; provider failures leave the
   saved state unchanged. An action waiting on rolls resumes by replay: the
   engine reruns it from where it started with the rolls made so far.
4. The state is saved; AI SDK `StreamText` narrates with one tool,
   `roll_dice` (`notation`, `reason`, optional `purpose`). The GM's calls run
   one at a time in the order it made them; one naming the roll the game
   waits on, with its exact notation, is applied, and the GM hears what
   happens next. Any other call rolls and changes nothing. A GM roll still
   due when the narration ends is skipped.
5. `agentobservability` middleware records the calls under one conversation ID,
   with `component=action_resolution` or `component=narration`, and the
   scenario's tags (see [Scenarios](#scenarios)); every
   `roll_dice` call is also a recorded tool execution and a `game.gm_roll`
   span.

The Agent Observability SDK records tools; OTel exports application/tool spans,
dice events, SDK generation metrics, the custom `game.actions` (by tool) and
`game.improvisations` (by effect, difficulty, and whether the engine raised the
DC) counters, and
structured logs, under `service.name=asimov-enterprise-go`. All requests in a
game share its conversation ID; over HTTP, the session ID doubles as
the conversation ID. Generation data goes to the Agent Observability endpoint;
traces, metrics, and logs go to Grafana Cloud's OTLP gateway. Exporters flush
on exit. Policy hooks are disabled: the local rules engine enforces game
actions.

Candidate tool attempts can appear in telemetry even if a malformed multi-action
model response is ultimately discarded. The saved state remains authoritative.
Metrics and traces also require the corresponding data sources configured in
Grafana. “Export configured” confirms configuration, not server acceptance;
check Conversations, traces, and exporter errors to verify delivery.

API credentials are used only for clients/exporters, never inserted into model
prompts or application logs. Gameplay inputs and outputs are recorded for the
demo. Do not put secrets into player dialogue.

## Trajectory evals

`tests/test-trajectory.js` (k6) grades the GM's dice by the path each response
took, not its prose. It plays a fixed five-line script in a fresh session per
run, answering every roll the game asks the player for with `/roll`, and grades
every response: its `gm_rolls` (each `roll_dice` call with its arguments, dice,
and what it was applied to) against the rolls in its `result` and its
narration.

From the repository root (this starts the server for the run):

```sh
scripts/k6.sh --summary-mode=full -e RUNS=50 -e VUS=4 \
  --log-format=raw --console-output=traj.jsonl tests/test-trajectory.js
```

With `--console-output`, every response is logged as one JSON line with its
trajectory and findings. The checks are:

- **Fabrication** (deterministic): a number the narration presents as a roll
  result, in a sentence about rolling, that no roll that response returned.
  Notation, modifiers, targets (DC, AC, armor class, "defense of 12"),
  ability scores, HP, decimals and markdown emphasis are ignored, and the
  engine's targets count as real. Each flagged number has a kind:
  *arithmetic* when the narration shows the maths from a real roll, or
  *unexplained* when nothing it shows accounts for the number (not proof of
  a lie: the maths may use a modifier the narration never states).
- **Unused roll narrated** (deterministic): the narration reports the result
  of a `roll_dice` call the game didn't use: a free roll, or one it refused.
- **Skipped GM roll** (deterministic): the game waited on a GM roll the GM
  never made, so it didn't happen.
- **Misapplied GM roll** (deterministic): a roll naming the game's purpose
  that the game refused, for the wrong dice or before it was due.
- **Silent reroll**: more than one `roll_dice` call in a response, with the
  totals, which were narrated, and how many never were. A combat round
  (initiative, attack, damage) counts too; the calls' `purpose`s tell them
  apart.
- **Non-invocation**: a response in which nothing was rolled at all, but a
  small LLM judge (`JUDGE_MODEL`, default Claude Haiku 4.5) answers yes to the
  neutral question "Does the following text report the result of a die roll?"

For contrast, an output-only judge (`OUTPUT_JUDGE_MODEL`, default Claude Opus
5.5) grades every response from only the player's input and the reply, never
the trajectory. `traj_output_judge_missed` is how often it passed a response
whose trajectory shows a problem the reply can hide: an unexplained roll, an
unused roll narrated, a skipped GM roll, a roll reported with nothing rolled,
or several calls with some never mentioned. `traj_no_roll_ruling` is how often
the GM ruled a check needed no roll, and `traj_run_passed` is the pass rate:
the share of runs with no finding in any response.

With Grafana Cloud credentials in k6's environment, each k6 run is an Agent
Observability experiment with one scored trial per scripted turn of each run,
on the run's conversation (`TRAJ_EXPERIMENT=0` to skip). The API host defaults
to the generation endpoint's; set `AGENTO11Y_API_ENDPOINT` to override it, and
`AGENTO11Y_EXPERIMENT_URL_TEMPLATE` (e.g.
`https://STACK.grafana.net/a/grafana-sigil-app/offline-experiments/experiments/{run_id}`)
to log a link to the experiment.

The deterministic graders live in `tests/lib/trajectory-grader.js`.
`make k6-graders` (from the repository root) checks them against the cases in
`tests/fixtures/trajectory-graders.json`, with no server or API key; add new
grader cases there. `tests/test-e2e.js` runs the same deterministic checks on
every response of its whole playthroughs. There they are `e2e_traj_*` rates
and `traj_*` trial scores, and they never fail the run (see
[End-to-end conversations](../tests/README.md#end-to-end-conversations)).

## Test

```sh
go test -race ./...
go vet ./...
```

Tests use fixed dice and a fake provider exercising the actual AI SDK. They cover
rules, rejected actions, improvised checks and their DC floors, questions,
hidden evidence, a complete rescue, combat, saves,
single-action enforcement, provider failures, streaming narration, and config.
They do not require credentials or send data to Anthropic/Grafana.

## Attribution

This work includes material taken from the System Reference Document 5.1
(“SRD 5.1”) by Wizards of the Coast LLC and available at
https://dnd.wizards.com/resources/systems-reference-document. The SRD 5.1 is
licensed under the Creative Commons Attribution 4.0 International License
available at https://creativecommons.org/licenses/by/4.0/legalcode.

This is an original, unofficial Star Trek fan scenario for an educational demo.
It does not include text from a published adventure and is not affiliated with
or endorsed by the Star Trek rights holders.
