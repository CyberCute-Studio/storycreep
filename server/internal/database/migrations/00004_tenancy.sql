-- +goose Up

-- Identity and tenancy (PRD §5.3, §5.4, ADR-002): accounts, workspaces, and
-- the tables of a workspace's members, project grants, invitations, API
-- tokens, and audit log, with a shell of the project table, which
-- route_project_workspace reads (00005). The project lifecycle (FR-PROJ-01)
-- adds the rest of the project's columns.
--
-- A workspace is the tenant boundary. Every table whose rows belong to a
-- workspace has a non-null workspace_id, and row-level security, forced, with
-- a policy that compares workspace_id with the request transaction's
-- app.workspace_id (package database, Scope), so that a transaction sees and
-- writes only its own workspace's rows, and a transaction without a workspace
-- none. The tables that span workspaces, "user", workspace, and session, have
-- no row-level security (§5.4). A test enumerates the tables and fails on one
-- that is neither (FR-TEN-01 AC1), and the isolation harness in
-- isolationtest tries every tenant table from another workspace (NFR-SEC-01).
--
-- Every tenant table's workspace_id has a foreign key to workspace, except
-- audit_entry's, so that the audit log outlives a workspace that is hard
-- deleted (FR-TEN-01 AC1, FR-TEN-05 AC2). A row that refers to another row of
-- its workspace does so through a foreign key that includes workspace_id, so
-- that it cannot refer to a row of another workspace. PostgreSQL checks
-- foreign keys past row-level security, so this also keeps a request from
-- probing whether an ID exists in another workspace.
--
-- Deleting a workspace or a user deletes the rows that belong to it, by
-- cascade. A cascade is not held by row-level security either, and deleting a
-- user reaches the rows of every workspace the user belongs to, so the
-- application login cannot delete from either table: hard deletion is the
-- maintenance path's (§5.4). Every foreign key to "user" has an index that
-- leads with it, so that deleting a user finds the rows that refer to the
-- user without reading every workspace's rows of the table. A foreign key to
-- another row of the workspace includes workspace_id, which an index of the
-- table leads with already.
--
-- IDs are UUIDv7s, which the server generates (§5.2), so no column has a
-- default for them.

-- Email addresses compare without regard to case (§5.3), with the type and
-- operators of the citext extension, which the migrations and the server
-- find in the public schema. CREATE EXTENSION IF NOT EXISTS keeps an
-- extension that the database has in another schema already, as a template
-- or a hosting provider can give it, and the first column of the type would
-- then fail with an error that the type does not exist, right after a CREATE
-- EXTENSION that succeeded. So the migration refuses such a database with an
-- error that says how to move the extension.
-- +goose StatementBegin
DO $$
DECLARE
	ext_schema name;
BEGIN
	SELECT n.nspname INTO ext_schema
	FROM pg_extension e JOIN pg_namespace n ON n.oid = e.extnamespace
	WHERE e.extname = 'citext';
	IF ext_schema <> 'public' THEN
		RAISE EXCEPTION USING MESSAGE = format('the database has the citext extension in the schema %I, where '
			|| 'Storycreep does not find it; move it to the public schema with ALTER EXTENSION citext SET SCHEMA '
			|| 'public, as a superuser, and migrate again', ext_schema);
	END IF;
END
$$;
-- +goose StatementEnd
CREATE EXTENSION IF NOT EXISTS citext WITH SCHEMA public;

CREATE TABLE "user" (
	id uuid PRIMARY KEY,
	email citext NOT NULL UNIQUE,
	-- The argon2id hash of the password (FR-AUTH-01), or null for an account
	-- without one.
	password_hash text,
	display_name text NOT NULL,
	-- The user's preferred locale as a BCP 47 tag, or null to follow the
	-- request's Accept-Language (FR-I18N-02).
	locale text,
	email_verified_at timestamptz,
	status text NOT NULL DEFAULT 'active'
		CHECK (status IN ('active', 'suspended', 'pending_deletion', 'deleted')),
	is_instance_admin boolean NOT NULL DEFAULT false,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE workspace (
	id uuid PRIMARY KEY,
	name text NOT NULL,
	-- Unique on the instance (FR-TEN-01 AC3).
	slug text NOT NULL UNIQUE,
	-- The plan that sets the workspace's limits and features (FR-ENT-01).
	plan_key text NOT NULL,
	settings jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(settings) = 'object'),
	status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'pending_deletion')),
	deleted_at timestamptz,
	hard_delete_after timestamptz,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now()
);

-- The sessions of a user, who may use them in any workspace (FR-AUTH-02).
CREATE TABLE session (
	id uuid PRIMARY KEY,
	user_id uuid NOT NULL REFERENCES "user" ON DELETE CASCADE,
	-- The SHA-256 hash of the session's secret, which only its cookie holds
	-- (FR-AUTH-02 AC3).
	token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
	user_agent text,
	ip inet,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	last_seen_at timestamptz NOT NULL DEFAULT now(),
	expires_at timestamptz NOT NULL,
	revoked_at timestamptz
);
CREATE INDEX session_user_id ON session (user_id);

