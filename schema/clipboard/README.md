# Clipboard

A clipboard is a selection of the nodes of one board of a project, as copied: the nodes, with a branch's outputs, the connections between them, and a declaration of each variable, component, and attribute definition that their texts, expressions, and speakers name (PRD FR-EDIT-07, plan note N58). `POST /api/v1/projects/{project_id}/clipboard/copy` answers with one, `POST /api/v1/projects/{project_id}/clipboard/resolve` reports what of one a paste onto a board would resolve to nothing, and the command `clipboard.paste` pastes one onto a board of the same project or of another of the workspace's. A client puts the clipboard on the system's clipboard as JSON, which its `format`, `storycreep.clipboard`, tells from other JSON.

| File | Contents |
| --- | --- |
| [`v1/clipboard.schema.json`](v1/clipboard.schema.json) | The JSON Schema of a clipboard of version 1, which the API's OpenAPI document gives it too. Its `$id` is the URL of the file on the main branch. `server/internal/clipboard` generates it from its types, and `go test ./...` fails when it is out of date; `go test ./internal/clipboard -run TestSchemaFile -update`, in `server/`, rewrites it. |

## Versions

A clipboard names its version in `version`. A change of what a clipboard holds, as a new field of a node, is a new version, with a schema of its own at a path of its version, as the published format's `schemaVersion` is (PRD §6.4). A server reads every version up to the one that it writes, and answers a newer one with 422 `validation_failed` at `clipboard.version`, so that a client that pastes a clipboard copied from a newer server learns why.

## What it holds

- The nodes, each once, in the order of their IDs, with every field of a node but its board, and its position, which a paste moves by its offset, so that the pasted nodes keep their places relative to each other. A branch holds its outputs in their order, its conditional outputs, the first its `if`, and its `else` after them.
- The connections that leave one of the nodes and enter one of them, in the order of the ports that they leave, by their nodes' IDs and then their outputs' IDs, an element's `out` first, and of each port's choices, which a paste keeps.
- A declaration of each variable, component, and attribute definition, not in the trash, that the nodes' and the connections' texts name by value references and mentions, that their expressions' names name at the clipboard's board, and that the elements' speakers are, as it was where it was copied: what a paste resolves it by, its ID, its key, and its scope, and what creating one in its place takes, as a variable's type, default, and description.

IDs are the IDs where the clipboard was copied. A paste gives every node, output, and connection a new ID, which the client makes, as it makes those of what it creates.

## How a paste resolves what the content names

A paste resolves each declaration at the board that it pastes onto:

- A variable resolves to the same variable, where the board reaches it, a global one or one of the board, and else to the variable of its key and scope: a global one, or one of the board. So a board's variable never resolves to a global one, nor a global one to a board's.
- A component resolves to the same component, and else to the one of its key.
- An attribute definition of a component that resolves resolves to the same definition, where it is of the type of the component that it resolves to, and else to the one of its key of that type.

None of them resolves to what is in the trash. What the content names that resolves is written as what it resolves to: a text's value reference or mention names its ID, and an expression's name is the shortest name that names it at the board, as a rename writes one (PRD FR-VAR-03). What resolves to nothing is kept as it is in a text, which the validation then reports (FR-TEXT-02 AC2), and written explicitly in an expression, as `global.<key>`, `board.<key>`, or `cmp.<component>.<attribute>`, so that it names nothing of another scope or kind meanwhile, and names what a later variable or component of its key is once one is created. A speaker, a jumper's target outside the clipboard, and a cover image that the board's project does not have are left out. A jumper's target that is one of the clipboard's nodes is that node's copy, and so is what an expression's node reference to one names.

A client that asks the report first, and creates what it lists in the batch of the paste, before it, has the paste resolve to what it created.
