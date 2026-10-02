package game

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
)

// Generate builds a scenario from one module of each kind, chosen by seed:
// what took the crew (the cause), where the key reading is, where the crew's
// records are, what guards the cause (a fight or a skill challenge), and the
// hazard of shutting the cause down. The same seed always builds the same
// scenario.
func Generate(seed uint64) *Scenario {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	c := causes[rng.IntN(len(causes))]
	rd := readings[rng.IntN(len(readings))]
	rc := records[rng.IntN(len(records))]
	en := encounters[rng.IntN(len(encounters))]
	hz := hazards[rng.IntN(len(hazards))]
	return assemble(seed, c, rd, rc, en, hz)
}

// moduleOrder is the order of a generated scenario's modules in its variant.
var moduleOrder = []string{"cause", "reading", "records", "encounter", "hazard"}

// Variants is how many distinct scenarios Generate can build.
func Variants() int {
	return len(causes) * len(readings) * len(records) * len(encounters) * len(hazards)
}

// A cause is what took the crew, and the device that did it.
type cause struct {
	id, room string
	// scene and details describe the device's room.
	scene   string
	details []string
	// device is the full name ("phase relay"), short the name in possessives
	// ("relay"), target the action target.
	device, short, target string
	logs                  string
	// reading is what the key reading recovers, meaning what it implies,
	// verb the option's verb phrase, and noun its short name.
	reading, meaning, verb, noun string
	// alive follows the crew records' first sentence.
	alive                     string
	source, sourceOpt, lead   string
	fix, fixNoun, fixDo       string
	done, fixLead             string
	rescueOpt, success, ready string
	notReady, longShot        string
	notReadyEx, summary       string
}

// A reading is where the key reading is found, with a check.
type reading struct {
	id, room, target, place string
	check                   CheckSpec
	lead, retry, approach   string
	// bridge is the bridge's description when the reading is there.
	bridge string
}

// A record is where the crew's records show they are alive.
type record struct {
	id, room, target    string
	first, option, lead string
	pattern, elsewhere  string
}

// An encounter module is what guards the cause.
type encounterModule struct {
	id   string
	enc  Encounter
	down string
	// rolls names the GM's rolls in the prompt, such as "the drone's rolls".
	rolls string
	// approaches are a challenge's ways to progress, with verb phrases.
	approaches []approachModule
}

type approachModule struct {
	target, verb string
	check        CheckSpec
}

// A hazard is what shutting the cause down can do to Data.
type hazard struct {
	id         string
	save       CheckSpec
	hz         Hazard
	noun       string
	avoid      string
	hurt, safe string
}

func skillCheck(ability, skill string, dc int) CheckSpec {
	return CheckSpec{Label: title(ability) + " (" + title(skill) + ")", Ability: ability, Skill: skill, DC: dc}
}

func saveCheck(ability string, dc int) CheckSpec {
	return CheckSpec{Label: title(ability) + " saving throw", Ability: ability, Save: true, DC: dc}
}

// roomName is how a location reads in a sentence.
var roomName = map[string]string{
	"bridge": "the bridge", "sickbay": "sickbay", "engineering": "engineering",
	"astrometrics": "astrometrics", "computer_core": "the computer core", "security": "the security office",
	"transporter_room": "the transporter room", "holodeck": "the holodeck", "cargo_bay": "the cargo bay",
	"deflector_control": "deflector control",
}

var bridgeDetails = []string{
	"The captain's chair, conn, and ops stations are unoccupied. Every console is still powered.",
	"The main viewscreen shows the ship holding position.",
	"A turbolift at the rear of the bridge reaches every deck.",
}