CREATE TABLE project (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	-- What the foreign keys that keep a row's project in the row's workspace
	-- refer to.
	UNIQUE (workspace_id, id)
);

CREATE TABLE membership (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	user_id uuid NOT NULL REFERENCES "user" ON DELETE CASCADE,
	role text NOT NULL CHECK (role IN ('owner', 'admin', 'editor', 'commenter', 'viewer', 'guest')),
	invited_by uuid REFERENCES "user" ON DELETE SET NULL,
	joined_at timestamptz NOT NULL DEFAULT now(),
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (workspace_id, id),
	-- Also what auth_user_memberships looks a user's memberships up by (00005).
	UNIQUE (user_id, workspace_id)
);
CREATE INDEX membership_invited_by ON membership (invited_by);

-- A member's role on one project, which replaces the workspace role there
-- (FR-TEN-04).
CREATE TABLE project_grant (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	membership_id uuid NOT NULL,
	project_id uuid NOT NULL,
	-- Never owner (FR-TEN-04), nor guest, which is a workspace role.
	role text NOT NULL CHECK (role IN ('admin', 'editor', 'commenter', 'viewer')),
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (workspace_id, membership_id, project_id),
	FOREIGN KEY (workspace_id, membership_id) REFERENCES membership (workspace_id, id) ON DELETE CASCADE,
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX project_grant_project ON project_grant (workspace_id, project_id);

CREATE TABLE invite (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	-- Null for a link invite, which whoever holds the link can accept
	-- (FR-TEN-02 AC3).
	email citext,
	role text NOT NULL CHECK (role IN ('owner', 'admin', 'editor', 'commenter', 'viewer', 'guest')),
	-- The SHA-256 hash of the invite's secret, which only its link holds.
	token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
	expires_at timestamptz NOT NULL,
	accepted_at timestamptz,
	revoked_at timestamptz,
	created_by uuid REFERENCES "user" ON DELETE SET NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX invite_workspace ON invite (workspace_id);
CREATE INDEX invite_created_by ON invite (created_by);

CREATE TABLE api_token (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	-- The user who created the token, whose permissions it never exceeds
	-- (FR-AUTH-04).
	user_id uuid NOT NULL REFERENCES "user" ON DELETE CASCADE,
	-- The one project the token is narrowed to, or null for the whole
	-- workspace.
	project_id uuid,
	name text NOT NULL,
	-- The SHA-256 hash of the token's secret (NFR-SEC-05).
	token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
	scopes text[] NOT NULL
		CHECK (scopes <@ ARRAY['project:read', 'project:write', 'snapshot:read', 'snapshot:write', 'export:read']),
	expires_at timestamptz,
	last_used_at timestamptz,
	revoked_at timestamptz,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	-- Deleting the project deletes the token with it. Setting project_id to
	-- null instead would widen the token to the whole workspace.
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE
);
CREATE INDEX api_token_workspace ON api_token (workspace_id, user_id);
CREATE INDEX api_token_user ON api_token (user_id);

-- The audit log (FR-TEN-07). Neither workspace_id nor the actor's IDs have a
-- foreign key, so that an entry outlives its workspace, account, and token,
-- until the audit retention job deletes it (FR-TEN-05 AC2, FR-GDPR-02).
-- Entries never change, so there is no updated_at, and the application login
-- may add and read them but neither change nor delete them (FR-TEN-07 AC1).
CREATE TABLE audit_entry (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL,
	actor_user_id uuid,
	actor_token_id uuid,
	action text NOT NULL,
	subject_kind text,
	subject_id uuid,
	result text NOT NULL,
	ip inet,
	user_agent text,
	request_id text,
	metadata jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(metadata) = 'object'),
	created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_entry_workspace ON audit_entry (workspace_id, created_at);

-- Row-level security on every tenant table (§5.4, NFR-SEC-01). It is forced,
-- although the superuser that owns the tables bypasses it all the same, so
-- that it would hold a table's owner if one were ever not a superuser. The
-- policies apply to storycreep_app, so that a role without a policy of its
-- own, which the context bootstrap's storycreep_definer has (00005), sees no
-- rows at all.

ALTER TABLE project ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON project TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

ALTER TABLE membership ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON membership TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

ALTER TABLE project_grant ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON project_grant TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

ALTER TABLE invite ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON invite TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

ALTER TABLE api_token ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON api_token TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

ALTER TABLE audit_entry ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON audit_entry TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);

-- The application's privileges. It deletes neither users nor workspaces (see
-- above), and only adds and reads audit entries.
GRANT SELECT, INSERT, UPDATE ON "user", workspace TO storycreep_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON session, project, membership, project_grant, invite, api_token
	TO storycreep_app;
GRANT SELECT, INSERT ON audit_entry TO storycreep_app;

-- The maintenance jobs read across workspaces (§5.4). Each job that changes
-- rows is granted that in the migration that adds it. Like the server, they
-- read which migrations are applied, to tell whether the database is ready
-- for them.
GRANT SELECT ON "user", workspace, session, project, membership, project_grant, invite, api_token, audit_entry,
	goose_db_version TO storycreep_maintenance;
