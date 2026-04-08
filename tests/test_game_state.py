import json
import pytest
from pydantic import ValidationError

from game_state import (
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
