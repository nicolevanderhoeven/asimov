import pytest

from game_state import DiceResult, GameState, STARTER_LOCATION, PlayerState, starter_character
from rules_engine import (
    AttackResult,
    DiceTrigger,
    RulesEngine,
    ability_modifier,
    conditions_impose_disadvantage,
    conditions_grant_attack_advantage,
    conditions_auto_fail_str_dex_saves,
    conditions_prevent_actions,
)

SKILL_ABILITIES = {
    "athletics": "STR",
    "investigation": "INT",
    "command": "CHA",
    "science": "INT",
    "engineering": "INT",
    "medical": "WIS",
}


def make_state(hp: int = 10) -> GameState:
    player = starter_character()
    player = player.model_copy(update={"hp": hp, "max_hp": max(hp, player.max_hp)})
    return GameState(session_id="test", player=player, location=STARTER_LOCATION)


# ---------------------------------------------------------------------------
# ability_modifier utility
# ---------------------------------------------------------------------------

class TestAbilityModifier:
    def test_score_10_gives_0(self):
        assert ability_modifier(10) == 0

    def test_score_15_gives_2(self):
        assert ability_modifier(15) == 2

    def test_score_8_gives_minus_1(self):
        assert ability_modifier(8) == -1

    def test_score_1_gives_minus_5(self):
        assert ability_modifier(1) == -5

    def test_score_20_gives_5(self):
        assert ability_modifier(20) == 5


# ---------------------------------------------------------------------------
# DiceTrigger
# ---------------------------------------------------------------------------

class TestDiceTrigger:
    def test_unknown_die_raises(self):
        from pydantic import ValidationError
        with pytest.raises(ValidationError):
            DiceTrigger(roll="d100")

    def test_modifier_defaults_to_zero(self):
        t = DiceTrigger(roll="d6")
        assert t.modifier == 0

    def test_dc_optional(self):
        t = DiceTrigger(roll="d6")
        assert t.dc is None

    def test_advantage_defaults_false(self):
        t = DiceTrigger(roll="d20")
        assert t.advantage is False
        assert t.disadvantage is False


# ---------------------------------------------------------------------------
# Resolve (legacy)
# ---------------------------------------------------------------------------

class TestResolve:
    def test_d20_range(self):
        engine = RulesEngine()
        for _ in range(50):
            result = engine.resolve(DiceTrigger(roll="d20"))
            assert 1 <= result.raw_result <= 20

    def test_d6_range(self):
        engine = RulesEngine()
        for _ in range(50):
            result = engine.resolve(DiceTrigger(roll="d6"))
            assert 1 <= result.raw_result <= 6

    def test_modifier_applied(self):
        engine = RulesEngine(seed=42)
        result = engine.resolve(DiceTrigger(roll="d20", modifier=3))
        assert result.total == result.raw_result + 3

    def test_success_when_total_meets_dc(self):
        trigger = DiceTrigger(roll="d20", dc=1, modifier=0)
        result = RulesEngine(seed=0).resolve(trigger)
        assert result.outcome == "success"

    def test_failure_when_total_below_dc(self):
        trigger = DiceTrigger(roll="d20", dc=21, modifier=0)
        result = RulesEngine(seed=0).resolve(trigger)
        assert result.outcome == "failure"

    def test_hit_when_no_dc(self):
        result = RulesEngine(seed=0).resolve(DiceTrigger(roll="d6"))
        assert result.outcome == "hit"

    def test_seed_reproducibility(self):
        t = DiceTrigger(roll="d20")
        results_a = [RulesEngine(seed=7).resolve(t).raw_result for _ in range(5)]
        results_b = [RulesEngine(seed=7).resolve(t).raw_result for _ in range(5)]
        assert results_a == results_b

    def test_natural_roll_populated(self):
        result = RulesEngine(seed=0).resolve(DiceTrigger(roll="d20"))
        assert result.natural_roll is not None


# ---------------------------------------------------------------------------
# Advantage / Disadvantage
# ---------------------------------------------------------------------------

