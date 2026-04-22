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

_RULE = "─" * 60
_RULE_SHORT = "─" * 40
_ABILITY_ORDER = ("STR", "DEX", "CON", "INT", "WIS", "CHA")


def _fmt_mod(mod: int) -> str:
    """`+2` / `-1` / `+0`."""
    return f"+{mod}" if mod >= 0 else f"{mod}"


def _stat_block(state) -> str:
    conditions = ", ".join(state.player.conditions) if state.player.conditions else "none"
    scene_id = state.scenario.current_scene if state.scenario else "unknown"
    return (
        f"[HP {state.player.hp}/{state.player.max_hp} | "
        f"Scene: {scene_id} | "
        f"Conditions: {conditions}]"
    )


def _format_help() -> str:
    return "\n".join([
        _RULE_SHORT,
        "  COMMANDS",
        _RULE_SHORT,
        "  /help           Show this help",
        "  /char           Show full character sheet",
        "  /inv            Show equipment and inventory",
        "  /status         Show current scene, HP, conditions, open check",
        "  /roll [skill]   Roll the active check (skill optional)",
        "  approach <id>   Choose an approach in the current scene",
        "  quit | exit     End the session",
        "",
        "  Anything else is treated as a free-text action.",
        _RULE_SHORT,
    ])


def _format_char_sheet(state, skill_abilities: dict[str, str]) -> str:
    """Render a full D&D-5e-style sheet from PlayerState + scenario skill mapping."""
    p = state.player
    prof = p.proficiency_bonus
    lines: list[str] = [_RULE, f"  CHARACTER SHEET", _RULE]
    lines.append(f"  {p.name} — {p.character_class}  Level {p.level}")
    lines.append(
        f"  HP {p.hp}/{p.max_hp}   AC {p.armor_class}   Prof {_fmt_mod(prof)}"
    )
    conditions = ", ".join(p.conditions) if p.conditions else "none"
    lines.append(f"  Conditions: {conditions}")
    lines.append("")

    lines.append("  Abilities")
    ability_cells = []
    for ab in _ABILITY_ORDER:
        score = p.attributes.get(ab, 10)
        mod = p.ability_modifier(ab)
        ability_cells.append(f"{ab} {score:>2} ({_fmt_mod(mod)})")
    lines.append("    " + "   ".join(ability_cells[:3]))
    lines.append("    " + "   ".join(ability_cells[3:]))
    lines.append("")

    lines.append("  Saving Throws  (★ = proficient)")
    save_cells = []
    for ab in _ABILITY_ORDER:
        mod = p.ability_modifier(ab)
        is_prof = ab in p.saving_throw_proficiencies
        if is_prof:
            mod += prof
        marker = " ★" if is_prof else "  "
        save_cells.append(f"{ab} {_fmt_mod(mod)}{marker}")
    lines.append("    " + "   ".join(save_cells[:3]))
    lines.append("    " + "   ".join(save_cells[3:]))
    lines.append("")

    lines.append("  Skills  (★ = proficient)")
    if skill_abilities:
        name_width = max(len(s) for s in skill_abilities) + 2
        for skill in sorted(skill_abilities):
            ability = skill_abilities[skill].upper()
            mod = p.skill_modifier(skill, skill_abilities)
            marker = "★" if skill in p.skill_proficiencies else " "
            lines.append(
                f"    {skill:<{name_width}}{_fmt_mod(mod):>3} {marker} [{ability}]"
            )
    else:
        lines.append("    (no scenario skill mapping loaded)")
    lines.append("")

    if p.class_features:
        lines.append("  Features")
        for feat in p.class_features.values():
            if isinstance(feat, dict):
                name = feat.get("name", "?")
                desc = feat.get("description", "")
                lines.append(f"    {name} — {desc}" if desc else f"    {name}")
            else:
                lines.append(f"    {feat}")
    lines.append(_RULE)
    return "\n".join(lines)


def _format_inventory(state) -> str:
    p = state.player
    lines = [_RULE_SHORT, "  INVENTORY", _RULE_SHORT, "  Equipped"]
    if p.equipment:
        for item in p.equipment:
            if not isinstance(item, dict):
                lines.append(f"    {item}")
                continue
            name = item.get("name", "?")
            kind = item.get("type", "item")
            if kind == "armor":
                ac = item.get("base_ac")
                max_dex = item.get("max_dex_bonus")
                detail = f"armor (AC {ac}, +DEX max {max_dex})" if ac is not None else "armor"
            elif "weapon" in kind:
                dmg = item.get("damage", "?")
                ability = item.get("ability", "?")
                kind_label = "ranged" if "ranged" in kind else "melee"
                detail = f"{kind_label} ({dmg}, {ability})"
            else:
                detail = kind
            lines.append(f"    {name:<28} {detail}")
    else:
        lines.append("    (nothing equipped)")
    lines.append("")
    lines.append("  Carried")
    if p.inventory:
        lines.append("    " + ", ".join(p.inventory))
    else:
        lines.append("    (empty)")
    lines.append(_RULE_SHORT)
    return "\n".join(lines)


def _format_status(state, data, open_check) -> str:
    """Scene + HP + conditions + pending check. Pass ``runner.open_check`` as last arg."""
    lines = [_RULE_SHORT, "  STATUS", _RULE_SHORT]
    scene_id = state.scenario.current_scene if state.scenario else "unknown"
    scene = data.scenes.get(scene_id) if data and hasattr(data, "scenes") else None
    scene_name = scene.name if scene else scene_id
    lines.append(f"  Turn {state.turn_number}  |  Scene: {scene_name} ({scene_id})")
    p = state.player
    conditions = ", ".join(p.conditions) if p.conditions else "none"
    lines.append(f"  HP {p.hp}/{p.max_hp}  AC {p.armor_class}  Conditions: {conditions}")
    if scene and scene.objectives:
        lines.append(f"  Objectives: {', '.join(scene.objectives)}")
    if open_check is not None:
        ability_full = {
            "STR": "Strength", "DEX": "Dexterity", "CON": "Constitution",
            "INT": "Intelligence", "WIS": "Wisdom", "CHA": "Charisma",
        }.get(open_check.ability, open_check.ability)
        lines.append(
            f"  Open check: {ability_full} ({open_check.skill.title()}), "
            f"DC {open_check.dc} — type `/roll`"
        )
    else:
        lines.append("  Open check: none — describe your next action")
    lines.append(_RULE_SHORT)
    return "\n".join(lines)


def _handle_slash_command(
    raw: str, state, data, runner,
) -> bool:
    """If *raw* is a known slash command, print it and return True; else False."""
    cmd = raw.lower().strip()
    if cmd == "/help":
        print(_format_help())
        return True
    if cmd == "/char":
        print(_format_char_sheet(state, runner.skill_abilities))
        return True
    if cmd == "/inv":
        print(_format_inventory(state))
        return True
    if cmd == "/status":
        print(_format_status(state, data, runner.open_check))
        return True
    return False


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

    # streaming=True so Sigil tags narration generations as stream mode and
    # records time_to_first_token. The classifier call overrides this with
    # stream=False at the call site so its sync metrics stay clean.
    llm = ChatAnthropic(model="claude-sonnet-4-6", temperature=0.7, streaming=True)
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
    print("Type '/help' for all commands, or 'quit' to end the session.\n")

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

        if raw.startswith("/") and _handle_slash_command(raw, state, data, runner):
            continue

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
