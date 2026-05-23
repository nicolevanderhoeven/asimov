"""
Tests for structured log events (loggingfw) and OTel span attributes (turn_loop).

OTel span verification uses the in-memory SDK exporters so no collector is needed.
"""
import json
import logging
from unittest.mock import MagicMock, patch

import pytest
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export.in_memory_span_exporter import InMemorySpanExporter
from opentelemetry.sdk.trace.export import SimpleSpanProcessor

from game_state import GameState, STARTER_LOCATION, starter_character
from loggingfw import _build_log_exporter, log_turn_event, log_session_event
from rules_engine import RulesEngine
from turn_loop import TurnLoop


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def make_state(hp: int = 12) -> GameState:
    player = starter_character()
    player = player.model_copy(update={"hp": hp})
    return GameState(session_id="obs-session", player=player, location=STARTER_LOCATION)


def mock_llm(narrative: str = "Silence.", state_delta: dict = None, dice_triggers: list = None):
    payload = {
        "narrative": narrative,
        "state_delta": state_delta or {},
        "dice_triggers": dice_triggers or [],
    }
    msg = MagicMock()
    msg.content = json.dumps(payload)
    llm = MagicMock()
    llm.stream.side_effect = lambda *_args, **_kwargs: iter([msg])
    return llm


def make_tracer_with_exporter() -> tuple:
    """Return (tracer_provider, span_exporter) backed by in-memory storage."""
    exporter = InMemorySpanExporter()
    provider = TracerProvider()
    provider.add_span_processor(SimpleSpanProcessor(exporter))
    return provider, exporter


# ---------------------------------------------------------------------------
# log_turn_event tests
# ---------------------------------------------------------------------------

class TestLogTurnEvent:
    def test_emits_log_record(self, caplog):
        with caplog.at_level(logging.INFO, logger="dnd.events"):
            log_turn_event(
                event="turn_complete",
                session_id="s1",
                turn_number=3,
                payload={"narrative": "You find a key."},
            )
        assert len(caplog.records) == 1
        body = json.loads(caplog.records[0].message)
        assert body["event"] == "turn_complete"
        assert body["session_id"] == "s1"
        assert body["turn_number"] == 3
        assert body["narrative"] == "You find a key."

    def test_includes_timestamp(self, caplog):
        with caplog.at_level(logging.INFO, logger="dnd.events"):
            log_turn_event("turn_complete", "s1", 0, {})
        body = json.loads(caplog.records[0].message)
        assert "timestamp" in body

    def test_turn_error_event(self, caplog):
        with caplog.at_level(logging.INFO, logger="dnd.events"):
            log_turn_event("turn_error", "s1", 1, {"error": "LLM failed"})
        body = json.loads(caplog.records[0].message)
        assert body["event"] == "turn_error"
        assert body["error"] == "LLM failed"


# ---------------------------------------------------------------------------
# log_session_event tests
# ---------------------------------------------------------------------------

class TestLogSessionEvent:
    def test_session_start_emitted(self, caplog):
        with caplog.at_level(logging.INFO, logger="dnd.events"):
            log_session_event("session_start", "s2", {"initial_state": {}})
        body = json.loads(caplog.records[0].message)
        assert body["event"] == "session_start"
        assert body["session_id"] == "s2"

    def test_session_end_emitted(self, caplog):
        with caplog.at_level(logging.INFO, logger="dnd.events"):
            log_session_event("session_end", "s2", {"total_turns": 5})
        body = json.loads(caplog.records[0].message)
        assert body["event"] == "session_end"
        assert body["total_turns"] == 5


# ---------------------------------------------------------------------------
# Log exporter wiring tests
# ---------------------------------------------------------------------------

