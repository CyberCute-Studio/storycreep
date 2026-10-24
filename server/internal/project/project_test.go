package project_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/role"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
)

func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

// fixture is a database of the test's own with a workspace whose owner is
// owner, and the projects' service with the pipeline of their commands.
type fixture struct {
	d        *dbtest.Database
	db       *database.DB
	ws       ids.UUID
	owner    ids.UUID
	pipeline *commands.Pipeline
	svc      *project.Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	d := dbtest.New(t)
	f := &fixture{d: d, db: d.Open(t), ws: ids.New()}
	admin := d.Admin(t)
	var err error
	if f.owner, err = isolationtest.NewUser(ctx, admin); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO workspace (id, name, slug, plan_key, settings) VALUES ($1, 'Harbour', $2, 'unlimited',
			'{"default_locale": "cs"}')`, []any{f.ws, "harbour-" + f.ws.String()}},
		{`INSERT INTO membership (id, workspace_id, user_id, role) VALUES ($1, $2, $3, 'owner')`,
			[]any{ids.New(), f.ws, f.owner}},
	} {
		if _, err := admin.Exec(ctx, c.sql, c.args...); err != nil {
			t.Fatal(err)
		}
	}
	reg := commands.NewRegistry()
	project.Register(reg)
	ent := entitlements.New(entitlements.Options{Meter: usage.Meter{}})
	f.pipeline = commands.New(f.db, commands.Options{Registry: reg, Entitlements: ent})
	f.svc = project.New(f.db, project.Options{Entitlements: ent, Pipeline: f.pipeline})
	return f
}

// create creates a project named name, as the owner.
func (f *fixture) create(t *testing.T, name string) project.Visible {
	t.Helper()
	p, err := f.svc.Create(context.Background(), f.owner, f.ws, project.Settings{Name: name}, audit.Client{})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// actor is the owner, as the actor of a batch.
func (f *fixture) actor() commands.Actor {
	return commands.Actor{UserID: f.owner}
}

// state reads the project id as the superuser, whether or not it is in the
// trash: what a change and its undo must leave alike.
func (f *fixture) state(t *testing.T, id string) string {
	t.Helper()
	var s string
	if err := f.d.Admin(t).QueryRow(context.Background(), `SELECT concat_ws('|', name, slug, description,
		settings::text, archived_at IS NOT NULL, deleted_at IS NOT NULL) FROM project WHERE id = $1`,
		decode(t, id)).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// inverse returns the inverse of the batch of the project id whose seq is
// seq, which the operation log keeps, as a batch of commands.
func (f *fixture) inverse(t *testing.T, id string, seq int64) []commands.Command {
	t.Helper()
	var steps []struct {
		Type    commands.Type   `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	var inverse []byte
	if err := f.d.Admin(t).QueryRow(context.Background(),
		"SELECT inverse FROM operation WHERE project_id = $1 AND seq = $2", decode(t, id), seq).Scan(
		&inverse); err != nil || json.Unmarshal(inverse, &steps) != nil {
		t.Fatalf("the inverse of %d, %s: %v", seq, inverse, err)
	}
	batch := make([]commands.Command, len(steps))
	for i, st := range steps {
		batch[i] = commands.Command{ID: ids.New(), Type: st.Type, Payload: st.Payload}
	}
	return batch
}

// decode returns the UUID of a project's ID.
func decode(t *testing.T, id string) ids.UUID {
	t.Helper()
	return decodeAs(t, ids.Project, id)
}

