## Context

The engine (`two_player_dnd.py`) runs a two-player D&D loop where a protagonist and storyteller agent take turns. It has no concept of a bounded scenario: there is no structured entry point, no scene graph, no mechanical resolution pipeline.

"The Silent Relay" requires the engine to boot from a scenario directory, traverse a defined scene graph, resolve structured checks and hazards, and terminate cleanly with a classified outcome — all without a second player agent.

The scenario data already exists in `scenarios/silent-relay/` as JSON files. The design task is to wire them into the existing loop with minimal disruption.

## Goals / Non-Goals

**Goals:**
- Define a `ScenarioLoader` that reads and validates a scenario directory into a typed Python structure
- Define a `SceneRunner` that drives the turn loop through scenes, resolving checks, hazards, approaches, and transitions
- Emit structured OTel spans at scenario and scene granularity using the existing OTel setup
- Keep the existing two-player game loop intact; add scenario mode as an opt-in path

**Non-Goals:**
- Replacing or rewriting `two_player_dnd.py`'s agent architecture
- Persistent state across sessions
- Supporting multiple simultaneous scenarios
- Authoring tooling or scenario validation UI

## Decisions

### 1. Scenario as a directory of JSON files (not a single blob)
Each concern (scenes, adversaries, hazards, clues, locations, state, rules) lives in its own file. This matches the existing `scenarios/silent-relay/` layout and keeps files diff-friendly and independently editable.

Alternatives considered: single `scenario.yaml` — rejected because it conflates authoring concerns and grows unwieldy at scale.

### 2. ScenarioLoader validates on load, not at runtime
On `start_scenario()`, all referenced IDs (adversaries, hazards, clues) are cross-validated against their respective data files. Broken references fail fast before any turns are taken.

Alternatives considered: lazy validation per scene — rejected because it produces confusing mid-game errors.

### 3. Scene progression is data-driven, not LLM-decided
The `next_scene` field in each scene definition drives transitions. The LLM storyteller narrates but does not route. Approach resolution (`diplomacy`, `science`, `force`) is resolved deterministically via the existing dice-mechanics engine.

Alternatives considered: let the LLM choose the next scene — rejected because it breaks reproducibility and makes tracing incoherent.

### 4. Single-player mode: remove the protagonist agent, keep the storyteller
In scenario mode, `play.py`'s `POST /play` accepts the human player's action directly. The storyteller LLM narrates outcomes. The protagonist agent is not instantiated.

Alternatives considered: keep both agents — rejected because the scenario is single-player by design and adding a protagonist agent introduces narrative drift.

### 5. OTel spans use the existing tracer from `two_player_dnd.py`
No new OTel configuration. Scenario spans are children of the session-level trace. Attributes follow OTel semantic conventions plus scenario-specific keys (`scenario.id`, `scene.id`, `approach.id`, `outcome.type`).

## Risks / Trade-offs

- **Scenario validation strictness** → Mitigation: validate all IDs on load; surface a clear error message listing broken references
- **LLM narration diverging from mechanical outcome** → Mitigation: pass resolved outcome and scene state explicitly in the storyteller prompt; instruct it to narrate the result, not decide it
- **`two_player_dnd.py` growing complex** → Mitigation: extract scenario logic into `scenario_runner.py`; `two_player_dnd.py` delegates to it when a scenario is active
- **Scene graph cycles or missing `next_scene`** → Mitigation: validate graph completeness (all non-terminal scenes have a valid `next_scene`) at load time

## Migration Plan

1. Add `scenario_runner.py` with `ScenarioLoader` and `SceneRunner` classes
2. Extend `play.py` with `POST /scenario/start?scenario=<name>` and update `POST /play` to route through `SceneRunner` when a scenario is active
3. Update `two_player_dnd.py` to skip protagonist agent init in scenario mode
4. Emit spans in `SceneRunner` using the existing tracer
5. No rollback required — scenario mode is additive and opt-in via query param

## Open Questions

- Should `POST /play` accept a `scenario` field in the JSON body, or is a session-level init endpoint (`/scenario/start`) cleaner? (Leaning toward init endpoint for explicit lifecycle management.)
- Should `alarm_state` from `initial_state.json` affect the storyteller prompt directly, or only influence mechanical outcomes?
