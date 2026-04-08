## 1. Data Model — ScenarioState extension

- [x] 1.1 Add `ScenarioState` Pydantic model (`current_scene: str`, `flags: dict[str, str]`, `alarm_state: str = "silent"`) — touches `two_player_dnd.py` or new `models.py`
- [x] 1.2 Add `scenario: ScenarioState | None = None` field to `GameState` — touches `two_player_dnd.py` / `models.py`
- [x] 1.3 Write unit tests for `ScenarioState` validation (defaults, serialisation round-trip) — touches `tests/test_models.py`

## 2. Scenario Loader

- [x] 2.1 Create `scenario_runner.py`; implement `ScenarioLoader` class with `load(name: str) -> ScenarioData` — new file
- [x] 2.2 Implement file-presence validation: raise `ScenarioLoadError` listing missing files — touches `scenario_runner.py`
- [x] 2.3 Implement cross-ID validation (adversary, hazard, clue references in scenes) — touches `scenario_runner.py`
- [x] 2.4 Implement scene-graph completeness validation (`next_scene` references, entry scene exists) — touches `scenario_runner.py`
- [x] 2.5 Implement `initial_state.json` loading and `GameState` initialisation from it — touches `scenario_runner.py`
- [x] 2.6 Write unit tests covering: valid load, missing file, broken reference, orphaned `next_scene` — touches `tests/test_scenario_loader.py`

## 3. Scene Runner

- [x] 3.1 Implement `SceneRunner` class with `enter_scene(scene_id)` and `current_scene` property — touches `scenario_runner.py`
- [x] 3.2 Implement skill check resolution: call existing dice-mechanics engine with `skill` + `dc`, record result in `TurnRecord` — touches `scenario_runner.py`
- [x] 3.3 Implement hazard resolution pipeline: iterate `obstacles`, resolve checks, apply `fail_effect` to player state — touches `scenario_runner.py`
- [x] 3.4 Implement approach resolution for `scene_3_core`: `diplomacy`/`science` → check → flag, `force` → combat with ≤2 adversaries — touches `scenario_runner.py`
- [x] 3.5 Implement scene transition logic: update `state.scenario.current_scene` to `next_scene` once objectives resolved — touches `scenario_runner.py`
- [x] 3.6 Implement terminal scene handling: finalise session, classify `outcome.type`, reject further turns — touches `scenario_runner.py`
- [x] 3.7 Write unit tests: scene entry, check pass/fail, hazard fail effect, approach escalation, terminal scene — touches `tests/test_scene_runner.py`

## 4. Flask integration

- [x] 4.1 Add `POST /scenario/start` endpoint accepting `{"scenario": "<name>"}`, calls `ScenarioLoader`, returns initial scene context — touches `play.py`
- [x] 4.2 Update `POST /play` to route through `SceneRunner.process_turn()` when `state.scenario` is not `None` — touches `play.py`
- [x] 4.3 Skip protagonist agent instantiation when scenario mode is active — touches `two_player_dnd.py`
- [x] 4.4 Pass resolved outcome and current scene state into storyteller system prompt — touches `two_player_dnd.py`
- [x] 4.5 Write integration test: start scenario via `/scenario/start`, play through all 4 scenes via `POST /play` — touches `tests/test_scenario_integration.py`

## 5. OTel instrumentation

- [x] 5.1 Emit `scenario` root span with `scenario.id` on session init; close on terminal scene — touches `scenario_runner.py`
- [x] 5.2 Emit `scene` child span with `scene.id` and `scene.name` on each `enter_scene`; close on transition — touches `scenario_runner.py`
- [x] 5.3 Emit `skill_check` child span with `check.skill`, `check.dc`, `check.roll`, `check.modifier`, `check.total`, `check.passed` — touches `scenario_runner.py`
- [x] 5.4 Emit `approach` child span with `approach.id` and `approach.outcome` on approach resolution — touches `scenario_runner.py`
- [x] 5.5 Emit `hazard` child span with `hazard.id`, `hazard.check`, `hazard.dc`, `hazard.passed`, and `hazard.effect` (on fail) — touches `scenario_runner.py`
- [x] 5.6 Set `outcome.type` attribute on root `scenario` span at session end — touches `scenario_runner.py`
- [ ] 5.7 Verify spans appear in Grafana Tempo trace view for a complete scenario run (manual smoke test) — run the app locally and use the Grafana Cloud dashboard
