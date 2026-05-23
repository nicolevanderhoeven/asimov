"""OpenTelemetry log export wiring for the asimov-dnd app.

Logs are exported via OTLP/HTTP directly to Grafana Cloud, matching the
traces/metrics path configured in ``otel_setup.py``. The same env vars are
reused:

    OTLP_ENDPOINT   e.g. ``https://otlp-gateway-prod-us-central-0.grafana.net/otlp``
    OTLP_HEADERS    base64-encoded ``"<instance_id>:<api_key>"`` (no ``Basic`` prefix)

If ``OTLP_ENDPOINT`` is unset, the exporter is constructed with no explicit
endpoint, which falls back to the OTel SDK default (``localhost:4318``).
That keeps the optional local-collector path working without code changes.
"""

import logging
import os

from opentelemetry.exporter.otlp.proto.http._log_exporter import OTLPLogExporter
from opentelemetry.sdk._logs import LoggerProvider, LoggingHandler
from opentelemetry.sdk._logs.export import BatchLogRecordProcessor
from opentelemetry.sdk.resources import Resource


logger = logging.getLogger(__name__)


def _build_log_exporter() -> OTLPLogExporter:
    """Construct an OTLP/HTTP log exporter from ``OTLP_ENDPOINT`` / ``OTLP_HEADERS``.

    Mirrors the pattern used in ``otel_setup.py`` so traces, metrics, and logs
    all share the same configuration surface.
    """
    endpoint = os.getenv("OTLP_ENDPOINT", "").strip()
    auth_b64 = os.getenv("OTLP_HEADERS", "").strip()

    kwargs: dict = {}
    if endpoint:
        kwargs["endpoint"] = f"{endpoint.rstrip('/')}/v1/logs"
    if auth_b64:
        kwargs["headers"] = {"Authorization": f"Basic {auth_b64}"}

    if not endpoint:
        logger.warning(
            "OTLP_ENDPOINT not set; OTLP log exporter will fall back to "
            "localhost defaults (only useful with a local OTel Collector)."
        )

    return OTLPLogExporter(**kwargs)


class CustomLogFW:
    """Sets up logging using OpenTelemetry with a specified service name and instance ID."""

    def __init__(self, service_name, instance_id):
        self.logger_provider = LoggerProvider(
            resource=Resource.create(
                {
                    "service.name": service_name,
                    "service.instance.id": instance_id,
                }
            )
        )

    def setup_logging(self):
        exporter = _build_log_exporter()

        self.logger_provider.add_log_record_processor(
            BatchLogRecordProcessor(exporter=exporter, max_export_batch_size=5)
        )

        handler = LoggingHandler(level=logging.NOTSET, logger_provider=self.logger_provider)
        return handler
