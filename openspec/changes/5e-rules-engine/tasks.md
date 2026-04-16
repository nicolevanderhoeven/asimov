## 1. PlayerState — 5e character model

- [x] 1.1 Add `proficiency_bonus: int = 2` field to `PlayerState` — touches `game_state.py`
- [x] 1.2 Add `skill_proficiencies: list[str]` field (e.g. `["athletics", "investigation"]`) — touches `game_state.py`
- [x] 1.3 Add `saving_throw_proficiencies: list[str]` field (e.g. `["STR", "CON"]`) — touches `game_state.py`
- [x] 1.4 Add `ability_modifier(ability: str) -> int` method: `floor((score - 10) / 2)` — touches `game_state.py`
- [x] 1.5 Add `skill_modifier(skill: str, skill_abilities: dict) -> int` method: ability mod + proficiency if proficient — touches `game_state.py`
- [x] 1.6 Add `class_features: dict[str, Any]` field to hold Second Wind / Self-Repair Cycle metadata — touches `game_state.py`
- [x] 1.7 Add `equipment: list[dict]` field with structured weapon/armor data (replaces flat `inventory` strings for mechanical items) — touches `game_state.py`
- [x] 1.8 Write unit tests: modifier derivation for all six abilities, skill modifier with/without proficiency, equipment structure — touches `tests/test_game_state.py`

## 2. RulesEngine — 5e ability checks and attacks

- [x] 2.1 Add `ability_modifier(score: int) -> int` utility function — touches `rules_engine.py`
- [x] 2.2 Refactor `DiceTrigger` to support `advantage: bool = False` and `disadvantage: bool = False` — touches `rules_engine.py`
- [x] 2.3 Implement advantage/disadvantage: roll 2d20, take higher (advantage) or lower (disadvantage); if both apply, they cancel — touches `rules_engine.py`
- [x] 2.4 Implement natural-20 / natural-1 handling: nat 20 on attack = critical hit, nat 1 on attack = auto-miss — touches `rules_engine.py`
- [x] 2.5 Add `resolve_ability_check(skill, dc, player, skill_abilities, advantage?, disadvantage?) -> DiceResult` that computes modifier from ability scores + proficiency — touches `rules_engine.py`
- [x] 2.6 Add `resolve_attack(attacker_bonus, target_ac, damage_str, ability_mod, critical?) -> AttackResult` with proper crit damage (double dice) — touches `rules_engine.py`
- [x] 2.7 Add `resolve_initiative(dex_modifier) -> int` — touches `rules_engine.py`
- [x] 2.8 Add `resolve_saving_throw(ability, dc, player, advantage?, disadvantage?) -> DiceResult` — touches `rules_engine.py`
- [x] 2.9 Update `DiceResult` model to include `natural_roll: int` (the raw d20 before advantage/disadvantage selection), `is_critical: bool`, `is_fumble: bool` — touches `game_state.py`
- [x] 2.10 Write unit tests: advantage picks higher, disadvantage picks lower, cancel-out, nat 20 crit, nat 1 miss, ability check with proficiency, saving throw — touches `tests/test_rules_engine.py`

## 3. Conditions — mechanical effects

- [x] 3.1 Define a `Condition` enum or constant set for the 5 supported conditions: `stunned`, `frightened`, `poisoned`, `incapacitated`, `prone` — touches `game_state.py`
- [x] 3.2 Add `check_conditions_for_disadvantage(conditions: list[str]) -> bool` helper that returns True if any active condition imposes disadvantage on checks — touches `rules_engine.py`
- [x] 3.3 Add `check_conditions_for_attack_advantage(conditions: list[str]) -> bool` helper for attacks against a target (e.g. stunned → advantage) — touches `rules_engine.py`
- [x] 3.4 Wire condition checks into `resolve_ability_check` and `resolve_attack` — touches `rules_engine.py`
- [x] 3.5 Update `_apply_hazard_effect` to apply SRD condition names instead of freeform strings — touches `scenario_runner.py`
- [x] 3.6 Write unit tests: poisoned gives disadvantage, stunned gives auto-fail on STR/DEX saves, attack advantage vs stunned target — touches `tests/test_rules_engine.py`

## 4. Positronic Operative — class definition and Data's character

