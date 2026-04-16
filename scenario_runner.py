"""Scenario loader and scene runner for bounded single-player scenarios.

Implements:
- ScenarioLoader: reads, validates, and initialises a scenario from disk
- SceneRunner: drives turn-by-turn scene progression with deterministic mechanics
- OTel instrumentation: scenario/scene/skill_check/hazard/approach spans

Uses 5e SRD rules (CC v5.2.1) for ability checks, saving throws, attacks, and combat.
"""

from __future__ import annotations

import json
import logging
import re
import uuid
from pathlib import Path
from typing import Any, Optional

from opentelemetry import trace
from opentelemetry.trace import StatusCode
from pydantic import BaseModel, Field

from game_state import (
    VALID_CONDITIONS,
    DiceResult,
    GameState,
    LocationState,
    PlayerState,
    ScenarioState,
    TurnRecord,
)
from rules_engine import (
    AttackResult,
    DiceTrigger,
    RulesEngine,
    ability_modifier,
    conditions_grant_attack_advantage,
    conditions_impose_disadvantage,
    conditions_prevent_actions,
)

logger = logging.getLogger(__name__)
tracer = trace.get_tracer("dnd.scenario")

SCENARIOS_DIR = Path(__file__).parent / "scenarios"

REQUIRED_FILES = [
    "scenario.json",
    "scenes.json",
    "adversaries.json",
    "hazards.json",
    "clues.json",
    "locations.json",
    "initial_state.json",
    "rules_profile.json",
    "npcs.json",
]

MAX_COMBAT_ROUNDS = 5


# ---------------------------------------------------------------------------
# Custom exceptions
# ---------------------------------------------------------------------------

class ScenarioLoadError(Exception):
    """Raised when required scenario files are missing."""


class ScenarioValidationError(Exception):
    """Raised when scenario data contains broken references or an incomplete scene graph."""


# ---------------------------------------------------------------------------
# Data containers (typed holders for loaded JSON)
# ---------------------------------------------------------------------------

class CheckDef(BaseModel):
    skill: str
    dc: int
    label: Optional[str] = None


class ApproachDef(BaseModel):
    id: str
    skills: list[str] = []
    dc: Optional[int] = None
    outcome: Optional[str] = None
    combat: bool = False
    adversaries: list[str] = []


class SceneDef(BaseModel):
    id: str
    name: str
    entry_text: str = ""
    objectives: list[str] = []
    obstacles: list[str] = []
    checks: list[CheckDef] = []
    approaches: list[ApproachDef] = []
    next_scene: Optional[str] = None
    end: bool = False


class AdversaryAbilityDef(BaseModel):
    name: str
    recharge: list[int] = []
    effect: str = ""


class AdversaryDef(BaseModel):
    id: str
    name: str
    hp: int
    ac: int
    attack_bonus: int
    damage: str
    ability_scores: dict[str, int] = Field(
        default_factory=lambda: {"STR": 10, "DEX": 14, "CON": 12, "INT": 6, "WIS": 10, "CHA": 1}
    )
    initiative_bonus: int = 0
    abilities: list[AdversaryAbilityDef] = []


class HazardDef(BaseModel):
    id: str
    name: str
    check: str
    dc: int
    fail_effect: str


class ClueDef(BaseModel):
    id: str
    location: str
    text: str


class LocationDef(BaseModel):
    id: str
    name: str
    tags: list[str] = []
    description: str = ""


class RulesProfile(BaseModel):
    core_die: str = "d20"
    difficulty_classes: dict[str, int] = {}
    skill_abilities: dict[str, str] = {}


class PlayProfile(BaseModel):
    player_count: int = 1
    recommended_level: int = 1
    combat_density: str = "low"
    max_simultaneous_hostiles: int = 2


class ScenarioMeta(BaseModel):
    scenario_id: str
    title: str
    version: str = "1.0"
    genre: str = "fantasy"
    tone: list[str] = []
    play_profile: PlayProfile = Field(default_factory=PlayProfile)
    entry_scene: str
    scene_order: list[str] = []


