from dotenv import load_dotenv
from flask import Flask, jsonify, request

load_dotenv()

from scripts.otel_setup import init as init_otel

init_otel()

from two_player_dnd import create_game

app = Flask(__name__)
(
    simulator,
    protagonist_name,
    storyteller_name,
    protagonist_description,
    storyteller_description,
    detailed_quest
) = create_game()


@app.route("/play", methods=["POST"])
def play():
    # silent=True: fall through to the message check below instead of
    # letting Flask's own JSON-parse-failure path raise a separate 400,
    # so every bad-input case returns the same {"error": ...} shape.
    data = request.get_json(silent=True) or {}
    message = data.get("message")
    if not isinstance(message, str) or not message.strip():
        return jsonify({"error": "'message' is required and must be a non-empty string"}), 400
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


if __name__ == "__main__":
    app.run(debug=True, port=5050)
