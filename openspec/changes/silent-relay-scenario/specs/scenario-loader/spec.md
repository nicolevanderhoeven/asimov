## ADDED Requirements

### Requirement: Scenario directory is discovered by name
The system SHALL locate a scenario by reading `scenarios/<name>/` relative to the project root. The directory SHALL contain: `scenario.json`, `scenes.json`, `adversaries.json`, `hazards.json`, `clues.json`, `locations.json`, `initial_state.json`, `rules_profile.json`.

#### Scenario: Valid scenario loads without error
- **WHEN** `ScenarioLoader("silent-relay").load()` is called and all required files are present
- **THEN** the loader returns a fully populated `ScenarioData` object with no validation errors

#### Scenario: Missing required file raises an error
- **WHEN** a required file (e.g. `scenes.json`) is absent from the scenario directory
- **THEN** the loader raises a `ScenarioLoadError` identifying the missing file before any game state is initialised

---

### Requirement: All entity IDs are cross-validated on load
The system SHALL verify that every ID referenced in `scenes.json` (adversaries, obstacles/hazards, clues) exists in its corresponding data file. Broken references SHALL raise a `ScenarioValidationError` listing all offending IDs.

#### Scenario: Scene references a valid adversary
- **WHEN** `scene_3_core` references `adv_security_drone` and that ID exists in `adversaries.json`
- **THEN** validation passes with no error

#### Scenario: Scene references a missing hazard
- **WHEN** a scene references `haz_unknown` and that ID does not exist in `hazards.json`
- **THEN** `ScenarioValidationError` is raised naming `haz_unknown` as the broken reference

---

### Requirement: Scenario graph is validated for completeness
The system SHALL verify that every non-terminal scene declares a `next_scene` that exists in the scene list. Scenes with `"end": true` are exempt. The entry scene declared in `scenario.json` SHALL exist in `scenes.json`.

#### Scenario: Complete graph passes validation
- **WHEN** all non-terminal scenes have a `next_scene` that resolves to a known scene ID
- **THEN** validation passes

#### Scenario: Orphaned next_scene raises an error
- **WHEN** a scene's `next_scene` points to a scene ID not present in `scenes.json`
- **THEN** `ScenarioValidationError` is raised

---

### Requirement: Initial game state is sourced from initial_state.json
The system SHALL initialise the session `GameState` from the values in `initial_state.json`, setting `player` fields and scenario-scoped fields (`current_scene`, `flags`, `alarm_state`) accordingly. The `current_scene` SHALL be set to `scenario.entry_scene`.

#### Scenario: Player state initialised from file
- **WHEN** a scenario session starts
- **THEN** `state.player.hp` equals the value of `player.hp` in `initial_state.json`

#### Scenario: current_scene set to entry scene
- **WHEN** a scenario session starts
- **THEN** `state.scenario.current_scene` equals `scenario.entry_scene` from `scenario.json`
