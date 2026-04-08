## Context

The existing app (`two_player_dnd.py`) runs a LangChain-based conversation loop between a protagonist agent and a storyteller agent. Game context lives entirely inside the LLM conversation history — there is no structured state object, no rules resolution, and no per-turn observability beyond raw LLM traces from OpenLIT.

`play.py` exposes this as `POST /play` (and `GET /` for metadata). `loggingfw.py` provides OTLP log export. We are extending the app with a single-player mode that introduces structured state, a discrete turn loop, and a lightweight rules engine — while leaving the two-player path untouched.

## Goals / Non-Goals

**Goals:**
- Define a `GameState` Pydantic model covering all game data (player, world, quests, NPCs, history)
- Implement a `TurnLoop` that sequences: input → rules check → LLM narration → state commit → OTel emit
- Build a `RulesEngine` that detects and resolves dice roll triggers deterministically
- Separate narrative text from state mutations in the LLM response contract
- Emit a structured OTel span + log event per turn with state-before/after attributes
- Make `GameState` fully JSON-serializable at each turn boundary

**Non-Goals:**
- Modifying the existing two-player game loop
- Persistent storage (database, file system) — state is in-memory per session
- Full D&D 5e ruleset — a simplified d20 + modifier model is sufficient
- Multi-session management or authentication

## Decisions

### 1. GameState: Pydantic v2 BaseModel

**Decision**: Represent all game state as a `Pydantic v2 BaseModel` hierarchy.

**Rationale**: Pydantic provides built-in JSON serialization (`model.model_dump_json()`), schema validation, and is already used by LangChain internally — keeping the dependency footprint flat. It also generates a JSON Schema that can be referenced in specs and tests.

**Alternative considered**: Plain `dataclass` or `TypedDict` — lower overhead but no validation, no JSON schema, harder to version.

---

### 2. Dice Roll Ownership: Rules Engine, not LLM

**Decision**: Dice triggers are declared in the LLM's structured output; the `RulesEngine` executes all rolls and applies outcomes before the narrative is finalized.

**Rationale**: Keeps outcomes deterministic and reproducible. The LLM describes *what* needs rolling (e.g. `{"roll": "d20", "skill": "Perception", "dc": 14}`); the engine rolls and feeds the result back for narrative generation. This makes every outcome observable, loggable, and replayable.

**Alternative considered**: Let the LLM narrate outcomes freely — simpler but non-reproducible; cannot be diffed or replayed.

---

### 3. Turn Orchestration: Custom TurnLoop class, not LangChain chain

**Decision**: A dedicated `TurnLoop` class owns the step sequence; it is not modelled as a LangChain `Chain` or `Agent`.

**Rationale**: The loop requires mutable state mutations between steps (state-before, dice resolution, state-after). LangChain chains are stateless by design and would require invasive custom callbacks. A plain Python class is simpler, easier to unit-test, and easier to instrument with OTel spans.

**Alternative considered**: LangChain `RunnableWithMessageHistory` — handles memory but not state mutation hooks; would need wrapping anyway.

---

### 4. LLM Response Contract: Structured JSON with narrative + actions

**Decision**: The storyteller LLM is prompted to return a JSON object:
```json
{
  "narrative": "<story text>",
  "state_delta": { "<field>": "<value>" },
  "dice_triggers": [{ "roll": "d20", "skill": "Stealth", "dc": 12 }]
}
```

**Rationale**: Clean separation of narrative (for display) and mechanics (for the rules engine and state manager). Pydantic validates the response before processing; malformed output triggers a retry or fallback.

**Alternative considered**: Parse free text for action keywords — brittle, hard to test, breaks observability.

---

### 5. State Storage: In-memory with per-turn JSON snapshot logged via OTel

**Decision**: `GameState` is held in memory for the session lifetime. At each turn boundary, `state.model_dump()` is serialized and attached to the OTel log event and span.

**Rationale**: Keeps the demo self-contained (no DB dependency). The OTel log stream provides an immutable audit trail. A session can be replayed by re-feeding the log events.

**Alternative considered**: SQLite per-session — adds infrastructure complexity not needed for a demo.

## Risks / Trade-offs

- **LLM structured output failures** → LLM may return malformed JSON. Mitigation: Pydantic validation with retry (up to 2 attempts); fall back to narrative-only response with no state mutation.
- **Per-turn OTel spans add latency** → Each turn emits a span + log record synchronously. Mitigation: Use `BatchSpanProcessor` (already configured); acceptable overhead for a demo.
- **In-memory state lost on restart** → Sessions are ephemeral. This is by design (non-goal); documented clearly.
- **Simplified dice model vs 5e accuracy** → Omitting advantage/disadvantage, spell slots, etc. keeps the engine auditable. Can be extended without changing the state schema.

## Migration Plan

1. Add `game_state.py`, `turn_loop.py`, `rules_engine.py`, `singleplayer_dnd.py` as new modules
2. Add `GET /singleplayer` and `POST /singleplayer/play` to `play.py` — existing `/` and `/play` routes unchanged
3. Extend `loggingfw.py` with a `log_turn_event()` helper for structured turn events
4. No schema migrations (no persistent storage)
5. Rollback: remove the new routes and modules; zero impact on existing two-player game

## Open Questions

- **Character creation**: Should the player choose class/name via an initial `POST /singleplayer/start` call, or use a fixed starter character for simplicity? (Recommendation: fixed starter for v1; parameterised start in a follow-up)
- **State context window**: Should the full `GameState` JSON be injected into every LLM prompt, or a prose summary? Full JSON is more precise but consumes more tokens.
