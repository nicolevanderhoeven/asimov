## MODIFIED Requirements

### Requirement: GameState schema covers all structured game data
The system SHALL define a `GameState` Pydantic v2 `BaseModel` that contains the following top-level fields:
- `session_id` (str): unique identifier for the game session
- `turn_number` (int): 0-indexed count of completed turns
- `player` (PlayerState): character stats and inventory
- `location` (LocationState): current room/area name and description
- `quests` (list[QuestState]): active and completed quest records
- `npcs` (list[NPCState]): known NPCs with relationship status
- `turn_history` (list[TurnRecord]): ordered list of all completed turns
- `scenario` (ScenarioState | None): scenario-scoped state; `None` when no scenario is active

`PlayerState` SHALL include: `name`, `character_class`, `hp`, `max_hp`, `armor_class`, `level`, `attributes` (STR/DEX/CON/INT/WIS/CHA as int), `inventory` (list of item name strings), `conditions` (list of active condition strings, e.g. `"poisoned"`).

`LocationState` SHALL include: `name` (str) and `description` (str).

`QuestState` SHALL include: `id`, `title`, `status` (`"active"` | `"completed"` | `"failed"`), `description`.

`NPCState` SHALL include: `name`, `description`, `disposition` (`"friendly"` | `"neutral"` | `"hostile"`).

`TurnRecord` SHALL include: `turn_number`, `player_input`, `dice_rolls` (list), `narrative`, `state_delta` (dict of changed fields).

`ScenarioState` SHALL include: `current_scene` (str), `flags` (dict[str, str]), `alarm_state` (str, default `"silent"`).

#### Scenario: Schema instantiation with defaults
- **WHEN** a new `GameState` is created with only `session_id` and a starter `PlayerState`
- **THEN** `turn_number` defaults to `0`, `quests` and `npcs` default to empty lists, `turn_history` defaults to an empty list, and `scenario` defaults to `None`

#### Scenario: Schema rejects invalid hp
- **WHEN** a `PlayerState` is instantiated with `hp` greater than `max_hp`
- **THEN** Pydantic validation raises a `ValidationError`

#### Scenario: Schema rejects unknown disposition
- **WHEN** an `NPCState` is instantiated with `disposition` set to an unrecognized string
- **THEN** Pydantic validation raises a `ValidationError`

#### Scenario: ScenarioState initialised from initial_state.json
- **WHEN** a scenario session starts and `initial_state.json` is loaded
- **THEN** `state.scenario.current_scene` equals `scenario.entry_scene`, `state.scenario.flags` is an empty dict, and `state.scenario.alarm_state` equals `"silent"`
