"""Positronic Operative — a sci-fi reflavor of the 5e SRD Fighter (Level 1).

Based on the D&D 5e System Reference Document (CC v5.2.1).
Designed for Lt. Cmdr. Data, USS Enterprise NCC-1701-D.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

from game_state import PlayerState


@dataclass(frozen=True)
class PositronicOperative:
    """5e Fighter L1, reskinned for a Starfleet android."""

    hit_die: str = "d10"
    base_hp: int = 10
    proficiency_bonus: int = 2

    saving_throw_proficiencies: tuple[str, ...] = ("STR", "CON")
    skill_proficiency_options: tuple[str, ...] = (
        "athletics", "investigation", "insight", "science", "engineering",
    )
    default_skill_proficiencies: tuple[str, ...] = ("athletics", "investigation")

    fighting_style: str = "Subroutine Focus: Defensive Protocols"
    fighting_style_ac_bonus: int = 1

    armor: dict[str, Any] = field(default_factory=lambda: {
        "name": "Starfleet Tactical Uniform",
        "type": "armor",
        "base_ac": 13,
        "max_dex_bonus": 2,
    })
    weapons: tuple[dict[str, Any], ...] = field(default_factory=lambda: (
        {"name": "Phaser", "type": "ranged_weapon", "damage": "1d8", "ability": "DEX"},
        {"name": "Positronic Strike", "type": "melee_weapon", "damage": "1d8", "ability": "STR"},
    ))

    self_repair_cycle: dict[str, str] = field(default_factory=lambda: {
        "name": "Self-Repair Cycle",
        "description": "Heal 1d10 + level HP. Usable once per scenario.",
        "heal_dice": "1d10",
    })

    def compute_ac(self, dex_modifier: int) -> int:
        """Base armor AC + DEX (capped). Fighting style bonus is already included in base_ac."""
        base = self.armor["base_ac"]
        dex_bonus = min(dex_modifier, self.armor["max_dex_bonus"])
        return base + dex_bonus


DATA_ATTRIBUTES: dict[str, int] = {
    "STR": 15,
    "DEX": 12,
    "CON": 14,
    "INT": 15,
    "WIS": 10,
    "CHA": 8,
}


def build_data_character() -> PlayerState:
    """Build Lt. Cmdr. Data as a Positronic Operative (Fighter L1)."""
    cls = PositronicOperative()
    import math
    con_mod = math.floor((DATA_ATTRIBUTES["CON"] - 10) / 2)
    dex_mod = math.floor((DATA_ATTRIBUTES["DEX"] - 10) / 2)

    return PlayerState(
        name="Data",
        character_class="Positronic Operative",
        hp=cls.base_hp + con_mod,
        max_hp=cls.base_hp + con_mod,
        armor_class=cls.compute_ac(dex_mod),
        level=1,
        proficiency_bonus=cls.proficiency_bonus,
        attributes=DATA_ATTRIBUTES.copy(),
        skill_proficiencies=list(cls.default_skill_proficiencies),
        saving_throw_proficiencies=list(cls.saving_throw_proficiencies),
        inventory=["phaser", "tricorder", "starfleet_uniform"],
        equipment=[cls.armor, *[dict(w) for w in cls.weapons]],
        class_features={
            "self_repair_cycle": dict(cls.self_repair_cycle),
            "subroutine_focus": {
                "name": cls.fighting_style,
                "description": "+1 AC when wearing armor (included in AC).",
            },
        },
        conditions=[],
    )
