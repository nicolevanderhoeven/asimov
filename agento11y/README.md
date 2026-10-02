# Online evaluators

This is an advanced, optional step. It needs a working
[Grafana Cloud setup](../docs/grafana-cloud-setup.md), the gcx CLI, and
[yq](https://github.com/mikefarah/yq) for the test script. The judges run in
your Grafana Cloud stack, on a judge model the stack provides, not with your
Anthropic key.

The k6 tests grade the game when you run them. The definitions in
this directory grade it all the time: Agent Observability
rules send a sample of the `asimov-enterprise-go` agent's generations, from
any source, to LLM-judge evaluators, and the scores appear on each
conversation and on the agent's Performance view, per agent version. They
check behavior the narrator's prompt forbids but the engine can't enforce,
including the defects this demo keeps on purpose.

Each narration's system prompt ends with the engine result as JSON, so a
judge can compare what the GM says with the authoritative state. Generations
carry a `component` tag (`narration` or `action_resolution`) from
[`go-game/internal/gm/gm.go`](../go-game/internal/gm/gm.go), which the rules
match on. Every evaluator returns a single pass/fail key.

| Evaluator | Scores | Fails when |
| --- | --- | --- |
| `asimov_no_false_ending` | Narration | The GM acts out the rescue or declares the scenario complete while the engine's status isn't `rescued`. |
| `asimov_no_false_kill` | Narration | The GM says the drone is destroyed, disabled, or dark while the engine still has it at positive HP; in a generated scenario, that whatever guards the cause (a foe or a skill challenge) is defeated or cleared while the engine has it active. |
| `asimov_roll_ownership` | Narration | A roll is made by the wrong side of the table: the GM skips its own roll (the drone's or the relay discharge's, or a generated scenario's foe, challenge, or hazard rolls) or tells the player to make it, rolls one of Data's rolls with `roll_dice`, asks the player to type their result, takes a number the player typed as a roll, or tells the player to `/roll` when no roll is due. |
| `asimov_dice_fidelity` | Narration | A roll value or outcome isn't backed by the engine's rolls or a `roll_dice` result, including the outcome of a roll the game never applied. The online counterpart of [`tests/lib/trajectory-grader.js`](../tests/lib/trajectory-grader.js). |
| `asimov_gm_voice` | Narration | The GM mentions the engine or the game's internals, refuses or blocks the player instead of "yes, and", or narrates Data in the third person. |
| `asimov_no_missed_roll` | Action resolution | The resolver rules `no_roll` on a check of Data's that an experienced 5e GM would have him roll: he could fail and failing costs something, or it's an attack. The online counterpart of the [ruling judge](../go-game/README.md#trajectory-evals)'s `missed_roll`. |
| `asimov_no_unneeded_roll` | Action resolution | The resolver asks for a roll that an experienced 5e GM wouldn't call for: Data can't fail, or failing costs nothing. The counterpart of the ruling judge's `unneeded_roll`. |
| `asimov_resolution_intent` | Action resolution | The resolver's single tool call doesn't match the player's input: the wrong action or tool, a player-dictated roll or fact handled as a normal action instead of `unsupported`, an in-character attempt marked `unsupported`, or `no_roll` on a task that could fail. |

| Rule | Evaluators | Sample rate |
| --- | --- | --- |
| `asimov_narration_defects` | false ending, false kill, roll ownership | 0.5 |
| `asimov_narration_quality` | dice fidelity, GM voice | 1.0 |
| `asimov_resolution` | resolution intent | 1.0 |
| `asimov_roll_rulings` | missed roll, unneeded roll | 0.25 |

The sample rate picks whole conversations, not single generations: a
conversation a rule picks has every matching generation judged, and one it
skips has none. With a rate of 0.1, a five-playthrough experiment arm has
about a 59% chance of getting no scores at all from that rule, which is what
left the first scenario experiment without quality or resolution scores.
The quality and resolution rules therefore judge every conversation, so each
arm of an experiment is scored. That costs about one judge call per narration
or per player input: roughly 180 per rule for a five-playthrough e2e run.
Lower them again for long or high-volume runs, keeping in mind that each
conversation is then either fully scored or not at all. All rules use the
`all_assistant_generations` selector: resolver generations contain only a
tool call, so `user_visible_turn` would never match them. No rule is scoped
to an agent version, so versions such as `pre-roll-dice` and `roll-dice-v2`
compare side by side. Each judge call reads about 3,000 tokens; a
five-playthrough `tests/test-e2e.js` run costs close to a thousand judge
calls across all four rules, most of them from the quality and resolution
rules.

Most resolver calls (moves, questions, actions with no check) have no ruling
for the roll-rulings judges to judge, and they pass those.
Either of its evaluators failing means an indefensible ruling. Watch the two
pass rates on the agent's Performance view, or list the failures:
`gcx agento11y rules list-scores asimov_roll_rulings --passed=false -o json`.

## Comparing scenarios

Generated scenarios (see [Scenarios](../go-game/README.md#scenarios)) are an
experiment in how the GM copes when the facts change every game. The judges
read the ground truth from each generation, so the same evaluators score
both: in the classic adventure, `asimov_no_false_kill` judges the drone as it
always has; in a generated one, it judges the `encounter` the view reports,
whether a foe or a skill challenge, and `asimov_roll_ownership` covers that
scenario's own GM rolls. The scenario is in each generation's tags:
`scenario` is `silent-enterprise` or `generated`, and generated ones add
`scenario_variant` and `scenario_seed`. For a clean split on the agent's
Performance view, run each arm under its own agent version, as
[the k6 tests](../tests/README.md#classic-and-generated-scenarios) show.

The classic game's prompts are unchanged, so its defect rates stay
comparable with earlier versions. The judges' wording did change (version
`2026-10-02`) to cover generated scenarios; to check that changed nothing
for classic traffic, run `test-evaluator.sh` on an old classic generation.

## Set up

To set them up on a new stack with the [gcx](https://github.com/grafana/gcx) CLI, from the repository root:

1. Log in and select the stack: `gcx login`, then confirm with
   `gcx config current-context`.
2. Check the judge model is available on that stack:
   `gcx agento11y judge list-providers` and
   `gcx agento11y judge list-models --provider <provider>`. The definitions
   use `anthropic-vertex` and `claude-sonnet-4-6`; if your stack offers
   something else, change `provider` and `model` in each file under
   `agento11y/evaluators/`.
3. Create the evaluators, then the rules that use them:

   ```sh
   for f in agento11y/evaluators/*.yaml; do gcx agento11y evaluators upsert -f "$f"; done
   for f in agento11y/rules/*.yaml; do gcx agento11y rules create -f "$f"; done
   ```

4. Play the game or run a k6 test, then check the scores, failures first:

   ```sh
   gcx agento11y rules list-scores asimov_narration_defects --passed=false -o json
   ```

   Rules only score generations that arrive after they exist, and the
   evaluation is asynchronous, so allow a few minutes.

To change an evaluator, edit its file, raise its `version` (re-using a
version is rejected), and run `upsert` again. To change a rule, run
`gcx agento11y rules update <rule-id> -f agento11y/rules/<rule-id>.yaml`.
Before you upsert a changed judge, try it on a real generation without
saving anything:

```sh
agento11y/test-evaluator.sh agento11y/evaluators/asimov_no_false_kill.yaml <generation-id> [<conversation-id>]
```

It needs [yq](https://github.com/mikefarah/yq). Find generation IDs with
`gcx agento11y conversations get <conversation-id>`. In a judge's prompt,
`{{tool_calls}}` holds only the calls in the judged generation's own output,
not those from earlier steps of the same narration; `{{tool_results}}` holds
them all.

