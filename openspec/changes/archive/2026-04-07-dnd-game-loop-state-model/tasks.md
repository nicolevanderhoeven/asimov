## 1. GameState Schema (`game_state.py`)

- [x] 1.1 Define `PlayerState` Pydantic model with `name`, `character_class`, `hp`, `max_hp`, `armor_class`, `level`, `attributes` (dict), `inventory` (list), `conditions` (list); add `hp <= max_hp` validator (`game_state.py`)
- [x] 1.2 Define `LocationState`, `QuestState`, `NPCState` Pydantic models with field constraints and literal enums for `disposition` and `status` (`game_state.py`)
- [x] 1.3 Define `DiceResult` model with `roll`, `modifier`, `raw_result`, `total`, `dc` (optional), `outcome` fields (`game_state.py`)
- [x] 1.4 Define `TurnRecord` model with `turn_number`, `player_input`, `dice_rolls`, `narrative`, `state_delta` fields (`game_state.py`)
- [x] 1.5 Define top-level `GameState` model composing all sub-models, with `session_id`, `turn_number`, `player`, `location`, `quests`, `npcs`, `turn_history` (`game_state.py`)
- [x] 1.6 Add a `STARTER_CHARACTER` factory function returning a default `PlayerState` for session initialization (`game_state.py`)
- [x] 1.7 Write unit tests: schema instantiation, `hp > max_hp` validation error, unknown `disposition` validation error, round-trip JSON serialization (`tests/test_game_state.py`)

## 2. Dice Mechanics (`rules_engine.py`)

- [x] 2.1 Define `DiceTrigger` Pydantic model with `roll` (literal enum of d4/d6/d8/d10/d12/d20), `skill` (optional str), `dc` (optional int), `modifier` (int, default 0) (`rules_engine.py`)
- [x] 2.2 Implement `RulesEngine` class with optional `seed` parameter; use `random.Random(seed)` internally for reproducibility (`rules_engine.py`)
- [x] 2.3 Implement `RulesEngine.resolve(trigger: DiceTrigger) -> DiceResult` — roll, apply modifier, compare to DC, return `DiceResult` (`rules_engine.py`)
- [x] 2.4 Implement `RulesEngine.apply_results(state: GameState, results: list[DiceResult]) -> GameState` — apply HP damage, floor at 0, return updated state copy (`rules_engine.py`)
- [x] 2.5 Write unit tests: roll range (1–N), modifier application, success/failure/hit outcomes, HP floor at zero, seed reproducibility (`tests/test_rules_engine.py`)

## 3. LLM Response Contract (`singleplayer_dnd.py`)

- [x] 3.1 Define `LLMTurnResponse` Pydantic model with `narrative` (str), `state_delta` (dict, default `{}`), `dice_triggers` (list[DiceTrigger], default `[]`) (`singleplayer_dnd.py`)
- [x] 3.2 Implement `build_storyteller_prompt(state: GameState, player_input: str) -> str` — inject serialized state JSON and player input into a system+human prompt template (`singleplayer_dnd.py`)
- [x] 3.3 Implement `invoke_storyteller(prompt: str, llm) -> LLMTurnResponse` — call LLM, parse JSON response with Pydantic, retry once on `ValidationError`, fall back to raw narrative on second failure (`singleplayer_dnd.py`)
- [x] 3.4 Write unit tests for `invoke_storyteller` using mocked LLM: valid response, first-fail/second-valid retry, double-fail fallback (`tests/test_singleplayer_dnd.py`)

## 4. Turn Loop (`turn_loop.py`)

- [x] 4.1 Implement `TurnLoop` class accepting `GameState`, `RulesEngine`, and LangChain LLM as constructor arguments (`turn_loop.py`)
- [x] 4.2 Implement `TurnLoop.validate_input(player_input: str) -> None` — raise `ValueError` on empty or >500-char input (`turn_loop.py`)
- [x] 4.3 Implement `TurnLoop.run(player_input: str) -> tuple[str, GameState]` — execute all 10 ordered steps; treat state as immutable until commit (`turn_loop.py`)
- [x] 4.4 Add state diff helper `compute_delta(before: GameState, after: GameState) -> dict` that returns only changed leaf fields (`turn_loop.py`)
- [x] 4.5 Write unit tests: full happy-path turn, empty input rejection, oversized input rejection, partial failure leaves state unchanged (`tests/test_turn_loop.py`)

## 5. OTel Observability (`loggingfw.py`, `turn_loop.py`)

- [x] 5.1 Add `log_turn_event(event: str, session_id: str, turn_number: int, payload: dict)` helper to `loggingfw.py` that emits a structured JSON log record via `CustomLogFW` (`loggingfw.py`)
- [x] 5.2 Add `log_session_event(event: str, session_id: str, payload: dict)` for `"session_start"` / `"session_end"` events (`loggingfw.py`)
- [x] 5.3 Wrap `TurnLoop.run()` in a `dnd.turn` OTel span; set span attributes: `dnd.session_id`, `dnd.turn_number`, `dnd.player_input_length`, `dnd.llm_retried`, `dnd.dice_roll_count`, `dnd.state_delta_keys` (`turn_loop.py`)
- [x] 5.4 Set span status to `ERROR` and emit `"turn_error"` log event when a turn fails (`turn_loop.py`)
- [x] 5.5 Emit `"turn_complete"` log event with `state_before`, `state_after`, `state_delta`, `dice_rolls`, `narrative` at turn commit (`turn_loop.py`)
- [x] 5.6 Write unit tests for `log_turn_event` and `log_session_event` using a mock OTel log exporter; verify span attributes on happy path and error path (`tests/test_observability.py`)

## 6. Flask Integration (`play.py`)

- [x] 6.1 Add `GET /singleplayer` route returning game metadata (name, description, starter character schema) (`play.py`)
- [x] 6.2 Add `POST /singleplayer/start` route that initializes a new `GameState`, emits `"session_start"` event, and returns `session_id` + initial state JSON (`play.py`)
- [x] 6.3 Add `POST /singleplayer/play` route accepting `{"session_id": ..., "input": ...}`, running a turn, and returning `{"narrative": ..., "state": ..., "dice_rolls": ...}` (`play.py`)
- [x] 6.4 Add `POST /singleplayer/end` route that emits `"session_end"` event and clears the session from in-memory store (`play.py`)
- [x] 6.5 Store active sessions in a module-level `dict[str, GameState]`; sessions are ephemeral (no persistence) (`play.py`)
- [x] 6.6 Write integration tests: start → play (2 turns) → end lifecycle; invalid session ID returns 404; empty input returns 400 (`tests/test_play_singleplayer.py`)

## 7. CLI Integration (`cli_singleplayer.py`)

- [x] 7.1 Create `cli_singleplayer.py` mirroring `cli_play.py` — initialize a session, read input from stdin, print narrative, loop until `quit` or HP = 0 (`cli_singleplayer.py`)
- [x] 7.2 Print a concise stat block (HP, location, active quests) before each prompt (`cli_singleplayer.py`)