var causes = []cause{
	{
		id: "phase_relay", room: "engineering",
		scene: "An experimental phase relay pulses beside the warp core.",
		details: []string{
			"The warp core pulses steadily, and main power reads nominal.",
			"The phase relay is a temporary installation, cabled into a power conduit beside the core.",
			"Tools lie where the engineering crew set them down.",
		},
		device: "phase relay", short: "relay", target: "relay",
		logs:    "The bridge logs show a subspace pulse coinciding with the disappearance. Data was unaffected; the pulse selected biological neural patterns.",
		reading: "the phase frequency of the pulse", meaning: "The crew may be out of phase rather than absent.", verb: "Recover the pulse frequency", noun: "the pulse frequency",
		alive:     "The crew remain alive in a subspace pocket; the transporter can target their stored {pattern}.",
		source:    "An experimental phase relay in engineering caused the pulse. Isolate the relay before attempting transport.",
		sourceOpt: "Read the relay's diagnostic display; no roll required.",
		lead:      "Something experimental is pulsing beside the warp core in engineering.",
		fix:       "Isolate the relay", fixNoun: "Isolation", fixDo: "isolate the relay",
		done:      "The phase relay is isolated. The transporter is safe to use once {noun} and {pattern} are known.",
		fixLead:   "the phase relay in engineering can be isolated",
		rescueOpt: "Use the recovered pulse frequency and {pattern} to return the crew.",
		success:   "The transporter locks onto the crew's {pattern} at the recovered phase frequency. The crew return alive; Captain Picard thanks you. The rescue is complete.",
		ready:     "Everything is ready: the transporter can return the crew from engineering.",
		notReady:  "Transport is not ready yet. The transporter still needs the pulse frequency, the crew's {pattern}, and the relay diagnostics.",
		longShot:  "beaming the crew back before the transporter is ready", notReadyEx: "a transport that isn't ready",
		summary: "a subspace pulse from an experimental phase relay in engineering phased the crew into a subspace pocket",
	},
	{
		id: "transporter", room: "transporter_room",
		scene: "The transporter pad glows under standby power. A modified pattern buffer hums beneath the control console.",
		details: []string{
			"The transporter console shows a test cycle still running, though the pad is empty.",
			"Six pad discs are lit in a ring, waiting.",
			"A diagnostic kit lies open beside the console.",
		},
		device: "pattern buffer", short: "buffer", target: "buffer",
		logs:    "The bridge logs show a ship-wide transporter cycle coinciding with the disappearance. Data was unaffected; the cycle locked onto biological patterns only.",
		reading: "the buffer's phase variance during the cycle", meaning: "The crew may be held in transit rather than lost.", verb: "Recover the buffer variance", noun: "the buffer variance",
		alive:     "The crew remain alive, suspended in transit; the transporter can rematerialise them from their stored {pattern}.",
		source:    "A modified pattern buffer in the transporter room ran the cycle and is still holding the crew. Halt the test cycle before attempting rematerialisation.",
		sourceOpt: "Read the buffer's diagnostic log; no roll required.",
		lead:      "The transporter room's console shows a test cycle that never finished.",
		fix:       "Halt the test cycle", fixNoun: "Halting it", fixDo: "halt the test cycle",
		done:      "The test cycle is halted. Rematerialisation is safe once {noun} and {pattern} are known.",
		fixLead:   "the pattern buffer's test cycle in the transporter room can be halted",
		rescueOpt: "Use the recovered buffer variance and {pattern} to rematerialise the crew.",
		success:   "The transporter rematerialises the crew from the buffer at the recovered variance. The crew return alive; Captain Picard thanks you. The rescue is complete.",
		ready:     "Everything is ready: the transporter room can rematerialise the crew.",
		notReady:  "Rematerialisation is not ready yet. The transporter still needs the buffer variance, the crew's {pattern}, and the buffer diagnostics.",
		longShot:  "rematerialising the crew before the buffer is safe", notReadyEx: "a rematerialisation that isn't ready",
		summary: "a modified pattern buffer in the transporter room pulled the crew into transit and is still holding them",
	},
	{
		id: "holodeck", room: "holodeck",
		scene: "The holodeck's yellow grid is bare, its arch flashing a program error. An overclocked holomatrix glows behind an open wall panel.",
		details: []string{
			"The arch console lists a program as still running, though the grid is empty.",
			"The grid lines pulse faintly, as if something is loading.",
			"A wall panel lies on the deck where it was removed.",
		},
		device: "holomatrix", short: "matrix", target: "matrix",
		logs:    "The bridge logs show a holodeck program expanding beyond the holodeck at the moment of the disappearance. Data was unaffected; the program captured only organic patterns.",
		reading: "the program's capture index", meaning: "The crew may be stored inside the program rather than gone.", verb: "Recover the capture index", noun: "the capture index",
		alive:     "The crew remain alive, held as patterns in the holodeck's memory; their {pattern} can restore them.",
		source:    "An overclocked holomatrix on the holodeck captured the crew into a running program. Shut down the matrix before attempting to restore them.",
		sourceOpt: "Read the holomatrix diagnostics; no roll required.",
		lead:      "The holodeck's arch has been flashing a program error since it happened.",
		fix:       "Shut down the matrix", fixNoun: "The shutdown", fixDo: "shut down the matrix",
		done:      "The holomatrix is shut down safely. The crew can be restored once {noun} and {pattern} are known.",
		fixLead:   "the holomatrix on the holodeck can be shut down",
		rescueOpt: "Use the recovered capture index and {pattern} to restore the crew from the program.",
		success:   "The holodeck releases its stored patterns at the recovered capture index, and the crew step out onto the grid alive; Captain Picard thanks you. The rescue is complete.",
		ready:     "Everything is ready: the holodeck can restore the crew.",
		notReady:  "The restore is not ready yet. The holodeck still needs the capture index, the crew's {pattern}, and the matrix diagnostics.",
		longShot:  "ending the program before the matrix is safe", notReadyEx: "a restore that isn't ready",
		summary: "an overclocked holomatrix on the holodeck captured the crew into a running program",
	},
	{
		id: "artifact", room: "cargo_bay",
		scene: "Cargo containers stand in rows around an alien artifact recovered on the last away mission. It glows with a slow violet pulse.",
		details: []string{
			"The artifact sits in an open stasis crate, its containment seal broken.",
			"A cargo manifest lists it as recovered on the last away mission, origin unknown.",
			"Antigrav sleds are parked where the cargo crew left them.",
		},
		device: "alien artifact", short: "artifact", target: "artifact",
		logs:    "The bridge logs show an energy surge from the cargo deck coinciding with the disappearance. Data was unaffected; the surge drew in living minds only.",
		reading: "the artifact's resonance signature", meaning: "The crew may be held inside the artifact's field rather than lost.", verb: "Recover the resonance signature", noun: "the resonance signature",
		alive:     "The crew remain alive inside the artifact's field; their {pattern} can guide them back out.",
		source:    "The alien artifact in the cargo bay absorbed the crew when its stasis seal failed. Reseal its stasis field before attempting to bring them back.",
		sourceOpt: "Study the artifact's surface glyphs with your tricorder; no roll required.",
		lead:      "Something recovered on the last away mission is glowing in the cargo bay.",
		fix:       "Reseal the stasis field", fixNoun: "Resealing", fixDo: "reseal the stasis field",
		done:      "The artifact's stasis field is resealed. The crew can be drawn out once {noun} and {pattern} are known.",
		fixLead:   "the artifact's stasis field in the cargo bay can be resealed",
		rescueOpt: "Use the recovered resonance signature and {pattern} to draw the crew out of the artifact.",
		success:   "Tuned to the recovered resonance, the artifact releases the crew one by one onto the cargo bay deck, alive; Captain Picard thanks you. The rescue is complete.",
		ready:     "Everything is ready: the artifact can release the crew.",
		notReady:  "The artifact can't release them yet. You still need the resonance signature, the crew's {pattern}, and the artifact's diagnostics.",
		longShot:  "pulling the crew out of the artifact before its field is sealed", notReadyEx: "a release that isn't ready",
		summary: "an alien artifact in the cargo bay absorbed the crew when its stasis seal failed",
	},
	{
		id: "deflector", room: "deflector_control",
		scene: "Deflector control overlooks the main deflector's emitter assembly. The emitter is still projecting an experimental field.",
		details: []string{
			"The deflector's emitter dish glows an unusual amber.",
			"A science team's experiment schedule is open on the control console.",
			"Three chairs at the console are pushed back.",
		},
		device: "deflector emitter", short: "emitter", target: "emitter",
		logs:    "The bridge logs show a deflector field experiment coinciding with the disappearance. Data was unaffected; the field shifted only organic matter out of step with time.",
		reading: "the field's temporal offset", meaning: "The crew may be a few seconds out of step with time rather than gone.", verb: "Recover the temporal offset", noun: "the temporal offset",
		alive:     "The crew remain alive, a few seconds out of step with the ship; their {pattern} can anchor them back.",
		source:    "The deflector emitter's experimental temporal field displaced the crew in time. Power down the emitter before bringing them back into sync.",
		sourceOpt: "Read the emitter's field telemetry; no roll required.",
		lead:      "The main deflector is still projecting something unusual from deflector control.",
		fix:       "Power down the emitter", fixNoun: "Powering down", fixDo: "power down the emitter",
		done:      "The deflector emitter is powered down safely. The crew can be brought back into sync once {noun} and {pattern} are known.",
		fixLead:   "the deflector emitter can be powered down from deflector control",
		rescueOpt: "Use the recovered temporal offset and {pattern} to bring the crew back into sync.",
		success:   "The deflector pulses once at the recovered offset, and the crew flicker back into the present, alive; Captain Picard thanks you. The rescue is complete.",
		ready:     "Everything is ready: the deflector can bring the crew back into sync.",
		notReady:  "The resync isn't ready yet. The deflector still needs the temporal offset, the crew's {pattern}, and the emitter telemetry.",
		longShot:  "pulling the crew back into sync before the emitter is safe", notReadyEx: "a resync that isn't ready",
		summary: "an experimental temporal field from the deflector emitter put the crew a few seconds out of step with time",
	},
}

