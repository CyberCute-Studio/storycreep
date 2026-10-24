package trash_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/trash"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
)

func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

// fixture is a database of the test's own with a workspace whose owner is
// owner, a project of it, the pipeline of the projects' and the boards'
// commands, and admin, a connection of the superuser to the database.
type fixture struct {
	d        *dbtest.Database
	db       *database.DB
	admin    *pgx.Conn
	ws       ids.UUID
	owner    ids.UUID
	project  ids.UUID
	pipeline *commands.Pipeline
	projects *project.Service
	boards   *board.Service
	trash    *trash.Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	d := dbtest.New(t)
	f := &fixture{d: d, db: d.Open(t), admin: d.Admin(t), ws: ids.New()}
	admin := f.admin
	var err error
	if f.owner, err = isolationtest.NewUser(ctx, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Harbour', $2,
		'unlimited')`, f.ws, "harbour-"+f.ws.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO membership (id, workspace_id, user_id, role) VALUES ($1, $2, $3,
		'owner')`, ids.New(), f.ws, f.owner); err != nil {
		t.Fatal(err)
	}
	reg := commands.NewRegistry()
	project.Register(reg)
	board.Register(reg)
	ent := entitlements.New(entitlements.Options{Meter: usage.Meter{}})
	f.pipeline = commands.New(f.db, commands.Options{Registry: reg, Entitlements: ent})
	f.projects = project.New(f.db, project.Options{Entitlements: ent, Pipeline: f.pipeline})
	f.boards = board.New(f.db, board.Options{Projects: f.projects})
	f.trash = trash.New(f.db, trash.Options{Projects: f.projects})
	f.project = f.newProject(t, "Harbour")
	return f
}

