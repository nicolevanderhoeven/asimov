"""OpenTelemetry bootstrap for the asimov-dnd app.

Sets up the global OTel ``TracerProvider`` and ``MeterProvider`` with OTLP
HTTP/protobuf exporters pointed at Grafana Cloud. Once configured, the
Sigil SDK (which calls ``trace.get_tracer`` / ``metrics.get_meter``) will
emit its ``gen_ai.*`` spans and histograms through this pipeline.

Enable by setting the following environment variables:

    OTLP_ENDPOINT   e.g. ``https://otlp-gateway-prod-us-central-0.grafana.net/otlp``
    OTLP_HEADERS    base64-encoded ``"<instance_id>:<api_key>"`` (no ``Basic`` prefix)

If either is missing, ``init()`` is a no-op and the global providers remain the
default ones (no exports). Safe to call multiple times; only the first call
registers providers.
"""

from __future__ import annotations

import atexit
import logging
import os
from pathlib import Path
from typing import Optional

logger = logging.getLogger(__name__)

_SERVICE_NAME = "asimov-dnd"
_DEFAULT_SERVICE_VERSION = "1.0.0"

_initialized: bool = False
_tracer_provider = None
_meter_provider = None
_service_version_cache: Optional[str] = None


def _resolve_git_short_sha(repo_root: Path) -> Optional[str]:
    """Filesystem-only git short-SHA resolver. Mirrors sigil_setup to keep versions aligned."""
    try:
        head_file = repo_root / ".git" / "HEAD"
        if not head_file.exists():
            return None
        head_content = head_file.read_text(encoding="utf-8").strip()
        if head_content.startswith("ref: "):
            ref_path = repo_root / ".git" / head_content[len("ref: "):].strip()
            if ref_path.exists():
                sha = ref_path.read_text(encoding="utf-8").strip()
                return sha[:7] if sha else None
            packed_refs = repo_root / ".git" / "packed-refs"
            if packed_refs.exists():
                ref_name = head_content[len("ref: "):].strip()
                for line in packed_refs.read_text(encoding="utf-8").splitlines():
                    if line.startswith("#") or "^" in line:
                        continue
                    parts = line.strip().split(" ", 1)
                    if len(parts) == 2 and parts[1] == ref_name:
                        return parts[0][:7]
            return None
        return head_content[:7]
    except Exception:
        return None


def _resolve_service_version() -> str:
    global _service_version_cache
    if _service_version_cache is not None:
        return _service_version_cache

    env_ver = os.getenv("ASIMOV_AGENT_VERSION", "").strip()
    if env_ver:
        _service_version_cache = env_ver
        return _service_version_cache

    sha = _resolve_git_short_sha(Path(__file__).resolve().parent)
    if sha:
        _service_version_cache = f"git-{sha}"
        return _service_version_cache

    _service_version_cache = _DEFAULT_SERVICE_VERSION
    return _service_version_cache


def init() -> bool:
    """Configure global OTel providers with OTLP exporters.

    Returns True if providers were configured, False if the function was a
    no-op (already initialized, or env vars missing, or setup failed).
    """
    global _initialized, _tracer_provider, _meter_provider

    if _initialized:
        return True

    endpoint = os.getenv("OTLP_ENDPOINT", "").strip()
    auth_b64 = os.getenv("OTLP_HEADERS", "").strip()
    if not endpoint or not auth_b64:
        logger.warning(
            "OTLP_ENDPOINT / OTLP_HEADERS not set; OTel export disabled "
            "(Sigil metrics + traces will be dropped)."
        )
        return False

    try:
        from opentelemetry import metrics, trace
        from opentelemetry.exporter.otlp.proto.http.metric_exporter import OTLPMetricExporter
        from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
        from opentelemetry.sdk.metrics import MeterProvider
        from opentelemetry.sdk.metrics.export import PeriodicExportingMetricReader
        from opentelemetry.sdk.resources import Resource
        from opentelemetry.sdk.trace import TracerProvider
        from opentelemetry.sdk.trace.export import BatchSpanProcessor
    except ImportError as exc:
        logger.warning("OpenTelemetry SDK missing; OTel export disabled: %s", exc)
        return False

    try:
        resource = Resource.create(
            {
                "service.name": _SERVICE_NAME,
                "service.version": _resolve_service_version(),
                "service.instance.id": os.getenv("HOSTNAME", "local"),
            }
        )
        headers = {"Authorization": f"Basic {auth_b64}"}

        span_exporter = OTLPSpanExporter(
            endpoint=f"{endpoint.rstrip('/')}/v1/traces",
            headers=headers,
        )
        metric_exporter = OTLPMetricExporter(
            endpoint=f"{endpoint.rstrip('/')}/v1/metrics",
            headers=headers,
        )

        tracer_provider = TracerProvider(resource=resource)
        tracer_provider.add_span_processor(BatchSpanProcessor(span_exporter))
        trace.set_tracer_provider(tracer_provider)

        metric_reader = PeriodicExportingMetricReader(
            metric_exporter,
            export_interval_millis=60_000,
        )
        meter_provider = MeterProvider(resource=resource, metric_readers=[metric_reader])
        metrics.set_meter_provider(meter_provider)

        _tracer_provider = tracer_provider
        _meter_provider = meter_provider
        _initialized = True
        atexit.register(_safe_shutdown)
        logger.info(
            "OTel initialised (endpoint=%s, service=%s@%s)",
            endpoint,
            _SERVICE_NAME,
            _resolve_service_version(),
        )
        return True
    except Exception as exc:
        logger.warning("Failed to initialise OTel providers: %s", exc)
        return False


def _safe_shutdown() -> None:
    global _tracer_provider, _meter_provider
    for provider in (_tracer_provider, _meter_provider):
        if provider is None:
            continue
        try:
            provider.shutdown()
        except Exception as exc:
            logger.warning("OTel provider shutdown raised: %s", exc)


def reset_for_tests() -> None:
    """Reset module-level state. Intended for tests only."""
    global _initialized, _tracer_provider, _meter_provider, _service_version_cache
    _initialized = False
    _tracer_provider = None
    _meter_provider = None
    _service_version_cache = None