var readings = []reading{
	{
		id: "bridge_sensors", room: "bridge", target: "sensors", place: "The bridge sensor buffer",
		check:    skillCheck("intelligence", "investigation", 12),
		lead:     "The bridge's damaged sensor buffer recorded whatever happened; its readings might still be recovered.",
		retry:    "The damaged buffer yields nothing stable. You may try again.",
		approach: "splice into the sensor buffer",
		bridge:   "Empty command chairs face a steady starfield. The operations console holds logs and a damaged sensor buffer. Turbolifts reach every deck.",
	},
	{
		id: "astrometrics", room: "astrometrics", target: "sensor_array", place: "The astrometrics sensor array",
		check:    skillCheck("wisdom", "perception", 12),
		lead:     "The astrometrics lab's sensors were sweeping the ship when it happened; their readings might still be recovered.",
		retry:    "The array's records are too noisy to read. You may try again.",
		approach: "realign the sensor array's filters",
	},
	{
		id: "computer_core", room: "computer_core", target: "core_memory", place: "The main computer core's memory",
		check:    skillCheck("intelligence", "investigation", 13),
		lead:     "The main computer core logged every system as it happened; its memory is fragmented but might still be read.",
		retry:    "The fragmented memory yields nothing coherent. You may try again.",
		approach: "reassemble the fragmented memory blocks",
	},
}

