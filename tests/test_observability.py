"""
Tests for the OTel log exporter wiring in ``loggingfw``.
"""
from unittest.mock import MagicMock, patch

from scripts.loggingfw import _build_log_exporter


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
        with patch("scripts.loggingfw.OTLPLogExporter", side_effect=fake):
            _build_log_exporter()

        assert captured["endpoint"] == "https://otlp.example/otlp/v1/logs"

    def test_endpoint_strips_trailing_slash(self, monkeypatch):
        monkeypatch.setenv("OTLP_ENDPOINT", "https://otlp.example/otlp/")
        monkeypatch.setenv("OTLP_HEADERS", "dGVzdDp0ZXN0")

        captured, fake = self._capture()
        with patch("scripts.loggingfw.OTLPLogExporter", side_effect=fake):
            _build_log_exporter()

        assert captured["endpoint"] == "https://otlp.example/otlp/v1/logs"

    def test_authorization_header_uses_basic_prefix(self, monkeypatch):
        monkeypatch.setenv("OTLP_ENDPOINT", "https://otlp.example/otlp")
        monkeypatch.setenv("OTLP_HEADERS", "dGVzdDp0ZXN0")

        captured, fake = self._capture()
        with patch("scripts.loggingfw.OTLPLogExporter", side_effect=fake):
            _build_log_exporter()

        assert captured["headers"] == {"Authorization": "Basic dGVzdDp0ZXN0"}

    def test_no_endpoint_omits_kwarg_so_sdk_default_applies(self, monkeypatch):
        """With ``OTLP_ENDPOINT`` unset, fall back to SDK defaults (localhost)."""
        monkeypatch.delenv("OTLP_ENDPOINT", raising=False)
        monkeypatch.delenv("OTLP_HEADERS", raising=False)

        captured, fake = self._capture()
        with patch("scripts.loggingfw.OTLPLogExporter", side_effect=fake):
            _build_log_exporter()

        assert "endpoint" not in captured
        assert "headers" not in captured

    def test_endpoint_only_omits_auth_header(self, monkeypatch):
        """Local-collector path: endpoint set, no auth needed."""
        monkeypatch.setenv("OTLP_ENDPOINT", "http://localhost:4318")
        monkeypatch.delenv("OTLP_HEADERS", raising=False)

        captured, fake = self._capture()
        with patch("scripts.loggingfw.OTLPLogExporter", side_effect=fake):
            _build_log_exporter()

        assert captured["endpoint"] == "http://localhost:4318/v1/logs"
        assert "headers" not in captured
