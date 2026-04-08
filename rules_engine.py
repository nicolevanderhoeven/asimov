from __future__ import annotations

import copy
import random
from typing import Literal, Optional

from pydantic import BaseModel

from game_state import DiceResult, GameState

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


# ---------------------------------------------------------------------------
# RulesEngine
# ---------------------------------------------------------------------------

class RulesEngine:
    """Resolves dice triggers deterministically and applies outcomes to GameState."""

    def __init__(self, seed: Optional[int] = None) -> None:
        self._rng = random.Random(seed)

    def resolve(self, trigger: DiceTrigger) -> DiceResult:
        """Roll the die, apply modifier, classify outcome."""
        sides = DIE_SIDES[trigger.roll]
        raw = self._rng.randint(1, sides)
        total = raw + trigger.modifier

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
        )

    def apply_results(self, state: GameState, results: list[DiceResult]) -> GameState:
        """Apply dice outcomes to a copy of GameState. Only 'hit' outcomes reduce HP."""
        new_state = state.model_copy(deep=True)
        for result in results:
            if result.outcome == "hit":
                new_hp = max(0, new_state.player.hp - result.total)
                new_state.player = new_state.player.model_copy(update={"hp": new_hp})
        return new_state