var records = []record{
	{
		id: "sickbay", room: "sickbay", target: "medical_records",
		first:     "Sickbay recorded living neural signatures after the disappearance.",
		option:    "Retrieve crew biopatterns using your credentials; no roll required.",
		lead:      "Sickbay's medical console retains the crew's most recent scans.",
		pattern:   "biopatterns",
		elsewhere: `"go to sickbay and pull the biopatterns" is simply inspect medical_records`,
	},
	{
		id: "security", room: "security", target: "internal_sensors",
		first:     "The security office's internal sensors logged living life signs after the disappearance, though they could not place them aboard.",
		option:    "Pull the internal sensor log using your security clearance; no roll required.",
		lead:      "The security office's internal sensors track every life sign aboard, and their log is still running.",
		pattern:   "life-sign records",
		elsewhere: `"go to security and check the internal sensors" is simply inspect internal_sensors`,
	},
}

// rooms are the rooms other than the cause's, which brings its own.
var rooms = map[string]Room{
	"sickbay": {
		Description: "The biobeds are empty. The medical console retains recent crew scans.",
		Details: []string{
			"The biobeds are made up and unoccupied. A tray of instruments sits untouched.",
			"The door to the chief medical officer's office is open, and her desk console is on.",
		},
	},
	"security": {
		Description: "The security office is empty, its weapons locker sealed. The internal sensor console still logs the ship.",
		Details: []string{
			"The duty officer's chair is pushed back from the desk.",
			"The brig's forcefields are up around empty cells.",
		},
	},
	"astrometrics": {
		Description: "The curved astrometrics display shows a frozen star chart. The sensor array's console scrolls corrupted readings.",
		Details: []string{
			"The display fills the far wall, frozen mid-sweep.",
			"Two chairs at the control podium are pushed back, as if their occupants stood at once.",
		},
	},
	"computer_core": {
		Description: "Isolinear chip banks hum behind transparent panels. A memory access terminal reports fragmented records.",
		Details: []string{
			"The core's cooling system runs steadily.",
			"A maintenance padd on the terminal shows a diagnostic interrupted partway through.",
		},
	},
}

