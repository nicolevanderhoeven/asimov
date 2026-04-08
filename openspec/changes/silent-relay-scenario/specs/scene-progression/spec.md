## ADDED Requirements

### Requirement: Scene entry emits entry text and available approaches
The system SHALL, upon entering a scene, surface the scene's `entry_text` to the storyteller prompt and include the scene's `objectives` and (for scenes with `approaches`) the available approach IDs.

#### Scenario: Entry text delivered on scene start
- **WHEN** the engine transitions to `scene_2_operations`
- **THEN** the storyteller receives the `entry_text` for that scene in its system context

#### Scenario: Approach options listed for multi-path scene
- **WHEN** the engine enters `scene_3_core`
- **THEN** the player is presented with three approach IDs: `diplomacy`, `science`, `force`

---

### Requirement: Skill checks are resolved deterministically via the dice-mechanics engine
The system SHALL resolve checks defined on a scene by calling the existing dice-mechanics engine with the specified `skill` and `dc`. The result (pass/fail) SHALL be recorded in the `TurnRecord` and used to determine narration context.

#### Scenario: Passed check advances scene normally
- **WHEN** a player attempts the `engineering` check (DC 13) in `scene_1_approach` and rolls ≥ 13 (after modifier)
- **THEN** the check resolves as a pass and no fail effect is applied

#### Scenario: Failed check applies hazard effect
- **WHEN** a player fails the `engineering` check for `haz_power_arc` in `scene_2_operations`
- **THEN** the player receives `1d4` damage and the fail effect is recorded in the turn state delta

---

### Requirement: Approach resolution determines scene_3_core outcome
The system SHALL resolve `scene_3_core` based on the player's declared approach:
- `diplomacy` / `science`: resolve as a skill check against DC 13; on pass, set outcome flag `peaceful` or `contained`; on fail, escalate to `force`
- `force`: initiate combat with `adv_security_drone` (max 1 adversary, per `max_simultaneous_hostiles`)

#### Scenario: Diplomacy succeeds
- **WHEN** the player selects `diplomacy` and passes the `command` or `insight` check (DC 13)
- **THEN** `state.scenario.flags["core_outcome"]` is set to `"peaceful"` and combat is not initiated

#### Scenario: Force approach triggers combat
- **WHEN** the player selects `force`
- **THEN** combat is initiated with exactly 1 instance of `adv_security_drone`

#### Scenario: Diplomacy fails, escalates to force
- **WHEN** the player selects `diplomacy` and fails the check
- **THEN** the system escalates to `force` approach and initiates combat

---

### Requirement: Scene transitions are driven by next_scene declarations
The system SHALL advance to the scene identified by the current scene's `next_scene` field once all objectives for the current scene are resolved (checks completed, approach outcome set, or combat concluded). Terminal scenes (`"end": true`) SHALL end the session and trigger outcome classification.

#### Scenario: Scene advances after objectives met
- **WHEN** all checks in `scene_2_operations` are resolved
- **THEN** `state.scenario.current_scene` is updated to `scene_3_core`

#### Scenario: Terminal scene ends the session
- **WHEN** the engine enters `scene_4_resolution` (marked `"end": true`)
- **THEN** the session is finalised, no further turns are accepted, and an outcome summary is generated

---

### Requirement: Hazards are resolved independently of scene checks
The system SHALL resolve hazards listed in a scene's `obstacles` field by presenting them as encounters during scene exploration. Each hazard uses its `check` skill and `dc`; on failure, `fail_effect` is applied to player state.

#### Scenario: Hazard resolved with successful check
- **WHEN** a player passes the check for `haz_signal_feedback` (science DC 13)
- **THEN** no `confusion` condition is applied

#### Scenario: Hazard fail effect applies condition
- **WHEN** a player fails the check for `haz_signal_feedback`
- **THEN** `"confusion"` is added to `state.player.conditions`
