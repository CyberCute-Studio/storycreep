package commands

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/jobs"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
)

func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

// The tests' entity is a note: a row of a project's content with a
// revision, as a node will be (plan item 35), in a table that the tests add
// to their own databases. Package ids knows it as a node, and the tests
// write its ID so, as nd_….
//
// The notes' commands need the operations of the permission matrix that
// every role in the workspace may do, listing the members, and that owners
// and admins may, changing the workspace's settings, which are
// workspace-scoped, as no operation about a project's content that the
// matrix has yet is (plan item 20).
const noteSchema = `
CREATE TABLE note (
	id uuid PRIMARY KEY,
	workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
	project_id uuid NOT NULL,
	body text NOT NULL,
	rev bigint NOT NULL,
	deleted_at timestamptz,
	FOREIGN KEY (workspace_id, project_id) REFERENCES project (workspace_id, id) ON DELETE CASCADE
);
ALTER TABLE note ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_isolation ON note TO storycreep_app
	USING (workspace_id = NULLIF(current_setting('app.workspace_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON note TO storycreep_app;`

// The notes' types of command.
const (
	// noteCreate creates a note, records an audit entry, and enqueues a
	// job, and counts against the workspace's projects, as though a note
	// were one. Deleting the note undoes it.
	noteCreate Type = "note.create"
	// noteUpdate replaces a note's body, which replacing it back undoes.
	noteUpdate Type = "note.update"
	// noteDelete deletes a note to the trash, and cannot be undone. It needs
	// owner or admin.
	noteDelete Type = "note.delete"
	// noteFail creates a note, and then refuses the batch with errRefused.
	noteFail Type = "note.fail"
	// noteMisreport changes a note as its payload's mode says Changed is not
	// to be called.
	noteMisreport Type = "note.misreport"
	// noteBadInverse is undone by a step that cannot be applied, or has an
	// inverse that reports a change.
	noteBadInverse Type = "note.bad_inverse"
	// noteMeter adds to a key of the workspace's usage what its payload
	// says, as creating a note would add to nodes. It declares the projects
	// limit, and no other.
	noteMeter Type = "note.meter"
	// noteTouch changes nothing, and applies to an archived project too, as
	// unarchiving one does, and noteRevive changes nothing either, and
	// applies to an archived project and to one in the trash, as restoring
	// one does.
	noteTouch  Type = "note.touch"
	noteRevive Type = "note.revive"
	// notePublish changes nothing, and needs snapshot:write rather than
	// project:write of an API token, as publishing a snapshot would.
	notePublish Type = "note.publish"
)

// errRefused is noteFail's refusal.
var errRefused = errors.New("the note refuses the batch")

// notePayload is the payload of a note's commands.
type notePayload struct {
	ID   string `json:"id"`
	Body string `json:"body" maxLength:"100"`
}

// noteIDPayload is the payload of a command that names only a note.
type noteIDPayload struct {
	ID string `json:"id"`
}

// misreportPayload is noteMisreport's payload.
type misreportPayload struct {
	ID   string `json:"id"`
	Mode string `json:"mode" enum:"kind,stale,zero"`
}

// badInversePayload is noteBadInverse's payload.
type badInversePayload struct {
	ID string `json:"id"`
	// Step is the type of its inverse's step, whose payload is an empty
	// object.
	Step Type `json:"step"`
	// Changes makes the inverse report a change of the note instead, which
	// only Apply may, and then be the note's deletion, which an undo could
	// apply.
	Changes bool `json:"changes,omitempty"`
}

// meterPayload is noteMeter's payload. Undeclared makes the command declare
// that it adds nothing to the projects limit, whatever it adds, and Later
// makes it add its delta once every command of the batch has applied
// (Tx.AddUsageOnceApplied).
type meterPayload struct {
	Key        string `json:"key"`
	Delta      int64  `json:"delta"`
	Undeclared bool   `json:"undeclared,omitempty"`
	Later      bool   `json:"later,omitempty"`
}

// noteState is a note as the States of its entity show it.
type noteState struct {
	ID      string `json:"id"`
	Body    string `json:"body"`
	Rev     int64  `json:"rev"`
	Deleted bool   `json:"deleted"`
}

// noteJob is the job that noteCreate enqueues.
type noteJob struct {
	NoteID string `json:"note_id"`
}

func (noteJob) Kind() string { return "test_note" }

