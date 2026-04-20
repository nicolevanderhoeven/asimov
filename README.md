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

1. Deploy OpenLIT by following the instructions [on their docs page](https://docs.openlit.io/latest/quickstart-observability) to run and install it.
2. Set up your Grafana instance or create a free [Grafana Cloud](https://nicole.to/kceu2025grafana) account.
3. **Configure your credentials securely:**
   - Copy `env.example` to `.env`: `cp env.example .env`
   - Edit `.env` and fill in your actual credentials:
     - `GRAFANA_CLOUD_USERNAME` - Your Grafana Cloud username
     - `GRAFANA_CLOUD_PASSWORD` - Your Grafana Cloud password  
     - `GRAFANA_CLOUD_OTLP_ENDPOINT` - Your OTLP endpoint URL
     - `OTLP_ENDPOINT` - Local collector endpoint (usually `http://localhost:4318`)
     - `OTLP_HEADERS` - Your auth headers for OpenLIT
     - `OPENAI_API_KEY` - Your OpenAI API token from [here](https://platform.openai.com/settings/organization/api-keys)
   - Copy `otel-config.template.yml` to `otel-config.yml`: `cp otel-config.template.yml otel-config.yml`
   - Replace the environment variable placeholders in `otel-config.yml` with your actual values:
     - Replace `${GRAFANA_CLOUD_USERNAME}` with your username
     - Replace `${GRAFANA_CLOUD_PASSWORD}` with your password
     - Replace `${GRAFANA_CLOUD_OTLP_ENDPOINT}` with your endpoint URL
4. In Grafana, import the GenAI Observability dashboard by OpenLIT by following the instructions [here](https://nicole.to/kceu25aidash).
5. Install k6 by following the instructions [here](https://nicole.to/asimovk6).
6. **(Optional) Enable the Grafana Sigil SDK** for normalized LLM generation telemetry on top of OpenLIT. Sigil runs side-by-side with OpenLIT and exports generations directly to the Sigil ingest endpoint on Grafana Cloud.
   - Install the SDK packages: `pip install sigil-sdk sigil-sdk-langchain` (or `pip install -r requirements.txt`).
   - Add these keys to your `.env` (leave `GRAFANA_CLOUD_SIGIL_ENDPOINT` unset to disable Sigil):
     - `GRAFANA_CLOUD_SIGIL_ENDPOINT` — e.g. `https://<your-stack>.grafana.net/api/v1/generations:export`
     - `GRAFANA_CLOUD_INSTANCE_ID` — your Grafana Cloud instance ID (same value as `GRAFANA_CLOUD_USERNAME`; `GRAFANA_CLOUD_INSTANCE` is also accepted as an alias)
     - `GRAFANA_CLOUD_API_KEY` — a Grafana Cloud API key with Sigil write scope
     - (optional) `ASIMOV_AGENT_VERSION` — explicit agent version string. If unset, the app uses `git-<short-sha>` when running from a checkout, falling back to `1.0.0`.
   - Generations flow directly from the app to the Sigil ingest endpoint using basic auth; OTel traces and metrics keep going through your existing collector. Open the **Sigil** app in Grafana Cloud to see conversations grouped by the `asimov-dnd` agent.
   - **Component tags** split per-call cost/latency in the Sigil agent catalog: `game_setup`, `dialogue`, `classifier`, `gm_qa`, `storyteller_single`, `storyteller_scenario`. Filter on the `sigil.component` tag.
   - **Verify request params are captured.** After your first conversations appear, open one in Sigil and confirm the generation payload has `gen_ai.request.temperature` and `gen_ai.request.max_tokens`. These are read from LangChain's invocation params by the adapter. If they're missing, file an issue against `sigil-sdk-langchain`.
   - **Multi-agent DAG placeholder.** The scenario runner emits `sigil.run.id` on the classifier generation and `sigil.run.parent_ids` on the downstream `gm_qa` / `storyteller_scenario` generations, so once Sigil adds first-class DAG support (or a `with_parent_generation_ids()` context helper in the SDK) the links can be backfilled from metadata without code changes here.

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

- [OpenLIT docs: Quickstart: AI Observability](https://docs.openlit.io/latest/quickstart-observability) for AI-specific instrumentation
- [Grafana Sigil SDK](https://github.com/grafana/sigil-sdk) for normalized LLM generation telemetry on Grafana Cloud
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