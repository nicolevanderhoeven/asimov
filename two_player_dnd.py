#!/usr/bin/env python
# coding: utf-8

# # Two-Player Dungeons & Dragons
#
# Each ``DialogueAgent`` tracks the conversation as a list of
# ``(speaker, content)`` tuples and, on each ``send()``, sends the LLM
# exactly two messages:
#
#   1. a ``SystemMessage`` containing the agent's base character/behaviour
#      prompt followed by a ``CONVERSATION SO FAR:`` block with the running
#      transcript baked in;
#   2. a single ``HumanMessage`` containing only the latest line spoken by
#      the other party (the line this turn is responding to).
#
# This shape is what makes Sigil's per-generation panes meaningful: the
# user input field shows only the latest player action (not the cumulative
# history), and the assistant output shows only this turn's response. The
# model still sees the full prior context — it just lives in the system
# slot instead of being scattered across alternating role messages.
#
# Trade-off: Anthropic prompt-prefix caching cannot reuse the system
# prefix across turns because the embedded transcript grows on every
# turn. We accept that cost to keep Sigil traces readable per turn.

from __future__ import annotations

import logging
from typing import Any, Callable, List, Optional

from langchain_core.messages import HumanMessage, SystemMessage

from sigil_setup import sigil_langchain_config

logger = logging.getLogger(__name__)

MAX_TRANSCRIPT_ENTRIES = 16

# Sent as the HumanMessage when the agent has no message from the other
# party to react to yet (Anthropic requires a non-empty user message).
_PRIMER_USER_TEXT = "Begin the adventure."

_TRANSCRIPT_HEADER = "CONVERSATION SO FAR:"
_TRANSCRIPT_FOOTER_TEMPLATE = (
    "Now continue the story in response to {other}'s latest action above. "
    "Stay in character as {self_name}."
)


class DialogueAgent:
    """A single conversational role in the two-player simulator.

    Internally keeps the conversation as ``(speaker_name, content)`` tuples
    so that on each turn we can render the history into the system prompt
    and send only the latest counterpart utterance as the ``HumanMessage``.
    """

    def __init__(
        self,
        name: str,
        system_message: SystemMessage,
        model: Any,
    ) -> None:
        self.name = name
        self.system_message = system_message
        self.model = model
        self.reset()

    def reset(self) -> None:
        self.transcript: list[tuple[str, str]] = []

    def _trim_transcript(self) -> None:
        if len(self.transcript) <= MAX_TRANSCRIPT_ENTRIES:
            return
        before = len(self.transcript)
        self.transcript = self.transcript[-MAX_TRANSCRIPT_ENTRIES:]
        logger.info(
            "Trimmed transcript for %s (%d → %d) to prevent context overflow",
            self.name,
            before,
            len(self.transcript),
        )

    def _split_latest_other(self) -> tuple[list[tuple[str, str]], Optional[tuple[str, str]]]:
        """Return ``(prior, latest_other)``.

        ``latest_other`` is the most recent entry whose speaker is not
        this agent; ``prior`` is the transcript with that entry removed.
        """
        for idx in range(len(self.transcript) - 1, -1, -1):
            speaker, _content = self.transcript[idx]
            if speaker != self.name:
                latest_other = self.transcript[idx]
                prior = self.transcript[:idx] + self.transcript[idx + 1 :]
                return prior, latest_other
        return list(self.transcript), None

    def _render_transcript_block(self, prior: list[tuple[str, str]]) -> str:
        if not prior:
            return ""
        lines = [f"{speaker}: {content}" for speaker, content in prior]
        # Derive the "other party" label from the full transcript, not just
        # ``prior`` — when the latest other-message is the only utterance
        # from the other party, ``prior`` contains only this agent's own
        # messages and we'd otherwise lose the counterpart's name.
        other_names = sorted(
            {s for s, _ in self.transcript if s != self.name}
        )
        other_label = ", ".join(other_names) if other_names else "the other party"
        footer = _TRANSCRIPT_FOOTER_TEMPLATE.format(
            other=other_label, self_name=self.name
        )
        return f"\n\n{_TRANSCRIPT_HEADER}\n" + "\n".join(lines) + f"\n\n{footer}"

    def _build_outgoing(self) -> list:
        prior, latest_other = self._split_latest_other()
        transcript_block = self._render_transcript_block(prior)
        system_content = f"{self.system_message.content}{transcript_block}"
        human_content = latest_other[1] if latest_other else _PRIMER_USER_TEXT
        return [
            SystemMessage(content=system_content),
            HumanMessage(content=human_content),
        ]

    def send(self) -> str:
        """Stream a response from this agent's LLM and return the full text."""
        chunks: list[str] = []
        for chunk in self.model.stream(
            self._build_outgoing(),
            config=sigil_langchain_config(component="dialogue"),
        ):
            piece = getattr(chunk, "content", None)
            if piece:
                chunks.append(piece)
        return "".join(chunks)

    def receive(self, name: str, message: str) -> None:
        """Record a message from ``name`` into this agent's transcript."""
        self.transcript.append((name, message))
        logger.info("%s received message: %s", self.name, message)
        self._trim_transcript()