// checkNoteID returns what is wrong with id as a note's ID.
func checkNoteID(id string) input.FieldErrors {
	if _, err := ids.Decode(ids.Node, id); err != nil {
		return input.FieldErrors{{Field: "id", Message: err.Error()}}
	}
	return nil
}

// noteUUID returns the UUID of the note's ID, which Check has checked.
func noteUUID(id string) ids.UUID {
	u, _ := ids.Decode(ids.Node, id)
	return u
}

// noteRef returns the entity of the note id.
func noteRef(id ids.UUID) Ref {
	return Ref{Kind: ids.Node, ID: id}
}

// readNote reads the note id of the batch's project, and pgx.ErrNoRows if it
// has none.
func readNote(ctx context.Context, tx *Tx, id ids.UUID) (noteState, error) {
	var s noteState
	err := tx.QueryRow(ctx, "SELECT body, rev, deleted_at IS NOT NULL FROM note "+
		"WHERE workspace_id = $1 AND project_id = $2 AND id = $3", tx.WorkspaceID(), tx.ProjectID(), id).
		Scan(&s.Body, &s.Rev, &s.Deleted)
	s.ID = ids.Encode(ids.Node, id)
	return s, err
}

// setNote changes the note id of the batch's project as set says, which
// assigns columns, increments its revision, and reports the change.
func setNote(ctx context.Context, tx *Tx, id ids.UUID, set string, args ...any) error {
	var rev int64
	if err := tx.QueryRow(ctx, "UPDATE note SET "+set+", rev = rev + 1 WHERE workspace_id = $1 AND project_id = $2 "+
		"AND id = $3 RETURNING rev", append([]any{tx.WorkspaceID(), tx.ProjectID(), id}, args...)...).
		Scan(&rev); err != nil {
		return err
	}
	return tx.Changed(ids.Node, id, rev)
}

// insertNote adds the note id to the batch's project, and reports it.
func insertNote(ctx context.Context, tx *Tx, id ids.UUID, body string) error {
	if _, err := tx.Exec(ctx, "INSERT INTO note (id, workspace_id, project_id, body, rev) VALUES ($1, $2, $3, $4, 1)",
		id, tx.WorkspaceID(), tx.ProjectID(), body); err != nil {
		return err
	}
	return tx.Changed(ids.Node, id, 1)
}