// decodeAs returns the UUID of an ID of the kind.
func decodeAs(t *testing.T, kind ids.Kind, id string) ids.UUID {
	t.Helper()
	u, err := ids.Decode(kind, id)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// TestCreate checks that a new project has the workspace's default locale
// unless it names its own, which it keeps in its canonical form, lets
// editors publish, and counts towards the workspace's projects.
func TestCreate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.create(t, "Harbour")
	if p.DefaultLocale != "cs" || p.PublishMinRole != project.PublishEditor || p.Role != role.Owner || p.Rev != 1 ||
		p.Slug != "harbour" {
		t.Errorf("the new project is %+v", p)
	}
	q, err := f.svc.Create(ctx, f.owner, f.ws, project.Settings{Name: "Lighthouse", DefaultLocale: "en-gb"},
		audit.Client{})
	if err != nil || q.DefaultLocale != "en-GB" {
		t.Errorf("a project created with en-gb has %q, %v; want en-GB", q.DefaultLocale, err)
	}
	var used int64
	if err := f.db.InTx(ctx, database.Scope{WorkspaceID: f.ws}, func(tx pgx.Tx) error {
		m, err := usage.Read(ctx, tx, f.ws)
		used = m[usage.Projects]
		return err
	}); err != nil || used != 2 {
		t.Errorf("the workspace uses %d projects, %v; want 2", used, err)
	}
}

