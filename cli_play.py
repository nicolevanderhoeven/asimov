from two_player_dnd import create_game

def run_interactive_game():
    
    simulator, protagonist_name, storyteller_name, protagonist_description, storyteller_description, specified_quest = create_game()

    print("\n🎲 Welcome to Two-Player D&D (CLI Mode)!")
    print(f"(Dungeon Master): {simulator.agents[0].message_history[-1]}\n")

    while True:
        user_input = input(f"You ({protagonist_name}): ").strip()
        if user_input.lower() in {"exit", "quit"}:
            print("👋 Ending game. Goodbye!")
            break
        if not user_input:
            print("Please enter an action.")
            continue

        simulator.inject(protagonist_name, user_input)
        name, message = simulator.step()
        print(f"\n({name}): {message}\n")

if __name__ == "__main__":
    run_interactive_game()
