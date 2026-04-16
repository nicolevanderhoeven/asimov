"""Integration tests for the /scenario Flask routes.

The LLM is mocked; the scenario runner uses real dice with seeded RNG.
Tests verify the full HTTP → ScenarioLoader → SceneRunner → response cycle.
"""
from __future__ import annotations

import sys
from unittest.mock import MagicMock, patch

import pytest


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------

def _stub_llm_instance(narrative: str = "The relay hums in response.") -> MagicMock:
    msg = MagicMock()
    msg.content = narrative
    llm = MagicMock()
    llm.invoke.return_value = msg
    return llm


@pytest.fixture()
def client():
    """Flask test client with two-player init and LLM mocked; scenario routes live."""
    sys.modules.pop("play", None)

    mock_llm = _stub_llm_instance()

    with patch("two_player_dnd.create_game") as mock_create_game:
        mock_create_game.return_value = (
            MagicMock(), "Hero", "DM", "brave", "wise", "Quest"
        )
        import play as play_module

    play_module.app.config["TESTING"] = True
    play_module._sessions.clear()
    play_module._scenario_runners.clear()

    with patch.object(play_module, "_get_llm", return_value=mock_llm):
        with play_module.app.test_client() as c:
            yield c, play_module

    sys.modules.pop("play", None)


# ---------------------------------------------------------------------------
# /scenario/info
# ---------------------------------------------------------------------------

class TestScenarioInfo:
    def test_returns_metadata(self, client):
        c, _ = client
        resp = c.get("/scenario/info")
        assert resp.status_code == 200
        body = resp.get_json()
        assert "endpoints" in body
        assert "start" in body["endpoints"]
        assert "play" in body["endpoints"]


# ---------------------------------------------------------------------------
# /scenario/start
# ---------------------------------------------------------------------------

class TestScenarioStart:
    def test_start_returns_201(self, client):
        c, _ = client
        resp = c.post("/scenario/start", json={"scenario": "silent-relay"})
        assert resp.status_code == 201

    def test_start_returns_session_id(self, client):
        c, play_module = client
        resp = c.post("/scenario/start", json={"scenario": "silent-relay"})
        body = resp.get_json()
        assert "session_id" in body
        assert body["session_id"] in play_module._scenario_runners

    def test_start_returns_entry_scene(self, client):
        c, _ = client
        resp = c.post("/scenario/start", json={"scenario": "silent-relay"})
        body = resp.get_json()
        assert body["scene"]["id"] == "scene_1_approach"
        assert "entry_text" in body["scene"]
        assert "objectives" in body["scene"]

    def test_start_returns_prologue(self, client):
        c, _ = client
        resp = c.post("/scenario/start", json={"scenario": "silent-relay"})
        body = resp.get_json()
        assert "prologue" in body
        assert body["prologue"] is not None
        assert "Data" in body["prologue"]

    def test_start_returns_player_summary(self, client):
        c, _ = client
        resp = c.post("/scenario/start", json={"scenario": "silent-relay"})
        body = resp.get_json()
        assert "player" in body
        assert body["player"]["name"] == "Data"
        assert body["player"]["character_class"] == "Positronic Operative"
        assert body["player"]["level"] == 1

    def test_start_returns_initial_state(self, client):
        c, _ = client
        resp = c.post("/scenario/start", json={"scenario": "silent-relay"})
        state = resp.get_json()["state"]
        assert state["turn_number"] == 0
        assert state["player"]["hp"] == 12
        assert state["player"]["name"] == "Data"
        assert state["player"]["proficiency_bonus"] == 2
        assert state["scenario"]["current_scene"] == "scene_1_approach"
        assert state["scenario"]["alarm_state"] == "silent"

    def test_start_missing_scenario_field_returns_400(self, client):
        c, _ = client
        resp = c.post("/scenario/start", json={})
        assert resp.status_code == 400

    def test_start_unknown_scenario_returns_400(self, client):
        c, _ = client
        resp = c.post("/scenario/start", json={"scenario": "nonexistent-scenario-xyz"})
        assert resp.status_code == 400


# ---------------------------------------------------------------------------
# /scenario/play — basic turn processing
# ---------------------------------------------------------------------------

