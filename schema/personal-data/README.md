# The personal-data export format

A personal-data export is a zip of what a Storycreep instance holds of one account (PRD FR-GDPR-01), as the inventory of PRD §9.6 says, from every workspace of the instance, whether or not the user is still a member of it. `POST /api/v1/auth/account/data-exports` asks for one, a job builds it without an operator, and the user who asked downloads it with `GET …/data-exports/{export_id}/download`, signed in, for seven days, after which it is deleted.

[`v1/archive.schema.json`](v1/archive.schema.json) is the JSON Schema of version 1. The server generates it from the Go types of `server/internal/authoring`, and `go test ./...` fails when it is out of date: run `go test ./internal/authoring -run TestPersonalSchemaFile -update` in `server/`. It describes an object of the archive's JSON files, by their paths, so a consumer validates each file against its property. Its `$id` names the version, so that a consumer can pin it.

**The archive contains personal data**, all of it the user's: their email address, the IP addresses and user agents of their sessions and of what they did, and the comments that they wrote. It holds no password, no hash of one, and no secret of a session, an API token, an email link, or an invitation, only when each was made and used. Its README says so too.

## Layout

| Path | Holds |
| --- | --- |
| `README.md` | What this archive holds and leaves out, and the personal-data notice |
| `personal-data.json` | The manifest: the format and its version, what wrote it, when the reading began, the account's ID, that the archive holds personal data, and each JSON file beside it with its item count and SHA-256 |
| `account.json` | The account: its email address, display name, locale, status, when its address was verified, whether it is an instance admin, and when it was made; and its credentials' metadata, whether it has a password and when that was last set |
| `email-links.json` | Each email link of the account that has not been deleted: its kind, and when it was made, sent, used, and expires |
| `sessions.json` | Each session of the account that has not been deleted: its IP address and user agent, and when it was started, last used, ended, and expires |
| `api-tokens.json` | Each API token of the account, revoked ones too: its name, workspace, project, scopes, and when it was made, last used, revoked, and expires |
| `memberships.json` | Each membership of the account: the workspace and its name, the role, when the user joined, and the grants on its projects |
| `invites.json` | Each invitation to the account's address, as addresses compare, of any workspace, once the account has verified the address, since anyone may have registered an address that is not verified, and each that the user accepted: the workspace and its name, kind, role, status as the invitations were read, and times, never who made it |
| `audit-log/acted.json` | Each entry of the audit log whose actor is the user, made by the time that the reading began, oldest first, one to a line, whole: the action, the workspace, the actor and their API token, the subject, the result, the IP address, the user agent, the request's ID, the metadata, and when |
| `audit-log/acted-on.json` | Each entry about the user, their account, one of their memberships, a removed one too, or one of their API tokens, whose actor is someone else, or nobody, made by the time that the reading began, oldest first, one to a line: the action, the result, and when, and who did it only as the role that it needs, "a workspace admin" or "an instance administrator" (no actor for a failed sign-in with the user's address, which anyone may have tried), never who they were, nor their IP address or user agent, which are their own personal data |
| `comments.json` | Each comment that the user wrote, in a project's trash too: its workspace, project, thread, parent, the thread's target, its rich-text body, and when it was posted, edited, deleted, and its thread resolved |
| `notifications.json` | Each notification of the user's: the comment that it tells of, its workspace and project, its kind, and when it was made and read, never who mentioned the user |
| `attribution.json` | A reference to each entity of a project's content that the user created, last changed, uploaded, published, or resolved, in the trash too: its kind, ID, workspace, project, what the user did, and when, without its content, which belongs to its workspace |
| `imported-identities.json` | Each imported identity of a workspace that stands for the user, as an archive that named them made it when it was imported there (FR-DATA-01): the workspace's ID, the display name that the archive gave the user, and when |

Each section's items are in a stable order: by ID, an audit log's by when each entry was made, the imported identities by their workspace's ID and then by display name, and the references of attribution by their tables, in the order of the format's enum, then by what the user did, and then by ID. The manifest comes last in the zip, as it names what was written before it; a zip's reader finds the files through its central directory wherever they are.

## What it leaves out

- The operation log, the history of the changes of each project's content, which belongs to its workspace and whose commands hold its content, and of which `attribution.json` holds who created and last changed each entity.
- Other people's rows that name the user only as who did something for them: the memberships of the members whom the user invited, the invitations that the user made, and the notifications of the members whom the user mentioned. The entries of what the user did, in `audit-log/acted.json`, record the invitations, and `comments.json` holds the comments that mentioned the members.
- The requests for authoring, workspace, and personal-data exports, and for imports, which are kept for seven days and recorded in the audit log.
- Billing data, which no instance keeps: on the hosted service, the payment provider holds it, and its own export gives it.

`server/internal/personaldata` holds the inventory in code, and tests hold it to PRD §9.6 and to the schema: every foreign key to an account is held or left out with a reason, and so is every column of the account.
