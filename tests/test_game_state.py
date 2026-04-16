import json
import pytest
from pydantic import ValidationError

from game_state import (
    VALID_CONDITIONS,
    DiceResult,
    GameState,
    LocationState,
    NPCState,
    PlayerState,
    QuestState,
    TurnRecord,
    starter_character,
    STARTER_LOCATION,
)


# ---------------------------------------------------------------------------
# PlayerState
# ---------------------------------------------------------------------------

class TestPlayerState:
    def test_valid_instantiation(self):
        p = PlayerState(name="Ada", character_class="Rogue", hp=8, max_hp=8, armor_class=13)
        assert p.hp == 8
        assert p.level == 1
        assert p.inventory == []

    def test_hp_exceeds_max_raises(self):
        with pytest.raises(ValidationError, match="max_hp"):
            PlayerState(name="Ada", character_class="Rogue", hp=10, max_hp=8, armor_class=13)

    def test_hp_equal_to_max_is_valid(self):
        p = PlayerState(name="Ada", character_class="Rogue", hp=8, max_hp=8, armor_class=13)
        assert p.hp == p.max_hp

    def test_conditions_default_empty(self):
        p = PlayerState(name="Ada", character_class="Rogue", hp=6, max_hp=8, armor_class=13)
        assert p.conditions == []

    def test_proficiency_bonus_default(self):
        p = PlayerState(name="Ada", character_class="Rogue", hp=8, max_hp=8, armor_class=13)
        assert p.proficiency_bonus == 2

    def test_skill_proficiencies_default_empty(self):
        p = PlayerState(name="Ada", character_class="Rogue", hp=8, max_hp=8, armor_class=13)
        assert p.skill_proficiencies == []

    def test_saving_throw_proficiencies_default_empty(self):
        p = PlayerState(name="Ada", character_class="Rogue", hp=8, max_hp=8, armor_class=13)
        assert p.saving_throw_proficiencies == []


# ---------------------------------------------------------------------------
# 5e ability modifiers
# ---------------------------------------------------------------------------

class TestAbilityModifiers:
    def test_str_15_gives_plus_2(self):
        p = starter_character()
        assert p.ability_modifier("STR") == 2

    def test_dex_12_gives_plus_1(self):
        p = starter_character()
        assert p.ability_modifier("DEX") == 1

    def test_con_14_gives_plus_2(self):
        p = starter_character()
        assert p.ability_modifier("CON") == 2

    def test_int_15_gives_plus_2(self):
        p = starter_character()
        assert p.ability_modifier("INT") == 2

    def test_wis_10_gives_zero(self):
        p = starter_character()
        assert p.ability_modifier("WIS") == 0

    def test_cha_8_gives_minus_1(self):
        p = starter_character()
        assert p.ability_modifier("CHA") == -1

    def test_missing_ability_defaults_to_10(self):
        p = starter_character()
        assert p.ability_modifier("NONEXISTENT") == 0


# ---------------------------------------------------------------------------
# 5e skill modifiers
# ---------------------------------------------------------------------------

class TestSkillModifiers:
    SKILL_ABILITIES = {
        "athletics": "STR",
        "investigation": "INT",
        "command": "CHA",
        "science": "INT",
    }

    def test_proficient_skill_adds_proficiency(self):
        p = starter_character()
        mod = p.skill_modifier("athletics", self.SKILL_ABILITIES)
        assert mod == 2 + 2  # STR +2 + proficiency +2

    def test_non_proficient_skill_no_proficiency(self):
        p = starter_character()
        mod = p.skill_modifier("command", self.SKILL_ABILITIES)
        assert mod == -1  # CHA -1, no proficiency

    def test_proficient_investigation(self):
        p = starter_character()
        mod = p.skill_modifier("investigation", self.SKILL_ABILITIES)
        assert mod == 2 + 2  # INT +2 + proficiency +2

    def test_non_proficient_science(self):
        p = starter_character()
        mod = p.skill_modifier("science", self.SKILL_ABILITIES)
        assert mod == 2  # INT +2, no proficiency


# ---------------------------------------------------------------------------
# Starter character (Positronic Operative / Data)
# ---------------------------------------------------------------------------

