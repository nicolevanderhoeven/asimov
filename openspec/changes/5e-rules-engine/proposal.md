## Why

The scenario runner uses D&D-like mechanics (d20 + flat modifier vs DC), but they diverge from actual 5e SRD rules in important ways: ability scores aren't used to derive modifiers, there's no proficiency bonus, no advantage/disadvantage, no critical hits, and combat is single-round with a hardcoded +2 attack modifier. The `rules_profile.json` file is required on disk but never read at runtime.

This change replaces the homebrew mechanics with a streamlined subset of 5e SRD rules (CC v5.2.1), keeping the sci-fi skin of The Silent Relay intact. The player character becomes **Data** — a Starfleet android modelled on the 5e Fighter chassis, reflavored as a **Positronic Operative**.

## What Changes

- Rewrite `rules_engine.py` to implement proper 5e ability checks: `d20 + ability_modifier + proficiency_bonus (if proficient)` vs DC, with advantage/disadvantage and natural-20/natural-1 handling
- Update `game_state.py` so `PlayerState` derives modifiers from ability scores (`floor((score - 10) / 2)`), tracks skill proficiencies and saving throw proficiencies, and includes proficiency bonus
- Define the **Positronic Operative** class (Fighter Level 1, reflavored) with Data's stats, equipment, and class features (Second Wind → Self-Repair Cycle, Fighting Style → Subroutine Focus)
- Update `scenario_runner.py` to use proper 5e check resolution, multi-round combat with initiative, attack rolls using correct modifiers, critical hits on natural 20, and SRD conditions with mechanical effects
- Update `scenarios/silent-relay/` data files to use 5e-aligned stats (proper ability scores, skill-to-ability mappings, SRD-compatible adversary stat blocks)
- Wire `rules_profile.json` into the runtime so it actually governs skill-to-ability mappings and DC tiers

## Capabilities

### New Capabilities

- `positronic-operative`: Defines the Positronic Operative class — a sci-fi reflavor of the 5e SRD Fighter (Level 1). Includes ability scores, proficiency bonus, skill proficiencies, saving throw proficiencies, equipment, Hit Points, and class features (Self-Repair Cycle, Subroutine Focus)
- `5e-ability-checks`: Defines how ability checks, saving throws, and skill checks are resolved per 5e SRD rules — `d20 + ability_modifier + proficiency_bonus` vs DC, with advantage/disadvantage

### Modified Capabilities

- `dice-mechanics`: Extended to support advantage/disadvantage (roll 2d20, take higher/lower), natural-20 auto-success on attacks, and proper modifier derivation from ability scores
- `game-state`: `PlayerState` gains `proficiency_bonus`, `skill_proficiencies`, `saving_throw_proficiencies`; modifier calculation is derived from `attributes` rather than stored as flat values in `skills`
- `scene-progression`: Check and hazard resolution use 5e ability check formula; combat uses proper 5e attack rolls with multi-round resolution and initiative

## Impact

- `rules_engine.py`: Major rewrite — adds ability modifier calculation, proficiency, advantage/disadvantage, 5e attack resolution
- `game_state.py`: `PlayerState` updated with new fields and derived-modifier logic
- `scenario_runner.py`: Check, hazard, approach, and combat resolution updated to use new `RulesEngine` API
- `scenarios/silent-relay/initial_state.json`: Updated with Data's 5e ability scores, proficiency, and Positronic Operative class data
- `scenarios/silent-relay/rules_profile.json`: Wired into runtime; defines skill→ability mappings
- `scenarios/silent-relay/adversaries.json`: Updated with 5e-compatible stat blocks
- `scenarios/silent-relay/hazards.json`: DCs and checks updated to reference ability-backed skills
- `tests/`: All existing scenario/rules tests updated; new tests for 5e mechanics
- No new dependencies

## Non-goals

- Levelling up or XP — Data stays at Level 1
- Spellcasting or magic systems
- Full 5e combat (opportunity attacks, reactions, bonus action economy beyond Second Wind)
- Multiple character classes or multiclassing
- Fantasy re-theming — the scenario stays sci-fi
- Parsing or embedding the SRD PDF at runtime — it's a design reference only