// noteRegistry returns a registry of the notes' commands.
func noteRegistry() *Registry {
	r := NewRegistry()
	RegisterEntity(r, Entity{
		Kind: ids.Node,
		Revs: func(ctx context.Context, tx *Tx, notes []ids.UUID) (map[ids.UUID]int64, error) {
			revs := map[ids.UUID]int64{}
			rows, err := tx.Query(ctx, "SELECT id, rev FROM note "+
				"WHERE workspace_id = $1 AND project_id = $2 AND id = ANY($3::uuid[])", tx.WorkspaceID(), tx.ProjectID(),
				notes)
			if err != nil {
				return nil, err
			}
			var id ids.UUID
			var rev int64
			_, err = pgx.ForEachRow(rows, []any{&id, &rev}, func() error {
				revs[id] = rev
				return nil
			})
			return revs, err
		},
		States: func(ctx context.Context, tx *Tx, notes []ids.UUID) (map[ids.UUID]any, error) {
			states := map[ids.UUID]any{}
			rows, err := tx.Query(ctx, "SELECT id, body, rev, deleted_at IS NOT NULL FROM note "+
				"WHERE workspace_id = $1 AND project_id = $2 AND id = ANY($3::uuid[])", tx.WorkspaceID(), tx.ProjectID(),
				notes)
			if err != nil {
				return nil, err
			}
			var id ids.UUID
			var s noteState
			_, err = pgx.ForEachRow(rows, []any{&id, &s.Body, &s.Rev, &s.Deleted}, func() error {
				s.ID = ids.Encode(ids.Node, id)
				states[id] = s
				return nil
			})
			return states, err
		},
	})
	touches := []ids.Kind{ids.Node}
	Register(r, Spec[notePayload]{
		Type: noteCreate, Capability: authz.ListMembers, Scope: scope.ProjectWrite, Touches: touches,
		Limits: []Limit[notePayload]{{Key: entitlements.Projects}},
		Check:  func(p notePayload) input.FieldErrors { return checkNoteID(p.ID) },
		Inverse: func(_ context.Context, _ *Tx, p notePayload) ([]Step, error) {
			return []Step{{Type: noteDelete, Payload: noteIDPayload{ID: p.ID}}}, nil
		},
		Apply: func(ctx context.Context, tx *Tx, p notePayload) error {
			id := noteUUID(p.ID)
			if err := insertNote(ctx, tx, id, p.Body); err != nil {
				return err
			}
			// An event that the audit log registers, in place of a note's
			// own.
			if err := tx.Audit(ctx, audit.Entry{Action: audit.ActionWorkspaceCreate, SubjectID: tx.WorkspaceID(),
				Result: audit.ResultSuccess}); err != nil {
				return err
			}
			_, err := tx.Enqueue(ctx, noteJob{NoteID: p.ID}, nil)
			return err
		},
	})
	Register(r, Spec[notePayload]{
		Type: noteUpdate, Capability: authz.ListMembers, Scope: scope.ProjectWrite, Touches: touches,
		Check: func(p notePayload) input.FieldErrors { return checkNoteID(p.ID) },
		Inverse: func(ctx context.Context, tx *Tx, p notePayload) ([]Step, error) {
			s, err := readNote(ctx, tx, noteUUID(p.ID))
			if err != nil {
				return nil, err
			}
			return []Step{{Type: noteUpdate, Payload: notePayload{ID: p.ID, Body: s.Body}}}, nil
		},
		Apply: func(ctx context.Context, tx *Tx, p notePayload) error {
			return setNote(ctx, tx, noteUUID(p.ID), "body = $4", p.Body)
		},
	})
	Register(r, Spec[noteIDPayload]{
		Type: noteDelete, Capability: authz.ManageWorkspaceSettings, Scope: scope.ProjectWrite, Touches: touches,
		NonUndoable: true,
		Check:       func(p noteIDPayload) input.FieldErrors { return checkNoteID(p.ID) },
		Apply: func(ctx context.Context, tx *Tx, p noteIDPayload) error {
			return setNote(ctx, tx, noteUUID(p.ID), "deleted_at = now()")
		},
	})
	Register(r, Spec[notePayload]{
		Type: noteFail, Capability: authz.ListMembers, Scope: scope.ProjectWrite, Touches: touches,
		NonUndoable: true,
		Check:       func(p notePayload) input.FieldErrors { return checkNoteID(p.ID) },
		Apply: func(ctx context.Context, tx *Tx, p notePayload) error {
			if err := insertNote(ctx, tx, noteUUID(p.ID), p.Body); err != nil {
				return err
			}
			return errRefused
		},
	})
	Register(r, Spec[misreportPayload]{
		Type: noteMisreport, Capability: authz.ListMembers, Scope: scope.ProjectWrite, Touches: touches,
		NonUndoable: true,
		Check:       func(p misreportPayload) input.FieldErrors { return checkNoteID(p.ID) },
		Apply: func(ctx context.Context, tx *Tx, p misreportPayload) error {
			id := noteUUID(p.ID)
			switch p.Mode {
			case "kind":
				return tx.Changed(ids.Board, id, 1)
			case "zero":
				return tx.Changed(ids.Node, id, 0)
			}
			s, err := readNote(ctx, tx, id)
			if err != nil {
				return err
			}
			return tx.Changed(ids.Node, id, s.Rev)
		},
	})
	Register(r, Spec[badInversePayload]{
		Type: noteBadInverse, Capability: authz.ListMembers, Scope: scope.ProjectWrite, Touches: touches,
		Inverse: func(_ context.Context, tx *Tx, p badInversePayload) ([]Step, error) {
			if p.Changes {
				if err := tx.Changed(ids.Node, noteUUID(p.ID), 1<<40); err != nil {
					return nil, err
				}
				return []Step{{Type: noteDelete, Payload: noteIDPayload{ID: p.ID}}}, nil
			}
			return []Step{{Type: p.Step, Payload: struct{}{}}}, nil
		},
		Apply: func(context.Context, *Tx, badInversePayload) error { return nil },
	})
	Register(r, Spec[meterPayload]{
		Type: noteMeter, Capability: authz.ListMembers, Scope: scope.ProjectWrite, Touches: touches,
		NonUndoable: true,
		Limits: []Limit[meterPayload]{{Key: entitlements.Projects, Adds: func(_ context.Context, _ *Tx,
			p meterPayload) (int64, error) {
			if p.Key == string(entitlements.Projects) && !p.Undeclared {
				return p.Delta, nil
			}
			return 0, nil
		}}},
		Apply: func(_ context.Context, tx *Tx, p meterPayload) error {
			if p.Later {
				return tx.AddUsageOnceApplied(usage.Key(p.Key), func(context.Context) (int64, error) {
					return p.Delta, nil
				})
			}
			return tx.AddUsage(usage.Key(p.Key), p.Delta)
		},
	})
	Register(r, Spec[noteIDPayload]{
		Type: noteTouch, Capability: authz.ListMembers, Scope: scope.ProjectWrite, Touches: touches,
		NonUndoable: true, Archived: true,
		Apply: func(context.Context, *Tx, noteIDPayload) error { return nil },
	})
	Register(r, Spec[noteIDPayload]{
		Type: notePublish, Capability: authz.ListMembers, Scope: scope.SnapshotWrite, Touches: touches,
		NonUndoable: true,
		Apply:       func(context.Context, *Tx, noteIDPayload) error { return nil },
	})
	Register(r, Spec[noteIDPayload]{
		Type: noteRevive, Capability: authz.ListMembers, Scope: scope.ProjectWrite, Touches: touches,
		NonUndoable: true, Archived: true,
		Trashed: true,
		Apply:   func(context.Context, *Tx, noteIDPayload) error { return nil },
	})
	return r
}

