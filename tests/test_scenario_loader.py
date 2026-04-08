"""Unit tests for ScenarioLoader (task 2.6)."""
from __future__ import annotations

import json
import shutil
from pathlib import Path

import pytest

from scenario_runner import (
    ScenarioLoadError,
    ScenarioLoader,
    ScenarioValidationError,
)

FIXTURE_DIR = Path(__file__).parent / "fixtures" / "scenario"


def _make_minimal_scenario(tmp_path: Path, overrides: dict | None = None) -> Path:
    """Write a minimal valid silent-relay-style scenario to tmp_path."""
    files: dict[str, object] = {
        "scenario.json": {
            "scenario_id": "test-scenario-v1",
            "title": "Test Scenario",
            "version": "1.0",
            "genre": "science_fiction",
            "tone": [],
            "play_profile": {
                "player_count": 1,
                "recommended_level": 1,
                "combat_density": "low",
                "max_simultaneous_hostiles": 2,
            },
            "entry_scene": "scene_1",
            "scene_order": ["scene_1", "scene_2"],
        },
        "scenes.json": [
            {
                "id": "scene_1",
                "name": "Scene One",
                "entry_text": "The story begins.",
                "objectives": ["Survive"],
                "obstacles": ["haz_one"],
                "checks": [{"skill": "engineering", "dc": 10, "label": "Fix it"}],
                "next_scene": "scene_2",
            },
            {
                "id": "scene_2",
                "name": "Scene Two",
                "entry_text": "The end approaches.",
                "objectives": [],
                "end": True,
            },
        ],
        "adversaries.json": [
            {
                "id": "adv_drone",
                "name": "Drone",
                "hp": 9,
                "defense": 12,
                "attack_bonus": 3,
                "damage": "1d6",
            }
        ],
        "hazards.json": [
            {
                "id": "haz_one",
                "name": "Hazard One",
                "check": "engineering",
                "dc": 10,
                "fail_effect": "minor damage",
            }
        ],
        "clues.json": [{"id": "clue_1", "location": "scene_1", "text": "A clue."}],
        "locations.json": [
            {"id": "loc_1", "name": "Location One", "tags": [], "description": "A place."}
        ],
        "initial_state.json": {
            "player": {
                "hp": 12,
                "max_hp": 12,
                "defense": 13,
                "conditions": [],
                "skills": {"engineering": 1},
            },
            "scenario": {"current_scene": "scene_1", "flags": {}, "alarm_state": "silent"},
        },
        "rules_profile.json": {
            "core_die": "d20",
            "difficulty_classes": {"easy": 10, "moderate": 13, "hard": 16},
            "skills": ["engineering"],
        },
        "npcs.json": [],
    }

    if overrides:
        files.update(overrides)

    tmp_path.mkdir(parents=True, exist_ok=True)
    for filename, content in files.items():
        path = tmp_path / filename
        if content is None:
            path.write_text("")
        else:
            path.write_text(json.dumps(content))

    return tmp_path


class TestScenarioLoaderValidLoad:
    def test_loads_silent_relay(self):
        """Integration: loads the real scenario from disk."""
        loader = ScenarioLoader()
        data, state = loader.load("silent-relay")
        assert data.meta.title == "The Silent Relay"
        assert data.meta.scenario_id == "silent-relay-v1"
        assert data.meta.entry_scene in data.scenes

    def test_all_scenes_present(self):
        loader = ScenarioLoader()
        data, _ = loader.load("silent-relay")
        assert set(data.meta.scene_order) == set(data.scenes.keys())

    def test_player_state_from_initial_state(self):
        loader = ScenarioLoader()
        _, state = loader.load("silent-relay")
        assert state.player.hp == 12
        assert state.player.skills["command"] == 3

    def test_current_scene_is_entry_scene(self):
        loader = ScenarioLoader()
        data, state = loader.load("silent-relay")
        assert state.scenario is not None
        assert state.scenario.current_scene == data.meta.entry_scene

    def test_flags_empty_on_load(self):
        loader = ScenarioLoader()
        _, state = loader.load("silent-relay")
        assert state.scenario is not None
        assert state.scenario.flags == {}

    def test_alarm_state_silent(self):
        loader = ScenarioLoader()
        _, state = loader.load("silent-relay")
        assert state.scenario is not None
        assert state.scenario.alarm_state == "silent"


