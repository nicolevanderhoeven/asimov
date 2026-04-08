"""Unit tests for ScenarioState model additions (task 1.3)."""
from __future__ import annotations

import json

import pytest
from pydantic import ValidationError

from game_state import (
    GameState,
    PlayerState,
    ScenarioState,
    STARTER_LOCATION,
    starter_character,
)


class TestScenarioState:
    def test_defaults(self):
        s = ScenarioState(current_scene="scene_1_approach")
        assert s.flags == {}
        assert s.alarm_state == "silent"

    def test_current_scene_required(self):
        with pytest.raises(ValidationError):
            ScenarioState()  # type: ignore[call-arg]

    def test_flags_populated(self):
        s = ScenarioState(
            current_scene="scene_3_core",
            flags={"core_outcome": "peaceful"},
        )
        assert s.flags["core_outcome"] == "peaceful"

    def test_alarm_state_custom(self):
        s = ScenarioState(current_scene="scene_2_operations", alarm_state="alert")
        assert s.alarm_state == "alert"

    def test_round_trip_serialisation(self):
        s = ScenarioState(
            current_scene="scene_2_operations",
            flags={"hazard:haz_power_arc": "passed"},
            alarm_state="silent",
        )
        restored = ScenarioState.model_validate_json(s.model_dump_json())
        assert restored == s

    def test_serialised_is_valid_json(self):
        s = ScenarioState(current_scene="scene_1_approach")
        parsed = json.loads(s.model_dump_json())
        assert parsed["current_scene"] == "scene_1_approach"


class TestGameStateScenarioField:
    def test_scenario_defaults_to_none(self):
        state = GameState(
            session_id="s1",
            player=starter_character(),
            location=STARTER_LOCATION,
        )
        assert state.scenario is None

    def test_scenario_set(self):
        scenario = ScenarioState(current_scene="scene_1_approach")
        state = GameState(
            session_id="s1",
            player=starter_character(),
            location=STARTER_LOCATION,
            scenario=scenario,
        )
        assert state.scenario is not None
        assert state.scenario.current_scene == "scene_1_approach"

    def test_game_state_round_trip_with_scenario(self):
        scenario = ScenarioState(
            current_scene="scene_3_core",
            flags={"core_outcome": "contained"},
        )
        state = GameState(
            session_id="s2",
            player=starter_character(),
            location=STARTER_LOCATION,
            scenario=scenario,
        )
        restored = GameState.model_validate_json(state.model_dump_json())
        assert restored.scenario is not None
        assert restored.scenario.flags["core_outcome"] == "contained"


class TestPlayerStateSkills:
    def test_skills_default_empty(self):
        p = PlayerState(name="Ada", character_class="Officer", hp=12, max_hp=12, armor_class=13)
        assert p.skills == {}

    def test_skills_populated(self):
        p = PlayerState(
            name="Ada",
            character_class="Officer",
            hp=12,
            max_hp=12,
            armor_class=13,
            skills={"command": 3, "science": 2},
        )
        assert p.skills["command"] == 3

    def test_skills_round_trip(self):
        p = PlayerState(
            name="Ada",
            character_class="Officer",
            hp=12,
            max_hp=12,
            armor_class=13,
            skills={"engineering": 1},
        )
        restored = PlayerState.model_validate_json(p.model_dump_json())
        assert restored.skills["engineering"] == 1
