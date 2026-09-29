# Asimov's Zeroth Law of Robotics: Observability for AI

Author: Nicole van der Hoeven ([site](https://nicole.to/site), [Mastodon](https://pkm.social/@nicole), [email](mailto:nicole@grafana.com))

This is a repository for the slides and code for the talk "Asimov's Zeroth Law of Robotics: Observability for AI" presented at:
- [KubeCon Europe 2025](https://nicolevanderhoeven.com/blog/20250402-asmiovs-zeroth-law-of-robotics/) in London, England ([video link](https://www.youtube.com/watch?v=x6EKTCAWtn8))
- [Dutch Cloud Native Days 2025](https://nicolevanderhoeven.com/blog/20250703-asimovs-zeroth-law-dutch-cloud-native-day/) in Utrecht, the Netherlands
- [Newcrafts 2025](https://nicolevanderhoeven.com/blog/20251106-asimovs-zeroth-law-newcrafts/) in Paris, France ([slides](https://nicole.to/asimovslides))
- ExpoQA 2026 in Madrid, Spain ([slides](https://nicole.to/expoqa2026))

This repository consists of:
- The Go app in [`go-game/`](go-game/README.md): **The Silent Enterprise**, using Grafana AI SDK and Agent Observability. It runs either as an interactive CLI (`go run ./cmd/enterprise`) or as an HTTP API (`go run ./cmd/enterprise --serve`) for load testing.
- A k6 load test against the HTTP API's action endpoint, in `tests/test.js`, using a live model to choose each action from the engine's own available options.
- A k6 test that uses AI to generate and judge adversarial player input against the HTTP API's natural-language endpoint, in `tests/test-ai.js`.
- A single-VU k6 functional test (LLM-chosen action sequence with adaptive state-transition checks, plus malformed-input/unknown-session edge cases) in `tests/test_functional.js`.
- A ramping-VU k6 traffic generator, for populating metrics/logs/traces under sustained load, in `tests/test_traffic.js`.
- (optional) A local OpenTelemetry Collector setup in [`collector/`](collector/) for routing telemetry through a Collector pipeline instead of direct OTLP. See [`collector/README.md`](collector/README.md).

![A diagram of the architecture from the original version of this talk: a Flask app sending traces, metrics, and logs to OpenTelemetry and generations to the Sigil API, with k6 driving load against it and everything terminating in Grafana Cloud. The app has since been rewritten in Go (see go-game/), but the OpenTelemetry/Grafana Cloud side of this diagram still applies.](/assets/Asimov's%20Zeroth%20Law%20of%20Robotics%20-%20ExpoQA%202026.jpg)

## Setup

Telemetry is Grafana's Agent Observability SDK + OTel, wired up in [`go-game/internal/telemetry`](go-game/internal/telemetry/telemetry.go). Agent Observability handles normalized generation export, and OTel handles `gen_ai.*` metrics, traces, and structured logs.

By default all signals (generations, traces, metrics, logs) go **directly** to Grafana Cloud's OTLP gateway. A local OTel Collector is **not required**. If you want to route through a Collector — for buffering, sampling, redaction, or fan-out — see the optional setup in [`collector/`](collector/).

### Steps

1. Create a free [Grafana Cloud](https://nicole.to/kceu2025grafana) account (or use an existing stack) and enable the **Sigil** app on that stack.
2. Install Go 1.26.3 or newer (or let Go's toolchain auto-download handle it).
3. Copy `env.example` to `.env`: `cp env.example .env`.
4. Fill in `.env`:
   - `ANTHROPIC_API_KEY` — your Anthropic API key.
   - **OTel (metrics + traces + logs):**
     - `OTLP_ENDPOINT` — your stack's OTLP gateway, e.g. `https://otlp-gateway-prod-us-central-0.grafana.net/otlp`.
     - `OTLP_HEADERS` — base64-encoded `"<instance_id>:<otlp_write_token>"`. The app prefixes `Basic ` automatically.
   - **Agent Observability (generations):**
     - `AGENTO11Y_ENDPOINT` (or the legacy alias `GRAFANA_CLOUD_SIGIL_ENDPOINT`) — e.g. `https://sigil-prod-us-central-0.grafana.net/api/v1/generations:export`.
     - `GRAFANA_CLOUD_INSTANCE_ID` — your Grafana Cloud instance ID (or set `GRAFANA_CLOUD_INSTANCE` as an alias).
     - `GRAFANA_CLOUD_API_KEY` — a Grafana Cloud API key with Sigil-write scope.
   - **Optional:**
     - `ASIMOV_AGENT_VERSION` — explicit agent version. Defaults to `go-experiment-v1`.
5. In the Sigil app, link `grafanacloud-<stack>-prom` as the Prometheus datasource and your stack's Tempo as the traces datasource. Without this, conversations will appear but rollup panels stay empty.
6. Install k6 by following the instructions [here](https://nicole.to/asimovk6) if you want to run load tests.

See [`go-game/README.md`](go-game/README.md) for full run instructions (CLI usage, `--offline` mode, save/resume, and the agent/observability design).

## Usage

1. Run the game: `cd go-game && go run ./cmd/enterprise`. See [`go-game/README.md`](go-game/README.md) for CLI commands (`/do`, natural language, `--offline`, `--resume`, etc.).
2. Interact with the game.
3. Monitor your app using the GenAI Observability dashboard as well as the Drilldown Logs/Metrics/Traces features in Grafana.
4. Run the k6 tests against the app's HTTP API instead of the CLI:
   - Start the server: `cd go-game && go run ./cmd/enterprise --serve --addr :8080` (needs `ANTHROPIC_API_KEY` in `../.env` or the shell environment; add `--offline` for a deterministic run with no LLM calls, which disables the `/resolve` route and returns `503` for it).
   - `test.js`, `test_functional.js`, and `test_traffic.js` read `available_actions` from the engine's own response and ask a live model to choose one (`k6 run -e ANTHROPIC_API_KEY=... tests/test.js`, etc.) — this keeps the actions they submit valid as location/combat change, instead of a fixed or blindly-random choice going stale. `ANTHROPIC_API_KEY` is optional for these three: without it, they fall back to picking randomly among the currently-available actions (still always valid, just not model-chosen).
   - `k6 run tests/test.js` — 10 VUs, each with its own session, submitting a short LLM-chosen action sequence and asserting on the resulting game state.
   - `k6 run tests/test-ai.js` — AI-generated adversarial natural-language input against the `/resolve` endpoint (needs `-e ANTHROPIC_API_KEY=...`; this one has no fallback, since it's testing the natural-language path itself).
   - `k6 run tests/test_functional.js` — a single-VU, single-session run of an LLM-chosen action sequence with adaptive-but-deterministic assertions (any chosen action must come back allowed with the turn counter advanced by one; a "move" must land at its target location), plus malformed-input/unknown-session edge cases.
   - `k6 run tests/test_traffic.js` — a ramping-VU load (0→10 VUs over ~7 minutes), each VU with its own session, mixing status checks and LLM-chosen actions, with a small share of intentionally malformed requests, to generate steady traffic for viewing metrics, logs, and traces in Grafana.

## Resources

- [Grafana Sigil SDK](https://github.com/grafana/sigil-sdk) for normalized LLM generation telemetry, metrics, and traces on Grafana Cloud
- [OpenTelemetry](https://opentelemetry.io/) for instrumentation
- Free [Grafana Cloud](https://nicole.to/kceu2025grafana) for visibility
- [Loki](https://nicole.to/lokirepo) for logs
- [Prometheus](https://nicole.to/promrepo) for metrics
- [Tempo](https://nicole.to/temporepo) for traces
- [k6](https://nicole.to/k6repo) for testing

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

Quotes:
- https://grafana.com/blog/going-beyond-ai-chat-response-how-were-building-an-agentic-system-to-drive-grafana
- https://www.datadoghq.com/blog/engineering/bits-ai-eval-platform/#why-tool-level-testing-and-live-replay-werent-enough
- https://www.anthropic.com/engineering/demystifying-evals-for-ai-agents
