## ADDED Requirements

### Requirement: Dice trigger schema in LLM response
The system SHALL recognize dice triggers declared by the LLM in the `dice_triggers` field of the structured response. Each trigger SHALL conform to:
```json
{ "roll": "d20", "skill": "<attribute>", "dc": <int>, "modifier": <int> }
```
where `roll` is one of `"d4"`, `"d6"`, `"d8"`, `"d10"`, `"d12"`, `"d20"`, `modifier` defaults to `0` if absent, and `dc` (difficulty class) is optional for damage rolls.

#### Scenario: Valid trigger is recognized
- **WHEN** the LLM response contains `dice_triggers: [{"roll": "d20", "skill": "Stealth", "dc": 14}]`
- **THEN** the `RulesEngine` processes exactly one dice roll for that trigger

#### Scenario: Unknown die type is rejected
- **WHEN** a trigger specifies `"roll": "d100"`
- **THEN** Pydantic validation raises a `ValidationError` and the turn loop's fallback path is taken

---

### Requirement: Dice rolls are resolved by the RulesEngine, not the LLM
The system SHALL use Python's `random.randint` (or a seeded equivalent for tests) to resolve all dice rolls. The LLM SHALL NOT determine the numerical outcome of any roll.

#### Scenario: Roll outcome is an integer in the expected range
- **WHEN** a `d20` is rolled
- **THEN** the result is an integer between 1 and 20 inclusive (before modifier)

#### Scenario: Modifier is applied to raw roll
- **WHEN** a `d20` roll with `modifier: 3` yields a raw result of 12
- **THEN** the final outcome is 15

#### Scenario: Rolls are reproducible with a seed
- **WHEN** the `RulesEngine` is initialized with a fixed random seed
- **THEN** re-running the same sequence of triggers produces identical outcomes

---

### Requirement: Roll outcome is classified as success or failure against DC
The system SHALL compare `(raw_roll + modifier)` against `dc`. If the total meets or exceeds the DC, the outcome is `"success"`; otherwise it is `"failure"`. If no `dc` is provided, the outcome is `"hit"` (for damage rolls).

#### Scenario: Roll meets DC — success
- **WHEN** `raw_roll + modifier >= dc`
- **THEN** `DiceResult.outcome` is `"success"`

#### Scenario: Roll below DC — failure
- **WHEN** `raw_roll + modifier < dc`
- **THEN** `DiceResult.outcome` is `"failure"`

#### Scenario: Damage roll without DC
- **WHEN** a trigger has no `dc` field
- **THEN** `DiceResult.outcome` is `"hit"` and the raw roll total is the damage value

---

### Requirement: Dice results are applied to GameState
The system SHALL apply dice outcomes to `GameState` according to a fixed mapping:
- A `"failure"` on an attack roll against a hostile NPC: no HP change to player
- A `"hit"` on a damage roll: reduce `player.hp` by the damage value (minimum 0)
- A `"success"` on a skill check: narrative hint provided; no mandatory state change
- HP SHALL never fall below 0

#### Scenario: Player takes damage on a hit
- **WHEN** a damage roll of 6 is resolved and `player.hp` is 10
- **THEN** `player.hp` becomes 4 in the committed `GameState`

#### Scenario: Player HP floored at zero
- **WHEN** a damage roll exceeds current `player.hp`
- **THEN** `player.hp` is set to 0, not a negative value

#### Scenario: Skill check success leaves HP unchanged
- **WHEN** a `d20` Perception check with `dc: 12` succeeds
- **THEN** `player.hp` is unchanged in the committed `GameState`

---

### Requirement: DiceResult is recorded in TurnRecord
The system SHALL include all `DiceResult` objects from a turn in `TurnRecord.dice_rolls`, each containing `roll`, `modifier`, `raw_result`, `total`, `dc` (if applicable), and `outcome`.

#### Scenario: DiceResult captured in turn history
- **WHEN** a turn resolves a `d20` roll with `modifier: 2`, `raw_result: 10`, `dc: 14`
- **THEN** `turn_record.dice_rolls[0]` contains `{"roll": "d20", "modifier": 2, "raw_result": 10, "total": 12, "dc": 14, "outcome": "failure"}`