class TestScenarioPlay:
    def _start(self, c) -> str:
        resp = c.post("/scenario/start", json={"scenario": "silent-relay"})
        return resp.get_json()["session_id"]

    def test_play_returns_200(self, client):
        c, _ = client
        session_id = self._start(c)
        resp = c.post("/scenario/play", json={"session_id": session_id, "input": "I approach."})
        assert resp.status_code == 200

    def test_play_returns_narrative(self, client):
        c, _ = client
        session_id = self._start(c)
        resp = c.post("/scenario/play", json={"session_id": session_id, "input": "I approach."})
        body = resp.get_json()
        assert "narrative" in body
        assert isinstance(body["narrative"], str)
        assert len(body["narrative"]) > 0

    def test_play_returns_state(self, client):
        c, _ = client
        session_id = self._start(c)
        resp = c.post("/scenario/play", json={"session_id": session_id, "input": "I dock."})
        body = resp.get_json()
        assert "state" in body
        assert "scenario" in body["state"]

    def test_play_turn_number_increments(self, client):
        c, _ = client
        session_id = self._start(c)
        # Prompts don't increment turn_number; /roll resolves a mechanic and does
        c.post("/scenario/play", json={"session_id": session_id, "input": "/roll"})
        resp = c.post("/scenario/play", json={"session_id": session_id, "input": "/roll"})
        body = resp.get_json()
        assert body["state"]["turn_number"] == 2

    def test_play_flags_accumulate(self, client):
        c, _ = client
        session_id = self._start(c)
        c.post("/scenario/play", json={"session_id": session_id, "input": "/roll"})
        resp = c.post("/scenario/play", json={"session_id": session_id, "input": "/roll"})
        flags = resp.get_json()["state"]["scenario"]["flags"]
        assert len(flags) >= 1

    def test_play_invalid_session_returns_404(self, client):
        c, _ = client
        resp = c.post("/scenario/play", json={"session_id": "does-not-exist", "input": "hi"})
        assert resp.status_code == 404

    def test_play_empty_input_returns_400(self, client):
        c, _ = client
        session_id = self._start(c)
        resp = c.post("/scenario/play", json={"session_id": session_id, "input": ""})
        assert resp.status_code == 400

    def test_play_with_approach(self, client):
        c, play_module = client
        session_id = self._start(c)
        runner = play_module._scenario_runners[session_id]

        all_flags = {
            "hazard:haz_docking_shear": "passed",
            "check:scene_1_approach:engineering": "passed",
            "check:scene_1_approach:science": "passed",
            "hazard:haz_power_arc": "passed",
            "hazard:haz_signal_feedback": "passed",
            "check:scene_2_operations:science": "passed",
            "check:scene_2_operations:engineering": "passed",
            "check:scene_2_operations:medical": "passed",
        }
        new_scenario = runner.state.scenario.model_copy(  # type: ignore[union-attr]
            update={"current_scene": "scene_3_core", "flags": all_flags}
        )
        runner._state = runner.state.model_copy(
            update={
                "scenario": new_scenario,
                "player": runner.state.player.model_copy(
                    update={"attributes": {**runner.state.player.attributes, "CHA": 30}}
                ),
            }
        )
        runner._start_scene_span("scene_3_core")

        # Step 1: select approach — returns /roll prompt (non-combat approach)
        resp = c.post(
            "/scenario/play",
            json={"session_id": session_id, "input": "I open a channel.", "approach": "diplomacy"},
        )
        assert resp.status_code == 200
        assert "/roll" in resp.get_json()["narrative"]

        # Step 2: roll to resolve
        resp = c.post(
            "/scenario/play",
            json={"session_id": session_id, "input": "/roll"},
        )
        assert resp.status_code == 200
        body = resp.get_json()
        flags = body["state"]["scenario"]["flags"]
        assert "approach" in flags


# ---------------------------------------------------------------------------
# Full scenario run (fast path — all checks seeded to pass)
# ---------------------------------------------------------------------------

class TestScenarioFullRun:
    def test_four_scene_run_completes(self, client):
        """Walk through all 4 scenes and verify session is marked complete."""
        c, play_module = client

        resp = c.post("/scenario/start", json={"scenario": "silent-relay"})
        session_id = resp.get_json()["session_id"]
        runner = play_module._scenario_runners[session_id]

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
        runner._state = runner.state.model_copy(
            update={
                "scenario": runner.state.scenario.model_copy(  # type: ignore[union-attr]
                    update={"current_scene": "scene_3_core", "flags": all_flags}
                )
            }
        )
        runner._start_scene_span("scene_3_core")

        resp = c.post("/scenario/play", json={"session_id": session_id, "input": "The relay is quiet."})
        body = resp.get_json()

        assert body["complete"] is True
        assert body["outcome"] == "peaceful"
        assert session_id not in play_module._scenario_runners
