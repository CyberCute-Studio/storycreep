# Conformance suite

Scenarios of a narrative's flow that every runtime of Storycreep's published format runs, so that the Go reference runtime, play mode's TypeScript engine, and the Godot addon's GDScript runtime give the same node sequence, text, choices, signals, and state for the same inputs (PRD §8.6, FR-PLAY-06). A third-party runtime that passes every scenario conforms.

The Go package `server/internal/flow` is the reference runtime, and runs every scenario (`TestConformance`); the TypeScript engine (plan item 44) and the GDScript runtime (item 103) run them too. PRD §5.8.5 is the contract of the flow, and §5.8.3–§5.8.9 of its expressions; this README describes the files, what a scenario observes, and how a runner compares it.

## The files

Each directory is a suite: a project's snapshot, and scenarios of it.

| Suite | Its scenarios are of |
| --- | --- |
| [`choices/`](choices/) | An element's choices and their conditions; the order of on-exit, on-traverse, and on-enter; an ending, a dead end, and no choices; `advance`, `start`, and `goto`; values set while the flow awaits a choice; a component's runtime attributes and a mention; calls that do nothing; and strict mode. |
| [`branches/`](branches/) | Branches: `if`, `elseif`, and `else`, a picked output's connections, the ends that a branch has, cycles and chains of branches, and `start` and `goto` of one. |
| [`jumpers/`](jumpers/) | Jumpers to the same board and to another, board entry and `resetOnEnter`, board-scoped variables of one key on two boards, `start`, `goto`, and `reset`, a jumper without a target, of an element's connection and of a branch's, and `load_state`. Its snapshot is a draft, which alone may have a jumper without a target. |
| [`values/`](values/) | The numeric and string semantics of PRD §5.8.9 in scripts and texts: type coercion, division by zero, overflow, conversions, strings and their bound, format specifiers, the built-ins, and strict mode of an expression's runtime error. |
| [`visits/`](visits/) | `visited()` and `visits()`, and the visit counts that entering a node increments and `reset()` clears. |

- **`<suite>/snapshot/`** is a published snapshot, as a game's `res://storycreep/` holds one (PRD §6.3): its manifest, `storycreep.json`, which lists each other file with its hash, the project's files, and a file of each board. A runtime loads it as it loads any snapshot. Each file's hash is of its bytes, as it is written, so a runner reads the files as bytes and must not let a checkout change their line endings, which `.gitattributes` keeps as LF. The manifest's `contentHash` is the server's to define (plan item 78), and no runtime checks it: a fixture's is the hash of its files' paths and hashes.
- **`<suite>/scenarios.json`** holds the suite's scenarios (below).

The files are generated: the suites are written in Go, in [`server/internal/flow/suites_test.go`](../../server/internal/flow/suites_test.go), so that their expressions are compiled from their sources, as the server compiles them, and their entities are named by mnemonics, as `nd_000000000000000000000START`, a node whose ID's body is `START` after zeros. `go test ./internal/flow -run TestConformanceFiles -update`, in `server/`, writes them, and `go test ./...` fails when they are not what the suites write. Edit the suites, not the files: a file edited by hand no longer has its manifest's hash.

## A scenario

`scenarios.json` is an object whose `scenarios` each run a new runtime of the suite's snapshot:

```json
{
  "note": "PRD §5.8.5: start() with neither a board nor a node starts at the project's entry: the node's visit count, then its on-enter, then its choices, of which one whose condition is false is hidden",
  "steps": [
    {
      "events": [
        { "event": "enter", "node": "nd_000000000000000000000START" },
        { "event": "ready", "node": "nd_000000000000000000000START" }
      ],
      "input": { "call": "start" },
      "view": {
        "canAdvance": false,
        "choices": [{ "id": "cn_00000000000000000000000ASK", "text": "Talk to Elara" }],
        "halted": false,
        "node": "nd_000000000000000000000START",
        "phase": "awaiting_choice",
        "text": "Hello, Ann. You have 1 gold."
      }
    }
  ]
}
```

| Key | Contents |
| --- | --- |
| `note` | What the scenario checks, unique within its file. |
| `strict` | `true` for a runtime in strict mode (PRD §5.8.5, FR-GODOT-08), or absent for one in non-strict mode. |
| `steps` | The steps, in order, each a call of the runtime and what it observes after it. |

The runtime is new: each variable and each component's runtime attribute has its value in the snapshot, no node has been visited, and the flow has not started.

### A step

| Key | Contents |
| --- | --- |
| `input` | The call, below. |
| `events` | The events that the call emits, in order, `[]` for none (below). |
| `view` | What a game sees of the flow after the call (below). |
| `state` | Absent, or the state that `save_state()` returns after the call (below). |

### The calls

An `input` is its `call` and that call's arguments. Every ID is in its canonical spelling.

| `call` | Arguments | The runtime's call |
| --- | --- | --- |
| `start` | `board` and `node`, either or both absent | `start(board, node)`, with `""` for one that is absent: the node of the board, the board's entry node, or the project's entry. |
| `goto` | `board` and `node`, either or both absent | `goto(board, node)`, as `start`. |
| `reset` | | `reset()`. |
| `choose` | `choice`, a connection's ID | `choose(choice)`. |
| `advance` | | `advance()`. |
| `set_variable` | `variable`, a variable's ID, and `value` | `set_variable(variable, value)`, of the value typed by the variable's declaration. |
| `set_attribute` | `component` and `attribute`, a component's ID and an attribute definition's, and `value` | `set_attribute(component, attribute, value)`, of the value typed by the definition's declaration. |
| `load_state` | `state`, a save state | `load_state(state)`, of a state of the suite's snapshot (PRD §8.5), whose values its declarations type. |

