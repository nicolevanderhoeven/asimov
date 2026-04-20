"""Unit tests for the otel_setup module."""

from __future__ import annotations

from unittest.mock import MagicMock, patch

import pytest


@pytest.fixture(autouse=True)
def _reset_otel_state(monkeypatch):
    """Clear OTLP env + reset module state between tests."""
    for var in ("OTLP_ENDPOINT", "OTLP_HEADERS", "ASIMOV_AGENT_VERSION", "HOSTNAME"):
        monkeypatch.delenv(var, raising=False)

    import otel_setup
    otel_setup.reset_for_tests()
    yield
    otel_setup.reset_for_tests()


class TestDisabledMode:
    """Without OTLP_* env vars, init() is a no-op."""

    def test_init_returns_false_when_env_missing(self):
        import otel_setup
        assert otel_setup.init() is False

    def test_init_returns_false_when_only_endpoint_set(self, monkeypatch):
        monkeypatch.setenv("OTLP_ENDPOINT", "https://otlp.example/otlp")
        import otel_setup
        assert otel_setup.init() is False

    def test_init_returns_false_when_only_headers_set(self, monkeypatch):
        monkeypatch.setenv("OTLP_HEADERS", "dGVzdDp0ZXN0")
        import otel_setup
        assert otel_setup.init() is False


class TestEnabledMode:
    """With both env vars set, init() wires the providers exactly once."""

    def _enable(self, monkeypatch):
        monkeypatch.setenv("OTLP_ENDPOINT", "https://otlp.example/otlp")
        monkeypatch.setenv("OTLP_HEADERS", "dGVzdDp0ZXN0")

    def test_init_returns_true_when_env_present(self, monkeypatch):
        self._enable(monkeypatch)
        import otel_setup
        with patch("opentelemetry.trace.set_tracer_provider"), patch(
            "opentelemetry.metrics.set_meter_provider"
        ):
            assert otel_setup.init() is True

    def test_init_is_idempotent(self, monkeypatch):
        """Calling init() twice should not re-register providers."""
        self._enable(monkeypatch)
        import otel_setup
        with patch("opentelemetry.trace.set_tracer_provider") as set_tp, patch(
            "opentelemetry.metrics.set_meter_provider"
        ) as set_mp:
            assert otel_setup.init() is True
            assert otel_setup.init() is True
            assert set_tp.call_count == 1
            assert set_mp.call_count == 1

    def test_auth_header_uses_basic_prefix(self, monkeypatch):
        """The Authorization header must be ``Basic <base64>``."""
        self._enable(monkeypatch)

        captured = {}

        def fake_span_exporter(*, endpoint, headers, **_):
            captured["span_endpoint"] = endpoint
            captured["span_headers"] = headers
            return MagicMock()

        def fake_metric_exporter(*, endpoint, headers, **_):
            captured["metric_endpoint"] = endpoint
            captured["metric_headers"] = headers
            return MagicMock()

        import otel_setup
        with patch(
            "opentelemetry.exporter.otlp.proto.http.trace_exporter.OTLPSpanExporter",
            side_effect=fake_span_exporter,
        ), patch(
            "opentelemetry.exporter.otlp.proto.http.metric_exporter.OTLPMetricExporter",
            side_effect=fake_metric_exporter,
        ), patch("opentelemetry.trace.set_tracer_provider"), patch(
            "opentelemetry.metrics.set_meter_provider"
        ):
            assert otel_setup.init() is True

        assert captured["span_headers"] == {"Authorization": "Basic dGVzdDp0ZXN0"}
        assert captured["metric_headers"] == {"Authorization": "Basic dGVzdDp0ZXN0"}

    def test_exporter_endpoints_appended_with_signal_paths(self, monkeypatch):
        """Endpoint should have ``/v1/traces`` and ``/v1/metrics`` appended."""
        self._enable(monkeypatch)

        captured = {}

        def fake_span_exporter(*, endpoint, headers, **_):
            captured["span_endpoint"] = endpoint
            return MagicMock()

        def fake_metric_exporter(*, endpoint, headers, **_):
            captured["metric_endpoint"] = endpoint
            return MagicMock()

        import otel_setup
        with patch(
            "opentelemetry.exporter.otlp.proto.http.trace_exporter.OTLPSpanExporter",
            side_effect=fake_span_exporter,
        ), patch(
            "opentelemetry.exporter.otlp.proto.http.metric_exporter.OTLPMetricExporter",
            side_effect=fake_metric_exporter,
        ), patch("opentelemetry.trace.set_tracer_provider"), patch(
            "opentelemetry.metrics.set_meter_provider"
        ):
            assert otel_setup.init() is True

        assert captured["span_endpoint"] == "https://otlp.example/otlp/v1/traces"
        assert captured["metric_endpoint"] == "https://otlp.example/otlp/v1/metrics"

    def test_init_swallows_exporter_errors(self, monkeypatch):
        """A failing exporter construction returns False but does not raise."""
        self._enable(monkeypatch)

        import otel_setup
        with patch(
            "opentelemetry.exporter.otlp.proto.http.trace_exporter.OTLPSpanExporter",
            side_effect=RuntimeError("exporter blew up"),
        ):
            assert otel_setup.init() is False


class TestServiceVersion:
    """_resolve_service_version precedence: env → git SHA → default."""

    def test_env_var_overrides(self, monkeypatch):
        monkeypatch.setenv("ASIMOV_AGENT_VERSION", "v2.0.0-custom")
        import otel_setup
        assert otel_setup._resolve_service_version() == "v2.0.0-custom"

    def test_falls_back_to_default_when_no_git(self, monkeypatch, tmp_path):
        """If git resolution fails, we get the hardcoded default."""
        import otel_setup
        with patch.object(otel_setup, "_resolve_git_short_sha", return_value=None):
            assert otel_setup._resolve_service_version() == "1.0.0"

    def test_git_sha_used_when_no_env(self, monkeypatch):
        import otel_setup
        with patch.object(otel_setup, "_resolve_git_short_sha", return_value="abc1234"):
            assert otel_setup._resolve_service_version() == "git-abc1234"
