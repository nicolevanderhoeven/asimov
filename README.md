# Asimov's Zeroth Law of Robotics: Observability for AI

Author: Nicole van der Hoeven ([Mastodon](https://pkm.social/@nicole))

This is a repository for the slides and code for the talk "Asimov's Zeroth Law of Robotics: Observability for AI" presented at:
- KubeCon Europe 2025 in London, England ([video link](https://www.youtube.com/watch?v=x6EKTCAWtn8))
- Dutch Cloud Native Days 2025 in Utrecht, the Netherlands

This repository consists of:
- A D&D-based AI game. Its main logic is in `two_player_dnd.py`, and `play.py` is the Flask wrapper for it.
- The OTel configuration, including authentication, in `otel-config.yml`
- The Docker compose configuration to run the OTel Collector, in `docker-compose.yml`.
- A k6 test to run against the AI app, in `test.js`.
- A custom logging framework, in `loggingfw.py`.
- A CLI wrapper for the game, in `cli_play.py`.

## Setup

1. Deploy OpenLIT by following the instructions [on their docs page](https://docs.openlit.io/latest/quickstart-observability) to run and install it.
2. Set up your Grafana instance or create a free [Grafana Cloud](https://nicole.to/kceu2025grafana) account.
3. Put your Grafana credentials in `.env`, specifically `OTLP_ENDPOINT`, `OTLP_HEADERS`, `GRAFANA_CLOUD_INSTANCE`, and `GRAFANA_CLOUD_API_KEY`. You can get these details by following the instructions [here](https://nicole.to/kceu25otlp).
4. Put your OpenAI API token in `.env` as a value for `OPENAI_API_KEY`. You can find your OpenAI token [here](https://platform.openai.com/settings/organization/api-keys).
5. In Grafana, import the GenAI Observability dashboard by OpenLIT by following the instructions [here](https://nicole.to/kceu25aidash).
6. Install k6 by following the instructions [here](https://nicole.to/asimovk6).

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
- [OpenTelemetry](https://opentelemetry.io/) for instrumentation
- Free [Grafana Cloud](https://nicole.to/kceu2025grafana) for visibility
- [Loki](https://nicole.to/kceu2025loki) for logs
- [Prometheus](https://prometheus.io/) for metrics
- [Tempo](https://nicole.to/kceu2025tempo) for traces
- [k6](https://nicole.to/kceu2025k6) for testing


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