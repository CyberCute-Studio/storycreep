# The workspace export format

A workspace export is a zip of a whole workspace (PRD FR-DATA-02): every project of it outside its trash, its members, its audit log, and its assets, which an owner takes to keep or to move. `POST /api/v1/workspaces/{workspace_id}/exports` asks for one, a job builds it, and the owner who asked for it downloads it with `GET …/exports/{export_id}/download`, signed in, while they are still an owner, for seven days, after which it is deleted.

[`v1/archive.schema.json`](v1/archive.schema.json) is the JSON Schema of version 1. The server generates it from the Go types of `server/internal/authoring`, and `go test ./...` fails when it is out of date: run `go test ./internal/authoring -run TestWorkspaceSchemaFile -update` in `server/`. It describes an object of the archive's JSON files, by their paths, so a consumer validates each file against its property. Its `$id` names the version, so that a consumer can pin it.

**The archive contains personal data**: the display name and the email address of every member, the IP address and the user agent of each entry of the audit log, and in each project's archive its comments and the names of the people involved. Its README says so too.

## Layout

| Path | Holds |
| --- | --- |
| `README.md` | What this archive holds, and the personal-data notice |
| `workspace.json` | The manifest: the format and its version, the version of the projects' authoring format, when the reading began, the workspace with its settings, each project with its archive's path, size, and SHA-256, the projects left out, and each section with its item count and SHA-256 |
| `projects/<project ID>.zip` | Each project outside the trash, archived ones too, as a full authoring export of its draft, with its comments, play states, release history, and assets, in the format of [`../authoring/`](../authoring/README.md), which the project import takes as it is |
| `members.json` | Every member, guests too: the membership's and the user's IDs, the display name, the email address, the role, when they joined, and their grants on the workspace's projects |
| `audit-log.json` | Every entry of the workspace's audit log made by the time that the reading began, oldest first, one to a line: the action, the actor and their API token, the subject, the result, the IP address, the user agent, the request's ID, the metadata, and when |
| `assets/index.json`, `assets/<ID's body>.<extension>` | The workspace's own assets, which belong to none of its projects, as an authoring export holds a project's. No route makes one yet, so the index is empty |

The JSON files come after the projects' archives in the zip, and the manifest last, as it names what was written before it; a zip's reader finds them through its central directory wherever they are. A file's items are in a stable order: by ID, and the audit log's by when each entry was made.

A project's archive imports as any authoring export does, so the instance that imports it holds it to its own limits: its size to that instance's `STORYCREEP_IMPORT_MAX_BYTES`, against which the size that `workspace.json` gives it can be checked, and its assets to 4,000, as [`../authoring/`](../authoring/README.md) says.

## What it leaves out

- **The trash.** A project in the workspace's trash is not in the archive, as nothing in a trash is exported (FR-DATA-01). The manifest's `trashedProjects` names each, with when it went there, and so a project that went to the trash, or was deleted for good, while the archive was being made, without that time.
- **One moment.** The workspace, its members, and the list of its projects are read as of one moment, and the audit log is read up to it. Each project is then read as of a moment of its own, which its `authoring.json` names, as reading every project at once would hold a transaction open for as long as the whole archive takes.
- **Accounts.** The archive holds who the members are, not their accounts: no password, session, API token, or other credential, and no member's other workspaces. Nor does it hold the workspace's open invitations, whose people are not members yet.

## Building it

The job reads the workspace, then each project as an authoring export of its draft reads it, and streams the zip into the assets' store, holding in memory one project's sections at a time and a thousand entries of the audit log at a time, and never a whole asset. It has an authoring export's time, 50 minutes; a workspace whose files take longer to copy than that fails, as does one with an asset whose file the store no longer has, and the owner is shown that it failed.
