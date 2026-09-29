# The Silent Enterprise

A single-player Star Trek adventure: you play Data; an Anthropic-backed Game
Master narrates an original mystery aboard an empty Enterprise. Investigate the
bridge, sickbay, and engineering, discover what happened, and recover the crew.

It uses Grafana's **AI SDK** for model calls and a typed action tool, and the
**Agent Observability Go SDK** for generation and tool recording. It is a first
playable prototype, with a bounded **2014 5e rules subset and explicit Star
Trek homebrew**.

## Run

Requires Go 1.26.3 (Go's automatic toolchain download can install it). Dependencies
are pinned in `go.mod`/`go.sum`, including the AI SDK revision used for this demo.

From this repository:

```sh
cd go-game
go run ./cmd/enterprise
```

By default this reads `../.env`. Shell environment variables take precedence.
Alternatively, copy `.env.example` to `.env`, fill it in, and run
`go run ./cmd/enterprise --env .env`.

- `ANTHROPIC_API_KEY`: required for the AI GM.
- `ANTHROPIC_MODEL`: defaults to `claude-sonnet-4-6`.
- `AGENTO11Y_ENDPOINT`: generation export endpoint; the existing
  `GRAFANA_CLOUD_SIGIL_ENDPOINT` is also accepted.
- `GRAFANA_CLOUD_INSTANCE_ID` and `GRAFANA_CLOUD_API_KEY`: generation export auth.
- `OTLP_ENDPOINT` and `OTLP_HEADERS`: existing OTLP gateway and base64 basic auth.
- `ASIMOV_AGENT_VERSION`: optional, defaults to `go-experiment-v1`.

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
in-memory session — useful for load testing (see `../tests/*.js`) since
concurrent clients never interleave turns into the same game state the way a
single shared session would.

```sh
go run ./cmd/enterprise --serve --addr :8080
go run ./cmd/enterprise --offline --serve --addr :8080  # no LLM; /resolve returns 503
```

| Method & path | Purpose |
| --- | --- |
| `POST /session` | Create a new session; returns its id and initial state |
| `GET /session/{id}` | Current state |
| `POST /session/{id}/actions` | Submit an exact `{"kind","target"}` action, as `/do` does |
| `POST /session/{id}/resolve` | Submit natural-language `{"input"}`, as free-text play does; an input of `/roll ABILITY` rolls the pending check |
| `POST /session/{id}/improvise` | Submit an exact improvisation, as `/try` does: `{"approach","ability","skill","difficulty","effect"}` |
| `POST /session/{id}/roll` | Roll the pending check: `{"ability"}`, plus `"narrate": true` for GM narration (needs an LLM) |
| `POST /dm` | Start a dice GM conversation (see [Trajectory harness](#trajectory-harness)); needs an LLM |
| `POST /dm/{id}/turns` | Play one `{"input"}` turn with the dice GM; returns the turn's whole trajectory |

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
| `/roll Intelligence` | Roll the check the GM just asked for |
| `/status` | Show location, health, and discovered evidence |
| `/sheet` | Show Data's fixed character sheet |
| `/quit` | Flush telemetry and exit |

Actions that call for a check (scanning the sensors, bypassing or attacking
the drone, isolating the relay) don't resolve right away. The GM names the
check and waits for you to roll it yourself, for example `/roll Intelligence`
or `/roll Dexterity`. The engine then draws the d20, shows it, and resolves the
action, and the GM narrates what happens. Until then the turn hasn't advanced,
and choosing a different action drops the pending roll. The engine still rolls
initiative and the drone's attacks for you. Over HTTP, a response with
`roll_required` means the action is waiting on `POST /session/{id}/roll`.

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
it decides what success and failure do. You then roll with `/roll` as usual.
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
rather than deciding it. Offline, the engine's own message is shown as the GM's
line. Online, it's shown only if narration fails. The model only
interprets intent and narrates; it cannot set rolls, damage, DCs, inventory, or
rescue flags. It receives only discovered scenario facts. A failed narration
does not undo a resolved action. Model interpretation and prose can still be wrong;
direct `/do` and `/try` commands bypass interpretation for reproducible demonstrations.

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
   `answer_question`.
3. Go validates it, rolls dice, and resolves a candidate state. Multiple tool
   requests are rejected; provider failures leave the saved state unchanged.
4. The resolved state is saved; AI SDK `StreamText` narrates with no tools.
5. `agentobservability` middleware records both calls under one conversation ID,
   with `component=action_resolution` or `component=narration`.

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

## Trajectory harness

`cmd/traj` grades a *different* agent from the game above: `internal/dicegm`,
a free-form Dungeon Master whose only mechanic is a `roll_dice` tool
(`notation`, `reason`). Unlike the game engine, nothing forces or checks the
model's rolls. The system prompt says only "Use the roll_dice tool for any
random outcome", every call the model makes is executed and kept, and the
narration is never reconciled with the dice. That is on purpose: the harness
exists to show what the model actually does.

```sh
go run ./cmd/traj -n 50                  # exports to Grafana like the game
go run ./cmd/traj -n 50 --no-telemetry   # local only
```

Each run plays a fixed five-turn script in a fresh conversation, with up to 10
model calls per turn. Every turn is written as one JSONL line to
`traj-traces/<UTC timestamp>.jsonl` (override with `-trace`). A line holds the
player's message, every `roll_dice` call in order with its arguments, dice and
total, each model step, the narration, and the checks. The checks are:

- **Fabrication** (deterministic): a number the narration presents as a roll
  result, in a sentence about rolling, that no call that turn returned.
  Notation, signed modifiers, DC/AC targets, ability scores, HP and decimals
  are ignored. This is a heuristic, so each flag quotes its sentence.
- **Silent reroll**: more than one call in a turn, with the totals, which ones
  were narrated, whether the narrated one was the highest, and how many were
  never mentioned. Legitimate multi-roll turns (initiative, attack, damage)
  count too; their `reason`s tell them apart.
- **Non-invocation**: zero calls, but a small LLM judge
  (`-judge-model`, default Claude Haiku 4.5) answers yes to the neutral
  question "Does the following text report the result of a die roll?"

`tests/test-trajectory.js` runs the same script and checks through k6 against
the `/dm` routes of `--serve`, grading in JavaScript that mirrors
`internal/trajeval`; keep the two in step.

With telemetry on, each harness invocation is also an Agent Observability
experiment. Every turn's checks become scores on the turn's final generation
(the narration) in its run's conversation: `no_fabrication`,
`no_silent_reroll`, `no_non_invocation` (judged turns only), `roll_dice_calls`,
`unmentioned_rolls`, and `final`. The dice GM tags its generations
`component=dicegm`, `scenario=dice-gm`, and `turn=N`, and chains each turn's
calls with parent generation IDs, which the trace records per step. Scores go
to the Agent Observability API, which defaults to the generation endpoint's
host; set `AGENTO11Y_API_ENDPOINT` to override it, and
`AGENTO11Y_EXPERIMENT_URL_TEMPLATE` (e.g.
`https://STACK.grafana.net/a/grafana-sigil-app/offline-experiments/experiments/{run_id}`)
to print a link to the experiment.

The report prints counts per check and per scripted turn, examples
(`-examples`), and the pass rate: the share of runs with no finding in any
turn. `-parallel` (default 4) sets how many runs play at once.

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
