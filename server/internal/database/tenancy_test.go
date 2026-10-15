package database_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// crossWorkspaceTables are the tables whose rows span workspaces, which have
// no workspace_id and no row-level security (PRD §5.4), besides River's,
// whose names start with river_ and which belong to the job queue. Every other
// table is a tenant table.
var crossWorkspaceTables = []string{"goose_db_version", "session", "user", "workspace"}

// auditTable is the one tenant table whose workspace_id has no foreign key,
// so that the audit log outlives a workspace that is hard deleted (FR-TEN-01
// AC1).
const auditTable = "audit_entry"

// tenantTableProblems enumerates the tables of the public schema and returns
// what is wrong with them: a table that is neither a tenant table nor one of
// crossWorkspaceTables, a tenant table whose workspace_id is missing, may be
// null, is not a uuid, lacks the foreign key to workspace (or, for
// audit_entry, has one), or leads no index, a tenant table without forced
// row-level security or without a policy for storycreep_app, a table that
// spans workspaces but has a workspace_id, and a tenant table that is not in
// registered, the isolation harness's tables, or a name in registered that is
// not a tenant table.
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
				WHERE p.polrelid = c.oid AND p.polcmd = '*' AND 'storycreep_app'::regrole = ANY (p.polroles))
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
	}
	tables, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (table, error) {
		var x table
		err := r.Scan(&x.name, &x.hasColumn, &x.notNull, &x.uuid, &x.fk, &x.index, &x.rowSecurity, &x.forced,
			&x.workspacePolicy)
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
		if !tb.notNull {
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
// workspace_id with a foreign key to workspace (audit_entry's has none),
// forced row-level security with a policy for the application, and a place
// among the isolation harness's tables, and that every other table is one of
// those that span workspaces.
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
		"loose: is not one of the isolation harness's tables",
		"river_extra: spans workspaces, but has a workspace_id",
		"stray: has no workspace_id; a table whose rows span workspaces is one of the cross-workspace tables",
		"unforced: row-level security is not enabled and forced",
		"unforced: has no policy for storycreep_app",
		"ghost: is one of the isolation harness's tables, but not a tenant table",
	}
	if !slices.Equal(got, want) {
		t.Errorf("problems =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
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
// every row through, and one whose policy for updates lets a row move into
// another workspace, and on operations that fail or find nothing on their own
// workspace's rows, so that TestTenantIsolation cannot pass because the
// harness stopped working.
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
		GRANT SELECT, INSERT, UPDATE, DELETE ON leaky, open_policy, loose_check TO storycreep_app`); err != nil {
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
	f := isolationtest.New(t, d, []isolationtest.Table{insert("leaky"), insert("open_policy"), insert("loose_check")})
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
	// of new rows refuses.
	want = append(want,
		"loose_check: move into a workspace: from another workspace's transaction: <nil>, want row-level security "+
			"to refuse it",
		"loose_check: move into a workspace: from another workspace's transaction, changes the workspace's rows",
		"failing: fails on its own workspace's rows: no such thing",
		"empty: finds none of its own workspace's rows, so it shows nothing about another's",
	)
	if !slices.Equal(rec.errors, want) {
		t.Errorf("the harness reported\n%s\nwant\n%s", strings.Join(rec.errors, "\n"), strings.Join(want, "\n"))
	}
}
