"""
Integration tests for the /singleplayer Flask routes.

The LLM and two-player game initialisation are mocked so these tests run
without any API keys or network access.
"""
import json
from unittest.mock import MagicMock, patch


import pytest


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------

VALID_LLM_NARRATIVE = "You hear the distant sound of dripping water."
VALID_LLM_RESPONSE = json.dumps({
    "narrative": VALID_LLM_NARRATIVE,
    "state_delta": {},
    "dice_triggers": [],
})


def _make_mock_llm():
    msg = MagicMock()
    msg.content = VALID_LLM_RESPONSE
    llm = MagicMock()
    llm.invoke.return_value = msg
    return llm


@pytest.fixture()
def client():
    """Flask test client with two-player initialisation and LLM mocked out."""
    import sys

    mock_llm_instance = _make_mock_llm()

    # Drop any cached play module so we get a fresh import with the mock
    sys.modules.pop("play", None)

    with patch("two_player_dnd.create_game") as mock_create_game:
        mock_create_game.return_value = (
            MagicMock(),        # simulator
            "Hero",             # protagonist_name
            "DM",               # storyteller_name
            "brave hero",       # protagonist_description
            "wise DM",          # storyteller_description
            "Defeat the dragon",  # detailed_quest
        )
        import play as play_module  # fresh import — create_game() uses the mock

    # After the context, restore the real function (already done by patch.__exit__)
    play_module.app.config["TESTING"] = True
    play_module._sessions.clear()

    with patch.object(play_module, "_get_llm", return_value=mock_llm_instance):
        with play_module.app.test_client() as c:
            yield c, play_module

    # Clean up so other test suites don't see this patched module
    sys.modules.pop("play", None)


# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------

class TestSingleplayerInfo:
    def test_get_returns_metadata(self, client):
        c, _ = client
        resp = c.get("/singleplayer")
        assert resp.status_code == 200
        body = resp.get_json()
        assert "name" in body
        assert "endpoints" in body


class TestSessionLifecycle:
    def test_start_creates_session(self, client):
        c, play_module = client
        resp = c.post("/singleplayer/start")
        assert resp.status_code == 201
        body = resp.get_json()
        assert "session_id" in body
        assert body["session_id"] in play_module._sessions

    def test_start_returns_initial_state(self, client):
        c, _ = client
        resp = c.post("/singleplayer/start")
        body = resp.get_json()
        state = body["state"]
        assert state["turn_number"] == 0
        assert state["player"]["hp"] > 0

    def test_play_two_turns(self, client):
        c, play_module = client
        session_id = c.post("/singleplayer/start").get_json()["session_id"]

        for i in range(2):
            resp = c.post(
                "/singleplayer/play",
                json={"session_id": session_id, "input": f"Turn {i + 1} action"},
            )
            assert resp.status_code == 200
            body = resp.get_json()
            assert body["narrative"] == VALID_LLM_NARRATIVE

        state = play_module._sessions[session_id]
        assert state.turn_number == 2

    def test_end_removes_session(self, client):
        c, play_module = client
        session_id = c.post("/singleplayer/start").get_json()["session_id"]
        resp = c.post("/singleplayer/end", json={"session_id": session_id})
        assert resp.status_code == 200
        assert resp.get_json()["ended"] is True
        assert session_id not in play_module._sessions

    def test_end_returns_total_turns(self, client):
        c, play_module = client
        session_id = c.post("/singleplayer/start").get_json()["session_id"]
        c.post("/singleplayer/play", json={"session_id": session_id, "input": "Go north"})
        resp = c.post("/singleplayer/end", json={"session_id": session_id})
        assert resp.get_json()["total_turns"] == 1


class TestErrorCases:
    def test_play_invalid_session_returns_404(self, client):
        c, _ = client
        resp = c.post("/singleplayer/play", json={"session_id": "does-not-exist", "input": "hi"})
        assert resp.status_code == 404

    def test_play_empty_input_returns_400(self, client):
        c, _ = client
        session_id = c.post("/singleplayer/start").get_json()["session_id"]
        resp = c.post("/singleplayer/play", json={"session_id": session_id, "input": ""})
        assert resp.status_code == 400

    def test_play_oversized_input_returns_400(self, client):
        c, _ = client
        session_id = c.post("/singleplayer/start").get_json()["session_id"]
        resp = c.post(
            "/singleplayer/play",
            json={"session_id": session_id, "input": "x" * 501},
        )
        assert resp.status_code == 400

    def test_end_invalid_session_returns_404(self, client):
        c, _ = client
        resp = c.post("/singleplayer/end", json={"session_id": "ghost-session"})
        assert resp.status_code == 404