// newProject creates a project of the fixture's workspace named name, and
// returns its UUID.
func (f *fixture) newProject(t *testing.T, name string) ids.UUID {
	t.Helper()
	p, err := f.projects.Create(context.Background(), f.owner, f.ws, project.Settings{Name: name}, audit.Client{})
	if err != nil {
		t.Fatal(err)
	}
	id, err := ids.Decode(ids.Project, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// actor is the owner, as the actor of a batch.
func (f *fixture) actor() commands.Actor {
	return commands.Actor{UserID: f.owner}
}

// cmd is a command of a batch: its type and its payload.
type cmd struct {
	typ     commands.Type
	payload any
}

// must applies the commands to the fixture's project as one batch, as the
// owner, and fails the test unless they apply.
func (f *fixture) must(t *testing.T, cmds ...cmd) commands.Result {
	t.Helper()
	batch := make([]commands.Command, len(cmds))
	for i, c := range cmds {
		raw, err := json.Marshal(c.payload)
		if err != nil {
			t.Fatal(err)
		}
		batch[i] = commands.Command{ID: ids.New(), Type: c.typ, Payload: raw}
	}
	res, err := f.pipeline.Apply(context.Background(), f.actor(), f.project, batch)
	if err != nil {
		t.Fatalf("%v: %v", cmds, err)
	}
	return res
}

// newBoard returns the command that creates the board id named name, in the
// folder folder, or at the top if folder is zero.
func newBoard(id ids.UUID, name string, folder ids.UUID) cmd {
	p := map[string]any{"id": ids.Encode(ids.Board, id), "name": name}
	if folder != (ids.UUID{}) {
		p["folder_id"] = ids.Encode(ids.Folder, folder)
	}
	return cmd{board.Create, p}
}

// newFolder returns the command that creates the folder id of the kind kind,
// in the folder parent, or at the top if parent is zero.
func newFolder(id ids.UUID, kind string, parent ids.UUID) cmd {
	p := map[string]any{"id": ids.Encode(ids.Folder, id), "kind": kind, "name": "Act"}
	if parent != (ids.UUID{}) {
		p["parent_id"] = ids.Encode(ids.Folder, parent)
	}
	return cmd{board.CreateFolder, p}
}

// deleteBoard and deleteFolder return the commands that delete the board or
// the folder id to the trash.
func deleteBoard(id ids.UUID) cmd {
	return cmd{board.Delete, map[string]any{"id": ids.Encode(ids.Board, id)}}
}

func deleteFolder(id ids.UUID) cmd {
	return cmd{board.DeleteFolder, map[string]any{"id": ids.Encode(ids.Folder, id)}}
}

// deletedAgo sets when the row id of the table, a board or a folder, was
// deleted to ago before now, as the superuser.
func (f *fixture) deletedAgo(t *testing.T, table string, id ids.UUID, ago time.Duration) {
	t.Helper()
	tag, err := f.admin.Exec(context.Background(), "UPDATE "+pgx.Identifier{table}.Sanitize()+
		" SET deleted_at = now() - $2::interval WHERE id = $1", id, database.Interval(ago))
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("set when the %s %s was deleted: %v", table, id, err)
	}
}

// exists reports whether the table, a board or a folder, has the row id, in
// the trash or not, as the superuser reads it.
func (f *fixture) exists(t *testing.T, table string, id ids.UUID) bool {
	t.Helper()
	var n int
	if err := f.admin.QueryRow(context.Background(), "SELECT count(*) FROM "+pgx.Identifier{table}.Sanitize()+
		" WHERE id = $1", id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

// list returns every item of the trash of the project, as the owner, page by
// page of limit items, following each page's cursor as the API does.
func (f *fixture) list(t *testing.T, projectID ids.UUID, limit int) []trash.Item {
	t.Helper()
	var all []trash.Item
	var after trash.Cursor
	for {
		// A page reads one more item than it holds, which tells whether
		// another follows, as api.PageParams.Fetch does.
		page, err := f.trash.List(context.Background(), f.actor(), projectID, after, limit+1)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) <= limit {
			return append(all, page...)
		}
		all = append(all, page[:limit]...)
		after = page[limit-1].After()
	}
}

// itemIDs returns the IDs of items.
func itemIDs(items []trash.Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}

// TestList checks the list of a project's trash: the boards in it, not the
// deleted folders, nor what is not in it, the most recently deleted first,
// those deleted at once by their UUIDs, each with its name, its deletion, and
// when its 30 days end, page by page alike, and that restoring a board takes
// it out of the list and brings it back where it was, with its folder.
func TestList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	act := ids.New()
	a, b, c, d, e, live := ids.New(), ids.New(), ids.New(), ids.New(), ids.New(), ids.New()
	f.must(t, newFolder(act, board.KindBoard, ids.UUID{}), newBoard(a, "Harbour", act), newBoard(b, "Pier", act),
		newBoard(c, "Lighthouse", ids.UUID{}), newBoard(d, "Cliffs", ids.UUID{}), newBoard(e, "Cove", ids.UUID{}),
		newBoard(live, "Village", ids.UUID{}))
	f.must(t, deleteBoard(a))
	f.must(t, deleteBoard(b), deleteFolder(act))
	f.must(t, deleteBoard(c))
	// One batch deletes d and e at once, at the latest time.
	f.must(t, deleteBoard(d), deleteBoard(e))
	f.deletedAgo(t, "board", a, 3*time.Hour)
	f.deletedAgo(t, "board", b, 2*time.Hour)
	f.deletedAgo(t, "board", c, time.Hour)
	first, second := d, e
	if bytes.Compare(first[:], second[:]) > 0 {
		first, second = second, first
	}
	want := []string{ids.Encode(ids.Board, first), ids.Encode(ids.Board, second), ids.Encode(ids.Board, c),
		ids.Encode(ids.Board, b), ids.Encode(ids.Board, a)}
	for _, limit := range []int{10, 2, 1} {
		items := f.list(t, f.project, limit)
		if got := itemIDs(items); !slices.Equal(got, want) {
			t.Errorf("the trash, %d at a time, lists %v, want %v", limit, got, want)
		}
		for i, it := range items {
			if it.Kind != "board" || it.PurgeAfter != it.DeletedAt.Add(trash.Retention) ||
				i > 0 && it.DeletedAt.After(items[i-1].DeletedAt) {
				t.Errorf("the trash lists %+v", it)
			}
		}
		if len(items) == len(want) && (items[4].Name != "Harbour" || items[2].Name != "Lighthouse" ||
			!items[0].DeletedAt.Equal(items[1].DeletedAt) ||
			time.Since(items[4].DeletedAt).Round(time.Hour) != 3*time.Hour) {
			t.Errorf("the trash lists %+v", items)
		}
	}

	// Restoring b brings it back where it was, in its folder, which comes
	// back too, and takes it out of the trash.
	f.must(t, cmd{board.Restore, map[string]any{"id": ids.Encode(ids.Board, b)}})
	if got := itemIDs(f.list(t, f.project, 10)); !slices.Equal(got, slices.Delete(slices.Clone(want), 3, 4)) {
		t.Errorf("after b's restore, the trash lists %v", got)
	}
	restored, err := f.boards.Get(ctx, f.actor(), f.project, b)
	if err != nil || restored.FolderID != ids.Encode(ids.Folder, act) {
		t.Errorf("the restored board is %+v, %v, want it in its folder", restored, err)
	}
	folders, err := f.boards.Folders(ctx, f.actor(), f.project, board.KindBoard, ids.UUID{}, 10)
	if err != nil || len(folders) != 1 || folders[0].ID != ids.Encode(ids.Folder, act) {
		t.Errorf("after b's restore, the folders are %+v, %v", folders, err)
	}

	// Another project's trash is its own.
	other := f.newProject(t, "Lighthouse")
	if got := f.list(t, other, 10); len(got) != 0 {
		t.Errorf("another project's trash lists %v", itemIDs(got))
	}
	// A project in the workspace's trash is not found, its own trash with it.
	f.must(t, cmd{project.Delete, map[string]any{}})
	if _, err := f.trash.List(ctx, f.actor(), f.project, trash.Cursor{}, 10); !errors.Is(err, project.ErrNotFound) {
		t.Errorf("the trash of a project in the workspace's trash: %v, want project.ErrNotFound", err)
	}
}

// TestPurge checks the purge of a project's trash: it deletes for good the
// boards that have been there for 30 days, and then the deleted folders whose
// 30 days have passed once they hold nothing, those inside others first, and
// leaves everything else as it is: the boards and the folders whose time has
// not passed, a folder that holds one of them, and what is not in the trash.
// It records no usage and no operation, and a second run finds nothing to
// purge.
func TestPurge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	outer, inner, spare, held := ids.New(), ids.New(), ids.New(), ids.New()
	old, fresh, edge, live := ids.New(), ids.New(), ids.New(), ids.New()
	f.must(t, newFolder(outer, board.KindBoard, ids.UUID{}), newFolder(inner, board.KindBoard, outer),
		newBoard(old, "Harbour", inner), newFolder(spare, board.KindVariable, ids.UUID{}),
		newFolder(held, board.KindBoard, ids.UUID{}), newBoard(fresh, "Pier", held),
		newBoard(edge, "Lighthouse", ids.UUID{}), newBoard(live, "Village", ids.UUID{}))
	f.must(t, deleteBoard(old), deleteFolder(inner), deleteFolder(outer), deleteFolder(spare), deleteBoard(fresh),
		deleteFolder(held), deleteBoard(edge))
	expired := trash.Retention + time.Minute
	for table, rows := range map[string][]ids.UUID{"board": {old}, "folder": {outer, inner, spare, held}} {
		for _, id := range rows {
			f.deletedAgo(t, table, id, expired)
		}
	}
	// fresh's time has not passed, so held, which holds it, stays, although
	// its own time has, as a folder deleted before the board in it could not
	// be.
	f.deletedAgo(t, "board", fresh, trash.Retention-time.Hour)
	f.deletedAgo(t, "board", edge, trash.Retention-time.Minute)
	admin := f.admin
	var deltas, seq int64
	if err := admin.QueryRow(ctx, `SELECT (SELECT count(*) FROM usage_delta), (SELECT seq FROM project WHERE id = $1)`,
		f.project).Scan(&deltas, &seq); err != nil {
		t.Fatal(err)
	}

	n, err := trash.Purge(ctx, f.db)
	if err != nil || n != 4 {
		t.Errorf("the purge deleted %d rows, %v; want 4", n, err)
	}
	for _, c := range []struct {
		table string
		id    ids.UUID
		name  string
		kept  bool
	}{
		{"board", old, "the expired board", false},
		{"folder", inner, "the expired folder that held it", false},
		{"folder", outer, "the expired folder that held that one", false},
		{"folder", spare, "the expired empty folder of variables", false},
		{"board", fresh, "the board whose time has not passed", true},
		{"folder", held, "the expired folder that holds it", true},
		{"board", edge, "the board whose time passes in a minute", true},
		{"board", live, "the board that is not in the trash", true},
	} {
		if got := f.exists(t, c.table, c.id); got != c.kept {
			t.Errorf("after the purge, %s exists: %t, want %t", c.name, got, c.kept)
		}
	}
	var deltasAfter, seqAfter int64
	if err := admin.QueryRow(ctx, `SELECT (SELECT count(*) FROM usage_delta), (SELECT seq FROM project WHERE id = $1)`,
		f.project).Scan(&deltasAfter, &seqAfter); err != nil {
		t.Fatal(err)
	}
	if deltasAfter != deltas || seqAfter != seq {
		t.Errorf("the purge recorded %d usage deltas and %d operations", deltasAfter-deltas, seqAfter-seq)
	}
	if n, err := trash.Purge(ctx, f.db); err != nil || n != 0 {
		t.Errorf("a second purge deleted %d rows, %v; want none", n, err)
	}
}

