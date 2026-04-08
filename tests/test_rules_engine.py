import pytest

from game_state import GameState, STARTER_LOCATION, starter_character
from rules_engine import DiceTrigger, RulesEngine


def make_state(hp: int = 10) -> GameState:
    player = starter_character()
    player = player.model_copy(update={"hp": hp, "max_hp": max(hp, player.max_hp)})
    return GameState(session_id="test", player=player, location=STARTER_LOCATION)


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
        engine = RulesEngine(seed=0)
        # Force a known outcome by using seed + searching
        trigger = DiceTrigger(roll="d20", dc=1, modifier=0)
        result = engine.resolve(trigger)
        assert result.outcome == "success"

    def test_failure_when_total_below_dc(self):
        engine = RulesEngine(seed=0)
        trigger = DiceTrigger(roll="d20", dc=21, modifier=0)
        result = engine.resolve(trigger)
        assert result.outcome == "failure"

    def test_hit_when_no_dc(self):
        engine = RulesEngine(seed=0)
        result = engine.resolve(DiceTrigger(roll="d6"))
        assert result.outcome == "hit"

    def test_seed_reproducibility(self):
        t = DiceTrigger(roll="d20")
        results_a = [RulesEngine(seed=7).resolve(t).raw_result for _ in range(5)]
        results_b = [RulesEngine(seed=7).resolve(t).raw_result for _ in range(5)]
        assert results_a == results_b


class TestApplyResults:
    def test_hit_reduces_hp(self):
        engine = RulesEngine()
        state = make_state(hp=10)
        from game_state import DiceResult
        results = [DiceResult(roll="d6", modifier=0, raw_result=4, total=4, outcome="hit")]
        new_state = engine.apply_results(state, results)
        assert new_state.player.hp == 6

    def test_hp_floored_at_zero(self):
        engine = RulesEngine()
        state = make_state(hp=2)
        from game_state import DiceResult
        results = [DiceResult(roll="d8", modifier=0, raw_result=8, total=8, outcome="hit")]
        new_state = engine.apply_results(state, results)
        assert new_state.player.hp == 0

    def test_success_does_not_change_hp(self):
        engine = RulesEngine()
        state = make_state(hp=10)
        from game_state import DiceResult
        results = [DiceResult(roll="d20", modifier=2, raw_result=14, total=16, dc=14, outcome="success")]
        new_state = engine.apply_results(state, results)
        assert new_state.player.hp == 10

    def test_original_state_not_mutated(self):
        engine = RulesEngine()
        state = make_state(hp=10)
        from game_state import DiceResult
        results = [DiceResult(roll="d6", modifier=0, raw_result=4, total=4, outcome="hit")]
        engine.apply_results(state, results)
        assert state.player.hp == 10