A value is a JSON boolean, number, or string, typed by its declaration, as the published format's and a save state's are, never by its JSON form: `2` is a `float` for a `float` variable (PRD §5.8.9).

### The events

An event is its `event` and the fields of its kind, each one of the signals of PRD §8.4.3. A runtime may emit other signals too, as Godot's `variable_changed` or `choices_changed`, which a scenario does not observe: a runner compares only these kinds, in the order that the call emits them.

| `event` | Fields | Signal | When |
| --- | --- | --- | --- |
| `enter` | `node` | `node_entered` | The flow entered the node, after its visit count and before its on-enter. A jumper is never entered. |
| `ready` | `node` | `node_ready` | The flow came to rest at the node, after its on-enter: it awaits a choice there, or ends there, when `exit` and `end` follow. A branch that the flow passes through has none. |
| `exit` | `node` | `node_exited` | The flow left the node, after its on-exit. |
| `end` | `reason`: `ending`, `dead_end`, or `no_choices` | `flow_ended` | The flow ended, after the on-exit of the node that it ended at. |
| `signal` | `name`, and `value`, absent for none | `narrative_signal` | A script emitted the signal. Its value is its `type` and its `value`, as the expression corpus writes one, since no declaration types it, which a runner compares by the type and the value that the type reads, as it compares a state's values, so that `2` and `2.0` are one `float`. |
| `error` | `code`; `at`, absent for an error of the call itself; and `expression`, absent for none | `runtime_error` | A runtime error (PRD §5.8.7). `at` is the ID of the entity whose expression reported it, a node, a connection, or a branch's output, and `expression` that expression, as the published format names its field: `onEnter`, `onExit`, `onTraverse`, or `condition`; or `at` is the jumper without a target of a `jumper_broken`, or the branch of a `flow_loop`, without an `expression`. |

### The view

| Key | Contents |
| --- | --- |
| `node` | The current node, as `current_node()` returns it, or absent before the flow starts. The node that the flow ended at stays current. |
| `phase` | `awaiting_choice` or `ended`, as a save state's cursor's, or absent before the flow starts. |
| `text` | The current node's text, as `text()` returns it: its rich text's plain text, laid out as [`../rich-text/README.md`](../rich-text/README.md) says, each value written as `to_string` writes it, or a number with its format specifier's digits, and each mention as its component's name, with the values as they are after the call (PRD §5.8.5, FR-TEXT-02); `""` for a branch, an element without text, or before the flow starts. |
| `choices` | The current choices, in order, `[]` while the flow does not await a choice: each its connection's `id`, and its `text`, the connection's label as `text` lays out the node's, `""` without one. |
| `canAdvance` | What `can_advance()` returns. |
| `halted` | Whether strict mode halted the flow. |

### The state

A step's `state` is a save state (PRD §8.5, [`../save-state/README.md`](../save-state/README.md)): what `save_state()` returns after the call. A runner compares its own with it by value: each key, each value typed by its declaration, so that `2` and `2.0` are one `float`, the cursor, and the visit counts. A state holds the value of every variable and of every runtime attribute of every component, and the visit count of every node that the flow has entered.

## The flow

PRD §5.8.5 says how the flow runs, and the scenarios check each rule of it. These are the ones that a runtime most easily gets wrong:

- **Order.** A choice runs the current node's on-exit, `exit`, the connection's on-traverse, then the board entry, when the target is of another board, which resets that board's `resetOnEnter` variables, then the target's visit count, `enter`, its on-enter, and its choices, `ready`. At an ending, at a dead end, or where no choice holds, the flow ends after `ready`: the node's on-exit, `exit`, `end`.
- **Choices** are built once, when the flow arrives at a node, and by `load_state`: a value that `set_variable` changes afterwards shows in the text at once, but changes no choice.
- **Branches** pass through, by the first connection of their picked output whose condition holds, evaluating no condition of a connection after it, and get `ready` only when the flow ends there. A branch's outputs are one `if`, then any `elseif`s, then an `else`, if it has one, as the published format has them (FR-EDIT-04). A call passes through at most 100 branches: the branch that the flow would leave once it has left 100 in the call ends it, with `error` `flow_loop` and as a `dead_end`, but for one whose connection leads to a jumper without a target, which the flow does not leave, and which is `jumper_broken` alone.
- **Jumpers** are replaced by their targets before anything happens. One without a target is `error` `jumper_broken`: a connection to it ends the flow at the node that it leaves, a dead end, after that node's on-exit, without the connection's on-traverse, a branch then getting its `ready` before its on-exit, as at any end there; a `start` or a `goto` of it moves the flow nowhere, and so does a `reset` of a project whose entry it is, which sets no value either, though no suite's entry is one.
- **Board entry.** Every `start`, `goto`, and `reset` is one, though it stays on the current board; `load_state` is none, nor is a connection or a jumper within a board.
- **Calls that do nothing.** A `start` or a `goto` of a board or a node that the snapshot does not have, or of a node of another board than the one that it names, is `unknown_node`, as are a `start` of neither and a `reset` of a project without an entry, though every suite's snapshot has one; a `choose` of a connection that is not among the current choices `invalid_choice`; an `advance` that `can_advance()` refuses `cannot_advance`. None of them moves the flow, nor runs an on-exit.
- **Strict mode.** A call that reports a runtime error runs as in non-strict mode, and then halts the flow, so that the state is the same in both: until `start`, `reset`, or `load_state`, each `choose`, `advance`, and `goto` is `flow_halted` and does nothing.

The scenarios do not yet load a state saved against another snapshot, whose entities have changed since (PRD §8.5): plan item 105 adds them.
