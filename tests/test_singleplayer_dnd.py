import json
from unittest.mock import MagicMock

import pytest
from pydantic import ValidationError

from game_state import GameState, STARTER_LOCATION, starter_character
from singleplayer_dnd import (
    LLMTurnResponse,
    build_storyteller_prompt,
    invoke_storyteller,
)


def make_state() -> GameState:
    return GameState(session_id="s1", player=starter_character(), location=STARTER_LOCATION)


def mock_llm_with_responses(*responses: str) -> MagicMock:
    """Build a mock LLM whose .invoke() cycles through the given response strings."""
    llm = MagicMock()
    side_effects = []
    for text in responses:
        msg = MagicMock()
        msg.content = text
        side_effects.append(msg)
    llm.invoke.side_effect = side_effects
    return llm


VALID_RESPONSE = json.dumps({
    "narrative": "You cautiously step into the torchlit hallway.",
    "state_delta": {"location.name": "Hallway"},
    "dice_triggers": [{"roll": "d20", "skill": "Perception", "dc": 13, "modifier": 1}],
})


class TestBuildStorytellerPrompt:
    def test_returns_two_strings(self):
        state = make_state()
        system, human = build_storyteller_prompt(state, "I look around")
        assert isinstance(system, str)
        assert isinstance(human, str)

    def test_state_json_in_system(self):
        state = make_state()
        system, _ = build_storyteller_prompt(state, "I look around")
        assert state.session_id in system

    def test_player_input_in_human(self):
        state = make_state()
        _, human = build_storyteller_prompt(state, "I search the chest")
        assert "I search the chest" in human


class TestInvokeStoryteller:
    def _call(self, llm) -> tuple:
        state = make_state()
        system, human = build_storyteller_prompt(state, "I go north")
        return invoke_storyteller(system, human, llm)

    def test_valid_response_parsed(self):
        llm = mock_llm_with_responses(VALID_RESPONSE)
        result, retried = self._call(llm)
        assert isinstance(result, LLMTurnResponse)
        assert "hallway" in result.narrative.lower()
        assert retried is False

    def test_valid_response_no_retry(self):
        llm = mock_llm_with_responses(VALID_RESPONSE)
        _, retried = self._call(llm)
        assert retried is False
        assert llm.invoke.call_count == 1

    def test_first_fail_second_valid_retries(self):
        llm = mock_llm_with_responses("not json at all", VALID_RESPONSE)
        result, retried = self._call(llm)
        assert retried is True
        assert llm.invoke.call_count == 2
        assert isinstance(result, LLMTurnResponse)
        assert result.narrative == "You cautiously step into the torchlit hallway."

    def test_double_fail_fallback_narrative(self):
        llm = mock_llm_with_responses("bad output", "also bad")
        result, retried = self._call(llm)
        assert retried is True
        assert llm.invoke.call_count == 2
        assert result.narrative == "also bad"
        assert result.state_delta == {}
        assert result.dice_triggers == []

    def test_markdown_fence_stripped(self):
        fenced = "```json\n" + VALID_RESPONSE + "\n```"
        llm = mock_llm_with_responses(fenced)
        result, _ = self._call(llm)
        assert isinstance(result, LLMTurnResponse)
        assert result.narrative != ""