// The roles of the world's users, by name: a member of each role of the
// permission matrix, a guest without a grant on the project, a guest with an
// editor's grant on it, a viewer with an editor's grant on it, an instance
// admin who is no member, and a user who is no member.
const (
	ownerUser         = "owner"
	adminUser         = "admin"
	editorUser        = "editor"
	commenterUser     = "commenter"
	viewerUser        = "viewer"
	guestUser         = "guest"
	grantedGuestUser  = "granted guest"
	grantedViewerUser = "granted viewer"
	instanceAdminUser = "instance admin"
	outsiderUser      = "outsider"
)

// notice is what a Notifier was told, and whether its context was one that
// nothing cancels.
type notice struct {
	project  ids.UUID
	seq      int64
	detached bool
}

// recorder is a Notifier that records what it is told.
type recorder struct {
	mu      sync.Mutex
	notices []notice
}

func (r *recorder) Committed(ctx context.Context, projectID ids.UUID, seq int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notices = append(r.notices, notice{projectID, seq, ctx.Done() == nil})
}

func (r *recorder) told() []notice {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.notices)
}

// betweenDB is a DB that calls between before its second transaction, which
// is a batch's own, once the pipeline has found the batch's project in the
// first.
type betweenDB struct {
	DB
	between func()
	calls   int
}

