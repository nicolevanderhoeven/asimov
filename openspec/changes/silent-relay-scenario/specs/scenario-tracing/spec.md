## ADDED Requirements

### Requirement: Scenario lifecycle is bounded by a root span
The system SHALL create a root OTel span named `scenario` when a session starts, and end it when the session terminates (terminal scene reached or session aborted). The span SHALL carry the attribute `scenario.id` set to the `scenario_id` value from `scenario.json`.

#### Scenario: Span created on session start
- **WHEN** `ScenarioLoader("silent-relay").load()` completes and the session is initialised
- **THEN** a span named `scenario` is active with `scenario.id = "silent-relay-v1"`

#### Scenario: Span ends on session termination
- **WHEN** the engine enters a terminal scene
- **THEN** the `scenario` span is ended and its `outcome.type` attribute is set to one of `"peaceful"`, `"contained"`, or `"force"`

---

### Requirement: Each scene entry creates a child span
The system SHALL create a child span named `scene` under the active `scenario` span upon entering each scene. The span SHALL carry `scene.id` (the scene's `id` field) and `scene.name`. The span SHALL end when the scene is exited or the session terminates.

#### Scenario: Scene span created on transition
- **WHEN** the engine transitions to `scene_2_operations`
- **THEN** a child span named `scene` is created with `scene.id = "scene_2_operations"` and `scene.name = "The Silent Station"`

#### Scenario: Scene span ends on next transition
- **WHEN** the engine transitions from `scene_2_operations` to `scene_3_core`
- **THEN** the `scene_2_operations` span is ended before the `scene_3_core` span is created

---

### Requirement: Skill checks emit a child span with pass/fail outcome
The system SHALL create a child span named `skill_check` under the active `scene` span for each check resolved. The span SHALL carry `check.skill`, `check.dc`, `check.roll` (raw d20 result), `check.modifier`, `check.total`, and `check.passed` (boolean).

#### Scenario: Check span captures roll and outcome
- **WHEN** the player resolves the `engineering` check (DC 13) in `scene_1_approach` with a roll of 15
- **THEN** a `skill_check` span is emitted with `check.skill = "engineering"`, `check.dc = 13`, `check.roll = 15`, `check.passed = true`

---

### Requirement: Approach resolution emits a child span
The system SHALL create a child span named `approach` under the active `scene` span when the player selects and resolves an approach in `scene_3_core`. The span SHALL carry `approach.id` and `approach.outcome` (one of `"peaceful"`, `"contained"`, `"force"`, `"combat"`).

#### Scenario: Approach span emitted after resolution
- **WHEN** the player resolves the `diplomacy` approach successfully
- **THEN** an `approach` span is emitted with `approach.id = "diplomacy"` and `approach.outcome = "peaceful"`

---

### Requirement: Hazard resolution emits a child span
The system SHALL create a child span named `hazard` under the active `scene` span for each hazard resolved. The span SHALL carry `hazard.id`, `hazard.check`, `hazard.dc`, `hazard.passed`, and (on failure) `hazard.effect`.

#### Scenario: Failed hazard span records effect
- **WHEN** the player fails the `haz_signal_feedback` check
- **THEN** a `hazard` span is emitted with `hazard.id = "haz_signal_feedback"`, `hazard.passed = false`, and `hazard.effect = "confusion"`
