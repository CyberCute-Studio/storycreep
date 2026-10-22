// Generated from schema/api/v1/openapi.json. Do not edit; run
// "go test ./internal/httpserver -run TestOpenAPIDocument -update" in server/
// instead.

export interface APITokenBody {
  /** When the token was made */
  created_at: string;
  /** When the token expires, after which every request made with it is answered with 401 unauthenticated; a token without it does not expire */
  expires_at?: string;
  /** The token's ID */
  id: string;
  /** When a request was last made with the token, to the minute; a token without it has not been used */
  last_used_at?: string;
  /** The token's name */
  name: string;
  /** The ID of the project that the token is narrowed to, on which alone it acts; a token without it acts on every project of its workspace */
  project_id?: string;
  /** The scopes that the token holds: snapshot:read reads the published snapshots; project:read reads the draft too, and implies snapshot:read; project:write changes the draft, and implies project:read; snapshot:write publishes snapshots, and implies snapshot:read; and export:read is the authoring-data export, which no other scope implies */
  scopes: Array<"snapshot:read" | "project:read" | "project:write" | "snapshot:write" | "export:read">;
  /** The ID of the account that made the token and owns it: a request made with the token does no more than the owner's role allows at the time */
  user_id: string;
  /** The ID of the workspace that the token belongs to, and acts in alone */
  workspace_id: string;
}

export interface Asset {
  /** The size of the file as it is stored, in bytes: for an SVG, once the server has removed what it does not allow */
  bytes: number;
  /** The image's type, which the server found from its content */
  content_type: "image/png" | "image/jpeg" | "image/gif" | "image/webp" | "image/svg+xml";
  /** When it was uploaded */
  created_at: string;
  /** The image's height in pixels, as it is shown, unless it is an SVG that does not say it, or says a size of more than 16,384 pixels on a side */
  height?: number;
  /** The asset's ID */
  id: string;
  /** The ID of the asset's project */
  project_id: string;
  /** The SHA-256 hash of the file as it is stored, in lowercase hex */
  sha256: string;
  /** The ID of the account that uploaded it, unless the account has been deleted */
  uploaded_by?: string;
  /** The image's width in pixels, as it is shown, unless it is an SVG that does not say it, or says a size of more than 16,384 pixels on a side */
  width?: number;
}

export interface Board {
  /** The board's colour, none or a name of the palette */
  color: "none" | "red" | "orange" | "yellow" | "green" | "teal" | "blue" | "purple" | "pink" | "gray";
  /** When the board was created */
  created_at: string;
  /** When the board was deleted to the project's trash, if it is there */
  deleted_at?: string;
  /** The board's description, which may be empty */
  description: string;
  /** The ID of the board's entry node, if it has one */
  entry_node_id?: string;
  /** The ID of the folder of boards that the board is in, if it is in one, or none for a board at the top */
  folder_id?: string;
  /** The board's ID */
  id: string;
  /** The board's name */
  name: string;
  /** The board's place among the folders and boards of its folder, or of the top: they are ordered by their order, compared by its bytes, then by their IDs (package orderkey) */
  order: string;
  /** The ID of the board's project */
  project_id: string;
  /** The board's revision, which every change of it increments, and which a change expects with expected_rev */
  rev: number;
  /** The board's slug, unique within the project, which names its file in the game project (PRD §6.3). It changes only when it is changed itself, never when the board is renamed (PRD FR-PROJ-02 AC1). */
  slug: string;
  /** When the board last changed */
  updated_at: string;
}

/** A command of the type board.create. */
export interface BoardCreateCommand {
  /** The command's ID, a UUID that the client makes, unique within the project. A command whose ID the project's batches applied already is answered as it was, and not applied again. */
  command_id: string;
  /** The revision that the client read of each entity that the command changes or refers to, by the entity's ID, as prj_…: the batch applies only if every entity still has it before the batch, and is answered with 409 revision_conflict otherwise. An entity that the batch creates has none. Without it, the command expects no revision. */
  expected_rev?: Record<string, number>;
  payload: BoardCreatePayload;
  /** The command's type. */
  type: "board.create";
}

/** The payload of a command of the type board.create. */
export interface BoardCreatePayload {
  /** The board's colour, none or a name of the palette. Without it, none. */
  color?: "none" | "red" | "orange" | "yellow" | "green" | "teal" | "blue" | "purple" | "pink" | "gray";
  /** The board's description, of at most 2000 characters, without white space around it, and without control characters but line feeds and tabs. Without it, the board has none. */
  description?: string;
  /** The ID of the folder of boards that the board goes into, which is not deleted. Without it, the board goes at the top. */
  folder_id?: string;
  /** The new board's ID, a bd_ ID of a UUIDv7 that the client makes, which no entity of the instance has (PRD §5.2) */
  id: string;
  /** The board's name, of 1 to 100 characters, not all of them blank, without white space around it, and without control characters, bidirectional controls, and line separators among them */
  name: string;
  /** The board's place among the children of its folder, or of the top. A key of base-62 digits, 0-9, A-Z, and a-z, that does not end in 0, of at most 128 digits, which orders the children by its bytes, and then by their IDs: pick one between the keys of the children that it goes between, as V where there are none, 8 between 7 and 9, or 7V between 7 and 8. Without it, the board goes after the last child. */
  order?: string;
  /** The board's slug, which names its file in the game project: groups of lowercase ASCII letters and digits joined by single hyphens, as harbour-intro, of at most 64 characters, that no other board of the project has, in its trash or not, and that is not a name that Windows reserves for a device, such as con or com1. A slug that is not one is answered with 422 slug_invalid, and one that another board has with 409 slug_taken. Without it, the server derives one from the name: its letters and digits in lowercase ASCII, without their accents, each run of other characters a hyphen, or board if that leaves nothing, with -board after a name that Windows reserves for a device, as con-board, and then with -2, -3, and so on, the first that no board of the project has (PRD FR-PROJ-02 AC1, AC4). */
  slug?: string;
}

/** A command of the type board.delete. */
export interface BoardDeleteCommand {
  /** The command's ID, a UUID that the client makes, unique within the project. A command whose ID the project's batches applied already is answered as it was, and not applied again. */
  command_id: string;
  /** The revision that the client read of each entity that the command changes or refers to, by the entity's ID, as prj_…: the batch applies only if every entity still has it before the batch, and is answered with 409 revision_conflict otherwise. An entity that the batch creates has none. Without it, the command expects no revision. */
  expected_rev?: Record<string, number>;
  payload: BoardDeletePayload;
  /** The command's type. */
  type: "board.delete";
}

/** The payload of a command of the type board.delete. */
export interface BoardDeletePayload {
  /** The board's ID */
  id: string;
}

/** A command of the type board.describe. */
export interface BoardDescribeCommand {
  /** The command's ID, a UUID that the client makes, unique within the project. A command whose ID the project's batches applied already is answered as it was, and not applied again. */
  command_id: string;
  /** The revision that the client read of each entity that the command changes or refers to, by the entity's ID, as prj_…: the batch applies only if every entity still has it before the batch, and is answered with 409 revision_conflict otherwise. An entity that the batch creates has none. Without it, the command expects no revision. */
  expected_rev?: Record<string, number>;
  payload: BoardDescribePayload;
  /** The command's type. */
  type: "board.describe";
}

/** The payload of a command of the type board.describe. */
export interface BoardDescribePayload {
  /** The board's new description, of at most 2000 characters, which may be empty, without white space around it, and without control characters but line feeds and tabs */
  description: string;
  /** The board's ID */
  id: string;
}

/** A command of the type board.move. */
export interface BoardMoveCommand {
  /** The command's ID, a UUID that the client makes, unique within the project. A command whose ID the project's batches applied already is answered as it was, and not applied again. */
  command_id: string;
  /** The revision that the client read of each entity that the command changes or refers to, by the entity's ID, as prj_…: the batch applies only if every entity still has it before the batch, and is answered with 409 revision_conflict otherwise. An entity that the batch creates has none. Without it, the command expects no revision. */
  expected_rev?: Record<string, number>;
  payload: BoardMovePayload;
  /** The command's type. */
  type: "board.move";
}

/** The payload of a command of the type board.move. */
export interface BoardMovePayload {
  /** The ID of the folder of boards that the board goes into, which is not deleted. Without it, the board goes to the top. */
  folder_id?: string;
  /** The board's ID */
  id: string;
  /** The board's place among the children of the folder, or of the top. A key of base-62 digits, 0-9, A-Z, and a-z, that does not end in 0, of at most 128 digits, which orders the children by its bytes, and then by their IDs: pick one between the keys of the children that it goes between, as V where there are none, 8 between 7 and 9, or 7V between 7 and 8. Without it, the board goes after the last child, or stays where it is if it is the last child of the folder already that is not deleted. */
  order?: string;
}

/** A command of the type board.rename. */
export interface BoardRenameCommand {
  /** The command's ID, a UUID that the client makes, unique within the project. A command whose ID the project's batches applied already is answered as it was, and not applied again. */
  command_id: string;
  /** The revision that the client read of each entity that the command changes or refers to, by the entity's ID, as prj_…: the batch applies only if every entity still has it before the batch, and is answered with 409 revision_conflict otherwise. An entity that the batch creates has none. Without it, the command expects no revision. */
  expected_rev?: Record<string, number>;
  payload: BoardRenamePayload;
  /** The command's type. */
  type: "board.rename";
}

/** The payload of a command of the type board.rename. */
export interface BoardRenamePayload {
  /** The board's ID */
  id: string;
  /** The board's new name, of 1 to 100 characters, not all of them blank, without white space around it, and without control characters, bidirectional controls, and line separators among them. The board keeps its slug (PRD FR-PROJ-02 AC1). */
  name: string;
}

/** A command of the type board.restore. */
export interface BoardRestoreCommand {
  /** The command's ID, a UUID that the client makes, unique within the project. A command whose ID the project's batches applied already is answered as it was, and not applied again. */
  command_id: string;
  /** The revision that the client read of each entity that the command changes or refers to, by the entity's ID, as prj_…: the batch applies only if every entity still has it before the batch, and is answered with 409 revision_conflict otherwise. An entity that the batch creates has none. Without it, the command expects no revision. */
  expected_rev?: Record<string, number>;
  payload: BoardRestorePayload;
  /** The command's type. */
  type: "board.restore";
}

/** The payload of a command of the type board.restore. */
export interface BoardRestorePayload {
  /** The board's ID */
  id: string;
}

/** A command of the type board.set_color. */
export interface BoardSetColorCommand {
  /** The command's ID, a UUID that the client makes, unique within the project. A command whose ID the project's batches applied already is answered as it was, and not applied again. */
  command_id: string;
  /** The revision that the client read of each entity that the command changes or refers to, by the entity's ID, as prj_…: the batch applies only if every entity still has it before the batch, and is answered with 409 revision_conflict otherwise. An entity that the batch creates has none. Without it, the command expects no revision. */
  expected_rev?: Record<string, number>;
  payload: BoardSetColorPayload;
  /** The command's type. */
  type: "board.set_color";
}

/** The payload of a command of the type board.set_color. */
export interface BoardSetColorPayload {
  /** The board's new colour, none or a name of the palette */
  color: "none" | "red" | "orange" | "yellow" | "green" | "teal" | "blue" | "purple" | "pink" | "gray";
  /** The board's ID */
  id: string;
}

/** A command of the type board.set_slug. */
export interface BoardSetSlugCommand {
  /** The command's ID, a UUID that the client makes, unique within the project. A command whose ID the project's batches applied already is answered as it was, and not applied again. */
  command_id: string;
  /** The revision that the client read of each entity that the command changes or refers to, by the entity's ID, as prj_…: the batch applies only if every entity still has it before the batch, and is answered with 409 revision_conflict otherwise. An entity that the batch creates has none. Without it, the command expects no revision. */
  expected_rev?: Record<string, number>;
  payload: BoardSetSlugPayload;
  /** The command's type. */
  type: "board.set_slug";
}

/** The payload of a command of the type board.set_slug. */
export interface BoardSetSlugPayload {
  /** The board's ID */
  id: string;
  /** The board's new slug, which renames its file in the game project: groups of lowercase ASCII letters and digits joined by single hyphens, as harbour-intro, of at most 64 characters, that no other board of the project has, in its trash or not, and that is not a name that Windows reserves for a device, such as con or com1. A slug that is not one is answered with 422 slug_invalid, and one that another board has with 409 slug_taken. */
  slug: string;
}

export interface ChangeRoleBody {
  /** The membership's new role. Only an owner grants the role owner, and an admin is answered with 403 owner_required. */
  role: "owner" | "admin" | "editor" | "commenter" | "viewer" | "guest";
}

/** A batch of commands, which applies whole, in one transaction, or not at all. */
export interface CommandBatchBody {
  /** The batch's commands, in the order to apply them: at least one, and at most 1000. Every command_id of the batch is its own. */
  commands: Array<CommandBody>;
}

/** A list of batches, each of which applies whole, in its own transaction, or not at all, in the order of the list, whatever the batches before it did. */
export interface CommandBatchesBody {
  /** The batches, at least one, and at most 100. */
  batches: Array<CommandBatchBody>;
}

/** A command of the batch: its ID, its type, its payload, as the type's schema says, and the revisions that it expects (PRD FR-API-03). */
export type CommandBody = BoardCreateCommand | BoardDeleteCommand | BoardDescribeCommand | BoardMoveCommand | BoardRenameCommand | BoardRestoreCommand | BoardSetColorCommand | BoardSetSlugCommand | FolderCreateCommand | FolderDeleteCommand | FolderMoveCommand | FolderRenameCommand | FolderRestoreCommand | ProjectArchiveCommand | ProjectDeleteCommand | ProjectDescribeCommand | ProjectRenameCommand | ProjectRestoreCommand | ProjectSetSlugCommand | ProjectUnarchiveCommand | ProjectUpdateSettingsCommand;

export interface CommandOutcomeBody {
  /** Why the batch was rejected: the error that a request of the batch alone would be answered with, as the error envelope holds it */
  error?: ErrorBody;
  /** The entities that the batch changed, as for a request of one batch; not for a batch that was rejected */
  revisions?: Array<EntityRevisionBody>;
  /** The project's sequence number after the batch, as for a request of one batch; not for a batch that was rejected */
  seq?: number;
  /** applied or already_applied, as for a request of one batch, or rejected for a batch that was refused, and applied nothing */
  status: "applied" | "already_applied" | "rejected";
}

export interface CommandResultBody {
  /** The entities that the batch changed, each with its revision after the batch, in the order of their first change. Those of a batch already applied are the entities that the batches that applied its commands changed, each with its current revision, without those that no longer exist. */
  revisions: Array<EntityRevisionBody> | null;
  /** The project's sequence number after the batch: the seq of its operation in the project's operation log. That of a batch already applied is the latest seq of the batches that applied its commands. */
  seq: number;
  /** applied for a batch that applied, or that was sent before, while its idempotency record is kept, for at least 24 hours, which is answered as it was then; already_applied for a batch whose commands were all applied before, once that record is pruned, or by batches grouped otherwise */
  status: "applied" | "already_applied";
}

export interface CommandResultsBody {
  /** A result for each batch of the request, in the order of the batches */
  results: Array<CommandOutcomeBody> | null;
}

export interface CompleteSetupBody {
  /** The first owner's account, which is an instance admin */
  owner: SetupOwnerBody;
  /** The setup code, which the server writes to its log while setup is pending, as 7K2M-Q9XD-4RTB-H8VW. Case, hyphens, and spaces do not matter, and O is read as 0, and I and L as 1. */
  setup_code: string;
  /** Who can register an account once setup has completed: closed (no one), invite (people holding an invitation), or open (anyone). Without it, closed. STORYCREEP_SIGNUP overrides it for as long as it is set, as GET /api/v1/setup says. */
  signup_policy?: "closed" | "invite" | "open";
  /** The SMTP server that the instance sends its email through: the links that verify an email address and reset a password. Without it, the instance sends no email, and an instance admin gives out the links as text. STORYCREEP_SMTP_URL overrides it for as long as it is set, as GET /api/v1/setup says. Setup does not connect to it. */
  smtp?: SetupSMTPBody;
  /** The bucket of an S3-compatible object storage that stores the files of the assets, the images that users upload. Without it, the server stores them in its asset directory, /var/lib/storycreep/assets. STORYCREEP_ASSET_STORAGE overrides it for as long as it is set, as GET /api/v1/setup says. Setup does not connect to it. */
  storage?: SetupStorageBody;
  /** The first workspace, which the owner owns, and which is the owner's personal workspace. Its default locale is STORYCREEP_DEFAULT_LOCALE. */
  workspace: SetupWorkspaceBody;
}

export interface CompletedSetupBody {
  /** The ID of the owner's account, which signs in with POST /api/v1/auth/login */
  owner_id: string;
  /** The first workspace */
  workspace: WorkspaceFields;
}

export interface CreateInviteBody {
  /** The email address to invite, which the account that accepts the invitation must have, in any case. Without it, the invitation is a link invitation, which whoever holds its link may accept, with any account. */
  email?: string;
  /** The role that accepting the invitation gives. Only an owner invites someone as owner, and an admin is answered with 403 owner_required. */
  role: "owner" | "admin" | "editor" | "commenter" | "viewer" | "guest";
}

export interface CreateProjectBody {
  /** The locale of the project's text, as a BCP 47 tag of a language, such as en or cs-CZ, which the project keeps in its canonical form. Without it, the workspace's default locale. */
  default_locale?: string;
  /** The project's description, of at most 2000 characters, without control characters but line feeds and tabs. White space around it is removed, and a line break written as CR LF, or as a CR alone, is kept as a line feed. Without it, the project has none. */
  description?: string;
  /** The project's name, of 1 to 100 characters, not all of them blank, and without control characters, bidirectional controls, and line separators among them. White space around it is removed. */
  name: string;
}

