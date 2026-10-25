package flow

import (
	"fmt"

	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/semantics"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// The suites of the conformance suite. Each scenario's events, views, and
// states are written from PRD §5.8.5 and schema/conformance/README.md, not
// from what this package's runtime gives, which TestConformance holds to
// them.

// The phases of a state's cursor.
const (
	awaitingChoice = published.PhaseAwaitingChoice
	endedPhase     = published.PhaseEnded
)

// choicesSuite is of an element's choices: conditions, on-exit, on-traverse,
// and on-enter in their order, an ending, a dead end, no choices, advance,
// start, goto, values set while the flow awaits a choice, a component's
// runtime attributes, and strict mode.
func choicesSuite() *suite {
	s := &suite{
		name: "choices", code: "C1", project: "Choices",
		variables: []fixtureVar{
			{id: "CASH", key: "gold", typ: value.Int, def: 0},
			{id: "MET", key: "met", typ: value.Bool, def: false},
			{id: "NAME", key: "name", typ: value.String, def: "Ann"},
		},
		types: []fixtureType{{id: "CHAR", key: "character", name: "Character", attributes: []fixtureAttribute{
			{id: "TRST", key: "trust", name: "Trust", typ: value.Int, def: 0, runtime: true},
			{id: "MD", key: "mood", name: "Mood", typ: value.Enum, def: "calm", runtime: true,
				options: []string{"calm", "wary", "angry"}},
			{id: "TAG", key: "title", name: "Title", typ: value.String, def: ""},
		}}},
		components: []fixtureComponent{{id: "KEEPER", key: "elara", name: "Elara", typ: "CHAR",
			values: map[string]any{"TRST": 1, "MD": "wary", "TAG": "Keeper"}}},
		boards: []fixtureBoard{{id: "PAGE", slug: "main", name: "Main", entry: "START",
			nodes: []fixtureNode{
				{id: "START", typ: element, text: "Hello, {name}. You have {gold} gold.", onEnter: "gold += 1",
					onExit: `signal("left_start", gold)`},
				{id: "MARKET", typ: element, text: "You buy a rope.", onExit: `signal("market_exit")`},
				{id: "CHAT", typ: element, text: "[elara] is {elara.mood}. Trust: {elara.trust}.",
					onEnter: "elara.trust += 1\nelara.mood = \"calm\"\nsignal(\"chat_enter\", elara.trust)"},
				{id: "BYE", typ: element, ending: true, text: "Goodbye, {name}.", onExit: `signal("bye_exit")`},
				{id: "GATED", typ: element, text: "A locked door.", onExit: `signal("gated_exit")`},
			},
			connections: []fixtureConnection{
				{id: "PAY", from: "START", to: "MARKET", label: "Buy a rope", condition: "gold >= 2",
					onTraverse: "gold -= 2"},
				{id: "ASK", from: "START", to: "CHAT", label: "Talk to [elara]",
					onTraverse: "met = true\nsignal(\"asking\", met)"},
				{id: "DEPART", from: "START", to: "BYE", label: "Leave", condition: "met"},
				{id: "BACK", from: "CHAT", to: "START"},
				{id: "REPEAT", from: "BYE", to: "START", label: "Again"},
				{id: "FREE", from: "GATED", to: "START", label: "Open", condition: "gold > 100"},
			}}},
		entry: [2]string{"PAGE", "START"},
	}
	greeting := func(name string, gold int) string { return fmt.Sprintf("Hello, %s. You have %d gold.", name, gold) }
	// atStart is the view at START, where met is false: buying a rope is a
	// choice from 2 gold.
	atStart := func(gold int) viewJSON {
		if gold >= 2 {
			return awaiting("START", greeting("Ann", gold), false, "PAY", "Buy a rope", "ASK", "Talk to Elara")
		}
		return awaiting("START", greeting("Ann", gold), false, "ASK", "Talk to Elara")
	}
	atStartMet := awaiting("START", greeting("Ann", 2), false, "PAY", "Buy a rope", "ASK", "Talk to Elara",
		"DEPART", "Leave")
	started := step(start("", ""), events(enter("START"), ready("START")), atStart(1))
	asked := step(choose("ASK"), events(signal("left_start", expr.IntValue(1)), exit("START"),
		signal("asking", expr.BoolValue(true)), enter("CHAT"), signal("chat_enter", expr.IntValue(2)),
		ready("CHAT")), awaiting("CHAT", "Elara is calm. Trust: 2.", true, "BACK", ""))
	back := step(in(callAdvance), events(exit("CHAT"), enter("START"), ready("START")), atStartMet)
	s.scenarios = []scenarioJSON{
		{
			Note: "PRD §5.8.5: start() with neither a board nor a node starts at the project's entry: the node's " +
				"visit count, then its on-enter, then its choices, of which one whose condition is false is hidden",
			Steps: []stepJSON{withState(started, s.state("PAGE", "START", awaitingChoice,
				map[string]any{"CASH": 1}, nil, map[string]int{"START": 1}))},
		},
		{
			Note: "PRD §5.8.5: a choice runs the current node's on-exit, then the connection's on-traverse, then " +
				"the target's visit count and on-enter, then its choices",
			Steps: []stepJSON{started, withState(asked, s.state("PAGE", "CHAT", awaitingChoice,
				map[string]any{"CASH": 1, "MET": true}, map[[2]string]any{{"KEEPER", "TRST"}: 2, {"KEEPER", "MD"}: "calm"},
				map[string]int{"START": 1, "CHAT": 1}))},
		},
		{
			Note: "advance() takes the one connection that leaves the node, which has no condition, and the next " +
				"node's choices are built from the state as it is when the flow arrives there",
			Steps: []stepJSON{started, asked, back},
		},
		{
			Note: "PRD §5.8.5: at a node that no connection leaves, the flow ends, a dead end: the node's ready, " +
				"then its on-exit, then the end, and the node stays the current one",
			Steps: []stepJSON{started, asked, back, withState(step(choose("PAY"), events(
				signal("left_start", expr.IntValue(2)), exit("START"), enter("MARKET"), ready("MARKET"),
				signal("market_exit"), exit("MARKET"), end(ReasonDeadEnd)), ended("MARKET", "You buy a rope.")),
				s.state("PAGE", "MARKET", endedPhase, map[string]any{"CASH": 0, "MET": true},
					map[[2]string]any{{"KEEPER", "TRST"}: 2, {"KEEPER", "MD"}: "calm"},
					map[string]int{"START": 2, "CHAT": 1, "MARKET": 1}))},
		},
		{
			Note: "PRD §5.8.5: at an ending, the flow ends after the node's on-exit, though a connection leaves it",
			Steps: []stepJSON{started, asked, back, step(choose("DEPART"), events(
				signal("left_start", expr.IntValue(2)), exit("START"), enter("BYE"), ready("BYE"),
				signal("bye_exit"), exit("BYE"), end(ReasonEnding)), ended("BYE", "Goodbye, Ann."))},
		},
		{
			Note: "PRD §5.8.5: at a node whose connections' conditions are all false, the flow ends with " +
				"no_choices, after its on-exit; goto() runs the current node's on-exit, but no connection's " +
				"on-traverse",
			Steps: []stepJSON{started, step(gotoNode(bd("PAGE"), nd("GATED")), events(
				signal("left_start", expr.IntValue(1)), exit("START"), enter("GATED"), ready("GATED"),
				signal("gated_exit"), exit("GATED"), end(ReasonNoChoices)), ended("GATED", "A locked door."))},
		},
		{
			Note: "a choice that is not one of the current node's is invalid_choice, and advance() at a node " +
				"that several connections leave is cannot_advance, and neither changes anything",
			Steps: []stepJSON{started,
				step(choose("PAY"), events(fault(InvalidChoice, "", "")), atStart(1)),
				step(in(callAdvance), events(fault(CannotAdvance, "", "")), atStart(1)),
			},
		},
		{
			Note: "once the flow ended, a choice is invalid_choice and an advance cannot_advance, and goto() " +
				"does not run the on-exit of the node that it ended at again",
			Steps: []stepJSON{started,
				step(gotoNode(bd("PAGE"), nd("BYE")), events(signal("left_start", expr.IntValue(1)), exit("START"),
					enter("BYE"), ready("BYE"), signal("bye_exit"), exit("BYE"), end(ReasonEnding)),
					ended("BYE", "Goodbye, Ann.")),
				step(choose("REPEAT"), events(fault(InvalidChoice, "", "")), ended("BYE", "Goodbye, Ann.")),
				step(in(callAdvance), events(fault(CannotAdvance, "", "")), ended("BYE", "Goodbye, Ann.")),
				step(gotoNode(bd("PAGE"), nd("START")), events(enter("START"), ready("START")), atStart(2)),
			},
		},
		{
			Note: "FR-PLAY-02 AC1: a value set while the flow awaits a choice shows in the text at once, and the " +
				"choices stay those that were built when the flow arrived at the node",
			Steps: []stepJSON{started,
				step(setVariable("CASH", 5), events(), awaiting("START", greeting("Ann", 5), false, "ASK",
					"Talk to Elara")),
				step(setVariable("NAME", "Bo"), events(), awaiting("START", greeting("Bo", 5), false, "ASK",
					"Talk to Elara")),
				step(choose("ASK"), events(signal("left_start", expr.IntValue(5)), exit("START"),
					signal("asking", expr.BoolValue(true)), enter("CHAT"), signal("chat_enter", expr.IntValue(2)),
					ready("CHAT")), awaiting("CHAT", "Elara is calm. Trust: 2.", true, "BACK", "")),
				step(setAttribute("KEEPER", "TRST", 7), events(), awaiting("CHAT", "Elara is calm. Trust: 7.", true,
					"BACK", "")),
				step(setAttribute("KEEPER", "MD", "angry"), events(), awaiting("CHAT", "Elara is angry. Trust: 7.",
					true, "BACK", "")),
			},
		},
		{
			Note: "PRD §5.8.5: start() at a node, or at a board's entry node, does not run the current node's " +
				"on-exit, wherever the flow is",
			Steps: []stepJSON{
				step(start("", nd("CHAT")), events(enter("CHAT"), signal("chat_enter", expr.IntValue(2)),
					ready("CHAT")), awaiting("CHAT", "Elara is calm. Trust: 2.", true, "BACK", "")),
				step(start(bd("PAGE"), ""), events(enter("START"), ready("START")), atStart(1)),
			},
		},
		{
			Note: "a start or a goto of a board or a node that the snapshot does not have, or of a node of " +
				"another board than the one that it names, is unknown_node, and moves the flow nowhere",
			Steps: []stepJSON{
				step(start("", nd("NADA")), events(fault(UnknownNode, "", "")), notStarted),
				started,
				step(gotoNode(bd("NADA"), ""), events(fault(UnknownNode, "", "")), atStart(1)),
				step(gotoNode("", nd("NADA")), events(fault(UnknownNode, "", "")), atStart(1)),
				step(gotoNode(bd("NADA"), nd("START")), events(fault(UnknownNode, "", "")), atStart(1)),
			},
		},
		{
			Note: "FR-GODOT-08 AC2: in strict mode, a call that reports a runtime error runs as it runs in " +
				"non-strict mode, and then halts the flow: a choice, an advance, and a goto report flow_halted " +
				"and change nothing, until start() clears the halt",
			Strict: true,
			Steps: []stepJSON{started,
				step(choose("PAY"), events(fault(InvalidChoice, "", "")), halted(atStart(1))),
				step(choose("ASK"), events(fault(FlowHalted, "", "")), halted(atStart(1))),
				step(in(callAdvance), events(fault(FlowHalted, "", "")), halted(atStart(1))),
				step(gotoNode(bd("PAGE"), nd("BYE")), events(fault(FlowHalted, "", "")), halted(atStart(1))),
				step(start("", ""), events(enter("START"), ready("START")), atStart(2)),
			},
		},
	}
	return s
}

// branchesSuite is of branches: each kind of output, a picked output's
// connections, the ends that a branch has, cycles and chains of branches,
// and start and goto of one.
func branchesSuite() *suite {
	s := &suite{
		name: "branches", code: "C2", project: "Branches",
		variables: []fixtureVar{{id: "PTS", key: "score", typ: value.Int, def: 0}},
		boards: []fixtureBoard{{id: "RANK", slug: "roll", name: "Roll", entry: "START",
			nodes: []fixtureNode{
				{id: "START", typ: element, text: "Roll the dice."},
				{id: "GATE", typ: branch, onEnter: `signal("gate_enter", score)`, onExit: `signal("gate_exit")`,
					outputs: []fixtureOutput{{id: "GH", kind: published.OutputIf, condition: "score >= 10"},
						{id: "GM", kind: published.OutputElseIf, condition: "score >= 5"},
						{id: "GE", kind: published.OutputElse}}},
				{id: "BEST", typ: element, ending: true, text: "You win with {score}."},
				{id: "EVEN", typ: element, ending: true, text: "A draw."},
				{id: "BAD", typ: element, ending: true, text: "You lose."},
				{id: "NEB", typ: branch, onExit: `signal("neb_exit")`,
					outputs: []fixtureOutput{{id: "NEA", kind: published.OutputIf, condition: "score > 0"}}},
				{id: "NCB", typ: branch, onExit: `signal("ncb_exit")`,
					outputs: []fixtureOutput{{id: "NCA", kind: published.OutputIf, condition: "true"}}},
				{id: "AFB", typ: branch, onExit: `signal("afb_exit")`,
					outputs: []fixtureOutput{{id: "AFF", kind: published.OutputIf, condition: "score > 100"},
						{id: "AFE", kind: published.OutputElse}}},
				{id: "CYC", typ: branch, onEnter: "score += 1",
					outputs: []fixtureOutput{{id: "CYA", kind: published.OutputIf, condition: "score < 3"},
						{id: "CYE", kind: published.OutputElse}}},
				{id: "END", typ: element, ending: true, text: "Looped {score} times."},
				{id: "SPN", typ: branch, outputs: []fixtureOutput{{id: "SPT", kind: published.OutputIf, condition: "true"}}},
				{id: "CHA", typ: branch, outputs: []fixtureOutput{{id: "CAT", kind: published.OutputIf, condition: "true"}}},
				{id: "CHB", typ: branch, outputs: []fixtureOutput{{id: "CBT", kind: published.OutputIf, condition: "true"}}},
			},
			connections: []fixtureConnection{
				{id: "HGH", from: "START", to: "GATE", label: "High", onTraverse: "score = 10"},
				{id: "MDD", from: "START", to: "GATE", label: "Mid", onTraverse: "score = 5"},
				{id: "SMA", from: "START", to: "GATE", label: "Low", onTraverse: "score = 1"},
				{id: "NEC", from: "START", to: "NEB", label: "No else"},
				{id: "NCC", from: "START", to: "NCB", label: "Unconnected"},
				{id: "AFC", from: "START", to: "AFB", label: "All false"},
				{id: "RPT", from: "START", to: "CYC", label: "Loop"},
				{id: "FVR", from: "START", to: "SPN", label: "Forever"},
				{id: "CHN", from: "START", to: "CHA", label: "Chain"},
				{id: "SKP", from: "GATE", port: "GH", to: "BAD", condition: "score > 100",
					onTraverse: `signal("skipped")`},
				{id: "TKN", from: "GATE", port: "GH", to: "BEST", onTraverse: `signal("taken")`},
				{id: "NVR", from: "GATE", port: "GH", to: "BAD", condition: "score / 0 > 0"},
				{id: "GMC", from: "GATE", port: "GM", to: "EVEN"},
				{id: "GEC", from: "GATE", port: "GE", to: "BAD"},
				{id: "NEX", from: "NEB", port: "NEA", to: "BEST"},
				{id: "AF1", from: "AFB", port: "AFE", to: "BEST", condition: "score > 0"},
				{id: "AF2", from: "AFB", port: "AFE", to: "BAD", condition: "score < 0"},
				{id: "CYB", from: "CYC", port: "CYA", to: "CYC"},
				{id: "CYD", from: "CYC", port: "CYE", to: "END"},
				{id: "SPB", from: "SPN", port: "SPT", to: "SPN"},
				{id: "CAB", from: "CHA", port: "CAT", to: "CHB"},
				{id: "CBB", from: "CHB", port: "CBT", to: "BEST"},
			}}},
		entry: [2]string{"RANK", "START"},
	}
	started := step(start("", ""), events(enter("START"), ready("START")), awaiting("START", "Roll the dice.",
		false, "HGH", "High", "MDD", "Mid", "SMA", "Low", "NEC", "No else", "NCC", "Unconnected", "AFC",
		"All false", "RPT", "Loop", "FVR", "Forever", "CHN", "Chain"))
	// spin are the events of a pass round the cycle of the branch SPN.
	spin := events(enter("SPN"))
	for range MaxPasses {
		spin = append(spin, exit("SPN"), enter("SPN"))
	}
	spin = append(events(exit("START")), append(spin, fault(FlowLoop, nd("SPN"), ""), ready("SPN"), exit("SPN"),
		end(ReasonDeadEnd))...)
	s.scenarios = []scenarioJSON{
		{
			Note: "FR-EDIT-04: a branch picks its first output whose condition holds, and goes on by itself by " +
				"the first connection of the output whose condition holds, without evaluating the conditions of " +
				"the connections after it, of which one would report division_by_zero: the branch's on-exit, " +
				"then that connection's on-traverse, without awaiting a choice",
			Steps: []stepJSON{started, withState(step(choose("HGH"), events(exit("START"), enter("GATE"),
				signal("gate_enter", expr.IntValue(10)), signal("gate_exit"), exit("GATE"), signal("taken"),
				enter("BEST"), ready("BEST"), exit("BEST"), end(ReasonEnding)), ended("BEST", "You win with 10.")),
				s.state("RANK", "BEST", endedPhase, map[string]any{"PTS": 10}, nil,
					map[string]int{"START": 1, "GATE": 1, "BEST": 1}))},
		},
		{
			Note: "FR-EDIT-04: a branch picks its elseif when its if's condition does not hold",
			Steps: []stepJSON{started, step(choose("MDD"), events(exit("START"), enter("GATE"),
				signal("gate_enter", expr.IntValue(5)), signal("gate_exit"), exit("GATE"), enter("EVEN"),
				ready("EVEN"), exit("EVEN"), end(ReasonEnding)), ended("EVEN", "A draw."))},
		},
		{
			Note: "FR-EDIT-04: a branch picks its else when no other output's condition holds",
			Steps: []stepJSON{started, step(choose("SMA"), events(exit("START"), enter("GATE"),
				signal("gate_enter", expr.IntValue(1)), signal("gate_exit"), exit("GATE"), enter("BAD"),
				ready("BAD"), exit("BAD"), end(ReasonEnding)), ended("BAD", "You lose."))},
		},
		{
			Note: "FR-EDIT-04 AC1: at a branch whose conditions are all false and which has no else, the flow " +
				"ends, a dead end: the branch's ready, then its on-exit, then the end",
			Steps: []stepJSON{started, step(choose("NEC"), events(exit("START"), enter("NEB"), ready("NEB"),
				signal("neb_exit"), exit("NEB"), end(ReasonDeadEnd)), ended("NEB", ""))},
		},
		{
			Note: "at a branch whose picked output no connection leaves, the flow ends, a dead end",
			Steps: []stepJSON{started, step(choose("NCC"), events(exit("START"), enter("NCB"), ready("NCB"),
				signal("ncb_exit"), exit("NCB"), end(ReasonDeadEnd)), ended("NCB", ""))},
		},
		{
			Note: "at a branch whose picked output's connections' conditions are all false, the flow ends with " +
				"no_choices",
			Steps: []stepJSON{started, step(choose("AFC"), events(exit("START"), enter("AFB"), ready("AFB"),
				signal("afb_exit"), exit("AFB"), end(ReasonNoChoices)), ended("AFB", ""))},
		},
		{
			Note: "a cycle of branches goes round until a condition that its scripts change fails, each pass a " +
				"visit of the branch",
			Steps: []stepJSON{started, withState(step(choose("RPT"), events(exit("START"), enter("CYC"),
				exit("CYC"), enter("CYC"), exit("CYC"), enter("CYC"), exit("CYC"), enter("END"), ready("END"),
				exit("END"), end(ReasonEnding)), ended("END", "Looped 3 times.")),
				s.state("RANK", "END", endedPhase, map[string]any{"PTS": 3}, nil,
					map[string]int{"START": 1, "CYC": 3, "END": 1}))},
		},
		{
			Note: "a cycle of branches whose conditions always hold ends once the flow has passed through 100 " +
				"branches in one call: flow_loop, then a dead end at the branch",
			Steps: []stepJSON{started, withState(step(choose("FVR"), spin, ended("SPN", "")),
				s.state("RANK", "SPN", endedPhase, nil, nil, map[string]int{"START": 1, "SPN": MaxPasses + 1}))},
		},
		{
			Note: "a chain of branches passes through each, and the flow comes to rest at the element after them",
			Steps: []stepJSON{started, step(choose("CHN"), events(exit("START"), enter("CHA"), exit("CHA"),
				enter("CHB"), exit("CHB"), enter("BEST"), ready("BEST"), exit("BEST"), end(ReasonEnding)),
				ended("BEST", "You win with 0."))},
		},
		{
			Note: "start() at a branch, and goto() of one, pass through it as a connection to it does",
			Steps: []stepJSON{
				step(start("", nd("GATE")), events(enter("GATE"), signal("gate_enter", expr.IntValue(0)),
					signal("gate_exit"), exit("GATE"), enter("BAD"), ready("BAD"), exit("BAD"), end(ReasonEnding)),
					ended("BAD", "You lose.")),
				withState(step(gotoNode("", nd("GATE")), events(enter("GATE"), signal("gate_enter", expr.IntValue(0)),
					signal("gate_exit"), exit("GATE"), enter("BAD"), ready("BAD"), exit("BAD"), end(ReasonEnding)),
					ended("BAD", "You lose.")),
					s.state("RANK", "BAD", endedPhase, nil, nil, map[string]int{"GATE": 2, "BAD": 2})),
			},
		},
	}
	return s
}

// jumpersSuite is of jumpers to the same board and to another, board entry
// and resetOnEnter, board-scoped variables of one key on two boards, start,
// goto, and reset, a jumper without a target, and load_state. Its snapshot
// is a draft, which alone may have a jumper without a target.
func jumpersSuite() *suite {
	s := &suite{
		name: "jumpers", code: "C3", project: "Jumpers", draft: true,
		variables: []fixtureVar{
			{id: "TRPS", key: "trips", typ: value.Int, def: 0},
			{id: "HTMP", key: "temp", typ: value.Int, def: 0, board: "HRB", reset: true},
			{id: "HKEEP", key: "keep", typ: value.Int, def: 0, board: "HRB"},
			{id: "BTMP", key: "temp", typ: value.Int, def: 0, board: "BEACN", reset: true},
			{id: "BKEEP", key: "keep", typ: value.Int, def: 0, board: "BEACN"},
		},
		boards: []fixtureBoard{
			{id: "HRB", slug: "harbour", name: "Harbour", entry: "HENTRY",
				nodes: []fixtureNode{
					{id: "HENTRY", typ: element, text: "Harbour: temp {temp}, keep {keep}.",
						onEnter: "temp += 1\nkeep += 1", onExit: `signal("harbour_exit")`},
					{id: "HSD", typ: element, text: "Side: temp {temp}.", onEnter: "temp += 10"},
					{id: "JSD", typ: jumper, target: [2]string{"HRB", "HSD"}},
					{id: "JBC", typ: jumper, target: [2]string{"BEACN", "BENTRY"}},
					{id: "JBRK", typ: jumper},
				},
				connections: []fixtureConnection{
					{id: "TSD", from: "HENTRY", to: "JSD", label: "Side"},
					{id: "TBC", from: "HENTRY", to: "JBC", label: "Beacon", onTraverse: "trips += 1"},
					{id: "TBRK", from: "HENTRY", to: "JBRK", label: "Broken", onTraverse: `signal("broken_traverse")`},
					{id: "SDB", from: "HSD", to: "HENTRY"},
				}},
			{id: "BEACN", slug: "lighthouse", name: "Lighthouse", entry: "BENTRY",
				nodes: []fixtureNode{
					{id: "BENTRY", typ: element, text: "Lighthouse: temp {temp}, keep {keep}, trips {trips}.",
						onEnter: "temp += 1\nkeep += 1"},
					{id: "JHM", typ: jumper, target: [2]string{"HRB", "HENTRY"}},
				},
				connections: []fixtureConnection{{id: "THM", from: "BENTRY", to: "JHM", label: "Home"}}},
		},
		entry: [2]string{"HRB", "HENTRY"},
	}
	harbour := func(temp, keep int) viewJSON {
		return awaiting("HENTRY", fmt.Sprintf("Harbour: temp %d, keep %d.", temp, keep), false, "TSD", "Side", "TBC",
			"Beacon", "TBRK", "Broken")
	}
	beacon := func(temp, keep, trips int) viewJSON {
		return awaiting("BENTRY", fmt.Sprintf("Lighthouse: temp %d, keep %d, trips %d.", temp, keep, trips), true,
			"THM", "Home")
	}
	started := step(start("", ""), events(enter("HENTRY"), ready("HENTRY")), harbour(1, 1))
	toBeacon := events(signal("harbour_exit"), exit("HENTRY"), enter("BENTRY"), ready("BENTRY"))
	saved := s.state("BEACN", "BENTRY", awaitingChoice,
		map[string]any{"TRPS": 7, "HTMP": 5, "HKEEP": 3, "BTMP": 4, "BKEEP": 2}, nil,
		map[string]int{"HENTRY": 3, "BENTRY": 2})
	s.scenarios = []scenarioJSON{
		{
			Note: "PRD §5.8.5, FR-EDIT-05 AC3, FR-VAR-02 AC3 and AC4: a jumper to another board is replaced by " +
				"its target: the node's on-exit, the connection's on-traverse, then the board entry, which " +
				"resets the board's resetOnEnter variables and no other, then the target's visit count and " +
				"on-enter; the jumper is never entered",
			Steps: []stepJSON{started,
				step(choose("TBC"), toBeacon, beacon(1, 1, 1)),
				step(choose("THM"), events(exit("BENTRY"), enter("HENTRY"), ready("HENTRY")), harbour(1, 2)),
				withState(step(choose("TBC"), toBeacon, beacon(1, 2, 2)), s.state("BEACN", "BENTRY", awaitingChoice,
					map[string]any{"TRPS": 2, "HTMP": 1, "HKEEP": 2, "BTMP": 1, "BKEEP": 2}, nil,
					map[string]int{"HENTRY": 2, "BENTRY": 2})),
			},
		},
		{
			Note: "PRD §5.8.5: a jumper to a node of its own board is no board entry, so that the board's " +
				"resetOnEnter variables keep their values",
			Steps: []stepJSON{started,
				step(choose("TSD"), events(signal("harbour_exit"), exit("HENTRY"), enter("HSD"), ready("HSD")),
					awaiting("HSD", "Side: temp 11.", true, "SDB", "")),
				step(in(callAdvance), events(exit("HSD"), enter("HENTRY"), ready("HENTRY")), harbour(12, 2)),
			},
		},
		{
			Note:  "PRD §5.8.5: start() at a jumper starts the flow at its target, as a board entry",
			Steps: []stepJSON{step(start("", nd("JBC")), events(enter("BENTRY"), ready("BENTRY")), beacon(1, 1, 0))},
		},
		{
			Note: "PRD §5.8.5: goto() of a jumper runs the current node's on-exit, then enters the jumper's " +
				"target as a board entry, though the target is of the current board",
			Steps: []stepJSON{started, step(gotoNode("", nd("JHM")), events(signal("harbour_exit"), exit("HENTRY"),
				enter("HENTRY"), ready("HENTRY")), harbour(1, 2))},
		},
		{
			Note: "PRD §5.8.5: start() of the current board is a board entry too, and runs no on-exit",
			Steps: []stepJSON{started,
				step(choose("TSD"), events(signal("harbour_exit"), exit("HENTRY"), enter("HSD"), ready("HSD")),
					awaiting("HSD", "Side: temp 11.", true, "SDB", "")),
				step(start(bd("HRB"), ""), events(enter("HENTRY"), ready("HENTRY")), harbour(1, 2)),
			},
		},
		{
			Note: "a choice of a jumper without a target, which only a draft has, is jumper_broken: the " +
				"connection's on-traverse does not run, and the flow ends at the node, a dead end, after its " +
				"on-exit",
			Steps: []stepJSON{started, withState(step(choose("TBRK"), events(fault(JumperBroken, nd("JBRK"), ""),
				signal("harbour_exit"), exit("HENTRY"), end(ReasonDeadEnd)),
				ended("HENTRY", "Harbour: temp 1, keep 1.")), s.state("HRB", "HENTRY", endedPhase,
				map[string]any{"HTMP": 1, "HKEEP": 1}, nil, map[string]int{"HENTRY": 1}))},
		},
		{
			Note: "start() or goto() of a jumper without a target is jumper_broken, and moves the flow nowhere, " +
				"without the current node's on-exit",
			Steps: []stepJSON{
				step(start("", nd("JBRK")), events(fault(JumperBroken, nd("JBRK"), "")), notStarted),
				started,
				step(gotoNode("", nd("JBRK")), events(fault(JumperBroken, nd("JBRK"), "")), harbour(1, 1)),
			},
		},
		{
			Note: "PRD §5.8.5: reset() sets every value to the snapshot's and clears every visit count, then " +
				"starts the flow at the project's entry, without the current node's on-exit",
			Steps: []stepJSON{started, step(choose("TBC"), toBeacon, beacon(1, 1, 1)),
				withState(step(in(callReset), events(enter("HENTRY"), ready("HENTRY")), harbour(1, 1)),
					s.state("HRB", "HENTRY", awaitingChoice, map[string]any{"HTMP": 1, "HKEEP": 1}, nil,
						map[string]int{"HENTRY": 1})),
			},
		},
		{
			Note: "PRD §5.8.5: load_state() is no board entry: it sets the values and the cursor without the " +
				"current node's on-exit, or the cursor node's visit count or on-enter, and builds the cursor " +
				"node's choices again",
			Steps: []stepJSON{started,
				withState(step(loadState(saved), events(), beacon(4, 2, 7)), saved),
				step(choose("THM"), events(exit("BENTRY"), enter("HENTRY"), ready("HENTRY")), harbour(1, 4)),
			},
		},
		{
			Note: "load_state() of a state whose flow ended leaves it ended, and a goto() after it does not run " +
				"the on-exit of the node that it ended at",
			Steps: []stepJSON{
				step(loadState(s.state("HRB", "HENTRY", endedPhase, map[string]any{"HTMP": 2, "HKEEP": 2}, nil,
					map[string]int{"HENTRY": 2})), events(), ended("HENTRY", "Harbour: temp 2, keep 2.")),
				step(gotoNode(bd("BEACN"), ""), events(enter("BENTRY"), ready("BENTRY")), beacon(1, 1, 0)),
			},
		},
	}
	return s
}

// valuesSuite is of the numeric and string semantics of PRD §5.8.9 in
// scripts and texts: type coercion, division by zero, overflow, conversions,
// strings, their length's bound, format specifiers, the built-ins, and
// strict mode of a runtime error of an expression.
func valuesSuite() *suite {
	s := &suite{
		name: "values", code: "C4", project: "Values",
		variables: []fixtureVar{
			{id: "N1", key: "i", typ: value.Int, def: 0},
			{id: "F1", key: "f", typ: value.Float, def: 0.0},
			{id: "S1", key: "s", typ: value.String, def: ""},
			{id: "B1", key: "ok", typ: value.Bool, def: false},
			{id: "MAX", key: "big", typ: value.Int, def: 9007199254740991},
			{id: "R1", key: "r", typ: value.Float, def: 0.0},
			{id: "G1", key: "g", typ: value.Float, def: 0.0},
			{id: "H1", key: "h", typ: value.Float, def: 0.0},
		},
		boards: []fixtureBoard{{id: "MATH", slug: "calc", name: "Calc", entry: "CAST",
			nodes: []fixtureNode{
				{id: "CAST", typ: element, ending: true, text: "f={f} i={i} s={s} ok={ok}",
					onEnter: "f = 1\nf += 2\ni = to_int(f) + 1\ns = to_string(f) + \"/\" + to_string(i)\n" +
						"ok = i == 4 and f == 3"},
				{id: "DVZ", typ: element, ending: true, text: "i={i} f={f} r={r} ok={ok}",
					onEnter: "i = 7 / 0 + 5\nf = 1.0 / 0.0 + 0.5\nr = to_float(i % 0)\nok = true"},
				{id: "BEYND", typ: element, ending: true, text: "i={i} s={s} ok={ok} f={f}",
					onEnter: "i = big + 1\ns = to_string(4294967297 * 4294967296)\nok = to_int(1.0e300) == 0\n" +
						"i = -big - 1 + 5\nf = to_float(big) + 1.0"},
				{id: "PARSE", typ: element, ending: true, text: "i={i} f={f} ok={ok} s={s} r={r}",
					onEnter: `i = to_int("3abc")
f = to_float(" 3")
ok = to_float("1e400") == 0.0
s = to_string(round(-2.5)) + " " + to_string(to_int(-2.7)) + " " + to_string(to_float("9007199254740993"))
r = to_float("2.2250738585072011e-308")`},
				{id: "TEXT", typ: element, ending: true, text: "i={i} s={s} ok={ok}",
					onEnter: `i = len("😀é")
s = upper("ßa") + lower("İ") + trim("\u00A0x\t")
ok = "\uFFFF" < "😀" and "a" != "A" and contains("😀é", "é")`},
				{id: "JNR", typ: element, ending: true, text: "i={i} ok={ok} s=[{s}]",
					onEnter: "s = \"abcdefghijklmnop\"\ns = s + s\ns = s + s\ns = s + s\ns = s + s\ns = s + s\n" +
						"s = s + s\ns = s + s\ns = s + s\ni = len(s)\ns = s + \"!\"\nok = s == \"\""},
				{id: "FMT", typ: element, ending: true, text: "{f:2} {r} {i:2} {f:0} {g} {g:1} {h:2} {h}",
					onEnter: "f = 1.005\nr = 0.1 + 0.2\ni = 5\ng = 1000000000.0 * 1000000000000.0\nh = -0.001"},
				{id: "TRNC", typ: element, ending: true, text: "i={i} s={s} f={f}",
					onEnter: "i = -7 / 2\ns = to_string(-7 % 2)\nf = -7 / 2.0"},
				{id: "FNS", typ: element, ending: true, text: "f={f} i={i} ok={ok} s={s} r={r}",
					onEnter: "f = min(1, 2.5)\ni = clamp(5, 10, 0) + max(-2, -3) + abs(-7)\n" +
						"ok = contains(\"harbour\", \"bou\") and starts_with(\"harbour\", \"har\") and " +
						"ends_with(\"harbour\", \"our\")\n" +
						"s = to_string(floor(2.5)) + to_string(ceil(2.5)) + to_string(abs(-1.5))\n" +
						"r = clamp(0.5, 0, 1) + to_float(len(\"abc\"))"},
				{id: "STRCT", typ: element, text: "ok={ok}", onEnter: "i = 1 / 0\nok = true"},
				{id: "AFTER", typ: element, ending: true, text: "After."},
			},
			connections: []fixtureConnection{{id: "NXT", from: "STRCT", to: "AFTER"}}}},
		entry: [2]string{"MATH", "CAST"},
	}
	// endsAt returns the events of a start at the ending n, with the errors
	// of its on-enter between its enter and its ready.
	endsAt := func(n string, errors ...string) []eventJSON {
		es := events(enter(n))
		for _, code := range errors {
			es = append(es, fault(semantics.RuntimeError(code), nd(n), ExpressionOnEnter))
		}
		return append(es, ready(n), exit(n), end(ReasonEnding))
	}
	run := func(note, node, text string, errors ...string) scenarioJSON {
		return scenarioJSON{Note: note, Steps: []stepJSON{step(start("", nd(node)), endsAt(node, errors...),
			ended(node, text))}}
	}
	castText := "f=3.0 i=4 s=3.0/4 ok=true"
	strictAt := events(enter("STRCT"), fault(semantics.DivisionByZero, nd("STRCT"), ExpressionOnEnter),
		ready("STRCT"))
	atStrict := awaiting("STRCT", "ok=true", true, "NXT", "")
	s.scenarios = []scenarioJSON{
		{
			Note: "PRD §5.8.3: an int is widened where a float is read, and to_int, to_string, and == of an int " +
				"and a float convert and compare as the language types them",
			Steps: []stepJSON{withState(step(start("", ""), endsAt("CAST"), ended("CAST", castText)),
				s.state("MATH", "CAST", endedPhase, map[string]any{"F1": 3.0, "N1": 4, "S1": "3.0/4", "B1": true},
					nil, map[string]int{"CAST": 1}))},
		},
		run("PRD §5.8.3, §5.8.7: a division or a modulo by zero is division_by_zero, and yields its type's "+
			"zero value, which the expression and the script go on with", "DVZ", "i=5 f=0.5 r=0.0 ok=true",
			"division_by_zero", "division_by_zero", "division_by_zero"),
		run("PRD §5.8.9: an int beyond ±(2^53 − 1) is integer_overflow and yields 0, a product checked "+
			"before it multiplies, a float's conversion before it converts, and an int widens to a float "+
			"exactly", "BEYND", "i=5 s=0 ok=true f=9007199254740992.0",
			"integer_overflow", "integer_overflow", "integer_overflow", "integer_overflow"),
		run("PRD §5.8.9: to_int and to_float read their exact grammars, conversion_failed beyond them, "+
			"float_not_finite beyond a double, correctly rounded, and round rounds half away from zero",
			"PARSE", "i=0 f=0.0 ok=true s=-3 -2 9007199254740992.0 r=2.225073858507201e-308",
			"conversion_failed", "conversion_failed", "float_not_finite"),
		run("PRD §5.8.9: strings are code points, cased by simple mappings and trimmed of White_Space by the "+
			"pinned tables, and compared by code point", "TEXT", "i=2 s=ßAix ok=true"),
		run("PRD §5.8.9: a string of more than 4,096 characters is string_too_long and yields the empty "+
			"string", "JNR", "i=4096 ok=true s=[]", "string_too_long"),
		run("FR-TEXT-02, PRD §5.8.9: a text's value is shown as to_string writes it, and with a format "+
			"specifier's digits rounded half away from zero, in positional notation, without the sign of a "+
			"value that rounds to zero", "FMT", "1.01 0.30000000000000004 5.00 1 1e+21 "+
			"1000000000000000000000.0 0.00 -0.001"),
		run("PRD §5.8.9: an int's / truncates toward zero, and % takes the sign of the dividend", "TRNC",
			"i=-3 s=-1 f=-3.5"),
		run("PRD §5.8.4: min, max, abs, and clamp are of their arguments' widened type, clamp is "+
			"min(max(x, lo), hi), and floor, ceil, len, contains, starts_with, and ends_with", "FNS",
			"f=1.0 i=5 ok=true s=231.5 r=3.5"),
		{
			Note: "FR-GODOT-08 AC2: in non-strict mode, the flow goes on after a runtime error of an " +
				"expression with its documented fallback, the zero value",
			Steps: []stepJSON{
				step(start("", nd("STRCT")), strictAt, atStrict),
				step(in(callAdvance), events(exit("STRCT"), enter("AFTER"), ready("AFTER"), exit("AFTER"),
					end(ReasonEnding)), ended("AFTER", "After.")),
			},
		},
		{
			Note: "FR-GODOT-08 AC2: in strict mode, a runtime error of an expression halts the flow once its " +
				"call has run as in non-strict mode, until reset() clears the halt",
			Strict: true,
			Steps: []stepJSON{
				step(start("", nd("STRCT")), strictAt, halted(atStrict)),
				step(in(callAdvance), events(fault(FlowHalted, "", "")), halted(atStrict)),
				step(in(callReset), endsAt("CAST"), ended("CAST", castText)),
				step(gotoNode("", nd("STRCT")), strictAt, halted(atStrict)),
			},
		},
	}
	return s
}

// visitsSuite is of visited() and visits(), which the visit counts that
// every entry of a node increments, a connection of a node to itself and a
// start and a goto among them, give, and reset() clears.
func visitsSuite() *suite {
	hub, asked := nd("HVB"), nd("ASKED")
	s := &suite{
		name: "visits", code: "C5", project: "Visits",
		variables: []fixtureVar{{id: "V", key: "seen", typ: value.Int, def: 0}},
		boards: []fixtureBoard{{id: "MEET", slug: "meet", name: "Meeting", entry: "HVB",
			nodes: []fixtureNode{
				{id: "HVB", typ: element, text: "Visit {seen}.", onEnter: `seen = visits(node("` + hub + `"))`},
				{id: "ASKED", typ: element, text: "She tells you of the light."},
				{id: "AWAY", typ: element, ending: true, text: "You leave after {seen} visits."},
			},
			connections: []fixtureConnection{
				{id: "ASK", from: "HVB", to: "ASKED", label: "Ask", condition: `not visited(node("` + asked + `"))`},
				{id: "STAY", from: "HVB", to: "HVB", label: "Wait", condition: `visits(node("` + hub + `")) < 3`},
				{id: "DEPART", from: "HVB", to: "AWAY", label: "Leave"},
				{id: "RET", from: "ASKED", to: "HVB"},
			}}},
		entry: [2]string{"MEET", "HVB"},
	}
	visit := func(n int, choices ...string) viewJSON {
		return awaiting("HVB", fmt.Sprintf("Visit %d.", n), false, choices...)
	}
	all := []string{"ASK", "Ask", "STAY", "Wait", "DEPART", "Leave"}
	started := step(start("", ""), events(enter("HVB"), ready("HVB")), visit(1, all...))
	again := events(exit("HVB"), enter("HVB"), ready("HVB"))
	s.scenarios = []scenarioJSON{
		{
			Note: "PRD §5.8.4: visited() and visits() read the visit counts, which the flow's entry of a node " +
				"increments before its on-enter, though a connection of the node to itself is how it enters it",
			Steps: []stepJSON{started,
				step(choose("STAY"), again, visit(2, all...)),
				step(choose("STAY"), again, visit(3, "ASK", "Ask", "DEPART", "Leave")),
				step(choose("ASK"), events(exit("HVB"), enter("ASKED"), ready("ASKED")),
					awaiting("ASKED", "She tells you of the light.", true, "RET", "")),
				step(in(callAdvance), events(exit("ASKED"), enter("HVB"), ready("HVB")), visit(4, "DEPART", "Leave")),
				withState(step(choose("DEPART"), events(exit("HVB"), enter("AWAY"), ready("AWAY"), exit("AWAY"),
					end(ReasonEnding)), ended("AWAY", "You leave after 4 visits.")),
					s.state("MEET", "AWAY", endedPhase, map[string]any{"V": 4}, nil,
						map[string]int{"HVB": 4, "ASKED": 1, "AWAY": 1})),
			},
		},
		{
			Note: "start() and goto() count a visit of the node that they enter",
			Steps: []stepJSON{started,
				step(gotoNode("", hub), again, visit(2, all...)),
				step(start("", hub), events(enter("HVB"), ready("HVB")), visit(3, "ASK", "Ask", "DEPART", "Leave")),
			},
		},
		{
			Note: "reset() clears the visit counts that visited() and visits() read",
			Steps: []stepJSON{started,
				step(choose("ASK"), events(exit("HVB"), enter("ASKED"), ready("ASKED")),
					awaiting("ASKED", "She tells you of the light.", true, "RET", "")),
				step(in(callAdvance), events(exit("ASKED"), enter("HVB"), ready("HVB")),
					visit(2, "STAY", "Wait", "DEPART", "Leave")),
				step(in(callReset), events(enter("HVB"), ready("HVB")), visit(1, all...)),
			},
		},
	}
	return s
}

// in returns the input of a call without arguments.
func in(call string) inputJSON {
	return inputJSON{Call: call}
}
