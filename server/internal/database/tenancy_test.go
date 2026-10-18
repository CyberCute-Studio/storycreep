package database_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// crossWorkspaceTables are the tables whose rows span workspaces, which have
// no workspace_id and no row-level security (PRD §5.4), besides River's,
// whose names start with river_ and which belong to the job queue. Every other
// table is a tenant table.
var crossWorkspaceTables = []string{"email_token", "goose_db_version", "instance", "instance_key", "login_attempt",
	"rate_limit", "session", "user", "workspace"}

// auditTable is the one tenant table whose workspace_id has no foreign key,
// so that the audit log outlives a workspace that is hard deleted, and may be
// null, for the events that belong to no workspace, such as a login
// (FR-TEN-01 AC1, FR-TEN-07).
const auditTable = "audit_entry"

// tenantTableProblems enumerates the tables of the public schema and returns
// what is wrong with them: a table that is neither a tenant table nor one of
// crossWorkspaceTables, a tenant table whose workspace_id is missing, may be
// null (except audit_entry's), is not a uuid, lacks the foreign key to
// workspace (or, for audit_entry, has one), or leads no index, a tenant table without forced
// row-level security or without a policy for storycreep_app, or that
// storycreep_maintenance may not read, as the maintenance jobs must (PRD
// §5.4), a table that spans workspaces but has a workspace_id, and a tenant
// table that is not in registered, the isolation harness's tables, or a name
// in registered that is not a tenant table.
func tenantTableProblems(t *testing.T, conn *pgx.Conn, registered []string) []string {
	t.Helper()
	rows, err := conn.Query(context.Background(), `
		SELECT c.relname, a.attnum IS NOT NULL, coalesce(a.attnotnull, false),
			coalesce(a.atttypid = 'uuid'::regtype, false),
			EXISTS (SELECT FROM pg_constraint k
				WHERE k.conrelid = c.oid AND k.contype = 'f' AND k.conkey = ARRAY[a.attnum]
					AND k.confrelid = 'public.workspace'::regclass
					AND k.confkey = ARRAY[(SELECT attnum FROM pg_attribute
						WHERE attrelid = 'public.workspace'::regclass AND attname = 'id')]),
			EXISTS (SELECT FROM pg_index i WHERE i.indrelid = c.oid AND i.indkey[0] = a.attnum),
			c.relrowsecurity, c.relforcerowsecurity,
			EXISTS (SELECT FROM pg_policy p
				WHERE p.polrelid = c.oid AND p.polcmd = '*' AND 'storycreep_app'::regrole = ANY (p.polroles)),
			has_table_privilege('storycreep_maintenance', c.oid, 'SELECT')
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_attribute a ON a.attrelid = c.oid AND a.attname = 'workspace_id' AND NOT a.attisdropped
		WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p')
		ORDER BY c.relname`)
	if err != nil {
		t.Fatal(err)
	}
	type table struct {
		name                                 string
		hasColumn, notNull, uuid, fk, index  bool
		rowSecurity, forced, workspacePolicy bool
		maintenanceReads                     bool
	}
	tables, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (table, error) {
		var x table
		err := r.Scan(&x.name, &x.hasColumn, &x.notNull, &x.uuid, &x.fk, &x.index, &x.rowSecurity, &x.forced,
			&x.workspacePolicy, &x.maintenanceReads)
		return x, err
	})
	if err != nil {
		t.Fatal(err)
	}
	var problems []string
	var tenant []string
	for _, tb := range tables {
		add := func(format string, args ...any) {
			problems = append(problems, tb.name+": "+fmt.Sprintf(format, args...))
		}
		if slices.Contains(crossWorkspaceTables, tb.name) || strings.HasPrefix(tb.name, "river_") {
			if tb.hasColumn {
				add("spans workspaces, but has a workspace_id")
			}
			continue
		}
		tenant = append(tenant, tb.name)
		if !tb.hasColumn {
			add("has no workspace_id; a table whose rows span workspaces is one of the cross-workspace tables")
			continue
		}
		if !tb.notNull && tb.name != auditTable {
			add("workspace_id may be null")
		}
		if !tb.uuid {
			add("workspace_id is not a uuid")
		}
		switch {
		case tb.name == auditTable && tb.fk:
			add("workspace_id has a foreign key to workspace, which would delete the audit log with its workspace")
		case tb.name != auditTable && !tb.fk:
			add("workspace_id has no foreign key to workspace")
		}
		if !tb.index {
			add("no index starts with workspace_id")
		}
		if !tb.rowSecurity || !tb.forced {
			add("row-level security is not enabled and forced")
		}
		if !tb.workspacePolicy {
			add("has no policy for storycreep_app")
		}
		if !tb.maintenanceReads {
			add("storycreep_maintenance may not read it")
		}
		if !slices.Contains(registered, tb.name) {
			add("is not one of the isolation harness's tables")
		}
	}
	for _, name := range registered {
		if !slices.Contains(tenant, name) {
			problems = append(problems, name+": is one of the isolation harness's tables, but not a tenant table")
		}
	}
	return problems
}