- [x] 4.1 Create a `classes/` module or `character_classes.py` with `PositronicOperative` dataclass defining: hit die (d10), proficiency bonus by level, saving throw proficiencies (STR, CON), skill proficiency options, fighting style, and Second Wind / Self-Repair Cycle — touches new file `character_classes.py`
- [x] 4.2 Create `build_data_character() -> PlayerState` factory that returns Data's full 5e-compliant character sheet as a `PlayerState` — touches `character_classes.py`
- [x] 4.3 Update `starter_character()` in `game_state.py` to use the Positronic Operative stats instead of the generic Fighter — touches `game_state.py`
- [x] 4.4 Write unit tests: Data's HP is `10 + CON_mod = 12`, AC is 14, proficiency bonus is +2, STR modifier is +2, CHA modifier is -1 — touches `tests/test_game_state.py`

## 5. Scenario data — 5e-aligned stats

- [x] 5.1 Update `scenarios/silent-relay/initial_state.json` with Data's 5e ability scores, proficiency bonus, skill proficiencies, equipment, and class features — touches `scenarios/silent-relay/initial_state.json`
- [x] 5.2 Update `scenarios/silent-relay/rules_profile.json` with `skill_abilities` mapping (command→CHA, science→INT, etc.) and DC tiers — touches `scenarios/silent-relay/rules_profile.json`
- [x] 5.3 Update `scenarios/silent-relay/adversaries.json` with 5e-compatible stat block (AC instead of defense, ability scores, initiative bonus) — touches `scenarios/silent-relay/adversaries.json`
- [x] 5.4 Update `scenarios/silent-relay/hazards.json` fail effects to use SRD condition names — touches `scenarios/silent-relay/hazards.json`
- [x] 5.5 Wire `rules_profile.json` loading into `ScenarioLoader` and pass `skill_abilities` to `SceneRunner` / `RulesEngine` — touches `scenario_runner.py`
- [x] 5.6 Update `AdversaryDef` model to include `ac` (replaces `defense`), ability scores, and `initiative_bonus` — touches `scenario_runner.py`

## 6. SceneRunner — 5e resolution integration

- [x] 6.1 Update `_resolve_check` to use `RulesEngine.resolve_ability_check()` with skill→ability mapping and proficiency — touches `scenario_runner.py`
- [x] 6.2 Update `_resolve_hazard` to use `resolve_ability_check()` and apply SRD conditions on failure — touches `scenario_runner.py`
- [x] 6.3 Rewrite `_resolve_combat_by_id` as a multi-round loop: roll initiative, alternate attack rolls, handle crits, cap at 5 rounds, 0 HP = scenario failure with `"defeated"` outcome — touches `scenario_runner.py`
- [x] 6.4 Implement adversary ability recharge: at start of adversary turn, roll 1d6; ability is available if roll is in its `recharge` range (e.g. Stun Pulse on 5–6) — touches `scenario_runner.py`
- [x] 6.5 Add Self-Repair Cycle (Second Wind) support: detect player intent, resolve `1d10 + level` healing, set `self_repair_used` flag — touches `scenario_runner.py`
- [x] 6.6 Update `_resolve_approach` to use new check/combat APIs — touches `scenario_runner.py`
- [x] 6.7 Update `_build_initial_state` in `ScenarioLoader` to read 5e fields from `initial_state.json` — touches `scenario_runner.py`
- [x] 6.8 Write unit tests: check with proficiency, hazard applies condition, multi-round combat, crit damage doubles dice, self-repair heals, 0 HP defeat, recharge roll for Stun Pulse — touches `tests/test_scene_runner.py`

## 7. Existing test updates

- [x] 7.1 Update `tests/test_scenario_loader.py` to use new `initial_state.json` schema and `AdversaryDef` with `ac` — touches `tests/test_scenario_loader.py`
- [x] 7.2 Update `tests/test_scene_runner.py` for new check resolution API and multi-round combat — touches `tests/test_scene_runner.py`
- [x] 7.3 Update `tests/test_scenario_integration.py` for end-to-end flow with 5e mechanics — touches `tests/test_scenario_integration.py`
- [x] 7.4 Run full test suite; fix any remaining breakage — all test files
