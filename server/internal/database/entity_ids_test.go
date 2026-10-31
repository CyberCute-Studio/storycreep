package database_test

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// entityIDTables are the tables of the projects' content whose rows'
// UUIDs entity_id holds (migration 00029, PRD §5.2), each of the kind that is
// its name: those whose rows a command creates with its client's ID, and
// expression, whose UUIDs the server makes, but which the usage lists'
// cursors show, so that a client could give a node one.
var entityIDTables = []string{"attribute_def", "board", "branch_output", "component", "component_type",
	"connection", "expression", "folder", "node", "variable"}

// notEntityIDTables are the other tables of a project whose rows have an ID
// of their own, and why entity_id does not hold them: the server makes
// their IDs, and the dependency index names none of them, so that a UUID that
// one of them shares with an entity of the content, which a client could
// give that entity only by copying it, confuses nothing.
var notEntityIDTables = map[string]string{
	"api_token":       "a token is no content: its ID is the server's, and a token narrowed to a project names it",
	"asset":           "an upload's ID is the server's, and no site of the index is an asset",
	"attribute_value": "a value's ID is the server's, and is never shown; a value is named by its component and its definition",
	"entity_id":       "it holds the others' UUIDs",
	"operation":       "an operation of the log is the server's, and no site of the index is one",
	"project_grant":   "a grant is no content: its ID is the server's, and it names a member's role on the project",
	"validation_mute": "a mute's ID is the server's, and is never shown; a mute is named by its owner and its code",
}

