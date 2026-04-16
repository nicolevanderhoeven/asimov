from __future__ import annotations

import math
from typing import Any, Literal, Optional

from pydantic import BaseModel, Field, model_validator


# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

VALID_CONDITIONS = frozenset({
    "stunned", "frightened", "poisoned", "incapacitated", "prone",
})

ABILITY_NAMES = ("STR", "DEX", "CON", "INT", "WIS", "CHA")


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
    proficiency_bonus: int = 2
    attributes: dict[str, int] = {
        "STR": 10, "DEX": 10, "CON": 10, "INT": 10, "WIS": 10, "CHA": 10
    }
    skill_proficiencies: list[str] = []
    saving_throw_proficiencies: list[str] = []
    skills: dict[str, int] = {}
    inventory: list[str] = []
    equipment: list[dict[str, Any]] = Field(default_factory=list)
    class_features: dict[str, Any] = Field(default_factory=dict)
    conditions: list[str] = []

    @model_validator(mode="after")
    def hp_within_bounds(self) -> "PlayerState":
        if self.hp > self.max_hp:
            raise ValueError(f"hp ({self.hp}) must not exceed max_hp ({self.max_hp})")
        return self

    def ability_modifier(self, ability: str) -> int:
        """5e SRD: floor((score - 10) / 2)."""
        score = self.attributes.get(ability.upper(), 10)
        return math.floor((score - 10) / 2)

    def skill_modifier(self, skill: str, skill_abilities: dict[str, str]) -> int:
        """Ability mod for the skill's governing ability, plus proficiency if proficient."""
        ability = skill_abilities.get(skill, "INT").upper()
        mod = self.ability_modifier(ability)
        if skill in self.skill_proficiencies:
            mod += self.proficiency_bonus
        return mod


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
    natural_roll: Optional[int] = None
    is_critical: bool = False
    is_fumble: bool = False


class TurnRecord(BaseModel):
    turn_number: int
    player_input: str
    dice_rolls: list[DiceResult] = []
    narrative: str
    state_delta: dict = {}


# ---------------------------------------------------------------------------
# Top-level GameState
# ---------------------------------------------------------------------------

class ScenarioState(BaseModel):
    current_scene: str
    flags: dict[str, str] = {}
    alarm_state: str = "silent"


class GameState(BaseModel):
    session_id: str
    turn_number: int = 0
    player: PlayerState
    location: LocationState
    quests: list[QuestState] = []
    npcs: list[NPCState] = []
    turn_history: list[TurnRecord] = []
    scenario: Optional[ScenarioState] = None


# ---------------------------------------------------------------------------
# Starter character factory
# ---------------------------------------------------------------------------

def starter_character(name: str = "Data", character_class: str = "Positronic Operative") -> PlayerState:
    """Return Data's 5e-compliant PlayerState (Fighter L1, reflavored)."""
    return PlayerState(
        name=name,
        character_class=character_class,
        hp=12,
        max_hp=12,
        armor_class=14,
        level=1,
        proficiency_bonus=2,
        attributes={"STR": 15, "DEX": 12, "CON": 14, "INT": 15, "WIS": 10, "CHA": 8},
        skill_proficiencies=["athletics", "investigation"],
        saving_throw_proficiencies=["STR", "CON"],
        inventory=["phaser", "tricorder", "starfleet_uniform"],
        equipment=[
            {"name": "Phaser", "type": "ranged_weapon", "damage": "1d8", "ability": "DEX"},
            {"name": "Positronic Strike", "type": "melee_weapon", "damage": "1d8", "ability": "STR"},
            {"name": "Starfleet Tactical Uniform", "type": "armor", "base_ac": 13, "max_dex_bonus": 2},
        ],
        class_features={
            "self_repair_cycle": {
                "name": "Self-Repair Cycle",
                "description": "Heal 1d10 + level HP. Usable once per scenario.",
                "heal_dice": "1d10",
            },
            "subroutine_focus": {
                "name": "Subroutine Focus: Defensive Protocols",
                "description": "+1 AC when wearing armor (included in AC).",
            },
        },
        conditions=[],
    )


STARTER_LOCATION = LocationState(
    name="The Ruined Gate",
    description=(
        "You stand before the crumbling stone arch of an ancient keep. "
        "Torchlight flickers inside. A crow watches you from above."
    ),
)
