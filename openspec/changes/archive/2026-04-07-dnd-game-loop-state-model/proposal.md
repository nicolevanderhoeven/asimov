## Why

The existing D&D app (`two_player_dnd.py`) implements a two-player loop between a protagonist agent and a storyteller agent, but has no structured game state — meaning player stats, inventory, quests, and world context are entirely implicit in the LLM conversation history. This makes it impossible to replay sessions, inspect outcomes, trigger rules-based mechanics (e.g. dice rolls, damage), or integrate rich observability beyond raw LLM traces. Extending the app to a single-player one-shot format with explicit state unlocks a much more compelling AI observability demo and a cleaner foundation for future gameplay features.

## What Changes

- Introduce a `GameState` schema representing all structured game data (player stats, inventory, location, quests, NPCs, turn counter)
- Implement a `TurnLoop` that sequences: player input → rules engine → LLM storyteller → state update → serialized log event
- Add a `RulesEngine` component that intercepts dice roll triggers, resolves outcomes, and applies mechanical effects before passing context to the LLM
- Separate narrative text (LLM output) from state mutations (rules engine output) in the response model
- Emit structured OTel events/spans per turn, carrying state deltas as attributes
- Make `GameState` fully serializable to JSON at each turn boundary (enabling replay, inspection, and log export)
- Expose the single-player game via `play.py` alongside the existing two-player mode

## Capabilities

### New Capabilities

- `game-state`: Structured schema and serialization for all game state (player, world, turn history)
- `turn-loop`: Discrete turn processing pipeline — input validation, rules resolution, LLM narration, state commit
- `dice-mechanics`: Dice roll detection, resolution, and outcome application within the rules engine
- `state-observability`: Per-turn OTel span and log emission carrying state snapshots and deltas

### Modified Capabilities

- None — the existing two-player loop in `two_player_dnd.py` is untouched; the new single-player mode is additive

## Impact

- **New files**: `game_state.py` (schema + serializer), `turn_loop.py` (orchestrator), `rules_engine.py` (dice + mechanics), `singleplayer_dnd.py` (game entrypoint)
- **Modified files**: `play.py` (add `/singleplayer` routes), `loggingfw.py` (structured turn-event emission)
- **Dependencies**: No new external packages required; uses existing LangChain, OpenTelemetry, and OpenLIT stack
- **Observability**: Each turn emits a span with state-before/after attributes, enabling Grafana dashboards over game progression

## Non-goals

- Multiplayer or two-player modes are not modified
- Persistent storage (database) — state is in-memory per session; JSON serialization is for logging/replay only
- Complex D&D ruleset (e.g. full 5e combat system) — a simplified dice-and-modifier model is sufficient for the demo
- Authentication or multi-session management
