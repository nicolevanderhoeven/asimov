#!/usr/bin/env python3
"""Single-player D&D CLI — mirrors cli_play.py for the two-player mode."""
from __future__ import annotations

import os
import uuid

from dotenv import load_dotenv

load_dotenv()


def _stat_block(state) -> str:
    """Return a concise one-line stat summary to display before each prompt."""
    from game_state import GameState
    active_quests = [q.title for q in state.quests if q.status == "active"]
    quest_str = ", ".join(active_quests) if active_quests else "none"
    return (
        f"[HP {state.player.hp}/{state.player.max_hp} | "
        f"AC {state.player.armor_class} | "
        f"Location: {state.location.name} | "
        f"Quests: {quest_str}]"
    )


def run_singleplayer():
    from langchain_anthropic import ChatAnthropic

    from game_state import GameState, starter_character, STARTER_LOCATION
    from loggingfw import log_session_event
    from rules_engine import RulesEngine
    from turn_loop import TurnLoop

    session_id = str(uuid.uuid4())
    player = starter_character()
    state = GameState(
        session_id=session_id,
        player=player,
        location=STARTER_LOCATION,
    )

    llm = ChatAnthropic(model="claude-sonnet-4-6", temperature=0.7)
    engine = RulesEngine()
    loop = TurnLoop(state, engine, llm)

    log_session_event(
        event="session_start",
        session_id=session_id,
        payload={"initial_state": state.model_dump()},
    )

    print("\n🎲 Single-Player D&D One-Shot")
    print(f"  Character: {player.name} the {player.character_class}")
    print(f"  Starting at: {STARTER_LOCATION.name}")
    print(f"  {STARTER_LOCATION.description}")
    print("\nType your actions and press Enter. Type 'quit' to end the session.\n")

    while True:
        state = loop.state
        print(_stat_block(state))

        if state.player.hp <= 0:
            print("\n💀 Your character has fallen. The adventure ends here.")
            break

        try:
            user_input = input("You: ").strip()
        except (EOFError, KeyboardInterrupt):
            print("\n👋 Ending game.")
            break

        if user_input.lower() in {"quit", "exit", "q"}:
            print("👋 Farewell, adventurer.")
            break

        try:
            narrative, new_state = loop.run(user_input)
        except ValueError as exc:
            print(f"[!] {exc}")
            continue

        print(f"\nDungeon Master: {narrative}\n")

    final_state = loop.state
    log_session_event(
        event="session_end",
        session_id=session_id,
        payload={
            "total_turns": final_state.turn_number,
            "final_state": final_state.model_dump(),
        },
    )
    print(f"\nSession ended after {final_state.turn_number} turns.")


if __name__ == "__main__":
    run_singleplayer()
