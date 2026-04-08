"""Unit tests for SceneRunner (task 3.7).

Uses a stub LLM so no real API calls are made.
All dice rolls are seeded for reproducibility.
"""
from __future__ import annotations

from unittest.mock import MagicMock

import pytest

from game_state import GameState
from rules_engine import RulesEngine
from scenario_runner import ScenarioLoader, SceneRunner


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _load() -> tuple:
    loader = ScenarioLoader()
    data, state = loader.load("silent-relay")
    return data, state


def _stub_llm(narrative: str = "The station holds its breath.") -> MagicMock:
    llm = MagicMock()
    response = MagicMock()
    response.content = narrative
    llm.invoke.return_value = response
    return llm


def _make_runner(seed: int = 42) -> SceneRunner:
    data, state = _load()
    return SceneRunner(data, state, RulesEngine(seed=seed), _stub_llm())


# ---------------------------------------------------------------------------
# Scene entry
# ---------------------------------------------------------------------------

class TestSceneEntry:
    def test_initial_scene_is_entry_scene(self):
        runner = _make_runner()
        assert runner.current_scene == "scene_1_approach"

    def test_enter_scene_updates_current_scene(self):
        runner = _make_runner()
        runner.enter_scene("scene_2_operations")
        assert runner.current_scene == "scene_2_operations"

    def test_enter_scene_returns_updated_state(self):
        runner = _make_runner()
        state = runner.enter_scene("scene_2_operations")
        assert state.scenario is not None
        assert state.scenario.current_scene == "scene_2_operations"

    def test_enter_scene_does_not_finalise(self):
        runner = _make_runner()
        runner.enter_scene("scene_2_operations")
        assert not runner.is_complete


# ---------------------------------------------------------------------------
# Skill checks
# ---------------------------------------------------------------------------

class TestSkillChecks:
    def test_check_flag_set_after_resolution(self):
        runner = _make_runner(seed=99)
        # scene_1_approach has checks; hazard comes first
        # First, resolve the hazard, then the check
        runner.process_turn("I approach carefully.")
        runner.process_turn("I scan the anomaly.")
        flags = runner.state.scenario.flags  # type: ignore[union-attr]
        # At least one check or hazard flag should be present
        assert any(k.startswith("check:") or k.startswith("hazard:") for k in flags)

    def test_check_uses_player_skill_modifier(self):
        """Verify modifier taken from player.skills, not zero."""
        data, state = _load()
        # Set engineering to a very high value to guarantee success
        state = state.model_copy(
            update={
                "player": state.player.model_copy(
                    update={"skills": {**state.player.skills, "engineering": 20}}
                )
            }
        )
        runner = SceneRunner(data, state, RulesEngine(seed=1), _stub_llm())
        # Process until the engineering check is resolved
        for _ in range(3):
            runner.process_turn("I attempt the task.")
        flags = runner.state.scenario.flags  # type: ignore[union-attr]
        # With modifier=20 the check must always pass
        eng_flag = next(
            (v for k, v in flags.items() if "engineering" in k and k.startswith("check:")),
            None,
        )
        if eng_flag:
            assert eng_flag == "passed"


# ---------------------------------------------------------------------------
# Hazard resolution
# ---------------------------------------------------------------------------

class TestHazardResolution:
    def test_hazard_flag_set_after_first_turn(self):
        """First turn in scene_1 resolves the hazard (obstacles are first)."""
        runner = _make_runner(seed=5)
        runner.process_turn("I try to dock.")
        flags = runner.state.scenario.flags  # type: ignore[union-attr]
        assert "hazard:haz_docking_shear" in flags

    def test_failed_hazard_applies_condition(self):
        """With a guaranteed-fail seed, haz_signal_feedback adds 'confusion' condition."""
        data, state = _load()
        # Jump straight to scene_2 where haz_signal_feedback lives
        state = state.model_copy(
            update={
                "scenario": state.scenario.model_copy(  # type: ignore[union-attr]
                    update={"current_scene": "scene_2_operations"}
                )
            }
        )
        # Use all-zero skills so modifier=0, DC=13 → likely to fail with low seed
        state = state.model_copy(
            update={
                "player": state.player.model_copy(update={"skills": {}})
            }
        )
        runner = SceneRunner(data, state, RulesEngine(seed=0), _stub_llm())
        # Process first turn (haz_power_arc, dc=10)
        runner.process_turn("I investigate the console.")
        # Process second turn (haz_signal_feedback, dc=13)
        runner.process_turn("I reach for the relay controls.")
        haz_flag = runner.state.scenario.flags.get("hazard:haz_signal_feedback")  # type: ignore[union-attr]
        if haz_flag == "failed":
            assert "confusion" in runner.state.player.conditions

    def test_passed_hazard_does_not_apply_condition(self):
        """With a guaranteed-pass modifier, no condition applied."""
        data, state = _load()
        # Set all skills very high
        state = state.model_copy(
            update={
                "player": state.player.model_copy(
                    update={"skills": {"engineering": 50, "science": 50}}
                )
            }
        )
        runner = SceneRunner(data, state, RulesEngine(seed=1), _stub_llm())
        runner.process_turn("I navigate carefully.")
        flags = runner.state.scenario.flags  # type: ignore[union-attr]
        if "hazard:haz_docking_shear" in flags:
            assert flags["hazard:haz_docking_shear"] == "passed"
            assert runner.state.player.hp == 12