// TestUndo checks the inverse of every command of a project: applying the
// inverse that the operation log keeps of a change leaves the project as it
// was before the change, and applying the inverse of a change that changed
// nothing, as unarchiving a project that is not archived, changes nothing.
func TestUndo(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.create(t, "Harbour")
	board, node := ids.Encode(ids.Board, ids.New()), ids.Encode(ids.Node, ids.New())
	// The entry's board is one of the project's (package board, N32), and its
	// node one of the board's (package node, N43).
	if _, err := f.d.Admin(t).Exec(ctx, `INSERT INTO board (id, workspace_id, project_id, "order", name, slug)
		VALUES ($1, $2, $3, 'V', 'Harbour', 'harbour')`, decodeAs(t, ids.Board, board), f.ws, decode(t, p.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.Admin(t).Exec(ctx, `INSERT INTO node (id, workspace_id, project_id, board_id, type, x, y)
		VALUES ($1, $2, $3, $4, 'branch', 0, 0)`, decodeAs(t, ids.Node, node), f.ws, decode(t, p.ID),
		decodeAs(t, ids.Board, board)); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		typ     commands.Type
		payload string
	}{
		{project.Rename, `{"name": "Lighthouse"}`},
		{project.SetSlug, `{"slug": "lighthouse"}`},
		{project.Describe, `{"description": "Elara's harbour"}`},
		{project.UpdateSettings, `{"default_locale": "en", "publish_min_role": "admin", "entry": {"board_id": "` +
			board + `", "node_id": "` + node + `"}}`},
		{project.UpdateSettings, `{"entry": {}}`},
		{project.Archive, `{}`},
		{project.Unarchive, `{}`},
		{project.Delete, `{}`},
		{project.Restore, `{}`},
	} {
		// Each change applies to the project as the undone one left it, so the
		// project has an entry for removing it, is archived for unarchiving,
		// and deleted for restoring.
		switch c.typ {
		case project.UpdateSettings:
			if c.payload == `{"entry": {}}` {
				if _, err := f.svc.Update(ctx, f.actor(), decode(t, p.ID),
					project.Patch{Entry: &project.ProjectEntry{BoardID: board}}); err != nil {
					t.Fatal(err)
				}
			}
		case project.Unarchive:
			if _, err := f.svc.Archive(ctx, f.actor(), decode(t, p.ID)); err != nil {
				t.Fatal(err)
			}
		case project.Restore:
			if err := f.svc.Delete(ctx, f.actor(), decode(t, p.ID)); err != nil {
				t.Fatal(err)
			}
		}
		before := f.state(t, p.ID)
		res, err := f.pipeline.Apply(ctx, f.actor(), decode(t, p.ID), []commands.Command{{ID: ids.New(), Type: c.typ,
			Payload: json.RawMessage(c.payload)}})
		if err != nil {
			t.Fatalf("%s: %v", c.typ, err)
		}
		if f.state(t, p.ID) == before {
			t.Errorf("%s %s changed nothing", c.typ, c.payload)
		}
		if _, err := f.pipeline.Apply(ctx, f.actor(), decode(t, p.ID), f.inverse(t, p.ID, res.Seq)); err != nil {
			t.Fatalf("undo %s: %v", c.typ, err)
		}
		if got := f.state(t, p.ID); got != before {
			t.Errorf("undoing %s left %s, want %s", c.typ, got, before)
		}
		// Leave the project neither archived nor deleted for the next change.
		if _, err := f.svc.Restore(ctx, f.actor(), decode(t, p.ID)); err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.Unarchive(ctx, f.actor(), decode(t, p.ID)); err != nil {
			t.Fatal(err)
		}
	}
	// A change that changes nothing is undone by nothing.
	res, err := f.pipeline.Apply(ctx, f.actor(), decode(t, p.ID), []commands.Command{{ID: ids.New(),
		Type: project.Unarchive, Payload: json.RawMessage(`{}`)}})
	if err != nil || len(res.Revisions) != 0 {
		t.Errorf("unarchiving a project that is not archived changed %v, %v", res.Revisions, err)
	}
}

// TestStoredText checks that a project's name, description, or default
// locale that is stored but that the rules of a new one refuse, as a row that
// the pipeline did not write may hold, or one that a later change of
// input.IsControl, of Go's Unicode tables, or of the tables of
// golang.org/x/text/language leaves, is changed as any other is, rather than
// fail every change of it with the step of its inverse that sets it back;
// that the undo of the change, which sends that step, is refused for it, as a
// batch that is not valid; and that what a command may write is as it was, so
// that a name, a description, or a locale that the rules refuse is a problem
// of its batch, listed with the others.
func TestStoredText(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.create(t, "Harbour")
	id := decode(t, p.ID)
	_, err := f.pipeline.Apply(ctx, f.actor(), id, []commands.Command{
		{ID: ids.New(), Type: project.Rename, Payload: json.RawMessage(`{"name": "Harbour\u0007"}`)},
		{ID: ids.New(), Type: project.Describe, Payload: json.RawMessage(`{"description": " padded"}`)},
		{ID: ids.New(), Type: project.UpdateSettings, Payload: json.RawMessage(`{"default_locale": "en-gb",
			"entry": {"node_id": "nd_nope"}}`)},
	})
	var invalid *commands.InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("a batch of a name, a description, and a locale that the rules refuse: %v", err)
	}
	var got []string
	for _, pr := range invalid.Problems {
		got = append(got, fmt.Sprintf("%d %s", pr.Command, pr.Field))
	}
	if want := []string{"0 payload.name", "1 payload.description", "2 payload.entry.node_id",
		"2 payload.default_locale"}; !slices.Equal(got, want) {
		t.Errorf("the batch's problems are at %q, want %q", got, want)
	}

	// A line separator, a control character, and a locale that is not in its
	// canonical form, none of which a command may write.
	if _, err := f.d.Admin(t).Exec(ctx, `UPDATE project SET name = 'Har' || chr(8232) || 'bour',
		description = 'Elara' || chr(27), settings = settings || '{"default_locale": "en-gb"}' WHERE id = $1`,
		id); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		typ            commands.Type
		payload, field string
	}{
		{project.Rename, `{"name": "Lighthouse"}`, "payload.name"},
		{project.Describe, `{"description": "Elara's harbour"}`, "payload.description"},
		{project.UpdateSettings, `{"default_locale": "cs"}`, "payload.default_locale"},
	} {
		res, err := f.pipeline.Apply(ctx, f.actor(), id, []commands.Command{{ID: ids.New(), Type: c.typ,
			Payload: json.RawMessage(c.payload)}})
		if err != nil {
			t.Errorf("%s of a stored value that the rules refuse: %v", c.typ, err)
			continue
		}
		_, err = f.pipeline.Apply(ctx, f.actor(), id, f.inverse(t, p.ID, res.Seq))
		if !errors.As(err, &invalid) || !slices.ContainsFunc(invalid.Problems, func(pr commands.Problem) bool {
			return pr.Command == 0 && pr.Field == c.field
		}) {
			t.Errorf("the undo of %s was not refused as invalid at %s: %v", c.typ, c.field, err)
		}
	}
	if got, err := f.svc.Get(ctx, f.actor(), id); err != nil || got.Name != "Lighthouse" ||
		got.Description != "Elara's harbour" || got.DefaultLocale != "cs" {
		t.Errorf("the project is %+v, %v", got, err)
	}

	// The database holds a name and a description to the characters that the
	// payload's schema takes, which it checks of the step too, so that no
	// stored one is longer.
	for _, c := range []struct{ sql, constraint string }{
		{`UPDATE project SET name = repeat('ř', 101) WHERE id = $1`, "project_name_length"},
		{`UPDATE project SET description = repeat('ř', 2001) WHERE id = $1`, "project_description_length"},
	} {
		var pg *pgconn.PgError
		if _, err := f.d.Admin(t).Exec(ctx, c.sql, id); !errors.As(err, &pg) || pg.Code != "23514" ||
			pg.ConstraintName != c.constraint {
			t.Errorf("%s was not refused by %s: %v", c.sql, c.constraint, err)
		}
	}
}

