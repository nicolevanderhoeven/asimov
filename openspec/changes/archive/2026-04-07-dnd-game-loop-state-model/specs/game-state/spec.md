## ADDED Requirements

### Requirement: GameState schema covers all structured game data
The system SHALL define a `GameState` Pydantic v2 `BaseModel` that contains the following top-level fields:
- `session_id` (str): unique identifier for the game session
- `turn_number` (int): 0-indexed count of completed turns
- `player` (PlayerState): character stats and inventory
- `location` (LocationState): current room/area name and description
- `quests` (list[QuestState]): active and completed quest records
- `npcs` (list[NPCState]): known NPCs with relationship status
- `turn_history` (list[TurnRecord]): ordered list of all completed turns

`PlayerState` SHALL include: `name`, `character_class`, `hp`, `max_hp`, `armor_class`, `level`, `attributes` (STR/DEX/CON/INT/WIS/CHA as int), `inventory` (list of item name strings), `conditions` (list of active condition strings, e.g. `"poisoned"`).

`LocationState` SHALL include: `name` (str) and `description` (str).

`QuestState` SHALL include: `id`, `title`, `status` (`"active"` | `"completed"` | `"failed"`), `description`.

`NPCState` SHALL include: `name`, `description`, `disposition` (`"friendly"` | `"neutral"` | `"hostile"`).

`TurnRecord` SHALL include: `turn_number`, `player_input`, `dice_rolls` (list), `narrative`, `state_delta` (dict of changed fields).

#### Scenario: Schema instantiation with defaults
- **WHEN** a new `GameState` is created with only `session_id` and a starter `PlayerState`
- **THEN** `turn_number` defaults to `0`, `quests` and `npcs` default to empty lists, and `turn_history` defaults to an empty list

#### Scenario: Schema rejects invalid hp
- **WHEN** a `PlayerState` is instantiated with `hp` greater than `max_hp`
- **THEN** Pydantic validation raises a `ValidationError`

#### Scenario: Schema rejects unknown disposition
- **WHEN** an `NPCState` is instantiated with `disposition` set to an unrecognized string
- **THEN** Pydantic validation raises a `ValidationError`

---

### Requirement: GameState is fully JSON-serializable
The system SHALL serialize any `GameState` instance to a valid JSON string via `state.model_dump_json()` and deserialize it via `GameState.model_validate_json(json_str)` without data loss.

#### Scenario: Round-trip serialization
- **WHEN** a `GameState` instance is serialized to JSON and immediately deserialized
- **THEN** the resulting object is equal to the original (`state == deserialized`)

#### Scenario: Serialized output is a valid JSON object
- **WHEN** `state.model_dump_json()` is called
- **THEN** the result is parseable by `json.loads()` with no exception

---

### Requirement: TurnRecord captures full turn context
The system SHALL append a `TurnRecord` to `GameState.turn_history` at the end of every completed turn, containing the player input, all dice rolls resolved in that turn, the narrative text, and a dict of state fields that changed.

#### Scenario: Turn record created after each turn
- **WHEN** a turn completes successfully
- **THEN** `len(state.turn_history)` increases by exactly 1 and the new record's `turn_number` matches `state.turn_number - 1`

#### Scenario: State delta captures only changed fields
- **WHEN** a turn changes `player.hp` from 10 to 7
- **THEN** `turn_record.state_delta` contains `{"player.hp": 7}` and does not include unchanged fields
