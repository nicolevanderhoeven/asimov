#!/usr/bin/env python
# coding: utf-8

# # Two-Player Dungeons & Dragons
#
# Each ``DialogueAgent`` stores its own conversation as a list of LangChain
# role-tagged messages (``HumanMessage`` / ``AIMessage``) from that agent's
# point of view, rather than concatenating every line into one giant user
# prompt. The role mapping is:
#
#   * messages this agent itself spoke      → ``AIMessage``
#   * messages any other party spoke        → ``HumanMessage``
#
# That makes Sigil (and any other LLM observability layer) see the actual
# per-turn player input rather than the full transcript-as-user-message.

from __future__ import annotations

import logging
from typing import Any, Callable, List

from langchain_core.messages import AIMessage, BaseMessage, HumanMessage, SystemMessage

from sigil_setup import sigil_langchain_config

logger = logging.getLogger(__name__)

MAX_HISTORY_MESSAGES = 16

# Anthropic requires the first message in a request to be from the user.
# If an agent's stored history starts with one of its own utterances (e.g.
# the storyteller's quest opener), we prepend this transient primer only
# on the wire — it is NOT stored in message_history.
_PRIMER_USER_MESSAGE = HumanMessage(content="Begin the adventure.")


class DialogueAgent:
    """A single conversational role in the two-player simulator.

    The agent tracks its own view of the conversation as a list of
    ``BaseMessage`` objects, with the agent's own utterances as
    ``AIMessage`` and everyone else's as ``HumanMessage``.
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
        self.message_history: list[BaseMessage] = []

    def _append(self, message: BaseMessage) -> None:
        """Append, merging consecutive same-role messages.

        Anthropic rejects two consecutive ``user`` or two consecutive
        ``assistant`` messages. Under the normal DM↔player flow this can't
        happen, but defending here keeps the agent robust to callers that
        inject two turns from the same speaker back-to-back.
        """
        if self.message_history and type(self.message_history[-1]) is type(message):
            previous = self.message_history[-1]
            merged_content = f"{previous.content}\n\n{message.content}"
            self.message_history[-1] = type(message)(content=merged_content)
        else:
            self.message_history.append(message)

    def _trim_history(self) -> None:
        if len(self.message_history) <= MAX_HISTORY_MESSAGES:
            return
        trimmed = self.message_history[-MAX_HISTORY_MESSAGES:]
        while trimmed and not isinstance(trimmed[0], HumanMessage):
            trimmed.pop(0)
        self.message_history = trimmed

    def _outgoing_messages(self) -> list[BaseMessage]:
        history = list(self.message_history)
        if not history or not isinstance(history[0], HumanMessage):
            history.insert(0, _PRIMER_USER_MESSAGE)
        return [self.system_message, *history]

    def send(self) -> str:
        """Stream a response from this agent's LLM and return the full text."""
        chunks: list[str] = []
        for chunk in self.model.stream(
            self._outgoing_messages(),
            config=sigil_langchain_config(component="dialogue"),
        ):
            piece = getattr(chunk, "content", None)
            if piece:
                chunks.append(piece)
        return "".join(chunks)

    def receive(self, name: str, message: str) -> None:
        """Record a message from ``name`` into this agent's history."""
        if name == self.name:
            self._append(AIMessage(content=message))
        else:
            self._append(HumanMessage(content=message))

        logger.info("%s received message: %s", self.name, message)

        before = len(self.message_history)
        self._trim_history()
        if before > len(self.message_history):
            logger.info(
                "Trimmed conversation history for %s (%d → %d) to prevent context overflow",
                self.name,
                before,
                len(self.message_history),
            )


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

    load_dotenv()  # Load .env file

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