// TestReconcileProjects checks that reconciliation corrects a workspace's
// usage of projects, deliberately corrupted, to a recount of the projects
// that count, those neither archived nor in the trash.
//
// Verifies: FR-ENT-02/AC1
func TestReconcileProjects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	for _, name := range []string{"A", "B", "C"} {
		f.create(t, name)
	}
	archived, deleted := f.create(t, "Archived"), f.create(t, "Deleted")
	if _, err := f.svc.Archive(ctx, f.actor(), decode(t, archived.ID)); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Delete(ctx, f.actor(), decode(t, deleted.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.Admin(t).Exec(ctx, "INSERT INTO usage_delta (id, workspace_id, key, delta) "+
		"VALUES ($1, $2, 'projects', 7)", ids.New(), f.ws); err != nil {
		t.Fatal(err)
	}
	if _, err := usage.Reconcile(ctx, f.db); err != nil {
		t.Fatal(err)
	}
	var used int64
	if err := f.db.InTx(ctx, database.Scope{WorkspaceID: f.ws}, func(tx pgx.Tx) error {
		m, err := usage.Read(ctx, tx, f.ws)
		used = m[usage.Projects]
		return err
	}); err != nil || used != 3 {
		t.Errorf("after reconciliation, the workspace uses %d projects, %v; want 3", used, err)
	}
}

// TestPurgeSkipsALockedProject checks that the purge skips a project whose
// time in the trash has passed but whose row a transaction holds, as a batch
// of commands does, rather than wait for it, and purges it once the row is
// free.
func TestPurgeSkipsALockedProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	p := f.create(t, "Harbour")
	if err := f.svc.Delete(ctx, f.actor(), decode(t, p.ID)); err != nil {
		t.Fatal(err)
	}
	admin := f.d.Admin(t)
	if _, err := admin.Exec(ctx, "UPDATE project SET hard_delete_after = now() - interval '1 second' WHERE id = $1",
		decode(t, p.ID)); err != nil {
		t.Fatal(err)
	}
	holder := f.d.Admin(t)
	for _, sql := range []string{"BEGIN", "SELECT seq FROM project WHERE id = $1 FOR NO KEY UPDATE"} {
		var args []any
		if sql != "BEGIN" {
			args = []any{decode(t, p.ID)}
		}
		if _, err := holder.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := project.Purge(ctx, f.db); err != nil || n != 0 {
		t.Errorf("a purge while the project's row is locked deleted %d, %v; want none", n, err)
	}
	if _, err := holder.Exec(ctx, "COMMIT"); err != nil {
		t.Fatal(err)
	}
	if n, err := project.Purge(ctx, f.db); err != nil || n != 1 {
		t.Errorf("a purge once the row is free deleted %d, %v; want 1", n, err)
	}
	var left pgtype.UUID
	if err := admin.QueryRow(ctx, "SELECT id FROM project WHERE id = $1", decode(t, p.ID)).Scan(&left); err !=
		pgx.ErrNoRows {
		t.Errorf("the purged project's row is still there: %v", err)
	}
}