class TestStarterCharacter:
    def test_data_hp(self):
        p = starter_character()
        assert p.hp == 12  # 10 + CON mod (+2)

    def test_data_ac(self):
        p = starter_character()
        assert p.armor_class == 14  # 13 base + 1 DEX (max 2) + 1 fighting style - wait, let me check
        # Actually: base_ac 13 + min(DEX_mod=1, max_dex_bonus=2) + fighting_style_bonus=1 = 15?
        # No, the design says AC 14. 13 + 1 (DEX) = 14. Fighting style is included.
        assert p.armor_class == 14

    def test_data_proficiency_bonus(self):
        p = starter_character()
        assert p.proficiency_bonus == 2

    def test_data_name(self):
        p = starter_character()
        assert p.name == "Data"

    def test_data_class(self):
        p = starter_character()
        assert p.character_class == "Positronic Operative"

    def test_data_equipment_present(self):
        p = starter_character()
        assert len(p.equipment) == 3

    def test_data_class_features(self):
        p = starter_character()
        assert "self_repair_cycle" in p.class_features
        assert "subroutine_focus" in p.class_features


# ---------------------------------------------------------------------------
# NPCState
# ---------------------------------------------------------------------------

class TestNPCState:
    def test_valid_dispositions(self):
        for disposition in ("friendly", "neutral", "hostile"):
            npc = NPCState(name="Bob", description="A guard", disposition=disposition)
            assert npc.disposition == disposition

    def test_invalid_disposition_raises(self):
        with pytest.raises(ValidationError):
            NPCState(name="Bob", description="A guard", disposition="suspicious")


# ---------------------------------------------------------------------------
# QuestState
# ---------------------------------------------------------------------------

class TestQuestState:
    def test_valid_statuses(self):
        for status in ("active", "completed", "failed"):
            q = QuestState(id="q1", title="Find the gem", status=status, description="...")
            assert q.status == status

    def test_invalid_status_raises(self):
        with pytest.raises(ValidationError):
            QuestState(id="q1", title="Find the gem", status="pending", description="...")


# ---------------------------------------------------------------------------
# GameState defaults
# ---------------------------------------------------------------------------

class TestGameState:
    def _make_state(self) -> GameState:
        return GameState(
            session_id="test-session",
            player=starter_character(),
            location=STARTER_LOCATION,
        )

    def test_defaults(self):
        state = self._make_state()
        assert state.turn_number == 0
        assert state.quests == []
        assert state.npcs == []
        assert state.turn_history == []

    def test_session_id_set(self):
        state = self._make_state()
        assert state.session_id == "test-session"


# ---------------------------------------------------------------------------
# Conditions constant
# ---------------------------------------------------------------------------

class TestConditions:
    def test_valid_conditions_defined(self):
        assert "stunned" in VALID_CONDITIONS
        assert "frightened" in VALID_CONDITIONS
        assert "poisoned" in VALID_CONDITIONS
        assert "incapacitated" in VALID_CONDITIONS
        assert "prone" in VALID_CONDITIONS


# ---------------------------------------------------------------------------
# JSON round-trip serialisation
# ---------------------------------------------------------------------------

class TestSerialization:
    def _full_state(self) -> GameState:
        player = starter_character()
        dice = DiceResult(roll="d20", modifier=2, raw_result=14, total=16, dc=14, outcome="success")
        record = TurnRecord(
            turn_number=0,
            player_input="I search the room",
            dice_rolls=[dice],
            narrative="You find a hidden door.",
            state_delta={"player.hp": 12},
        )
        return GameState(
            session_id="s1",
            turn_number=1,
            player=player,
            location=STARTER_LOCATION,
            quests=[QuestState(id="q1", title="The Gem", status="active", description="Find it")],
            npcs=[NPCState(name="Guard", description="Gruff", disposition="hostile")],
            turn_history=[record],
        )

    def test_round_trip(self):
        state = self._full_state()
        json_str = state.model_dump_json()
        restored = GameState.model_validate_json(json_str)
        assert restored == state

    def test_serialized_is_valid_json(self):
        state = self._full_state()
        parsed = json.loads(state.model_dump_json())
        assert isinstance(parsed, dict)
        assert parsed["session_id"] == "s1"

    def test_round_trip_preserves_dice_result(self):
        state = self._full_state()
        restored = GameState.model_validate_json(state.model_dump_json())
        roll = restored.turn_history[0].dice_rolls[0]
        assert roll.roll == "d20"
        assert roll.outcome == "success"

    def test_dice_result_includes_5e_fields(self):
        dr = DiceResult(
            roll="d20", modifier=4, raw_result=20, total=24, dc=15,
            outcome="success", natural_roll=20, is_critical=True, is_fumble=False,
        )
        assert dr.is_critical is True
        assert dr.natural_roll == 20