var encounters = []encounterModule{
	{
		id: "security_drone", down: "With the drone down", rolls: "the drone's rolls",
		enc: Encounter{
			Kind: Combat, Name: "security drone", Short: "drone", Target: "drone",
			Active:        "A damaged security drone guards its controls.",
			Cleared:       "The disabled security drone hangs inert beside its controls.",
			ActiveDetail:  "A fixed-mount security drone with a cracked casing guards the {device}'s controls. It has not fired.",
			ClearedDetail: "The security drone hangs inert on its mount.",
			Lead:          "A damaged security drone in {room} guards the {device}'s controls.",
			HP:            10, AC: 12, AttackBonus: 3, Initiative: "1d20+1", Damage: "1d4+1", CritDamage: "2d4+1",
			Purpose: "drone", Fires: "fires", Stationary: "fixed drone",
			Bypass: &CheckSpec{Label: "Intelligence (Arcana)", Ability: "intelligence", Skill: "arcana", DC: 13},
			Scene:  "The damaged security drone is 15 feet away and fires from its fixed mount. It blocks the {device}.",
			SceneDetails: []string{
				"The drone is a fixed-mount security unit with a cracked casing. It tracks Data and fires short bursts.",
				"The {device}'s controls are behind the drone.",
			},
		},
	},
	{
		id: "exocomp", down: "With the exocomp shut down", rolls: "the exocomp's rolls",
		enc: Encounter{
			Kind: Combat, Name: "malfunctioning exocomp", Short: "exocomp", Target: "exocomp",
			Active:        "A malfunctioning exocomp hovers over its controls, plasma cutter extended.",
			Cleared:       "The deactivated exocomp rests on the deck beside its controls.",
			ActiveDetail:  "A tethered exocomp hovers at the {device}'s controls, its plasma cutter sparking. It has not struck.",
			ClearedDetail: "The exocomp rests on the deck, its indicator lights dark.",
			Lead:          "A malfunctioning exocomp in {room} hovers over the {device}'s controls.",
			HP:            8, AC: 13, AttackBonus: 4, Initiative: "1d20+3", Damage: "1d4+2", CritDamage: "2d4+2",
			Purpose: "exocomp", Fires: "slashes", Stationary: "tethered exocomp",
			Bypass: &CheckSpec{Label: "Intelligence (Arcana)", Ability: "intelligence", Skill: "arcana", DC: 14},
			Scene:  "The malfunctioning exocomp darts 10 feet away on its tether, plasma cutter flaring. It guards the {device}.",
			SceneDetails: []string{
				"The exocomp is a small repair unit, tethered beside the {device}. Its plasma cutter spits sparks.",
				"The {device}'s controls are behind the exocomp.",
			},
		},
	},
	{
		id: "hologram", down: "With the hologram gone", rolls: "the hologram's rolls",
		enc: Encounter{
			Kind: Combat, Name: "holographic security officer", Short: "hologram", Target: "hologram",
			Active:        "A holographic security officer, projected by an emergency emitter, stands guard over its controls.",
			Cleared:       "The emergency emitter is dark; the holographic officer is gone.",
			ActiveDetail:  "An emergency holo-emitter above the {device}'s controls projects a security officer who watches Data. It has not fired.",
			ClearedDetail: "The emergency holo-emitter above the controls is dark.",
			Lead:          "A holographic security officer in {room} guards the {device}'s controls.",
			HP:            12, AC: 11, AttackBonus: 3, Initiative: "1d20+2", Damage: "1d6", CritDamage: "2d6",
			Purpose: "hologram", Fires: "fires", Stationary: "emitter-bound hologram",
			Bypass: &CheckSpec{Label: "Intelligence (Arcana)", Ability: "intelligence", Skill: "arcana", DC: 12},
			Scene:  "The holographic security officer levels a phaser 20 feet away, flickering as its emitter strains. It guards the {device}.",
			SceneDetails: []string{
				"The hologram is a security subroutine projected by a single emergency emitter above the {device}.",
				"The {device}'s controls are behind the hologram.",
			},
		},
	},
	{
		id: "containment_field", down: "With the field stable", rolls: "the field's feedback",
		enc: Encounter{
			Kind: Challenge, Name: "cascading containment field", Short: "field", Target: "field",
			Active:        "A cascading containment field crackles around its controls.",
			Cleared:       "The containment field around its controls has settled into a stable shimmer.",
			ActiveDetail:  "Containment field emitters around the {device}'s controls flicker and buckle, throwing off feedback.",
			ClearedDetail: "The containment field around the {device}'s controls is stable and passable.",
			Lead:          "A cascading containment field in {room} surrounds the {device}'s controls.",
			Needed:        3,
			Hazard:        &Hazard{Purpose: "field_damage", Label: "Field feedback", Notation: "1d4"},
			Progress:      "The field steadies a little. (%d of %d successes)",
			Success:       "The containment field settles into a stable, passable shimmer.",
			Setback:       "The field lashes back with a burst of feedback.",
		},
		approaches: []approachModule{
			{"field_emitters", "Recalibrate the field emitters", skillCheck("intelligence", "arcana", 13)},
			{"power_feed", "Reroute the field's power feed by hand", skillCheck("dexterity", "sleight_of_hand", 12)},
			{"emitter_housing", "Brace the buckling emitter housing", skillCheck("strength", "athletics", 13)},
		},
	},
	{
		id: "security_lockout", down: "With the lockout released", rolls: "the lockout's countermeasures",
		enc: Encounter{
			Kind: Challenge, Name: "security lockout", Short: "lockout", Target: "lockout",
			Active:        "A security lockout seals its controls behind a humming barrier.",
			Cleared:       "The security lockout has released its controls.",
			ActiveDetail:  "A security lockout seals the {device}'s controls; its countermeasure nodes glow along the barrier.",
			ClearedDetail: "The lockout barrier around the {device}'s controls is down.",
			Lead:          "A security lockout in {room} seals the {device}'s controls.",
			Needed:        3,
			Hazard:        &Hazard{Purpose: "countermeasure_damage", Label: "Security countermeasure", Notation: "1d4"},
			Progress:      "Another layer of the lockout gives way. (%d of %d successes)",
			Success:       "The last layer of the lockout releases.",
			Setback:       "The lockout's countermeasures answer with a stinging jolt.",
		},
		approaches: []approachModule{
			{"lockout_trace", "Trace the lockout through the security subroutines", skillCheck("intelligence", "investigation", 13)},
			{"access_panel", "Hotwire the access panel", skillCheck("dexterity", "sleight_of_hand", 12)},
			{"command_authority", "Assert your command authority to the security subroutine", skillCheck("charisma", "persuasion", 11)},
		},
	},
}