class ScenarioData(BaseModel):
    meta: ScenarioMeta
    scenes: dict[str, SceneDef]
    adversaries: dict[str, AdversaryDef]
    hazards: dict[str, HazardDef]
    clues: dict[str, ClueDef]
    locations: dict[str, LocationDef]
    rules_profile: RulesProfile = Field(default_factory=RulesProfile)


# ---------------------------------------------------------------------------
# ScenarioLoader
# ---------------------------------------------------------------------------

class ScenarioLoader:
    """Loads a scenario directory into a ScenarioData object and initial GameState."""

    def __init__(self, base_dir: Path = SCENARIOS_DIR) -> None:
        self._base_dir = base_dir

    def load(self, name: str) -> tuple[ScenarioData, GameState]:
        """Load and validate scenario *name*. Returns (ScenarioData, initial GameState).

        Raises:
            ScenarioLoadError: if required files are missing
            ScenarioValidationError: if references or scene graph are broken
        """
        scenario_dir = self._base_dir / name
        self._validate_files(scenario_dir)

        raw = self._read_all(scenario_dir)

        meta = ScenarioMeta.model_validate(raw["scenario"])
        scenes = {s["id"]: SceneDef.model_validate(s) for s in raw["scenes"]}
        adversaries = {a["id"]: AdversaryDef.model_validate(a) for a in raw["adversaries"]}
        hazards = {h["id"]: HazardDef.model_validate(h) for h in raw["hazards"]}
        clues = {c["id"]: ClueDef.model_validate(c) for c in raw["clues"]}
        locations = {loc["id"]: LocationDef.model_validate(loc) for loc in raw["locations"]}
        rules_profile = RulesProfile.model_validate(raw.get("rules_profile", {}))

        data = ScenarioData(
            meta=meta,
            scenes=scenes,
            adversaries=adversaries,
            hazards=hazards,
            clues=clues,
            locations=locations,
            rules_profile=rules_profile,
        )

        self._cross_validate(data)
        self._validate_graph(data)

        initial_state = self._build_initial_state(raw["initial_state"], meta)

        logger.info("Scenario '%s' loaded (%d scenes)", name, len(scenes))
        return data, initial_state

    # ------------------------------------------------------------------
    # Validation helpers
    # ------------------------------------------------------------------

    def _validate_files(self, scenario_dir: Path) -> None:
        missing = [f for f in REQUIRED_FILES if not (scenario_dir / f).exists()]
        if missing:
            raise ScenarioLoadError(
                f"Scenario directory '{scenario_dir}' is missing required files: {missing}"
            )

    def _read_all(self, scenario_dir: Path) -> dict:
        result = {}
        for filename in REQUIRED_FILES:
            path = scenario_dir / filename
            key = filename.removesuffix(".json")
            text = path.read_text().strip()
            result[key] = json.loads(text) if text else []
        return result

    def _cross_validate(self, data: ScenarioData) -> None:
        """Verify all entity IDs referenced in scenes exist in their data files."""
        errors: list[str] = []
        for scene in data.scenes.values():
            for adversary_id in [
                adv_id
                for approach in scene.approaches
                for adv_id in approach.adversaries
            ]:
                if adversary_id not in data.adversaries:
                    errors.append(
                        f"Scene '{scene.id}' references unknown adversary '{adversary_id}'"
                    )
            for hazard_id in scene.obstacles:
                if hazard_id not in data.hazards:
                    errors.append(
                        f"Scene '{scene.id}' references unknown hazard '{hazard_id}'"
                    )
        if errors:
            raise ScenarioValidationError(
                "Broken references in scenario:\n" + "\n".join(f"  - {e}" for e in errors)
            )

    def _validate_graph(self, data: ScenarioData) -> None:
        """Verify entry_scene exists and all non-terminal scenes have a valid next_scene."""
        errors: list[str] = []

        if data.meta.entry_scene not in data.scenes:
            errors.append(
                f"entry_scene '{data.meta.entry_scene}' not found in scenes"
            )

        for scene in data.scenes.values():
            if scene.end:
                continue
            if not scene.next_scene:
                errors.append(f"Non-terminal scene '{scene.id}' has no next_scene")
            elif scene.next_scene not in data.scenes:
                errors.append(
                    f"Scene '{scene.id}' next_scene '{scene.next_scene}' is not a known scene"
                )

        if errors:
            raise ScenarioValidationError(
                "Incomplete scene graph:\n" + "\n".join(f"  - {e}" for e in errors)
            )

    def _build_initial_state(self, initial: dict, meta: ScenarioMeta) -> GameState:
        """Construct a GameState from initial_state.json values."""
        p = initial.get("player", {})
        sc = initial.get("scenario", {})

        player = PlayerState(
            name=p.get("name", "Data"),
            character_class=p.get("character_class", "Positronic Operative"),
            hp=p.get("hp", 12),
            max_hp=p.get("max_hp", 12),
            armor_class=p.get("armor_class", p.get("defense", 14)),
            level=p.get("level", 1),
            proficiency_bonus=p.get("proficiency_bonus", 2),
            attributes=p.get("attributes", {
                "STR": 15, "DEX": 12, "CON": 14, "INT": 15, "WIS": 10, "CHA": 8,
            }),
            skill_proficiencies=p.get("skill_proficiencies", []),
            saving_throw_proficiencies=p.get("saving_throw_proficiencies", []),
            skills=p.get("skills", {}),
            inventory=p.get("inventory", []),
            equipment=p.get("equipment", []),
            class_features=p.get("class_features", {}),
            conditions=p.get("conditions", []),
        )
        scenario_state = ScenarioState(
            current_scene=meta.entry_scene,
            flags=sc.get("flags", {}),
            alarm_state=sc.get("alarm_state", "silent"),
        )
        entry_loc = meta.entry_scene.replace("_", " ").title()
        return GameState(
            session_id=str(uuid.uuid4()),
            player=player,
            location=LocationState(name=meta.title, description=entry_loc),
            scenario=scenario_state,
        )


