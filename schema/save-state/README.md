# The save-state format

A save state is the narrative state that a runtime's `save_state()` returns and its `load_state()` reads (PRD §8.5, FR-GODOT-07): where the flow is, and the values of the variables and of the components' runtime attributes, and how many times the flow has entered each node, by ID, never by key, so that a save survives a rename (FR-COMP-03 AC3). It holds no text. It is versioned by its own `stateVersion`, of which only 1 exists, apart from the published format's `schemaVersion` (§6.4, FR-GODOT-07 AC2).

[`v1/save-state.schema.json`](v1/save-state.schema.json) is its JSON Schema, which [`server/internal/published`](../../server/internal/published) generates from its Go type, `SaveState`, as it generates the published format's documents, by the rules of [`../published/README.md`](../published/README.md): no `null` anywhere, a key that the schema does not define taken and ignored, and every ID in its canonical spelling. The web app's TypeScript types of it are `web/shared/src/save-state/v1.gen.ts`. Its `$id` names its version, as `…/schema/save-state/v1/save-state.schema.json`.

| Field | Holds |
| --- | --- |
| `stateVersion` | 1. A runtime refuses a state of a newer version than its own with `runtime_error(state_version_unsupported)`, and applies nothing. |
| `projectId` | The project's ID. A runtime refuses a state of another project unless `allow_cross_project` is set. |
| `snapshotId`, `snapshotVersion` | The snapshot that the state was saved against, which a state saved against a draft has neither of (§7.8). |
| `schemaVersion` | The published format's version of that snapshot. |
| `cursor` | Where the flow is: its current node, `nodeId`, of `boardId`, and its `phase`, `awaiting_choice` when the node's choices are built and the game chooses one, or advances by its one unconditional connection, or `ended` when the flow ended at the node, after its on-exit ran (§5.8.5). A state saved before the flow started has none, and a jumper, which is never the current node, is never a cursor's. |
| `variables` | Each variable's value, global and of each board, by its ID. |
| `attributes` | Each component's runtime attributes' values, by the component's ID and the attribute definition's. |
| `visits` | How many times the flow has entered each node that it has entered, by its ID, each at least 1. |

A game stores a state as it likes, and may read it back with every number a float, as Godot's `JSON.parse_string` reads it, so that `2` comes back as `2.0`: the schema takes an integral number wherever it takes an integer, as JSON Schema does, and a runtime types each value by its declaration, a variable's or an attribute definition's (§5.8.9), so that a state round-trips (FR-GODOT-07 AC3). The schema's second example is such a state. What a runtime does with a state whose entities have changed since it was saved, as a variable that no longer exists or whose type changed, is §8.5's.