class TestBuildLogExporter:
    """``_build_log_exporter`` reads the same env vars as ``otel_setup``."""

    def _capture(self):
        captured = {}

        def fake_exporter(**kwargs):
            captured.update(kwargs)
            return MagicMock()

        return captured, fake_exporter

    def test_endpoint_appends_v1_logs_path(self, monkeypatch):
        monkeypatch.setenv("OTLP_ENDPOINT", "https://otlp.example/otlp")
        monkeypatch.setenv("OTLP_HEADERS", "dGVzdDp0ZXN0")

        captured, fake = self._capture()
        with patch("loggingfw.OTLPLogExporter", side_effect=fake):
            _build_log_exporter()

        assert captured["endpoint"] == "https://otlp.example/otlp/v1/logs"

    def test_endpoint_strips_trailing_slash(self, monkeypatch):
        monkeypatch.setenv("OTLP_ENDPOINT", "https://otlp.example/otlp/")
        monkeypatch.setenv("OTLP_HEADERS", "dGVzdDp0ZXN0")

        captured, fake = self._capture()
        with patch("loggingfw.OTLPLogExporter", side_effect=fake):
            _build_log_exporter()

        assert captured["endpoint"] == "https://otlp.example/otlp/v1/logs"

    def test_authorization_header_uses_basic_prefix(self, monkeypatch):
        monkeypatch.setenv("OTLP_ENDPOINT", "https://otlp.example/otlp")
        monkeypatch.setenv("OTLP_HEADERS", "dGVzdDp0ZXN0")

        captured, fake = self._capture()
        with patch("loggingfw.OTLPLogExporter", side_effect=fake):
            _build_log_exporter()

        assert captured["headers"] == {"Authorization": "Basic dGVzdDp0ZXN0"}

    def test_no_endpoint_omits_kwarg_so_sdk_default_applies(self, monkeypatch):
        """With ``OTLP_ENDPOINT`` unset, fall back to SDK defaults (localhost)."""
        monkeypatch.delenv("OTLP_ENDPOINT", raising=False)
        monkeypatch.delenv("OTLP_HEADERS", raising=False)

        captured, fake = self._capture()
        with patch("loggingfw.OTLPLogExporter", side_effect=fake):
            _build_log_exporter()

        assert "endpoint" not in captured
        assert "headers" not in captured

    def test_endpoint_only_omits_auth_header(self, monkeypatch):
        """Local-collector path: endpoint set, no auth needed."""
        monkeypatch.setenv("OTLP_ENDPOINT", "http://localhost:4318")
        monkeypatch.delenv("OTLP_HEADERS", raising=False)

        captured, fake = self._capture()
        with patch("loggingfw.OTLPLogExporter", side_effect=fake):
            _build_log_exporter()

        assert captured["endpoint"] == "http://localhost:4318/v1/logs"
        assert "headers" not in captured


# ---------------------------------------------------------------------------
# OTel span attribute tests
# ---------------------------------------------------------------------------

class TestTurnSpanAttributes:
    def _run_turn(self, state, llm, exporter, provider):
        import opentelemetry.trace as otel_trace
        from opentelemetry.trace import NonRecordingSpan

        with patch("turn_loop.tracer", provider.get_tracer("dnd.singleplayer")):
            loop = TurnLoop(state, RulesEngine(seed=42), llm)
            loop.run("I search the room")

        return exporter.get_finished_spans()

    def test_span_created_per_turn(self):
        provider, exporter = make_tracer_with_exporter()
        state = make_state()
        spans = self._run_turn(state, mock_llm(), exporter, provider)
        dnd_spans = [s for s in spans if s.name == "dnd.turn"]
        assert len(dnd_spans) == 1

    def test_span_attributes_on_normal_turn(self):
        provider, exporter = make_tracer_with_exporter()
        state = make_state()
        dice_triggers = [{"roll": "d6"}, {"roll": "d6"}]
        spans = self._run_turn(state, mock_llm(dice_triggers=dice_triggers), exporter, provider)
        span = next(s for s in spans if s.name == "dnd.turn")
        attrs = dict(span.attributes)
        assert attrs["dnd.session_id"] == "obs-session"
        assert attrs["dnd.turn_number"] == 0
        assert attrs["dnd.dice_roll_count"] == 2

    def test_span_status_error_on_failure(self):
        from opentelemetry.trace import StatusCode

        provider, exporter = make_tracer_with_exporter()
        state = make_state()
        broken_llm = MagicMock()
        broken_llm.stream.side_effect = RuntimeError("boom")

        with patch("turn_loop.tracer", provider.get_tracer("dnd.singleplayer")):
            loop = TurnLoop(state, RulesEngine(), broken_llm)
            with pytest.raises(RuntimeError):
                loop.run("I do something")

        span = next(s for s in exporter.get_finished_spans() if s.name == "dnd.turn")
        assert span.status.status_code == StatusCode.ERROR
