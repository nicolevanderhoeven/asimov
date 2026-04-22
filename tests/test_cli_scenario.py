"""Unit tests for cli_scenario's slash-command formatters.

Pure-string renderers driven off a synthetic ``PlayerState`` / skill map — no
LLM, no scenario loader, no I/O.
"""
from __future__ import annotations

from types import SimpleNamespace

import pytest

from cli_scenario import (
    _format_char_sheet,
    _format_help,
    _format_inventory,
    _format_status,
    _handle_slash_command,
)
from game_state import GameState, LocationState, PlayerState, ScenarioState


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------

SKILL_ABILITIES = {
    "athletics": "STR",
    "engineering": "INT",
    "medical": "WIS",
    "command": "CHA",
    "stealth": "DEX",
}


def _player() -> PlayerState:
    return PlayerState(
        name="Data",
        character_class="Positronic Operative",
        hp=8,
        max_hp=12,
        armor_class=14,
        level=1,
        proficiency_bonus=2,
        attributes={"STR": 15, "DEX": 12, "CON": 14, "INT": 15, "WIS": 10, "CHA": 8},
        skill_proficiencies=["athletics", "engineering"],
        saving_throw_proficiencies=["STR", "CON"],
        inventory=["phaser", "tricorder"],
        equipment=[
            {"name": "Starfleet Tactical Uniform", "type": "armor", "base_ac": 13, "max_dex_bonus": 2},
            {"name": "Phaser", "type": "ranged_weapon", "damage": "1d8", "ability": "DEX"},
            {"name": "Positronic Strike", "type": "melee_weapon", "damage": "1d8", "ability": "STR"},
        ],
        class_features={
            "self_repair": {"name": "Self-Repair Cycle", "description": "Heal 1d10 + level HP."},
        },
        conditions=[],
    )


def _state(scene_id: str = "scene_2_operations", turn: int = 4) -> GameState:
    return GameState(
        session_id="test",
        turn_number=turn,
        player=_player(),
        location=LocationState(name="Ops", description="..."),
        scenario=ScenarioState(current_scene=scene_id, flags={}),
    )


# ---------------------------------------------------------------------------
# /help
# ---------------------------------------------------------------------------

class TestHelp:
    def test_lists_all_commands(self):
        out = _format_help()
        for cmd in ["/help", "/char", "/inv", "/status", "/roll", "approach", "quit"]:
            assert cmd in out


# ---------------------------------------------------------------------------
# /char
# ---------------------------------------------------------------------------

class TestCharSheet:
    def test_header_includes_identity_and_hp(self):
        out = _format_char_sheet(_state(), SKILL_ABILITIES)
        assert "Data — Positronic Operative" in out
        assert "Level 1" in out
        assert "HP 8/12" in out
        assert "AC 14" in out
        assert "Prof +2" in out

    def test_abilities_block_shows_all_six(self):
        out = _format_char_sheet(_state(), SKILL_ABILITIES)
        # STR 15 (+2), CHA 8 (-1) — spot check both signs.
        assert "STR 15 (+2)" in out
        assert "CHA  8 (-1)" in out
        assert "INT 15 (+2)" in out

    def test_saves_mark_proficiencies(self):
        out = _format_char_sheet(_state(), SKILL_ABILITIES)
        # Data is prof in STR and CON saves → +4 with star; DEX save unprof → +1 no star.
        assert "STR +4 ★" in out
        assert "CON +4 ★" in out
        assert "DEX +1" in out

    def test_skills_list_all_from_mapping_and_star_proficient(self):
        out = _format_char_sheet(_state(), SKILL_ABILITIES)
        # Every skill in the mapping should appear.
        for skill in SKILL_ABILITIES:
            assert skill in out
        # Athletics and engineering are proficient → stars.
        assert "★" in out
        # Proficient engineering: INT +2 + prof +2 = +4.
        assert "engineering" in out
        lines = [ln for ln in out.splitlines() if "engineering" in ln]
        assert any("+4" in ln and "★" in ln and "[INT]" in ln for ln in lines)
        # Unproficient medical: WIS +0.
        med_lines = [ln for ln in out.splitlines() if "medical" in ln]
        assert any("+0" in ln and "[WIS]" in ln for ln in med_lines)
        assert all("★" not in ln for ln in med_lines)

    def test_features_rendered(self):
        out = _format_char_sheet(_state(), SKILL_ABILITIES)
        assert "Self-Repair Cycle" in out
        assert "Heal 1d10 + level HP." in out

    def test_empty_skill_mapping_degrades_gracefully(self):
        out = _format_char_sheet(_state(), {})
        assert "no scenario skill mapping" in out


