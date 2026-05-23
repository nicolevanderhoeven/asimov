"""Smoke test: emit one log event, one span, and one metric, then flush.

Run with `python3 scripts/smoke_telemetry.py` from the repo root. This makes
no LLM calls — it just exercises the OTel + loggingfw wiring so you can
confirm data lands in Grafana Cloud.

After it completes, look in your Grafana Cloud stack for:
  - Logs:    {service_name="asimov-dnd"} |= "smoke_telemetry"
  - Traces:  service.name="asimov-dnd"   span.name="smoke.test"
  - Metrics: smoke_telemetry_runs_total{service_name="asimov-dnd"}
"""

from __future__ import annotations

import logging
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from dotenv import load_dotenv

load_dotenv()

from loggingfw import CustomLogFW, log_session_event
from otel_setup import init as init_otel

import os
from opentelemetry import metrics, trace


def main() -> None:
    print("Initialising OTel (traces + metrics)...")
    if not init_otel():
        print("  WARNING: OTel init returned False — check OTLP_ENDPOINT/OTLP_HEADERS.")

    print("Wiring loggingfw...")
    log_fw = CustomLogFW(
        service_name="asimov-dnd",
        instance_id=os.getenv("HOSTNAME", "local"),
    )
    handler = log_fw.setup_logging()
    root_logger = logging.getLogger()
    root_logger.addHandler(handler)
    root_logger.setLevel(logging.INFO)

    print("Emitting span...")
    tracer = trace.get_tracer("smoke.test")
    with tracer.start_as_current_span("smoke.test") as span:
        span.set_attribute("smoke.kind", "telemetry-wiring")

        print("Emitting metric...")
        meter = metrics.get_meter("smoke.test")
        counter = meter.create_counter(
            "smoke_telemetry_runs_total",
            description="Times the telemetry smoke script ran.",
        )
        counter.add(1, {"result": "ok"})

        print("Emitting structured log event...")
        log_session_event(
            event="smoke_telemetry",
            session_id="smoke-001",
            payload={"note": "Hello from the telemetry smoke test."},
        )

    print("Sleeping 2s to let batch processors flush before shutdown...")
    time.sleep(2)
    print("Done. Process will now exit; atexit hooks will flush remaining data.")


if __name__ == "__main__":
    main()
