#!/usr/bin/env python
# coding: utf-8

# # Two-Player Dungeons & Dragons

# ## Import LangChain related modules 

def create_game():
    from typing import Callable, List

    from langchain.schema import (
        HumanMessage,
        SystemMessage,
    )
    from langchain_openai import ChatOpenAI

    from dotenv import load_dotenv
    import os
    import logging
    from loggingfw import CustomLogFW


    load_dotenv()  # Load .env file

    # Set up logging
    logFW = CustomLogFW(service_name='main_app', instance_id='1')
    handler= logFW.setup_logging()
    logger = logging.getLogger()
    logger.addHandler(handler)
    logger.setLevel(logging.INFO)
    logger.error("Welcome to two-player D&D, Harry Potter edition!")

    # Initialize OpenLIT library

    otlpEndpoint = os.getenv("OTLP_ENDPOINT")
    otlpHeaders = os.getenv("OTLP_HEADERS")

    from openlit import openlit
    openlit.init(
        application_name="two-player-dnd",
        otlp_endpoint=otlpEndpoint,
        otlp_headers="Authorization=Basic%20"+ otlpHeaders,
        event_logger=logger,
    )

    # ## `DialogueAgent` class
    # The `DialogueAgent` class is a simple wrapper around the `ChatOpenAI` model that stores the message history from the `dialogue_agent`'s point of view by simply concatenating the messages as strings.
    # 
    # It exposes two methods: 
    # - `send()`: applies the chatmodel to the message history and returns the message string
    # - `receive(name, message)`: adds the `message` spoken by `name` to message history


    class DialogueAgent:
        def __init__(
            self,
            name: str,
            system_message: SystemMessage,
            model: ChatOpenAI,
        ) -> None:
            self.name = name
            self.system_message = system_message
            self.model = model
            self.prefix = f"{self.name}: "
            self.reset()

        def reset(self):
            self.message_history = ["Here is the conversation so far."]

        from openlit import trace

        def send(self) -> str:
            """
            Applies the chatmodel to the message history
            and returns the message string
            """
            message = self.model.invoke(
                [
                    self.system_message,
                    HumanMessage(content="\n".join(self.message_history + [self.prefix])),
                ]
            )
            return message.content

        def receive(self, name: str, message: str) -> None:
            self.message_history.append(f"{name}: {message}")
            logger.info(f"{self.name} received message: {message}")

    # ## `DialogueSimulator` class
    # The `DialogueSimulator` class takes a list of agents. At each step, it performs the following:
    # 1. Select the next speaker
    # 2. Calls the next speaker to send a message 
    # 3. Broadcasts the message to all other agents
    # 4. Update the step counter.
    # The selection of the next speaker can be implemented as any function, but in this case we simply loop through the agents.


    class DialogueSimulator:
        def __init__(
            self,
            agents: List[DialogueAgent],
            selection_function: Callable[[int, List[DialogueAgent]], int],
        ) -> None:
            self.agents = agents
            self._step = 0
            self.select_next_speaker = selection_function

        def reset(self):
            for agent in self.agents:
                agent.reset()

        def inject(self, name: str, message: str):
            """
            Initiates the conversation with a {message} from {name}
            """
            for agent in self.agents:
                logger.info(f"{agent.name}: {message}")
                agent.receive(name, message)
                
            # increment time
            self._step += 1
            logger.info(f"step: {self._step}")

        def step(self) -> tuple[str, str]:
            speaker_idx = self.select_next_speaker(self._step, self.agents)
            speaker = self.agents[speaker_idx]
            message = speaker.send()
            for receiver in self.agents:
                receiver.receive(speaker.name, message)
                logger.info(f"receiver: {receiver.name}, message: {message}")
            self._step += 1
            return speaker.name, message

    # ## Define roles and quest

    protagonist_name = "Harry Potter"
    storyteller_name = "Dungeon Master"
    quest = "Find all of Lord Voldemort's seven horcruxes."
    logger.info(f"Quest assigned: ${quest}")
    word_limit = 50  # word limit for task brainstorming


    # ## Ask an LLM to add detail to the game description

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
    protagonist_description = ChatOpenAI(temperature=1.0)(
        protagonist_specifier_prompt
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
    storyteller_description = ChatOpenAI(temperature=1.0)(
        storyteller_specifier_prompt
    ).content

    # ## Protagonist and dungeon master system messages

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

    # ## Use an LLM to create an elaborate quest description

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
    specified_quest = ChatOpenAI(temperature=1.0)(quest_specifier_prompt).content

    # ## Main Loop

    protagonist = DialogueAgent(
        name=protagonist_name,
        system_message=protagonist_system_message,
        model=ChatOpenAI(temperature=0.2),
    )
    storyteller = DialogueAgent(
        name=storyteller_name,
        system_message=storyteller_system_message,
        model=ChatOpenAI(temperature=0.2),
    )

    def select_next_speaker(step: int, agents: List[DialogueAgent]) -> int:
        idx = step % len(agents)
        return idx

    simulator = DialogueSimulator(
        agents=[storyteller, protagonist], 
        selection_function=select_next_speaker
    )
    simulator.reset()
    simulator.inject(storyteller_name, specified_quest)

    return (
    simulator,
    protagonist_name,
    storyteller_name,
    protagonist_description,
    storyteller_description,
    specified_quest
)

if __name__ == "__main__":
    simulator, protagonist_name, _ = create_game()
    while True:
        user_input = input(">>> ")
        simulator.inject(protagonist_name, user_input)
        name, message = simulator.step()
