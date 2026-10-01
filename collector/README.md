# Optional: route telemetry through a local OTel Collector

By default, the game exports traces, metrics, logs, and Agent Observability generations **directly** to Grafana Cloud (see the root [`README.md`](../README.md)). That's the simplest setup and is what the demo uses.

This directory contains an opt-in alternative: run a local **OpenTelemetry Collector** in front of Grafana Cloud and have the app export to the Collector instead. Agent Observability generations still go direct, because they aren't OTLP.

## When to use this

Pick this path if you want any of:

- **Buffering / retry** that survives Grafana Cloud blips or local network drops.
- **Processing** — sampling, attribute scrubbing/redaction, batching, transforms.
- **Fan-out** to a second backend (e.g. local Tempo/Jaeger for dev, plus Grafana Cloud).
- **Centralised auth** — credentials live in the Collector, not in every app instance.
- A demo of an OTel Collector pipeline alongside the AI app.

If none of those apply, stay with the default direct path.

## Setup

1. Copy the config template (the copy is git-ignored; keep credentials in `.env`, not in it):
   ```bash
   cd collector
   cp otel-config.template.yml otel-config.yml
   ```
2. In the repository-root `.env`, add the destination Grafana Cloud endpoint and credentials the **Collector** will forward to (separate from what the app uses):
   ```bash
   GRAFANA_CLOUD_OTLP_ENDPOINT=https://otlp-gateway-prod-us-central-0.grafana.net/otlp
   GRAFANA_CLOUD_OTLP_HEADERS=<base64 "otlp_instance_id:token">, the same value OTLP_HEADERS had
   ```
3. Repoint the **app** at the local Collector by changing `.env`:
   ```bash
   OTLP_ENDPOINT=http://localhost:4318
   # OTLP_HEADERS is unused on this path (the Collector handles auth) — leave blank
   OTLP_HEADERS=
   ```
4. Start the Collector, from `collector/`:
   ```bash
   docker compose up -d
   ```
5. Run the game as usual (`make play`), and check the path with `make doctor`. Traces, metrics, and logs now flow game → Collector → Grafana Cloud. The game accepts a plain-HTTP `OTLP_ENDPOINT` only on localhost, so run it on the same machine as the Collector.

To switch back to the direct path, restore your original `OTLP_ENDPOINT` / `OTLP_HEADERS` and `docker compose down`.

## Production notes

This setup is fine for demos and local development but would need hardening for production:

- The `otel/opentelemetry-collector-contrib` image is pinned to `0.150.1`; update it deliberately.
- No persistent buffering — add a [`file_storage`](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/extension/storage/filestorage) extension and a `sending_queue` with persistent storage on the exporter.
- No TLS or auth on the receivers — the Collector listens on `0.0.0.0` inside the Docker network, but if you expose it more widely, add TLS and either mTLS or a token check.
- No PII redaction — add a `transform` or `attributes` processor before exporters if prompts/responses might contain sensitive data.
- No tail-based sampling — for any non-trivial trace volume, add the `tailsamplingprocessor`.

Ask before adapting this for production usage; the right shape depends on your environment.