// TestEntityIDTables checks that entity_id holds the UUIDs of every table of
// a project's content whose rows have an ID of their own, but those that
// notEntityIDTables lists with why: each of entityIDTables has the two
// triggers that add the UUIDs of the rows that a statement inserts and
// delete those of the rows that it deletes, of its kind, which is a kind
// that the table's check takes, and a table of a project that is in neither
// list fails the test, so that a new table of content joins one of them.
func TestEntityIDTables(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := dbtest.New(t).Admin(t)
	rows, err := admin.Query(ctx, `SELECT c.relname::text FROM pg_class c
		WHERE c.relnamespace = 'public'::regnamespace AND c.relkind = 'r'
			AND EXISTS (SELECT FROM pg_attribute a WHERE a.attrelid = c.oid AND a.attname = 'project_id'
				AND NOT a.attisdropped)
			AND EXISTS (SELECT FROM pg_constraint k JOIN pg_attribute a ON a.attrelid = c.oid AND a.attname = 'id'
				WHERE k.conrelid = c.oid AND k.contype = 'p' AND k.conkey = ARRAY[a.attnum])
		ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, table := range tables {
		_, exempt := notEntityIDTables[table]
		switch {
		case slices.Contains(entityIDTables, table) && exempt:
			t.Errorf("%s is in both entityIDTables and notEntityIDTables", table)
		case slices.Contains(entityIDTables, table):
			want = append(want,
				table+": CREATE TRIGGER entity_id_delete AFTER DELETE ON public."+table+
					" REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION entity_id_delete('"+table+"')",
				table+": CREATE TRIGGER entity_id_insert AFTER INSERT ON public."+table+
					" REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION entity_id_insert('"+table+"')")
		case !exempt:
			t.Errorf("%s is a table of a project whose rows have IDs, which entity_id does not hold: add its "+
				"triggers in a migration, and add it to entityIDTables, or add it to notEntityIDTables with why "+
				"not", table)
		}
	}
	for _, table := range append(slices.Clone(entityIDTables), mapKeys(notEntityIDTables)...) {
		if !slices.Contains(tables, table) {
			t.Errorf("%s is listed, but is no table of a project whose rows have IDs", table)
		}
	}
	rows, err = admin.Query(ctx, `SELECT c.relname::text || ': ' || pg_get_triggerdef(t.oid)
		FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid
		WHERE NOT t.tgisinternal AND (t.tgname IN ('entity_id_insert', 'entity_id_delete')
			OR t.tgfoid IN ('entity_id_insert'::regproc, 'entity_id_delete'::regproc))
		ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("the triggers of entity_id are\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	var check string
	if err := admin.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint
		WHERE conrelid = 'entity_id'::regclass AND contype = 'c'`).Scan(&check); err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, m := range regexp.MustCompile(`'([a-z_]+)'::text`).FindAllStringSubmatch(check, -1) {
		kinds = append(kinds, m[1])
	}
	slices.Sort(kinds)
	if !slices.Equal(kinds, entityIDTables) {
		t.Errorf("entity_id's check takes the kinds %q, want %q", kinds, entityIDTables)
	}
}

// mapKeys returns the keys of m, in order.
func mapKeys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// TestEntityIDsBackfill upgrades a database whose content's UUIDs entity_id
// does not hold yet, as the migrations before it left it, with a folder and
// a component type that a client gave one UUID, and checks that the
// migration adds the UUID of each entity, of its kind, and one of the two
// that share one, rather than fail; that the insert of an entity of another
// kind of a UUID that it holds fails then, on entity_id_pkey, whatever the
// workspace; that deleting an entity frees its UUID, but not the one that it
// shares with another kind's; and that deleting the project deletes them all.
func TestEntityIDsBackfill(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.Empty(t)
	if _, err := database.Migrate(ctx, d.AdminURL, d.AppURL,
		database.MigrateOptions{Migrations: migrationsBefore(t, "00029")}); err != nil {
		t.Fatal(err)
	}
	admin := d.Admin(t)
	ws, other, project, otherProject := ids.New(), ids.New(), ids.New(), ids.New()
	folder, board, keeper, cast := ids.New(), ids.New(), ids.New(), ids.New()
	exec := func(sql string, args ...any) error {
		_, err := admin.Exec(ctx, sql, args...)
		return err
	}
	for _, c := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Harbour', 'harbour', 'unlimited'),
			($2, 'Lighthouse', 'lighthouse', 'unlimited')`, []any{ws, other}},
		{`INSERT INTO project (id, workspace_id, name) VALUES ($1, $2, 'Harbour'), ($3, $4, 'Lighthouse')`,
			[]any{project, ws, otherProject, other}},
		{`INSERT INTO folder (id, workspace_id, project_id, kind, name, "order")
			VALUES ($1, $2, $3, 'board', 'Act 1', 'V')`, []any{folder, ws, project}},
		{`INSERT INTO board (id, workspace_id, project_id, folder_id, "order", name, slug)
			VALUES ($1, $2, $3, $4, 'V', 'Harbour', 'harbour')`, []any{board, ws, project, folder}},
		{`INSERT INTO node (id, workspace_id, project_id, board_id, type, kind, text, x, y)
			VALUES ($1, $2, $3, $4, 'element', 'dialogue', '{"type": "doc", "content": [{"type": "paragraph"}]}', 0, 0)`,
			[]any{keeper, ws, project, board}},
		{`INSERT INTO expression (workspace_id, project_id, board_id, node_id, field, context, source)
			VALUES ($1, $2, $3, $4, 'on_enter', 'script', 'seen = 1')`, []any{ws, project, board, keeper}},
		// A component type whose UUID is the folder's, as a client could
		// give it before the migration.
		{`INSERT INTO component_type (id, workspace_id, project_id, key, name) VALUES ($1, $2, $3, 'cast', 'Cast')`,
			[]any{folder, ws, project}},
		{`INSERT INTO component_type (id, workspace_id, project_id, key, name) VALUES ($1, $2, $3, 'prop', 'Prop')`,
			[]any{cast, ws, project}},
	} {
		if err := exec(c.sql, c.args...); err != nil {
			t.Fatalf("%s: %v", c.sql, err)
		}
	}
	if _, err := database.Migrate(ctx, d.AdminURL, d.AppURL, database.MigrateOptions{}); err != nil {
		t.Fatal(err)
	}
	held := func() []string {
		t.Helper()
		rows, err := admin.Query(ctx, `SELECT kind || ' ' || id::text FROM entity_id ORDER BY 1`)
		if err != nil {
			t.Fatal(err)
		}
		got, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	var script ids.UUID
	if err := admin.QueryRow(ctx, `SELECT id FROM expression`).Scan(&script); err != nil {
		t.Fatal(err)
	}
	got := held()
	want := []string{"board " + board.String(), "component_type " + cast.String(), "expression " + script.String(),
		"node " + keeper.String()}
	// One of the folder and the component type that share a UUID holds it.
	shared := slices.IndexFunc(got, func(s string) bool { return strings.HasSuffix(s, " "+folder.String()) })
	if shared < 0 || got[shared] != "folder "+folder.String() && got[shared] != "component_type "+folder.String() {
		t.Errorf("the backfill holds %q, want the shared UUID of the folder or the component type", got)
	} else {
		got = slices.Delete(got, shared, shared+1)
	}
	if !slices.Equal(got, want) {
		t.Errorf("the backfill holds %q, and the shared UUID, want %q", got, want)
	}

	// An entity of another kind may hold none of them, in any workspace.
	for _, c := range []struct {
		what, sql string
		args      []any
	}{
		{"a board of the node's UUID", `INSERT INTO board (id, workspace_id, project_id, "order", name, slug)
			VALUES ($1, $2, $3, 'W', 'Pier', 'pier')`, []any{keeper, ws, project}},
		{"another workspace's variable of the expression's UUID", `INSERT INTO variable (id, workspace_id,
			project_id, scope, "order", key, type, default_value) VALUES ($1, $2, $3, 'global', 'V', 'seen', 'bool',
			'false')`, []any{script, other, otherProject}},
	} {
		if err := exec(c.sql, c.args...); !database.UniqueViolation(err, "entity_id_pkey") {
			t.Errorf("%s: %v, want a violation of entity_id_pkey", c.what, err)
		}
	}
	// Deleting the node frees its UUID, which a board then takes; deleting
	// the shared UUID's other holder leaves it held.
	if err := exec(`DELETE FROM node WHERE id = $1`, keeper); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO board (id, workspace_id, project_id, "order", name, slug)
		VALUES ($1, $2, $3, 'W', 'Pier', 'pier')`, keeper, ws, project); err != nil {
		t.Errorf("a board of a deleted node's UUID: %v", err)
	}
	holder := strings.Fields(held()[slices.IndexFunc(held(), func(s string) bool {
		return strings.HasSuffix(s, " "+folder.String())
	})])[0]
	notHolder := map[string]string{"folder": "component_type", "component_type": "folder"}[holder]
	if err := exec(`DELETE FROM `+pgx.Identifier{notHolder}.Sanitize()+` WHERE id = $1`, folder); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(held(), holder+" "+folder.String()) {
		t.Errorf("deleting the %s that did not hold the shared UUID deleted the %s's", notHolder, holder)
	}
	// Deleting the project deletes them all.
	if err := exec(`DELETE FROM project WHERE id = $1`, project); err != nil {
		t.Fatal(err)
	}
	if got := held(); len(got) != 0 {
		t.Errorf("with its project deleted, entity_id holds %q", got)
	}
}
