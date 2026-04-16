## Context

The scenario runner resolves all mechanics in code — the LLM only narrates outcomes. The current `RulesEngine` is a thin wrapper around `d20 + flat_modifier vs DC`. It doesn't derive modifiers from ability scores, doesn't know about proficiency, and doesn't support advantage/disadvantage or critical hits. Combat is a single round with a hardcoded +2 player attack modifier.

The SRD v5.2.1 (CC) defines a clean, well-tested mechanical core. This design implements a streamlined subset of it — enough to make checks, saves, and combat feel like real 5e — while keeping the sci-fi scenario intact and the codebase small.

## Goals / Non-Goals

**Goals:**
- Implement 5e ability modifier derivation: `floor((ability_score - 10) / 2)`
- Implement proficiency bonus applied to proficient skills and saving throws
- Support advantage and disadvantage on d20 rolls
- Implement natural-20 (critical hit on attacks, auto-success on saves/checks in some tables) and natural-1 handling
- Refactor combat to support multiple rounds, initiative, proper attack rolls, and damage
- Define the Positronic Operative class (Fighter L1 reflavor) with Data's stats
- Map scenario skills to ability scores via `rules_profile.json`
- Implement the 5e conditions that appear in the scenario (frightened, poisoned, stunned, incapacitated) with their mechanical effects
- Keep all existing OTel instrumentation working

**Non-Goals:**
- Full action economy (bonus actions, reactions, opportunity attacks beyond Second Wind)
- Spellcasting, spell slots, cantrips
- Levelling, XP, or multi-level progression
- Multiple character classes
- Long rest / short rest resource management beyond Second Wind's single use

## Decisions

### 1. Ability modifiers are derived, never stored

`PlayerState.attributes` stays as `{"STR": 15, "DEX": 12, ...}`. The modifier is always computed as `floor((score - 10) / 2)`. No cached `modifiers` dict — this keeps a single source of truth and matches how 5e character sheets work.

Alternative considered: storing pre-computed modifiers — rejected because it creates drift risk and complicates state updates.

### 2. Skill-to-ability mapping lives in rules_profile.json

The scenario already has this file on disk (required but unused). We wire it in:

```json
{
  "skill_abilities": {
    "command": "CHA",
    "science": "INT",
    "engineering": "INT",
    "medical": "WIS",
    "security": "STR",
    "insight": "WIS",
    "athletics": "STR",
    "stealth": "DEX",
    "investigation": "INT"
  }
}
```

This lets different scenarios define different skill lists without changing code. The `RulesEngine` receives the mapping at construction time.

Alternative considered: hardcoding the SRD's 18 skills — rejected because the scenario uses sci-fi skill names and we want to preserve that.

### 3. Positronic Operative = Fighter L1, reflavored

Based on the SRD Fighter class, mapped to sci-fi:

| 5e Fighter | Positronic Operative |
|---|---|
| Hit Points: 10 + CON mod | Hull Integrity: 10 + CON mod = 12 |
| Armor: chain shirt (AC 13 + DEX, max 2) | Starfleet Tactical Uniform: AC 14 |
| Fighting Style: Defense (+1 AC) | Subroutine Focus: Defensive Protocols (+1 AC, included in 14) |
| Second Wind: 1d10 + level HP, 1/short rest | Self-Repair Cycle: 1d10 + 1 HP, once per scenario |
| Proficient saves: STR, CON | Same |
| Proficient skills (2): Athletics, Investigation | Athletics (STR), Investigation (INT) |
| Weapons: longsword, longbow | Phaser (ranged, 1d8), Positronic Strike (melee, 1d8 + STR) |

**Data's ability scores** (standard array, rearranged):

| Ability | Score | Modifier |
|---|---|---|
| STR | 15 | +2 |
| DEX | 12 | +1 |
| CON | 14 | +2 |
| INT | 15 | +2 |
| WIS | 10 | +0 |
| CHA | 8 | -1 |

Proficiency bonus at Level 1: **+2**

### 4. Ability check formula

Per 5e SRD:

```
d20 + ability_modifier + (proficiency_bonus if proficient in skill)
```

