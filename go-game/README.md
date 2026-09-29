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
| `POST /session/{id}/roll` | Roll the pending check: `{"ability"}`, plus `"narrate": true` for GM narration (needs an LLM) |

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

The `[Engine]` result and displayed rolls are authoritative. The model only
interprets intent and narrates; it cannot set rolls, damage, DCs, inventory, or
rescue flags. It receives only discovered scenario facts. A failed narration
does not undo a resolved action. Model interpretation and prose can still be wrong;
direct `/do` commands bypass interpretation for reproducible demonstrations.

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

Not yet implemented: arbitrary creative-action adjudication, character creation,
classes, spellcasting, leveling, full movement/range simulation, conditions,
death saves, rests, or additional adventures. An unsupported action is not
necessarily illegal in tabletop D&D; the app explains that it is unsupported.

Rules references:
[2014 ability checks](https://www.dndbeyond.com/sources/dnd/basic-rules-2014/using-ability-scores),
[2014 combat](https://www.dndbeyond.com/sources/dnd/basic-rules-2014/combat).

## Agent and observability design

1. A player turn opens a `game.turn` span.
2. AI SDK `GenerateText` selects one typed `resolve_action` tool call.
3. Go validates it, rolls dice, and resolves a candidate state. Multiple tool
   requests are rejected; provider failures leave the saved state unchanged.
4. The resolved state is saved; AI SDK `StreamText` narrates with no tools.
5. `agentobservability` middleware records both calls under one conversation ID,
   with `component=action_resolution` or `component=narration`.

The Agent Observability SDK records tools; OTel exports application/tool spans,
dice events, SDK generation metrics, the custom `game.actions` counter, and
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

## Test

```sh
go test -race ./...
go vet ./...
```

Tests use fixed dice and a fake provider exercising the actual AI SDK. They cover
rules, rejected actions, hidden evidence, a complete rescue, combat, saves,
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