# ---------------------------------------------------------------------------
# SceneRunner
# ---------------------------------------------------------------------------

_SCENARIO_STORYTELLER_SYSTEM = """\
You are the narrator for a Star Trek–inspired investigation scenario.
Your job is to narrate what happens next based on the mechanical outcome provided.
Be vivid but concise (3–5 sentences). Stay in genre. Do not invent mechanics.

Scene: {scene_name}
Scene context: {entry_text}
Current objectives: {objectives}

Mechanical outcome: {mechanic_summary}
"""

_SCENARIO_STORYTELLER_HUMAN = "Player action: {player_input}"


class SceneRunner:
    """Drives turn-by-turn scene progression for a loaded scenario.

    Each call to ``process_turn`` resolves one pending mechanic (hazard, skill
    check, or approach), advances state, and returns LLM narrative.

    OTel span hierarchy:
        scenario (root, spans full session)
        └── scene (one per scene)
            ├── skill_check (per check resolved)
            ├── hazard (per hazard resolved)
            └── approach (for approach resolution)
    """

    def __init__(
        self,
        data: ScenarioData,
        state: GameState,
        rules_engine: RulesEngine,
        llm: Any,
    ) -> None:
        self._data = data
        self._state = state
        self._rules = rules_engine
        self._llm = llm
        self._skill_abilities = data.rules_profile.skill_abilities

        # OTel spans — manually managed across HTTP requests
        self._scenario_span: Any = trace.INVALID_SPAN
        self._scenario_ctx: Any = None
        self._scene_span: Any = trace.INVALID_SPAN
        self._scene_ctx: Any = None

        self._is_complete = False
        self._outcome_type: Optional[str] = None

        self._start_scenario_span()
        self._start_scene_span(self._state.scenario.current_scene)  # type: ignore[union-attr]

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    @property
    def current_scene(self) -> str:
        assert self._state.scenario is not None
        return self._state.scenario.current_scene

    @property
    def is_complete(self) -> bool:
        return self._is_complete

    @property
    def outcome_type(self) -> Optional[str]:
        return self._outcome_type

    @property
    def state(self) -> GameState:
        return self._state

    def enter_scene(self, scene_id: str) -> GameState:
        """Transition to *scene_id*, emit a scene span, update state."""
        assert self._state.scenario is not None

        self._end_scene_span()
        self._state = self._state.model_copy(
            update={
                "scenario": self._state.scenario.model_copy(
                    update={"current_scene": scene_id}
                )
            }
        )
        self._start_scene_span(scene_id)
        logger.info("Entered scene '%s'", scene_id)
        return self._state

    def process_turn(
        self,
        player_input: str,
        approach: Optional[str] = None,
    ) -> tuple[str, GameState]:
        """Resolve the next pending mechanic and return (narrative, updated_state).

        Raises:
            ValueError: if the session is already complete or input is empty.
        """
        if self._is_complete:
            raise ValueError("This scenario session is already complete.")
        if not player_input or not player_input.strip():
            raise ValueError("Player input must not be empty.")

        scene = self._current_scene_def()
        mechanic_summary, self._state, mechanic_resolved = self._resolve_next_mechanic(
            scene, approach, player_input
        )

        if not mechanic_resolved:
            return mechanic_summary, self._state

        if self._state.player.hp <= 0:
            self._finalise_session("defeated")
            narrative = self._narrate(scene, mechanic_summary, player_input)
            self._append_turn(player_input, narrative)
            return narrative, self._state

        if self._scene_complete(scene):
            if scene.end:
                outcome = self._classify_outcome()
                self._finalise_session(outcome)
            else:
                self.enter_scene(scene.next_scene)  # type: ignore[arg-type]
                next_scene_def = self._current_scene_def()
                if next_scene_def.end and self._scene_complete(next_scene_def):
                    outcome = self._classify_outcome()
                    self._finalise_session(outcome)

        narrative = self._narrate(scene, mechanic_summary, player_input)
        self._append_turn(player_input, narrative)

        return narrative, self._state

    # ------------------------------------------------------------------
    # Turn record
    # ------------------------------------------------------------------

    def _append_turn(self, player_input: str, narrative: str) -> None:
        record = TurnRecord(
            turn_number=self._state.turn_number,
            player_input=player_input,
            narrative=narrative,
        )
        self._state = self._state.model_copy(
            update={
                "turn_number": self._state.turn_number + 1,
                "turn_history": [*self._state.turn_history, record],
            }
        )

    # ------------------------------------------------------------------
    # Mechanic resolution
    # ------------------------------------------------------------------

    def _resolve_next_mechanic(
        self, scene: SceneDef, approach: Optional[str], player_input: str
    ) -> tuple[str, GameState, bool]:
        """Resolve one pending mechanic.

        Returns (summary, updated_state, mechanic_resolved).
        mechanic_resolved=False when the return is an input prompt (no dice).
        """
        assert self._state.scenario is not None
        flags = self._state.scenario.flags

        # Self-Repair Cycle detection
        if self._is_self_repair_request(player_input) and "self_repair_used" not in flags:
            summary, state = self._resolve_self_repair()
            return summary, state, True

        # 1. Pending hazards
        for hazard_id in scene.obstacles:
            flag_key = f"hazard:{hazard_id}"
            if flag_key not in flags:
                summary, state = self._resolve_hazard(hazard_id)
                return summary, state, True

        # 2. Pending skill checks
        for check in scene.checks:
            flag_key = f"check:{scene.id}:{check.skill}"
            if flag_key not in flags:
                summary, state = self._resolve_check(scene.id, check)
                return summary, state, True

        # 3. Approach
        if scene.approaches and "approach" not in flags:
            if approach is None:
                available = [a.id for a in scene.approaches]
                prompt = f"Choose your approach: {', '.join(available)}"
                return prompt, self._state, False
            summary, state = self._resolve_approach(scene, approach)
            return summary, state, True

        return "The situation develops.", self._state, True

    def _resolve_check(
        self, scene_id: str, check: CheckDef
    ) -> tuple[str, GameState]:
        assert self._state.scenario is not None

        with tracer.start_as_current_span("skill_check", context=self._scene_ctx) as span:
            result = self._rules.resolve_ability_check(
                skill=check.skill,
                dc=check.dc,
                player=self._state.player,
                skill_abilities=self._skill_abilities,
            )
            span.set_attribute("check.skill", check.skill)
            span.set_attribute("check.dc", check.dc)
            span.set_attribute("check.roll", result.raw_result)
            span.set_attribute("check.modifier", result.modifier)
            span.set_attribute("check.total", result.total)
            span.set_attribute("check.passed", result.outcome == "success")

        passed = result.outcome == "success"
        flag_key = f"check:{scene_id}:{check.skill}"
        label = check.label or check.skill

        new_flags = {**self._state.scenario.flags, flag_key: "passed" if passed else "failed"}
        self._state = self._state.model_copy(
            update={
                "scenario": self._state.scenario.model_copy(update={"flags": new_flags})
            }
        )

        summary = (
            f"{label} check ({check.skill} DC {check.dc}): "
            f"rolled {result.raw_result} + {result.modifier} = {result.total} — "
            f"{'SUCCESS' if passed else 'FAILURE'}"
        )
        logger.info("Resolved check: %s", summary)
        return summary, self._state

    def _resolve_hazard(self, hazard_id: str) -> tuple[str, GameState]:
        assert self._state.scenario is not None

        hazard = self._data.hazards[hazard_id]

        with tracer.start_as_current_span("hazard", context=self._scene_ctx) as span:
            result = self._rules.resolve_ability_check(
                skill=hazard.check,
                dc=hazard.dc,
                player=self._state.player,
                skill_abilities=self._skill_abilities,
            )
            passed = result.outcome == "success"
            span.set_attribute("hazard.id", hazard_id)
            span.set_attribute("hazard.check", hazard.check)
            span.set_attribute("hazard.dc", hazard.dc)
            span.set_attribute("hazard.passed", passed)
            if not passed:
                span.set_attribute("hazard.effect", hazard.fail_effect)

        new_flags = {
            **self._state.scenario.flags,
            f"hazard:{hazard_id}": "passed" if passed else "failed",
        }
        new_state = self._state.model_copy(
            update={
                "scenario": self._state.scenario.model_copy(update={"flags": new_flags})
            }
        )

        if not passed:
            new_state = self._apply_hazard_effect(new_state, hazard.fail_effect)

        self._state = new_state

        summary = (
            f"Hazard '{hazard.name}' ({hazard.check} DC {hazard.dc}): "
            f"rolled {result.raw_result} + {result.modifier} = {result.total} — "
            f"{'AVOIDED' if passed else f'FAILED — {hazard.fail_effect}'}"
        )
        logger.info("Resolved hazard: %s", summary)
        return summary, self._state

    def _apply_hazard_effect(self, state: GameState, effect: str) -> GameState:
        """Apply a hazard fail_effect — either damage (NdM) or an SRD condition."""
        if re.match(r"\d+d\d+", effect):
            damage = self._rules.roll_damage(effect)
            new_hp = max(0, state.player.hp - damage)
            state = state.model_copy(
                update={"player": state.player.model_copy(update={"hp": new_hp})}
            )
        elif effect.lower() in VALID_CONDITIONS:
            condition = effect.lower()
            if condition not in state.player.conditions:
                state = state.model_copy(
                    update={
                        "player": state.player.model_copy(
                            update={"conditions": [*state.player.conditions, condition]}
                        )
                    }
                )
        else:
            condition = effect.lower().replace(" ", "_")
            if condition not in state.player.conditions:
                state = state.model_copy(
                    update={
                        "player": state.player.model_copy(
                            update={"conditions": [*state.player.conditions, condition]}
                        )
                    }
                )
        return state

    def _resolve_approach(
        self, scene: SceneDef, approach_id: str
    ) -> tuple[str, GameState]:
        assert self._state.scenario is not None

        approach_def = next(
            (a for a in scene.approaches if a.id == approach_id), None
        )
        if approach_def is None:
            valid = [a.id for a in scene.approaches]
            raise ValueError(
                f"Unknown approach '{approach_id}'. Valid: {valid}"
            )

        with tracer.start_as_current_span("approach", context=self._scene_ctx) as span:
            span.set_attribute("approach.id", approach_id)

            if approach_def.combat:
                outcome = self._resolve_combat(approach_def)
                summary = f"Force approach — combat with security drone: {outcome}"
                span.set_attribute("approach.outcome", "combat")
                outcome_flag = "force"
            else:
                primary_skill = approach_def.skills[0] if approach_def.skills else "command"
                dc = approach_def.dc or 13
                result = self._rules.resolve_ability_check(
                    skill=primary_skill,
                    dc=dc,
                    player=self._state.player,
                    skill_abilities=self._skill_abilities,
                )
                passed = result.outcome == "success"

                if passed:
                    outcome_flag = approach_def.outcome or approach_id
                    summary = (
                        f"{approach_id.title()} approach — {primary_skill} DC {dc}: "
                        f"rolled {result.raw_result} + {result.modifier} = {result.total} — SUCCESS"
                    )
                    span.set_attribute("approach.outcome", outcome_flag)
                else:
                    outcome_flag = "force"
                    combat_outcome = self._resolve_combat_by_id("adv_security_drone")
                    summary = (
                        f"{approach_id.title()} approach — {primary_skill} DC {dc}: "
                        f"rolled {result.raw_result} + {result.modifier} = {result.total} — FAILED, "
                        f"escalated to force: {combat_outcome}"
                    )
                    span.set_attribute("approach.outcome", "combat")

        new_flags = {
            **self._state.scenario.flags,
            "approach": approach_id,
            "core_outcome": outcome_flag,
        }
        self._state = self._state.model_copy(
            update={
                "scenario": self._state.scenario.model_copy(update={"flags": new_flags})
            }
        )
        return summary, self._state

    # ------------------------------------------------------------------
    # 5e Combat
    # ------------------------------------------------------------------

    def _resolve_combat(self, approach_def: ApproachDef) -> str:
        adversary_id = approach_def.adversaries[0] if approach_def.adversaries else "adv_security_drone"
        return self._resolve_combat_by_id(adversary_id)

    def _resolve_combat_by_id(self, adversary_id: str) -> str:
        """Multi-round 5e combat. Returns outcome description."""
        adversary = self._data.adversaries.get(adversary_id)
        if not adversary:
            return "combat resolved"

        max_hostiles = self._data.meta.play_profile.max_simultaneous_hostiles
        assert max_hostiles <= 2

        adv_hp = adversary.hp
        adv_dex_mod = ability_modifier(adversary.ability_scores.get("DEX", 10))
        player_dex_mod = self._state.player.ability_modifier("DEX")
        player_str_mod = self._state.player.ability_modifier("STR")
        player_attack_bonus = player_str_mod + self._state.player.proficiency_bonus

        player_init = self._rules.resolve_initiative(player_dex_mod)
        adv_init = self._rules.resolve_initiative(adv_dex_mod)
        player_goes_first = player_init >= adv_init

        stun_pulse_available = True
        player_stunned_this_round = False
        combat_log: list[str] = []

        for round_num in range(1, MAX_COMBAT_ROUNDS + 1):
            if player_goes_first:
                adv_hp, player_stunned_this_round = self._player_combat_turn(
                    adversary, adv_hp, player_attack_bonus, player_str_mod,
                    player_stunned_this_round, combat_log,
                )
                if adv_hp <= 0:
                    combat_log.append(f"Round {round_num}: Drone destroyed!")
                    break
                stun_pulse_available, player_stunned_this_round = self._adversary_combat_turn(
                    adversary, stun_pulse_available, player_stunned_this_round, combat_log,
                )
                if self._state.player.hp <= 0:
                    combat_log.append(f"Round {round_num}: Player defeated!")
                    break
            else:
                stun_pulse_available, player_stunned_this_round = self._adversary_combat_turn(
                    adversary, stun_pulse_available, player_stunned_this_round, combat_log,
                )
                if self._state.player.hp <= 0:
                    combat_log.append(f"Round {round_num}: Player defeated!")
                    break
                adv_hp, player_stunned_this_round = self._player_combat_turn(
                    adversary, adv_hp, player_attack_bonus, player_str_mod,
                    player_stunned_this_round, combat_log,
                )
                if adv_hp <= 0:
                    combat_log.append(f"Round {round_num}: Drone destroyed!")
                    break

        result_str = "; ".join(combat_log[-3:])
        return f"{result_str} (player HP: {self._state.player.hp})"

    def _player_combat_turn(
        self,
        adversary: AdversaryDef,
        adv_hp: int,
        player_attack_bonus: int,
        player_str_mod: int,
        player_stunned: bool,
        combat_log: list[str],
    ) -> tuple[int, bool]:
        """Player's turn. Returns (remaining_adv_hp, player_still_stunned)."""
        if conditions_prevent_actions(self._state.player.conditions):
            combat_log.append("Player is incapacitated — skips turn")
            if player_stunned:
                self._remove_condition("stunned")
                player_stunned = False
            return adv_hp, player_stunned

        disadvantage = conditions_impose_disadvantage(self._state.player.conditions)
        attack = self._rules.resolve_attack(
            attacker_bonus=player_attack_bonus,
            target_ac=adversary.ac,
            damage_str="1d8",
            ability_mod=player_str_mod,
            disadvantage=disadvantage,
        )
        if attack.hit:
            adv_hp -= attack.damage
            crit_str = " (CRITICAL HIT!)" if attack.is_critical else ""
            combat_log.append(f"Player hits for {attack.damage}{crit_str}")
        else:
            combat_log.append("Player misses")

        return adv_hp, player_stunned

    def _adversary_combat_turn(
        self,
        adversary: AdversaryDef,
        stun_pulse_available: bool,
        player_stunned: bool,
        combat_log: list[str],
    ) -> tuple[bool, bool]:
        """Adversary's turn. Returns (stun_pulse_available, player_stunned)."""
        # Recharge check for abilities
        for ability_def in adversary.abilities:
            if not stun_pulse_available and ability_def.recharge:
                recharge_roll = self._rules._rng.randint(1, 6)
                if recharge_roll in ability_def.recharge:
                    stun_pulse_available = True

        # Try Stun Pulse if available
        used_stun = False
        for ability_def in adversary.abilities:
            if stun_pulse_available and ability_def.name == "Stun Pulse":
                save_result = self._rules.resolve_saving_throw(
                    ability_name="CON", dc=12, player=self._state.player,
                )
                if save_result.outcome == "failure":
                    self._apply_condition("stunned")
                    player_stunned = True
                    combat_log.append("Drone uses Stun Pulse — player STUNNED!")
                else:
                    combat_log.append("Drone uses Stun Pulse — player resists")
                stun_pulse_available = False
                used_stun = True
                break

        if not used_stun:
            advantage = conditions_grant_attack_advantage(self._state.player.conditions)
            attack = self._rules.resolve_attack(
                attacker_bonus=adversary.attack_bonus,
                target_ac=self._state.player.armor_class,
                damage_str=adversary.damage,
                ability_mod=ability_modifier(adversary.ability_scores.get("STR", 10)),
                advantage=advantage,
            )
            if attack.hit:
                new_hp = max(0, self._state.player.hp - attack.damage)
                self._state = self._state.model_copy(
                    update={"player": self._state.player.model_copy(update={"hp": new_hp})}
                )
                crit_str = " (CRIT!)" if attack.is_critical else ""
                combat_log.append(f"Drone hits for {attack.damage}{crit_str}")
            else:
                combat_log.append("Drone misses")

        return stun_pulse_available, player_stunned

    def _apply_condition(self, condition: str) -> None:
        if condition not in self._state.player.conditions:
            self._state = self._state.model_copy(
                update={
                    "player": self._state.player.model_copy(
                        update={"conditions": [*self._state.player.conditions, condition]}
                    )
                }
            )

    def _remove_condition(self, condition: str) -> None:
        if condition in self._state.player.conditions:
            new_conditions = [c for c in self._state.player.conditions if c != condition]
            self._state = self._state.model_copy(
                update={
                    "player": self._state.player.model_copy(
                        update={"conditions": new_conditions}
                    )
                }
            )

    # ------------------------------------------------------------------
    # Self-Repair Cycle (Second Wind)
    # ------------------------------------------------------------------

    def _is_self_repair_request(self, player_input: str) -> bool:
        keywords = ("self-repair", "self_repair", "repair cycle", "second wind", "heal")
        return any(kw in player_input.lower() for kw in keywords)

    def _resolve_self_repair(self) -> tuple[str, GameState]:
        assert self._state.scenario is not None
        heal = self._rules.roll_damage("1d10") + self._state.player.level
        new_hp = min(self._state.player.max_hp, self._state.player.hp + heal)
        healed = new_hp - self._state.player.hp

        new_flags = {**self._state.scenario.flags, "self_repair_used": "true"}
        self._state = self._state.model_copy(
            update={
                "player": self._state.player.model_copy(update={"hp": new_hp}),
                "scenario": self._state.scenario.model_copy(update={"flags": new_flags}),
            }
        )

        summary = f"Self-Repair Cycle activated: healed {healed} HP (now {new_hp}/{self._state.player.max_hp})"
        logger.info(summary)
        return summary, self._state

    # ------------------------------------------------------------------
    # Scene state helpers
    # ------------------------------------------------------------------

    def _current_scene_def(self) -> SceneDef:
        return self._data.scenes[self.current_scene]

    def _scene_complete(self, scene: SceneDef) -> bool:
        """Return True when all hazards, checks, and (if applicable) approaches are resolved."""
        assert self._state.scenario is not None
        flags = self._state.scenario.flags

        for hazard_id in scene.obstacles:
            if f"hazard:{hazard_id}" not in flags:
                return False
        for check in scene.checks:
            if f"check:{scene.id}:{check.skill}" not in flags:
                return False
        if scene.approaches and "approach" not in flags:
            return False
        return True

    def _classify_outcome(self) -> str:
        """Derive overall outcome type from scenario flags."""
        assert self._state.scenario is not None
        core_outcome = self._state.scenario.flags.get("core_outcome")
        if core_outcome in ("peaceful", "contained"):
            return core_outcome
        return "force"

    # ------------------------------------------------------------------
    # Narrative generation
    # ------------------------------------------------------------------

    def _narrate(self, scene: SceneDef, mechanic_summary: str, player_input: str) -> str:
        """Call the LLM storyteller with scenario context and mechanical outcome."""
        system = _SCENARIO_STORYTELLER_SYSTEM.format(
            scene_name=scene.name,
            entry_text=scene.entry_text,
            objectives=", ".join(scene.objectives) if scene.objectives else "Resolve the situation",
            mechanic_summary=mechanic_summary,
        )
        human = _SCENARIO_STORYTELLER_HUMAN.format(player_input=player_input)

        try:
            from langchain_core.messages import HumanMessage, SystemMessage
            response = self._llm.invoke([
                SystemMessage(content=system),
                HumanMessage(content=human),
            ])
            return response.content if hasattr(response, "content") else str(response)
        except Exception as exc:
            logger.warning("Storyteller LLM failed, using fallback: %s", exc)
            return f"[{scene.name}] {mechanic_summary}"

    # ------------------------------------------------------------------
    # Session finalisation
    # ------------------------------------------------------------------

    def _finalise_session(self, outcome_type: str) -> None:
        assert self._state.scenario is not None
        self._is_complete = True
        self._outcome_type = outcome_type

        self._scenario_span.set_attribute("outcome.type", outcome_type)
        self._scenario_span.set_status(StatusCode.OK)
        self._end_scene_span()
        self._scenario_span.end()

        logger.info("Scenario session complete. Outcome: %s", outcome_type)

    # ------------------------------------------------------------------
    # OTel span management
    # ------------------------------------------------------------------

    def _start_scenario_span(self) -> None:
        assert self._state.scenario is not None
        self._scenario_span = tracer.start_span("scenario")
        self._scenario_span.set_attribute(
            "scenario.id", self._data.meta.scenario_id
        )
        self._scenario_ctx = trace.set_span_in_context(self._scenario_span)

    def _start_scene_span(self, scene_id: str) -> None:
        scene = self._data.scenes.get(scene_id)
        self._scene_span = tracer.start_span("scene", context=self._scenario_ctx)
        self._scene_span.set_attribute("scene.id", scene_id)
        if scene:
            self._scene_span.set_attribute("scene.name", scene.name)
        self._scene_ctx = trace.set_span_in_context(
            self._scene_span, self._scenario_ctx
        )

    def _end_scene_span(self) -> None:
        if self._scene_span and self._scene_span is not trace.INVALID_SPAN:
            self._scene_span.end()