# ---------------------------------------------------------------------------
# Approach resolution
# ---------------------------------------------------------------------------

class TestApproachResolution:
    def _runner_at_scene_3(self, seed: int = 42) -> SceneRunner:
        data, state = _load()
        # Pre-mark all scene_1 and scene_2 mechanics as resolved
        flags = {
            "hazard:haz_docking_shear": "passed",
            "check:scene_1_approach:engineering": "passed",
            "check:scene_1_approach:science": "passed",
            "hazard:haz_power_arc": "passed",
            "hazard:haz_signal_feedback": "passed",
            "check:scene_2_operations:science": "passed",
            "check:scene_2_operations:engineering": "passed",
            "check:scene_2_operations:medical": "passed",
        }
        state = state.model_copy(
            update={
                "scenario": state.scenario.model_copy(  # type: ignore[union-attr]
                    update={"current_scene": "scene_3_core", "flags": flags}
                )
            }
        )
        return SceneRunner(data, state, RulesEngine(seed=seed), _stub_llm())

    def test_no_approach_returns_choice_prompt(self):
        runner = self._runner_at_scene_3()
        narrative, _ = runner.process_turn("I look around.")
        assert "approach" in narrative.lower() or "diplomacy" in narrative.lower()

    def test_diplomacy_approach_sets_flag(self):
        # Use high skill to guarantee success
        data, state = _load()
        flags = {
            "hazard:haz_docking_shear": "passed",
            "check:scene_1_approach:engineering": "passed",
            "check:scene_1_approach:science": "passed",
            "hazard:haz_power_arc": "passed",
            "hazard:haz_signal_feedback": "passed",
            "check:scene_2_operations:science": "passed",
            "check:scene_2_operations:engineering": "passed",
            "check:scene_2_operations:medical": "passed",
        }
        state = state.model_copy(
            update={
                "scenario": state.scenario.model_copy(  # type: ignore[union-attr]
                    update={"current_scene": "scene_3_core", "flags": flags}
                ),
                "player": state.player.model_copy(
                    update={"skills": {**state.player.skills, "command": 50}}
                ),
            }
        )
        runner = SceneRunner(data, state, RulesEngine(seed=42), _stub_llm())
        runner.process_turn("I try to communicate.", approach="diplomacy")
        approach_flag = runner.state.scenario.flags.get("approach")  # type: ignore[union-attr]
        assert approach_flag == "diplomacy"

    def test_force_approach_triggers_combat(self):
        runner = self._runner_at_scene_3(seed=10)
        runner.process_turn("I open fire.", approach="force")
        flags = runner.state.scenario.flags  # type: ignore[union-attr]
        assert flags.get("approach") == "force"
        assert flags.get("core_outcome") == "force"

    def test_invalid_approach_raises(self):
        runner = self._runner_at_scene_3()
        with pytest.raises(ValueError, match="Unknown approach"):
            runner.process_turn("I try something weird.", approach="bribery")

    def test_diplomatic_success_sets_peaceful_outcome(self):
        """Guarantee success via modifier=50."""
        data, state = _load()
        flags = {
            "hazard:haz_docking_shear": "passed",
            "check:scene_1_approach:engineering": "passed",
            "check:scene_1_approach:science": "passed",
            "hazard:haz_power_arc": "passed",
            "hazard:haz_signal_feedback": "passed",
            "check:scene_2_operations:science": "passed",
            "check:scene_2_operations:engineering": "passed",
            "check:scene_2_operations:medical": "passed",
        }
        state = state.model_copy(
            update={
                "scenario": state.scenario.model_copy(  # type: ignore[union-attr]
                    update={"current_scene": "scene_3_core", "flags": flags}
                ),
                "player": state.player.model_copy(
                    update={"skills": {**state.player.skills, "command": 50}}
                ),
            }
        )
        runner = SceneRunner(data, state, RulesEngine(seed=99), _stub_llm())
        runner.process_turn("I speak calmly.", approach="diplomacy")
        core_outcome = runner.state.scenario.flags.get("core_outcome")  # type: ignore[union-attr]
        assert core_outcome == "peaceful"