class TestAdvantageDisadvantage:
    def test_advantage_picks_higher(self):
        engine = RulesEngine(seed=42)
        rolls = []
        for _ in range(100):
            r1 = engine._rng.randint(1, 20)
            r2 = engine._rng.randint(1, 20)
            rolls.append(max(r1, r2))

        engine2 = RulesEngine(seed=42)
        for expected in rolls:
            result = engine2.resolve(DiceTrigger(roll="d20", advantage=True))
            assert result.raw_result == expected

    def test_disadvantage_picks_lower(self):
        engine = RulesEngine(seed=42)
        rolls = []
        for _ in range(100):
            r1 = engine._rng.randint(1, 20)
            r2 = engine._rng.randint(1, 20)
            rolls.append(min(r1, r2))

        engine2 = RulesEngine(seed=42)
        for expected in rolls:
            result = engine2.resolve(DiceTrigger(roll="d20", disadvantage=True))
            assert result.raw_result == expected

    def test_advantage_and_disadvantage_cancel(self):
        engine_both = RulesEngine(seed=42)
        engine_neither = RulesEngine(seed=42)
        for _ in range(20):
            r_both = engine_both.resolve(DiceTrigger(roll="d20", advantage=True, disadvantage=True))
            r_neither = engine_neither.resolve(DiceTrigger(roll="d20"))
            assert r_both.raw_result == r_neither.raw_result


# ---------------------------------------------------------------------------
# 5e Ability Check
# ---------------------------------------------------------------------------

class TestResolveAbilityCheck:
    def test_proficient_skill_uses_proficiency(self):
        player = starter_character()
        engine = RulesEngine(seed=42)
        result = engine.resolve_ability_check("athletics", dc=10, player=player, skill_abilities=SKILL_ABILITIES)
        assert result.modifier == 4  # STR +2 + proficiency +2

    def test_non_proficient_skill_no_proficiency(self):
        player = starter_character()
        engine = RulesEngine(seed=42)
        result = engine.resolve_ability_check("command", dc=10, player=player, skill_abilities=SKILL_ABILITIES)
        assert result.modifier == -1  # CHA -1

    def test_poisoned_imposes_disadvantage(self):
        player = starter_character().model_copy(update={"conditions": ["poisoned"]})
        engine1 = RulesEngine(seed=42)
        result = engine1.resolve_ability_check("athletics", dc=10, player=player, skill_abilities=SKILL_ABILITIES)
        # With disadvantage, should consume 2 random rolls
        engine2 = RulesEngine(seed=42)
        r1 = engine2._rng.randint(1, 20)
        r2 = engine2._rng.randint(1, 20)
        expected = min(r1, r2)
        assert result.raw_result == expected


# ---------------------------------------------------------------------------
# 5e Saving Throw
# ---------------------------------------------------------------------------

class TestResolveSavingThrow:
    def test_proficient_save_adds_proficiency(self):
        player = starter_character()
        engine = RulesEngine(seed=42)
        result = engine.resolve_saving_throw("STR", dc=10, player=player)
        assert result.modifier == 4  # STR +2 + proficiency +2

    def test_non_proficient_save_no_proficiency(self):
        player = starter_character()
        engine = RulesEngine(seed=42)
        result = engine.resolve_saving_throw("WIS", dc=10, player=player)
        assert result.modifier == 0  # WIS +0, no proficiency

    def test_stunned_auto_fails_str_save(self):
        player = starter_character().model_copy(update={"conditions": ["stunned"]})
        engine = RulesEngine(seed=42)
        result = engine.resolve_saving_throw("STR", dc=5, player=player)
        assert result.outcome == "failure"

    def test_stunned_auto_fails_dex_save(self):
        player = starter_character().model_copy(update={"conditions": ["stunned"]})
        engine = RulesEngine(seed=42)
        result = engine.resolve_saving_throw("DEX", dc=5, player=player)
        assert result.outcome == "failure"

    def test_stunned_does_not_auto_fail_con_save(self):
        player = starter_character().model_copy(update={"conditions": ["stunned"]})
        engine = RulesEngine(seed=42)
        result = engine.resolve_saving_throw("CON", dc=1, player=player)
        assert result.outcome == "success"


# ---------------------------------------------------------------------------
# 5e Attack Roll
# ---------------------------------------------------------------------------