// TestPurgeMany checks that the purge of a project whose trash holds more
// rows than one of its transactions deletes goes on in another transaction
// until it has purged them all, the folder that the last board was in too.
func TestPurgeMany(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	folder := ids.New()
	f.must(t, newFolder(folder, board.KindBoard, ids.UUID{}), deleteFolder(folder))
	f.deletedAgo(t, "folder", folder, trash.Retention+time.Minute)
	const boards = 1001
	if _, err := f.admin.Exec(ctx, `INSERT INTO board (id, workspace_id, project_id, folder_id, "order", name, slug,
		deleted_at) SELECT gen_random_uuid(), $1, $2, CASE WHEN i = $3 THEN $4::uuid END, 'V', 'Harbour',
		'harbour-' || i, now() - interval '31 days' FROM generate_series(1, $3) i`, f.ws, f.project, boards,
		folder); err != nil {
		t.Fatal(err)
	}
	if n, err := trash.Purge(ctx, f.db); err != nil || n != boards+1 {
		t.Errorf("the purge deleted %d rows, %v; want %d", n, err, boards+1)
	}
	var left int
	if err := f.admin.QueryRow(ctx, "SELECT (SELECT count(*) FROM board) + (SELECT count(*) FROM folder)").Scan(
		&left); err != nil || left != 0 {
		t.Errorf("after the purge, %d boards and folders are left, %v", left, err)
	}
}

