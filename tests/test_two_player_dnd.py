"""Unit tests for the refactored DialogueAgent / DialogueSimulator.

These tests pin the new role-based message contract so that Sigil records
genuine per-turn user input rather than the full transcript stitched into
one giant ``HumanMessage``.
"""

from unittest.mock import MagicMock

import pytest
from langchain_core.messages import AIMessage, HumanMessage, SystemMessage

from two_player_dnd import (
    MAX_HISTORY_MESSAGES,
    DialogueAgent,
    DialogueSimulator,
)


def make_streaming_llm(*responses: str) -> MagicMock:
    """Return a mock LLM whose ``.stream(...)`` yields the given responses in order.

    Mirrors ``mock_llm_with_responses`` in ``test_singleplayer_dnd.py`` so
    Sigil's TTFT path (which depends on ``.stream``) is exercised.
    """
    llm = MagicMock()
    iters = []
    for text in responses:
        chunk = MagicMock()
        chunk.content = text
        iters.append(iter([chunk]))
    llm.stream.side_effect = iters
    return llm


def make_agent(name: str = "Dungeon Master", llm: MagicMock | None = None) -> DialogueAgent:
    return DialogueAgent(
        name=name,
        system_message=SystemMessage(content=f"You are {name}."),
        model=llm or MagicMock(),
    )


class TestReceiveRoleMapping:
    def test_own_name_recorded_as_ai_message(self):
        agent = make_agent("Dungeon Master")
        agent.receive("Dungeon Master", "You stand before a rift.")
        assert len(agent.message_history) == 1
        msg = agent.message_history[0]
        assert isinstance(msg, AIMessage)
        assert msg.content == "You stand before a rift."

    def test_other_name_recorded_as_human_message(self):
        agent = make_agent("Dungeon Master")
        agent.receive("Data", "I scan the rift.")
        assert len(agent.message_history) == 1
        msg = agent.message_history[0]
        assert isinstance(msg, HumanMessage)
        assert msg.content == "I scan the rift."

    def test_no_speaker_prefix_leaks_into_content(self):
        """Stored content must NOT include the legacy ``"Name: "`` prefix.

        The whole point of this refactor is that role metadata carries the
        speaker identity, so the literal ``"Dungeon Master: ..."`` strings
        the old code wrote into a single user message must not reappear.
        """
        agent = make_agent("Dungeon Master")
        agent.receive("Data", "I scan the rift.")
        agent.receive("Dungeon Master", "The rift pulses.")
        for msg in agent.message_history:
            assert not msg.content.startswith("Data:")
            assert not msg.content.startswith("Dungeon Master:")


class TestConsecutiveSameRoleMerging:
    def test_two_other_messages_merge_into_one_human(self):
        agent = make_agent("Dungeon Master")
        agent.receive("Data", "I scan the rift.")
        agent.receive("Data", "I draw my phaser.")
        assert len(agent.message_history) == 1
        merged = agent.message_history[0]
        assert isinstance(merged, HumanMessage)
        assert "I scan the rift." in merged.content
        assert "I draw my phaser." in merged.content

    def test_two_own_messages_merge_into_one_ai(self):
        agent = make_agent("Dungeon Master")
        agent.receive("Dungeon Master", "The rift opens.")
        agent.receive("Dungeon Master", "Energy crackles.")
        assert len(agent.message_history) == 1
        merged = agent.message_history[0]
        assert isinstance(merged, AIMessage)

    def test_alternating_messages_not_merged(self):
        agent = make_agent("Dungeon Master")
        agent.receive("Dungeon Master", "The rift opens.")
        agent.receive("Data", "I scan it.")
        agent.receive("Dungeon Master", "It pulses brighter.")
        assert len(agent.message_history) == 3
        assert isinstance(agent.message_history[0], AIMessage)
        assert isinstance(agent.message_history[1], HumanMessage)
        assert isinstance(agent.message_history[2], AIMessage)


class TestSendOutgoingShape:
    def _outgoing_from_call(self, llm: MagicMock) -> list:
        args, _kwargs = llm.stream.call_args
        return args[0]

    def test_send_uses_stream_not_invoke(self):
        """Streaming preserves Sigil's TTFT histogram path."""
        llm = make_streaming_llm("Some narration.")
        agent = make_agent("Dungeon Master", llm)
        agent.receive("Data", "I scan the rift.")
        agent.send()
        assert llm.stream.call_count == 1
        llm.invoke.assert_not_called()

    def test_send_returns_concatenated_chunks(self):
        llm = MagicMock()
        chunks = []
        for piece in ["Hello ", "world."]:
            c = MagicMock()
            c.content = piece
            chunks.append(c)
        llm.stream.side_effect = lambda *_a, **_kw: iter(chunks)
        agent = make_agent("Dungeon Master", llm)
        agent.receive("Data", "Greet me.")
        result = agent.send()
        assert result == "Hello world."

    def test_outgoing_starts_with_system_message(self):
        llm = make_streaming_llm("ok")
        agent = make_agent("Dungeon Master", llm)
        agent.receive("Data", "I scan the rift.")
        agent.send()
        outgoing = self._outgoing_from_call(llm)
        assert isinstance(outgoing[0], SystemMessage)

    def test_primer_prepended_when_history_starts_with_ai(self):
        """Anthropic requires the first message after system to be user.

        If the storyteller's history starts with its own quest opener
        (``AIMessage``), ``send()`` must inject a transient user primer.
        """
        llm = make_streaming_llm("ok")
        agent = make_agent("Dungeon Master", llm)
        agent.receive("Dungeon Master", "You stand on the bridge.")
        agent.send()
        outgoing = self._outgoing_from_call(llm)
        # outgoing = [SystemMessage, HumanMessage(primer), AIMessage(quest)]
        assert isinstance(outgoing[0], SystemMessage)
        assert isinstance(outgoing[1], HumanMessage)
        assert isinstance(outgoing[2], AIMessage)
        assert outgoing[2].content == "You stand on the bridge."

    def test_primer_prepended_when_history_empty(self):
        llm = make_streaming_llm("ok")
        agent = make_agent("Dungeon Master", llm)
        agent.send()
        outgoing = self._outgoing_from_call(llm)
        assert isinstance(outgoing[0], SystemMessage)
        assert isinstance(outgoing[1], HumanMessage)
        assert len(outgoing) == 2

    def test_no_primer_when_history_starts_with_human(self):
        llm = make_streaming_llm("ok")
        agent = make_agent("Dungeon Master", llm)
        agent.receive("Data", "I scan the rift.")
        agent.send()
        outgoing = self._outgoing_from_call(llm)
        # outgoing = [SystemMessage, HumanMessage(Data's line)]
        assert isinstance(outgoing[0], SystemMessage)
        assert isinstance(outgoing[1], HumanMessage)
        assert outgoing[1].content == "I scan the rift."
        assert len(outgoing) == 2

    def test_primer_is_not_stored_in_history(self):
        llm = make_streaming_llm("ok")
        agent = make_agent("Dungeon Master", llm)
        agent.receive("Dungeon Master", "You stand on the bridge.")
        agent.send()
        assert len(agent.message_history) == 1
        assert isinstance(agent.message_history[0], AIMessage)