class DialogueSimulator:
    """Drives a turn-by-turn dialogue across a fixed list of agents."""

    def __init__(
        self,
        agents: List[DialogueAgent],
        selection_function: Callable[[int, List[DialogueAgent]], int],
    ) -> None:
        self.agents = agents
        self._step = 0
        self.select_next_speaker = selection_function

    def reset(self) -> None:
        for agent in self.agents:
            agent.reset()

    def inject(self, name: str, message: str) -> None:
        """Initiate or continue the conversation with a ``message`` from ``name``."""
        for agent in self.agents:
            logger.info("%s: %s", agent.name, message)
            agent.receive(name, message)

        self._step += 1
        logger.info("step: %d", self._step)

    def step(self) -> tuple[str, str]:
        speaker_idx = self.select_next_speaker(self._step, self.agents)
        speaker = self.agents[speaker_idx]
        message = speaker.send()
        for receiver in self.agents:
            receiver.receive(speaker.name, message)
            logger.info("receiver: %s, message: %s", receiver.name, message)
        self._step += 1
        return speaker.name, message


def create_game():
    from langchain_anthropic import ChatAnthropic
    from dotenv import load_dotenv
    from loggingfw import CustomLogFW
    from otel_setup import init as init_otel

    load_dotenv()

    # Set up logging — service.name and instance.id match otel_setup.py so
    # logs, traces, and metrics correlate under the same resource attributes.
    import os as _os
    logFW = CustomLogFW(service_name='asimov-dnd', instance_id=_os.getenv('HOSTNAME', 'local'))
    handler = logFW.setup_logging()
    root_logger = logging.getLogger()
    root_logger.addHandler(handler)
    root_logger.setLevel(logging.INFO)
    root_logger.error("Welcome to two-player D&D, the Asimov edition!")

    init_otel()

    protagonist_name = "Data"
    storyteller_name = "Dungeon Master"
    quest = "Determine why the rest of the crew are not on the Starship Enterprise and rescue them."
    logger.info("Quest assigned: $%s", quest)
    word_limit = 50

    game_description = f"""Here is the topic for a Dungeons & Dragons game: {quest}.
            There is one player in this game: the protagonist, {protagonist_name}.
            The story is narrated by the storyteller, {storyteller_name}."""

    player_descriptor_system_message = SystemMessage(
        content="You can add detail to the description of a Dungeons & Dragons player."
    )

    protagonist_specifier_prompt = [
        player_descriptor_system_message,
        HumanMessage(
            content=f"""{game_description}
            Please reply with a creative description of the protagonist, {protagonist_name}, in {word_limit} words or less. 
            Speak directly to {protagonist_name}.
            Do not add anything else."""
        ),
    ]
    _creative_llm = ChatAnthropic(model="claude-sonnet-4-6", temperature=1.0)
    protagonist_description = _creative_llm.invoke(
        protagonist_specifier_prompt,
        config=sigil_langchain_config(component="game_setup"),
    ).content

    storyteller_specifier_prompt = [
        player_descriptor_system_message,
        HumanMessage(
            content=f"""{game_description}
            Please reply with a creative description of the storyteller, {storyteller_name}, in {word_limit} words or less. 
            Speak directly to {storyteller_name}.
            Do not add anything else."""
        ),
    ]
    storyteller_description = _creative_llm.invoke(
        storyteller_specifier_prompt,
        config=sigil_langchain_config(component="game_setup"),
    ).content

    protagonist_system_message = SystemMessage(
        content=(
            f"""{game_description}
    Never forget you are the protagonist, {protagonist_name}, and I am the storyteller, {storyteller_name}. 
    Your character description is as follows: {protagonist_description}.
    You will propose actions you plan to take and I will explain what happens when you take those actions.
    Speak in the first person from the perspective of {protagonist_name}.
    For describing your own body movements, wrap your description in '*'.
    Do not change roles!
    Do not speak from the perspective of {storyteller_name}.
    Do not forget to finish speaking by saying, 'It is your turn, {storyteller_name}.'
    Do not add anything else.
    Remember you are the protagonist, {protagonist_name}.
    Stop speaking the moment you finish speaking from your perspective.
    """
        )
    )

    storyteller_system_message = SystemMessage(
        content=(
            f"""{game_description}
    Never forget you are the storyteller, {storyteller_name}, and I am the protagonist, {protagonist_name}. 
    Your character description is as follows: {storyteller_description}.
    I will propose actions I plan to take and you will explain what happens when I take those actions.
    Speak in the first person from the perspective of {storyteller_name}.
    When you refer to me, use second person pronouns like 'you' and 'your'.
    For describing your own body movements, wrap your description in '*'.
    Do not change roles!
    Do not speak from the perspective of {protagonist_name}.
    Do not forget to finish speaking by saying, 'It is your turn, {protagonist_name}.'
    Do not add anything else.
    Remember you are the storyteller, {storyteller_name}.
    Stop speaking the moment you finish speaking from your perspective.
    """
        )
    )

    quest_specifier_prompt = [
        SystemMessage(content="You can make a task more specific."),
        HumanMessage(
            content=f"""{game_description}

            You are the storyteller, {storyteller_name}.
            Please make the quest more specific. Be creative and imaginative.
            Please reply with the specified quest in {word_limit} words or less. 
            Speak directly to the protagonist {protagonist_name}.
            Do not add anything else."""
        ),
    ]
    specified_quest = _creative_llm.invoke(
        quest_specifier_prompt,
        config=sigil_langchain_config(component="game_setup"),
    ).content

    # streaming=True so Sigil tags dialogue generations as stream mode and
    # records time_to_first_token; DialogueAgent.send() uses .stream().
    _dialogue_llm = ChatAnthropic(model="claude-sonnet-4-6", temperature=0.2, streaming=True)
    protagonist = DialogueAgent(
        name=protagonist_name,
        system_message=protagonist_system_message,
        model=_dialogue_llm,
    )
    storyteller = DialogueAgent(
        name=storyteller_name,
        system_message=storyteller_system_message,
        model=_dialogue_llm,
    )

    def select_next_speaker(step: int, agents: List[DialogueAgent]) -> int:
        # The storyteller is at index 0 and is the only LLM-driven speaker;
        # the human user supplies the protagonist's lines via inject().
        return 0

    simulator = DialogueSimulator(
        agents=[storyteller, protagonist],
        selection_function=select_next_speaker,
    )
    simulator.reset()
    print(f"\n=== GAME SETUP ===")
    print(f"Protagonist: {protagonist_name}")
    print(f"Storyteller: {storyteller_name}")
    print(f"Quest: {specified_quest}")
    print(f"\n=== STARTING GAME ===")
    simulator.inject(storyteller_name, specified_quest)

    return (
        simulator,
        protagonist_name,
        storyteller_name,
        protagonist_description,
        storyteller_description,
        specified_quest,
    )


if __name__ == "__main__":
    simulator, protagonist_name, storyteller_name, *_ = create_game()

    print(f"\n=== INSTRUCTIONS ===")
    print(f"You are playing as {protagonist_name}.")
    print(f"Describe your actions and the {storyteller_name} will respond.")
    print(f"Type 'quit' to exit the game.\n")

    while True:
        user_input = input(f"\n{protagonist_name} >>> ")
        if user_input.lower() == 'quit':
            print("Thanks for playing!")
            break

        print(f"\n[DEBUG] User input injected as {protagonist_name}: {user_input}")
        simulator.inject(protagonist_name, user_input)

        name, message = simulator.step()
        print(f"\n[DEBUG] {name} is responding")
        print(f"\n{name}: {message}")