// TestPurgeSkipsALockedProject checks that the purge takes a project's lock
// as a batch of its commands does, and skips a project whose lock a
// transaction holds, as a batch does while it applies, rather than wait for
// it, which a later run purges once the lock is free.
func TestPurgeSkipsALockedProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	old := ids.New()
	f.must(t, newBoard(old, "Harbour", ids.UUID{}), deleteBoard(old))
	f.deletedAgo(t, "board", old, trash.Retention+time.Minute)
	// The transaction that holds the lock has a connection of its own.
	tx, err := f.d.Admin(t).Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // Rolled back below.
	if _, err := tx.Exec(ctx, "SELECT 1 FROM project WHERE id = $1 FOR NO KEY UPDATE", f.project); err != nil {
		t.Fatal(err)
	}
	if n, err := trash.Purge(ctx, f.db); err != nil || n != 0 {
		t.Errorf("the purge of a locked project deleted %d rows, %v; want none", n, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := trash.Purge(ctx, f.db); err != nil || n != 1 {
		t.Errorf("the purge once the lock is free deleted %d rows, %v; want 1", n, err)
	}
}

// TestPurgeGoesOnPastAFailure checks that the purge goes on to the
// workspace's next project past one whose purge fails, and returns its
// error, so that a project whose purge keeps failing keeps the others' trash
// from none of the runs.
func TestPurgeGoesOnPastAFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	// The purge reaches the projects in the order of their IDs, so the one
	// whose purge fails is the first.
	failing, purged := f.project, f.newProject(t, "Lighthouse")
	if bytes.Compare(failing[:], purged[:]) > 0 {
		failing, purged = purged, failing
	}
	boards := map[ids.UUID]ids.UUID{failing: ids.New(), purged: ids.New()}
	for p, b := range boards {
		if _, err := f.admin.Exec(ctx, `INSERT INTO board (id, workspace_id, project_id, "order", name, slug,
			deleted_at) VALUES ($1, $2, $3, 'V', 'Harbour', 'harbour', now() - interval '31 days')`, b, f.ws,
			p); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.admin.Exec(ctx, `CREATE FUNCTION fail_purge() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'the purge fails'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.admin.Exec(ctx, "CREATE TRIGGER fail_purge BEFORE DELETE ON board FOR EACH ROW WHEN "+
		"(OLD.project_id = '"+failing.String()+"') EXECUTE FUNCTION fail_purge()"); err != nil {
		t.Fatal(err)
	}
	n, err := trash.Purge(ctx, f.db)
	if err == nil || !strings.Contains(err.Error(), "the purge fails") || n != 1 {
		t.Errorf("the purge of a workspace whose first project's purge fails deleted %d rows, %v; want 1, and "+
			"the error", n, err)
	}
	if !f.exists(t, "board", boards[failing]) || f.exists(t, "board", boards[purged]) {
		t.Errorf("after the purge, the board of the project whose purge fails exists: %t, the other's: %t; want "+
			"only the first", f.exists(t, "board", boards[failing]), f.exists(t, "board", boards[purged]))
	}
}

// TestRestorePurged checks what a restore of a board that the purge has
// deleted, as the undo of its deletion, is answered with: a batch that
// expects the board's revision with a revision conflict that says that the
// board no longer exists, and one that does not with a refusal of its ID.
func TestRestorePurged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	id := ids.New()
	f.must(t, newBoard(id, "Harbour", ids.UUID{}))
	res := f.must(t, deleteBoard(id))
	if len(res.Revisions) != 1 {
		t.Fatalf("the deletion changed %v", res.Revisions)
	}
	f.deletedAgo(t, "board", id, trash.Retention+time.Minute)
	if _, err := trash.Purge(ctx, f.db); err != nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(`{"id":"` + ids.Encode(ids.Board, id) + `"}`)
	ref := commands.Ref{Kind: ids.Board, ID: id}
	_, err := f.pipeline.Apply(ctx, f.actor(), f.project, []commands.Command{{ID: ids.New(), Type: board.Restore,
		Payload: payload, ExpectedRev: map[commands.Ref]int64{ref: res.Revisions[0].Rev}}})
	var conflict *commands.RevisionConflictError
	if !errors.As(err, &conflict) || len(conflict.Conflicts) != 1 || conflict.Conflicts[0].Exists ||
		conflict.Conflicts[0].Ref != ref {
		t.Errorf("the restore of a purged board that expects its revision: %v, want a conflict of a board that no "+
			"longer exists", err)
	}
	_, err = f.pipeline.Apply(ctx, f.actor(), f.project, []commands.Command{{ID: ids.New(), Type: board.Restore,
		Payload: payload}})
	var refused *commands.RefusedError
	if !errors.As(err, &refused) || refused.Field != "id" {
		t.Errorf("the restore of a purged board: %v, want a refusal of its ID", err)
	}
}

// trashTables are the tables of a project's content whose rows go to its
// trash, with the kind of entity that the trash lists each row as, or "" for
// a table whose rows it does not list. A table of a project's content with a
// deleted_at must be one of them (TestTrashTables). A node of the
// workspace's board is in the trash with it, so it is deleted before it, and
// a connection of the node is in the trash with the node, so it is deleted
// before the node.
var trashTables = []struct{ name, kind string }{{"folder", ""}, {"connection", ""}, {"node", "node"},
	{"board", "board"}, {"variable", "variable"}, {"attribute_def", ""}, {"component_type", ""},
	{"component", "component"}}

// TestTrashTables checks that every table of a project's content with a
// deleted_at is registered with the trash, and holds each to what that
// means: a live view of the table's columns, which reads the table as its
// reader, so that row-level security holds it, and which leaves out the rows
// in the trash, so that what reads the content through the views, as the
// search, the exports, and the snapshots do, finds none of them (FR-PROJ-06
// AC2); a branch of the trash's list, if the table's rows are listed, which
// lists a row in the trash; and the purge, which deletes a row whose time in
// the trash has passed, and none of another workspace's.
//
// Verifies: FR-PROJ-06/AC2
func TestTrashTables(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := isolationtest.New(t, dbtest.New(t), isolationtest.Tables)
	admin := f.Admin()
	rows, err := admin.Query(ctx, `SELECT c.table_name::text FROM information_schema.columns c
		JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name
		WHERE c.table_schema = 'public' AND t.table_type = 'BASE TABLE' AND c.column_name = 'deleted_at'
			AND EXISTS (SELECT 1 FROM information_schema.columns p WHERE p.table_schema = c.table_schema
				AND p.table_name = c.table_name AND p.column_name = 'project_id')
		ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	var registered []string
	for _, tt := range trashTables {
		registered = append(registered, tt.name)
	}
	slices.Sort(registered)
	if !slices.Equal(tables, registered) {
		t.Errorf("the tables of a project's content with a deleted_at are %v, want those registered with the "+
			"trash, %v", tables, registered)
	}

	// columns returns the columns of the relation, with their types, in
	// order. PostgreSQL takes every column of a view to be nullable, so
	// whether a column is is not compared.
	columns := func(relation string) string {
		var s string
		if err := admin.QueryRow(ctx, `SELECT coalesce(string_agg(column_name || ' ' || data_type, ', '
			ORDER BY ordinal_position), '') FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1`, relation).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	// live returns the IDs of the workspace's rows that the view shows, read
	// in a transaction of the workspace.
	live := func(view string, w *isolationtest.Workspace) []ids.UUID {
		var got []ids.UUID
		if err := f.DB.InTx(ctx, w.Scope(), func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, "SELECT id FROM "+pgx.Identifier{view}.Sanitize())
			if err != nil {
				return err
			}
			got, err = pgx.CollectRows(rows, pgx.RowTo[ids.UUID])
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return got
	}
	for _, tt := range trashTables {
		view := "live_" + tt.name
		var options []string
		if err := admin.QueryRow(ctx, `SELECT coalesce(reloptions, '{}') FROM pg_class
			WHERE relname = $1 AND relkind = 'v'`, view).Scan(&options); err != nil {
			t.Errorf("the table %s has no view %s: %v", tt.name, view, err)
			continue
		}
		if !slices.Contains(options, "security_invoker=true") {
			t.Errorf("the view %s is not security_invoker: %v", view, options)
		}
		if got, want := columns(view), columns(tt.name); got != want {
			t.Errorf("the view %s has the columns %s, want its table's %s", view, got, want)
		}
		if got := live(view, f.A); !slices.Equal(got, []ids.UUID{f.A.Rows[tt.name]}) {
			t.Errorf("before its deletion, the view %s shows %v, want the workspace's row %s", view, got,
				f.A.Rows[tt.name])
		}
		if _, err := admin.Exec(ctx, "UPDATE "+pgx.Identifier{tt.name}.Sanitize()+
			" SET deleted_at = now() - interval '31 days' WHERE id = $1", f.A.Rows[tt.name]); err != nil {
			t.Fatal(err)
		}
		if got := live(view, f.A); len(got) != 0 {
			t.Errorf("the view %s shows %v of the trash", view, got)
		}
		if got := live(view, f.B); !slices.Equal(got, []ids.UUID{f.B.Rows[tt.name]}) {
			t.Errorf("the view %s shows %v of another workspace, want its row %s", view, got, f.B.Rows[tt.name])
		}
	}

	// The trash lists each row in it of a table whose rows it lists.
	var items []queries.TrashItemsRow
	if err := f.DB.InTx(ctx, f.A.Scope(), func(tx pgx.Tx) error {
		items, err = queries.New(tx).TrashItems(ctx, queries.TrashItemsParams{WorkspaceID: uuid(f.A.ID),
			ProjectID: uuid(f.A.Rows["project"]), MaxRows: 100})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var got, want []string
	for _, it := range items {
		got = append(got, it.Kind+" "+ids.UUID(it.ID.Bytes).String())
	}
	for _, tt := range trashTables {
		if tt.kind != "" {
			want = append(want, tt.kind+" "+f.A.Rows[tt.name].String())
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("the trash lists %v, want %v", got, want)
	}
	for _, it := range items {
		if !slices.Contains(trash.Kinds(), it.Kind) {
			t.Errorf("the trash lists an entity of the kind %q, which its schema does not take", it.Kind)
		}
	}

	// The purge of a workspace deletes each of its rows whose time has
	// passed, and none of the other workspace's, whose time has passed too.
	for _, tt := range trashTables {
		if _, err := admin.Exec(ctx, "UPDATE "+pgx.Identifier{tt.name}.Sanitize()+
			" SET deleted_at = now() - interval '31 days' WHERE id = $1", f.B.Rows[tt.name]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := trash.PurgeWorkspace(ctx, f.DB, f.A.ID); err != nil {
		t.Fatal(err)
	}
	for _, tt := range trashTables {
		for w, want := range map[*isolationtest.Workspace]int{f.A: 0, f.B: 1} {
			var n int
			if err := admin.QueryRow(ctx, "SELECT count(*) FROM "+pgx.Identifier{tt.name}.Sanitize()+
				" WHERE id = $1", w.Rows[tt.name]).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != want {
				t.Errorf("after the purge, the %s %s is there %d times, want %d", tt.name, w.Rows[tt.name], n, want)
			}
		}
	}
}

// uuid returns id as a uuid column.
func uuid(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

// TestTrashIsolation tries the queries of the trash, and the reads of the
// live views, from another workspace's transaction, where row-level security
// must keep them from reading or deleting its rows, or locking its projects
// (NFR-SEC-01).
//
// Verifies: NFR-SEC-01
func TestTrashIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := isolationtest.New(t, dbtest.New(t), isolationtest.Tables)
	retention := database.Interval(trash.Retention)
	// The live views, read before the workspaces' rows go to the trash.
	for _, op := range []isolationtest.Op{
		{Name: "a project's live boards", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			rows, err := queries.New(tx).Boards(ctx, queries.BoardsParams{WorkspaceID: uuid(target.ID),
				ProjectID: uuid(target.Rows["project"]), MaxRows: 10})
			var out []ids.UUID
			for _, r := range rows {
				out = append(out, r.LiveBoard.ID.Bytes)
			}
			return out, err
		}},
		{Name: "a project's live folders", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			rows, err := queries.New(tx).Folders(ctx, queries.FoldersParams{WorkspaceID: uuid(target.ID),
				ProjectID: uuid(target.Rows["project"]), MaxRows: 10})
			var out []ids.UUID
			for _, r := range rows {
				out = append(out, r.LiveFolder.ID.Bytes)
			}
			return out, err
		}},
		{Name: "a board's live nodes", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			rows, err := queries.New(tx).BoardNodes(ctx, queries.BoardNodesParams{WorkspaceID: uuid(target.ID),
				ProjectID: uuid(target.Rows["project"]), BoardID: uuid(target.Rows["board"]), MaxRows: 10})
			var out []ids.UUID
			for _, r := range rows {
				out = append(out, r.LiveNode.ID.Bytes)
			}
			return out, err
		}},
		{Name: "a board's live connections", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			rows, err := queries.New(tx).BoardConnections(ctx, queries.BoardConnectionsParams{
				WorkspaceID: uuid(target.ID), ProjectID: uuid(target.Rows["project"]),
				BoardID: uuid(target.Rows["board"]), MaxRows: 10})
			var out []ids.UUID
			for _, r := range rows {
				out = append(out, r.LiveConnection.ID.Bytes)
			}
			return out, err
		}},
		{Name: "a project's live variables", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			rows, err := queries.New(tx).Variables(ctx, queries.VariablesParams{WorkspaceID: uuid(target.ID),
				ProjectID: uuid(target.Rows["project"]), MaxRows: 10})
			var out []ids.UUID
			for _, r := range rows {
				out = append(out, r.LiveVariable.ID.Bytes)
			}
			return out, err
		}},
		{Name: "a project's live component types", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			rows, err := queries.New(tx).ComponentTypes(ctx, queries.ComponentTypesParams{WorkspaceID: uuid(target.ID),
				ProjectID: uuid(target.Rows["project"]), MaxRows: 10})
			var out []ids.UUID
			for _, r := range rows {
				out = append(out, r.LiveComponentType.ID.Bytes)
			}
			return out, err
		}},
		{Name: "a project's live attribute definitions", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			rows, err := queries.New(tx).TypeAttributeDefs(ctx, queries.TypeAttributeDefsParams{
				WorkspaceID: uuid(target.ID), ProjectID: uuid(target.Rows["project"]),
				TypeIds: []pgtype.UUID{uuid(target.Rows["component_type"])}})
			var out []ids.UUID
			for _, r := range rows {
				out = append(out, r.LiveAttributeDef.ID.Bytes)
			}
			return out, err
		}},
		{Name: "a project's live components", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			rows, err := queries.New(tx).Components(ctx, queries.ComponentsParams{WorkspaceID: uuid(target.ID),
				ProjectID: uuid(target.Rows["project"]), MaxRows: 10})
			var out []ids.UUID
			for _, r := range rows {
				out = append(out, r.LiveComponent.ID.Bytes)
			}
			return out, err
		}},
	} {
		f.Check(t, op)
	}
	for _, w := range []*isolationtest.Workspace{f.A, f.B} {
		for _, table := range []string{"board", "node", "connection", "folder", "variable", "component_type",
			"attribute_def", "component"} {
			if _, err := f.Admin().Exec(ctx, "UPDATE "+table+" SET deleted_at = now() - interval '31 days' "+
				"WHERE id = $1", w.Rows[table]); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, op := range []isolationtest.Op{
		{Name: "a project's trash", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			rows, err := queries.New(tx).TrashItems(ctx, queries.TrashItemsParams{WorkspaceID: uuid(target.ID),
				ProjectID: uuid(target.Rows["project"]), MaxRows: 10})
			var out []ids.UUID
			for _, r := range rows {
				out = append(out, r.ID.Bytes)
			}
			return out, err
		}},
		{Name: "the projects to purge", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			rows, err := queries.New(tx).ProjectsToPurge(ctx, queries.ProjectsToPurgeParams{
				WorkspaceID: uuid(target.ID), Retention: retention})
			var out []ids.UUID
			for _, r := range rows {
				out = append(out, r.Bytes)
			}
			return out, err
		}},
		{Name: "lock a project to purge", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			id, err := queries.New(tx).LockProjectToPurge(ctx, queries.LockProjectToPurgeParams{
				WorkspaceID: uuid(target.ID), ID: uuid(target.Rows["project"])})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil
			}
			return []ids.UUID{id.Bytes}, err
		}},
		{Name: "purge a project's trash", Blind: true, Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			q := queries.New(tx)
			if _, err := q.PurgeBoards(ctx, queries.PurgeBoardsParams{WorkspaceID: uuid(target.ID),
				ProjectID: uuid(target.Rows["project"]), Retention: retention, MaxRows: 10}); err != nil {
				return nil, err
			}
			if _, err := q.PurgeNodes(ctx, queries.PurgeNodesParams{WorkspaceID: uuid(target.ID),
				ProjectID: uuid(target.Rows["project"]), Retention: retention, MaxRows: 10}); err != nil {
				return nil, err
			}
			if _, err := q.PurgeConnections(ctx, queries.PurgeConnectionsParams{WorkspaceID: uuid(target.ID),
				ProjectID: uuid(target.Rows["project"]), Retention: retention, MaxRows: 10}); err != nil {
				return nil, err
			}
			if _, err := q.PurgeVariables(ctx, queries.PurgeVariablesParams{WorkspaceID: uuid(target.ID),
				ProjectID: uuid(target.Rows["project"]), Retention: retention, MaxRows: 10}); err != nil {
				return nil, err
			}
			if _, err := q.PurgeComponents(ctx, queries.PurgeComponentsParams{WorkspaceID: uuid(target.ID),
				ProjectID: uuid(target.Rows["project"]), Retention: retention, MaxRows: 10}); err != nil {
				return nil, err
			}
			if _, err := q.PurgeAttributeDefs(ctx, queries.PurgeAttributeDefsParams{WorkspaceID: uuid(target.ID),
				ProjectID: uuid(target.Rows["project"]), Retention: retention, MaxRows: 10}); err != nil {
				return nil, err
			}
			if _, err := q.PurgeComponentTypes(ctx, queries.PurgeComponentTypesParams{WorkspaceID: uuid(target.ID),
				ProjectID: uuid(target.Rows["project"]), Retention: retention, MaxRows: 10}); err != nil {
				return nil, err
			}
			_, err := q.PurgeFolders(ctx, queries.PurgeFoldersParams{WorkspaceID: uuid(target.ID),
				ProjectID: uuid(target.Rows["project"]), Retention: retention, MaxRows: 10})
			return nil, err
		}},
	} {
		f.Check(t, op)
	}
}

// TestKinds checks that the kinds of entity that the trash lists are those
// that the schema of its items takes, which their enum tag gives, so that a
// kind added to the list and not to the schema, or to the schema and not to
// the list, fails.
func TestKinds(t *testing.T) {
	t.Parallel()
	f, ok := reflect.TypeFor[trash.Item]().FieldByName("Kind")
	if got, want := f.Tag.Get("enum"), strings.Join(trash.Kinds(), ","); !ok || got != want {
		t.Errorf("an item's kind takes %q, want %q", got, want)
	}
}
