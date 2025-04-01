from flask import Flask, request, jsonify
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
    data = request.get_json()
    message = data.get("message")
    simulator.inject(protagonist_name, message)
    name, response = simulator.step()
    return jsonify({"speaker": name, "response": response})

@app.route("/", methods=["GET"])
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
