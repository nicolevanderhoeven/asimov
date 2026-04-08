from __future__ import annotations

import copy
import logging
from typing import Any

from opentelemetry import trace
from opentelemetry.trace import StatusCode

from game_state import DiceResult, GameState, TurnRecord
from loggingfw import log_turn_event
from rules_engine import RulesEngine
from singleplayer_dnd import LLMTurnResponse, build_storyteller_prompt, invoke_storyteller

logger = logging.getLogger(__name__)
tracer = trace.get_tracer("dnd.singleplayer")

MAX_INPUT_LENGTH = 500


# ---------------------------------------------------------------------------
# State diff helper
# ---------------------------------------------------------------------------

def compute_delta(before: GameState, after: GameState) -> dict:
    """Return a flat dict of dotted-key → new-value for every changed leaf field."""
    before_flat = _flatten(before.model_dump())
    after_flat = _flatten(after.model_dump())
    delta = {}
    for key, after_val in after_flat.items():
        if before_flat.get(key) != after_val:
            delta[key] = after_val
    return delta


def _flatten(obj: Any, prefix: str = "") -> dict:
    """Recursively flatten a dict/list structure to dotted keys."""
    result: dict = {}
    if isinstance(obj, dict):
        for k, v in obj.items():
            full_key = f"{prefix}.{k}" if prefix else k
            result.update(_flatten(v, full_key))
    elif isinstance(obj, list):
        for i, v in enumerate(obj):
            result.update(_flatten(v, f"{prefix}[{i}]"))
    else:
        result[prefix] = obj
    return result


# ---------------------------------------------------------------------------
# TurnLoop
# ---------------------------------------------------------------------------

class TurnLoop:
    """Orchestrates a single player turn through all 10 ordered steps."""

    def __init__(self, state: GameState, rules_engine: RulesEngine, llm: Any) -> None:
        self.state = state
        self.rules_engine = rules_engine
        self.llm = llm

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    def validate_input(self, player_input: str) -> None:
        """Raise ValueError if input is empty or exceeds MAX_INPUT_LENGTH."""
        if not player_input or not player_input.strip():
            raise ValueError("Player input must not be empty.")
        if len(player_input) > MAX_INPUT_LENGTH:
            raise ValueError(
                f"Player input is too long ({len(player_input)} chars); "
                f"maximum is {MAX_INPUT_LENGTH}."
            )

    def run(self, player_input: str) -> tuple[str, GameState]:
        """
        Execute all 10 turn steps atomically.

        Returns (narrative, updated_GameState).
        Raises ValueError on invalid input.
        The span is set to ERROR and a turn_error log is emitted on unexpected failures.
        """
        # Step 1 — Validate input (raises ValueError; no span needed)
        self.validate_input(player_input)

        with tracer.start_as_current_span("dnd.turn") as span:
            span.set_attribute("dnd.session_id", self.state.session_id)
            span.set_attribute("dnd.turn_number", self.state.turn_number)
            span.set_attribute("dnd.player_input_length", len(player_input))

            state_before = self.state.model_copy(deep=True)

            try:
                return self._execute_turn(player_input, state_before, span)
            except Exception as exc:
                span.set_status(StatusCode.ERROR, str(exc))
                log_turn_event(
                    event="turn_error",
                    session_id=self.state.session_id,
                    turn_number=self.state.turn_number,
                    payload={
                        "player_input": player_input,
                        "error": str(exc),
                        "state_before": state_before.model_dump(),
                    },
                )
                raise

    # ------------------------------------------------------------------
    # Internal
    # ------------------------------------------------------------------

    def _execute_turn(
        self,
        player_input: str,
        state_before: GameState,
        span: Any,
    ) -> tuple[str, GameState]:
        # Step 2 — Pre-LLM rules check (currently a no-op hook; extend here for future checks)
        working_state = state_before.model_copy(deep=True)

        # Step 3 & 4 — Invoke LLM, validate response (with retry)
        system_prompt, human_message = build_storyteller_prompt(working_state, player_input)
        llm_response, llm_retried = invoke_storyteller(system_prompt, human_message, self.llm)
        span.set_attribute("dnd.llm_retried", llm_retried)

        # Step 5 — Resolve dice triggers
        dice_results: list[DiceResult] = [
            self.rules_engine.resolve(trigger)
            for trigger in llm_response.dice_triggers
        ]
        span.set_attribute("dnd.dice_roll_count", len(dice_results))

        # Step 6 — Apply state_delta + dice outcomes
        working_state = self.rules_engine.apply_results(working_state, dice_results)
        working_state = _apply_state_delta(working_state, llm_response.state_delta)

        # Compute delta before committing
        delta = compute_delta(state_before, working_state)
        span.set_attribute("dnd.state_delta_keys", ",".join(delta.keys()))

        # Step 7 — Append TurnRecord
        record = TurnRecord(
            turn_number=working_state.turn_number,
            player_input=player_input,
            dice_rolls=dice_results,
            narrative=llm_response.narrative,
            state_delta=delta,
        )
        working_state = working_state.model_copy(
            update={"turn_history": [*working_state.turn_history, record]}
        )

        # Step 8 — Increment turn counter
        working_state = working_state.model_copy(
            update={"turn_number": working_state.turn_number + 1}
        )

        # Step 9 — Commit state
        self.state = working_state

        # Step 9 (continued) — Emit OTel log event
        log_turn_event(
            event="turn_complete",
            session_id=self.state.session_id,
            turn_number=record.turn_number,
            payload={
                "player_input": player_input,
                "narrative": llm_response.narrative,
                "dice_rolls": [r.model_dump() for r in dice_results],
                "state_before": state_before.model_dump(),
                "state_after": self.state.model_dump(),
                "state_delta": delta,
            },
        )

        # Step 10 — Return narrative + new state
        return llm_response.narrative, self.state


# ---------------------------------------------------------------------------
# State delta applicator
# ---------------------------------------------------------------------------

def _apply_state_delta(state: GameState, delta: dict) -> GameState:
    """
    Apply a flat dot-notation delta dict to GameState.

    Supports top-level and one-level-deep nested fields
    (e.g. ``player.hp``, ``location.name``).
    Unrecognised keys are logged and skipped.
    """
    if not delta:
        return state

    state_dict = state.model_dump()

    for dotted_key, value in delta.items():
        parts = dotted_key.split(".", 1)
        if len(parts) == 1:
            if parts[0] in state_dict:
                state_dict[parts[0]] = value
            else:
                logger.warning("Unknown state_delta key: %s", dotted_key)
        else:
            top, nested = parts
            if top in state_dict and isinstance(state_dict[top], dict):
                state_dict[top][nested] = value
            else:
                logger.warning("Cannot apply state_delta key: %s", dotted_key)

    return GameState.model_validate(state_dict)