class TestTrimming:
    def test_history_trimmed_to_max(self):
        agent = make_agent("Dungeon Master")
        # Alternate roles so nothing merges.
        for i in range(MAX_HISTORY_MESSAGES + 4):
            sender = "Data" if i % 2 == 0 else "Dungeon Master"
            agent.receive(sender, f"line {i}")
        assert len(agent.message_history) <= MAX_HISTORY_MESSAGES

    def test_trim_preserves_user_first(self):
        """After trimming, history must still start with a HumanMessage so
        the next ``send()`` call doesn't need to drop a primer mid-conversation."""
        agent = make_agent("Dungeon Master")
        for i in range(MAX_HISTORY_MESSAGES + 6):
            sender = "Dungeon Master" if i % 2 == 0 else "Data"
            agent.receive(sender, f"line {i}")
        assert agent.message_history, "history should not be empty after trim"
        assert isinstance(agent.message_history[0], HumanMessage)


class TestDialogueSimulatorFlow:
    def _build(self, dm_responses: list[str]) -> tuple[DialogueSimulator, DialogueAgent, DialogueAgent, MagicMock]:
        llm = make_streaming_llm(*dm_responses)
        storyteller = DialogueAgent(
            name="Dungeon Master",
            system_message=SystemMessage(content="You are the DM."),
            model=llm,
        )
        protagonist = DialogueAgent(
            name="Data",
            system_message=SystemMessage(content="You are Data."),
            model=MagicMock(),  # never .send()-ed in this flow
        )
        simulator = DialogueSimulator(
            agents=[storyteller, protagonist],
            selection_function=lambda step, agents: 0,
        )
        return simulator, storyteller, protagonist, llm

    def test_inject_quest_then_player_then_step(self):
        simulator, storyteller, protagonist, llm = self._build([
            "Excellent initiative, Data! The corridor glows. It is your turn, Data."
        ])

        simulator.inject("Dungeon Master", "You awaken on the Enterprise.")
        simulator.inject("Data", "I scan the rift.")

        # Storyteller view: AI(quest), H(Data line)
        assert [type(m) for m in storyteller.message_history] == [AIMessage, HumanMessage]
        # Protagonist view: H(quest), AI(Data line)
        assert [type(m) for m in protagonist.message_history] == [HumanMessage, AIMessage]

        speaker, message = simulator.step()
        assert speaker == "Dungeon Master"
        assert "It is your turn" in message

        # After step, both agents see the new DM line in their own POV.
        assert [type(m) for m in storyteller.message_history] == [AIMessage, HumanMessage, AIMessage]
        assert [type(m) for m in protagonist.message_history] == [HumanMessage, AIMessage, HumanMessage]

        # Wire payload to Anthropic on the DM step: [System, primer Human, AI(quest), Human(Data)].
        args, _kwargs = llm.stream.call_args
        outgoing = args[0]
        assert isinstance(outgoing[0], SystemMessage)
        assert isinstance(outgoing[1], HumanMessage)  # primer
        assert isinstance(outgoing[2], AIMessage)     # quest
        assert isinstance(outgoing[3], HumanMessage)  # Data's actual input
        assert outgoing[3].content == "I scan the rift."

    def test_no_transcript_blob_in_outgoing_user_message(self):
        """Regression test for the original Sigil bug.

        The user-role payload that hits the LLM must NOT contain phrases
        like ``"Here is the conversation so far."`` or the literal
        ``"Dungeon Master: ..."`` transcript that the old implementation
        produced.
        """
        simulator, _storyteller, _protagonist, llm = self._build([
            "You step forward. It is your turn, Data."
        ])
        simulator.inject("Dungeon Master", "You awaken on the Enterprise.")
        simulator.inject("Data", "I choose the Roman corridor!")
        simulator.step()

        args, _kwargs = llm.stream.call_args
        outgoing = args[0]
        for msg in outgoing:
            assert "Here is the conversation so far" not in msg.content
            assert "Dungeon Master: " not in msg.content
            assert "Data: " not in msg.content
