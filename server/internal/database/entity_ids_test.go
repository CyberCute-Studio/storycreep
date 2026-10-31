package database_test

import (
	"context"
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

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
// that the table's check takes, and a primary key named <table>_pkey, by
// which database.IDTaken finds the table of a violation; and a table of a
// project, plain or partitioned, with an id that a unique index holds,
// alone or with other columns, whatever the primary key, that is in neither
// list fails the test, so that a new table of content joins one of them.
func TestEntityIDTables(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := dbtest.New(t).Admin(t)
	rows, err := admin.Query(ctx, `SELECT c.relname::text FROM pg_class c
		WHERE c.relnamespace = 'public'::regnamespace AND c.relkind IN ('r', 'p') AND NOT c.relispartition
			AND EXISTS (SELECT FROM pg_attribute a WHERE a.attrelid = c.oid AND a.attname = 'project_id'
				AND NOT a.attisdropped)
			AND EXISTS (SELECT FROM pg_index i JOIN pg_attribute a ON a.attrelid = c.oid AND a.attname = 'id'
					AND NOT a.attisdropped
				WHERE i.indrelid = c.oid AND i.indisunique AND a.attnum = ANY (i.indkey::smallint[]))
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
	for _, table := range append(slices.Clone(entityIDTables), slices.Sorted(maps.Keys(notEntityIDTables))...) {
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
	// database.IDTaken finds the table of a violation by the name of its
	// primary key, which PostgreSQL gives as <table>_pkey, but which a table
	// that a migration renames keeps from its old name.
	keyed := append([]string{"entity_id"}, entityIDTables...)
	rows, err = admin.Query(ctx, `SELECT c.relname::text || ': ' || k.conname::text
		FROM pg_constraint k JOIN pg_class c ON c.oid = k.conrelid
		WHERE c.relnamespace = 'public'::regnamespace AND c.relname = ANY ($1::text[]) AND k.contype = 'p'
		ORDER BY 1`, keyed)
	if err != nil {
		t.Fatal(err)
	}
	if got, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		t.Fatal(err)
	}
	want = nil
	for _, table := range keyed {
		want = append(want, table+": "+table+"_pkey")
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("the primary keys of entity_id and the content's tables are %q, want %q", got, want)
	}
}

// TestEntityIDsBackfill upgrades a database whose content's UUIDs entity_id
// does not hold yet, as the migrations before it left it, with a folder and
// a component type that a client gave one UUID, and checks that the
// migration adds the UUID of each entity, of its kind, and the folder's of
// the two that share one, whose kind it adds first, rather than fail; that
// the insert of an entity of another kind of a UUID that it holds fails
// then, on entity_id_pkey, whatever the workspace, as one of its own kind
// fails on its table's primary key, both of which database.IDTaken reports,
// though not a violation of another key; that deleting an entity frees its
// UUID, but deleting the component type does not free the one that it
// shares with the folder; and that deleting the project deletes them all.
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
	// The folder holds the UUID that it shares with a component type.
	if got, want := held(), []string{"board " + board.String(), "component_type " + cast.String(),
		"expression " + script.String(), "folder " + folder.String(), "node " + keeper.String()}; !slices.Equal(got,
		want) {
		t.Errorf("the backfill holds %q, want %q", got, want)
	}

	// An entity of another kind may hold none of them, in any workspace, nor
	// one of the same kind, whose table's own key refuses it first, either of
	// which database.IDTaken reports, from the error alone.
	for _, c := range []struct {
		what, sql, key string
		args           []any
	}{
		{"a board of the node's UUID", `INSERT INTO board (id, workspace_id, project_id, "order", name, slug)
			VALUES ($1, $2, $3, 'W', 'Pier', 'pier')`, "entity_id_pkey", []any{keeper, ws, project}},
		{"another workspace's variable of the expression's UUID", `INSERT INTO variable (id, workspace_id,
			project_id, scope, "order", key, type, default_value) VALUES ($1, $2, $3, 'global', 'V', 'seen', 'bool',
			'false')`, "entity_id_pkey", []any{script, other, otherProject}},
		{"another workspace's board of the board's UUID", `INSERT INTO board (id, workspace_id, project_id, "order",
			name, slug) VALUES ($1, $2, $3, 'V', 'Lamp', 'lamp')`, "board_pkey", []any{board, other, otherProject}},
	} {
		if err := exec(c.sql, c.args...); !database.UniqueViolation(err, c.key) || !database.IDTaken(err) {
			t.Errorf("%s: %v, want a violation of %s, which database.IDTaken reports", c.what, err, c.key)
		}
	}
	// A key of the table that is not its primary key is no ID.
	if err := exec(`INSERT INTO board (id, workspace_id, project_id, "order", name, slug)
		VALUES ($1, $2, $3, 'W', 'Quay', 'harbour')`, ids.New(), ws, project); err == nil || database.IDTaken(err) {
		t.Errorf("a board of another board's slug: %v, want a violation that database.IDTaken does not report", err)
	}
	// Deleting the node frees its UUID, which a board then takes; deleting
	// the component type that shares the folder's UUID leaves it held.
	if err := exec(`DELETE FROM node WHERE id = $1`, keeper); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO board (id, workspace_id, project_id, "order", name, slug)
		VALUES ($1, $2, $3, 'W', 'Pier', 'pier')`, keeper, ws, project); err != nil {
		t.Errorf("a board of a deleted node's UUID: %v", err)
	}
	if err := exec(`DELETE FROM component_type WHERE id = $1`, folder); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(held(), "folder "+folder.String()) {
		t.Error("deleting the component type that shares the folder's UUID deleted the folder's")
	}
	// Deleting the project deletes them all.
	if err := exec(`DELETE FROM project WHERE id = $1`, project); err != nil {
		t.Fatal(err)
	}
	if got := held(); len(got) != 0 {
		t.Errorf("with its project deleted, entity_id holds %q", got)
	}
}

// TestEntityIDsMigrationWaits migrates a database while a batch of commands
// of another instance of the server applies, as one may during an upgrade
// (PRD FR-ADMIN-04): the batch has locked its project's row, as every batch
// does first (LockProjectSeq), and created a node when the migration starts,
// and then creates a folder, whose table the migration locks before the
// node's, and sets the project's seq. The migration waits for the batch,
// rather than lock what the batch writes next, which would deadlock the two
// and fail one of them, and then adds the UUIDs that the batch added.
func TestEntityIDsMigrationWaits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.Empty(t)
	if _, err := database.Migrate(ctx, d.AdminURL, d.AppURL,
		database.MigrateOptions{Migrations: migrationsBefore(t, "00029")}); err != nil {
		t.Fatal(err)
	}
	admin := d.Admin(t)
	ws, project, board, node, folder := ids.New(), ids.New(), ids.New(), ids.New(), ids.New()
	for _, c := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Harbour', 'harbour', 'unlimited')`,
			[]any{ws}},
		{`INSERT INTO project (id, workspace_id, name) VALUES ($1, $2, 'Harbour')`, []any{project, ws}},
		{`INSERT INTO board (id, workspace_id, project_id, "order", name, slug)
			VALUES ($1, $2, $3, 'V', 'Harbour', 'harbour')`, []any{board, ws, project}},
	} {
		if _, err := admin.Exec(ctx, c.sql, c.args...); err != nil {
			t.Fatalf("%s: %v", c.sql, err)
		}
	}
	batch, err := d.Admin(t).Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// A failed step ends the batch, so that the migration goes on.
	defer func() { _ = batch.Rollback(ctx) }()
	step := func(sql string, args ...any) {
		t.Helper()
		if _, err := batch.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("the batch: %s: %v", sql, err)
		}
	}
	step(`SELECT seq FROM project WHERE workspace_id = $1 AND id = $2 FOR NO KEY UPDATE`, ws, project)
	step(`INSERT INTO node (id, workspace_id, project_id, board_id, type, kind, text, x, y)
		VALUES ($1, $2, $3, $4, 'element', 'dialogue', '{"type": "doc", "content": [{"type": "paragraph"}]}', 0, 0)`,
		node, ws, project, board)
	migrated := make(chan error, 1)
	go func() {
		_, err := database.Migrate(ctx, d.AdminURL, d.AppURL, database.MigrateOptions{})
		migrated <- err
	}()
	// The migration waits for a lock of a table that the batch holds.
	deadline := time.Now().Add(time.Minute)
	for waiting := false; !waiting; {
		select {
		case err := <-migrated:
			t.Fatalf("the migration ended without waiting for the batch: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("the migration did not wait for the batch")
		}
		if err := admin.QueryRow(ctx, `SELECT EXISTS (SELECT FROM pg_locks WHERE NOT granted
			AND locktype = 'relation' AND database = (SELECT oid FROM pg_database WHERE datname = current_database()))`).
			Scan(&waiting); err != nil {
			t.Fatal(err)
		}
	}
	step(`INSERT INTO folder (id, workspace_id, project_id, kind, name, "order")
		VALUES ($1, $2, $3, 'board', 'Act 1', 'V')`, folder, ws, project)
	step(`UPDATE project SET seq = 1 WHERE workspace_id = $1 AND id = $2`, ws, project)
	if err := batch.Commit(ctx); err != nil {
		t.Fatalf("the batch: %v", err)
	}
	if err := <-migrated; err != nil {
		t.Fatalf("the migration: %v", err)
	}
	rows, err := admin.Query(ctx, `SELECT kind || ' ' || id::text FROM entity_id ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"board " + board.String(), "folder " + folder.String(), "node " + node.String()}; !slices.Equal(
		got, want) {
		t.Errorf("entity_id holds %q, want %q", got, want)
	}
}

// TestEntityIDsDeleteLooksUp checks that the trigger that deletes the UUIDs
// of the rows that a statement deletes looks each up by entity_id's primary
// key, in a session of the application that deleted thousands of rows of
// the table at once before, as the purge of a project or of a board does:
// the session keeps the plan of the trigger's statement, made for the size
// of the first statement's rows, so that a join of those rows would read the
// whole of entity_id for each later statement, however few rows it deleted
// (migration 00029).
func TestEntityIDsDeleteLooksUp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	admin := d.Admin(t)
	ws, project, last := ids.New(), ids.New(), ids.New()
	for _, c := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Harbour', 'harbour', 'unlimited')`,
			[]any{ws}},
		{`INSERT INTO project (id, workspace_id, name) VALUES ($1, $2, 'Harbour')`, []any{project, ws}},
		// The UUIDs of the rest of the content, as of a few large projects.
		{`INSERT INTO entity_id (id, workspace_id, project_id, kind)
			SELECT gen_random_uuid(), $1, $2, 'node' FROM generate_series(1, 30000)`, []any{ws, project}},
		{`INSERT INTO folder (id, workspace_id, project_id, kind, name, "order")
			SELECT gen_random_uuid(), $1, $2, 'board', 'Act', 'V' FROM generate_series(1, 10000)`,
			[]any{ws, project}},
		{`INSERT INTO folder (id, workspace_id, project_id, kind, name, "order")
			VALUES ($1, $2, $3, 'board', 'Act', 'V')`, []any{last, ws, project}},
		// No analysis between the two deletions makes the session plan the
		// trigger's statement anew; the folders' lets the cascades of their
		// deletion, which find each folder's children, read them by their
		// index.
		{`ALTER TABLE entity_id SET (autovacuum_enabled = false)`, nil},
		{`ANALYZE entity_id, folder`, nil},
	} {
		if _, err := admin.Exec(ctx, c.sql, c.args...); err != nil {
			t.Fatalf("%s: %v", c.sql, err)
		}
	}
	session := dbtest.Connect(t, d.AppURL)
	if _, err := session.Exec(ctx, `SELECT set_config('app.workspace_id', $1, false)`, ws.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Exec(ctx, `DELETE FROM folder WHERE workspace_id = $1 AND project_id = $2 AND id <> $3`, ws,
		project, last); err != nil {
		t.Fatal(err)
	}
	tx, err := session.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// What the session has read of entity_id, by scanning it, which reads
	// every row, and by its indexes, as a join of the deleted rows by the
	// workspace's index reads the workspace's, which counts what the
	// statements before have read too, unless the session has reported them
	// since, but not within a transaction.
	reads := func() (scans, read, fetched int64) {
		t.Helper()
		if err := tx.QueryRow(ctx, `SELECT seq_scan, seq_tup_read, idx_tup_fetch FROM pg_stat_xact_user_tables
			WHERE relid = 'public.entity_id'::regclass`).Scan(&scans, &read, &fetched); err != nil {
			t.Fatal(err)
		}
		return scans, read, fetched
	}
	scans, read, fetched := reads()
	if _, err := tx.Exec(ctx, `DELETE FROM folder WHERE workspace_id = $1 AND project_id = $2 AND id = $3`, ws,
		project, last); err != nil {
		t.Fatal(err)
	}
	scansAfter, readAfter, fetchedAfter := reads()
	if scans, read, fetched = scansAfter-scans, readAfter-read, fetchedAfter-fetched; scans != 0 || read != 0 ||
		fetched > 1 {
		t.Errorf("deleting a folder after 10,000 scanned entity_id %d times, reading %d rows, and fetched %d rows "+
			"by its indexes, want its UUID's row alone, by the primary key", scans, read, fetched)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM entity_id WHERE kind = 'folder'`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Errorf("with the folders deleted, entity_id holds %d of their UUIDs", left)
	}
}
