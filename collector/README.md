# Optional: route telemetry through a local OTel Collector

By default, this app exports traces, metrics, logs, and Sigil generations **directly** to Grafana Cloud (see the root [`README.md`](../README.md)). That's the simplest setup and is what the demo uses.

This directory contains an opt-in alternative: run a local **OpenTelemetry Collector** in front of Grafana Cloud and have the app export to the Collector instead. Sigil generations still go direct (Sigil is not OTLP).

## When to use this

Pick this path if you want any of:

- **Buffering / retry** that survives Grafana Cloud blips or local network drops.
- **Processing** — sampling, attribute scrubbing/redaction, batching, transforms.
- **Fan-out** to a second backend (e.g. local Tempo/Jaeger for dev, plus Grafana Cloud).
- **Centralised auth** — credentials live in the Collector, not in every app instance.
- A demo of an OTel Collector pipeline alongside the AI app.

If none of those apply, stay with the default direct path.

## Setup

1. Copy the config template:
   ```bash
   cp otel-config.template.yml otel-config.yml
   ```
2. In your project-root `.env`, add the destination Grafana Cloud endpoint and credentials the **Collector** will forward to (separate from what the app uses):
   ```bash
   GRAFANA_CLOUD_OTLP_ENDPOINT=https://otlp-gateway-prod-us-central-0.grafana.net/otlp
   GRAFANA_CLOUD_OTLP_HEADERS=<base64 "instance_id:otlp_write_token">
   ```
3. Repoint the **app** at the local Collector by changing `.env`:
   ```bash
   OTLP_ENDPOINT=http://localhost:4318
   # OTLP_HEADERS is unused on this path (the Collector handles auth) — leave blank
   OTLP_HEADERS=
   ```
4. Start the Collector:
   ```bash
   cd collector
   docker compose up -d
   ```
5. Run the app as usual (`python play.py`). Traces, metrics, and logs now flow App → Collector → Grafana Cloud.

To switch back to the direct path, restore your original `OTLP_ENDPOINT` / `OTLP_HEADERS` and `docker compose down`.

## Production notes

This setup is fine for demos and local development but would need hardening for production:

- The `latest` tag on `otel/opentelemetry-collector-contrib` should be pinned to a specific version.
- No persistent buffering — add a [`file_storage`](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/extension/storage/filestorage) extension and a `sending_queue` with persistent storage on the exporter.
- No TLS or auth on the receivers — the Collector listens on `0.0.0.0` inside the Docker network, but if you expose it more widely, add TLS and either mTLS or a token check.
- No PII redaction — add a `transform` or `attributes` processor before exporters if prompts/responses might contain sensitive data.
- No tail-based sampling — for any non-trivial trace volume, add the `tailsamplingprocessor`.

Ask before adapting this for production usage; the right shape depends on your environment.
