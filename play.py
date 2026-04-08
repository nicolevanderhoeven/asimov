import os
import uuid

from dotenv import load_dotenv
from flask import Flask, abort, jsonify, request
from two_player_dnd import create_game

from game_state import GameState, starter_character, STARTER_LOCATION
from loggingfw import log_session_event
from rules_engine import RulesEngine
from turn_loop import TurnLoop

load_dotenv()

app = Flask(__name__)
(
    simulator,
    protagonist_name,
    storyteller_name,
    protagonist_description,
    storyteller_description,
    detailed_quest
) = create_game()

# ---------------------------------------------------------------------------
# In-memory single-player session store  (ephemeral — lost on restart)
# ---------------------------------------------------------------------------
_sessions: dict[str, GameState] = {}


def _get_llm():
    """Lazy-initialise the LangChain LLM used by the single-player storyteller."""
    from langchain_anthropic import ChatAnthropic
    return ChatAnthropic(model="claude-sonnet-4-6", temperature=0.7)


# ---------------------------------------------------------------------------
# Existing two-player routes (unchanged)
# ---------------------------------------------------------------------------

@app.route("/play", methods=["POST"])
def play():
    data = request.get_json()
    message = data.get("message")
    simulator.inject(protagonist_name, message)
    name, response = simulator.step()
    return jsonify({"speaker": name, "response": response})


@app.route("/", methods=["GET"])
def home():
    return jsonify({
        "protagonist": {
            "name": protagonist_name,
            "description": protagonist_description
        },
        "storyteller": {
            "name": storyteller_name,
            "description": storyteller_description
        },
        "quest": detailed_quest
    })


# ---------------------------------------------------------------------------
# Single-player routes
# ---------------------------------------------------------------------------

@app.route("/singleplayer", methods=["GET"])
def singleplayer_info():
    """Return metadata about the single-player mode and starter character schema."""
    return jsonify({
        "name": "Single-Player D&D One-Shot",
        "description": (
            "A single-player D&D one-shot adventure with structured game state, "
            "dice mechanics, and full OTel observability."
        ),
        "starter_character_schema": starter_character().model_json_schema(),
        "endpoints": {
            "start": "POST /singleplayer/start",
            "play": "POST /singleplayer/play",
            "end": "POST /singleplayer/end",
        },
    })


@app.route("/singleplayer/start", methods=["POST"])
def singleplayer_start():
    """Initialise a new game session and return the session_id + initial state."""
    session_id = str(uuid.uuid4())
    player = starter_character()
    state = GameState(
        session_id=session_id,
        player=player,
        location=STARTER_LOCATION,
    )
    _sessions[session_id] = state

    log_session_event(
        event="session_start",
        session_id=session_id,
        payload={"initial_state": state.model_dump()},
    )

    return jsonify({
        "session_id": session_id,
        "state": state.model_dump(),
    }), 201


@app.route("/singleplayer/play", methods=["POST"])
def singleplayer_play():
    """Process one player turn and return narrative + updated state."""
    data = request.get_json(silent=True) or {}
    session_id = data.get("session_id")
    player_input = data.get("input", "")

    if not session_id or session_id not in _sessions:
        abort(404, description="Session not found.")

    state = _sessions[session_id]
    loop = TurnLoop(state, RulesEngine(), _get_llm())

    try:
        narrative, new_state = loop.run(player_input)
    except ValueError as exc:
        abort(400, description=str(exc))

    _sessions[session_id] = new_state
    last_record = new_state.turn_history[-1] if new_state.turn_history else None

    return jsonify({
        "narrative": narrative,
        "state": new_state.model_dump(),
        "dice_rolls": [r.model_dump() for r in (last_record.dice_rolls if last_record else [])],
    })


@app.route("/singleplayer/end", methods=["POST"])
def singleplayer_end():
    """End a session, emit the session_end event, and clear the in-memory state."""
    data = request.get_json(silent=True) or {}
    session_id = data.get("session_id")

    if not session_id or session_id not in _sessions:
        abort(404, description="Session not found.")

    state = _sessions.pop(session_id)

    log_session_event(
        event="session_end",
        session_id=session_id,
        payload={
            "total_turns": state.turn_number,
            "final_state": state.model_dump(),
        },
    )

    return jsonify({"ended": True, "total_turns": state.turn_number})


if __name__ == "__main__":
    app.run(debug=True, port=5050)
