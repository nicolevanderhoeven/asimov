## ADDED Requirements

### Requirement: Turn loop executes in a fixed sequence
The system SHALL process each player turn in the following ordered steps with no steps skipped:
1. Validate player input (non-empty, within length limit)
2. Pass current `GameState` + player input to the `RulesEngine` for pre-LLM checks
3. Invoke the storyteller LLM with a prompt containing the serialized state and player input
4. Validate the LLM response structure (Pydantic)
5. Pass any `dice_triggers` from the LLM response to the `RulesEngine` for resolution
6. Apply `state_delta` and dice outcomes to `GameState` to produce `GameState'`
7. Append a `TurnRecord` to `GameState'.turn_history`
8. Increment `GameState'.turn_number`
9. Emit an OTel span and log event for the turn
10. Return the narrative text and updated `GameState'` to the caller

#### Scenario: All steps execute on a normal turn
- **WHEN** a player submits valid input and the LLM returns a well-formed response
- **THEN** all 10 steps complete and the caller receives a narrative string and an updated `GameState` with `turn_number` incremented by 1

#### Scenario: Step ordering is preserved
- **WHEN** a turn contains both a dice trigger and a state delta
- **THEN** dice resolution (step 5) occurs before state mutation (step 6)

---

### Requirement: Player input is validated before processing
The system SHALL reject player input that is empty or exceeds 500 characters, returning an error without invoking the LLM or mutating state.

#### Scenario: Empty input rejected
- **WHEN** a player submits an empty string
- **THEN** the turn loop returns an error response and `GameState.turn_number` remains unchanged

#### Scenario: Oversized input rejected
- **WHEN** a player submits a string longer than 500 characters
- **THEN** the turn loop returns an error response indicating the input is too long

---

### Requirement: LLM response validation with retry on malformed output
The system SHALL validate the LLM response against the expected JSON schema. If validation fails, the system SHALL retry the LLM call once. If the second attempt also fails, the system SHALL return the raw LLM text as the narrative with no state mutation.

#### Scenario: First LLM response is valid
- **WHEN** the LLM returns a well-formed JSON response on the first attempt
- **THEN** no retry is made and processing continues normally

#### Scenario: First response invalid, second valid
- **WHEN** the first LLM response fails Pydantic validation and the second attempt succeeds
- **THEN** the second response is used and state is mutated normally

#### Scenario: Both responses invalid
- **WHEN** both LLM attempts return malformed output
- **THEN** the raw text of the second response is used as the narrative and `state_delta` is empty (no state mutation)

---

### Requirement: State is immutable during a turn until commit
The system SHALL not mutate the live `GameState` until all validation and dice resolution in a turn are complete. A turn either commits fully or not at all.

#### Scenario: Partial failure does not corrupt state
- **WHEN** dice resolution raises an unexpected exception mid-turn
- **THEN** the original `GameState` is returned unchanged and an error is surfaced to the caller