export interface CreateTokenBody {
  /** When the token expires, in RFC 3339, which must be in the future, and which the server keeps to the microsecond, as the answer gives it; without it, the token does not expire */
  expires_at?: string;
  /** The token's name, which says what it is for, of 1 to 100 characters, not all of them blank, and without control characters, bidirectional controls, and line separators among them. White space around it is removed. */
  name: string;
  /** The scopes that the token holds, one at least, each kept once: snapshot:read reads the published snapshots; project:read reads the draft too, and implies snapshot:read; project:write changes the draft, and implies project:read; snapshot:write publishes snapshots, and implies snapshot:read; and export:read is the authoring-data export, which no other scope implies. A token that must not see unreleased work, such as a CI job's that only pulls releases, holds snapshot:read alone. */
  scopes: Array<"snapshot:read" | "project:read" | "project:write" | "snapshot:write" | "export:read">;
}

export interface CreateWorkspaceBody {
  /** The workspace's default locale, as a BCP 47 tag of a locale that the server has a catalog for. Without it, the locale of the request: the one that its Accept-Language prefers among those, or else STORYCREEP_DEFAULT_LOCALE. */
  default_locale?: string;
  /** The workspace's name, of 1 to 100 characters, not all of them blank, and without control characters, bidirectional controls and line separators among them. White space around it is removed. */
  name: string;
  /** The workspace's slug: groups of lowercase ASCII letters and digits joined by single hyphens, of at most 64 characters, and not a reserved word, or the answer is 422 slug_invalid; if another workspace has it, the answer is 409 slug_taken. Without it, the slug is derived from the name: its letters and digits in lowercase ASCII, without accents, each run of other characters a hyphen, cut to 64 characters, or workspace if the name has none. If another workspace has that slug, or it is a reserved word, -2, -3, and so on up to -10 is added, and after that a random suffix of 8 lowercase letters and digits. */
  slug?: string;
}

export interface CreatedAPITokenBody {
  /** When the token was made */
  created_at: string;
  /** When the token expires, after which every request made with it is answered with 401 unauthenticated; a token without it does not expire */
  expires_at?: string;
  /** The token's ID */
  id: string;
  /** When a request was last made with the token, to the minute; a token without it has not been used */
  last_used_at?: string;
  /** The token's name */
  name: string;
  /** The ID of the project that the token is narrowed to, on which alone it acts; a token without it acts on every project of its workspace */
  project_id?: string;
  /** The scopes that the token holds: snapshot:read reads the published snapshots; project:read reads the draft too, and implies snapshot:read; project:write changes the draft, and implies project:read; snapshot:write publishes snapshots, and implies snapshot:read; and export:read is the authoring-data export, which no other scope implies */
  scopes: Array<"snapshot:read" | "project:read" | "project:write" | "snapshot:write" | "export:read">;
  /** The token's secret, which starts with scp_, and which a request sends as Authorization: Bearer <secret>. This is the only answer that holds it: the server keeps only its SHA-256 hash, so keep it now, as a secret. */
  secret: string;
  /** The ID of the account that made the token and owns it: a request made with the token does no more than the owner's role allows at the time */
  user_id: string;
  /** The ID of the workspace that the token belongs to, and acts in alone */
  workspace_id: string;
}

export interface EntityRevisionBody {
  /** The entity's ID, as prj_… */
  id: string;
  /** The entity's revision */
  rev: number;
}

export interface ErrorBody {
  /** A stable error code; docs/error-codes.md lists them */
  code: string;
  /** More about the error, for programs; the code decides the keys */
  details?: Record<string, unknown>;
  /** What went wrong, for people, in the language that the Content-Language header names */
  message: string;
  /** The request's ID, which the server's log records it under */
  request_id: string;
}

export interface ErrorEnvelope {
  error: ErrorBody;
}

export interface Folder {
  /** When the folder was created */
  created_at: string;
  /** When the folder was deleted, if it is */
  deleted_at?: string;
  /** The folder's ID */
  id: string;
  /** What the folder holds: boards, variables, or components, each in a tree of folders of its own */
  kind: "board" | "variable" | "component";
  /** The folder's name */
  name: string;
  /** The folder's place among the folders, and for a folder of boards the boards, of its parent, or of the top: they are ordered by their order, compared by its bytes, then by their IDs (package orderkey) */
  order: string;
  /** The ID of the folder that the folder is in, of the same kind, if it is in one, or none for a folder at the top */
  parent_id?: string;
  /** The ID of the folder's project */
  project_id: string;
  /** The folder's revision, which every change of it increments, and which a change expects with expected_rev */
  rev: number;
  /** When the folder last changed */
  updated_at: string;
}

/** A command of the type folder.create. */
export interface FolderCreateCommand {
  /** The command's ID, a UUID that the client makes, unique within the project. A command whose ID the project's batches applied already is answered as it was, and not applied again. */
  command_id: string;
  /** The revision that the client read of each entity that the command changes or refers to, by the entity's ID, as prj_…: the batch applies only if every entity still has it before the batch, and is answered with 409 revision_conflict otherwise. An entity that the batch creates has none. Without it, the command expects no revision. */
  expected_rev?: Record<string, number>;
  payload: FolderCreatePayload;
  /** The command's type. */
  type: "folder.create";
}

/** The payload of a command of the type folder.create. */
export interface FolderCreatePayload {
  /** The new folder's ID, a fld_ ID of a UUIDv7 that the client makes, which no entity of the instance has (PRD §5.2) */
  id: string;
  /** What the folder holds: boards, variables, or components, each in a tree of folders of its own */
  kind: "board" | "variable" | "component";
  /** The folder's name, of 1 to 100 characters, not all of them blank, without white space around it, and without control characters, bidirectional controls, and line separators among them */
  name: string;
  /** The folder's place among the children of its parent, or of the top. A key of base-62 digits, 0-9, A-Z, and a-z, that does not end in 0, of at most 128 digits, which orders the children by its bytes, and then by their IDs: pick one between the keys of the children that it goes between, as V where there are none, 8 between 7 and 9, or 7V between 7 and 8. Without it, the folder goes after the last child. */
  order?: string;
  /** The ID of the folder of the same kind that the folder goes into, which is not deleted. Without it, the folder goes at the top. */
  parent_id?: string;
}

/** A command of the type folder.delete. */
export interface FolderDeleteCommand {
  /** The command's ID, a UUID that the client makes, unique within the project. A command whose ID the project's batches applied already is answered as it was, and not applied again. */
  command_id: string;
  /** The revision that the client read of each entity that the command changes or refers to, by the entity's ID, as prj_…: the batch applies only if every entity still has it before the batch, and is answered with 409 revision_conflict otherwise. An entity that the batch creates has none. Without it, the command expects no revision. */
  expected_rev?: Record<string, number>;
  payload: FolderDeletePayload;
  /** The command's type. */
  type: "folder.delete";
}

/** The payload of a command of the type folder.delete. */
export interface FolderDeletePayload {
  /** The ID of the folder to delete, which holds no folder and no board that is not deleted, or the command is answered with 409 folder_not_empty. What is in the project's trash does not count, and stays in the folder, which restoring it restores too. */
  id: string;
}

/** A command of the type folder.move. */
export interface FolderMoveCommand {
  /** The command's ID, a UUID that the client makes, unique within the project. A command whose ID the project's batches applied already is answered as it was, and not applied again. */
  command_id: string;
  /** The revision that the client read of each entity that the command changes or refers to, by the entity's ID, as prj_…: the batch applies only if every entity still has it before the batch, and is answered with 409 revision_conflict otherwise. An entity that the batch creates has none. Without it, the command expects no revision. */
  expected_rev?: Record<string, number>;
  payload: FolderMovePayload;
  /** The command's type. */
  type: "folder.move";
}

/** The payload of a command of the type folder.move. */
export interface FolderMovePayload {
  /** The folder's ID */
  id: string;
  /** The folder's place among the children of its new parent, or of the top. A key of base-62 digits, 0-9, A-Z, and a-z, that does not end in 0, of at most 128 digits, which orders the children by its bytes, and then by their IDs: pick one between the keys of the children that it goes between, as V where there are none, 8 between 7 and 9, or 7V between 7 and 8. Without it, the folder goes after the last child, or stays where it is if it is the last child of its parent already that is not deleted. */
  order?: string;
  /** The ID of the folder of the same kind that the folder goes into, which is not deleted, and neither the folder itself nor a folder inside it. Without it, the folder goes to the top. */
  parent_id?: string;
}

/** A command of the type folder.rename. */
export interface FolderRenameCommand {
  /** The command's ID, a UUID that the client makes, unique within the project. A command whose ID the project's batches applied already is answered as it was, and not applied again. */
  command_id: string;
  /** The revision that the client read of each entity that the command changes or refers to, by the entity's ID, as prj_…: the batch applies only if every entity still has it before the batch, and is answered with 409 revision_conflict otherwise. An entity that the batch creates has none. Without it, the command expects no revision. */
  expected_rev?: Record<string, number>;
  payload: FolderRenamePayload;
  /** The command's type. */
  type: "folder.rename";
}

/** The payload of a command of the type folder.rename. */
export interface FolderRenamePayload {
  /** The folder's ID */
  id: string;
  /** The folder's new name, of 1 to 100 characters, not all of them blank, without white space around it, and without control characters, bidirectional controls, and line separators among them */
  name: string;
}

/** A command of the type folder.restore. */
export interface FolderRestoreCommand {
  /** The command's ID, a UUID that the client makes, unique within the project. A command whose ID the project's batches applied already is answered as it was, and not applied again. */
  command_id: string;
  /** The revision that the client read of each entity that the command changes or refers to, by the entity's ID, as prj_…: the batch applies only if every entity still has it before the batch, and is answered with 409 revision_conflict otherwise. An entity that the batch creates has none. Without it, the command expects no revision. */
  expected_rev?: Record<string, number>;
  payload: FolderRestorePayload;
  /** The command's type. */
  type: "folder.restore";
}

/** The payload of a command of the type folder.restore. */
export interface FolderRestorePayload {
  /** The folder's ID */
  id: string;
}

export interface GrantBody {
  /** When the grant was given */
  created_at: string;
  /** The ID of the membership that has the grant */
  membership_id: string;
  /** The ID of the project */
  project_id: string;
  /** The member's role on the project, which replaces their role in the workspace there, raising or lowering it. A guest acts only on the projects that they have a grant on. */
  role: "admin" | "editor" | "commenter" | "viewer";
  /** When the grant was given, or its role last changed */
  updated_at: string;
  /** The ID of the member's account */
  user_id: string;
}

export interface InstanceBody {
  /** Whether the instance sends email, through the SMTP server of STORYCREEP_SMTP_URL or of first-run setup. Without it, a page says that email is not configured: the links that reset a password and verify an email address are not emailed, and an instance admin gives them out as text. */
  email_configured: boolean;
}

export interface InviteBody {
  /** When it was made */
  created_at: string;
  /** The ID of the account that made it, unless that account was deleted */
  created_by?: string;
  /** The invited address, of an invitation of the kind email */
  email?: string;
  /** When it expires: 14 days after it was made, sent anew, or emailed */
  expires_at: string;
  /** The invitation's ID */
  id: string;
  /** Whom it invites: email, the address of email, which the account that accepts it must have, or link, whoever holds its link */
  kind: "email" | "link";
  /** The role that accepting it gives in the workspace */
  role: "owner" | "admin" | "editor" | "commenter" | "viewer" | "guest";
  /** When the instance emailed its link, which then lasts its 14 days from then. Without it, no email holds its current link: the instance sends no email, the invitation is a link invitation, or its email is still to be sent, or was not sent, as to an address that the SMTP server refuses, or over the instance's limit of invitation emails to the address's mailbox, which sending the invitation anew tries again. */
  sent_at?: string;
  /** pending until it expires, and expired from then until it is sent anew. A pending invitation is accepted with its link once it has one: one that the instance emails has none until its email is sent, as sent_at says. */
  status: "pending" | "expired";
  /** Its link, which opens the web app's page that accepts it, with its token in the URL's fragment. It is here only in the answer that made the invitation or sent it anew, and only when the instance does not email it: for a link invitation, and for every invitation on an instance that sends no email. Whoever holds it can accept the invitation, so give it only to the person invited. The database holds no copy, and sending the invitation anew gives a new link, in place of this one. */
  url?: string;
}

export interface InvitePreviewBody {
  /** The invited address, which the account that accepts it must have, of an invitation of the kind email */
  email?: string;
  /** When it expires */
  expires_at: string;
  /** Whom it invites: email, the address of email, or link, whoever holds its link */
  kind: "email" | "link";
  /** The role that accepting it gives in the workspace */
  role: "owner" | "admin" | "editor" | "commenter" | "viewer" | "guest";
  /** The name of the workspace that it invites to */
  workspace_name: string;
}

export interface InviteTokenBody {
  /** The token of the invitation's link: what follows token= in the fragment of the URL that the link opens */
  token: string;
}

export interface IssueLinkBody {
  /** The email address of the account, in any case */
  email: string;
}

export interface LimitUsageBody {
  /** The most that the workspace may use: its plan's limit, or the instance's cap if that is smaller. Without it, the workspace may use any amount. */
  limit?: number;
  /** How much the workspace uses */
  used: number;
  /** Set when the workspace uses at least 80% of its limit, so that a client can warn before a change is refused with 409 limit_exceeded */
  warning: boolean;
}

export interface LinkBody {
  /** When the link expires */
  expires_at: string;
  /** The link, which opens the web app's page that uses it, with its token in the URL's fragment. Whoever holds it can use it, so give it only to the account's user. */
  url: string;
}

export interface LoginBody {
  /** The account's email address, in any case */
  email: string;
  /** The account's password */
  password: string;
}

export interface MemberBody {
  /** The member's display name */
  display_name: string;
  /** The member's email address, which only an owner or an admin of the workspace sees */
  email?: string;
  /** The ID of the membership */
  id: string;
  /** When the member joined the workspace */
  joined_at: string;
  /** The membership's role. A guest acts only on the projects granted to them. */
  role: "owner" | "admin" | "editor" | "commenter" | "viewer" | "guest";
  /** The ID of the member's account */
  user_id: string;
}

export interface MembershipBody {
  /** When the workspace was created */
  created_at: string;
  /** The workspace's default locale, as a BCP 47 tag of a locale that the server has a catalog for */
  default_locale: string;
  /** The workspace's ID */
  id: string;
  /** The ID of the signed-in user's membership of the workspace */
  membership_id: string;
  /** The workspace's name */
  name: string;
  /** The role of the signed-in user's membership. A guest acts only on the projects granted to them, and GET /api/v1/workspaces/{workspace_id} answers them with 404 not_found. */
  role: "owner" | "admin" | "editor" | "commenter" | "viewer" | "guest";
  /** The workspace's slug, which is unique on the instance */
  slug: string;
  /** When its name, slug, or settings last changed */
  updated_at: string;
}

export interface MeteredUsageBody {
  /** How much the workspace uses */
  used: number;
}

export interface PageAPITokenBody {
  items: Array<APITokenBody>;
  /** The cursor of the next page, or null if this is the last page */
  next_cursor: string | null;
}

export interface PageBoard {
  items: Array<Board>;
  /** The cursor of the next page, or null if this is the last page */
  next_cursor: string | null;
}

export interface PageFolder {
  items: Array<Folder>;
  /** The cursor of the next page, or null if this is the last page */
  next_cursor: string | null;
}

export interface PageGrantBody {
  items: Array<GrantBody>;
  /** The cursor of the next page, or null if this is the last page */
  next_cursor: string | null;
}

export interface PageInviteBody {
  items: Array<InviteBody>;
  /** The cursor of the next page, or null if this is the last page */
  next_cursor: string | null;
}

export interface PageMemberBody {
  items: Array<MemberBody>;
  /** The cursor of the next page, or null if this is the last page */
  next_cursor: string | null;
}

export interface PageMembershipBody {
  items: Array<MembershipBody>;
  /** The cursor of the next page, or null if this is the last page */
  next_cursor: string | null;
}

export interface PageProject {
  items: Array<Project>;
  /** The cursor of the next page, or null if this is the last page */
  next_cursor: string | null;
}

export interface PageProjectBody {
  items: Array<ProjectBody>;
  /** The cursor of the next page, or null if this is the last page */
  next_cursor: string | null;
}

export interface PageSessionBody {
  items: Array<SessionBody>;
  /** The cursor of the next page, or null if this is the last page */
  next_cursor: string | null;
}

export interface PasswordBody {
  /** The account's password */
  current_password: string;
  /** The new password: at least 12 characters, and not one of the most common passwords, or the answer is weak_password */
  new_password: string;
}

export interface PasswordResetBody {
  /** The email address of the account whose password to reset, in any case */
  email: string;
}

export interface Project {
  /** When the project was archived, if it is: an archived project is read-only */
  archived_at?: string;
  /** When the project was created */
  created_at: string;
  /** The locale of the project's text, as a BCP 47 tag */
  default_locale: string;
  /** When the project was deleted to the trash, if it is there */
  deleted_at?: string;
  /** The project's description, which may be empty */
  description: string;
  /** Where the game starts: the entry board, and the node on it */
  entry: ProjectEntry;
  /** When a project in the trash is deleted for good, after which it cannot be restored */
  hard_delete_after?: string;
  /** The project's ID */
  id: string;
  /** The project's name */
  name: string;
  /** The least role that may publish the project's snapshots: editor, or admin once raised (PRD FR-SNAP-01 AC3) */
  publish_min_role: "editor" | "admin";
  /** The project's revision, which every change of its name, description, settings, archiving, or deletion increments, and which a change expects with expected_rev */
  rev: number;
  /** The sequence number of the project's latest operation, 0 before its first */
  seq: number;
  /** The project's slug, which the published format's manifest shows (PRD §6.5): derived from the project's name when it was created, and changed only when it is changed itself, never when the project is renamed. Projects may share a slug. */
  slug: string;
  /** When the project last changed */
  updated_at: string;
  /** The ID of the project's workspace */
  workspace_id: string;
}

