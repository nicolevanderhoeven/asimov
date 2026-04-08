"""Scenario loader and scene runner for bounded single-player scenarios.

Implements:
- ScenarioLoader: reads, validates, and initialises a scenario from disk
- SceneRunner: drives turn-by-turn scene progression with deterministic mechanics
- OTel instrumentation: scenario/scene/skill_check/hazard/approach spans
"""

from __future__ import annotations

import json
import logging
import os
import random
import re
import uuid
from pathlib import Path
from typing import Any, Optional

from opentelemetry import trace
from opentelemetry.trace import StatusCode
from pydantic import BaseModel, Field

from game_state import (
    DiceResult,
    GameState,
    LocationState,
    PlayerState,
    ScenarioState,
    TurnRecord,
)
from rules_engine import DiceTrigger, RulesEngine

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


class HazardDef(BaseModel):
    id: str
    name: str
    check: str
    dc: int
    fail_effect: str


class AdversaryDef(BaseModel):
    id: str
    name: str
    hp: int
    defense: int
    attack_bonus: int
    damage: str


class ClueDef(BaseModel):
    id: str
    location: str
    text: str


class LocationDef(BaseModel):
    id: str
    name: str
    tags: list[str] = []
    description: str = ""


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

        data = ScenarioData(
            meta=meta,
            scenes=scenes,
            adversaries=adversaries,
            hazards=hazards,
            clues=clues,
            locations=locations,
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
            name="Player",
            character_class="Officer",
            hp=p.get("hp", 12),
            max_hp=p.get("max_hp", 12),
            armor_class=p.get("defense", 13),
            skills=p.get("skills", {}),
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
            scene, approach
        )

        # Return approach prompt directly without LLM narration
        if not mechanic_resolved:
            return mechanic_summary, self._state

        # Auto-advance if all mechanics for the current scene are resolved
        if self._scene_complete(scene):
            if scene.end:
                outcome = self._classify_outcome()
                self._finalise_session(outcome)
            else:
                self.enter_scene(scene.next_scene)  # type: ignore[arg-type]
                # Immediately finalise if the new scene is terminal with no mechanics
                next_scene_def = self._current_scene_def()
                if next_scene_def.end and self._scene_complete(next_scene_def):
                    outcome = self._classify_outcome()
                    self._finalise_session(outcome)

        narrative = self._narrate(scene, mechanic_summary, player_input)

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

        return narrative, self._state

    # ------------------------------------------------------------------
    # Mechanic resolution
    # ------------------------------------------------------------------

    def _resolve_next_mechanic(
        self, scene: SceneDef, approach: Optional[str]
    ) -> tuple[str, GameState, bool]:
        """Resolve one pending mechanic.

        Returns (summary, updated_state, mechanic_resolved).
        mechanic_resolved=False when the return is an input prompt (no dice).
        """
        assert self._state.scenario is not None
        flags = self._state.scenario.flags

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

        # 3. Approach (scene_3_core)
        if scene.approaches and "approach" not in flags:
            if approach is None:
                available = [a.id for a in scene.approaches]
                prompt = f"Choose your approach: {', '.join(available)}"
                return prompt, self._state, False
            summary, state = self._resolve_approach(scene, approach)
            return summary, state, True

        # 4. Nothing pending — scene already resolved this turn
        return "The situation develops.", self._state, True

    def _resolve_check(
        self, scene_id: str, check: CheckDef
    ) -> tuple[str, GameState]:
        assert self._state.scenario is not None

        modifier = self._state.player.skills.get(check.skill, 0)
        trigger = DiceTrigger(roll="d20", skill=check.skill, dc=check.dc, modifier=modifier)

        with tracer.start_as_current_span("skill_check", context=self._scene_ctx) as span:
            result = self._rules.resolve(trigger)
            span.set_attribute("check.skill", check.skill)
            span.set_attribute("check.dc", check.dc)
            span.set_attribute("check.roll", result.raw_result)
            span.set_attribute("check.modifier", modifier)
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
            f"rolled {result.raw_result} + {modifier} = {result.total} — "
            f"{'SUCCESS' if passed else 'FAILURE'}"
        )
        logger.info("Resolved check: %s", summary)
        return summary, self._state

    def _resolve_hazard(self, hazard_id: str) -> tuple[str, GameState]:
        assert self._state.scenario is not None

        hazard = self._data.hazards[hazard_id]
        modifier = self._state.player.skills.get(hazard.check, 0)
        trigger = DiceTrigger(roll="d20", skill=hazard.check, dc=hazard.dc, modifier=modifier)

        with tracer.start_as_current_span("hazard", context=self._scene_ctx) as span:
            result = self._rules.resolve(trigger)
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
            f"rolled {result.raw_result} + {modifier} = {result.total} — "
            f"{'AVOIDED' if passed else f'FAILED — {hazard.fail_effect}'}"
        )
        logger.info("Resolved hazard: %s", summary)
        return summary, self._state

    def _apply_hazard_effect(self, state: GameState, effect: str) -> GameState:
        """Apply a hazard fail_effect to the player state."""
        if "damage" in effect.lower() or re.match(r"\d+d\d+", effect):
            damage = self._roll_damage(effect)
            new_hp = max(0, state.player.hp - damage)
            state = state.model_copy(
                update={"player": state.player.model_copy(update={"hp": new_hp})}
            )
        else:
            # Treat as a condition string (e.g. "confusion", "minor damage")
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

    def _roll_damage(self, damage_str: str) -> int:
        """Parse and roll a damage expression like '1d4' or '1d6+1'."""
        match = re.match(r"(\d+)d(\d+)([+-]\d+)?", damage_str.replace(" ", ""))
        if not match:
            return 1
        num, sides = int(match.group(1)), int(match.group(2))
        bonus = int(match.group(3) or 0)
        return sum(random.randint(1, sides) for _ in range(num)) + bonus

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
                # Skill check for non-combat approaches
                primary_skill = approach_def.skills[0] if approach_def.skills else "command"
                modifier = self._state.player.skills.get(primary_skill, 0)
                dc = approach_def.dc or 13
                trigger = DiceTrigger(roll="d20", skill=primary_skill, dc=dc, modifier=modifier)
                result = self._rules.resolve(trigger)
                passed = result.outcome == "success"

                if passed:
                    outcome_flag = approach_def.outcome or approach_id
                    summary = (
                        f"{approach_id.title()} approach — {primary_skill} DC {dc}: "
                        f"rolled {result.raw_result} + {modifier} = {result.total} — SUCCESS"
                    )
                    span.set_attribute("approach.outcome", outcome_flag)
                else:
                    # Escalate to force on failure
                    outcome_flag = "force"
                    combat_outcome = self._resolve_combat_by_id("adv_security_drone")
                    summary = (
                        f"{approach_id.title()} approach — {primary_skill} DC {dc}: "
                        f"rolled {result.raw_result} + {modifier} = {result.total} — FAILED, "
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

    def _resolve_combat(self, approach_def: ApproachDef) -> str:
        adversary_id = approach_def.adversaries[0] if approach_def.adversaries else "adv_security_drone"
        return self._resolve_combat_by_id(adversary_id)

    def _resolve_combat_by_id(self, adversary_id: str) -> str:
        """Resolve a single round of combat, return outcome description."""
        adversary = self._data.adversaries.get(adversary_id)
        if not adversary:
            return "combat resolved"

        max_hostiles = self._data.meta.play_profile.max_simultaneous_hostiles
        assert max_hostiles <= 2  # enforced by scenario design

        # Player attacks adversary
        player_attack = self._rules.resolve(DiceTrigger(roll="d20", dc=adversary.defense, modifier=2))
        player_hits = player_attack.outcome == "success"

        # Adversary attacks player
        adv_attack = self._rules.resolve(
            DiceTrigger(roll="d20", dc=self._state.player.armor_class, modifier=adversary.attack_bonus)
        )
        adv_hits = adv_attack.outcome == "success"

        if adv_hits:
            damage = self._roll_damage(adversary.damage)
            new_hp = max(0, self._state.player.hp - damage)
            self._state = self._state.model_copy(
                update={"player": self._state.player.model_copy(update={"hp": new_hp})}
            )

        hit_str = "hit" if player_hits else "missed"
        adv_str = f"hit for {self._roll_damage(adversary.damage)}" if adv_hits else "missed"
        return f"Player {hit_str} the drone; drone {adv_str} (player HP: {self._state.player.hp})"

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
