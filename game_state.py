from __future__ import annotations

from typing import Literal, Optional
from pydantic import BaseModel, model_validator


# ---------------------------------------------------------------------------
# Sub-models
# ---------------------------------------------------------------------------

class PlayerState(BaseModel):
    name: str
    character_class: str
    hp: int
    max_hp: int
    armor_class: int
    level: int = 1
    attributes: dict[str, int] = {
        "STR": 10, "DEX": 10, "CON": 10, "INT": 10, "WIS": 10, "CHA": 10
    }
    inventory: list[str] = []
    conditions: list[str] = []

    @model_validator(mode="after")
    def hp_within_bounds(self) -> "PlayerState":
        if self.hp > self.max_hp:
            raise ValueError(f"hp ({self.hp}) must not exceed max_hp ({self.max_hp})")
        return self


class LocationState(BaseModel):
    name: str
    description: str


class QuestState(BaseModel):
    id: str
    title: str
    status: Literal["active", "completed", "failed"]
    description: str


class NPCState(BaseModel):
    name: str
    description: str
    disposition: Literal["friendly", "neutral", "hostile"]


class DiceResult(BaseModel):
    roll: str
    modifier: int
    raw_result: int
    total: int
    dc: Optional[int] = None
    outcome: Literal["success", "failure", "hit"]


class TurnRecord(BaseModel):
    turn_number: int
    player_input: str
    dice_rolls: list[DiceResult] = []
    narrative: str
    state_delta: dict = {}


# ---------------------------------------------------------------------------
# Top-level GameState
# ---------------------------------------------------------------------------

class GameState(BaseModel):
    session_id: str
    turn_number: int = 0
    player: PlayerState
    location: LocationState
    quests: list[QuestState] = []
    npcs: list[NPCState] = []
    turn_history: list[TurnRecord] = []


# ---------------------------------------------------------------------------
# Starter character factory
# ---------------------------------------------------------------------------

def starter_character(name: str = "Aldric", character_class: str = "Fighter") -> PlayerState:
    """Return a default PlayerState for session initialisation."""
    return PlayerState(
        name=name,
        character_class=character_class,
        hp=12,
        max_hp=12,
        armor_class=14,
        level=1,
        attributes={"STR": 15, "DEX": 12, "CON": 14, "INT": 10, "WIS": 10, "CHA": 8},
        inventory=["shortsword", "shield", "torch", "10 gold pieces"],
        conditions=[],
    )


STARTER_LOCATION = LocationState(
    name="The Ruined Gate",
    description=(
        "You stand before the crumbling stone arch of an ancient keep. "
        "Torchlight flickers inside. A crow watches you from above."
    ),
)
