import os
import uuid

from dotenv import load_dotenv
from flask import Flask, abort, jsonify, request

load_dotenv()

from otel_setup import init as init_otel

init_otel()

from two_player_dnd import create_game

from game_state import GameState, starter_character, STARTER_LOCATION
from loggingfw import log_session_event
from rules_engine import RulesEngine
from scenario_runner import ScenarioLoadError, ScenarioLoader, ScenarioValidationError, SceneRunner
from turn_loop import TurnLoop

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
# In-memory session stores (ephemeral — lost on restart)
# ---------------------------------------------------------------------------
_sessions: dict[str, GameState] = {}

# Scenario sessions: session_id → SceneRunner
_scenario_runners: dict[str, SceneRunner] = {}


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


# ---------------------------------------------------------------------------
# Scenario routes
# ---------------------------------------------------------------------------

@app.route("/scenario/start", methods=["POST"])
def scenario_start():
    """Initialise a scenario session.

    Body: {"scenario": "<scenario-name>"}
    Returns: {"session_id": ..., "scene": ..., "state": ...}
    """
    data = request.get_json(silent=True) or {}
    scenario_name = data.get("scenario", "").strip()
    if not scenario_name:
        abort(400, description="'scenario' field is required.")

    try:
        loader = ScenarioLoader()
        scenario_data, initial_state = loader.load(scenario_name)
    except ScenarioLoadError as exc:
        abort(400, description=str(exc))
    except ScenarioValidationError as exc:
        abort(422, description=str(exc))

    runner = SceneRunner(
        data=scenario_data,
        state=initial_state,
        rules_engine=RulesEngine(),
        llm=_get_llm(),
    )
    session_id = initial_state.session_id
    _scenario_runners[session_id] = runner

    log_session_event(
        event="scenario_start",
        session_id=session_id,
        payload={
            "scenario": scenario_name,
            "scenario_id": scenario_data.meta.scenario_id,
            "initial_scene": runner.current_scene,
        },
    )

    scene_def = scenario_data.scenes[runner.current_scene]
    return jsonify({
        "session_id": session_id,
        "prologue": scenario_data.meta.prologue or None,
        "player": {
            "name": runner.state.player.name,
            "character_class": runner.state.player.character_class,
            "level": runner.state.player.level,
        },
        "scene": {
            "id": scene_def.id,
            "name": scene_def.name,
            "entry_text": scene_def.entry_text,
            "objectives": scene_def.objectives,
        },
        "state": runner.state.model_dump(),
    }), 201


@app.route("/scenario/play", methods=["POST"])
def scenario_play():
    """Process one player turn within a scenario session.

    Body: {"session_id": "...", "input": "...", "approach": null}
    Returns: {"narrative": ..., "scene": ..., "state": ..., "complete": bool, "outcome": null|str}
    """
    data = request.get_json(silent=True) or {}
    session_id = data.get("session_id")
    player_input = data.get("input", "")
    approach = data.get("approach")  # optional explicit approach ID

    if not session_id or session_id not in _scenario_runners:
        abort(404, description="Scenario session not found.")

    runner = _scenario_runners[session_id]

    try:
        narrative, new_state = runner.process_turn(player_input, approach=approach)
    except ValueError as exc:
        abort(400, description=str(exc))

    scene_def = runner._data.scenes[runner.current_scene]
    response = {
        "narrative": narrative,
        "mechanic_log": runner.last_mechanic_log,
        "scene": {
            "id": scene_def.id,
            "name": scene_def.name,
        },
        "state": new_state.model_dump(),
        "complete": runner.is_complete,
        "outcome": runner.outcome_type,
    }

    if runner.is_complete:
        _scenario_runners.pop(session_id, None)
        log_session_event(
            event="scenario_end",
            session_id=session_id,
            payload={
                "outcome": runner.outcome_type,
                "total_turns": new_state.turn_number,
            },
        )

    return jsonify(response)


@app.route("/scenario/info", methods=["GET"])
def scenario_info():
    """Return metadata about the scenario mode and available scenarios."""
    return jsonify({
        "description": "Single-player scenario mode — investigation-driven one-shots.",
        "endpoints": {
            "start": "POST /scenario/start",
            "play": "POST /scenario/play",
        },
        "play_body": {
            "session_id": "string (from /scenario/start)",
            "input": "string — player action",
            "approach": "string|null — explicit approach ID for multi-path scenes",
        },
    })


if __name__ == "__main__":
    app.run(debug=True, port=5050)
