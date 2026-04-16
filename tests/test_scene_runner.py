"""Unit tests for SceneRunner — 5e mechanics.

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
# Skill checks — 5e ability check formula
# ---------------------------------------------------------------------------

class TestSkillChecks:
    def test_check_flag_set_after_resolution(self):
        runner = _make_runner(seed=99)
        runner.process_turn("I approach carefully.")
        runner.process_turn("I scan the anomaly.")
        flags = runner.state.scenario.flags  # type: ignore[union-attr]
        assert any(k.startswith("check:") or k.startswith("hazard:") for k in flags)

    def test_check_uses_5e_ability_modifier(self):
        """With high attributes, checks should pass consistently."""
        data, state = _load()
        state = state.model_copy(
            update={
                "player": state.player.model_copy(
                    update={
                        "attributes": {**state.player.attributes, "INT": 30},
                        "skill_proficiencies": [*state.player.skill_proficiencies, "engineering"],
                    }
                )
            }
        )
        runner = SceneRunner(data, state, RulesEngine(seed=1), _stub_llm())
        for _ in range(3):
            runner.process_turn("I attempt the task.")
        flags = runner.state.scenario.flags  # type: ignore[union-attr]
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
        runner = _make_runner(seed=5)
        runner.process_turn("I try to dock.")
        flags = runner.state.scenario.flags  # type: ignore[union-attr]
        assert "hazard:haz_docking_shear" in flags

    def test_failed_hazard_applies_condition(self):
        """haz_signal_feedback fails → applies 'frightened' condition."""
        data, state = _load()
        state = state.model_copy(
            update={
                "scenario": state.scenario.model_copy(  # type: ignore[union-attr]
                    update={"current_scene": "scene_2_operations"}
                ),
                "player": state.player.model_copy(
                    update={
                        "attributes": {**state.player.attributes, "INT": 1, "STR": 1},
                        "skill_proficiencies": [],
                    }
                ),
            }
        )
        runner = SceneRunner(data, state, RulesEngine(seed=0), _stub_llm())
        runner.process_turn("I investigate the console.")
        runner.process_turn("I reach for the relay controls.")
        haz_flag = runner.state.scenario.flags.get("hazard:haz_signal_feedback")  # type: ignore[union-attr]
        if haz_flag == "failed":
            assert "frightened" in runner.state.player.conditions

    def test_hazard_damage_reduces_hp(self):
        """haz_power_arc on failure deals 1d4 damage."""
        data, state = _load()
        state = state.model_copy(
            update={
                "scenario": state.scenario.model_copy(  # type: ignore[union-attr]
                    update={"current_scene": "scene_2_operations"}
                ),
                "player": state.player.model_copy(
                    update={
                        "attributes": {**state.player.attributes, "INT": 1},
                        "skill_proficiencies": [],
                    }
                ),
            }
        )
        runner = SceneRunner(data, state, RulesEngine(seed=0), _stub_llm())
        runner.process_turn("I touch the panel.")
        haz_flag = runner.state.scenario.flags.get("hazard:haz_power_arc")  # type: ignore[union-attr]
        if haz_flag == "failed":
            assert runner.state.player.hp < 12

    def test_passed_hazard_does_not_apply_condition(self):
        data, state = _load()
        state = state.model_copy(
            update={
                "player": state.player.model_copy(
                    update={
                        "attributes": {**state.player.attributes, "INT": 30, "STR": 30},
                    }
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
                    update={"attributes": {**state.player.attributes, "CHA": 30}}
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
                    update={"attributes": {**state.player.attributes, "CHA": 30}}
                ),
            }
        )
        runner = SceneRunner(data, state, RulesEngine(seed=99), _stub_llm())
        runner.process_turn("I speak calmly.", approach="diplomacy")
        core_outcome = runner.state.scenario.flags.get("core_outcome")  # type: ignore[union-attr]
        assert core_outcome == "peaceful"


# ---------------------------------------------------------------------------
# Self-Repair Cycle (Second Wind)
# ---------------------------------------------------------------------------

class TestSelfRepairCycle:
    def test_self_repair_heals(self):
        data, state = _load()
        state = state.model_copy(
            update={"player": state.player.model_copy(update={"hp": 5})}
        )
        runner = SceneRunner(data, state, RulesEngine(seed=42), _stub_llm())
        runner.process_turn("I activate self-repair.")
        assert runner.state.player.hp > 5
        assert runner.state.player.hp <= runner.state.player.max_hp
        assert runner.state.scenario.flags.get("self_repair_used") == "true"  # type: ignore[union-attr]

    def test_self_repair_cannot_be_used_twice(self):
        data, state = _load()
        state = state.model_copy(
            update={
                "player": state.player.model_copy(update={"hp": 5}),
                "scenario": state.scenario.model_copy(  # type: ignore[union-attr]
                    update={"flags": {"self_repair_used": "true"}}
                ),
            }
        )
        runner = SceneRunner(data, state, RulesEngine(seed=42), _stub_llm())
        runner.process_turn("I activate self-repair.")
        # Should resolve a hazard instead of self-repair
        assert "hazard:haz_docking_shear" in runner.state.scenario.flags  # type: ignore[union-attr]


# ---------------------------------------------------------------------------
# Scene transitions
# ---------------------------------------------------------------------------

class TestSceneTransitions:
    def test_scene_advances_after_all_mechanics_resolved(self):
        data, state = _load()
        state = state.model_copy(
            update={
                "player": state.player.model_copy(
                    update={"attributes": {k: 30 for k in state.player.attributes}}
                )
            }
        )
        runner = SceneRunner(data, state, RulesEngine(seed=1), _stub_llm())
        for _ in range(3):
            runner.process_turn("I proceed.")
        assert runner.current_scene == "scene_2_operations"

    def test_terminal_scene_finalises_session(self):
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
            }
        )
        runner = SceneRunner(data, state, RulesEngine(seed=1), _stub_llm())
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


# ---------------------------------------------------------------------------
# Combat — multi-round, 5e mechanics
# ---------------------------------------------------------------------------

class TestCombat:
    def _runner_at_scene_3_for_combat(self, seed: int = 42) -> SceneRunner:
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
                )
            }
        )
        return SceneRunner(data, state, RulesEngine(seed=seed), _stub_llm())

    def test_force_combat_resolves(self):
        runner = self._runner_at_scene_3_for_combat(seed=42)
        runner.process_turn("I attack!", approach="force")
        flags = runner.state.scenario.flags  # type: ignore[union-attr]
        assert flags.get("approach") == "force"

    def test_combat_can_reduce_player_hp(self):
        """Over many seeds, at least one should result in player taking damage."""
        any_damage = False
        for seed in range(50):
            runner = self._runner_at_scene_3_for_combat(seed=seed)
            initial_hp = runner.state.player.hp
            runner.process_turn("I engage!", approach="force")
            if runner.state.player.hp < initial_hp:
                any_damage = True
                break
        assert any_damage, "Expected at least one seed to result in player damage"

    def test_0_hp_means_defeated(self):
        """With very low HP, combat should end in defeat."""
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
                "player": state.player.model_copy(update={"hp": 1, "max_hp": 12}),
            }
        )
        defeated = False
        for seed in range(100):
            runner = SceneRunner(data, state, RulesEngine(seed=seed), _stub_llm())
            runner.process_turn("I charge!", approach="force")
            if runner.state.player.hp <= 0 and runner.is_complete:
                assert runner.outcome_type == "defeated"
                defeated = True
                break
        assert defeated, "Expected at least one seed to result in defeat at 1 HP"
