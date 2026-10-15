-- +goose Up

-- The database roles (PRD §5.4, ADR-002). Migrations run as a superuser, which
-- owns every table. The server connects as an ordinary login that is granted
-- storycreep_app (provision.go).
--
--   storycreep_app          The application's privileges. It owns nothing,
--                           and row-level security applies to it.
--   storycreep_definer      Owns the SECURITY DEFINER functions of the context
--                           bootstrap (§5.4), and nothing else.
--   storycreep_maintenance  Bypasses row-level security for the closed list of
--                           maintenance jobs (§5.4). It owns nothing.
--
-- None of them can log in. Roles belong to the PostgreSQL cluster, not to the
-- database, so a role that exists already, such as one another Storycreep
-- database in the cluster created, is kept, and only the attributes that
-- differ are corrected. Altering a role that needs no change would still
-- conflict with the same statement run from another database at the same time.

-- +goose StatementBegin
DO $$
DECLARE
	r record;
	attributes text;
BEGIN
	FOR r IN SELECT * FROM (VALUES
		('storycreep_app', false),
		('storycreep_definer', false),
		('storycreep_maintenance', true)
	) AS roles (name, bypassrls)
	LOOP
		attributes := 'NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION '
			|| CASE WHEN r.bypassrls THEN 'BYPASSRLS' ELSE 'NOBYPASSRLS' END;
		IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = r.name) THEN
			EXECUTE format('CREATE ROLE %I %s', r.name, attributes);
		ELSIF EXISTS (
			SELECT FROM pg_roles
			WHERE rolname = r.name
				AND (rolcanlogin OR rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication
					OR rolbypassrls <> r.bypassrls)
		) THEN
			EXECUTE format('ALTER ROLE %I %s', r.name, attributes);
		END IF;
	END LOOP;
END
$$;
-- +goose StatementEnd

-- PostgreSQL 15 and later no longer let every role create objects in the
-- public schema, but a cluster upgraded from an earlier version keeps the old
-- privilege.
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO storycreep_app, storycreep_definer, storycreep_maintenance;

-- A server that does not apply migrations itself reads which ones are
-- applied, to tell whether the database is ready for it.
GRANT SELECT ON goose_db_version TO storycreep_app;
