# Asimov's Zeroth Law of Robotics: Observability for AI

Author: Nicole van der Hoeven ([Mastodon](https://pkm.social/@nicole))

This is a repository for the slides and code for the talk "Asimov's Zeroth Law of Robotics: Observability for AI" presented at:
- [KubeCon Europe 2025](https://nicolevanderhoeven.com/blog/20250402-asmiovs-zeroth-law-of-robotics/) in London, England ([video link](https://www.youtube.com/watch?v=x6EKTCAWtn8))
- [Dutch Cloud Native Days 2025](https://nicolevanderhoeven.com/blog/20250703-asimovs-zeroth-law-dutch-cloud-native-day/) in Utrecht, the Netherlands
- [Newcrafts 2025](https://nicolevanderhoeven.com/blog/20251106-asimovs-zeroth-law-newcrafts/) in Paris, France

This repository consists of:
- A D&D-based AI game. Its main logic is in `two_player_dnd.py`, and `play.py` is the Flask wrapper for it.
- The OTel configuration, including authentication, in `otel-config.yml`
- The Docker compose configuration to run the OTel Collector, in `docker-compose.yml`.
- A k6 test to run against the AI app, in `test.js`.
- A custom logging framework, in `loggingfw.py`.
- A CLI wrapper for the game, in `cli_play.py`.
- (new) A k6 test that uses AI to test the AI app, in `test-ai.js`.

![A diagram of the architecture of the AI app, showing the D&D game, the OpenTelemetry Collector, and Grafana Cloud](/assets/Asimov's%20Zeroth%20Law%20of%20Robotics%20-%20Dutch%20Cloud%20Native%20Day%202025.svg)

## Setup

Telemetry is now Sigil-only. The Sigil SDK handles both normalized generation export **and** `gen_ai.*` OTel metrics/traces, so OpenLIT is no longer needed. Two small modules wire this up:

- `sigil_setup.py` — singleton Sigil client + LangChain callback helper (generations).
- `otel_setup.py` — bootstraps the global OTel `TracerProvider` + `MeterProvider` with OTLP/HTTP exporters. Sigil's histograms (`gen_ai.client.operation.duration`, `gen_ai.client.token.usage`, `gen_ai.client.time_to_first_token`, `gen_ai.client.tool_calls_per_operation`) and spans flow through these.

### Steps

1. Create a free [Grafana Cloud](https://nicole.to/kceu2025grafana) account (or use an existing stack) and enable the **Sigil** app on that stack.
2. Install dependencies: `pip install -r requirements.txt`.
3. Copy `env.example` to `.env`: `cp env.example .env`.
4. Fill in `.env`:
   - `ANTHROPIC_API_KEY` — your Anthropic API key.
   - `OPENAI_API_KEY` — your OpenAI API key (if using OpenAI models).
   - **OTel (metrics + traces):**
     - `OTLP_ENDPOINT` — your stack's OTLP gateway, e.g. `https://otlp-gateway-prod-us-central-0.grafana.net/otlp`.
     - `OTLP_HEADERS` — base64-encoded `"<instance_id>:<otlp_write_token>"`. The app prefixes `Basic ` automatically.
   - **Sigil (generations):**
     - `GRAFANA_CLOUD_SIGIL_ENDPOINT` — e.g. `https://sigil-prod-us-central-0.grafana.net/api/v1/generations:export`.
     - `GRAFANA_CLOUD_INSTANCE_ID` — your Grafana Cloud instance ID (or set `GRAFANA_CLOUD_INSTANCE` as an alias).
     - `GRAFANA_CLOUD_API_KEY` — a Grafana Cloud API key with Sigil-write scope.
   - **Optional:**
     - `ASIMOV_AGENT_VERSION` — explicit agent version. Defaults to `git-<short-sha>`, falling back to `1.0.0`.
     - `SSL_CERT_FILE` — path to your CA bundle if your Python install lacks trust roots (common on python.org macOS builds). `certifi/cacert.pem` works.
5. In the Sigil app, link `grafanacloud-<stack>-prom` as the Prometheus datasource and your stack's Tempo as the traces datasource. Without this, conversations will appear but rollup panels stay empty.
6. Install k6 by following the instructions [here](https://nicole.to/asimovk6) if you want to run load tests.

### What you get in the Sigil app

- **Conversations** — every LLM call, grouped by `conversation_id`, with full inputs/outputs, tagged by `sigil.component` (`game_setup`, `dialogue`, `classifier`, `gm_qa`, `storyteller_single`, `storyteller_scenario`).
- **Rollup metrics** — requests, error rate, p50/p95 latency, token consumption, tool calls per operation (from Sigil's `gen_ai.client.*` histograms).
- **Traces** — one span per LLM call, with `gen_ai.*` semantic-convention attributes.
- **DAG placeholder** — the scenario runner emits `sigil.run.id` on classifier generations and `sigil.run.parent_ids` on downstream `gm_qa` / `storyteller_scenario` generations, so multi-agent links can be rendered once Sigil exposes native DAG support.
- **Request params** — `gen_ai.request.temperature` and `gen_ai.request.max_tokens` appear on each generation (read from LangChain's invocation params by `sigil-sdk-langchain`).

### Time to first token (TTFT)

TTFT only populates for streaming calls. This app uses `.invoke()` (non-streaming), so TTFT panels stay empty. Switch specific call sites to `.stream()` if you want TTFT coverage.

## Usage

To replicate my setup as I demonstrate in the talk:
1. Start the Docker daemon and deploy the OTel Collector by running: `docker compose up -d`.
2. Run the D&D app by running: `python play.py`. Alternatively, you can run the CLI version of the game by running `python cli_play.py`.
3. Interact with the game.

If you're using the Flask app:
    - You can start the game by sending this to the command line: `curl -X GET http://localhost:5050/`.
    - You can respond to the game by sending a POST request with your input, like this:
```bash
 curl -X POST http://localhost:5050/play \
     -H "Content-Type: application/json" \
     -d '{"message": "I scan the ship for life signs."}'
```

If you're using the CLI version, type your input directly into the terminal after the welcome message. Type `exit` or `quit` to end the game.

4. Monitor your app using the GenAI Observability dashboard as well as the Drilldown Logs/Metrics/Traces features in Grafana.
5. Run the k6 test using `k6 run test.js`.

## Resources

- [Grafana Sigil SDK](https://github.com/grafana/sigil-sdk) for normalized LLM generation telemetry, metrics, and traces on Grafana Cloud
- [OpenTelemetry](https://opentelemetry.io/) for instrumentation
- Free [Grafana Cloud](https://nicole.to/kceu2025grafana) for visibility
- [Loki](https://nicole.to/lokirepo) for logs
- [Prometheus](https://nicole.to/promrepo) for metrics
- [Tempo](https://nicole.to/temporepo) for traces
- [k6](https://nicole.to/k6repo) for testing


## Slides

You can find the slides [here](https://nicole.to/asimovslides).

## References

Asimov, I. (1942). Runaround. In I, Robot (pp. 1-42). Gnome Press.

Pictures in presentation:
- https://animalia-life.club/qa/pictures/hal-9000-im-sorry-dave
- https://screenrant.com/star-trek-next-generation-data-make-no-sense-illogical/
- https://www.blogtorwho.com/smile-reactions/
- https://emsonthra.wordpress.com/2017/05/12/character-analysis-david-8/
- https://daleksrus.fandom.com/wiki/The_Daleks
- https://warnerbros.fandom.com/wiki/Rosey
- https://moviesandmania.com/2014/04/10/hell-is-other-robots-futurama-episode-animated-tv/
- https://www.reddit.com/r/SummerGlau/comments/gs9rlj/battle_damaged_terminator_used_unseen_outtake/
- https://theconversation.com/how-long-until-we-can-build-r2-d2-and-c-3po-52400
- https://www.flickr.com/photos/elferrada/2708912082
- https://memory-alpha.fandom.com/wiki/Locutus_of_Borg
- https://ita.animalia-life.club/vero-acciaio-tutti-i-personaggi-dei-robot