/** A command of the type project.archive. */
export interface ProjectArchiveCommand {
  /** The command's ID, a UUID that the client makes, unique within the project. A command whose ID the project's batches applied already is answered as it was, and not applied again. */
  command_id: string;
  /** The revision that the client read of each entity that the command changes or refers to, by the entity's ID, as prj_…: the batch applies only if every entity still has it before the batch, and is answered with 409 revision_conflict otherwise. An entity that the batch creates has none. Without it, the command expects no revision. */
  expected_rev?: Record<string, number>;
  payload: ProjectArchivePayload;
  /** The command's type. */
  type: "project.archive";
}

/** The payload of a command of the type project.archive. */
export type ProjectArchivePayload = Record<string, never>;

export interface ProjectBody {
  /** When the project was archived, if it is: an archived project is read-only */
  archived_at?: string;
  /** When the project was created */
  created_at: string;
  /** The locale of the project's text, as a BCP 47 tag */
  default_locale: string;
  /** When the project was deleted to the trash, if it is there */
  deleted_at?: string;
  /** The project's description, which may be empty */
  description: string;
  /** Where the game starts: the entry board, and the node on it */
  entry: ProjectEntry;
  /** When a project in the trash is deleted for good, after which it cannot be restored */
  hard_delete_after?: string;
  /** The project's ID */
  id: string;
  /** The project's name */
  name: string;
  /** The least role that may publish the project's snapshots: editor, or admin once raised (PRD FR-SNAP-01 AC3) */
  publish_min_role: "editor" | "admin";
  /** The project's revision, which every change of its name, description, settings, archiving, or deletion increments, and which a change expects with expected_rev */
  rev: number;
  /** The role that the signed-in user acts with on the project: their grant on it, or else their role in its workspace, or owner for an instance admin when STORYCREEP_INSTANCE_ADMIN_ACCESS is on (PRD §5.5) */
  role: "owner" | "admin" | "editor" | "commenter" | "viewer";
  /** The sequence number of the project's latest operation, 0 before its first */
  seq: number;
  /** The project's slug, which the published format's manifest shows (PRD §6.5): derived from the project's name when it was created, and changed only when it is changed itself, never when the project is renamed. Projects may share a slug. */
  slug: string;
  /** When the project last changed */
  updated_at: string;
  /** The ID of the project's workspace */
  workspace_id: string;
}

/** A command of the type project.delete. Only a session may apply it: a batch with it that an API token sends is refused with 403 session_required, whatever the token's scopes (PRD FR-AUTH-04). */
export interface ProjectDeleteCommand {
  /** The command's ID, a UUID that the client makes, unique within the project. A command whose ID the project's batches applied already is answered as it was, and not applied again. */
  command_id: string;
  /** The revision that the client read of each entity that the command changes or refers to, by the entity's ID, as prj_…: the batch applies only if every entity still has it before the batch, and is answered with 409 revision_conflict otherwise. An entity that the batch creates has none. Without it, the command expects no revision. */
  expected_rev?: Record<string, number>;
  payload: ProjectDeletePayload;
  /** The command's type. */
  type: "project.delete";
}

/** The payload of a command of the type project.delete. */
export type ProjectDeletePayload = Record<string, never>;

/** A command of the type project.describe. */
export interface ProjectDescribeCommand {
  /** The command's ID, a UUID that the client makes, unique within the project. A command whose ID the project's batches applied already is answered as it was, and not applied again. */
  command_id: string;
  /** The revision that the client read of each entity that the command changes or refers to, by the entity's ID, as prj_…: the batch applies only if every entity still has it before the batch, and is answered with 409 revision_conflict otherwise. An entity that the batch creates has none. Without it, the command expects no revision. */
  expected_rev?: Record<string, number>;
  payload: ProjectDescribePayload;
  /** The command's type. */
  type: "project.describe";
}

/** The payload of a command of the type project.describe. */
export interface ProjectDescribePayload {
  /** The project's new description, of at most 2000 characters, which may be empty, without white space around it, and without control characters but line feeds and tabs */
  description: string;
}

export interface ProjectEntry {
  /** The ID of the project's entry board, if it has one */
  board_id?: string;
  /** The ID of the entry node on the entry board, if it has one */
  node_id?: string;
}

/** A command of the type project.rename. */
export interface ProjectRenameCommand {
  /** The command's ID, a UUID that the client makes, unique within the project. A command whose ID the project's batches applied already is answered as it was, and not applied again. */
  command_id: string;
  /** The revision that the client read of each entity that the command changes or refers to, by the entity's ID, as prj_…: the batch applies only if every entity still has it before the batch, and is answered with 409 revision_conflict otherwise. An entity that the batch creates has none. Without it, the command expects no revision. */
  expected_rev?: Record<string, number>;
  payload: ProjectRenamePayload;
  /** The command's type. */
  type: "project.rename";
}

/** The payload of a command of the type project.rename. */
export interface ProjectRenamePayload {
  /** The project's new name, of 1 to 100 characters, not all of them blank, without white space around it, and without control characters, bidirectional controls, and line separators among them */
  name: string;
}

/** A command of the type project.restore. Only a session may apply it: a batch with it that an API token sends is refused with 403 session_required, whatever the token's scopes (PRD FR-AUTH-04). */
export interface ProjectRestoreCommand {
  /** The command's ID, a UUID that the client makes, unique within the project. A command whose ID the project's batches applied already is answered as it was, and not applied again. */
  command_id: string;
  /** The revision that the client read of each entity that the command changes or refers to, by the entity's ID, as prj_…: the batch applies only if every entity still has it before the batch, and is answered with 409 revision_conflict otherwise. An entity that the batch creates has none. Without it, the command expects no revision. */
  expected_rev?: Record<string, number>;
  payload: ProjectRestorePayload;
  /** The command's type. */
  type: "project.restore";
}

/** The payload of a command of the type project.restore. */
export type ProjectRestorePayload = Record<string, never>;

/** A command of the type project.set_slug. */
export interface ProjectSetSlugCommand {
  /** The command's ID, a UUID that the client makes, unique within the project. A command whose ID the project's batches applied already is answered as it was, and not applied again. */
  command_id: string;
  /** The revision that the client read of each entity that the command changes or refers to, by the entity's ID, as prj_…: the batch applies only if every entity still has it before the batch, and is answered with 409 revision_conflict otherwise. An entity that the batch creates has none. Without it, the command expects no revision. */
  expected_rev?: Record<string, number>;
  payload: ProjectSetSlugPayload;
  /** The command's type. */
  type: "project.set_slug";
}

/** The payload of a command of the type project.set_slug. */
export interface ProjectSetSlugPayload {
  /** The project's new slug: groups of lowercase ASCII letters and digits joined by single hyphens, as harbour, of at most 64 characters, that is not a name that Windows reserves for a device, such as con or com1. A slug that is not one is answered with 422 slug_invalid. */
  slug: string;
}

/** A command of the type project.unarchive. */
export interface ProjectUnarchiveCommand {
  /** The command's ID, a UUID that the client makes, unique within the project. A command whose ID the project's batches applied already is answered as it was, and not applied again. */
  command_id: string;
  /** The revision that the client read of each entity that the command changes or refers to, by the entity's ID, as prj_…: the batch applies only if every entity still has it before the batch, and is answered with 409 revision_conflict otherwise. An entity that the batch creates has none. Without it, the command expects no revision. */
  expected_rev?: Record<string, number>;
  payload: ProjectUnarchivePayload;
  /** The command's type. */
  type: "project.unarchive";
}

/** The payload of a command of the type project.unarchive. */
export type ProjectUnarchivePayload = Record<string, never>;

/** A command of the type project.update_settings. */
export interface ProjectUpdateSettingsCommand {
  /** The command's ID, a UUID that the client makes, unique within the project. A command whose ID the project's batches applied already is answered as it was, and not applied again. */
  command_id: string;
  /** The revision that the client read of each entity that the command changes or refers to, by the entity's ID, as prj_…: the batch applies only if every entity still has it before the batch, and is answered with 409 revision_conflict otherwise. An entity that the batch creates has none. Without it, the command expects no revision. */
  expected_rev?: Record<string, number>;
  payload: ProjectUpdateSettingsPayload;
  /** The command's type. */
  type: "project.update_settings";
}

/** The payload of a command of the type project.update_settings. */
export interface ProjectUpdateSettingsPayload {
  /** The locale of the project's text, as a BCP 47 tag of a language in its canonical form, such as en or cs-CZ */
  default_locale?: string;
  /** The project's entry, which replaces its entry: without a board, the project has none. A board other than the entry's is one of the project's boards, in its trash or not, or the command is answered with 422 validation_failed; the node needs not exist yet. */
  entry?: ProjectEntry;
  /** The least role that may publish the project's snapshots */
  publish_min_role?: "editor" | "admin";
}

export interface RegisterBody {
  /** The name that other people see, of 1 to 100 characters, not all of them blank, and without control characters, bidirectional controls and line separators among them. White space around it is removed. */
  display_name: string;
  /** The account's email address, which signs in: an address alone, as jo@example.com, of at most 254 bytes, without spaces or characters that are not printed. Addresses compare without regard to case or to how their characters are composed. */
  email: string;
  /** The password: at least 12 characters, and not one of the most common passwords, or the answer is weak_password. */
  password: string;
}

export interface RegisterWithInviteBody {
  /** The name that other people see, as for POST /api/v1/auth/register */
  display_name: string;
  /** The account's email address, as for POST /api/v1/auth/register. An invitation to an address takes only that address, as citext compares them, and the address is the invitation's when this is left out; a link invitation needs one. */
  email?: string;
  /** The password, as for POST /api/v1/auth/register */
  password: string;
  /** The token of the invitation's link: what follows token= in the fragment of the URL that the link opens */
  token: string;
}

export interface ResetPasswordBody {
  /** The new password: at least 12 characters, and not one of the most common passwords, or the answer is weak_password */
  new_password: string;
  /** The token of the link that resets the password: what follows token= in the fragment of the URL that the link opens */
  token: string;
}

export interface RevokedBody {
  /** How many sessions were revoked */
  revoked: number;
}

export interface SessionBody {
  /** When its user signed in */
  created_at: string;
  /** Whether it is the session of the request */
  current: boolean;
  /** When it ends at the latest, however often it is used: STORYCREEP_SESSION_LIFETIME after its user signed in, or the lifetime in force when its user signed in, if that was shorter */
  expires_at: string;
  /** The session's ID */
  id: string;
  /** When it ends unless it is used before: STORYCREEP_SESSION_IDLE_TIMEOUT after its last use, or at expires_at if that is sooner */
  idle_expires_at: string;
  /** The IP address that its user signed in from: the address that the connection came from, or, if that is one of STORYCREEP_TRUSTED_PROXIES, the client's address that the proxies forwarded */
  ip?: string;
  /** When it was last used, to the minute */
  last_seen_at: string;
  /** The User-Agent header of the client that its user signed in with */
  user_agent?: string;
}

export interface SetGrantBody {
  /** The member's role on the project: admin, editor, commenter, or viewer. owner is a role of the workspace alone, and is answered with 400 owner_not_grantable. */
  role: "owner" | "admin" | "editor" | "commenter" | "viewer";
}

export interface SetupBody {
  /** The signup policy that the configuration sets, if it sets one, or else the one that setup records when the request names none: closed. */
  signup_policy: "closed" | "invite" | "open";
  /** Whether the configuration sets the signup policy, with STORYCREEP_SIGNUP, or because the instance is hosted, which then overrides the policy that setup records, for as long as it sets it. A setup screen offers no choice of policy then. */
  signup_policy_fixed: boolean;
  /** Whether the configuration sets the SMTP server, with STORYCREEP_SMTP_URL, which then overrides the one that setup records, for as long as it sets it. A setup screen asks for no SMTP server then. */
  smtp_fixed: boolean;
  /** Whether the configuration sets where the files of the assets are stored, with STORYCREEP_ASSET_STORAGE, which then overrides the bucket that setup records, for as long as it sets it. A setup screen asks for no bucket then. */
  storage_fixed: boolean;
}

export interface SetupOwnerBody {
  /** The owner's display name, as for POST /api/v1/auth/register */
  display_name: string;
  /** The owner's email address, which signs in, as for POST /api/v1/auth/register */
  email: string;
  /** The owner's password: at least 12 characters, and not one of the most common passwords, or the answer is weak_password. */
  password: string;
}

export interface SetupSMTPBody {
  /** The address that the instance's email comes from, alone or with a name, as Storycreep <noreply@example.com>, which the server must let it send from */
  from: string;
  /** The SMTP server's host name or IP address */
  host: string;
  /** The password that signs in to the server, with the username */
  password?: string;
  /** The SMTP server's port, as 587 for starttls or 465 for tls */
  port: number;
  /** How the connection is protected: tls, from its start; starttls, which the server must offer, before anything is sent; or none, for a relay on the same host or network, which then cannot sign in unless it is on localhost, 127.0.0.1, or ::1 */
  security: "tls" | "starttls" | "none";
  /** The username that signs in to the server, with PLAIN or LOGIN, with the password; without it, the server is not signed in to */
  username?: string;
}

export interface SetupStorageBody {
  /** The access key ID of the keys that read, write, list, and delete the bucket's objects */
  access_key_id: string;
  /** The bucket, which must exist: 3 to 63 lowercase letters, digits, dots, and hyphens */
  bucket: string;
  /** The host name or IP address of the S3-compatible object storage, as s3.eu-central-1.amazonaws.com, which the users' browsers must reach too, because they read the files through URLs of it that the server signs */
  host: string;
  /** The storage's port; without it, 443 for tls and 80 for none */
  port?: number;
  /** The bucket's region, as eu-central-1; without it, us-east-1, which MinIO and most S3-compatible storages take */
  region?: string;
  /** The secret key of those keys */
  secret_access_key: string;
  /** How the connection is protected: tls, HTTPS, which is the default, or none, plain HTTP, which the users' browsers read the files over too, for an instance that is itself served over plain HTTP, as on a local network */
  security?: "tls" | "none";
}

export interface SetupWorkspaceBody {
  /** The workspace's name, as for POST /api/v1/workspaces */
  name: string;
  /** The workspace's slug, as for POST /api/v1/workspaces, with the same errors. Without it, the slug is derived from the name. */
  slug?: string;
}

export interface SignedInBody {
  /** The session's CSRF token: every request made with the session that changes something, with a method other than GET, HEAD, or OPTIONS, sends it in its X-CSRF-Token header, or is answered with 403 csrf_failed. It stays the same for as long as the session does, until changing the password gives the session a new one. */
  csrf_token: string;
  /** The session */
  session: SessionBody;
  /** The ID of the account that is signed in */
  user_id: string;
}

export interface StatusBody {
  /** ok for /healthz, and ready for /readyz */
  status: "ok" | "ready";
}

export interface UpdateProjectBody {
  /** The new locale of the project's text, as a BCP 47 tag of a language */
  default_locale?: string;
  /** The project's new description, as for POST /api/v1/workspaces/{workspace_id}/projects; empty for none */
  description?: string;
  /** The project's new entry, which replaces its entry: an object without board_id removes it. node_id needs board_id. A board other than the entry's is one of the project's boards, in its trash or not, or the answer is 422 validation_failed; the node needs not exist yet. */
  entry?: ProjectEntry;
  /** The project's revision that the change was made against. If the project has another, the answer is 409 revision_conflict, and nothing changes. Without it, the change is made whatever the revision. A body that names no field to change changes nothing, so it is answered with the project, whatever its revision. */
  expected_rev?: number;
  /** The project's new name, as for POST /api/v1/workspaces/{workspace_id}/projects. The project keeps its slug. */
  name?: string;
  /** The least role that may publish the project's snapshots: editor, or admin to keep editors from publishing (PRD FR-SNAP-01 AC3). A change is recorded in the audit log. */
  publish_min_role?: "editor" | "admin";
  /** The project's new slug, which the published format's manifest shows: groups of lowercase ASCII letters and digits joined by single hyphens, as harbour, of at most 64 characters, that is not a name that Windows reserves for a device, such as con or com1. A slug that is not one is answered with 422 slug_invalid. */
  slug?: string;
}

export interface UpdateWorkspaceBody {
  /** The workspace's new default locale, a BCP 47 tag of a locale that the server has a catalog for */
  default_locale?: string;
  /** The workspace's new name, as for POST /api/v1/workspaces */
  name?: string;
  /** The workspace's new slug, as for POST /api/v1/workspaces, with the same errors. A slug that the workspace has already is kept, as a reserved word that a later release added to the list is. */
  slug?: string;
}

export interface UsageBody {
  /** The requests that the workspace's API tokens have made: each that a token was accepted for, whatever its answer then; a request that a token is refused for, as one without the route's scope, is not counted */
  api_requests: MeteredUsageBody;
  /** The boards of the workspace's projects */
  boards: MeteredUsageBody;
  /** The workspace's members, guests included */
  members: LimitUsageBody;
  /** The nodes of the workspace's projects */
  nodes: MeteredUsageBody;
  /** The workspace's projects that count towards its limit: those that are neither archived nor in its trash */
  projects: LimitUsageBody;
  /** The snapshots of the workspace's projects */
  snapshots: MeteredUsageBody;
  /** The bytes that the workspace's stored files take up. Uploads that run at once may take it past its limit by what they add together. */
  storage_bytes: LimitUsageBody;
}

