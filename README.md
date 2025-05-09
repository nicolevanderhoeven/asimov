# Asimov's Zeroth Law of Robotics: Observability for AI

Author: Nicole van der Hoeven

This is a repository for the slides and code for the talk "Asimov's Zeroth Law of Robotics: Observability for AI" presented at KubeCon Europe 2025 in London, England.

## Usage

This repository consists of:
- A D&D-based AI game. Its main logic is in `two_player_dnd.py`, and `play.py` is the Flask wrapper for it.
- The OTel configuration, including authentication, in `otel-config.yml`
- The Docker compose configuration to run the OTel Collector, in `docker-compose.yml`.
- A k6 test to run against the AI app, in `test.js`.
- A custom logging framework, in `loggingfw.py`.

To replicate my setup as I demonstrate in the talk:
1. Start the Docker daemon and deploy the OTel Collector by running: `docker compose up -d`.
2. Deploy OpenLIT by following the instructions [on their docs page](https://docs.openlit.io/latest/quickstart-observability) to run and install it.
3. Set up your Grafana instance or create a free [Grafana Cloud](https://nicole.to/kceu2025grafana) account. 
4. Put your Grafana credentials in `.env`, specifically `OTLP_ENDPOINT`, `OTLP_HEADERS`, `GRAFANA_CLOUD_INSTANCE`, and `GRAFANA_CLOUD_API_KEY`. You can get these details by following the instructions [here](https://nicole.to/kceu25otlp).
5. Put your OpenAI API token in `.env` as a value for `OPENAI_KEY`. You can find your OpenAI token [here](https://platform.openai.com/settings/organization/api-keys).
6. In Grafana, import the GenAI Observability dashboard by OpenLIT by following the instructions [here](https://nicole.to/kceu25aidash).
7. Run the D&D app by running: `python play.py`.
8. Monitor your app using the GenAI Observability dashboard as well as the Drilldown Logs/Metrics/Traces features in Grafana.
9. Install k6 and run the k6 test using `k6 run test.js`.

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