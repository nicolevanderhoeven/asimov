from __future__ import annotations

import math
import random
import re
from typing import Literal, Optional

from pydantic import BaseModel

from game_state import VALID_CONDITIONS, DiceResult, GameState, PlayerState

# ---------------------------------------------------------------------------
# DiceTrigger — declared by the LLM; resolved by the RulesEngine
# ---------------------------------------------------------------------------

DIE_SIDES: dict[str, int] = {
    "d4": 4,
    "d6": 6,
    "d8": 8,
    "d10": 10,
    "d12": 12,
    "d20": 20,
}

DieType = Literal["d4", "d6", "d8", "d10", "d12", "d20"]


class DiceTrigger(BaseModel):
    roll: DieType
    skill: Optional[str] = None
    dc: Optional[int] = None
    modifier: int = 0
    advantage: bool = False
    disadvantage: bool = False


class AttackResult(BaseModel):
    hit: bool
    natural_roll: int
    total_attack: int
    is_critical: bool
    is_fumble: bool
    damage: int
    target_ac: int


# ---------------------------------------------------------------------------
# 5e utility functions
# ---------------------------------------------------------------------------

def ability_modifier(score: int) -> int:
    """5e SRD: floor((score - 10) / 2)."""
    return math.floor((score - 10) / 2)


def conditions_impose_disadvantage(conditions: list[str]) -> bool:
    """Return True if any active condition imposes disadvantage on ability checks/attacks."""
    return bool({"frightened", "poisoned"} & set(conditions))


def conditions_grant_attack_advantage(target_conditions: list[str]) -> bool:
    """Return True if attacks against a target with these conditions have advantage."""
    return bool({"stunned", "prone"} & set(target_conditions))


def conditions_prevent_actions(conditions: list[str]) -> bool:
    """Return True if any condition prevents the creature from acting."""
    return bool({"stunned", "incapacitated"} & set(conditions))


def conditions_auto_fail_str_dex_saves(conditions: list[str]) -> bool:
    """Stunned creatures auto-fail STR and DEX saving throws."""
    return "stunned" in conditions


# ---------------------------------------------------------------------------
# RulesEngine
# ---------------------------------------------------------------------------