export interface VerifyEmailBody {
  /** The token of the link that verifies the email address: what follows token= in the fragment of the URL that the link opens */
  token: string;
}

export interface VersionBody {
  /** The revision of the source the binary was built from */
  commit?: string;
  /** The time of that revision, in RFC 3339 */
  commit_time?: string;
  /** The version of Go the binary was built with */
  go_version: string;
  /** The release version, or (devel) for any other build, such as the main branch's image */
  version: string;
}

export interface WorkspaceBody {
  /** When the workspace was created */
  created_at: string;
  /** The workspace's default locale, as a BCP 47 tag of a locale that the server has a catalog for */
  default_locale: string;
  /** The workspace's ID */
  id: string;
  /** The workspace's name */
  name: string;
  /** The role that the signed-in user acts with in the workspace: their membership's, or owner for an instance admin when STORYCREEP_INSTANCE_ADMIN_ACCESS is on */
  role: "owner" | "admin" | "editor" | "commenter" | "viewer";
  /** The workspace's slug, which is unique on the instance */
  slug: string;
  /** When its name, slug, or settings last changed */
  updated_at: string;
}

export interface WorkspaceFields {
  /** When the workspace was created */
  created_at: string;
  /** The workspace's default locale, as a BCP 47 tag of a locale that the server has a catalog for */
  default_locale: string;
  /** The workspace's ID */
  id: string;
  /** The workspace's name */
  name: string;
  /** The workspace's slug, which is unique on the instance */
  slug: string;
  /** When its name, slug, or settings last changed */
  updated_at: string;
}