class TestScenarioLoaderMissingFiles:
    def test_missing_file_raises_load_error(self, tmp_path):
        scenario_dir = _make_minimal_scenario(tmp_path / "missing-file")
        (scenario_dir / "scenes.json").unlink()

        loader = ScenarioLoader(base_dir=tmp_path)
        with pytest.raises(ScenarioLoadError, match="scenes.json"):
            loader.load("missing-file")

    def test_multiple_missing_files_listed(self, tmp_path):
        scenario_dir = _make_minimal_scenario(tmp_path / "many-missing")
        (scenario_dir / "scenes.json").unlink()
        (scenario_dir / "adversaries.json").unlink()

        loader = ScenarioLoader(base_dir=tmp_path)
        with pytest.raises(ScenarioLoadError) as exc_info:
            loader.load("many-missing")
        msg = str(exc_info.value)
        assert "scenes.json" in msg
        assert "adversaries.json" in msg

    def test_empty_npcs_file_is_valid(self, tmp_path):
        """npcs.json is allowed to be empty."""
        scenario_dir = _make_minimal_scenario(tmp_path / "empty-npcs", {"npcs.json": None})

        loader = ScenarioLoader(base_dir=tmp_path)
        data, state = loader.load("empty-npcs")
        assert data is not None


class TestScenarioLoaderCrossValidation:
    def test_valid_scene_adversary_reference_passes(self):
        loader = ScenarioLoader()
        data, _ = loader.load("silent-relay")
        # scene_3_core references adv_security_drone which exists
        assert "adv_security_drone" in data.adversaries

    def test_broken_adversary_reference_raises(self, tmp_path):
        scenes = [
            {
                "id": "scene_1",
                "name": "Scene",
                "entry_text": "",
                "approaches": [
                    {"id": "force", "combat": True, "adversaries": ["adv_nonexistent"]}
                ],
                "next_scene": "scene_2",
            },
            {"id": "scene_2", "name": "End", "entry_text": "", "end": True},
        ]
        scenario_dir = _make_minimal_scenario(
            tmp_path / "broken-adv", {"scenes.json": scenes}
        )
        loader = ScenarioLoader(base_dir=tmp_path)
        with pytest.raises(ScenarioValidationError, match="adv_nonexistent"):
            loader.load("broken-adv")

    def test_broken_hazard_reference_raises(self, tmp_path):
        scenes = [
            {
                "id": "scene_1",
                "name": "Scene",
                "entry_text": "",
                "obstacles": ["haz_nonexistent"],
                "end": True,
            }
        ]
        scenario_dir = _make_minimal_scenario(
            tmp_path / "broken-haz",
            {
                "scenes.json": scenes,
                "scenario.json": {
                    "scenario_id": "t",
                    "title": "T",
                    "version": "1.0",
                    "entry_scene": "scene_1",
                    "scene_order": ["scene_1"],
                    "play_profile": {
                        "player_count": 1,
                        "recommended_level": 1,
                        "combat_density": "low",
                        "max_simultaneous_hostiles": 2,
                    },
                },
            },
        )
        loader = ScenarioLoader(base_dir=tmp_path)
        with pytest.raises(ScenarioValidationError, match="haz_nonexistent"):
            loader.load("broken-haz")


class TestScenarioLoaderGraphValidation:
    def test_orphaned_next_scene_raises(self, tmp_path):
        scenes = [
            {
                "id": "scene_1",
                "name": "Scene",
                "entry_text": "",
                "next_scene": "scene_nonexistent",
            }
        ]
        scenario_dir = _make_minimal_scenario(
            tmp_path / "orphan",
            {
                "scenes.json": scenes,
                "scenario.json": {
                    "scenario_id": "t",
                    "title": "T",
                    "version": "1.0",
                    "entry_scene": "scene_1",
                    "scene_order": ["scene_1"],
                    "play_profile": {
                        "player_count": 1,
                        "recommended_level": 1,
                        "combat_density": "low",
                        "max_simultaneous_hostiles": 2,
                    },
                },
            },
        )
        loader = ScenarioLoader(base_dir=tmp_path)
        with pytest.raises(ScenarioValidationError, match="scene_nonexistent"):
            loader.load("orphan")

    def test_missing_entry_scene_raises(self, tmp_path):
        scenario_dir = _make_minimal_scenario(
            tmp_path / "bad-entry",
            {
                "scenario.json": {
                    "scenario_id": "t",
                    "title": "T",
                    "version": "1.0",
                    "entry_scene": "scene_nonexistent",
                    "scene_order": ["scene_1"],
                    "play_profile": {
                        "player_count": 1,
                        "recommended_level": 1,
                        "combat_density": "low",
                        "max_simultaneous_hostiles": 2,
                    },
                }
            },
        )
        loader = ScenarioLoader(base_dir=tmp_path)
        with pytest.raises(ScenarioValidationError, match="entry_scene"):
            loader.load("bad-entry")

    def test_complete_graph_passes(self):
        loader = ScenarioLoader()
        data, _ = loader.load("silent-relay")
        # All non-terminal scenes have valid next_scene (graph validated on load)
        for scene in data.scenes.values():
            if not scene.end:
                assert scene.next_scene in data.scenes
