# Asimov's Zeroth Law of Robotics: Observability for AI

Author: Nicole van der Hoeven ([site](https://nicole.to/site), [Mastodon](https://pkm.social/@nicole), [email](mailto:nicole@grafana.com))

This is a repository for the slides and code for the talk "Asimov's Zeroth Law of Robotics: Observability for AI" presented at:
- [KubeCon Europe 2025](https://nicolevanderhoeven.com/blog/20250402-asmiovs-zeroth-law-of-robotics/) in London, England ([video link](https://www.youtube.com/watch?v=x6EKTCAWtn8))
- [Dutch Cloud Native Days 2025](https://nicolevanderhoeven.com/blog/20250703-asimovs-zeroth-law-dutch-cloud-native-day/) in Utrecht, the Netherlands
- [Newcrafts 2025](https://nicolevanderhoeven.com/blog/20251106-asimovs-zeroth-law-newcrafts/) in Paris, France ([slides](https://nicole.to/asimovslides))
- ExpoQA 2026 in Madrid, Spain ([slides](https://nicole.to/expoqa2026))

This repository consists of:
- The Go app in [`go-game/`](go-game/README.md): **The Silent Enterprise**, using Grafana AI SDK and Agent Observability. It runs either as an interactive CLI (`go run ./cmd/enterprise`) or as an HTTP API (`go run ./cmd/enterprise --serve`) for testing.
- One fixed-prompt k6 regression test in [`tests/test-code.js`](tests/test-code.js). It checks the Python-era positronic, Enterprise, and role-confusion cases with JavaScript, plus basic game and HTTP behavior. k6 itself makes no Anthropic calls.
- One AI-judged k6 test in [`tests/test-ai.js`](tests/test-ai.js). Claude varies the lore and role probes, and another Claude model grades the game's narration against facts fixed in the script.
- One k6 trajectory test in [`tests/test-trajectory.js`](tests/test-trajectory.js). It plays a fixed script against the dice GM (`/dm`), a separate agent whose model owns the dice through a `roll_dice` tool, and grades the path each turn took: rolls narrated that no call returned, more than one call in a turn, and rolls reported with no call at all.
- One short k6 traffic seed in [`tests/test_traffic.js`](tests/test_traffic.js). It drives the game's natural-language endpoint to populate application telemetry in Grafana.
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

See [`go-game/README.md`](go-game/README.md) for full run instructions (CLI usage, `--offline` mode, and the agent/observability design).

## Usage

1. Run the game: `cd go-game && go run ./cmd/enterprise`. See [`go-game/README.md`](go-game/README.md) for CLI commands (`/do`, natural language, `--offline`, etc.).
2. Interact with the game.
3. Monitor your app using the GenAI Observability dashboard as well as the Drilldown Logs/Metrics/Traces features in Grafana.
4. Run the k6 scripts against the app's HTTP API instead of the CLI. Start the server with `cd go-game && go run ./cmd/enterprise --serve --addr :8080` (it loads `../.env`). These scripts use `/resolve` (the trajectory test uses `/dm`), so the server must run with a working Anthropic key; `--offline` disables that route. Use `-e BASE_URL=http://host:8080` with k6 if the server is elsewhere.

   | Command | Purpose |
   | --- | --- |
   | `k6 run tests/test-code.js` | One pass of fixed prompts and code assertions. Only the game calls Anthropic. |
   | `k6 run tests/test-ai.js` | One pass of varied probes and LLM judgments. k6 needs `ANTHROPIC_API_KEY` in its own environment. |
   | `k6 run --summary-mode=full tests/test-trajectory.js` | Ten runs of a five-turn script against the dice GM, graded on each turn's trajectory. Set `-e RUNS=50 -e VUS=4` for more. Add `--log-format=raw --console-output=traj.jsonl` to save one JSON line per turn with its full trajectory and findings. k6 needs `ANTHROPIC_API_KEY` in its own environment for the judge. |
   | `k6 run tests/test_traffic.js` | One minute of paced game traffic, including actions, a question, and a rejected rule override. Use `-u 2 -d 3m` to seed more traffic. |

   The AI test uses `GENERATOR_MODEL=claude-sonnet-4-6` and `JUDGE_MODEL=claude-opus-5-5` by default; set those k6 environment variables to change models. The trajectory test uses `JUDGE_MODEL=claude-haiku-4-5-20251001` by default, a small judge asked only whether a zero-roll turn reports a die roll. All four scripts fail the run when a check fails; for the trajectory test that means the model misbehaved, which is the point, and the `traj_*` rates and per-turn checks show how often. The traffic seed populates **the game's** configured Grafana telemetry through its server; k6's own metrics need a separate k6 output configuration to appear in Grafana. Each `/resolve` request usually makes two game model calls, so increasing traffic also increases Anthropic usage.

### Original code-based AI checks

`tests/test-code.js` reuses the three original prompts from `tests/test.js` at
commit `98b13b5`. The Go API returns `narration` and an authoritative `result`
instead of the Python response's `speaker` field, and it does not promise the
old turn-ending phrase. The H01–H09 checks therefore look for the same lore
(`positronic` and ship), a GM response that returns control to Data, no role
switch, and continued focus on the missing crew. Each prompt gets a fresh
session so a prior answer cannot supply a keyword. The code checks are useful
regression signals, but a keyword match alone does not prove a factual answer;
the AI test evaluates the full narration against explicit lore and role rubrics.

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