var hazards = []hazard{
	{id: "discharge", save: saveCheck("dexterity", 12), hz: Hazard{Purpose: "discharge_damage", Label: "Electrical discharge", Notation: "1d6"}, noun: "discharge", avoid: "a 1d6 electrical discharge", hurt: "suffer an electrical discharge", safe: "avoid the electrical discharge"},
	{id: "radiation", save: saveCheck("constitution", 13), hz: Hazard{Purpose: "radiation_damage", Label: "Radiation burst", Notation: "1d6"}, noun: "radiation burst", avoid: "a 1d6 radiation burst", hurt: "take a burst of radiation", safe: "shield yourself from the radiation"},
	{id: "plasma", save: saveCheck("dexterity", 13), hz: Hazard{Purpose: "plasma_damage", Label: "Plasma venting", Notation: "1d6"}, noun: "venting plasma", avoid: "1d6 damage from venting plasma", hurt: "are scorched by venting plasma", safe: "dodge the venting plasma"},
}

// saveName is how an option names a saving throw: "Dexterity save".
func saveName(c CheckSpec) string { return title(c.Ability) + " save" }

func assemble(seed uint64, c cause, rd reading, rc record, em encounterModule, hz hazard) *Scenario {
	r := strings.NewReplacer("{pattern}", rc.pattern, "{noun}", c.noun, "{device}", c.device, "{room}", roomName[c.room])
	sc := &Scenario{
		ID:      fmt.Sprintf("generated-%d", seed),
		Seed:    seed,
		Modules: map[string]string{"cause": c.id, "reading": rd.id, "records": rc.id, "encounter": em.id, "hazard": hz.id},
		Opening: classic.Opening,
		Start:   "bridge",
		Rooms:   map[string]Room{},
	}
	bridge := Room{Description: "Empty command chairs face a steady starfield. The operations console holds the ship's logs. Turbolifts reach every deck.", Details: bridgeDetails}
	if rd.bridge != "" {
		bridge.Description = rd.bridge
	}
	sc.Rooms["bridge"] = bridge
	sc.Rooms[rd.room] = withBridge(sc.Rooms, rd.room)
	sc.Rooms[rc.room] = rooms[rc.room]
	sc.Rooms[c.room] = Room{Description: c.scene, Details: c.details}
	for _, loc := range []string{"bridge", rd.room, rc.room, c.room} {
		if !slices.Contains(sc.Locations, loc) {
			sc.Locations = append(sc.Locations, loc)
		}
	}

	check := rd.check
	sc.Clues = []Clue{
		{
			Key: "logs", Location: "bridge", Action: Action{"inspect", "logs"},
			Text:   c.logs,
			Option: "Read the operations log; no roll required.",
			Lead:   "The operations console on the bridge is flashing a diagnostic warning and holds the ship's logs.",
		},
		{
			Key: "reading", Location: rd.room, Action: Action{"scan", rd.target}, Required: true,
			Text:        rd.place + " contains " + c.reading + ". " + c.meaning,
			Option:      fmt.Sprintf("%s: %s, DC %d. A failed attempt can be retried.", c.verb, check.Label, check.DC),
			Check:       &check,
			Retry:       rd.retry,
			Lead:        rd.lead,
			Effect:      &Effect{ID: "recover_reading", Description: fmt.Sprintf("Recover %s from %s by another method.", c.noun, lowerFirst(rd.place)), MinDC: 15, OnFailure: "nothing is recovered; you may try again"},
			EffectRetry: "The attempt recovers nothing stable. You may try again.",
		},
		{
			Key: "records", Location: rc.room, Action: Action{"inspect", rc.target}, Required: true,
			Text:   rc.first + " " + r.Replace(c.alive),
			Option: rc.option,
			Lead:   rc.lead,
		},
		{
			Key: "source", Location: c.room, Action: Action{"inspect", c.target}, Required: true,
			Text:   c.source,
			Option: c.sourceOpt,
			Lead:   c.lead,
		},
	}

	e := em.enc
	e.Location = c.room
	e.Active, e.Cleared = r.Replace(e.Active), r.Replace(e.Cleared)
	e.ActiveDetail, e.ClearedDetail = r.Replace(e.ActiveDetail), r.Replace(e.ClearedDetail)
	e.Lead, e.Scene = r.Replace(e.Lead), r.Replace(e.Scene)
	e.SceneDetails = replaceAll(r, e.SceneDetails)
	e.Access = r.Replace("The {device}'s controls are accessible.")
	encounterSummary := ""
	if e.Kind == Challenge {
		e.Approaches = nil
		labels := []string{}
		for _, a := range em.approaches {
			e.Approaches = append(e.Approaches, Approach{
				Target: a.target,
				Option: fmt.Sprintf("%s: %s, DC %d. One of %d successes needed; a failure triggers %s %s.", a.verb, a.check.Label, a.check.DC, e.Needed, e.Hazard.Notation, strings.ToLower(e.Hazard.Label)),
				Check:  a.check,
			})
			labels = append(labels, fmt.Sprintf("%s (%s DC %d)", a.target, a.check.Label, a.check.DC))
		}
		e.Advance = &Effect{ID: "advance_" + e.Target, Description: fmt.Sprintf("Get past the %s by another method: counts as one of the %d successes it needs.", e.Name, e.Needed), MinDC: 15, OnFailure: fmt.Sprintf("no progress, and %s (%s damage)", lowerFirst(strings.TrimSuffix(e.Setback, ".")), e.Hazard.Notation)}
		encounterSummary = fmt.Sprintf("A %s guards the %s: a skill challenge needing %d successes from any of %s; each failure deals %s %s damage (the GM's roll). There is no combat.", e.Name, c.device, e.Needed, strings.Join(labels, ", "), e.Hazard.Notation, strings.ToLower(e.Hazard.Label))
	} else {
		encounterSummary = fmt.Sprintf("A %s (HP %d, AC %d) guards the %s; a tricorder bypass (%s DC %d) disables it, and failure starts combat.", e.Name, e.HP, e.AC, c.device, e.Bypass.Label, e.Bypass.DC)
	}
	sc.Encounter = e

	sc.Fix = Fix{
		Target: c.target, Location: c.room,
		Option: fmt.Sprintf("%s: %s DC %d to avoid %s. %s succeeds either way.", c.fix, saveName(hz.save), hz.save.DC, hz.avoid, c.fixNoun),
		Save:   hz.save,
		Hazard: hz.hz,
		Hurt:   fmt.Sprintf("You %s but %s.", c.fixDo, hz.hurt),
		Safe:   fmt.Sprintf("You %s and %s.", c.fixDo, hz.safe),
		Done:   r.Replace(c.done),
		Lead:   em.down + ", " + c.fixLead + ".",
	}
	sc.Rescue = Rescue{
		Location: c.room,
		Option:   r.Replace(c.rescueOpt),
		NotReady: r.Replace(c.notReady),
		Success:  r.Replace(c.success),
		Ending:   classic.Rescue.Ending,
		Lead:     c.ready,
	}
	sc.Computer = classic.Computer
	sc.Prompt = Prompt{
		Elsewhere: rc.elsewhere,
		LongShot:  c.longShot,
		Approach:  rd.approach,
		NotReady:  c.notReadyEx,
		GMRolls:   fmt.Sprintf("%s and the %s's %s", em.rolls, c.short, hz.noun),
	}
	sc.Summary = fmt.Sprintf("The Silent Enterprise (generated scenario %s): Data must find the missing crew; %s. Clues: logs (bridge operations log: what happened), reading (%s at %s, %s DC %d, retryable: %s), records (%s at %s: the crew are alive and their %s), source (%s at %s: the %s caused it). %s The %s must then be secured (%s: %s DC %d against %s; it succeeds either way). Rescue at %s needs reading, records, source, and the secured %s.",
		sc.Variant(), c.summary,
		rd.target, rd.room, check.Label, check.DC, c.reading,
		rc.target, rc.room, rc.pattern,
		c.target, c.room, c.device,
		encounterSummary,
		c.device, c.target, saveName(hz.save), hz.save.DC, hz.avoid,
		c.room, c.device)
	return sc
}

// withBridge is the reading's room: the bridge, already there, or one of
// the rooms.
func withBridge(have map[string]Room, loc string) Room {
	if r, ok := have[loc]; ok {
		return r
	}
	return rooms[loc]
}

func replaceAll(r *strings.Replacer, ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = r.Replace(s)
	}
	return out
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
