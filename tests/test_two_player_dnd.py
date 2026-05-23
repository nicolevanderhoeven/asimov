"""Unit tests for DialogueAgent / DialogueSimulator.

The dialogue agent sends exactly two messages to the LLM per turn:

1. A ``SystemMessage`` containing the agent's base character/behaviour
   prompt with the running transcript appended under
   ``CONVERSATION SO FAR:``.
2. A single ``HumanMessage`` containing only the latest line spoken by
   the other party — the line this turn is responding to.

This shape is what makes Sigil's per-generation panes meaningful: the
user input field shows only the latest player action, never the
cumulative transcript or any DM narration.
"""

from unittest.mock import MagicMock

import pytest
from langchain_core.messages import HumanMessage, SystemMessage

from two_player_dnd import (
    MAX_TRANSCRIPT_ENTRIES,
    DialogueAgent,
    DialogueSimulator,
)


def make_streaming_llm(*responses: str) -> MagicMock:
    """Return a mock LLM whose ``.stream(...)`` yields the given responses in order.

    Mirrors ``mock_llm_with_responses`` in ``test_singleplayer_dnd.py`` so
    the Sigil TTFT path (which depends on ``.stream``) stays exercised.
    """
    llm = MagicMock()
    iters = []
    for text in responses:
        chunk = MagicMock()
        chunk.content = text
        iters.append(iter([chunk]))
    llm.stream.side_effect = iters
    return llm


def make_agent(
    name: str = "Dungeon Master",
    base_system: str = "You are the Dungeon Master.",
    llm: MagicMock | None = None,
) -> DialogueAgent:
    return DialogueAgent(
        name=name,
        system_message=SystemMessage(content=base_system),
        model=llm or MagicMock(),
    )


def outgoing(llm: MagicMock) -> list:
    args, _kwargs = llm.stream.call_args
    return args[0]


class TestReceiveRecordsTranscript:
    def test_receive_appends_tuple(self):
        agent = make_agent("Dungeon Master")
        agent.receive("Data", "I scan the rift.")
        agent.receive("Dungeon Master", "The rift pulses.")
        assert agent.transcript == [
            ("Data", "I scan the rift."),
            ("Dungeon Master", "The rift pulses."),
        ]

    def test_repeated_same_speaker_kept_as_separate_entries(self):
        agent = make_agent("Dungeon Master")
        agent.receive("Data", "I scan the rift.")
        agent.receive("Data", "I scan it again.")
        assert len(agent.transcript) == 2
        assert all(s == "Data" for s, _ in agent.transcript)


class TestSendOutgoingShape:
    def test_send_uses_stream_not_invoke(self):
        """Streaming preserves Sigil's TTFT histogram path."""
        llm = make_streaming_llm("Some narration.")
        agent = make_agent("Dungeon Master", llm=llm)
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
        agent = make_agent("Dungeon Master", llm=llm)
        agent.receive("Data", "Greet me.")
        assert agent.send() == "Hello world."

    def test_outgoing_is_exactly_system_then_human(self):
        llm = make_streaming_llm("ok")
        agent = make_agent("Dungeon Master", llm=llm)
        agent.receive("Data", "I scan the rift.")
        agent.send()
        msgs = outgoing(llm)
        assert len(msgs) == 2
        assert isinstance(msgs[0], SystemMessage)
        assert isinstance(msgs[1], HumanMessage)