func (d *betweenDB) InTx(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error {
	if d.calls++; d.calls == 2 {
		d.between()
	}
	return d.DB.InTx(ctx, scope, fn)
}

// beforeDB is a DB whose transactions call before with each query that they
// run through QueryRow, before they run it, as the reads of the queries'
// :one statements, such as the read of a workspace's plan, are run.
type beforeDB struct {
	DB
	before func(sql string)
}

func (d *beforeDB) InTx(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error {
	return d.DB.InTx(ctx, scope, func(tx pgx.Tx) error { return fn(beforeTx{Tx: tx, before: d.before}) })
}

// beforeTx is a transaction of a beforeDB.
type beforeTx struct {
	pgx.Tx
	before func(sql string)
}

func (t beforeTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	t.before(sql)
	return t.Tx.QueryRow(ctx, sql, args...)
}

// fixedMeter is an entitlements.Meter whose usage of each limit is fixed.
type fixedMeter map[entitlements.LimitKey]int64

func (m fixedMeter) Usage(_ context.Context, _ pgx.Tx, _ ids.UUID, key entitlements.LimitKey) (int64, error) {
	return m[key], nil
}

// world is a database with the note table, a workspace with a project and a
// user of each role, and a pipeline of the notes' commands.
type world struct {
	d     *dbtest.Database
	db    *database.DB
	admin *pgx.Conn
	ws    ids.UUID
	// project is the workspace's project.
	project ids.UUID
	users   map[string]ids.UUID
	queue   *jobs.Queue
	notes   *recorder
}

// newWorld returns a new world.
func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := context.Background()
	d := dbtest.New(t)
	w := &world{d: d, db: d.Open(t), admin: d.Admin(t), ws: ids.New(), project: ids.New(), users: map[string]ids.UUID{},
		notes: &recorder{}}
	w.exec(t, noteSchema)
	w.exec(t, "INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Harbour', $2, 'unlimited')", w.ws,
		"harbour-"+w.ws.String()[:8])
	w.exec(t, "INSERT INTO project (id, workspace_id, name) VALUES ($1, $2, 'Harbour')", w.project, w.ws)
	for name, membership := range map[string]string{ownerUser: "owner", adminUser: "admin", editorUser: "editor",
		commenterUser: "commenter", viewerUser: "viewer", guestUser: "guest", grantedGuestUser: "guest",
		grantedViewerUser: "viewer", instanceAdminUser: "", outsiderUser: ""} {
		u := w.user(t, name == instanceAdminUser)
		w.users[name] = u
		if membership == "" {
			continue
		}
		m := ids.New()
		w.exec(t, "INSERT INTO membership (id, workspace_id, user_id, role) VALUES ($1, $2, $3, $4)", m, w.ws, u,
			membership)
		if name == grantedGuestUser || name == grantedViewerUser {
			w.exec(t, "INSERT INTO project_grant (id, workspace_id, membership_id, project_id, role) "+
				"VALUES ($1, $2, $3, $4, 'editor')", ids.New(), w.ws, m, w.project)
		}
	}
	reg := jobs.NewRegistry()
	jobs.Register(reg, func(context.Context, *jobs.Job[noteJob]) error { return nil })
	q, err := jobs.Open(ctx, w.db, config.DatabaseURL{}, jobs.Options{Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(q.Close)
	w.queue = q
	return w
}

// user adds a user, an instance admin if admin is set, and returns its ID.
func (w *world) user(t *testing.T, admin bool) ids.UUID {
	t.Helper()
	id := ids.New()
	w.exec(t, `INSERT INTO "user" (id, email, display_name, is_instance_admin) VALUES ($1, $2, 'Someone', $3)`, id,
		id.String()+"@example.com", admin)
	return id
}

// exec runs a statement as the superuser.
func (w *world) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := w.admin.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// count returns the number that the query, run as the superuser, counts.
func (w *world) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := w.admin.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// waitsForALock reports whether a session of the world's database comes to
// wait for a lock within a minute, as a batch does for the project's row
// while another transaction holds it.
func (w *world) waitsForALock(t *testing.T) bool {
	t.Helper()
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if w.count(t, "SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() "+
			"AND wait_event_type = 'Lock'") > 0 {
			return true
		}
	}
	return false
}

// pipeline returns a pipeline of the notes' commands in the world, with
// opts changed by configure, if it is not nil.
func (w *world) pipeline(t *testing.T, configure func(*Options)) *Pipeline {
	t.Helper()
	opts := Options{Registry: noteRegistry(), Entitlements: entitlements.New(entitlements.Options{}), Jobs: w.queue,
		Notifier: w.notes}
	if configure != nil {
		configure(&opts)
	}
	return New(w.db, opts)
}

// as returns the actor of the world's user name.
func (w *world) as(name string) Actor {
	return Actor{UserID: w.users[name]}
}

// written is a digest of what batches leave in the world's database: its
// notes, operations, applied commands, the project's seq, audit entries, and
// jobs.
type written struct {
	notes, operations, applied, entries, jobs int
	seq                                       int64
}

// written returns what batches left in the world's database.
func (w *world) written(t *testing.T) written {
	t.Helper()
	var seq int64
	if err := w.admin.QueryRow(context.Background(), "SELECT seq FROM project WHERE id = $1", w.project).
		Scan(&seq); err != nil {
		t.Fatal(err)
	}
	return written{
		notes:      w.count(t, "SELECT count(*) FROM note"),
		operations: w.count(t, "SELECT count(*) FROM operation"),
		applied:    w.count(t, "SELECT count(*) FROM applied_command"),
		entries:    w.count(t, "SELECT count(*) FROM audit_entry"),
		jobs:       w.count(t, "SELECT count(*) FROM river_job WHERE kind = 'test_note'"),
		seq:        seq,
	}
}

// command returns a command of the type with a new ID, whose payload is p
// encoded, which expects the revisions revs.
func command(t *testing.T, typ Type, p any, revs map[Ref]int64) Command {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return Command{ID: ids.New(), Type: typ, Payload: raw, ExpectedRev: revs}
}

// newNote returns the ID of a new note, and its public form.
func newNote() (ids.UUID, string) {
	id := ids.New()
	return id, ids.Encode(ids.Node, id)
}
