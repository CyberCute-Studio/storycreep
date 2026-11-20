package schemaversion_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion"
)

// logEnv is a project of a new database, of a workspace of its own, and the
// database's connections.
type logEnv struct {
	db                *database.DB
	admin             *pgx.Conn
	workspace, member ids.UUID
}

// newLogEnv returns a new database with a workspace, a member of it, and no
// project yet.
func newLogEnv(t *testing.T) *logEnv {
	t.Helper()
	ctx := context.Background()
	d := dbtest.New(t)
	e := &logEnv{db: d.Open(t), admin: d.Admin(t), workspace: ids.New(), member: ids.New()}
	if _, err := e.admin.Exec(ctx, `INSERT INTO "user" (id, email, display_name) VALUES ($1, 'tomas@example.com',
		'Tomas')`, e.member); err != nil {
		t.Fatal(err)
	}
	if _, err := e.admin.Exec(ctx, `INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Harbour',
		'harbour', 'unlimited')`, e.workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := e.admin.Exec(ctx, `INSERT INTO membership (id, workspace_id, user_id, role)
		VALUES ($1, $2, $3, 'owner')`, ids.New(), e.workspace, e.member); err != nil {
		t.Fatal(err)
	}
	return e
}

// project adds a project to the workspace, and returns its ID.
func (e *logEnv) project(t *testing.T) ids.UUID {
	t.Helper()
	id := ids.New()
	if _, err := e.admin.Exec(context.Background(), "INSERT INTO project (id, workspace_id, name) VALUES ($1, $2, "+
		"'Harbour')", id, e.workspace); err != nil {
		t.Fatal(err)
	}
	return id
}

// record logs steps of the snapshot of the project, in a transaction of the
// workspace, or fails the test.
func (e *logEnv) record(t *testing.T, project, snapshot ids.UUID, steps []schemaversion.Step) {
	t.Helper()
	if err := e.db.InTx(context.Background(), database.Scope{WorkspaceID: e.workspace, UserID: e.member},
		func(tx pgx.Tx) error {
			return schemaversion.Record(context.Background(), tx, e.workspace, project, snapshot, steps)
		}); err != nil {
		t.Fatal(err)
	}
}

// log returns the migration log of the project, or fails the test.
func (e *logEnv) log(t *testing.T, project ids.UUID) []schemaversion.Logged {
	t.Helper()
	var out []schemaversion.Logged
	if err := e.db.InTx(context.Background(), database.Scope{WorkspaceID: e.workspace, UserID: e.member},
		func(tx pgx.Tx) error {
			var err error
			out, err = schemaversion.Log(context.Background(), tx, e.workspace, project)
			return err
		}); err != nil {
		t.Fatal(err)
	}
	return out
}

// sameEntries reports whether a and b are the same entries, their values
// compared as JSON values, as the log's jsonb holds them.
func sameEntries(t *testing.T, a, b []schemaversion.Entry) bool {
	t.Helper()
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].File != b[i].File || a[i].Entity != b[i].Entity || a[i].Field != b[i].Field ||
			!sameJSONValue(t, a[i].Before, b[i].Before) || !sameJSONValue(t, a[i].After, b[i].After) {
			return false
		}
	}
	return true
}

// sameJSONValue reports whether a and b are the same JSON value, or both
// none.
func sameJSONValue(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("not JSON: %s", a)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatalf("not JSON: %s", b)
	}
	return reflect.DeepEqual(x, y)
}

// TestLog checks that Record stores each step of a snapshot's migration in
// its project's migration log, with what it reported, a changed value and a
// dropped one, or nothing, which Log reads back, by the snapshots' IDs; that
// a later run of a step that the log holds already replaces its report; that
// a project's log is its own; and that the log is deleted with its project.
//
// Verifies: FR-DATA-03/AC2
func TestLog(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newLogEnv(t)
	project, other := e.project(t), e.project(t)
	first, second := ids.New(), ids.New()
	if second.String() < first.String() {
		first, second = second, first
	}
	reported := []schemaversion.Entry{
		{File: "boards/harbour-intro.json", Entity: "nd_000000000000000000000000N1", Field: "position",
			Before: json.RawMessage(`{"x":0.13,"y":-0.05}`), After: json.RawMessage(`{"x":0,"y":0}`)},
		{File: "project/settings.json", Field: "flags", Before: json.RawMessage(`["fast"]`)},
	}
	e.record(t, project, second, []schemaversion.Step{{From: 1, To: 2}, {From: 2, To: 3}})
	e.record(t, project, first, []schemaversion.Step{{From: 1, To: 2}})
	// The step from 1 again, as a later run of a fixed migration reports it,
	// replaces what it reported.
	e.record(t, project, second, []schemaversion.Step{{From: 1, To: 2, Entries: reported}})
	got := e.log(t, project)
	want := []schemaversion.Logged{
		{Snapshot: ids.Encode(ids.Snapshot, first), Step: schemaversion.Step{From: 1, To: 2}},
		{Snapshot: ids.Encode(ids.Snapshot, second), Step: schemaversion.Step{From: 1, To: 2, Entries: reported}},
		{Snapshot: ids.Encode(ids.Snapshot, second), Step: schemaversion.Step{From: 2, To: 3}},
	}
	if len(got) != len(want) {
		t.Fatalf("the log holds %d steps, %+v; want %d", len(got), got, len(want))
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Snapshot != w.Snapshot || g.From != w.From || g.To != w.To || !sameEntries(t, g.Entries, w.Entries) ||
			g.At.IsZero() {
			t.Errorf("step %d: %+v; want %+v, logged at a moment", i, g, w)
		}
	}
	if got := e.log(t, other); len(got) != 0 {
		t.Errorf("another project's log holds %+v; want none", got)
	}
	if _, err := e.admin.Exec(ctx, "DELETE FROM project WHERE id = $1", project); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := e.admin.QueryRow(ctx, "SELECT count(*) FROM schema_migration_log").Scan(&left); err != nil ||
		left != 0 {
		t.Errorf("once its project is deleted, the log holds %d steps, %v; want none", left, err)
	}
}