class RulesEngine:
    """Resolves dice triggers deterministically and applies outcomes to GameState."""

    def __init__(self, seed: Optional[int] = None) -> None:
        self._rng = random.Random(seed)

    # ---- low-level d20 roll with advantage/disadvantage ----

    def _roll_d20(
        self, advantage: bool = False, disadvantage: bool = False
    ) -> tuple[int, int]:
        """Roll a d20, respecting advantage/disadvantage.

        Returns (selected_roll, natural_roll).
        When advantage and disadvantage both apply, they cancel out.
        """
        if advantage and disadvantage:
            advantage = disadvantage = False

        roll1 = self._rng.randint(1, 20)
        if advantage or disadvantage:
            roll2 = self._rng.randint(1, 20)
            if advantage:
                selected = max(roll1, roll2)
            else:
                selected = min(roll1, roll2)
            return selected, selected
        return roll1, roll1

    # ---- legacy resolve (kept for backward compatibility) ----

    def resolve(self, trigger: DiceTrigger) -> DiceResult:
        """Roll the die, apply modifier, classify outcome."""
        sides = DIE_SIDES[trigger.roll]

        if trigger.roll == "d20":
            selected, natural = self._roll_d20(trigger.advantage, trigger.disadvantage)
            raw = selected
        else:
            raw = self._rng.randint(1, sides)
            natural = raw

        total = raw + trigger.modifier

        is_critical = (natural == 20) if trigger.roll == "d20" else False
        is_fumble = (natural == 1) if trigger.roll == "d20" else False

        if trigger.dc is not None:
            outcome: Literal["success", "failure", "hit"] = (
                "success" if total >= trigger.dc else "failure"
            )
        else:
            outcome = "hit"

        return DiceResult(
            roll=trigger.roll,
            modifier=trigger.modifier,
            raw_result=raw,
            total=total,
            dc=trigger.dc,
            outcome=outcome,
            natural_roll=natural,
            is_critical=is_critical,
            is_fumble=is_fumble,
        )

    # ---- 5e ability check ----

    def resolve_ability_check(
        self,
        skill: str,
        dc: int,
        player: PlayerState,
        skill_abilities: dict[str, str],
        advantage: bool = False,
        disadvantage: bool = False,
    ) -> DiceResult:
        """5e ability check: d20 + ability_mod + proficiency (if proficient) vs DC."""
        if conditions_impose_disadvantage(player.conditions):
            disadvantage = True

        modifier = player.skill_modifier(skill, skill_abilities)
        selected, natural = self._roll_d20(advantage, disadvantage)
        total = selected + modifier

        return DiceResult(
            roll="d20",
            modifier=modifier,
            raw_result=selected,
            total=total,
            dc=dc,
            outcome="success" if total >= dc else "failure",
            natural_roll=natural,
            is_critical=(natural == 20),
            is_fumble=(natural == 1),
        )

    # ---- 5e saving throw ----

    def resolve_saving_throw(
        self,
        ability_name: str,
        dc: int,
        player: PlayerState,
        advantage: bool = False,
        disadvantage: bool = False,
    ) -> DiceResult:
        """5e saving throw: d20 + ability_mod + proficiency (if proficient in save)."""
        ability_upper = ability_name.upper()

        if ability_upper in ("STR", "DEX") and conditions_auto_fail_str_dex_saves(player.conditions):
            return DiceResult(
                roll="d20", modifier=0, raw_result=0, total=0,
                dc=dc, outcome="failure", natural_roll=0,
                is_critical=False, is_fumble=True,
            )

        if conditions_impose_disadvantage(player.conditions):
            disadvantage = True

        mod = player.ability_modifier(ability_upper)
        if ability_upper in player.saving_throw_proficiencies:
            mod += player.proficiency_bonus

        selected, natural = self._roll_d20(advantage, disadvantage)
        total = selected + mod

        return DiceResult(
            roll="d20", modifier=mod, raw_result=selected, total=total,
            dc=dc, outcome="success" if total >= dc else "failure",
            natural_roll=natural, is_critical=(natural == 20),
            is_fumble=(natural == 1),
        )

    # ---- 5e attack roll ----

    def resolve_attack(
        self,
        attacker_bonus: int,
        target_ac: int,
        damage_str: str,
        ability_mod: int = 0,
        advantage: bool = False,
        disadvantage: bool = False,
    ) -> AttackResult:
        """5e attack roll: d20 + attacker_bonus vs target AC. Nat 20 = crit, nat 1 = miss."""
        selected, natural = self._roll_d20(advantage, disadvantage)

        is_critical = (natural == 20)
        is_fumble = (natural == 1)

        if is_fumble:
            return AttackResult(
                hit=False, natural_roll=natural, total_attack=selected + attacker_bonus,
                is_critical=False, is_fumble=True, damage=0, target_ac=target_ac,
            )

        total_attack = selected + attacker_bonus
        hit = is_critical or (total_attack >= target_ac)

        damage = 0
        if hit:
            damage = self._roll_damage(damage_str, critical=is_critical) + ability_mod

        return AttackResult(
            hit=hit, natural_roll=natural, total_attack=total_attack,
            is_critical=is_critical, is_fumble=is_fumble,
            damage=max(0, damage), target_ac=target_ac,
        )

    # ---- initiative ----

    def resolve_initiative(self, dex_modifier: int) -> int:
        """d20 + DEX modifier."""
        return self._rng.randint(1, 20) + dex_modifier

    # ---- damage rolling ----

    def _roll_damage(self, damage_str: str, critical: bool = False) -> int:
        """Parse and roll a damage expression like '1d6+2'. Crits double the dice count."""
        match = re.match(r"(\d+)d(\d+)([+-]\d+)?", damage_str.replace(" ", ""))
        if not match:
            return 1
        num, sides = int(match.group(1)), int(match.group(2))
        bonus = int(match.group(3) or 0)
        if critical:
            num *= 2
        return sum(self._rng.randint(1, sides) for _ in range(num)) + bonus

    def roll_damage(self, damage_str: str, critical: bool = False) -> int:
        """Public interface for damage rolling."""
        return self._roll_damage(damage_str, critical=critical)

    # ---- legacy apply_results ----

    def apply_results(self, state: GameState, results: list[DiceResult]) -> GameState:
        """Apply dice outcomes to a copy of GameState. Only 'hit' outcomes reduce HP."""
        new_state = state.model_copy(deep=True)
        for result in results:
            if result.outcome == "hit":
                new_hp = max(0, new_state.player.hp - result.total)
                new_state.player = new_state.player.model_copy(update={"hp": new_hp})
        return new_state
