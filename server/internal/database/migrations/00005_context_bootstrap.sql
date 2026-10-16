-- +goose Up

-- The context bootstrap (PRD §5.4): the four SECURITY DEFINER functions that a
-- request calls before it knows its workspace, to find the workspace. They
-- are the only way past row-level security that the request path can reach,
-- and no other migration declares a SECURITY DEFINER function: a test checks
-- that the database's SECURITY DEFINER functions are exactly these four.
--
-- Each returns only the identifiers that the server needs to authorise the
-- request and to set app.workspace_id, never content. The request then reads
-- the rest, such as a token's scopes, expiry, and revocation, in a
-- transaction of that workspace, under row-level security.
--
-- They run as storycreep_definer, which owns them and nothing else.
-- Row-level security holds it as it holds any role that is not a superuser,
-- so it has policies of its own, which let it read the rows of every
-- workspace, and is granted only the columns that the functions read. Only
-- storycreep_app may call them. Each sets its search_path to pg_catalog and,
-- last, pg_temp, and names its table with the table's schema, so that no
-- object that a caller creates can take the place of one that it uses.

GRANT SELECT (id, workspace_id, user_id, token_hash) ON api_token TO storycreep_definer;
GRANT SELECT (id, workspace_id, token_hash) ON invite TO storycreep_definer;
GRANT SELECT (id, workspace_id, user_id, role) ON membership TO storycreep_definer;
GRANT SELECT (id, workspace_id) ON project TO storycreep_definer;

CREATE POLICY context_bootstrap ON api_token FOR SELECT TO storycreep_definer USING (true);
CREATE POLICY context_bootstrap ON invite FOR SELECT TO storycreep_definer USING (true);
CREATE POLICY context_bootstrap ON membership FOR SELECT TO storycreep_definer USING (true);
CREATE POLICY context_bootstrap ON project FOR SELECT TO storycreep_definer USING (true);

-- The API token whose secret has the SHA-256 hash token_hash (FR-AUTH-04),
-- whether or not it is revoked or expired, so that a refused use can be
-- audited in its workspace. No row if there is none.
-- +goose StatementBegin
CREATE FUNCTION auth_resolve_token(token_hash bytea)
RETURNS TABLE (token_id uuid, workspace_id uuid, user_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
AS $$
	SELECT t.id, t.workspace_id, t.user_id FROM public.api_token t WHERE t.token_hash = $1
$$;
-- +goose StatementEnd

-- The invite whose secret has the SHA-256 hash token_hash (FR-TEN-02),
-- whether or not it is accepted, revoked, or expired, so that the server can
-- say which. No row if there is none.
-- +goose StatementBegin
CREATE FUNCTION auth_resolve_invite(token_hash bytea)
RETURNS TABLE (invite_id uuid, workspace_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
AS $$
	SELECT i.id, i.workspace_id FROM public.invite i WHERE i.token_hash = $1
$$;
-- +goose StatementEnd

-- The memberships of the user user_id, for the workspace switcher and the
-- landing page (FR-PUB-05). It returns them only to a transaction whose
-- app.user_id is that user, so that a caller that passes the wrong user's ID
-- learns nothing.
-- +goose StatementBegin
CREATE FUNCTION auth_user_memberships(user_id uuid)
RETURNS TABLE (membership_id uuid, workspace_id uuid, role text)
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
AS $$
	SELECT m.id, m.workspace_id, m.role FROM public.membership m
	WHERE m.user_id = $1 AND $1 = NULLIF(current_setting('app.user_id', true), '')::uuid
$$;
-- +goose StatementEnd

-- The workspace of the project project_id, which a URL names, or null if
-- there is no such project. The server answers 404 both then and when the
-- actor cannot access the workspace (§5.4).
-- +goose StatementBegin
CREATE FUNCTION route_project_workspace(project_id uuid)
RETURNS uuid
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
AS $$
	SELECT p.workspace_id FROM public.project p WHERE p.id = $1
$$;
-- +goose StatementEnd

ALTER FUNCTION auth_resolve_token(bytea) OWNER TO storycreep_definer;
ALTER FUNCTION auth_resolve_invite(bytea) OWNER TO storycreep_definer;
ALTER FUNCTION auth_user_memberships(uuid) OWNER TO storycreep_definer;
ALTER FUNCTION route_project_workspace(uuid) OWNER TO storycreep_definer;

-- PostgreSQL lets every role call a new function.
REVOKE ALL ON FUNCTION auth_resolve_token(bytea), auth_resolve_invite(bytea), auth_user_memberships(uuid),
	route_project_workspace(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION auth_resolve_token(bytea), auth_resolve_invite(bytea), auth_user_memberships(uuid),
	route_project_workspace(uuid) TO storycreep_app;
