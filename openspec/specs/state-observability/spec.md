## ADDED Requirements

### Requirement: Each turn emits an OTel span
The system SHALL wrap each `TurnLoop` execution in a dedicated OpenTelemetry span named `dnd.turn`. The span SHALL be a child of the active request span (if one exists) and SHALL be closed after the turn commits or errors.

Span attributes SHALL include:
- `dnd.session_id` (str)
- `dnd.turn_number` (int)
- `dnd.player_input_length` (int) — character count of player input
- `dnd.llm_retried` (bool) — whether the LLM was retried
- `dnd.dice_roll_count` (int) — number of dice rolls in the turn
- `dnd.state_delta_keys` (str) — comma-separated list of state fields that changed

#### Scenario: Span created per turn
- **WHEN** a turn completes successfully
- **THEN** exactly one `dnd.turn` span is exported with `dnd.turn_number` equal to the completed turn's number

#### Scenario: Span attributes populated on normal turn
- **WHEN** a turn involves 2 dice rolls and changes `player.hp`
- **THEN** the span has `dnd.dice_roll_count = 2` and `dnd.state_delta_keys = "player.hp"`

#### Scenario: Span status set to ERROR on turn failure
- **WHEN** a turn fails due to both LLM attempts returning malformed output
- **THEN** the `dnd.turn` span status is set to `ERROR` with a descriptive message

---

### Requirement: Each turn emits a structured OTel log event
The system SHALL emit a structured log event via `loggingfw.CustomLogFW` at the end of each turn. The log body SHALL be a JSON-serialized `TurnEventLog` containing:
- `event`: `"turn_complete"` or `"turn_error"`
- `session_id` (str)
- `turn_number` (int)
- `player_input` (str)
- `narrative` (str) — omitted on error
- `dice_rolls` (list of `DiceResult` dicts)
- `state_before` (dict) — full `GameState` snapshot before the turn
- `state_after` (dict) — full `GameState` snapshot after the turn (omitted on error)
- `state_delta` (dict) — only the changed fields

#### Scenario: Log event emitted on successful turn
- **WHEN** a turn completes
- **THEN** a log record with `event = "turn_complete"` is emitted containing non-empty `narrative` and `state_after`

#### Scenario: Log event emitted on turn error
- **WHEN** a turn fails
- **THEN** a log record with `event = "turn_error"` is emitted; `state_after` is absent and `state_before` reflects the unchanged state

#### Scenario: State snapshots are valid JSON
- **WHEN** a turn log event is emitted
- **THEN** both `state_before` and `state_after` are valid JSON objects deserializable by `GameState.model_validate()`

---

### Requirement: Session lifecycle events are logged
The system SHALL emit log events for session start (`"session_start"`) and session end (`"session_end"`), each containing `session_id`, `timestamp`, and (for end) `total_turns` and final `GameState` snapshot.

#### Scenario: Session start event emitted
- **WHEN** a new single-player session is initialized
- **THEN** a `"session_start"` log event is emitted with the `session_id` and initial `GameState`

#### Scenario: Session end event emitted
- **WHEN** the session is explicitly ended (player quits or HP reaches 0)
- **THEN** a `"session_end"` log event is emitted with `total_turns` and the final `GameState`

---

### Requirement: Turn traces are compatible with existing OTel Collector config
The system SHALL emit spans and logs using the existing `loggingfw.CustomLogFW` and OpenLIT-initialized tracer, routed through the OTel Collector defined in `otel-config.template.yml`, without requiring changes to the collector config.

#### Scenario: Spans visible in Grafana Tempo
- **WHEN** the application runs with the OTel Collector active
- **THEN** `dnd.turn` spans appear in Grafana Tempo under the same service as existing two-player traces

#### Scenario: Log events visible in Grafana Loki
- **WHEN** the application emits a `"turn_complete"` log event
- **THEN** the structured log record appears in Grafana Loki queryable by `session_id`
