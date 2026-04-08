## Why

The engine supports a two-player D&D game, but lacks a bounded, single-player scenario format. "The Silent Relay" introduces a concrete, data-driven scenario — a Star Trek–inspired investigation — to validate the engine's scenario loading, scene progression, and multi-path resolution under a constrained, production-like story.

## What Changes

- Add a `scenario-loader` capability: reads structured scenario files (JSON) and initialises game state from them
- Add a `scene-progression` capability: drives scene transitions based on outcomes, flags, and approach resolution
- Add a `scenario-tracing` capability: emits per-scenario and per-scene OTel spans with structured attributes

No changes to existing `dice-mechanics`, `game-state`, or `turn-loop` specs are required — the scenario integrates via existing interfaces.

## Capabilities

### New Capabilities

- `scenario-loader`: Defines how a scenario directory (scenario.json, scenes.json, adversaries.json, hazards.json, clues.json, locations.json, initial_state.json, rules_profile.json) is discovered, validated, and loaded into game state at session start
- `scene-progression`: Defines how the engine advances through scenes — evaluating objectives, approach outcomes, and flags to determine `next_scene`; triggers combat, hazard resolution, and clue discovery
- `scenario-tracing`: Defines the OTel span structure emitted for scenario lifecycle events: scenario start/end, scene entry/exit, approach resolution, hazard checks, and final outcome classification

### Modified Capabilities

- `game-state`: The state model gains scenario-scoped fields (`current_scene`, `flags`, `alarm_state`); existing player state shape is unchanged but initial values are now sourced from `initial_state.json`

## Impact

- `two_player_dnd.py`: Will be extended or split to support single-player scenario mode; scenario boot path reads from `scenarios/<name>/`
- `play.py`: May expose a `POST /scenario/start` or query param to select a scenario
- `loggingfw.py`: No changes required; new spans use existing OTel setup
- `scenarios/silent-relay/`: Source data files — these are the reference implementation of the scenario format
- No new dependencies anticipated; existing LangChain + OTel stack is sufficient

## Non-goals

- Multiplayer scenario support
- Scenario editor or authoring UI
- Dynamic scenario generation (LLM-authored scenes)
- Persistent save/resume across sessions