With advantage: roll 2d20, take higher.
With disadvantage: roll 2d20, take lower.

A check succeeds if total >= DC.

### 5. Combat uses 5e attack roll mechanics

**Attack roll**: `d20 + ability_modifier + proficiency_bonus` vs target AC.
- Natural 20: critical hit (double damage dice).
- Natural 1: automatic miss.

**Damage**: roll damage dice + ability modifier. Melee uses STR mod, ranged uses DEX mod.

**Initiative**: `d20 + DEX modifier` for each combatant. Higher goes first. Ties broken by DEX score.

**Multi-round**: combat continues until one side reaches 0 HP or the scenario defines a retreat condition. For the streamlined approach, we cap at 5 rounds (if exceeded, the surviving side wins by attrition).

**Adversary stat blocks** updated to 5e format:

```json
{
  "id": "adv_security_drone",
  "name": "Security Drone",
  "hp": 11,
  "ac": 13,
  "attack_bonus": 4,
  "damage": "1d6+2",
  "ability_scores": {"STR": 12, "DEX": 14, "CON": 12, "INT": 6, "WIS": 10, "CHA": 1},
  "initiative_bonus": 2,
  "abilities": [
    { "name": "Stun Pulse", "recharge": [5, 6], "effect": "Target must succeed on a DC 12 CON save or be stunned until end of next turn" }
  ]
}
```

### 6. Conditions have mechanical effects

Implement a subset of SRD conditions relevant to the scenario:

| Condition | Mechanical Effect |
|---|---|
| **stunned** | Auto-fail STR/DEX saves, attacks against have advantage, can't take actions |
| **frightened** | Disadvantage on ability checks and attacks while source is visible |
| **poisoned** | Disadvantage on attack rolls and ability checks |
| **incapacitated** | Can't take actions or reactions |
| **prone** | Disadvantage on attack rolls; melee attacks against have advantage, ranged have disadvantage |

Conditions are checked by `RulesEngine` before resolving rolls — e.g., if the player has `poisoned`, all their checks and attacks get disadvantage automatically.

### 7. Self-Repair Cycle (Second Wind) as a player action

The player can invoke Self-Repair Cycle once per scenario session. It heals `1d10 + level` HP (capped at max_hp). This is triggered by player input (e.g., "I activate self-repair") and resolved as a mechanic before narration, same as other mechanics.

Tracked via a flag: `self_repair_used: true` in scenario flags.

## Risks / Trade-offs

- **Backward compatibility**: Existing tests will break because modifier calculation changes. Mitigation: update all tests as part of the task list; the change is schema-level, not behavioral surprise.
- **Combat round cap**: 5-round max is artificial. Mitigation: this is a demo app, not a full VTT; the cap prevents infinite loops in adversary-heavy scenarios.
- **Skill name mapping**: Custom skill names (command, science) mean we can't directly reference SRD skill descriptions. Mitigation: the mechanical formula is identical; only the labels differ.
- **Conditions subset**: Only 5 of 15 SRD conditions are implemented. Mitigation: these are the ones relevant to the scenario's hazards and adversary abilities; more can be added later.

## Migration Plan

1. Update `game_state.py` — add proficiency fields, modifier derivation, condition types
2. Rewrite `rules_engine.py` — 5e check formula, advantage/disadvantage, attack rolls, combat loop
3. Update `scenario_runner.py` — wire new engine API into check/hazard/combat resolution
4. Update `scenarios/silent-relay/` data files — 5e stats, skill mappings, adversary blocks
5. Update all tests — adapt to new modifier calculation and combat flow
6. All changes are additive to the scenario runner path; the two-player game loop is unaffected

## Resolved Questions

- **Stun Pulse recharge**: Uses 5e recharge mechanic (recharge 5–6). At the start of the drone's turn, roll 1d6; on a 5 or 6, Stun Pulse is available again. This prevents the stun-lock problem while keeping combat tense.
- **0 HP = scenario failure**: No death saves. When the player reaches 0 HP, the scenario ends with an outcome of `"defeated"`. This keeps the flow simple and avoids tracking a mechanic that only matters in multi-character parties.
