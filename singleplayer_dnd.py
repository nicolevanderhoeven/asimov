from __future__ import annotations

import json
import logging
from typing import Any

from pydantic import BaseModel, ValidationError

from game_state import GameState
from rules_engine import DiceTrigger
from sigil_setup import sigil_langchain_config

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# LLM response contract
# ---------------------------------------------------------------------------

class LLMTurnResponse(BaseModel):
    narrative: str
    state_delta: dict = {}
    dice_triggers: list[DiceTrigger] = []


# ---------------------------------------------------------------------------
# Prompt builder
# ---------------------------------------------------------------------------

_SYSTEM_PROMPT = """\
You are the Dungeon Master for a single-player D&D one-shot adventure.
Your job is to narrate what happens in response to the player's action.

IMPORTANT: You MUST respond with valid JSON matching this exact schema:
{{
  "narrative": "<story text describing what happens>",
  "state_delta": {{}},
  "dice_triggers": []
}}

Rules for dice_triggers:
- Include an entry whenever an action requires a dice roll (skill checks, attacks, damage).
- Each entry: {{"roll": "d20"|"d6"|"d8"|"d4"|"d10"|"d12", "skill": "<optional>", "dc": <int or omit>, "modifier": <int>}}
- For damage rolls, omit "dc". For skill/attack checks, include "dc".
- You will be told the dice results so you can incorporate them into the narrative.

Rules for state_delta:
- Only include keys that actually change. Use dot-notation for nested fields (e.g. "player.hp").
- You will NOT set HP directly; the rules engine handles HP from dice. Use state_delta for
  location changes, quest updates, NPC disposition changes, inventory additions, etc.
- Example: {{"location.name": "The Throne Room", "location.description": "A vast hall..."}}

Current game state (JSON):
{state_json}

Respond ONLY with the JSON object. No preamble, no markdown fences.
"""

_USER_TEMPLATE = """\
Player action: {player_input}
"""


def build_storyteller_prompt(state: GameState, player_input: str) -> tuple[str, str]:
    """Return (system_prompt, human_message) for the storyteller LLM."""
    state_json = state.model_dump_json(indent=2)
    system = _SYSTEM_PROMPT.format(state_json=state_json)
    human = _USER_TEMPLATE.format(player_input=player_input)
    return system, human


# ---------------------------------------------------------------------------
# LLM invocation with retry + fallback
# ---------------------------------------------------------------------------

def invoke_storyteller(
    system_prompt: str,
    human_message: str,
    llm: Any,
) -> tuple[LLMTurnResponse, bool]:
    """
    Call the LLM and parse the structured response.

    Returns (LLMTurnResponse, llm_retried).
    On double failure, returns a fallback response with the raw text as narrative
    and no state mutations.
    """
    from langchain_core.messages import HumanMessage, SystemMessage

    messages = [
        SystemMessage(content=system_prompt),
        HumanMessage(content=human_message),
    ]

    raw_text: str = ""
    retried = False

    for attempt in range(2):
        try:
            chunks: list[str] = []
            for chunk in llm.stream(
                messages,
                config=sigil_langchain_config(component="storyteller_single"),
            ):
                piece = getattr(chunk, "content", None)
                if piece:
                    chunks.append(piece)
            raw_text = "".join(chunks)
            parsed = LLMTurnResponse.model_validate_json(_extract_json(raw_text))
            return parsed, retried
        except (ValidationError, ValueError, json.JSONDecodeError) as exc:
            logger.warning("LLM response parse failed (attempt %d): %s", attempt + 1, exc)
            if attempt == 0:
                retried = True

    # Double failure — return raw text as narrative with no state changes
    logger.error("Both LLM attempts returned malformed output; falling back to raw narrative")
    fallback = LLMTurnResponse(narrative=raw_text or "The dungeon falls silent.", state_delta={}, dice_triggers=[])
    return fallback, retried


def _extract_json(text: str) -> str:
    """Strip markdown fences if present, otherwise return as-is."""
    text = text.strip()
    if text.startswith("```"):
        lines = text.splitlines()
        # Drop first and last fence lines
        inner = lines[1:-1] if lines[-1].strip() == "```" else lines[1:]
        return "\n".join(inner)
    return text