func tableNames(tables []isolationtest.Table) []string {
	var names []string
	for _, tb := range tables {
		names = append(names, tb.Name)
	}
	return names
}

// TestTenantTables enumerates the tables that the migrations create, and
// checks that each one whose rows belong to a workspace has a non-null
// workspace_id with a foreign key to workspace (audit_entry's has none, and
// is null for an event of no workspace),
// forced row-level security with a policy for the application, a grant that
// lets storycreep_maintenance read it, and a place among the isolation
// harness's tables, and that every other table is one of those that span
// workspaces.
//
// Verifies: FR-TEN-01/AC1, NFR-SEC-01
func TestTenantTables(t *testing.T) {
	t.Parallel()
	d := dbtest.New(t)
	for _, p := range tenantTableProblems(t, d.Admin(t), tableNames(isolationtest.Tables)) {
		t.Error(p)
	}
}

// TestTenantTablesCheckFindsOmissions runs the check of TestTenantTables on
// tables that break its rules, so that TestTenantTables cannot pass because
// the check stopped working.
//
// Verifies: FR-TEN-01/AC1
func TestTenantTablesCheckFindsOmissions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	admin := d.Admin(t)
	if _, err := admin.Exec(ctx, `
		CREATE TABLE stray (id uuid PRIMARY KEY);
		CREATE TABLE loose (id uuid PRIMARY KEY, workspace_id text);
		CREATE TABLE unforced (id uuid PRIMARY KEY, workspace_id uuid NOT NULL REFERENCES workspace);
		CREATE INDEX unforced_workspace ON unforced (workspace_id);
		ALTER TABLE unforced ENABLE ROW LEVEL SECURITY;
		CREATE POLICY reads ON unforced FOR SELECT TO storycreep_app USING (true);
		CREATE TABLE river_extra (id uuid PRIMARY KEY, workspace_id uuid);
		ALTER TABLE audit_entry ADD FOREIGN KEY (workspace_id) REFERENCES workspace`); err != nil {
		t.Fatal(err)
	}
	got := tenantTableProblems(t, admin, append(tableNames(isolationtest.Tables), "unforced", "ghost"))
	want := []string{
		"audit_entry: workspace_id has a foreign key to workspace, which would delete the audit log with its workspace",
		"loose: workspace_id may be null",
		"loose: workspace_id is not a uuid",
		"loose: workspace_id has no foreign key to workspace",
		"loose: no index starts with workspace_id",
		"loose: row-level security is not enabled and forced",
		"loose: has no policy for storycreep_app",
		"loose: storycreep_maintenance may not read it",
		"loose: is not one of the isolation harness's tables",
		"river_extra: spans workspaces, but has a workspace_id",
		"stray: has no workspace_id; a table whose rows span workspaces is one of the cross-workspace tables",
		"unforced: row-level security is not enabled and forced",
		"unforced: has no policy for storycreep_app",
		"unforced: storycreep_maintenance may not read it",
		"ghost: is one of the isolation harness's tables, but not a tenant table",
	}
	if !slices.Equal(got, want) {
		t.Errorf("problems =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// tablePrivileges lists, for each of storycreep_app and
// storycreep_maintenance and each of the tables, which privileges the role
// has on the table: on the whole table or, for those that PostgreSQL grants
// on columns too, on any of its columns, granted to the role, to a role whose
// privileges it inherits, or to PUBLIC, or given by a predefined role such as
// pg_write_all_data.
func tablePrivileges(t *testing.T, conn *pgx.Conn, tables []string) []string {
	t.Helper()
	rows, err := conn.Query(context.Background(), `
		SELECT format('%s on %s: %s', r.rolname, c.relname, array_to_string(ARRAY(
			SELECT p.name FROM unnest(ARRAY['SELECT', 'INSERT', 'UPDATE', 'DELETE', 'TRUNCATE', 'REFERENCES', 'TRIGGER'])
				WITH ORDINALITY p (name, n)
			WHERE CASE WHEN p.name IN ('DELETE', 'TRUNCATE', 'TRIGGER') THEN has_table_privilege(r.oid, c.oid, p.name)
				ELSE has_any_column_privilege(r.oid, c.oid, p.name) END
			ORDER BY p.n), ', '))
		FROM pg_roles r, pg_class c
		WHERE r.rolname IN ('storycreep_app', 'storycreep_maintenance')
			AND c.relnamespace = 'public'::regnamespace AND c.relname = ANY ($1)
		ORDER BY r.rolname, c.relname`, tables)
	var got []string
	if err == nil {
		got, err = pgx.CollectRows(rows, pgx.RowTo[string])
	}
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// TestTenancyPrivileges pins what the server's roles may do with the tables
// of the tenancy migration, 00004, which the isolation harness does not: it
// expects refused whatever operations the grants refuse. The application may
// delete neither a user nor a workspace, whose cascades reach every
// workspace's rows past row-level security, and may neither change nor
// delete an audit entry (FR-TEN-07 AC1); storycreep_maintenance may only read
// the tables; and neither role may truncate one, which row-level security
// does not hold either. Only these grants keep the logins from that, which is
// why the server refuses a login with pg_write_all_data (checkLogin). A
// migration that grants a job more, as the jobs that delete rows will need,
// changes the test, and says why with its grant. The test then grants more on
// a table and on a column, to check that it finds them.
//
// Verifies: NFR-SEC-01, FR-TEN-07/AC1
func TestTenancyPrivileges(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	admin := d.Admin(t)
	tables := []string{"api_token", "audit_entry", "invite", "membership", "project", "project_grant", "session",
		"user", "workspace"}
	want := []string{
		"storycreep_app on api_token: SELECT, INSERT, UPDATE, DELETE",
		"storycreep_app on audit_entry: SELECT, INSERT",
		"storycreep_app on invite: SELECT, INSERT, UPDATE, DELETE",
		"storycreep_app on membership: SELECT, INSERT, UPDATE, DELETE",
		"storycreep_app on project: SELECT, INSERT, UPDATE, DELETE",
		"storycreep_app on project_grant: SELECT, INSERT, UPDATE, DELETE",
		"storycreep_app on session: SELECT, INSERT, UPDATE, DELETE",
		"storycreep_app on user: SELECT, INSERT, UPDATE",
		"storycreep_app on workspace: SELECT, INSERT, UPDATE",
	}
	for _, table := range tables {
		want = append(want, "storycreep_maintenance on "+table+": SELECT")
	}
	if got := tablePrivileges(t, admin, tables); !slices.Equal(got, want) {
		t.Errorf("privileges =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	if _, err := admin.Exec(ctx, `
		GRANT DELETE ON workspace TO storycreep_app;
		GRANT UPDATE (result) ON audit_entry TO storycreep_app;
		GRANT TRUNCATE ON project TO storycreep_app;
		GRANT INSERT (id) ON session TO storycreep_maintenance`); err != nil {
		t.Fatal(err)
	}
	var changed []string
	for _, p := range tablePrivileges(t, admin, tables) {
		if !slices.Contains(want, p) {
			changed = append(changed, p)
		}
	}
	wantChanged := []string{
		"storycreep_app on audit_entry: SELECT, INSERT, UPDATE",
		"storycreep_app on project: SELECT, INSERT, UPDATE, DELETE, TRUNCATE",
		"storycreep_app on workspace: SELECT, INSERT, UPDATE, DELETE",
		"storycreep_maintenance on session: SELECT, INSERT",
	}
	if !slices.Equal(changed, wantChanged) {
		t.Errorf("after more grants, the check finds\n%s\nwant\n%s", strings.Join(changed, "\n"),
			strings.Join(wantChanged, "\n"))
	}
}

// errRolledBack rolls back a transaction whose statements worked.
var errRolledBack = errors.New("rolled back")

// crossWorkspaceReferences adds, in transactions of workspace A that it rolls
// back, rows of A that refer through each foreign key between tenant tables
// to a row of workspace B, and returns the keys through which it could, by
// the table and column that refers. It first adds each row with a reference
// to A's row instead, and fails the test unless that works, so that a
// statement that fails for another reason does not pass for one that the key
// refuses.
func crossWorkspaceReferences(t *testing.T, f *isolationtest.Fixture) []string {
	t.Helper()
	ctx := context.Background()
	a := f.A
	const grant = "INSERT INTO project_grant (id, workspace_id, membership_id, project_id, role) " +
		"VALUES ($1, $2, $3, $4, 'viewer')"
	const token = "INSERT INTO api_token (id, workspace_id, user_id, project_id, name, token_hash, scopes) " +
		"VALUES ($1, $2, $3, $4, 'isolation', $5, ARRAY['project:read'])"
	// The fixture's operation has a random seq, which 1 is not.
	const operation = "INSERT INTO operation (id, workspace_id, project_id, seq, command_ids, type, payload, " +
		"entity_refs) VALUES ($1, $2, $3, 1, ARRAY[$4::uuid], 'isolation.test', '[]', '[]')"
	const applied = "INSERT INTO applied_command (workspace_id, project_id, command_id, seq) VALUES ($1, $2, $3, 1)"
	var reached []string
	for _, c := range []struct {
		key string
		sql string
		// args are the arguments of a new row of A that refers to w's row.
		args func(w *isolationtest.Workspace) []any
	}{
		{"project_grant.membership_id", grant, func(w *isolationtest.Workspace) []any {
			return []any{ids.New(), a.ID, w.Rows["membership"], a.Rows["project"]}
		}},
		{"project_grant.project_id", grant, func(w *isolationtest.Workspace) []any {
			return []any{ids.New(), a.ID, a.Rows["membership"], w.Rows["project"]}
		}},
		{"api_token.project_id", token, func(w *isolationtest.Workspace) []any {
			return []any{ids.New(), a.ID, a.UserID, w.Rows["project"], isolationtest.TokenHash()}
		}},
		{"operation.project_id", operation, func(w *isolationtest.Workspace) []any {
			return []any{ids.New(), a.ID, w.Rows["project"], ids.New()}
		}},
		{"applied_command.project_id", applied, func(w *isolationtest.Workspace) []any {
			return []any{a.ID, w.Rows["project"], ids.New()}
		}},
	} {
		for _, w := range []*isolationtest.Workspace{a, f.B} {
			err := f.DB.InTx(ctx, a.Scope(), func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, c.sql, c.args(w)...); err != nil {
					return err
				}
				return errRolledBack
			})
			var pgErr *pgconn.PgError
			switch {
			case w == a:
				if !errors.Is(err, errRolledBack) {
					t.Errorf("%s: a row that refers to its own workspace's: %v", c.key, err)
				}
			case errors.Is(err, errRolledBack):
				reached = append(reached, c.key)
			case !errors.As(err, &pgErr) || pgErr.Code != "23503":
				t.Errorf("%s: a row that refers to another workspace's: %v, want a foreign key violation", c.key, err)
			}
		}
	}
	return reached
}

// TestWorkspaceScopedForeignKeys checks that a row cannot refer to a row of
// another workspace through the foreign keys between tenant tables:
// project_grant's to membership and to project, and api_token's to project
// (00004), and operation's and applied_command's to project (00012), each of
// which includes workspace_id. PostgreSQL checks foreign keys past row-level
// security, so a key on the ID alone would let a transaction of workspace A
// refer to B's rows, and find out whether an ID exists in B. The test then
// weakens each key to one on the ID alone, to check that it finds them.
//
// Verifies: NFR-SEC-01
func TestWorkspaceScopedForeignKeys(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := isolationtest.New(t, dbtest.New(t), isolationtest.Tables)
	if got := crossWorkspaceReferences(t, f); len(got) > 0 {
		t.Errorf("rows of one workspace refer to another's through %v", got)
	}

	if _, err := f.Admin().Exec(ctx, `
		ALTER TABLE project_grant DROP CONSTRAINT project_grant_workspace_id_membership_id_fkey,
			DROP CONSTRAINT project_grant_workspace_id_project_id_fkey,
			ADD FOREIGN KEY (membership_id) REFERENCES membership ON DELETE CASCADE,
			ADD FOREIGN KEY (project_id) REFERENCES project ON DELETE CASCADE;
		ALTER TABLE api_token DROP CONSTRAINT api_token_workspace_id_project_id_fkey,
			ADD FOREIGN KEY (project_id) REFERENCES project ON DELETE CASCADE;
		ALTER TABLE operation DROP CONSTRAINT operation_workspace_id_project_id_fkey,
			ADD FOREIGN KEY (project_id) REFERENCES project ON DELETE CASCADE;
		ALTER TABLE applied_command DROP CONSTRAINT applied_command_workspace_id_project_id_fkey,
			ADD FOREIGN KEY (project_id) REFERENCES project ON DELETE CASCADE`); err != nil {
		t.Fatal(err)
	}
	want := []string{"project_grant.membership_id", "project_grant.project_id", "api_token.project_id",
		"operation.project_id", "applied_command.project_id"}
	if got := crossWorkspaceReferences(t, f); !slices.Equal(got, want) {
		t.Errorf("with keys on the ID alone, rows refer to another workspace's through %v, want %v", got, want)
	}
}

// projectKey is the foreign key that every table with a project_id has, as
// PostgreSQL writes it: with the workspace_id of the row, to its project in
// the same workspace, which deletes the row with the project.
const projectKey = "FOREIGN KEY (workspace_id, project_id) REFERENCES project(workspace_id, id) ON DELETE CASCADE"

// projectTableProblems returns the tables of the public schema with a
// project_id column that lacks projectKey, so that deleting the project
// would fail on their rows, or leave them.
func projectTableProblems(t *testing.T, conn *pgx.Conn) []string {
	t.Helper()
	rows, err := conn.Query(context.Background(), `
		SELECT c.relname FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attname = 'project_id' AND NOT a.attisdropped
		WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p')
			AND NOT EXISTS (SELECT FROM pg_constraint k
				WHERE k.conrelid = c.oid AND k.contype = 'f' AND pg_get_constraintdef(k.oid) = $1)
		ORDER BY c.relname`, projectKey)
	if err != nil {
		t.Fatal(err)
	}
	problems, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return problems
}

// TestProjectTables checks that every table with a project_id has a foreign
// key, with its workspace_id, to its project, which deletes its rows with the
// project, so that the purge of a project whose time in the trash has passed
// deletes every row of its content, whichever table a later release adds it
// to (PRD FR-PROJ-01 AC3). It then adds a table whose project_id has a key on
// the ID alone that deletes nothing, and one with no key at all, to check
// that it finds them.
//
// Verifies: FR-PROJ-01/AC3
func TestProjectTables(t *testing.T) {
	t.Parallel()
	d := dbtest.New(t)
	admin := d.Admin(t)
	if got := projectTableProblems(t, admin); len(got) > 0 {
		t.Errorf("the tables %v have a project_id without the key %q", got, projectKey)
	}
	if _, err := admin.Exec(context.Background(), `
		CREATE TABLE loose_key (workspace_id uuid, project_id uuid REFERENCES project (id));
		CREATE TABLE no_key (workspace_id uuid, project_id uuid)`); err != nil {
		t.Fatal(err)
	}
	if got, want := projectTableProblems(t, admin), []string{"loose_key", "no_key"}; !slices.Equal(got, want) {
		t.Errorf("with tables that lack the key, the check finds %v, want %v", got, want)
	}
}

// TestTenancyMigrationNeedsCitextInPublic checks that the tenancy migration
// refuses a database that has the citext extension in a schema other than
// public, where the migrations would not find its type, with an error that
// says how to move the extension, and that it applies once the extension is
// moved.
func TestTenancyMigrationNeedsCitextInPublic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.Empty(t)
	admin := d.Admin(t)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA extensions; CREATE EXTENSION citext WITH SCHEMA extensions"); err != nil {
		t.Fatal(err)
	}
	_, err := database.Migrate(ctx, d.AdminURL, d.AppURL, database.MigrateOptions{})
	var merr *database.MigrationError
	if !errors.As(err, &merr) || merr.Name != "00004_tenancy.sql" ||
		!strings.Contains(err.Error(), "the citext extension in the schema extensions") ||
		!strings.Contains(err.Error(), "ALTER EXTENSION citext SET SCHEMA public") {
		t.Fatalf("Migrate returned %v, want the tenancy migration to refuse citext outside public, and say how to "+
			"move it", err)
	}
	if _, err := admin.Exec(ctx, "ALTER EXTENSION citext SET SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Migrate(ctx, d.AdminURL, d.AppURL, database.MigrateOptions{}); err != nil {
		t.Errorf("Migrate after the extension moved to public: %v", err)
	}
}

// bootstrapFunction is a SECURITY DEFINER function as the catalog describes
// it.
type bootstrapFunction struct {
	// signature is its name and argument types, as regprocedure writes them.
	signature string
	result    string
	owner     string
	config    string
	// executors are the roles other than the owner that may call it.
	executors string
}

// securityDefinerFunctions returns the database's SECURITY DEFINER functions,
// outside PostgreSQL's own schemas.
func securityDefinerFunctions(t *testing.T, conn *pgx.Conn) []bootstrapFunction {
	t.Helper()
	rows, err := conn.Query(context.Background(), `
		SELECT p.oid::regprocedure::text, pg_get_function_result(p.oid), p.proowner::regrole::text,
			coalesce(array_to_string(p.proconfig, ' '), ''),
			coalesce((SELECT string_agg(CASE WHEN a.grantee = 0 THEN 'PUBLIC' ELSE a.grantee::regrole::text END, ' '
					ORDER BY 1)
				FROM aclexplode(coalesce(p.proacl, acldefault('f', p.proowner))) a
				WHERE a.privilege_type = 'EXECUTE' AND a.grantee <> p.proowner), '')
		FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE p.prosecdef AND n.nspname <> 'information_schema' AND n.nspname NOT LIKE 'pg\_%'
		ORDER BY 1`)
	var got []bootstrapFunction
	if err == nil {
		got, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (bootstrapFunction, error) {
			var f bootstrapFunction
			err := r.Scan(&f.signature, &f.result, &f.owner, &f.config, &f.executors)
			return f, err
		})
	}
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// TestContextBootstrapFunctions checks that the database's SECURITY DEFINER
// functions are exactly the four of the context bootstrap (PRD §5.4), owned
// by storycreep_definer, with a search_path of their own, callable by
// storycreep_app alone, and returning identifiers only. It checks that
// another such function is found, too.
func TestContextBootstrapFunctions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	admin := d.Admin(t)
	const config = "search_path=pg_catalog, pg_temp"
	want := []bootstrapFunction{
		{"auth_resolve_invite(bytea)", "TABLE(invite_id uuid, workspace_id uuid)", "storycreep_definer", config,
			"storycreep_app"},
		{"auth_resolve_token(bytea)", "TABLE(token_id uuid, workspace_id uuid, user_id uuid)", "storycreep_definer",
			config, "storycreep_app"},
		{"auth_user_memberships(uuid)", "TABLE(membership_id uuid, workspace_id uuid, role text)",
			"storycreep_definer", config, "storycreep_app"},
		{"route_project_workspace(uuid)", "uuid", "storycreep_definer", config, "storycreep_app"},
	}
	if got := securityDefinerFunctions(t, admin); !slices.Equal(got, want) {
		t.Errorf("SECURITY DEFINER functions =\n%+v\nwant\n%+v", got, want)
	}

	if _, err := admin.Exec(ctx, "CREATE FUNCTION extra() RETURNS int LANGUAGE sql SECURITY DEFINER AS 'SELECT 1'"); err != nil {
		t.Fatal(err)
	}
	got := securityDefinerFunctions(t, admin)
	if !slices.ContainsFunc(got, func(f bootstrapFunction) bool {
		return f.signature == "extra()" && f.owner == "postgres" && f.executors == "PUBLIC"
	}) {
		t.Errorf("the check does not find another SECURITY DEFINER function: %+v", got)
	}
}

// TestContextBootstrapResolves checks what the context bootstrap's functions
// return to the application login before it knows the workspace, which
// row-level security would otherwise hide from it: a token's and an invite's
// workspace by the hash of their secret, revoked or not, a project's
// workspace, and the current user's memberships, but no other user's.
func TestContextBootstrapResolves(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	f := isolationtest.New(t, d, isolationtest.Tables)
	admin := f.Admin()
	b := f.B

	var tokenHash, inviteHash []byte
	if err := admin.QueryRow(ctx, "UPDATE api_token SET revoked_at = now() WHERE id = $1 RETURNING token_hash",
		b.Rows["api_token"]).Scan(&tokenHash); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx, "SELECT token_hash FROM invite WHERE id = $1", b.Rows["invite"]).
		Scan(&inviteHash); err != nil {
		t.Fatal(err)
	}
	// A user who is a member of both workspaces.
	user, err := isolationtest.NewUser(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	memberships := map[ids.UUID]ids.UUID{f.A.ID: ids.New(), b.ID: ids.New()}
	for ws, m := range memberships {
		if _, err := admin.Exec(ctx, "INSERT INTO membership (id, workspace_id, user_id, role) VALUES ($1, $2, $3, 'viewer')",
			m, ws, user); err != nil {
			t.Fatal(err)
		}
	}

	type membership struct {
		ID, WorkspaceID ids.UUID
		Role            string
	}
	listMemberships := func(scope database.Scope) []membership {
		t.Helper()
		var got []membership
		err := f.DB.InTx(ctx, scope, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, "SELECT membership_id, workspace_id, role FROM auth_user_memberships($1) "+
				"ORDER BY membership_id", user)
			if err == nil {
				got, err = pgx.CollectRows(rows, pgx.RowToStructByPos[membership])
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	// Without a workspace, as a request is before it finds its own.
	err = f.DB.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		var token, workspace, owner ids.UUID
		if err := tx.QueryRow(ctx, "SELECT token_id, workspace_id, user_id FROM auth_resolve_token($1)", tokenHash).
			Scan(&token, &workspace, &owner); err != nil || token != b.Rows["api_token"] || workspace != b.ID ||
			owner != b.UserID {
			t.Errorf("auth_resolve_token: %s, %s, %s, %v; want B's token, workspace, and user", token, workspace, owner, err)
		}
		var invite ids.UUID
		if err := tx.QueryRow(ctx, "SELECT invite_id, workspace_id FROM auth_resolve_invite($1)", inviteHash).
			Scan(&invite, &workspace); err != nil || invite != b.Rows["invite"] || workspace != b.ID {
			t.Errorf("auth_resolve_invite: %s, %s, %v; want B's invite and workspace", invite, workspace, err)
		}
		if err := tx.QueryRow(ctx, "SELECT route_project_workspace($1)", b.Rows["project"]).Scan(&workspace); err != nil ||
			workspace != b.ID {
			t.Errorf("route_project_workspace: %s, %v; want B", workspace, err)
		}

		unknown := isolationtest.TokenHash()
		for _, q := range []string{"SELECT count(*) FROM auth_resolve_token($1)", "SELECT count(*) FROM auth_resolve_invite($1)"} {
			var n int
			if err := tx.QueryRow(ctx, q, unknown).Scan(&n); err != nil || n != 0 {
				t.Errorf("%s with an unknown hash: %d rows, %v; want none", q, n, err)
			}
		}
		var none *ids.UUID
		if err := tx.QueryRow(ctx, "SELECT route_project_workspace($1)", ids.New()).Scan(&none); err != nil || none != nil {
			t.Errorf("route_project_workspace of an unknown project: %v, %v; want null", none, err)
		}
		// The application login reads none of the rows itself.
		var visible int
		if err := tx.QueryRow(ctx, "SELECT (SELECT count(*) FROM api_token) + (SELECT count(*) FROM invite) + "+
			"(SELECT count(*) FROM project) + (SELECT count(*) FROM membership)").Scan(&visible); err != nil || visible != 0 {
			t.Errorf("without a workspace, the application login reads %d rows, %v; want none", visible, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []membership{{memberships[f.A.ID], f.A.ID, "viewer"}, {memberships[b.ID], b.ID, "viewer"}}
	slices.SortFunc(want, func(x, y membership) int { return strings.Compare(x.ID.String(), y.ID.String()) })
	if got := listMemberships(database.Scope{UserID: user}); !slices.Equal(got, want) {
		t.Errorf("auth_user_memberships for the current user = %v, want %v", got, want)
	}
	for _, scope := range []database.Scope{{UserID: b.UserID}, {}, {WorkspaceID: b.ID, UserID: b.UserID}} {
		if got := listMemberships(scope); len(got) != 0 {
			t.Errorf("auth_user_memberships for another user than the current one (%+v) = %v, want none", scope, got)
		}
	}
}

// TestTenantIsolation runs the isolation harness over every tenant table:
// from a transaction of workspace A, a read, update, delete, or insert that
// is pointed at workspace B's rows, by B's ID or by no filter at all, as a
// repository with a broken filter would be, must neither return, change, nor
// add any of them, and an update must not move A's rows into B.
//
// Verifies: NFR-SEC-01
func TestTenantIsolation(t *testing.T) {
	t.Parallel()
	f := isolationtest.New(t, dbtest.New(t), isolationtest.Tables)
	f.CheckTables(t)
}

// recorder is a testing.TB that records the errors reported to it instead of
// failing the test.
type recorder struct {
	testing.TB
	errors []string
}

func (r *recorder) Helper() {}

func (r *recorder) Errorf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

// TestIsolationHarnessFindsLeaks runs the isolation harness on tables that
// row-level security does not hold, one without it, one whose policy lets
// every row through, one whose policy for updates lets a row move into
// another workspace, and two with a policy for reads that holds, behind which
// a policy for deletes lets every row through on one, and a policy for
// updates lets every row into the transaction's own workspace on the other,
// and on operations that fail or find nothing on their own workspace's rows,
// so that TestTenantIsolation cannot pass because the harness stopped
// working.
//
// Verifies: NFR-SEC-01
func TestIsolationHarnessFindsLeaks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	if _, err := d.Admin(t).Exec(ctx, `
		CREATE TABLE leaky (id uuid PRIMARY KEY, workspace_id uuid NOT NULL);
		CREATE TABLE open_policy (id uuid PRIMARY KEY, workspace_id uuid NOT NULL);
		ALTER TABLE open_policy ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
		CREATE POLICY everything ON open_policy TO storycreep_app USING (true);
		CREATE TABLE loose_check (id uuid PRIMARY KEY, workspace_id uuid NOT NULL);
		ALTER TABLE loose_check ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
		CREATE POLICY workspace_isolation ON loose_check TO storycreep_app
			USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);
		CREATE POLICY moves ON loose_check FOR UPDATE TO storycreep_app
			USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid) WITH CHECK (true);
		CREATE TABLE loose_delete (id uuid PRIMARY KEY, workspace_id uuid NOT NULL);
		ALTER TABLE loose_delete ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
		CREATE POLICY workspace_isolation ON loose_delete TO storycreep_app
			USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);
		CREATE POLICY deletes ON loose_delete FOR DELETE TO storycreep_app USING (true);
		CREATE TABLE loose_update (id uuid PRIMARY KEY, workspace_id uuid NOT NULL);
		ALTER TABLE loose_update ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
		CREATE POLICY workspace_isolation ON loose_update TO storycreep_app
			USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);
		CREATE POLICY updates ON loose_update FOR UPDATE TO storycreep_app
			USING (true) WITH CHECK (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);
		GRANT SELECT, INSERT, UPDATE, DELETE ON leaky, open_policy, loose_check, loose_delete, loose_update
			TO storycreep_app`); err != nil {
		t.Fatal(err)
	}
	insert := func(table string) isolationtest.Table {
		return isolationtest.Table{Name: table,
			Insert: func(_ context.Context, _ isolationtest.Querier, w *isolationtest.Workspace) (isolationtest.Statement, error) {
				id := ids.New()
				return isolationtest.Statement{ID: id, SQL: "INSERT INTO " + table + " (id, workspace_id) VALUES ($1, $2)",
					Args: []any{id, w.ID}}, nil
			}}
	}
	f := isolationtest.New(t, d, []isolationtest.Table{insert("leaky"), insert("open_policy"), insert("loose_check"),
		insert("loose_delete"), insert("loose_update")})
	rec := &recorder{TB: t}
	f.CheckTables(rec)
	f.Check(rec, isolationtest.Op{Name: "failing", Run: func(context.Context, pgx.Tx, *isolationtest.Workspace) ([]ids.UUID, error) {
		return nil, fmt.Errorf("no such thing")
	}})
	f.Check(rec, isolationtest.Op{Name: "empty", Run: func(context.Context, pgx.Tx, *isolationtest.Workspace) ([]ids.UUID, error) {
		return nil, nil
	}})

	var want []string
	for _, table := range []string{"leaky", "open_policy"} {
		want = append(want,
			table+": read by workspace: from another workspace's transaction, reaches 1 of the workspace's rows",
			table+": read without a filter: from another workspace's transaction, reaches 1 of the workspace's rows",
			table+": update without a filter: from another workspace's transaction, reaches 1 of the workspace's rows",
			table+": delete without a filter: from another workspace's transaction, reaches 1 of the workspace's rows",
			table+": update by workspace: from another workspace's transaction, reaches 1 of the workspace's rows",
			table+": delete by workspace: from another workspace's transaction, reaches 1 of the workspace's rows",
			table+": delete by workspace: from another workspace's transaction, changes the workspace's rows",
			table+": move into a workspace: from another workspace's transaction: <nil>, want row-level security "+
				"to refuse it",
			table+": move into a workspace: from another workspace's transaction, changes the workspace's rows",
			table+": insert: from another workspace's transaction: <nil>, want row-level security to refuse it",
			table+": insert: from another workspace's transaction, changes the workspace's rows",
		)
	}
	// loose_check holds every operation but the move, which only the check
	// of new rows refuses. loose_delete and loose_update hold every operation
	// that reads a column, which their policy for reads holds too, and only
	// the delete and the update without a filter find their looser policy.
	want = append(want,
		"loose_check: move into a workspace: from another workspace's transaction: <nil>, want row-level security "+
			"to refuse it",
		"loose_check: move into a workspace: from another workspace's transaction, changes the workspace's rows",
		"loose_delete: delete without a filter: from another workspace's transaction, reaches 1 of the workspace's rows",
		"loose_update: update without a filter: from another workspace's transaction, reaches 1 of the workspace's rows",
		"failing: fails on its own workspace's rows: no such thing",
		"empty: finds none of its own workspace's rows, so it shows nothing about another's",
	)
	if !slices.Equal(rec.errors, want) {
		t.Errorf("the harness reported\n%s\nwant\n%s", strings.Join(rec.errors, "\n"), strings.Join(want, "\n"))
	}
}