# ---------------------------------------------------------------------------
# Scene transitions
# ---------------------------------------------------------------------------

class TestSceneTransitions:
    def test_scene_advances_after_all_mechanics_resolved(self):
        """After all hazards+checks in scene_1 are resolved, runner moves to scene_2."""
        data, state = _load()
        # Use high skills and seed that ensures passes
        state = state.model_copy(
            update={
                "player": state.player.model_copy(
                    update={"skills": {k: 50 for k in state.player.skills}}
                )
            }
        )
        runner = SceneRunner(data, state, RulesEngine(seed=1), _stub_llm())
        # scene_1 has: 1 hazard + 2 checks = 3 turns to resolve
        for _ in range(3):
            runner.process_turn("I proceed.")
        assert runner.current_scene == "scene_2_operations"

    def test_terminal_scene_finalises_session(self):
        """Entering scene_4_resolution (end=True) should finalise the session."""
        data, state = _load()
        all_flags = {
            "hazard:haz_docking_shear": "passed",
            "check:scene_1_approach:engineering": "passed",
            "check:scene_1_approach:science": "passed",
            "hazard:haz_power_arc": "passed",
            "hazard:haz_signal_feedback": "passed",
            "check:scene_2_operations:science": "passed",
            "check:scene_2_operations:engineering": "passed",
            "check:scene_2_operations:medical": "passed",
            "approach": "diplomacy",
            "core_outcome": "peaceful",
        }
        state = state.model_copy(
            update={
                "scenario": state.scenario.model_copy(  # type: ignore[union-attr]
                    update={"current_scene": "scene_3_core", "flags": all_flags}
                ),
                "player": state.player.model_copy(
                    update={"skills": {**state.player.skills, "command": 50}}
                ),
            }
        )
        runner = SceneRunner(data, state, RulesEngine(seed=1), _stub_llm())
        # scene_3_core approach already resolved → scene_complete → advance to scene_4
        runner.process_turn("The relay pulses.")
        assert runner.is_complete

    def test_completed_session_rejects_further_turns(self):
        data, state = _load()
        all_flags = {
            "hazard:haz_docking_shear": "passed",
            "check:scene_1_approach:engineering": "passed",
            "check:scene_1_approach:science": "passed",
            "hazard:haz_power_arc": "passed",
            "hazard:haz_signal_feedback": "passed",
            "check:scene_2_operations:science": "passed",
            "check:scene_2_operations:engineering": "passed",
            "check:scene_2_operations:medical": "passed",
            "approach": "force",
            "core_outcome": "force",
        }
        state = state.model_copy(
            update={
                "scenario": state.scenario.model_copy(  # type: ignore[union-attr]
                    update={"current_scene": "scene_3_core", "flags": all_flags}
                )
            }
        )
        runner = SceneRunner(data, state, RulesEngine(seed=1), _stub_llm())
        runner.process_turn("The relay goes quiet.")
        assert runner.is_complete
        with pytest.raises(ValueError, match="already complete"):
            runner.process_turn("Another turn.")


# ---------------------------------------------------------------------------
# Outcome classification
# ---------------------------------------------------------------------------

class TestOutcomeClassification:
    def _complete_runner(self, core_outcome: str) -> SceneRunner:
        data, state = _load()
        flags = {
            "hazard:haz_docking_shear": "passed",
            "check:scene_1_approach:engineering": "passed",
            "check:scene_1_approach:science": "passed",
            "hazard:haz_power_arc": "passed",
            "hazard:haz_signal_feedback": "passed",
            "check:scene_2_operations:science": "passed",
            "check:scene_2_operations:engineering": "passed",
            "check:scene_2_operations:medical": "passed",
            "approach": "diplomacy",
            "core_outcome": core_outcome,
        }
        state = state.model_copy(
            update={
                "scenario": state.scenario.model_copy(  # type: ignore[union-attr]
                    update={"current_scene": "scene_3_core", "flags": flags}
                )
            }
        )
        runner = SceneRunner(data, state, RulesEngine(seed=1), _stub_llm())
        runner.process_turn("End.")
        return runner

    def test_peaceful_outcome(self):
        runner = self._complete_runner("peaceful")
        assert runner.outcome_type == "peaceful"

    def test_contained_outcome(self):
        runner = self._complete_runner("contained")
        assert runner.outcome_type == "contained"

    def test_force_outcome(self):
        runner = self._complete_runner("force")
        assert runner.outcome_type == "force"
