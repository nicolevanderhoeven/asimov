from opentelemetry._logs import set_logger_provider
from opentelemetry.exporter.otlp.proto.grpc._log_exporter import OTLPLogExporter
from opentelemetry.sdk._logs import LoggerProvider, LoggingHandler
from opentelemetry.sdk._logs.export import BatchLogRecordProcessor
from opentelemetry.sdk.resources import Resource

import json
import logging
import os
from datetime import datetime, timezone

from dotenv import load_dotenv


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
        otlpEndpoint = os.getenv("OTLP_ENDPOINT")
        otlpHeaders = os.getenv("OTLP_HEADERS")
        exporter = OTLPLogExporter()

        self.logger_provider.add_log_record_processor(
            BatchLogRecordProcessor(exporter=exporter, max_export_batch_size=5)
        )

        handler = LoggingHandler(level=logging.NOTSET, logger_provider=self.logger_provider)
        return handler


# ---------------------------------------------------------------------------
# Structured turn / session event helpers
# ---------------------------------------------------------------------------

_event_logger = logging.getLogger("dnd.events")


def log_turn_event(
    event: str,
    session_id: str,
    turn_number: int,
    payload: dict,
) -> None:
    """Emit a structured JSON log record for a single turn event.

    ``event`` should be ``"turn_complete"`` or ``"turn_error"``.
    ``payload`` is merged with the envelope fields before serialisation.
    """
    record = {
        "event": event,
        "session_id": session_id,
        "turn_number": turn_number,
        "timestamp": datetime.now(timezone.utc).isoformat(),
        **payload,
    }
    _event_logger.info(json.dumps(record))


def log_session_event(
    event: str,
    session_id: str,
    payload: dict,
) -> None:
    """Emit a structured JSON log record for session lifecycle events.

    ``event`` should be ``"session_start"`` or ``"session_end"``.
    """
    record = {
        "event": event,
        "session_id": session_id,
        "timestamp": datetime.now(timezone.utc).isoformat(),
        **payload,
    }
    _event_logger.info(json.dumps(record))
