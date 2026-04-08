import json
from unittest.mock import MagicMock, patch

import pytest

from game_state import GameState, STARTER_LOCATION, starter_character
from rules_engine import RulesEngine
from turn_loop import TurnLoop, compute_delta


def make_state(hp: int = 12) -> GameState:
    player = starter_character()
    player = player.model_copy(update={"hp": hp})
    return GameState(session_id="test-session", player=player, location=STARTER_LOCATION)


def mock_llm(narrative: str = "The torch flickers.", state_delta: dict = None, dice_triggers: list = None):
    response_payload = {
        "narrative": narrative,
        "state_delta": state_delta or {},
        "dice_triggers": dice_triggers or [],
    }
    msg = MagicMock()
    msg.content = json.dumps(response_payload)
    llm = MagicMock()
    llm.invoke.return_value = msg
    return llm


class TestValidateInput:
    def test_empty_raises(self):
        loop = TurnLoop(make_state(), RulesEngine(), mock_llm())
        with pytest.raises(ValueError, match="empty"):
            loop.validate_input("")

    def test_whitespace_only_raises(self):
        loop = TurnLoop(make_state(), RulesEngine(), mock_llm())
        with pytest.raises(ValueError, match="empty"):
            loop.validate_input("   ")

    def test_oversized_raises(self):
        loop = TurnLoop(make_state(), RulesEngine(), mock_llm())
        with pytest.raises(ValueError, match="too long"):
            loop.validate_input("x" * 501)

    def test_exactly_500_is_valid(self):
        loop = TurnLoop(make_state(), RulesEngine(), mock_llm())
        loop.validate_input("x" * 500)  # should not raise


class TestRunHappyPath:
    def test_returns_narrative_and_state(self):
        state = make_state()
        loop = TurnLoop(state, RulesEngine(), mock_llm("You step forward."))
        narrative, new_state = loop.run("I go north")
        assert narrative == "You step forward."
        assert isinstance(new_state, GameState)

    def test_turn_number_incremented(self):
        state = make_state()
        loop = TurnLoop(state, RulesEngine(), mock_llm())
        _, new_state = loop.run("Look around")
        assert new_state.turn_number == 1

    def test_turn_record_appended(self):
        state = make_state()
        loop = TurnLoop(state, RulesEngine(), mock_llm())
        _, new_state = loop.run("I search the room")
        assert len(new_state.turn_history) == 1
        assert new_state.turn_history[0].player_input == "I search the room"

    def test_dice_triggers_resolved_and_recorded(self):
        dice_triggers = [{"roll": "d6", "modifier": 0}]
        state = make_state()
        loop = TurnLoop(state, RulesEngine(seed=1), mock_llm(dice_triggers=dice_triggers))
        _, new_state = loop.run("I attack")
        record = new_state.turn_history[0]
        assert len(record.dice_rolls) == 1
        assert record.dice_rolls[0].outcome == "hit"

    def test_state_delta_applied(self):
        state = make_state()
        loop = TurnLoop(
            state,
            RulesEngine(),
            mock_llm(state_delta={"location.name": "Dark Corridor"}),
        )
        _, new_state = loop.run("I walk through the door")
        assert new_state.location.name == "Dark Corridor"

    def test_internal_state_updated(self):
        state = make_state()
        loop = TurnLoop(state, RulesEngine(), mock_llm())
        loop.run("First turn")
        assert loop.state.turn_number == 1


class TestRunFailurePath:
    def test_original_state_unchanged_on_exception(self):
        state = make_state()
        original_turn = state.turn_number

        broken_llm = MagicMock()
        broken_llm.invoke.side_effect = RuntimeError("LLM blew up")

        loop = TurnLoop(state, RulesEngine(), broken_llm)
        with pytest.raises(RuntimeError):
            loop.run("I try something")

        assert loop.state.turn_number == original_turn

    def test_empty_input_does_not_call_llm(self):
        llm = mock_llm()
        loop = TurnLoop(make_state(), RulesEngine(), llm)
        with pytest.raises(ValueError):
            loop.run("")
        llm.invoke.assert_not_called()


class TestComputeDelta:
    def test_changed_field_detected(self):
        before = make_state(hp=10)
        after = before.model_copy(deep=True)
        after = after.model_copy(update={"player": after.player.model_copy(update={"hp": 6})})
        delta = compute_delta(before, after)
        assert "player.hp" in delta
        assert delta["player.hp"] == 6

    def test_unchanged_fields_excluded(self):
        state = make_state()
        delta = compute_delta(state, state.model_copy(deep=True))
        assert delta == {}

    def test_multiple_changes_all_detected(self):
        before = make_state()
        after = before.model_copy(
            update={
                "player": before.player.model_copy(update={"hp": 5}),
                "turn_number": 3,
            }
        )
        delta = compute_delta(before, after)
        assert "player.hp" in delta
        assert "turn_number" in delta