class TestHumanMessageIsLatestPlayerTurnOnly:
    """Core regression for the Sigil 'user input includes narration' bug."""

    def test_human_message_is_just_latest_other_message(self):
        llm = make_streaming_llm("ok")
        agent = make_agent("Dungeon Master", llm=llm)
        agent.receive("Dungeon Master", "You awaken on the Enterprise.")
        agent.receive("Data", "I scan the rift.")
        agent.receive("Dungeon Master", "The rift pulses.")
        agent.receive("Data", "I draw my phaser.")
        agent.send()
        human = outgoing(llm)[1]
        assert human.content == "I draw my phaser."

    def test_human_message_does_not_contain_dm_narration(self):
        llm = make_streaming_llm("ok")
        agent = make_agent("Dungeon Master", llm=llm)
        agent.receive("Dungeon Master", "You awaken alone on the Enterprise.")
        agent.receive("Data", "I scan the rift.")
        agent.receive("Dungeon Master", "The rift pulses with bluish-green energy.")
        agent.receive("Data", "I choose the Roman corridor!")
        agent.send()
        human_content = outgoing(llm)[1].content
        assert "You awaken" not in human_content
        assert "rift pulses" not in human_content
        assert "Dungeon Master:" not in human_content
        assert "Here is the conversation so far" not in human_content

    def test_human_message_does_not_include_prior_player_inputs(self):
        """Cumulative-history bug: Sigil should not see a stack of identical
        player lines from prior turns mashed into one HumanMessage."""
        llm = make_streaming_llm("ok")
        agent = make_agent("Dungeon Master", llm=llm)
        for _ in range(4):
            agent.receive("Data", "I do an internal scan of my brain.")
            agent.receive("Dungeon Master", "Your scan reveals...")
        agent.receive("Data", "I open the encrypted file.")
        agent.send()
        human_content = outgoing(llm)[1].content
        assert human_content == "I open the encrypted file."
        assert "internal scan of my brain" not in human_content


class TestSystemMessageEmbedsHistory:
    def test_base_system_prompt_preserved(self):
        llm = make_streaming_llm("ok")
        agent = make_agent(
            "Dungeon Master",
            base_system="You are the cosmic Dungeon Master.",
            llm=llm,
        )
        agent.receive("Data", "I scan the rift.")
        agent.send()
        system_content = outgoing(llm)[0].content
        assert "You are the cosmic Dungeon Master." in system_content

    def test_prior_transcript_lines_appear_in_system(self):
        llm = make_streaming_llm("ok")
        agent = make_agent("Dungeon Master", llm=llm)
        agent.receive("Dungeon Master", "You awaken on the Enterprise.")
        agent.receive("Data", "I scan the rift.")
        agent.receive("Dungeon Master", "The rift pulses with bluish-green energy.")
        agent.receive("Data", "I draw my phaser.")
        agent.send()
        system_content = outgoing(llm)[0].content
        assert "CONVERSATION SO FAR:" in system_content
        assert "Dungeon Master: You awaken on the Enterprise." in system_content
        assert "Data: I scan the rift." in system_content
        assert "Dungeon Master: The rift pulses with bluish-green energy." in system_content

    def test_latest_other_message_is_not_duplicated_in_system_block(self):
        """The latest player line goes only in the HumanMessage; it must
        not also appear inside the transcript block in the system prompt
        (otherwise the model sees it twice)."""
        llm = make_streaming_llm("ok")
        agent = make_agent("Dungeon Master", llm=llm)
        agent.receive("Dungeon Master", "You stand on the bridge.")
        agent.receive("Data", "I look around.")
        agent.send()
        msgs = outgoing(llm)
        system_block = msgs[0].content.split("CONVERSATION SO FAR:", 1)[1]
        assert "I look around" not in system_block
        assert msgs[1].content == "I look around."

    def test_system_includes_continue_instruction(self):
        llm = make_streaming_llm("ok")
        agent = make_agent("Dungeon Master", llm=llm)
        agent.receive("Dungeon Master", "You awaken.")
        agent.receive("Data", "I look around.")
        agent.send()
        system_content = outgoing(llm)[0].content
        assert "Now continue the story" in system_content
        assert "Data" in system_content
        assert "Dungeon Master" in system_content

    def test_no_conversation_block_when_no_prior_history(self):
        """If the only entry is the latest other message, the system
        prompt should not have a stray empty 'CONVERSATION SO FAR:' block."""
        llm = make_streaming_llm("ok")
        agent = make_agent("Dungeon Master", llm=llm)
        agent.receive("Data", "I look around.")
        agent.send()
        system_content = outgoing(llm)[0].content
        assert "CONVERSATION SO FAR:" not in system_content