class TestResolveAttack:
    def test_hit_on_meeting_ac(self):
        engine = RulesEngine(seed=42)
        result = engine.resolve_attack(attacker_bonus=20, target_ac=1, damage_str="1d6", ability_mod=2)
        assert result.hit is True
        assert result.damage > 0

    def test_miss_on_low_roll(self):
        engine = RulesEngine(seed=42)
        result = engine.resolve_attack(attacker_bonus=0, target_ac=30, damage_str="1d6", ability_mod=0)
        if not result.is_critical:
            assert result.hit is False
            assert result.damage == 0

    def test_nat_1_always_misses(self):
        for seed in range(1000):
            engine = RulesEngine(seed=seed)
            result = engine.resolve_attack(attacker_bonus=100, target_ac=1, damage_str="1d6")
            if result.natural_roll == 1:
                assert result.hit is False
                assert result.is_fumble is True
                break

    def test_nat_20_always_hits_and_crits(self):
        for seed in range(1000):
            engine = RulesEngine(seed=seed)
            result = engine.resolve_attack(attacker_bonus=0, target_ac=30, damage_str="1d6")
            if result.natural_roll == 20:
                assert result.hit is True
                assert result.is_critical is True
                break

    def test_crit_doubles_damage_dice(self):
        for seed in range(1000):
            engine = RulesEngine(seed=seed)
            result = engine.resolve_attack(attacker_bonus=0, target_ac=30, damage_str="1d6", ability_mod=0)
            if result.is_critical:
                assert result.damage >= 2  # 2d6 minimum is 2
                break


# ---------------------------------------------------------------------------
# Initiative
# ---------------------------------------------------------------------------

class TestInitiative:
    def test_initiative_in_range(self):
        engine = RulesEngine()
        for _ in range(50):
            init = engine.resolve_initiative(dex_modifier=2)
            assert 3 <= init <= 22  # 1+2 to 20+2


# ---------------------------------------------------------------------------
# Conditions helpers
# ---------------------------------------------------------------------------

class TestConditionHelpers:
    def test_frightened_imposes_disadvantage(self):
        assert conditions_impose_disadvantage(["frightened"]) is True

    def test_poisoned_imposes_disadvantage(self):
        assert conditions_impose_disadvantage(["poisoned"]) is True

    def test_stunned_does_not_impose_disadvantage_directly(self):
        assert conditions_impose_disadvantage(["stunned"]) is False

    def test_stunned_grants_attack_advantage(self):
        assert conditions_grant_attack_advantage(["stunned"]) is True

    def test_prone_grants_attack_advantage(self):
        assert conditions_grant_attack_advantage(["prone"]) is True

    def test_stunned_auto_fails_str_dex(self):
        assert conditions_auto_fail_str_dex_saves(["stunned"]) is True

    def test_stunned_prevents_actions(self):
        assert conditions_prevent_actions(["stunned"]) is True

    def test_incapacitated_prevents_actions(self):
        assert conditions_prevent_actions(["incapacitated"]) is True

    def test_no_conditions_no_effects(self):
        assert conditions_impose_disadvantage([]) is False
        assert conditions_grant_attack_advantage([]) is False
        assert conditions_prevent_actions([]) is False


# ---------------------------------------------------------------------------
# Apply results (legacy)
# ---------------------------------------------------------------------------

class TestApplyResults:
    def test_hit_reduces_hp(self):
        engine = RulesEngine()
        state = make_state(hp=10)
        results = [DiceResult(roll="d6", modifier=0, raw_result=4, total=4, outcome="hit")]
        new_state = engine.apply_results(state, results)
        assert new_state.player.hp == 6

    def test_hp_floored_at_zero(self):
        engine = RulesEngine()
        state = make_state(hp=2)
        results = [DiceResult(roll="d8", modifier=0, raw_result=8, total=8, outcome="hit")]
        new_state = engine.apply_results(state, results)
        assert new_state.player.hp == 0

    def test_success_does_not_change_hp(self):
        engine = RulesEngine()
        state = make_state(hp=10)
        results = [DiceResult(roll="d20", modifier=2, raw_result=14, total=16, dc=14, outcome="success")]
        new_state = engine.apply_results(state, results)
        assert new_state.player.hp == 10

    def test_original_state_not_mutated(self):
        engine = RulesEngine()
        state = make_state(hp=10)
        results = [DiceResult(roll="d6", modifier=0, raw_result=4, total=4, outcome="hit")]
        engine.apply_results(state, results)
        assert state.player.hp == 10
