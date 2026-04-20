"""One-off utility to seed Sigil's error-rate metric series in Prometheus.

Context
-------
Sigil derives error rate from the ``error_type`` label on
``gen_ai.client.operation.duration``. Successful generations carry
``error_type=""``, failed ones carry ``"provider_call_error"`` (or similar).
Prometheus returns "no data" for a filtered series that has never emitted
samples, so the Sigil UI error-rate panel shows blank until at least one
real failure exists in the stack.

This script deliberately triggers ONE authentication failure against
Anthropic so the error series is created. After the first export batch
flushes, the error-rate panel starts rendering (and reading 0 during
normal operation).

Usage
-----
From the repo root:

    python3 -m scripts.seed_error_metrics

Safe to re-run. Does not modify your ``.env`` — the invalid key override
lives only in this process's environment.
"""

from __future__ import annotations

import logging
import os
import sys
import time
from pathlib import Path

_LOG = logging.getLogger("seed_error_metrics")


def _bootstrap_env() -> None:
    """Load .env like the real app, then stomp ANTHROPIC_API_KEY with a bad value."""
    from dotenv import load_dotenv

    repo_root = Path(__file__).resolve().parent.parent
    load_dotenv(repo_root / ".env")

    # Invalid but well-formed Anthropic key — triggers a 401 at the first
    # streamed chunk. Format must look plausible; an empty string would
    # short-circuit inside the SDK before hitting the wire.
    os.environ["ANTHROPIC_API_KEY"] = "sk-ant-api03-DELIBERATELY_INVALID_FOR_METRIC_SEEDING"


def _fire_one_failing_generation() -> None:
    """Make one .stream() call that will 401 so Sigil records an error."""
    sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

    from otel_setup import init as init_otel
    from sigil_setup import sigil_langchain_config
    from langchain_anthropic import ChatAnthropic
    from langchain_core.messages import HumanMessage

    if not init_otel():
        raise SystemExit(
            "OTel init failed — check OTLP_ENDPOINT / OTLP_HEADERS in .env. "
            "Without OTel, the error metric cannot be exported."
        )

    llm = ChatAnthropic(
        model="claude-sonnet-4-6",
        temperature=0.0,
        streaming=True,
    )

    try:
        for _ in llm.stream(
            [HumanMessage(content="Seed error metric. This call is expected to fail.")],
            config=sigil_langchain_config(component="error_seed"),
        ):
            pass
    except Exception as exc:
        _LOG.info(
            "Got expected auth failure: %s: %s",
            type(exc).__name__,
            exc,
        )
        return

    raise RuntimeError(
        "Call unexpectedly succeeded — invalid key was not rejected. "
        "Check the override and try again."
    )


def main() -> None:
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(levelname)s %(name)s %(message)s",
    )

    _bootstrap_env()
    _fire_one_failing_generation()

    # OTel metric reader in otel_setup.py exports every 60s. Give it one
    # full cycle plus a buffer so the error series is definitely on the wire.
    flush_seconds = 65
    _LOG.info("Waiting %ds for OTel metric export batch to flush…", flush_seconds)
    time.sleep(flush_seconds)
    _LOG.info("Done. Error series should now exist in Prometheus.")


if __name__ == "__main__":
    main()