/** The API's operations, by operation ID: what each takes, and what it answers with each status that is not an error's. */
export interface Operations {
  /**
   * Accept an invitation
   *
   * Accepts an invitation with its link as the signed-in user, who joins its workspace with its role, and returns the membership. An invitation to an address that is not the account's is answered with 403 invite_email_mismatch (FR-TEN-02 AC3), while a link invitation may be accepted by any account. A user who is a member of the workspace already is answered with 409 already_member, and a workspace that has as many members, guests included, as its plan, or STORYCREEP_WORKSPACE_LIMITS on a self-hosted instance, allows with 409 limit_exceeded, and the invitation stays open either way. Accepting again an invitation that the user accepted, as registering with it does, returns the membership again, while the user is still a member. A token that belongs to no invitation, as one that sending the invitation anew replaced, or whose workspace no longer exists, is answered with 400 token_invalid, an invitation that has been revoked with 410 invite_revoked, one that has been accepted with 410 invite_accepted, and one that has expired with 410 invite_expired (FR-TEN-02 AC1). It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, reading, accepting, or registering with an invitation's link, and making, listing, or revoking an API token share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, accepting one, and making, listing, or revoking an API token share one of 10 a minute for each account. No request made with an API token counts against these, so that the requests of a token, which count against its owner's account too, cannot keep the owner from finding and revoking it, after which no limit counts them. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  acceptInvite: {
    input: {
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
      body: InviteTokenBody;
    };
    responses: {
      200: MembershipBody;
    };
  };
  /**
   * Apply batches of commands to a project
   *
   * Applies a batch of commands to a project, every change of whose content is a command of a type that the body's schema describes, with its payload (PRD FR-API-03). The body is a batch, {"commands": [...]}, or a list of batches, {"batches": [{"commands": [...]}, ...]}. A batch applies whole, in one transaction, or not at all, and is one step of undo. Each command is authorised by the signed-in user's role as its type's operation of the permission matrix needs (PRD §5.6), and a role that does not allow one refuses its batch with 403 insufficient_role, or owner_required. A batch with a command that only a session may apply, as deleting or restoring the project, which an API token sends is refused with 403 session_required, whatever the token's scopes (PRD FR-AUTH-04). A project that does not exist, is in the trash, or on which the signed-in user's role is none, as it is for a user who is not a member of its workspace or a guest without a grant on it, is answered with 404 not_found, so that the answer does not tell whether it exists. So is a project that the request's API token does not cover: one of another workspace, or one other than the project that the token is narrowed to (PRD FR-AUTH-04 AC3). Every expected_rev of a batch is checked against the entities as they are before the batch, and a stale one refuses it with 409 revision_conflict, whose details hold the entities' current revisions and states. An archived project refuses a batch with 409 project_archived, unless each of its commands applies to an archived project, and a batch that would take the workspace past a limit of its plan is refused with 409 limit_exceeded. A batch that its schema, or its commands' own checks, refuse is answered with 422 validation_failed, whose details locate each problem, as body.commands[2].payload.name. A command may also refuse its batch for what the project's state makes of its payload, at the payload's field, as body.commands[0].payload.folder_id: with 422 validation_failed for a value that the state does not take, as the ID of a folder that the project does not have, or that is deleted, or with the code that the field's description names, as 422 slug_invalid for a slug that is not one, 409 slug_taken for a slug that another board has, or 409 folder_not_empty for a folder to delete that holds folders or boards.
   *
   * A command_id is the client's, unique within the project, and makes the batch idempotent. A batch sent again, as when its answer was lost, is answered as it was the first time and applies nothing again, before the project's state and the workspace's limits are checked: the same batch, with the same commands in the same order, for at least 24 hours, whether it applied or was refused with revision_conflict, project_archived, or limit_exceeded; after that, a batch whose commands applied is answered with the status already_applied, the seq that applied them, and the current revisions of the entities that they changed, and one that was refused is checked anew. So is any other batch whose commands were all applied before, however they are grouped. A batch with commands that were applied and commands that were not is refused with 422 idempotency_mismatch, which lists the applied ones, and applies nothing. A command's payload is not compared with the one that its ID was applied with. Any other refusal, as of the role, is checked anew when the batch is sent again.
   *
   * A request of a list of batches applies each, in its own transaction, in the order of the list, and is answered with 200 and a result of each, applied or rejected with its error, so that a rejected batch, of any of the refusals above, never keeps the later ones from applying. A body that is neither form, or has more than 100 batches, is answered with 422 validation_failed, and a batch of more than 1000 commands is rejected so. A body that is not one strict JSON value, as one with a key twice in an object, anywhere in it, is answered with 400 invalid_request, as on every route. The body is at most 5 MB, as every route's, and a larger one is answered with 413 request_too_large, so that a request of many large batches may need to be split. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or an API token that holds the scope project:write, or a scope that implies it, as Authorization: Bearer <token>; otherwise the answer is 401 unauthenticated, or 403 insufficient_scope for a token without the scope, each with the Bearer scheme's challenge in its WWW-Authenticate header (RFC 6750). A request made with a token does no more than the token's owner's role allows at the time. It changes something, so a request made with the session cookie also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed; a request made with a token needs none. Requests that change something share a rate limit of 600 a minute for each IP address, 300 for each account, and 300 for each API token, whose requests count against its owner's account too. The RateLimit headers say how much is left of the limit of whichever of them has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is write, and whose Retry-After header says how many seconds to wait.
   */
  applyCommands: {
    input: {
      path: {
        /** The project's ID */
        project_id: string;
      };
      headers?: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return: a request made with the session cookie without it is answered with 403 csrf_failed, and a request made with an API token needs none */
        "X-CSRF-Token"?: string;
      };
      body: CommandBatchBody | CommandBatchesBody;
    };
    responses: {
      200: CommandResultBody | CommandResultsBody;
    };
  };
  /**
   * Archive a project
   *
   * Archives a project, which makes it read-only: every change of it but unarchiving, deleting, and restoring it is answered with 409 project_archived (PRD FR-PROJ-01 AC2). An archived project does not count towards the workspace's projects. Only an owner or an admin of the project may, and anyone else with a role on it is answered with 403 insufficient_role. A project that does not exist, is in the trash, or on which the signed-in user's role is none, as it is for a user who is not a member of its workspace or a guest without a grant on it, is answered with 404 not_found, so that the answer does not tell whether it exists. So is a project that the request's API token does not cover: one of another workspace, or one other than the project that the token is narrowed to (PRD FR-AUTH-04 AC3). A project that is archived already is answered with 409 project_archived. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or an API token that holds the scope project:write, or a scope that implies it, as Authorization: Bearer <token>; otherwise the answer is 401 unauthenticated, or 403 insufficient_scope for a token without the scope, each with the Bearer scheme's challenge in its WWW-Authenticate header (RFC 6750). A request made with a token does no more than the token's owner's role allows at the time. It changes something, so a request made with the session cookie also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed; a request made with a token needs none. Requests that change something share a rate limit of 600 a minute for each IP address, 300 for each account, and 300 for each API token, whose requests count against its owner's account too. The RateLimit headers say how much is left of the limit of whichever of them has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is write, and whose Retry-After header says how many seconds to wait.
   */
  archiveProject: {
    input: {
      path: {
        /** The project's ID */
        project_id: string;
      };
      headers?: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return: a request made with the session cookie without it is answered with 403 csrf_failed, and a request made with an API token needs none */
        "X-CSRF-Token"?: string;
      };
    };
    responses: {
      200: ProjectBody;
    };
  };
  /**
   * Change a member's role
   *
   * Changes the role of a membership of a workspace, and returns the member, with their email address. A membership that has the role already stays as it is. Only an owner grants the role owner, or changes an owner's role, and an admin is answered with 403 owner_required (FR-TEN-03 AC4). A workspace keeps an owner whose account is active, so changing the role of its last one is answered with 409 last_owner (FR-TEN-02 AC2). An owner or an admin may change their own role, as long as the workspace keeps an owner. The change also revokes the workspace's open invitations, expired or not, that the member made, and those whose link the member was last given as text, as by sending one anew, whose role the new role may not give: every one, for a role below admin, and those as owner, for admin, so that no link that they hold gives what their role no longer may. Only an owner or an admin of the workspace may, and anyone else with a role in it is answered with 403 insufficient_role. A workspace that does not exist, or in which the signed-in user's role is none, as it is for a user who is not a member or is a guest, is answered with 404 not_found, so that the answer does not tell whether it exists. So is a membership that the workspace does not have. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. Requests that change something share a rate limit of 600 a minute for each IP address, and 300 for each account. The RateLimit headers say how much is left of the limit of whichever of the two has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is write, and whose Retry-After header says how many seconds to wait.
   */
  changeMemberRole: {
    input: {
      path: {
        /** The ID of the membership whose role to change */
        membership_id: string;
        /** The workspace's ID */
        workspace_id: string;
      };
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
      body: ChangeRoleBody;
    };
    responses: {
      200: MemberBody;
    };
  };
  /**
   * Change the account's password
   *
   * Changes the signed-in account's password, if current_password is the account's, or the answer is 403 wrong_password. The new password must have at least 12 characters, counted as Unicode code points once it is in NFKC, and must not be one of the 10,000 most common passwords, or the answer is 422 weak_password. Changing it revokes every other session of the account, stops the account's links that reset its password from working, and gives the session of the request a new secret, in the Set-Cookie header, and a new CSRF token, in the body, so that the old ones are no longer valid. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, reading, accepting, or registering with an invitation's link, and making, listing, or revoking an API token share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, accepting one, and making, listing, or revoking an API token share one of 10 a minute for each account. No request made with an API token counts against these, so that the requests of a token, which count against its owner's account too, cannot keep the owner from finding and revoking it, after which no limit counts them. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  changePassword: {
    input: {
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
      body: PasswordBody;
    };
    responses: {
      200: SignedInBody;
    };
  };
  /**
   * Complete first-run setup
   *
   * Completes the instance's first-run setup, once: creates the first owner's account, which is an instance admin, and the first workspace, which the owner owns, and records the signup policy, the SMTP server, and the bucket of the assets' files. Until then, POST /api/v1/auth/register answers 403 setup_required, whatever the policy. The setup code, which the server writes to its log while setup is pending, must be the instance's, or the answer is 403 invalid_setup_code. A field that the request does not allow, as an SMTP server's host that is not one, a username without a password, a bucket's name in capitals, or an email address that has an account, as one that storycreep create-user made, is answered with 422 validation_failed. Of two setups at once, one completes, and the other is answered with 404 not_found. The owner then signs in with POST /api/v1/auth/login. Once setup has completed, the answer is 404 not_found, for everyone, and stays so. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, reading, accepting, or registering with an invitation's link, and making, listing, or revoking an API token share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, accepting one, and making, listing, or revoking an API token share one of 10 a minute for each account. No request made with an API token counts against these, so that the requests of a token, which count against its owner's account too, cannot keep the owner from finding and revoking it, after which no limit counts them. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  completeSetup: {
    input: {
      body: CompleteSetupBody;
    };
    responses: {
      201: CompletedSetupBody;
    };
  };
  /**
   * Invite someone to a workspace
   *
   * Invites an email address, or, without one, whoever holds the invitation's link, to join a workspace with a role, for 14 days. Only an owner invites someone as owner, and an admin is answered with 403 owner_required. Only an owner or an admin of the workspace may, and anyone else with a role in it is answered with 403 insufficient_role. A workspace that does not exist, or in which the signed-in user's role is none, as it is for a user who is not a member or is a guest, is answered with 404 not_found, so that the answer does not tell whether it exists. An address that belongs to a member of the workspace is answered with 409 already_member, and one that has an open invitation of the workspace already, expired or not, with 409 invite_exists. On an instance that sends email, as GET /api/v1/instance says, an invitation to an address is emailed, with a link that lasts its 14 days from when the email is sent, and the answer holds no link. A workspace emails an address one invitation in 10 minutes at most, so the email of an invitation made sooner after the workspace last emailed the address, as after revoking an invitation that was emailed and inviting the address again, is sent once those 10 minutes have passed. The instance also emails a mailbox 10 invitations at most in each clock hour (UTC), from all workspaces together, counting the addresses that differ only in a +tag, as jo+work@example.com and jo@example.com, in how their domain is written, as in full-width letters, or, at Gmail, in dots, as one. The email of an invitation over that limit is not sent, and the invitation stays without a link, its sent_at empty; send it anew later. An email counts once, in the hour that the instance first tries to send it, so that one sent again after a failure, as while the SMTP server is down, counts nothing more. On an instance that sends no email, and for a link invitation on every instance, the answer's url is the link, which the signed-in user gives the person invited; the server keeps no copy of it. The email names the instance and the role, but neither the workspace nor who invites, which the page that the link opens shows instead (POST /api/v1/invites/lookup). It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, reading, accepting, or registering with an invitation's link, and making, listing, or revoking an API token share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, accepting one, and making, listing, or revoking an API token share one of 10 a minute for each account. No request made with an API token counts against these, so that the requests of a token, which count against its owner's account too, cannot keep the owner from finding and revoking it, after which no limit counts them. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  createInvite: {
    input: {
      path: {
        /** The workspace's ID */
        workspace_id: string;
      };
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
      body: CreateInviteBody;
    };
    responses: {
      201: InviteBody;
    };
  };
  /**
   * Create a project
   *
   * Creates a project in the workspace. Only an owner or an admin of the workspace may, and anyone else with a role in it is answered with 403 insufficient_role. A workspace that does not exist, or in which the signed-in user's role is none, as it is for a user who is not a member or is a guest, is answered with 404 not_found, so that the answer does not tell whether it exists. A name, a description, or a default locale that its field does not allow is answered with 422 validation_failed, and a workspace that has as many projects as its plan allows with 409 limit_exceeded; archived projects and those in the trash do not count. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. Requests that change something share a rate limit of 600 a minute for each IP address, and 300 for each account. The RateLimit headers say how much is left of the limit of whichever of the two has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is write, and whose Retry-After header says how many seconds to wait.
   */
  createProject: {
    input: {
      path: {
        /** The workspace's ID */
        workspace_id: string;
      };
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
      body: CreateProjectBody;
    };
    responses: {
      201: ProjectBody;
    };
  };
  /**
   * Make an API token narrowed to a project
   *
   * Makes an API token narrowed to the project for the signed-in user, who owns it, with the scopes that it holds, and an expiry, if it has one. It acts on this project alone, and answers every other with 404 not_found. Only a user whose role on the project is owner, admin, or editor may, as a guest whose grant on it is, and anyone else with a role on it is answered with 403 insufficient_role. A project that does not exist, is in the trash, or on which the signed-in user's role is none, as it is for a user who is not a member of its workspace or a guest without a grant on it, is answered with 404 not_found, so that the answer does not tell whether it exists. The answer holds the token's secret, which no other answer ever holds: the server keeps only its SHA-256 hash. A request made with the token does no more than its owner's role allows at the time, and fails from the next request once the token expires or is revoked, or once its owner's membership is removed, which revokes it. A name or scopes that their field does not allow, or an expiry that is not in the future, are answered with 422 validation_failed. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. It refuses every API token so that no token makes, lists, or revokes another. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, reading, accepting, or registering with an invitation's link, and making, listing, or revoking an API token share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, accepting one, and making, listing, or revoking an API token share one of 10 a minute for each account. No request made with an API token counts against these, so that the requests of a token, which count against its owner's account too, cannot keep the owner from finding and revoking it, after which no limit counts them. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  createProjectToken: {
    input: {
      path: {
        /** The project's ID */
        project_id: string;
      };
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
      body: CreateTokenBody;
    };
    responses: {
      201: CreatedAPITokenBody;
    };
  };
  /**
   * Create a workspace
   *
   * Creates a workspace, whose owner the signed-in user becomes. Where the instance always sends email, as hosted mode does, a user who has not verified their email address is answered with 403 email_unverified, whatever the request holds. A name or a default locale that its field does not allow is answered with 422 validation_failed. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. Requests that change something share a rate limit of 600 a minute for each IP address, and 300 for each account. The RateLimit headers say how much is left of the limit of whichever of the two has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is write, and whose Retry-After header says how many seconds to wait.
   */
  createWorkspace: {
    input: {
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
      body: CreateWorkspaceBody;
    };
    responses: {
      201: WorkspaceBody;
    };
  };
  /**
   * Make an API token of a workspace
   *
   * Makes an API token of the whole workspace for the signed-in user, who owns it, with the scopes that it holds, and an expiry, if it has one. It acts on every project of the workspace, and on no other workspace's. Only an owner, an admin, or an editor of the workspace may, and anyone else with a role in it is answered with 403 insufficient_role. POST /api/v1/projects/{project_id}/tokens makes one narrowed to a project. A workspace that does not exist, or in which the signed-in user's role is none, as it is for a user who is not a member or is a guest, is answered with 404 not_found, so that the answer does not tell whether it exists. The answer holds the token's secret, which no other answer ever holds: the server keeps only its SHA-256 hash. A request made with the token does no more than its owner's role allows at the time, and fails from the next request once the token expires or is revoked, or once its owner's membership is removed, which revokes it. A name or scopes that their field does not allow, or an expiry that is not in the future, are answered with 422 validation_failed. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. It refuses every API token so that no token makes, lists, or revokes another. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, reading, accepting, or registering with an invitation's link, and making, listing, or revoking an API token share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, accepting one, and making, listing, or revoking an API token share one of 10 a minute for each account. No request made with an API token counts against these, so that the requests of a token, which count against its owner's account too, cannot keep the owner from finding and revoking it, after which no limit counts them. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  createWorkspaceToken: {
    input: {
      path: {
        /** The workspace's ID */
        workspace_id: string;
      };
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
      body: CreateTokenBody;
    };
    responses: {
      201: CreatedAPITokenBody;
    };
  };
  /**
   * Delete a project to the trash
   *
   * Deletes a project to its workspace's trash, archived or not, where it stays for 30 days, and POST /api/v1/projects/{project_id}/restore restores it (PRD FR-PROJ-01 AC3). Then it is deleted for good, with all of its content. Meanwhile, every route of the project but that one answers it with 404 not_found, and it does not count towards the workspace's projects. Only an owner or an admin of the workspace may, and anyone else with a role on the project is answered with 403 insufficient_role. A project that does not exist, is in the trash, or on which the signed-in user's role is none, as it is for a user who is not a member of its workspace or a guest without a grant on it, is answered with 404 not_found, so that the answer does not tell whether it exists. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. Requests that change something share a rate limit of 600 a minute for each IP address, and 300 for each account. The RateLimit headers say how much is left of the limit of whichever of the two has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is write, and whose Retry-After header says how many seconds to wait.
   */
  deleteProject: {
    input: {
      path: {
        /** The project's ID */
        project_id: string;
      };
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
    };
    responses: {
      204: undefined;
    };
  };
  /**
   * Get an asset
   *
   * Returns an asset of a project: its type, size, and hash. A project that does not exist, is in the trash, or on which the signed-in user's role is none, as it is for a user who is not a member of its workspace or a guest without a grant on it, is answered with 404 not_found, so that the answer does not tell whether it exists. So is a project that the request's API token does not cover: one of another workspace, or one other than the project that the token is narrowed to (PRD FR-AUTH-04 AC3). An asset that the project does not have, as one of another project, is answered with 404 not_found too, whatever its ID. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or an API token that holds the scope project:read, or a scope that implies it, as Authorization: Bearer <token>; otherwise the answer is 401 unauthenticated, or 403 insufficient_scope for a token without the scope, each with the Bearer scheme's challenge in its WWW-Authenticate header (RFC 6750). A request made with a token does no more than the token's owner's role allows at the time. Requests that read share a rate limit of 1,200 a minute for each IP address, 600 for each account, and 600 for each API token, whose requests count against its owner's account too. The RateLimit headers say how much is left of the limit of whichever of them has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is read, and whose Retry-After header says how many seconds to wait.
   */
  getAsset: {
    input: {
      path: {
        /** The asset's ID */
        asset_id: string;
        /** The project's ID */
        project_id: string;
      };
    };
    responses: {
      200: Asset;
    };
  };
  /**
   * Get an asset's file
   *
   * Serves an asset's file, as it is stored. The answer depends on where the instance stores its files (STORYCREEP_ASSET_STORAGE). The local driver's files are served by this route, with the file's type, Content-Security-Policy: sandbox, X-Content-Type-Options: nosniff, and Content-Disposition: inline for an image, which every asset is, and attachment for any other type (PRD FR-ASSET-02 AC3), an ETag, which If-None-Match revalidates with 304, and Cache-Control: private, max-age=300, so that a browser uses its copy for 5 minutes, as long as a URL of an S3 bucket works, before it asks again, and the server checks the user's access again. An S3 bucket's are answered with 302 Found, whose Location is a URL of the bucket that the server signs for 5 minutes, with the file's type, its Content-Disposition, and Cache-Control: private, max-age=300 fixed in its signature, and the redirect has Cache-Control: private, max-age=240, so that a browser that follows it again from its cache reaches a URL that still works. The bucket's origin is not the application's, so a file read from it reaches nothing of the application's (NFR-SEC-10); a client that reads the file with fetch, rather than showing it as an image, follows the redirect there, so the bucket's CORS rules must then allow the application's origin. Either way, the server checks the user's access to the project before it reads anything of the file, so that no URL reads another workspace's file, whatever it guesses (FR-ASSET-01 AC2). A project that does not exist, is in the trash, or on which the signed-in user's role is none, as it is for a user who is not a member of its workspace or a guest without a grant on it, is answered with 404 not_found, so that the answer does not tell whether it exists. So is a project that the request's API token does not cover: one of another workspace, or one other than the project that the token is narrowed to (PRD FR-AUTH-04 AC3). An asset that the project does not have, as one of another project, is answered with 404 not_found too, whatever its ID. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or an API token that holds the scope project:read, or a scope that implies it, as Authorization: Bearer <token>; otherwise the answer is 401 unauthenticated, or 403 insufficient_scope for a token without the scope, each with the Bearer scheme's challenge in its WWW-Authenticate header (RFC 6750). A request made with a token does no more than the token's owner's role allows at the time. Requests that read share a rate limit of 1,200 a minute for each IP address, 600 for each account, and 600 for each API token, whose requests count against its owner's account too. The RateLimit headers say how much is left of the limit of whichever of them has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is read, and whose Retry-After header says how many seconds to wait.
   */
  getAssetContent: {
    input: {
      path: {
        /** The asset's ID */
        asset_id: string;
        /** The project's ID */
        project_id: string;
      };
      headers?: {
        /** The ETag of the file that the client has, which is answered with 304 Not Modified while it is current */
        "If-None-Match"?: string;
      };
    };
    responses: {
      200: Blob;
      302: undefined;
      304: undefined;
    };
  };
  /**
   * Get an asset's thumbnail
   *
   * Serves an asset's thumbnail: a PNG image that fits in 256 × 256 pixels, of a PNG, JPEG, GIF, or WebP image larger than that, or else the file itself, which is also served until the thumbnail is made, soon after the upload, and for an image whose thumbnail cannot be made. Until the thumbnail is made, the local driver's file, and a 304, have Cache-Control: private, max-age=30 instead, and an S3 bucket's is answered with 302 Found to the file's own route, /content, with Cache-Control: private, max-age=30, so that a browser asks for the thumbnail again soon after it is made, and reads the file meanwhile as it keeps it from that route. The local driver serves the file so too in place of a thumbnail that its directory lost, as one that was not copied with the files, until the server makes the thumbnail again. The answer depends on where the instance stores its files (STORYCREEP_ASSET_STORAGE). The local driver's files are served by this route, with the file's type, Content-Security-Policy: sandbox, X-Content-Type-Options: nosniff, and Content-Disposition: inline for an image, which every asset is, and attachment for any other type (PRD FR-ASSET-02 AC3), an ETag, which If-None-Match revalidates with 304, and Cache-Control: private, max-age=300, so that a browser uses its copy for 5 minutes, as long as a URL of an S3 bucket works, before it asks again, and the server checks the user's access again. An S3 bucket's are answered with 302 Found, whose Location is a URL of the bucket that the server signs for 5 minutes, with the file's type, its Content-Disposition, and Cache-Control: private, max-age=300 fixed in its signature, and the redirect has Cache-Control: private, max-age=240, so that a browser that follows it again from its cache reaches a URL that still works. The bucket's origin is not the application's, so a file read from it reaches nothing of the application's (NFR-SEC-10); a client that reads the file with fetch, rather than showing it as an image, follows the redirect there, so the bucket's CORS rules must then allow the application's origin. Either way, the server checks the user's access to the project before it reads anything of the file, so that no URL reads another workspace's file, whatever it guesses (FR-ASSET-01 AC2). A project that does not exist, is in the trash, or on which the signed-in user's role is none, as it is for a user who is not a member of its workspace or a guest without a grant on it, is answered with 404 not_found, so that the answer does not tell whether it exists. So is a project that the request's API token does not cover: one of another workspace, or one other than the project that the token is narrowed to (PRD FR-AUTH-04 AC3). An asset that the project does not have, as one of another project, is answered with 404 not_found too, whatever its ID. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or an API token that holds the scope project:read, or a scope that implies it, as Authorization: Bearer <token>; otherwise the answer is 401 unauthenticated, or 403 insufficient_scope for a token without the scope, each with the Bearer scheme's challenge in its WWW-Authenticate header (RFC 6750). A request made with a token does no more than the token's owner's role allows at the time. Requests that read share a rate limit of 1,200 a minute for each IP address, 600 for each account, and 600 for each API token, whose requests count against its owner's account too. The RateLimit headers say how much is left of the limit of whichever of them has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is read, and whose Retry-After header says how many seconds to wait.
   */
  getAssetThumbnail: {
    input: {
      path: {
        /** The asset's ID */
        asset_id: string;
        /** The project's ID */
        project_id: string;
      };
      headers?: {
        /** The ETag of the file that the client has, which is answered with 304 Not Modified while it is current */
        "If-None-Match"?: string;
      };
    };
    responses: {
      200: Blob;
      302: undefined;
      304: undefined;
    };
  };
  /**
   * Get a board
   *
   * Returns a board of the project. They change through the project's command endpoint, POST /api/v1/projects/{project_id}/commands, whose commands of the types board.* and folder.* create, rename, move, delete, and restore them. A project that does not exist, is in the trash, or on which the signed-in user's role is none, as it is for a user who is not a member of its workspace or a guest without a grant on it, is answered with 404 not_found, so that the answer does not tell whether it exists. So is a project that the request's API token does not cover: one of another workspace, or one other than the project that the token is narrowed to (PRD FR-AUTH-04 AC3). A board that the project does not have, or that is in its trash, is answered with 404 not_found. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or an API token that holds the scope project:read, or a scope that implies it, as Authorization: Bearer <token>; otherwise the answer is 401 unauthenticated, or 403 insufficient_scope for a token without the scope, each with the Bearer scheme's challenge in its WWW-Authenticate header (RFC 6750). A request made with a token does no more than the token's owner's role allows at the time. Requests that read share a rate limit of 1,200 a minute for each IP address, 600 for each account, and 600 for each API token, whose requests count against its owner's account too. The RateLimit headers say how much is left of the limit of whichever of them has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is read, and whose Retry-After header says how many seconds to wait.
   */
  getBoard: {
    input: {
      path: {
        /** The board's ID */
        board_id: string;
        /** The project's ID */
        project_id: string;
      };
    };
    responses: {
      200: Board;
    };
  };
  /** Report that the process is alive */
  getHealth: {
    input: Record<string, never>;
    responses: {
      200: StatusBody;
    };
  };
  /**
   * Get what the instance offers
   *
   * Says whether the instance sends email, so that a page can say that email is not configured, and where the links that reset a password and verify an email address come from. Requests that read share a rate limit of 1,200 a minute for each IP address. The RateLimit headers say how much is left of the limit of the IP address, and a request over a limit is answered with 429 rate_limited, whose details.bucket is read, and whose Retry-After header says how many seconds to wait.
   */
  getInstance: {
    input: Record<string, never>;
    responses: {
      200: InstanceBody;
    };
  };
  /**
   * Get the API's OpenAPI document
   *
   * The OpenAPI 3.1 document of the server's operations, this one included. It is the same for every server of a release, and is committed to the repository as schema/api/v1/openapi.json. Requests that read share a rate limit of 1,200 a minute for each IP address. The RateLimit headers say how much is left of the limit of the IP address, and a request over a limit is answered with 429 rate_limited, whose details.bucket is read, and whose Retry-After header says how many seconds to wait.
   */
  getOpenAPIDocument: {
    input: {
      headers?: {
        /** The ETag of the copy of the document that the client has */
        "If-None-Match"?: string;
      };
    };
    responses: {
      200: Record<string, unknown>;
      304: undefined;
    };
  };
  /**
   * Get a project
   *
   * Returns a project, with the role that the signed-in user acts with on it. A project that does not exist, is in the trash, or on which the signed-in user's role is none, as it is for a user who is not a member of its workspace or a guest without a grant on it, is answered with 404 not_found, so that the answer does not tell whether it exists. So is a project that the request's API token does not cover: one of another workspace, or one other than the project that the token is narrowed to (PRD FR-AUTH-04 AC3). It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or an API token that holds the scope snapshot:read, or a scope that implies it, as Authorization: Bearer <token>; otherwise the answer is 401 unauthenticated, or 403 insufficient_scope for a token without the scope, each with the Bearer scheme's challenge in its WWW-Authenticate header (RFC 6750). A request made with a token does no more than the token's owner's role allows at the time. Requests that read share a rate limit of 1,200 a minute for each IP address, 600 for each account, and 600 for each API token, whose requests count against its owner's account too. The RateLimit headers say how much is left of the limit of whichever of them has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is read, and whose Retry-After header says how many seconds to wait.
   */
  getProject: {
    input: {
      path: {
        /** The project's ID */
        project_id: string;
      };
    };
    responses: {
      200: ProjectBody;
    };
  };
  /**
   * Report whether the server can serve
   *
   * Answers 200 when the server has finished starting, the database has every migration applied, and the database answers. Otherwise it answers 503 with not_ready, and details.reason says why.
   */
  getReadiness: {
    input: Record<string, never>;
    responses: {
      200: StatusBody;
    };
  };
  /**
   * Get the session of the request
   *
   * Returns the signed-in account's ID, the session of the request, and its CSRF token, which a page that the browser loads again reads here. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). Requests that read share a rate limit of 1,200 a minute for each IP address, and 600 for each account. The RateLimit headers say how much is left of the limit of whichever of the two has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is read, and whose Retry-After header says how many seconds to wait.
   */
  getSession: {
    input: Record<string, never>;
    responses: {
      200: SignedInBody;
    };
  };
  /**
   * Get what first-run setup asks for
   *
   * Tells a setup screen that the instance's first-run setup is pending, and whether it can choose the signup policy, the SMTP server, and the bucket of the assets' files. Once setup has completed, the answer is 404 not_found, for everyone, and stays so. Requests that read share a rate limit of 1,200 a minute for each IP address. The RateLimit headers say how much is left of the limit of the IP address, and a request over a limit is answered with 429 rate_limited, whose details.bucket is read, and whose Retry-After header says how many seconds to wait.
   */
  getSetup: {
    input: Record<string, never>;
    responses: {
      200: SetupBody;
    };
  };
  /** Describe the server's build */
  getVersion: {
    input: Record<string, never>;
    responses: {
      200: VersionBody;
    };
  };
  /**
   * Get a workspace
   *
   * Returns a workspace, with the role that the signed-in user acts with in it. A workspace that does not exist, or in which the signed-in user's role is none, as it is for a user who is not a member or is a guest, is answered with 404 not_found, so that the answer does not tell whether it exists. So is a workspace other than the one that the request's API token belongs to (PRD FR-AUTH-04 AC3). It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or an API token that holds the scope snapshot:read, or a scope that implies it, as Authorization: Bearer <token>; otherwise the answer is 401 unauthenticated, or 403 insufficient_scope for a token without the scope, each with the Bearer scheme's challenge in its WWW-Authenticate header (RFC 6750). A request made with a token does no more than the token's owner's role allows at the time. Requests that read share a rate limit of 1,200 a minute for each IP address, 600 for each account, and 600 for each API token, whose requests count against its owner's account too. The RateLimit headers say how much is left of the limit of whichever of them has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is read, and whose Retry-After header says how many seconds to wait.
   */
  getWorkspace: {
    input: {
      path: {
        /** The workspace's ID */
        workspace_id: string;
      };
    };
    responses: {
      200: WorkspaceBody;
    };
  };
  /**
   * Get a workspace's usage
   *
   * Returns how much the workspace uses of each thing that the plans meter, and, for those that its plan limits, the limit and whether it uses 80% of it or more, which the workspace's settings and the invitation dialog show. A change that would take the workspace past a limit is refused with 409 limit_exceeded. A self-hosted instance limits nothing, unless STORYCREEP_WORKSPACE_LIMITS caps a limit. Only an owner or an admin of the workspace may read it, as its settings, and anyone else with a role in it is answered with 403 insufficient_role. A workspace that does not exist, or in which the signed-in user's role is none, as it is for a user who is not a member or is a guest, is answered with 404 not_found, so that the answer does not tell whether it exists. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). Requests that read share a rate limit of 1,200 a minute for each IP address, and 600 for each account. The RateLimit headers say how much is left of the limit of whichever of the two has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is read, and whose Retry-After header says how many seconds to wait.
   */
  getWorkspaceUsage: {
    input: {
      path: {
        /** The workspace's ID */
        workspace_id: string;
      };
    };
    responses: {
      200: UsageBody;
    };
  };
  /**
   * Take a link that resets a password
   *
   * Returns a link that resets the password of the active account that the email address belongs to, which lasts an hour, and is used once with POST /api/v1/auth/password-reset/confirm. It is for an instance that sends no email, whose links an instance admin gives out instead, to the account's user alone, because whoever holds a link can use it; an instance that sends email answers 403 email_configured. A user who is not an instance admin, such as a workspace's owner or admin, is answered with 403 instance_admin_required, whatever the request holds. An address that belongs to no active account is answered with 404 not_found. The audit log records the issue. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, reading, accepting, or registering with an invitation's link, and making, listing, or revoking an API token share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, accepting one, and making, listing, or revoking an API token share one of 10 a minute for each account. No request made with an API token counts against these, so that the requests of a token, which count against its owner's account too, cannot keep the owner from finding and revoking it, after which no limit counts them. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  issuePasswordResetLink: {
    input: {
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
      body: IssueLinkBody;
    };
    responses: {
      201: LinkBody;
    };
  };
  /**
   * Take a link that verifies an email address
   *
   * Returns a link that verifies the email address of the active account that it belongs to, which lasts 24 hours, and is used once with POST /api/v1/auth/verify-email. It is for an instance that sends no email, whose links an instance admin gives out instead, to the account's user alone, because whoever holds a link can use it; an instance that sends email answers 403 email_configured. A user who is not an instance admin, such as a workspace's owner or admin, is answered with 403 instance_admin_required, whatever the request holds. An address that belongs to no active account is answered with 404 not_found. The audit log records the issue. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, reading, accepting, or registering with an invitation's link, and making, listing, or revoking an API token share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, accepting one, and making, listing, or revoking an API token share one of 10 a minute for each account. No request made with an API token counts against these, so that the requests of a token, which count against its owner's account too, cannot keep the owner from finding and revoking it, after which no limit counts them. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  issueVerificationLink: {
    input: {
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
      body: IssueLinkBody;
    };
    responses: {
      201: LinkBody;
    };
  };
  /**
   * List a project's boards
   *
   * Lists the project's boards that are not in its trash, in the order of their IDs, oldest first. Each names its folder and its place among the folder's children, from which a client builds the tree of folders (GET /api/v1/projects/{project_id}/folders). They change through the project's command endpoint, POST /api/v1/projects/{project_id}/commands, whose commands of the types board.* and folder.* create, rename, move, delete, and restore them. A project that does not exist, is in the trash, or on which the signed-in user's role is none, as it is for a user who is not a member of its workspace or a guest without a grant on it, is answered with 404 not_found, so that the answer does not tell whether it exists. So is a project that the request's API token does not cover: one of another workspace, or one other than the project that the token is narrowed to (PRD FR-AUTH-04 AC3). It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or an API token that holds the scope project:read, or a scope that implies it, as Authorization: Bearer <token>; otherwise the answer is 401 unauthenticated, or 403 insufficient_scope for a token without the scope, each with the Bearer scheme's challenge in its WWW-Authenticate header (RFC 6750). A request made with a token does no more than the token's owner's role allows at the time. Requests that read share a rate limit of 1,200 a minute for each IP address, 600 for each account, and 600 for each API token, whose requests count against its owner's account too. The RateLimit headers say how much is left of the limit of whichever of them has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is read, and whose Retry-After header says how many seconds to wait.
   */
  listBoards: {
    input: {
      path: {
        /** The project's ID */
        project_id: string;
      };
      query?: {
        /** The next_cursor of the previous page; empty for the first page */
        cursor?: string;
        /** How many items the page holds at most */
        limit?: number;
      };
    };
    responses: {
      200: PageBoard;
    };
  };
  /**
   * List a project's folders
   *
   * Lists the project's folders that are not deleted, of boards, variables, and components, or of the kind that kind names, in the order of their IDs, oldest first. Each names its parent and its place among the parent's children, from which a client builds each kind's tree. They change through the project's command endpoint, POST /api/v1/projects/{project_id}/commands, whose commands of the types board.* and folder.* create, rename, move, delete, and restore them. A project that does not exist, is in the trash, or on which the signed-in user's role is none, as it is for a user who is not a member of its workspace or a guest without a grant on it, is answered with 404 not_found, so that the answer does not tell whether it exists. So is a project that the request's API token does not cover: one of another workspace, or one other than the project that the token is narrowed to (PRD FR-AUTH-04 AC3). It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or an API token that holds the scope project:read, or a scope that implies it, as Authorization: Bearer <token>; otherwise the answer is 401 unauthenticated, or 403 insufficient_scope for a token without the scope, each with the Bearer scheme's challenge in its WWW-Authenticate header (RFC 6750). A request made with a token does no more than the token's owner's role allows at the time. Requests that read share a rate limit of 1,200 a minute for each IP address, 600 for each account, and 600 for each API token, whose requests count against its owner's account too. The RateLimit headers say how much is left of the limit of whichever of them has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is read, and whose Retry-After header says how many seconds to wait.
   */
  listFolders: {
    input: {
      path: {
        /** The project's ID */
        project_id: string;
      };
      query?: {
        /** The next_cursor of the previous page; empty for the first page */
        cursor?: string;
        /** What the folders hold: boards, variables, or components. Without it, folders of every kind. */
        kind?: "board" | "variable" | "component";
        /** How many items the page holds at most */
        limit?: number;
      };
    };
    responses: {
      200: PageFolder;
    };
  };
  /**
   * List a workspace's invitations
   *
   * Lists the open invitations of a workspace, those that have been neither accepted nor revoked, expired or not, in the order of their IDs, oldest first. Their links are not among them: the server keeps none, and sending an invitation anew gives a new one. Only an owner or an admin of the workspace may, and anyone else with a role in it is answered with 403 insufficient_role. A workspace that does not exist, or in which the signed-in user's role is none, as it is for a user who is not a member or is a guest, is answered with 404 not_found, so that the answer does not tell whether it exists. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). Requests that read share a rate limit of 1,200 a minute for each IP address, and 600 for each account. The RateLimit headers say how much is left of the limit of whichever of the two has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is read, and whose Retry-After header says how many seconds to wait.
   */
  listInvites: {
    input: {
      path: {
        /** The workspace's ID */
        workspace_id: string;
      };
      query?: {
        /** The next_cursor of the previous page; empty for the first page */
        cursor?: string;
        /** How many items the page holds at most */
        limit?: number;
      };
    };
    responses: {
      200: PageInviteBody;
    };
  };
  /**
   * List a member's grants
   *
   * Lists a member's grants on the workspace's projects that are not in its trash, in the order of the projects' IDs, oldest first. Only an owner or an admin of the workspace may, and anyone else with a role in it is answered with 403 insufficient_role. A workspace that does not exist, or in which the signed-in user's role is none, as it is for a user who is not a member or is a guest, is answered with 404 not_found, so that the answer does not tell whether it exists. So is a membership that the workspace does not have. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). Requests that read share a rate limit of 1,200 a minute for each IP address, and 600 for each account. The RateLimit headers say how much is left of the limit of whichever of the two has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is read, and whose Retry-After header says how many seconds to wait.
   */
  listMemberGrants: {
    input: {
      path: {
        /** The ID of the membership whose grants to list */
        membership_id: string;
        /** The workspace's ID */
        workspace_id: string;
      };
      query?: {
        /** The next_cursor of the previous page; empty for the first page */
        cursor?: string;
        /** How many items the page holds at most */
        limit?: number;
      };
    };
    responses: {
      200: PageGrantBody;
    };
  };
  /**
   * List a workspace's members
   *
   * Lists the members of a workspace, guests included, in the order of their memberships' IDs, oldest first, with their display names and roles, to every member with a role in it. Only its owners and admins also see their email addresses. A workspace that does not exist, or in which the signed-in user's role is none, as it is for a user who is not a member or is a guest, is answered with 404 not_found, so that the answer does not tell whether it exists. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). Requests that read share a rate limit of 1,200 a minute for each IP address, and 600 for each account. The RateLimit headers say how much is left of the limit of whichever of the two has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is read, and whose Retry-After header says how many seconds to wait.
   */
  listMembers: {
    input: {
      path: {
        /** The workspace's ID */
        workspace_id: string;
      };
      query?: {
        /** The next_cursor of the previous page; empty for the first page */
        cursor?: string;
        /** How many items the page holds at most */
        limit?: number;
      };
    };
    responses: {
      200: PageMemberBody;
    };
  };
  /**
   * List a project's grants
   *
   * Lists the members' grants on a project, in the order of their memberships' IDs, oldest first. A member with a grant acts on the project with its role instead of their role in the workspace, and a guest acts only on the projects that they have one on. Only an owner or an admin of the project's workspace may, and anyone else with a role on the project, as a viewer or a guest with an admin grant on it, is answered with 403 insufficient_role: a grant never authorises an operation of the workspace (PRD FR-TEN-04 AC4). A project that does not exist, is in the trash, or on which the signed-in user's role is none, as it is for a user who is not a member of its workspace or a guest without a grant on it, is answered with 404 not_found, so that the answer does not tell whether it exists. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). Requests that read share a rate limit of 1,200 a minute for each IP address, and 600 for each account. The RateLimit headers say how much is left of the limit of whichever of the two has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is read, and whose Retry-After header says how many seconds to wait.
   */
  listProjectGrants: {
    input: {
      path: {
        /** The project's ID */
        project_id: string;
      };
      query?: {
        /** The next_cursor of the previous page; empty for the first page */
        cursor?: string;
        /** How many items the page holds at most */
        limit?: number;
      };
    };
    responses: {
      200: PageGrantBody;
    };
  };
  /**
   * List a project's API tokens
   *
   * Lists the API tokens narrowed to the project that are not revoked, expired ones included, in the order of their IDs, oldest first, without their secrets: every member's, to an owner or an admin of the project's workspace, and their own, to a user whose role on the project is owner, admin, or editor. Anyone else with a role on it is answered with 403 insufficient_role. A project that does not exist, is in the trash, or on which the signed-in user's role is none, as it is for a user who is not a member of its workspace or a guest without a grant on it, is answered with 404 not_found, so that the answer does not tell whether it exists. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). It refuses every API token so that no token makes, lists, or revokes another. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, reading, accepting, or registering with an invitation's link, and making, listing, or revoking an API token share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, accepting one, and making, listing, or revoking an API token share one of 10 a minute for each account. No request made with an API token counts against these, so that the requests of a token, which count against its owner's account too, cannot keep the owner from finding and revoking it, after which no limit counts them. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  listProjectTokens: {
    input: {
      path: {
        /** The project's ID */
        project_id: string;
      };
      query?: {
        /** The next_cursor of the previous page; empty for the first page */
        cursor?: string;
        /** How many items the page holds at most */
        limit?: number;
      };
    };
    responses: {
      200: PageAPITokenBody;
    };
  };
  /**
   * List a workspace's projects
   *
   * Lists the workspace's projects that are not in its trash, archived ones included, in the order of their IDs, oldest first, each with the role that the signed-in user acts with on it. A guest is shown only the projects granted to them. A workspace that does not exist, or of which the signed-in user is neither a member nor an instance admin who acts as its owner, as with STORYCREEP_INSTANCE_ADMIN_ACCESS on, is answered with 404 not_found, so that the answer does not tell whether it exists. So is a workspace other than the one that the request's API token belongs to, and a token narrowed to a project is shown that project alone (PRD FR-AUTH-04 AC3). It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or an API token that holds the scope snapshot:read, or a scope that implies it, as Authorization: Bearer <token>; otherwise the answer is 401 unauthenticated, or 403 insufficient_scope for a token without the scope, each with the Bearer scheme's challenge in its WWW-Authenticate header (RFC 6750). A request made with a token does no more than the token's owner's role allows at the time. Requests that read share a rate limit of 1,200 a minute for each IP address, 600 for each account, and 600 for each API token, whose requests count against its owner's account too. The RateLimit headers say how much is left of the limit of whichever of them has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is read, and whose Retry-After header says how many seconds to wait.
   */
  listProjects: {
    input: {
      path: {
        /** The workspace's ID */
        workspace_id: string;
      };
      query?: {
        /** The next_cursor of the previous page; empty for the first page */
        cursor?: string;
        /** How many items the page holds at most */
        limit?: number;
      };
    };
    responses: {
      200: PageProjectBody;
    };
  };
  /**
   * List the account's sessions
   *
   * Lists the signed-in account's active sessions, newest first, each with the IP address and user agent that it started with, and when it was last used. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). Requests that read share a rate limit of 1,200 a minute for each IP address, and 600 for each account. The RateLimit headers say how much is left of the limit of whichever of the two has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is read, and whose Retry-After header says how many seconds to wait.
   */
  listSessions: {
    input: {
      query?: {
        /** The next_cursor of the previous page; empty for the first page */
        cursor?: string;
        /** How many items the page holds at most */
        limit?: number;
      };
    };
    responses: {
      200: PageSessionBody;
    };
  };
  /**
   * List the projects in a workspace's trash
   *
   * Lists the workspace's projects that were deleted and can still be restored, in the order of their IDs, oldest first. A deleted project stays in the trash for 30 days, until its hard_delete_after, and is then deleted for good, with all of its content. Only an owner or an admin of the workspace may list them, and anyone else with a role in it is answered with 403 insufficient_role. A workspace that does not exist, or in which the signed-in user's role is none, as it is for a user who is not a member or is a guest, is answered with 404 not_found, so that the answer does not tell whether it exists. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). Requests that read share a rate limit of 1,200 a minute for each IP address, and 600 for each account. The RateLimit headers say how much is left of the limit of whichever of the two has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is read, and whose Retry-After header says how many seconds to wait.
   */
  listTrashedProjects: {
    input: {
      path: {
        /** The workspace's ID */
        workspace_id: string;
      };
      query?: {
        /** The next_cursor of the previous page; empty for the first page */
        cursor?: string;
        /** How many items the page holds at most */
        limit?: number;
      };
    };
    responses: {
      200: PageProject;
    };
  };
  /**
   * List a workspace's API tokens
   *
   * Lists the API tokens of the whole workspace that are not revoked, expired ones included, in the order of their IDs, oldest first, without their secrets: every member's, to an owner or an admin of the workspace, and their own, to an editor. Anyone else with a role in it is answered with 403 insufficient_role. The tokens narrowed to a project are listed by GET /api/v1/projects/{project_id}/tokens. A workspace that does not exist, or in which the signed-in user's role is none, as it is for a user who is not a member or is a guest, is answered with 404 not_found, so that the answer does not tell whether it exists. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). It refuses every API token so that no token makes, lists, or revokes another. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, reading, accepting, or registering with an invitation's link, and making, listing, or revoking an API token share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, accepting one, and making, listing, or revoking an API token share one of 10 a minute for each account. No request made with an API token counts against these, so that the requests of a token, which count against its owner's account too, cannot keep the owner from finding and revoking it, after which no limit counts them. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  listWorkspaceTokens: {
    input: {
      path: {
        /** The workspace's ID */
        workspace_id: string;
      };
      query?: {
        /** The next_cursor of the previous page; empty for the first page */
        cursor?: string;
        /** How many items the page holds at most */
        limit?: number;
      };
    };
    responses: {
      200: PageAPITokenBody;
    };
  };
  /**
   * List the account's workspaces
   *
   * Lists the workspaces that the signed-in user is a member of, in the order of their IDs, oldest first, each with the role of the user's membership, guest included. A client switches between them by naming one by its ID in the routes that it calls: the server keeps no current workspace. Every account has one at least from its first sign-in, which gives it its personal workspace. A request made with an API token is shown, of these, the token's workspace alone, in which alone it acts, whether or not the token is narrowed to one of its projects. So the token of an instance admin who acts as the owner of a workspace without being a member of it, as with STORYCREEP_INSTANCE_ADMIN_ACCESS on, is shown none, as the admin's session is not shown that workspace either; GET /api/v1/workspaces/{workspace_id} reads it. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or an API token that holds the scope snapshot:read, or a scope that implies it, as Authorization: Bearer <token>; otherwise the answer is 401 unauthenticated, or 403 insufficient_scope for a token without the scope, each with the Bearer scheme's challenge in its WWW-Authenticate header (RFC 6750). A request made with a token does no more than the token's owner's role allows at the time. Requests that read share a rate limit of 1,200 a minute for each IP address, 600 for each account, and 600 for each API token, whose requests count against its owner's account too. The RateLimit headers say how much is left of the limit of whichever of them has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is read, and whose Retry-After header says how many seconds to wait.
   */
  listWorkspaces: {
    input: {
      query?: {
        /** The next_cursor of the previous page; empty for the first page */
        cursor?: string;
        /** How many items the page holds at most */
        limit?: number;
      };
    };
    responses: {
      200: PageMembershipBody;
    };
  };
  /**
   * Sign in
   *
   * Signs in with an account's email address and password, and starts a session, whose secret the Set-Cookie header gives the client, and whose CSRF token the body holds. The account's first sign-in also gives it its personal workspace, named after its display name, which it owns; GET /api/v1/workspaces lists it. Where the instance always sends email, as hosted mode does, that is the first sign-in once the account's email address is verified, or the verification itself. An address of no account, a wrong password, and an account that cannot sign in, such as a suspended one, are all answered with 401 invalid_credentials, after the same work, so that the answer does not tell them apart. A request that a browser's Sec-Fetch-Site header says comes from another site, or from another origin of the same site, is answered with 403 csrf_failed; browsers send that header only to https URLs and to loopback addresses, such as localhost. After 10 failed sign-ins in a row with an email address, whether or not it belongs to an account, the address is locked for a minute, and each further failure, once a lock ends, locks it for twice as long as the lock before, up to 15 minutes: while it is locked, every sign-in with it is answered with 429 rate_limited, whose details.reason is locked, even with the right password. A successful sign-in forgets the failures, and so do 24 hours without one. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, reading, accepting, or registering with an invitation's link, and making, listing, or revoking an API token share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, accepting one, and making, listing, or revoking an API token share one of 10 a minute for each account. No request made with an API token counts against these, so that the requests of a token, which count against its owner's account too, cannot keep the owner from finding and revoking it, after which no limit counts them. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  login: {
    input: {
      body: LoginBody;
    };
    responses: {
      200: SignedInBody;
    };
  };
  /**
   * Sign out
   *
   * Ends the session of the request: deletes it, so that its secret is no longer valid on any server, and clears its cookie. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. Requests that change something share a rate limit of 600 a minute for each IP address, and 300 for each account. The RateLimit headers say how much is left of the limit of whichever of the two has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is write, and whose Retry-After header says how many seconds to wait.
   */
  logout: {
    input: {
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
    };
    responses: {
      204: undefined;
    };
  };
  /**
   * Read an invitation with its link
   *
   * Returns what the holder of an invitation's link sees of it, so that the page that accepts it can show it: the workspace's name, the role, and the invited address, if it has one. It is a POST, so that the token travels in the body, never in a URL that a log could hold. A token that belongs to no invitation, as one that sending the invitation anew replaced, or whose workspace no longer exists, is answered with 400 token_invalid, an invitation that has been revoked with 410 invite_revoked, one that has been accepted with 410 invite_accepted, and one that has expired with 410 invite_expired (FR-TEN-02 AC1). Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, reading, accepting, or registering with an invitation's link, and making, listing, or revoking an API token share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, accepting one, and making, listing, or revoking an API token share one of 10 a minute for each account. No request made with an API token counts against these, so that the requests of a token, which count against its owner's account too, cannot keep the owner from finding and revoking it, after which no limit counts them. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  lookupInvite: {
    input: {
      body: InviteTokenBody;
    };
    responses: {
      200: InvitePreviewBody;
    };
  };
  /**
   * Register an account
   *
   * Creates an account with an email address, a display name, and a password, if the instance's signup policy lets anyone register: the policy that STORYCREEP_SIGNUP sets, or else the one that first-run setup chose. Until setup has completed, the answer is 403 setup_required, whatever the policy. Under closed the answer is 403 signup_closed, and under invite 403 invite_required, because only an invitation creates an account then; each comes whatever the request holds. An email address or a display name that its field does not allow is answered with 422 validation_failed. The password must have at least 12 characters, counted as Unicode code points once it is in NFKC, and must not be one of the 10,000 most common passwords, compared without regard to case, or the answer is 422 weak_password. Otherwise the answer is 202 without a body, whether or not the address has an account already, which then stays as it was, so that the answer does not tell whether an address has an account. On an instance that sends email, as GET /api/v1/instance says, a new account is emailed a link that verifies its address, which POST /api/v1/auth/verify-email uses, and an address that has an account already is emailed nothing. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, reading, accepting, or registering with an invitation's link, and making, listing, or revoking an API token share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, accepting one, and making, listing, or revoking an API token share one of 10 a minute for each account. No request made with an API token counts against these, so that the requests of a token, which count against its owner's account too, cannot keep the owner from finding and revoking it, after which no limit counts them. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  registerAccount: {
    input: {
      body: RegisterBody;
    };
    responses: {
      202: undefined;
    };
  };
  /**
   * Register an account with an invitation
   *
   * Creates an account, as POST /api/v1/auth/register does, and accepts an invitation with its link as the new account, which joins the invitation's workspace with its role, under the invite and the open signup policies. Until first-run setup has completed, the answer is 403 setup_required, whatever the policy. Under closed, the answer is 403 signup_closed, whatever the request holds, and only an account that exists already accepts an invitation, with POST /api/v1/invites/accept. An invitation to an address registers that address, and another is answered with 403 invite_email_mismatch (FR-TEN-02 AC3). A token that belongs to no invitation, as one that sending the invitation anew replaced, or whose workspace no longer exists, is answered with 400 token_invalid, an invitation that has been revoked with 410 invite_revoked, one that has been accepted with 410 invite_accepted, and one that has expired with 410 invite_expired (FR-TEN-02 AC1). A link invitation, which registers any address, to a workspace that has as many members as it may have, as POST /api/v1/invites/accept says, is answered with 409 limit_exceeded, whatever the address, for as long as the workspace is full, so that its link, which stays open, does not register one account after another. The fields are checked as POST /api/v1/auth/register checks them, with its errors. Otherwise the answer is 202 without a body, whether or not the address has an account already, so that it does not tell whether an address has one: an account that exists stays as it was, and the invitation stays open for it. So it does for a new account, which joins no workspace, if the workspace of an invitation to an address is full. The client then signs in, and accepts the invitation with POST /api/v1/invites/accept, which returns the membership either way, or answers 409 limit_exceeded while the workspace is full. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, reading, accepting, or registering with an invitation's link, and making, listing, or revoking an API token share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, accepting one, and making, listing, or revoking an API token share one of 10 a minute for each account. No request made with an API token counts against these, so that the requests of a token, which count against its owner's account too, cannot keep the owner from finding and revoking it, after which no limit counts them. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  registerWithInvite: {
    input: {
      body: RegisterWithInviteBody;
    };
    responses: {
      202: undefined;
    };
  };
  /**
   * Remove a member from a workspace
   *
   * Removes a membership of a workspace, and with it the member's project grants there; the account stays as it was. It also revokes the workspace's open invitations, expired or not, that the member made, those whose link the member was last given as text, as by sending one anew, and those to the member's address, so that no link that they hold lets them back in. Only an owner removes an owner, and an admin is answered with 403 owner_required. A workspace keeps an owner whose account is active, so removing its last one is answered with 409 last_owner (FR-TEN-02 AC2). An owner or an admin may remove themselves, as long as the workspace keeps an owner. Only an owner or an admin of the workspace may, and anyone else with a role in it is answered with 403 insufficient_role. A workspace that does not exist, or in which the signed-in user's role is none, as it is for a user who is not a member or is a guest, is answered with 404 not_found, so that the answer does not tell whether it exists. So is a membership that the workspace does not have. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. Requests that change something share a rate limit of 600 a minute for each IP address, and 300 for each account. The RateLimit headers say how much is left of the limit of whichever of the two has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is write, and whose Retry-After header says how many seconds to wait.
   */
  removeMember: {
    input: {
      path: {
        /** The ID of the membership to remove */
        membership_id: string;
        /** The workspace's ID */
        workspace_id: string;
      };
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
    };
    responses: {
      204: undefined;
    };
  };
  /**
   * Remove a member's grant on a project
   *
   * Removes a member's grant on a project, after which they act on it with their role in the workspace, and a guest no longer acts on it at all. Only an owner sets or removes an owner's grant, which replaces their role on the project, and an admin is answered with 403 owner_required. Only an owner or an admin of the project's workspace may, and anyone else with a role on the project, as a viewer or a guest with an admin grant on it, is answered with 403 insufficient_role: a grant never authorises an operation of the workspace (PRD FR-TEN-04 AC4). A project that does not exist, is in the trash, or on which the signed-in user's role is none, as it is for a user who is not a member of its workspace or a guest without a grant on it, is answered with 404 not_found, so that the answer does not tell whether it exists. So is a membership that the workspace does not have, or that has no grant on the project. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. Requests that change something share a rate limit of 600 a minute for each IP address, and 300 for each account. The RateLimit headers say how much is left of the limit of whichever of the two has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is write, and whose Retry-After header says how many seconds to wait.
   */
  removeProjectGrant: {
    input: {
      path: {
        /** The ID of the membership whose grant to remove */
        membership_id: string;
        /** The project's ID */
        project_id: string;
      };
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
    };
    responses: {
      204: undefined;
    };
  };
  /**
   * Ask for a link that verifies the account's email address
   *
   * Emails a link that verifies the signed-in account's email address, which lasts 24 hours, and is used once with POST /api/v1/auth/verify-email. An address that is verified already is emailed nothing, and nor is one that was emailed such a link in the last 10 minutes that still works: the answer is the same, and the earlier link keeps working. Registering emails one too. An instance that sends no email answers 403 email_not_configured, whatever the request holds, as GET /api/v1/instance says it will. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, reading, accepting, or registering with an invitation's link, and making, listing, or revoking an API token share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, accepting one, and making, listing, or revoking an API token share one of 10 a minute for each account. No request made with an API token counts against these, so that the requests of a token, which count against its owner's account too, cannot keep the owner from finding and revoking it, after which no limit counts them. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  requestEmailVerification: {
    input: {
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
    };
    responses: {
      202: undefined;
    };
  };
  /**
   * Ask for a link that resets a password
   *
   * Emails a link that resets the password of the active account that the email address belongs to, which lasts an hour, and is used once with POST /api/v1/auth/password-reset/confirm. The answer is 202 without a body, as soon, whether or not the address belongs to an account, so that it does not tell whether it does; an address of no account is emailed nothing. Nor is an account that was emailed such a link in the last 10 minutes that still works, so that asking again and again does not flood its inbox: the answer is the same, and the earlier link keeps working. An instance that sends no email answers 403 email_not_configured, whatever the request holds, as GET /api/v1/instance says it will. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, reading, accepting, or registering with an invitation's link, and making, listing, or revoking an API token share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, accepting one, and making, listing, or revoking an API token share one of 10 a minute for each account. No request made with an API token counts against these, so that the requests of a token, which count against its owner's account too, cannot keep the owner from finding and revoking it, after which no limit counts them. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  requestPasswordReset: {
    input: {
      body: PasswordResetBody;
    };
    responses: {
      202: undefined;
    };
  };
  /**
   * Send an invitation anew
   *
   * Gives an open invitation, expired or not, a new link and 14 days more. The old link stops working at once. As when it was made, the new link is emailed, or is the answer's url, and an email waits until 10 minutes have passed since the workspace last emailed the address, so sending an invitation anew again and again emails it once in 10 minutes at most, with the latest link. The instance also emails a mailbox 10 invitations at most in each clock hour (UTC), from all workspaces together, counting the addresses that differ only in a +tag, as jo+work@example.com and jo@example.com, in how their domain is written, as in full-width letters, or, at Gmail, in dots, as one. The email of an invitation over that limit is not sent, and the invitation stays without a link, its sent_at empty; send it anew later. An email counts once, in the hour that the instance first tries to send it, so that one sent again after a failure, as while the SMTP server is down, counts nothing more. Only an owner sends an invitation as owner anew, and an admin is answered with 403 owner_required. Only an owner or an admin of the workspace may, and anyone else with a role in it is answered with 403 insufficient_role. A workspace that does not exist, or in which the signed-in user's role is none, as it is for a user who is not a member or is a guest, is answered with 404 not_found, so that the answer does not tell whether it exists. So is an invitation that the workspace does not have. One that has been accepted is answered with 410 invite_accepted, one that has been revoked with 410 invite_revoked, and one to an address that belongs to a member of the workspace by now with 409 already_member. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, reading, accepting, or registering with an invitation's link, and making, listing, or revoking an API token share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, accepting one, and making, listing, or revoking an API token share one of 10 a minute for each account. No request made with an API token counts against these, so that the requests of a token, which count against its owner's account too, cannot keep the owner from finding and revoking it, after which no limit counts them. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  resendInvite: {
    input: {
      path: {
        /** The invitation's ID */
        invite_id: string;
        /** The workspace's ID */
        workspace_id: string;
      };
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
    };
    responses: {
      200: InviteBody;
    };
  };
  /**
   * Reset a password with a link
   *
   * Replaces the password of the account of a link that resets it, and uses the link, which works once. It signs the account out everywhere: every session of the account is revoked, and the user then signs in with the new password. The account's other reset links stop working too. A token that does not exist, of another kind of link, or of an account that can no longer use it, is answered with 400 token_invalid, a link that has been used with 400 token_used, and one that has expired with 400 token_expired. The new password must have at least 12 characters, counted as Unicode code points once it is in NFKC, and must not be one of the 10,000 most common passwords, or the answer is 422 weak_password, which leaves the link as it was. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, reading, accepting, or registering with an invitation's link, and making, listing, or revoking an API token share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, accepting one, and making, listing, or revoking an API token share one of 10 a minute for each account. No request made with an API token counts against these, so that the requests of a token, which count against its owner's account too, cannot keep the owner from finding and revoking it, after which no limit counts them. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  resetPassword: {
    input: {
      body: ResetPasswordBody;
    };
    responses: {
      204: undefined;
    };
  };
  /**
   * Restore a project from the trash
   *
   * Restores a project from its workspace's trash, as it was when it was deleted, within 30 days of its deletion; after that, it is answered with 404 not_found. A project that is not in the trash stays as it is. A restored project that is not archived counts towards the workspace's projects again: a workspace that has as many projects as its plan allows is answered with 409 limit_exceeded. Only an owner or an admin of the workspace may, and anyone else with a role on the project is answered with 403 insufficient_role. A project that does not exist, or on which the signed-in user's role is none, is answered with 404 not_found. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. Requests that change something share a rate limit of 600 a minute for each IP address, and 300 for each account. The RateLimit headers say how much is left of the limit of whichever of the two has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is write, and whose Retry-After header says how many seconds to wait.
   */
  restoreProject: {
    input: {
      path: {
        /** The project's ID */
        project_id: string;
      };
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
    };
    responses: {
      200: ProjectBody;
    };
  };
  /**
   * Revoke an invitation
   *
   * Revokes an open invitation, whose link is then answered with 410 invite_revoked (FR-TEN-02 AC1). Revoking it again changes nothing. An admin may revoke an invitation as owner too. Only an owner or an admin of the workspace may, and anyone else with a role in it is answered with 403 insufficient_role. A workspace that does not exist, or in which the signed-in user's role is none, as it is for a user who is not a member or is a guest, is answered with 404 not_found, so that the answer does not tell whether it exists. So is an invitation that the workspace does not have. One that has been accepted is answered with 410 invite_accepted. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. Requests that change something share a rate limit of 600 a minute for each IP address, and 300 for each account. The RateLimit headers say how much is left of the limit of whichever of the two has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is write, and whose Retry-After header says how many seconds to wait.
   */
  revokeInvite: {
    input: {
      path: {
        /** The invitation's ID */
        invite_id: string;
        /** The workspace's ID */
        workspace_id: string;
      };
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
    };
    responses: {
      204: undefined;
    };
  };
  /**
   * Revoke the account's other sessions
   *
   * Revokes every active session of the signed-in account but the session of the request, which signs the account out everywhere else. POST /api/v1/auth/logout ends the session of the request. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. Requests that change something share a rate limit of 600 a minute for each IP address, and 300 for each account. The RateLimit headers say how much is left of the limit of whichever of the two has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is write, and whose Retry-After header says how many seconds to wait.
   */
  revokeOtherSessions: {
    input: {
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
    };
    responses: {
      200: RevokedBody;
    };
  };
  /**
   * Revoke an API token narrowed to a project
   *
   * Revokes an API token narrowed to the project, after which every request made with it is answered with 401 unauthenticated. An owner or an admin of the project's workspace revokes any member's, and a user whose role on the project is owner, admin, or editor their own; anyone else with a role on it is answered with 403 insufficient_role, and so is such a user who names another member's token, as a guest with an admin grant on the project, which authorises nothing of the workspace (PRD FR-TEN-04 AC4). A project that does not exist, is in the trash, or on which the signed-in user's role is none, as it is for a user who is not a member of its workspace or a guest without a grant on it, is answered with 404 not_found, so that the answer does not tell whether it exists. So is a token that is not narrowed to the project, or that is revoked already. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. It refuses every API token so that no token makes, lists, or revokes another. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, reading, accepting, or registering with an invitation's link, and making, listing, or revoking an API token share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, accepting one, and making, listing, or revoking an API token share one of 10 a minute for each account. No request made with an API token counts against these, so that the requests of a token, which count against its owner's account too, cannot keep the owner from finding and revoking it, after which no limit counts them. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  revokeProjectToken: {
    input: {
      path: {
        /** The project's ID */
        project_id: string;
        /** The token's ID */
        token_id: string;
      };
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
    };
    responses: {
      204: undefined;
    };
  };
  /**
   * Revoke a session
   *
   * Revokes one of the signed-in account's active sessions, which is then no longer valid on any server. Revoking the session of the request signs it out, and the answer clears its cookie. A session that is not an active session of the account is answered with 404 not_found. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. Requests that change something share a rate limit of 600 a minute for each IP address, and 300 for each account. The RateLimit headers say how much is left of the limit of whichever of the two has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is write, and whose Retry-After header says how many seconds to wait.
   */
  revokeSession: {
    input: {
      path: {
        /** The session's ID */
        session_id: string;
      };
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
    };
    responses: {
      204: undefined;
    };
  };
  /**
   * Revoke an API token of a workspace
   *
   * Revokes an API token of the whole workspace, after which every request made with it is answered with 401 unauthenticated. An owner or an admin of the workspace revokes any member's, and an editor their own; anyone else with a role in it is answered with 403 insufficient_role, and an editor who names another member's token too. A workspace that does not exist, or in which the signed-in user's role is none, as it is for a user who is not a member or is a guest, is answered with 404 not_found, so that the answer does not tell whether it exists. So is a token that the workspace does not have, one narrowed to a project, which DELETE /api/v1/projects/{project_id}/tokens/{token_id} revokes, and one that is revoked already. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. It refuses every API token so that no token makes, lists, or revokes another. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, reading, accepting, or registering with an invitation's link, and making, listing, or revoking an API token share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, accepting one, and making, listing, or revoking an API token share one of 10 a minute for each account. No request made with an API token counts against these, so that the requests of a token, which count against its owner's account too, cannot keep the owner from finding and revoking it, after which no limit counts them. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  revokeWorkspaceToken: {
    input: {
      path: {
        /** The token's ID */
        token_id: string;
        /** The workspace's ID */
        workspace_id: string;
      };
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
    };
    responses: {
      204: undefined;
    };
  };
  /**
   * Set a member's grant on a project
   *
   * Gives a member of the project's workspace a role on the project, which replaces their role in the workspace there, raising or lowering it, and returns the grant: it adds the member's grant on the project, or changes its role. A grant that has the role already stays as it is. A grant is the only way that a guest acts on a project, and it applies only to the project's own operations: every operation of the workspace, as deleting the project or managing its grants, is still authorised by the member's role in the workspace. The role owner is answered with 400 owner_not_grantable (PRD FR-TEN-04 AC5). Only an owner sets or removes an owner's grant, which replaces their role on the project, and an admin is answered with 403 owner_required. Only an owner or an admin of the project's workspace may, and anyone else with a role on the project, as a viewer or a guest with an admin grant on it, is answered with 403 insufficient_role: a grant never authorises an operation of the workspace (PRD FR-TEN-04 AC4). A project that does not exist, is in the trash, or on which the signed-in user's role is none, as it is for a user who is not a member of its workspace or a guest without a grant on it, is answered with 404 not_found, so that the answer does not tell whether it exists. So is a membership that the workspace does not have. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. Requests that change something share a rate limit of 600 a minute for each IP address, and 300 for each account. The RateLimit headers say how much is left of the limit of whichever of the two has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is write, and whose Retry-After header says how many seconds to wait.
   */
  setProjectGrant: {
    input: {
      path: {
        /** The ID of the membership of the project's workspace whose grant to set */
        membership_id: string;
        /** The project's ID */
        project_id: string;
      };
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
      body: SetGrantBody;
    };
    responses: {
      200: GrantBody;
    };
  };
  /**
   * Unarchive a project
   *
   * Unarchives a project, which can then be changed again, and counts towards the workspace's projects again: a workspace that has as many projects as its plan allows is answered with 409 limit_exceeded. A project that is not archived stays as it is. Only an owner or an admin of the project may, and anyone else with a role on it is answered with 403 insufficient_role. A project that does not exist, is in the trash, or on which the signed-in user's role is none, as it is for a user who is not a member of its workspace or a guest without a grant on it, is answered with 404 not_found, so that the answer does not tell whether it exists. So is a project that the request's API token does not cover: one of another workspace, or one other than the project that the token is narrowed to (PRD FR-AUTH-04 AC3). It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or an API token that holds the scope project:write, or a scope that implies it, as Authorization: Bearer <token>; otherwise the answer is 401 unauthenticated, or 403 insufficient_scope for a token without the scope, each with the Bearer scheme's challenge in its WWW-Authenticate header (RFC 6750). A request made with a token does no more than the token's owner's role allows at the time. It changes something, so a request made with the session cookie also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed; a request made with a token needs none. Requests that change something share a rate limit of 600 a minute for each IP address, 300 for each account, and 300 for each API token, whose requests count against its owner's account too. The RateLimit headers say how much is left of the limit of whichever of them has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is write, and whose Retry-After header says how many seconds to wait.
   */
  unarchiveProject: {
    input: {
      path: {
        /** The project's ID */
        project_id: string;
      };
      headers?: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return: a request made with the session cookie without it is answered with 403 csrf_failed, and a request made with an API token needs none */
        "X-CSRF-Token"?: string;
      };
    };
    responses: {
      200: ProjectBody;
    };
  };
  /**
   * Change a project's name, slug, description, or settings
   *
   * Changes a project's name, slug, description, default locale, publish_min_role, or entry, in one batch of the project's commands; a field that the body leaves out, or sets to null, stays as it is. Only an owner or an admin of the project may, and anyone else with a role on it is answered with 403 insufficient_role. A project that does not exist, is in the trash, or on which the signed-in user's role is none, as it is for a user who is not a member of its workspace or a guest without a grant on it, is answered with 404 not_found, so that the answer does not tell whether it exists. So is a project that the request's API token does not cover: one of another workspace, or one other than the project that the token is narrowed to (PRD FR-AUTH-04 AC3). A field whose value it does not allow is answered with 422 validation_failed, and a slug that is not one with 422 slug_invalid. An archived project is read-only, and the change is answered with 409 project_archived. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or an API token that holds the scope project:write, or a scope that implies it, as Authorization: Bearer <token>; otherwise the answer is 401 unauthenticated, or 403 insufficient_scope for a token without the scope, each with the Bearer scheme's challenge in its WWW-Authenticate header (RFC 6750). A request made with a token does no more than the token's owner's role allows at the time. It changes something, so a request made with the session cookie also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed; a request made with a token needs none. Requests that change something share a rate limit of 600 a minute for each IP address, 300 for each account, and 300 for each API token, whose requests count against its owner's account too. The RateLimit headers say how much is left of the limit of whichever of them has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is write, and whose Retry-After header says how many seconds to wait.
   */
  updateProject: {
    input: {
      path: {
        /** The project's ID */
        project_id: string;
      };
      headers?: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return: a request made with the session cookie without it is answered with 403 csrf_failed, and a request made with an API token needs none */
        "X-CSRF-Token"?: string;
      };
      body: UpdateProjectBody;
    };
    responses: {
      200: ProjectBody;
    };
  };
  /**
   * Change a workspace's settings
   *
   * Changes a workspace's name, slug, or default locale; a field that the body leaves out, or sets to null, stays as it is. Only an owner or an admin of the workspace may, and anyone else with a role in it is answered with 403 insufficient_role. A workspace that does not exist, or in which the signed-in user's role is none, as it is for a user who is not a member or is a guest, is answered with 404 not_found, so that the answer does not tell whether it exists. A name or a default locale that its field does not allow is answered with 422 validation_failed. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or the answer is 401 unauthenticated. It is session-only: a request made with an API token, whatever its scopes, is answered with 403 session_required (PRD FR-AUTH-04). It changes something, so it also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed. Requests that change something share a rate limit of 600 a minute for each IP address, and 300 for each account. The RateLimit headers say how much is left of the limit of whichever of the two has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is write, and whose Retry-After header says how many seconds to wait.
   */
  updateWorkspace: {
    input: {
      path: {
        /** The workspace's ID */
        workspace_id: string;
      };
      headers: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return; without it the answer is 403 csrf_failed */
        "X-CSRF-Token": string;
      };
      body: UpdateWorkspaceBody;
    };
    responses: {
      200: WorkspaceBody;
    };
  };
  /**
   * Upload an image to a project
   *
   * Uploads an image, which the project's content can then refer to, as a node's cover or a component's portrait (PRD FR-ASSET-02). The body is the file: a PNG, JPEG, GIF, WebP, or SVG image of at most STORYCREEP_ASSET_MAX_BYTES, 10 MB unless the instance says otherwise, or the answer is 413 request_too_large. The server tells the type from the file's content alone, and refuses any other file, as text whose name ends in .png, with 422 image_invalid. An SVG image is stored without its scripts, event handlers, and references to anything outside it, which the server removes; it is never stored as it was uploaded, and one that would be stored larger than STORYCREEP_ASSET_MAX_BYTES, as the server writes some of its characters as references, is answered with 413 request_too_large too. A thumbnail of a large image is made soon after. The answer is 201 with the new asset, or 200 with the asset that the project has of the same file already, which makes an upload that a client sends again harmless. A file that the workspace does not store yet must fit in its storage: an upload that would take the workspace past its plan's storage_bytes is answered with 413 storage_limit_exceeded, with the workspace's usage and limit. Uploads that run at once are each checked against the usage before any of them adds to it, so together they may take the workspace past its limit by as much as their own files (PRD FR-ENT-02). Only an owner, an admin, or an editor of the project may upload, and anyone else with a role on it is answered with 403 insufficient_role, before the file is read. Until the instance's first-run setup has completed, which may yet choose a bucket for the files, an upload is answered with 403 setup_required, before the file is read, unless STORYCREEP_ASSET_STORAGE names where the files are stored. The server reads only so many files at once, and a quarter of them of one user's, so an upload may wait for others to end before its file is read. A project that does not exist, is in the trash, or on which the signed-in user's role is none, as it is for a user who is not a member of its workspace or a guest without a grant on it, is answered with 404 not_found, so that the answer does not tell whether it exists. So is a project that the request's API token does not cover: one of another workspace, or one other than the project that the token is narrowed to (PRD FR-AUTH-04 AC3). An archived project is read-only, and the change is answered with 409 project_archived. It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or an API token that holds the scope project:write, or a scope that implies it, as Authorization: Bearer <token>; otherwise the answer is 401 unauthenticated, or 403 insufficient_scope for a token without the scope, each with the Bearer scheme's challenge in its WWW-Authenticate header (RFC 6750). A request made with a token does no more than the token's owner's role allows at the time. It changes something, so a request made with the session cookie also needs the session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed; a request made with a token needs none. Requests that change something share a rate limit of 600 a minute for each IP address, 300 for each account, and 300 for each API token, whose requests count against its owner's account too. The RateLimit headers say how much is left of the limit of whichever of them has the fewest requests left, and a request over a limit is answered with 429 rate_limited, whose details.bucket is write, and whose Retry-After header says how many seconds to wait.
   */
  uploadAsset: {
    input: {
      path: {
        /** The project's ID */
        project_id: string;
      };
      headers?: {
        /** The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return: a request made with the session cookie without it is answered with 403 csrf_failed, and a request made with an API token needs none */
        "X-CSRF-Token"?: string;
      };
      /** The file, as it is, whatever its name: the server tells its type from its content, and ignores the request's Content-Type, which may be any. */
      body: Blob;
    };
    responses: {
      200: Asset;
      201: Asset;
    };
  };
  /**
   * Verify an email address with a link
   *
   * Records that the email address of the account of a link that verifies it is verified, and uses the link, which works once, and only while the address is still the account's. Where the instance requires a verified address before a workspace can be created, as hosted mode does, it also gives the account its personal workspace, if it has had none; should that fail, the answer is still 204, because the address is verified, and the account's next sign-in gives the workspace. A token that does not exist, of another kind of link, or of an account that can no longer use it, is answered with 400 token_invalid, a link that has been used with 400 token_used, and one that has expired with 400 token_expired. Signing in, registering, changing a password, completing first-run setup, asking for a link that resets a password or verifies an email address, using one, an instance admin's taking one as text, inviting someone to a workspace, sending an invitation anew, reading, accepting, or registering with an invitation's link, and making, listing, or revoking an API token share a rate limit of 20 requests a minute for each IP address. Registering, with an invitation or without, and asking for a link that resets a password also share one of 10 a minute for each email address, so that each counts against the other for the same address, and changing a password, asking for a link that verifies the signed-in account's email address, an instance admin's taking a link as text, inviting someone, sending an invitation anew, accepting one, and making, listing, or revoking an API token share one of 10 a minute for each account. No request made with an API token counts against these, so that the requests of a token, which count against its owner's account too, cannot keep the owner from finding and revoking it, after which no limit counts them. The RateLimit headers say how much of the limit of the IP address, or of the account, is left, and a request over a limit is answered with 429 rate_limited, whose details.reason is rate, whose RateLimit headers report the limit that it is over, that of the email address included, and whose Retry-After header says how many seconds to wait.
   */
  verifyEmail: {
    input: {
      body: VerifyEmailBody;
    };
    responses: {
      204: undefined;
    };
  };
}

