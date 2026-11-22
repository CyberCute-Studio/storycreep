# The authoring export format

An authoring export is a zip of a project's authoring data (PRD FR-DATA-01): what a backup or a move to another instance needs, which the published game format leaves out. `POST /api/v1/projects/{project_id}/authoring-exports` asks for one, a job builds it, and the user who asked for it downloads it for seven days.

[`v1/archive.schema.json`](v1/archive.schema.json) is the JSON Schema of version 1. The server generates it from the Go types of `server/internal/authoring`, and `go test ./...` fails when it is out of date: run `go test ./internal/authoring -run TestSchemaFile -update` in `server/`. It describes an object of the archive's JSON files by their paths, so a consumer validates each file against the property of its path.

**A full export contains personal data**: the comments, and the display names of the people who wrote them, saved play states, muted findings, and published releases. The archive's README says so too. A content-only export leaves all of it out.

## Layout

| Path | Holds | In |
| --- | --- | --- |
| `README.md` | What this archive holds, and the personal-data notice | every archive |
| `authoring.json` | The manifest: the format and its version, the project, the source, the profile, each section with its item count and SHA-256, and what was left out | every archive |
| `draft/project.json` | The project's name, slug, description, and settings | an archive of the draft |
| `draft/folders.json`, `boards.json`, `variables.json`, `component_types.json`, `components.json`, `nodes.json`, `branch_outputs.json`, `connections.json` | The draft's entities, outside its trash, each by its public ID | an archive of the draft |
| `draft/expressions.json` | The source of each expression, by its owner and field. An import compiles them again, so no AST is held | an archive of the draft |
| `draft/validation_mutes.json` | The muted warnings and infos of entities outside the trash | an archive of the draft |
| `release/…` | The files of the release, byte for byte, in the published layout | an archive of a release |
| `comments.json` | The threads that are shown. A root that its author deleted keeps its place without its body or its author | the full profile |
| `play_states.json` | The saved play states | the full profile |
| `annotations.json` | The canvas annotations, empty until they exist (FR-EDIT-12) | the full profile |
| `people.json` | The ID and display name of everyone whom the archive names. Never an email address | the full profile |
| `releases/index.json`, `releases/<snapshot ID>/…` | Every ready release, with its metadata and its files byte for byte | unless left out |
| `assets/index.json`, `assets/<ID's body>.<extension>` | The project's assets, node cover images among them, as they were uploaded | unless left out |

A section that the manifest does not list is not in the archive. A file's items are in a stable order, by ID where they have one.

## An archive of a release

A release holds only what a game needs, so an archive of one has no folders, no component descriptions, and none of the other data that only the draft holds. Its comments and play states are those of the project when the archive was made: a comment whose thread's target is in the release, and a play state with no cursor or whose cursor's node is in the release. The others are left out, and `leftOut` in the manifest and the README count them, so that nothing goes missing silently.

## Versions

`formatVersion` changes with any change that an importer could misread, and each version has a schema of its own at its path. The section registry, `authoring.Sections`, is the one list of what the format holds: a table of authoring data joins a section there, or `authoring.NotExported` with why not, which `TestEveryProjectTableIsAccounted` checks, and plan item 84's import reads the same list.
