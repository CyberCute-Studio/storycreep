# Diff corpus

Pairs of file sets of the published game format with the entity-level diff of each pair, which every implementation of the diff gives, so that the server's diff, which the web app reads, and the Godot addon's, which it computes of the files that it holds, are one diff (PRD §7.4, FR-SNAP-04 AC4, FR-GODOT-03 AC7, plan note N87). An implementation that gives the diff of every case, and the runs of every word case, conforms.

The Go package `server/internal/diff` is the server's diff, and runs every case (`TestCorpus`) and every word case (`TestWordsCorpus`), and checks that every case's diff validates against the diff's JSON Schema, [`../diff/v1/diff.schema.json`](../diff/v1/diff.schema.json) (`TestSchemaFile`); the GDScript diff will run them too (plan item 98).

## The files

| Path | What it holds |
| --- | --- |
| `sets/<name>/` | A snapshot of the published format, as a game's `res://storycreep/` holds one (PRD §6.3): its manifest, `storycreep.json`, its project's files, and a file of each board. `base` is version 1's fixture project, [`../fixtures/v1`](../fixtures/v1), and each other set is `base`, or another set, with the changes that its cases are about. Each validates against version 1. |
| [`cases.json`](cases.json) | `cases`: each a case's `name`, a `description` of what it is about, the names of its `from` and `to` sets, and its `diff`, the diff of `from` into `to`. |
| [`words.json`](words.json) | `cases`: each a case of the word-level diff of two plain texts: its `name`, its `before` and `after` texts, and its `words`, the runs of their diff. |

The files are generated: the sets are written in Go, in `server/internal/diff/sets_test.go`, as changes of the fixture's files, and laid out as the serializer writes a snapshot (§6.2), with each manifest's hashes of its files; the cases' diffs and runs, in `cases_test.go`, are written from PRD §7.4 and this README, not from what the Go diff gives. `go test ./internal/diff -run TestCorpusFiles -update`, in `server/`, writes them, and `go test ./...` fails when they are not what the cases write. Edit the cases, not the files.

A runner reads each set's files as bytes, as a snapshot's are read, diffs the `from` set into the `to` set, and compares its diff with the case's as JSON values: objects by their keys, whatever their order, arrays by their items in order, strings by their code points, and numbers by the double that each is, so that `1e+21` is `1e21`. A key that one has and the other does not is a difference. Words compare alike.

## The diff

A diff compares two snapshots at one version of the published format, `schemaVersion`, version 1 here, each of which validates against it, as a consumer checks a snapshot ([`../published/README.md`](../published/README.md)), so that its files agree: a board's own fields, its manifest's, are those of its file's `board`. What a diff of a snapshot that does not validate is, this README does not say. Each file is read as a consumer reads it (PRD §6.4): of a key that an object has twice, the last; and a key that the format does not define, at any depth, a key that differs from one that it defines only by its case among them, is ignored, as no field and as no part of a value. Two values are the same where they are one JSON value, read so, as a runner compares two diffs above: objects by their keys, whatever their order, arrays by their items in order, strings by their code points, and numbers by the double that each is, `-0` being `0`, so that `2` and `2.0` are one value, as a consumer reads a value by its declaration's type, a variable's default, an attribute definition's default, and a component's value of an attribute alike. Two values of the same bytes are the same, so an implementation may skip a file, a node, or a connection whose bytes are the same on both sides, as the Go diff does: a node or a connection that moved changed both boards' files, and one on two boards is never skipped.

### Its object