# ---------------------------------------------------------------------------
# /inv
# ---------------------------------------------------------------------------

class TestInventory:
    def test_armor_shows_ac_and_dex_cap(self):
        out = _format_inventory(_state())
        assert "Starfleet Tactical Uniform" in out
        assert "armor (AC 13, +DEX max 2)" in out

    def test_weapons_show_damage_and_ability(self):
        out = _format_inventory(_state())
        assert "Phaser" in out and "ranged (1d8, DEX)" in out
        assert "Positronic Strike" in out and "melee (1d8, STR)" in out

    def test_carried_items_listed(self):
        out = _format_inventory(_state())
        assert "phaser, tricorder" in out


# ---------------------------------------------------------------------------
# /status
# ---------------------------------------------------------------------------

def _fake_scene(name: str, objectives: list[str]):
    return SimpleNamespace(name=name, objectives=objectives)


def _fake_data(scene_name: str = "The Silent Station"):
    return SimpleNamespace(
        scenes={"scene_2_operations": _fake_scene(scene_name, ["Recover logs", "Restore power"])}
    )


def _fake_open_check(kind="check", skill="science", ability="INT", dc=13):
    return SimpleNamespace(kind=kind, skill=skill, ability=ability, dc=dc, label=None)


class TestStatus:
    def test_shows_scene_name_and_id(self):
        out = _format_status(_state(), _fake_data(), None)
        assert "The Silent Station" in out
        assert "scene_2_operations" in out
        assert "Turn 4" in out

    def test_shows_hp_and_conditions(self):
        out = _format_status(_state(), _fake_data(), None)
        assert "HP 8/12" in out
        assert "AC 14" in out
        assert "Conditions: none" in out

    def test_shows_objectives(self):
        out = _format_status(_state(), _fake_data(), None)
        assert "Recover logs" in out
        assert "Restore power" in out

    def test_shows_open_check_with_full_ability_name(self):
        out = _format_status(_state(), _fake_data(), _fake_open_check())
        assert "Intelligence (Science)" in out
        assert "DC 13" in out
        assert "/roll" in out

    def test_no_open_check_message(self):
        out = _format_status(_state(), _fake_data(), None)
        assert "Open check: none" in out


# ---------------------------------------------------------------------------
# Dispatcher
# ---------------------------------------------------------------------------

class _FakeRunner:
    skill_abilities = SKILL_ABILITIES
    open_check = None


class TestSlashDispatcher:
    def test_help_recognised(self, capsys):
        assert _handle_slash_command("/help", _state(), _fake_data(), _FakeRunner()) is True
        assert "/help" in capsys.readouterr().out

    def test_char_recognised(self, capsys):
        assert _handle_slash_command("/char", _state(), _fake_data(), _FakeRunner()) is True
        assert "CHARACTER SHEET" in capsys.readouterr().out

    def test_inv_recognised(self, capsys):
        assert _handle_slash_command("/inv", _state(), _fake_data(), _FakeRunner()) is True
        assert "INVENTORY" in capsys.readouterr().out

    def test_status_recognised(self, capsys):
        assert _handle_slash_command("/status", _state(), _fake_data(), _FakeRunner()) is True
        assert "STATUS" in capsys.readouterr().out

    def test_roll_not_hijacked(self):
        # /roll must fall through to the runner, so the dispatcher returns False.
        assert _handle_slash_command("/roll", _state(), _fake_data(), _FakeRunner()) is False

    def test_unknown_slash_command_returns_false(self):
        assert _handle_slash_command("/foobar", _state(), _fake_data(), _FakeRunner()) is False

    def test_case_insensitive(self, capsys):
        assert _handle_slash_command("/HELP", _state(), _fake_data(), _FakeRunner()) is True
        assert "/help" in capsys.readouterr().out