class TestPrimerFallback:
    def test_primer_used_when_transcript_empty(self):
        llm = make_streaming_llm("ok")
        agent = make_agent("Dungeon Master", llm=llm)
        agent.send()
        msgs = outgoing(llm)
        assert isinstance(msgs[0], SystemMessage)
        assert isinstance(msgs[1], HumanMessage)
        assert msgs[1].content == "Begin the adventure."

    def test_primer_used_when_only_self_messages_in_transcript(self):
        """Edge case: agent has spoken but no other party has responded yet.
        Anthropic still needs a non-empty user message."""
        llm = make_streaming_llm("ok")
        agent = make_agent("Dungeon Master", llm=llm)
        agent.receive("Dungeon Master", "You stand on the bridge.")
        agent.send()
        msgs = outgoing(llm)
        assert msgs[1].content == "Begin the adventure."
        assert "Dungeon Master: You stand on the bridge." in msgs[0].content


class TestTrimming:
    def test_transcript_trimmed_to_max(self):
        agent = make_agent("Dungeon Master")
        for i in range(MAX_TRANSCRIPT_ENTRIES + 4):
            sender = "Data" if i % 2 == 0 else "Dungeon Master"
            agent.receive(sender, f"line {i}")
        assert len(agent.transcript) == MAX_TRANSCRIPT_ENTRIES

    def test_trim_drops_oldest_first(self):
        agent = make_agent("Dungeon Master")
        for i in range(MAX_TRANSCRIPT_ENTRIES + 3):
            agent.receive("Data", f"line {i}")
        assert agent.transcript[0][1] == "line 3"
        assert agent.transcript[-1][1] == f"line {MAX_TRANSCRIPT_ENTRIES + 2}"


class TestDialogueSimulatorFlow:
    def _build(self, dm_responses: list[str]):
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

        assert storyteller.transcript == [
            ("Dungeon Master", "You awaken on the Enterprise."),
            ("Data", "I scan the rift."),
        ]
        assert protagonist.transcript == storyteller.transcript

        speaker, message = simulator.step()
        assert speaker == "Dungeon Master"
        assert "It is your turn" in message

        # Wire payload to Anthropic on the DM step.
        msgs = outgoing(llm)
        assert isinstance(msgs[0], SystemMessage)
        assert isinstance(msgs[1], HumanMessage)
        assert msgs[1].content == "I scan the rift."
        system_content = msgs[0].content
        assert "You are the DM." in system_content
        assert "Dungeon Master: You awaken on the Enterprise." in system_content

    def test_per_turn_sigil_view_after_five_repeated_inputs(self):
        """Regression for the cumulative-history Sigil bug.

        After 5 identical /play calls, the 5th LLM call should still see a
        single ``HumanMessage`` containing only the latest player line,
        not five copies of it joined together.
        """
        responses = [f"Response {i}. It is your turn, Data." for i in range(1, 6)]
        simulator, _storyteller, _protagonist, llm = self._build(responses)

        simulator.inject("Dungeon Master", "You awaken on the Enterprise.")
        for _ in range(5):
            simulator.inject("Data", "I do an internal scan of my brain to determine its status.")
            simulator.step()

        msgs = outgoing(llm)
        assert msgs[1].content == "I do an internal scan of my brain to determine its status."
        # And the HumanMessage must not contain a cumulative pile.
        assert msgs[1].content.count("internal scan") == 1
        # The DM's prior responses live in the system block, not the user message.
        system_content = msgs[0].content
        assert "Response 1." in system_content
        assert "Response 4." in system_content