| Key | Value |
| --- | --- |
| `from`, `to` | Each side, as its manifest's `snapshot` has it: `kind` `snapshot` with its `id`, `version`, and `contentHash` for a release, or `kind` `draft` with its `contentHash` for the draft, which has neither an ID nor a version (§7.8). The server's diff names a release by the snapshot that its request names, which its build's manifest names alike. |
| `schemaVersion` | The version that both sides were compared at. |
| `project` | `fields`, the changes of the project's own fields, and `variables`, `componentTypes`, `attributes`, and `components`, the changes of each kind of entity of the project's files, each sorted by ID, and `omitted`, below. |
| `boards` | A group of each board that was added, removed, or modified, or on which a change is listed, sorted by the board's ID: a removed node's, branch output's, or connection's change on the board that `from` had it on, and every other's on the board that `to` has it on, so that a board that a node or a connection moved off has no group for it. A group has its `board`, its `id`, `name`, and `slug` as the `to` side has it, or as the `from` side had it where `to` does not have it; its `change`, what happened to the board itself, `added`, `removed`, or `modified`, which a board that only holds changes has none of; its `fields`, of a modified board alone; its `changes`, the changes of its nodes, then of its branch outputs, then of its connections, each kind sorted by ID; and `omitted`, below. |
| `summary` | `added`, `removed`, `moved`, `modified`, and `cosmetic`: how many changes there are of each, of every kind, the project's own counting as one modified change of the kind `project`, and a board's own as one of the kind `board`; and `kinds`, the same five counts of each kind: `attribute`, `board`, `branch_output`, `component`, `component_type`, `connection`, `node`, `project`, and `variable`. A modified change that is cosmetic counts as `cosmetic` alone. |
| `truncated` | Whether the server left changes out to size the diff for display, below. No case is truncated, and the Godot addon's diff never is. |

`omitted` is how many changes of the project's lists, or of a board's `changes`, the server left out; 0 otherwise.

### The entities and their fields

Each entity is compared by its ID, by these fields, each as its file holds it, the name of each the key of its file's object that the format defines, but for the few that the diff adds, which the last column names. A field that its file leaves out, as an optional field that is not set, is a field that the entity does not have, and so is one that its file holds with the value that the format leaves out, which version 1 has two of: a board variable's `resetOnEnter` of `false`, and an element's `tags` of `[]`.

| Kind | Its fields | Added by the diff |
| --- | --- | --- |
| The project | `name` and `slug`, of the manifest's `project`; `entry` and `signals`, of `project/settings.json` | |
| `variable` | every field of its object but `id` | |
| `component_type` | every field but `id` and `attributes`, its attribute definitions, which are entities of their own | |
| `attribute` (an attribute definition) | every field but `id` | `componentType`, its type's ID; `order`, its place among its type's attributes, from 0 |
| `component` | every field but `id` and `attributes`; and the value of each of its attributes, a field `attributes` of its own, with the `attribute`'s ID | |
| `board` | `entryNodeId`, `name`, and `slug`, of the manifest's `boards` | |
| `node` | every field of its object but `id`, and a branch's `outputs`, which are entities of their own | |
| `branch_output` | every field but `id` | `branch`, its branch's ID |
| `connection` | every field but `id` | |

Each node, branch output, and connection is on a board: its file's, and a branch output its branch's.

### What happened to an entity

- An entity that only `to` has was `added`, and one that only `from` has `removed`.
- A node or a connection that both have, on different boards, was `moved`. It names both boards, `fromBoard`, the board that `from` has it on, by its `id`, `name`, and `slug` as `from` has them, and `toBoard`, the one that `to` has it on, as `to` has them, and lists the `fields` that differ too, if any. A moved node is never cosmetic, though its `position` alone differs. A branch output whose branch moved is not moved itself: it is modified, if a field of it differs.
- An entity that both have, on the same board, whose fields differ, a field that one has and the other does not among them, was `modified`, with the `fields` that differ. A modified node whose `position` alone differs is `cosmetic`, which a view collapses by default.
- A board that only one side has is `added` or `removed`, and so is each node, branch output, and connection of it that the other side does not have. One that the other side has, on another board, is not: a node or a connection was `moved`, onto the added board or off the removed one, and a branch output is `modified`, if a field of it differs. A board whose fields differ is `modified`, as its slug, and so its file's path, may be.

A change of an entity has the entity's `kind`, its `id`, and its `change`, and names it by what its kind shows of it, as `to` has it, or as `from` had it for one that was removed, each where the entity has it and it is not the empty string, which the `preview` of an element's text of white space alone is: `key` and `type` of a variable; `key` and `name` of a component type and of a component; `componentType`, `key`, and `name` of an attribute definition; `type`, `title`, and an element's text's `preview` of a node; `branch` of a branch output; and `from`, `to`, and the plain text of its label, `label`, of a connection. It has `fields` where a field differs, of a modified or a moved entity alone, so that an added or removed entity lists no fields; `fromBoard` and `toBoard` of a moved one alone; and `cosmetic`, `true`, of a cosmetic one alone. It has no other member.

