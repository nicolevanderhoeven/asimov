"""Utility/setup scripts package for the asimov-dnd app.

Importable submodules:
    - ``scripts.loggingfw``    — OTel log exporter wiring (``CustomLogFW``).
    - ``scripts.otel_setup``   — Global OTel ``Tracer``/``Meter`` provider bootstrap.
    - ``scripts.sigil_setup``  — Sigil client singleton + LangChain callback helper.

Runnable entry points (run as modules from the repo root):
    - ``python -m scripts.seed_error_metrics`` — one-off Sigil error-series seeder.
"""
