#!/usr/bin/env python3
"""CLI for playing a bounded scenario (e.g. The Silent Relay) interactively.

Usage:
    python3 cli_scenario.py                  # defaults to silent-relay
    python3 cli_scenario.py silent-relay
"""
from __future__ import annotations

import sys

from dotenv import load_dotenv

load_dotenv()

from otel_setup import init as init_otel

init_otel()


def _stat_block(state) -> str:
    conditions = ", ".join(state.player.conditions) if state.player.conditions else "none"
    scene_id = state.scenario.current_scene if state.scenario else "unknown"
    return (
        f"[HP {state.player.hp}/{state.player.max_hp} | "
        f"Scene: {scene_id} | "
        f"Conditions: {conditions}]"
    )


def _print_scene_header(scene_def) -> None:
    print(f"\n{'─' * 60}")
    print(f"  {scene_def.name.upper()}")
    print(f"{'─' * 60}")
    if scene_def.entry_text:
        print(f"  {scene_def.entry_text}")
    if scene_def.objectives:
        print(f"  Objectives: {', '.join(scene_def.objectives)}")
    if scene_def.approaches:
        approach_ids = [a.id for a in scene_def.approaches]
        print(f"  Available approaches: {', '.join(approach_ids)}")
    print()


def run_scenario(scenario_name: str = "silent-relay") -> None:
    from langchain_anthropic import ChatAnthropic

    from loggingfw import log_session_event
    from rules_engine import RulesEngine
    from scenario_runner import ScenarioLoadError, ScenarioLoader, ScenarioValidationError, SceneRunner

    print(f"\n🚀 Loading scenario: {scenario_name} …")
    try:
        loader = ScenarioLoader()
        data, initial_state = loader.load(scenario_name)
    except ScenarioLoadError as exc:
        print(f"[ERROR] Could not load scenario: {exc}")
        sys.exit(1)
    except ScenarioValidationError as exc:
        print(f"[ERROR] Scenario validation failed: {exc}")
        sys.exit(1)

    llm = ChatAnthropic(model="claude-sonnet-4-6", temperature=0.7)
    runner = SceneRunner(data, initial_state, RulesEngine(), llm)

    log_session_event(
        event="scenario_start",
        session_id=initial_state.session_id,
        payload={
            "scenario": scenario_name,
            "scenario_id": data.meta.scenario_id,
        },
    )

    print(f"\n{'=' * 60}")
    print(f"  {data.meta.title.upper()}")
    print(f"  Genre: {data.meta.genre}  |  Tone: {', '.join(data.meta.tone)}")
    print(f"{'=' * 60}")

    if data.meta.prologue:
        print()
        for paragraph in data.meta.prologue.split("\n"):
            paragraph = paragraph.strip()
            if paragraph:
                print(f"  {paragraph}")
            else:
                print()

    p = initial_state.player
    print(f"\n{'─' * 60}")
    print(f"  CHARACTER: {p.name}  |  {p.character_class}  |  Level {p.level}")
    print(f"  HP: {p.hp}/{p.max_hp}  |  AC: {p.armor_class}")
    equip_names = [e.get("name", e) if isinstance(e, dict) else str(e) for e in p.equipment]
    if equip_names:
        print(f"  Equipment: {', '.join(equip_names)}")
    print(f"{'─' * 60}")

    print("\nType your actions and press Enter.")
    print("When prompted for a check, type:  /roll <skill>  or just  /roll")
    print("When prompted for an approach, type:  approach <name>")
    print("  e.g.  /roll engineering   |   approach diplomacy")
    print("Type 'quit' to end the session.\n")

    current_scene_id = None

    while not runner.is_complete:
        state = runner.state

        # Print scene header on first entry or scene change
        if state.scenario and state.scenario.current_scene != current_scene_id:
            current_scene_id = state.scenario.current_scene
            _print_scene_header(data.scenes[current_scene_id])

        print(_stat_block(state))

        if state.player.hp <= 0:
            print("\n💀 You have been incapacitated. The mission ends here.")
            break

        try:
            raw = input("You: ").strip()
        except (EOFError, KeyboardInterrupt):
            print("\n👋 Ending session.")
            break

        if raw.lower() in {"quit", "exit", "q"}:
            print("👋 Session ended.")
            break

        # Parse optional approach prefix
        approach: str | None = None
        user_input = raw
        if raw.lower().startswith("approach "):
            parts = raw.split(maxsplit=1)
            if len(parts) == 2:
                approach = parts[1].strip().lower()
                user_input = f"I choose the {approach} approach."

        if not user_input:
            continue

        try:
            narrative, _ = runner.process_turn(user_input, approach=approach)
        except ValueError as exc:
            print(f"[!] {exc}")
            continue

        if runner.last_mechanic_log:
            print(f"\n{'─' * 40}")
            for line in runner.last_mechanic_log.splitlines():
                print(f"  {line}")
            print(f"{'─' * 40}")

        print(f"\nNarrator: {narrative}\n")

    final_state = runner.state
    print(f"\n{'=' * 60}")
    if runner.is_complete:
        outcome = runner.outcome_type or "unknown"
        print(f"  MISSION COMPLETE  |  Outcome: {outcome.upper()}")
    else:
        print("  SESSION ENDED")
    print(f"  Turns taken: {final_state.turn_number}")
    print(f"{'=' * 60}\n")

    log_session_event(
        event="scenario_end",
        session_id=final_state.session_id,
        payload={
            "outcome": runner.outcome_type,
            "total_turns": final_state.turn_number,
            "final_state": final_state.model_dump(),
        },
    )


if __name__ == "__main__":
    name = sys.argv[1] if len(sys.argv) > 1 else "silent-relay"
    run_scenario(name)