### A field's change

Each change of `fields` has the `field`'s name, and `attribute` for a component's attribute, and its value on each side, `before` and `after`, each left out where its side does not have the field, sorted by `field`, a component's attributes by their IDs. A value is as its file holds it, without the keys that the format does not define, but:

- A rich text, an element's `text` or a connection's `label`, is its plain text, as [`../rich-text/README.md`](../rich-text/README.md) derives it from its segments, and its change has `words`, the word-level diff of the plain texts below, an absent one read as the empty text, but for a change whose two plain texts are both empty, whose diff has no run, and which has no `words`.
- An expression, a `condition`, or an `onEnter`, `onExit`, or `onTraverse` script, is its `source`, and its change has `refsChanged`: whether the sets of what the two ASTs name differ, each AST's being the `ref` of each of its nodes of the kinds `var` and `node_ref`, and the `ref`, a period, and the `attribute` of each of its `attr` nodes, and an absent expression's the empty set.

A field's change has no other member.

### The word-level diff

The runs of the diff of the plain text `before` into the plain text `after`, each `op` and `text`, are made in these steps:

1. Each text is split into tokens: each maximal run of white space, the code points of Unicode's `White_Space` of [`../unicode/`](../unicode/)'s tables, and each maximal run of the other code points, in order.
2. The tokens that the two start with alike are equal, and then those that what is left of them ends with alike.
3. Between them, the shortest edit script of the tokens, `a` into `b`, is the one that Myers's greedy algorithm finds going forward, below, if it needs at most 500 tokens deleted and inserted; otherwise every token of `a` between them is deleted, and then every token of `b` inserted.
4. The tokens, in order, make regions: each run of equal tokens is an equal region of their text, and each run of the other tokens a change, of the text of its deleted tokens and of its inserted tokens, each in their order.
5. From the first region to the last, an equal region of white space alone with a change right before it and right after it joins the two changes and itself into one, which deletes the first change's deleted text, the white space, and the second's, and inserts the first's inserted text, the white space, and the second's.
6. Each equal region is an `equal` run, and each change a `delete` run of its deleted text, if it has any, and then an `insert` run of its inserted text, if it has any.

So the `equal` and `delete` runs, in order, are `before`, and the `equal` and `insert` runs `after`. Myers's algorithm, of `a`, of `n` tokens, into `b`, of `m`, keeps `v`, the furthest `x` of each diagonal `k`, `x − y`, from `−d − 1` to `d + 1`, all 0 at first, and a copy of `v` before each `d`:

```text
for d from 0 to min(n + m, 500):
  trace[d] = copy of v
  for k from -d to d by 2:
    if k == -d or (k != d and v[k - 1] < v[k + 1]): x = v[k + 1]        # down: insert b[y]
    else: x = v[k - 1] + 1                                              # right: delete a[x]
    y = x - k
    while x < n and y < m and a[x] == b[y]: x, y = x + 1, y + 1         # equal tokens
    v[k] = x
    if x >= n and y >= m: the script is backtrack(d); stop
# more than 500 edits: no script

backtrack(D): from x = n, y = m, for d from D down to 0:
  v = trace[d]; k = x - y
  prev = k + 1 if k == -d or (k != d and v[k - 1] < v[k + 1]) else k - 1
  px = v[prev]; py = px - prev
  while x > px and y > py: prepend equal a[x - 1]; x, y = x - 1, y - 1
  if d > 0: prepend insert b[y - 1] if x == px, else delete a[x - 1]
  x, y = px, py
```

## The server's truncation

`POST /api/v1/projects/{project_id}/diff` (FR-SYNC-02) sizes the diff for display: it lists at most its request's `limit` of changes, 1,000 unless the request asks for up to 5,000, and at most 8 MiB of them, in the order in which the diff lists them, the project's `variables`, `componentTypes`, `attributes`, and `components`, then each board's `changes`, in the order of the boards. Every change after the first that does not fit is left out, and counted in `omitted`, and the diff is `truncated`. It keeps every board, the project's and each board's own fields, and the summary, which counts every change (AC2).