/** The method and path of each operation, and whether it answers with a file (blob). */
export const routes = {
  acceptInvite: { method: "POST", path: "/api/v1/invites/accept" },
  applyCommands: { method: "POST", path: "/api/v1/projects/{project_id}/commands" },
  archiveProject: { method: "POST", path: "/api/v1/projects/{project_id}/archive" },
  changeMemberRole: { method: "PATCH", path: "/api/v1/workspaces/{workspace_id}/members/{membership_id}" },
  changePassword: { method: "POST", path: "/api/v1/auth/password" },
  completeSetup: { method: "POST", path: "/api/v1/setup" },
  createInvite: { method: "POST", path: "/api/v1/workspaces/{workspace_id}/invites" },
  createProject: { method: "POST", path: "/api/v1/workspaces/{workspace_id}/projects" },
  createProjectToken: { method: "POST", path: "/api/v1/projects/{project_id}/tokens" },
  createWorkspace: { method: "POST", path: "/api/v1/workspaces" },
  createWorkspaceToken: { method: "POST", path: "/api/v1/workspaces/{workspace_id}/tokens" },
  deleteProject: { method: "DELETE", path: "/api/v1/projects/{project_id}" },
  getAsset: { method: "GET", path: "/api/v1/projects/{project_id}/assets/{asset_id}" },
  getAssetContent: { method: "GET", path: "/api/v1/projects/{project_id}/assets/{asset_id}/content", blob: true },
  getAssetThumbnail: { method: "GET", path: "/api/v1/projects/{project_id}/assets/{asset_id}/thumbnail", blob: true },
  getBoard: { method: "GET", path: "/api/v1/projects/{project_id}/boards/{board_id}" },
  getHealth: { method: "GET", path: "/healthz" },
  getInstance: { method: "GET", path: "/api/v1/instance" },
  getOpenAPIDocument: { method: "GET", path: "/api/v1/openapi.json" },
  getProject: { method: "GET", path: "/api/v1/projects/{project_id}" },
  getReadiness: { method: "GET", path: "/readyz" },
  getSession: { method: "GET", path: "/api/v1/auth/session" },
  getSetup: { method: "GET", path: "/api/v1/setup" },
  getVersion: { method: "GET", path: "/version" },
  getWorkspace: { method: "GET", path: "/api/v1/workspaces/{workspace_id}" },
  getWorkspaceUsage: { method: "GET", path: "/api/v1/workspaces/{workspace_id}/usage" },
  issuePasswordResetLink: { method: "POST", path: "/api/v1/admin/password-reset-links" },
  issueVerificationLink: { method: "POST", path: "/api/v1/admin/verification-links" },
  listBoards: { method: "GET", path: "/api/v1/projects/{project_id}/boards" },
  listFolders: { method: "GET", path: "/api/v1/projects/{project_id}/folders" },
  listInvites: { method: "GET", path: "/api/v1/workspaces/{workspace_id}/invites" },
  listMemberGrants: { method: "GET", path: "/api/v1/workspaces/{workspace_id}/members/{membership_id}/grants" },
  listMembers: { method: "GET", path: "/api/v1/workspaces/{workspace_id}/members" },
  listProjectGrants: { method: "GET", path: "/api/v1/projects/{project_id}/grants" },
  listProjectTokens: { method: "GET", path: "/api/v1/projects/{project_id}/tokens" },
  listProjects: { method: "GET", path: "/api/v1/workspaces/{workspace_id}/projects" },
  listSessions: { method: "GET", path: "/api/v1/auth/sessions" },
  listTrashedProjects: { method: "GET", path: "/api/v1/workspaces/{workspace_id}/trash/projects" },
  listWorkspaceTokens: { method: "GET", path: "/api/v1/workspaces/{workspace_id}/tokens" },
  listWorkspaces: { method: "GET", path: "/api/v1/workspaces" },
  login: { method: "POST", path: "/api/v1/auth/login" },
  logout: { method: "POST", path: "/api/v1/auth/logout" },
  lookupInvite: { method: "POST", path: "/api/v1/invites/lookup" },
  registerAccount: { method: "POST", path: "/api/v1/auth/register" },
  registerWithInvite: { method: "POST", path: "/api/v1/invites/register" },
  removeMember: { method: "DELETE", path: "/api/v1/workspaces/{workspace_id}/members/{membership_id}" },
  removeProjectGrant: { method: "DELETE", path: "/api/v1/projects/{project_id}/grants/{membership_id}" },
  requestEmailVerification: { method: "POST", path: "/api/v1/auth/email-verification" },
  requestPasswordReset: { method: "POST", path: "/api/v1/auth/password-reset" },
  resendInvite: { method: "POST", path: "/api/v1/workspaces/{workspace_id}/invites/{invite_id}/resend" },
  resetPassword: { method: "POST", path: "/api/v1/auth/password-reset/confirm" },
  restoreProject: { method: "POST", path: "/api/v1/projects/{project_id}/restore" },
  revokeInvite: { method: "DELETE", path: "/api/v1/workspaces/{workspace_id}/invites/{invite_id}" },
  revokeOtherSessions: { method: "DELETE", path: "/api/v1/auth/sessions" },
  revokeProjectToken: { method: "DELETE", path: "/api/v1/projects/{project_id}/tokens/{token_id}" },
  revokeSession: { method: "DELETE", path: "/api/v1/auth/sessions/{session_id}" },
  revokeWorkspaceToken: { method: "DELETE", path: "/api/v1/workspaces/{workspace_id}/tokens/{token_id}" },
  setProjectGrant: { method: "PUT", path: "/api/v1/projects/{project_id}/grants/{membership_id}" },
  unarchiveProject: { method: "POST", path: "/api/v1/projects/{project_id}/unarchive" },
  updateProject: { method: "PATCH", path: "/api/v1/projects/{project_id}" },
  updateWorkspace: { method: "PATCH", path: "/api/v1/workspaces/{workspace_id}" },
  uploadAsset: { method: "POST", path: "/api/v1/projects/{project_id}/assets" },
  verifyEmail: { method: "POST", path: "/api/v1/auth/verify-email" },
} as const satisfies Record<keyof Operations, { method: string; path: string; blob?: true }>;
