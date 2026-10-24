package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/role"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
)

// operationRow is a row of the operation log, as the superuser reads it.
type operationRow struct {
	typ                         string
	commandIDs                  []ids.UUID
	actor                       *ids.UUID
	token                       *ids.UUID
	payload, inverse, entityRef []byte
}

// operation returns the world's operation of the seq.
func (w *world) operation(t *testing.T, seq int64) operationRow {
	t.Helper()
	var o operationRow
	if err := w.admin.QueryRow(context.Background(), "SELECT type, command_ids, actor_user_id, actor_token_id, "+
		"payload, inverse, entity_refs FROM operation WHERE workspace_id = $1 AND project_id = $2 AND seq = $3",
		w.ws, w.project, seq).Scan(&o.typ, &o.commandIDs, &o.actor, &o.token, &o.payload, &o.inverse,
		&o.entityRef); err != nil {
		t.Fatalf("read the operation %d: %v", seq, err)
	}
	return o
}

// sameJSON reports whether the JSON documents a and b are equal.
func sameJSON(t *testing.T, a []byte, b string) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("%s: %v", a, err)
	}
	if err := json.Unmarshal([]byte(b), &y); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	ex, _ := json.Marshal(x)
	ey, _ := json.Marshal(y)
	return string(ex) == string(ey)
}

// TestApplyAppendsTheOperation checks that a batch that applies is appended
// to the project's operation log with the project's next seq, the commands
// that it applied, the inverse that undoes them from the last to the first,
// and the entities that it changed with their revisions after it, which its
// Result names too, and its actor, with the API token that they act with;
// that each of its commands is recorded as applied with the seq; that the
// audit entries and the jobs of its commands commit with it, the entries
// with its actor, token, and client; and that the Notifier is told its seq
// once it has committed, with a context that the request's cancellation does
// not reach.
func TestApplyAppendsTheOperation(t *testing.T) {
	// The request's context can be cancelled, as a request's is.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newWorld(t)
	p := w.pipeline(t, nil)
	a, aID := newNote()
	b, bID := newNote()

	create := command(t, noteCreate, notePayload{ID: aID, Body: "first"}, nil)
	res, err := p.Apply(ctx, w.as(ownerUser), w.project, []Command{create})
	if err != nil {
		t.Fatal(err)
	}
	if want := (Result{Seq: 1, Revisions: []Revision{{noteRef(a), 1}}}); !slices.Equal(res.Revisions,
		want.Revisions) || res.Seq != want.Seq {
		t.Errorf("Apply = %+v, want %+v", res, want)
	}

	update := command(t, noteUpdate, notePayload{ID: aID, Body: "second"}, map[Ref]int64{noteRef(a): 1})
	createB := command(t, noteCreate, notePayload{ID: bID, Body: "other"}, nil)
	again := command(t, noteUpdate, notePayload{ID: aID, Body: "third"}, nil)
	// The second batch is applied with an API token, by a client whose
	// address and user agent the audit log records.
	byToken := Actor{UserID: w.users[ownerUser],
		Token:  &scope.Token{ID: ids.New(), WorkspaceID: w.ws, Scopes: []scope.Scope{scope.ProjectWrite}},
		Client: audit.Client{IP: netip.MustParseAddr("192.0.2.7"), UserAgent: "importer/1.0"}}
	res, err = p.Apply(ctx, byToken, w.project, []Command{update, createB, again})
	if err != nil {
		t.Fatal(err)
	}
	if want := []Revision{{noteRef(a), 3}, {noteRef(b), 1}}; res.Seq != 2 || !slices.Equal(res.Revisions, want) {
		t.Errorf("Apply = %+v, want seq 2 and the revisions %+v", res, want)
	}

	first := w.operation(t, 1)
	if first.typ != string(noteCreate) || !slices.Equal(first.commandIDs, []ids.UUID{create.ID}) ||
		first.actor == nil || *first.actor != w.users[ownerUser] || first.token != nil {
		t.Errorf("the first operation: %s of %v by %v with %v", first.typ, first.commandIDs, first.actor, first.token)
	}
	if !sameJSON(t, first.inverse, fmt.Sprintf(`[{"type": "note.delete", "payload": {"id": %q},
		"expected_rev": {%[1]q: 1}}]`, aID)) {
		t.Errorf("the first operation's inverse: %s", first.inverse)
	}
	second := w.operation(t, 2)
	if second.typ != batchType || !slices.Equal(second.commandIDs, []ids.UUID{update.ID, createB.ID, again.ID}) ||
		second.actor == nil || *second.actor != byToken.UserID || second.token == nil ||
		*second.token != byToken.Token.ID {
		t.Errorf("the second operation: %s of %v by %v with %v", second.typ, second.commandIDs, second.actor,
			second.token)
	}
	wantPayload := fmt.Sprintf(`[
		{"command_id": %q, "type": "note.update", "payload": {"id": %q, "body": "second"}},
		{"command_id": %q, "type": "note.create", "payload": {"id": %q, "body": "other"}},
		{"command_id": %q, "type": "note.update", "payload": {"id": %q, "body": "third"}}]`,
		update.ID, aID, createB.ID, bID, again.ID, aID)
	if !sameJSON(t, second.payload, wantPayload) {
		t.Errorf("the second operation's payload: %s, want %s", second.payload, wantPayload)
	}
	// The inverse undoes the last command first, each from the state that it
	// changed, and expects the revisions that the batch left of what its
	// command changed, which the batch's answer holds as its undo.
	wantInverse := fmt.Sprintf(`[
		{"type": "note.update", "payload": {"id": %q, "body": "second"}, "expected_rev": {%[1]q: 3}},
		{"type": "note.delete", "payload": {"id": %q}, "expected_rev": {%[2]q: 1}},
		{"type": "note.update", "payload": {"id": %[1]q, "body": "first"}, "expected_rev": {%[1]q: 3}}]`, aID, bID)
	if !sameJSON(t, second.inverse, wantInverse) {
		t.Errorf("the second operation's inverse: %s, want %s", second.inverse, wantInverse)
	}
	if undo, err := json.Marshal(wireUndo(res.Undo)); err != nil || !sameJSON(t, undo, wantInverse) {
		t.Errorf("the second batch's undo: %s, want %s", undo, wantInverse)
	}
	wantRefs := fmt.Sprintf(`[{"kind": "node", "id": %q, "rev": 3}, {"kind": "node", "id": %q, "rev": 1}]`, a, b)
	if !sameJSON(t, second.entityRef, wantRefs) {
		t.Errorf("the second operation's entity refs: %s, want %s", second.entityRef, wantRefs)
	}

	for _, c := range []struct {
		id  ids.UUID
		seq int64
	}{{create.ID, 1}, {update.ID, 2}, {createB.ID, 2}, {again.ID, 2}} {
		if n := w.count(t, "SELECT count(*) FROM applied_command WHERE workspace_id = $1 AND project_id = $2 "+
			"AND command_id = $3 AND seq = $4", w.ws, w.project, c.id, c.seq); n != 1 {
			t.Errorf("the command %s is recorded %d times with the seq %d", c.id, n, c.seq)
		}
	}
	got := w.written(t)
	if want := (written{notes: 2, operations: 2, applied: 4, entries: 2, jobs: 2, seq: 2}); got != want {
		t.Errorf("the batches wrote %+v, want %+v", got, want)
	}
	if n := w.count(t, "SELECT count(*) FROM audit_entry WHERE workspace_id = $1 AND actor_user_id = $2", w.ws,
		w.users[ownerUser]); n != 2 {
		t.Errorf("%d of the audit entries are the batches' actor's in their workspace, want 2", n)
	}
	if n := w.count(t, "SELECT count(*) FROM audit_entry WHERE workspace_id = $1 AND actor_user_id = $2 "+
		"AND actor_token_id = $3 AND ip = $4 AND user_agent = $5", w.ws, byToken.UserID, byToken.Token.ID,
		byToken.Client.IP, byToken.Client.UserAgent); n != 1 {
		t.Errorf("%d of the audit entries are the second batch's, with its token and client, want 1", n)
	}
	if want := []notice{{w.project, 1, true}, {w.project, 2, true}}; !slices.Equal(w.notes.told(), want) {
		t.Errorf("the notifier was told %v, want %v", w.notes.told(), want)
	}
}

// TestBatchIsAtomic checks that a batch with one failing command applies none
// of its commands: whatever refuses it, it leaves no note, no operation, no
// applied command, no audit entry, and no job, the project's seq stays as it
// was, and the Notifier is told nothing.
//
// Verifies: FR-API-03/AC2
func TestBatchIsAtomic(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	p := w.pipeline(t, nil)
	x, xID := newNote()
	if _, err := p.Apply(ctx, w.as(ownerUser), w.project,
		[]Command{command(t, noteCreate, notePayload{ID: xID, Body: "kept"}, nil)}); err != nil {
		t.Fatal(err)
	}
	before, told := w.written(t), len(w.notes.told())

	for _, tt := range []struct {
		name string
		// batch returns the batch's commands, after one that would apply on
		// its own: a note's creation.
		batch func() []Command
		want  func(error) bool
	}{
		{
			name: "a command refuses it",
			batch: func() []Command {
				_, id := newNote()
				return []Command{command(t, noteFail, notePayload{ID: id, Body: "refused"}, nil)}
			},
			want: func(err error) bool { return errors.Is(err, errRefused) },
		},
		{
			name: "a stale revision",
			batch: func() []Command {
				return []Command{command(t, noteUpdate, notePayload{ID: xID, Body: "late"}, map[Ref]int64{noteRef(x): 7})}
			},
			want: func(err error) bool {
				var conflict *RevisionConflictError
				return errors.As(err, &conflict)
			},
		},
		{
			name: "a database error",
			batch: func() []Command {
				// The note exists already.
				return []Command{command(t, noteCreate, notePayload{ID: xID, Body: "twice"}, nil)}
			},
			want: func(err error) bool { return err != nil },
		},
		{
			name: "an invalid command",
			batch: func() []Command {
				return []Command{{ID: ids.New(), Type: noteUpdate, Payload: json.RawMessage(`{"id": 1}`)}}
			},
			want: func(err error) bool {
				var invalid *InvalidError
				return errors.As(err, &invalid)
			},
		},
		{
			name: "a misreported change",
			batch: func() []Command {
				return []Command{command(t, noteMisreport, misreportPayload{ID: xID, Mode: "stale"},
					map[Ref]int64{noteRef(x): 1})}
			},
			want: func(err error) bool { return err != nil },
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, id := newNote()
			batch := append([]Command{command(t, noteCreate, notePayload{ID: id, Body: "lost"}, nil)}, tt.batch()...)
			_, err := p.Apply(ctx, w.as(ownerUser), w.project, batch)
			if !tt.want(err) {
				t.Errorf("Apply: %v", err)
			}
			if after := w.written(t); after != before {
				t.Errorf("the refused batch wrote %+v, where there was %+v", after, before)
			}
			if n := len(w.notes.told()); n != told {
				t.Errorf("the notifier was told of %d batches, want %d", n, told)
			}
		})
	}
}

// TestRevisionConflict checks that a batch whose command expects a revision
// that an entity no longer has is refused with each such entity's current
// revision and state, or that it no longer exists, and changes nothing, and
// that every expected revision of a batch is checked against the entities as
// they were before it.
//
// Verifies: FR-API-03/AC3
func TestRevisionConflict(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	p := w.pipeline(t, nil)
	owner := w.as(ownerUser)
	x, xID := newNote()
	trashed, trashedID := newNote()
	gone, goneID := newNote()
	apply := func(cmds ...Command) (Result, error) {
		return p.Apply(ctx, owner, w.project, cmds)
	}
	if _, err := apply(command(t, noteCreate, notePayload{ID: xID, Body: "one"}, nil),
		command(t, noteCreate, notePayload{ID: trashedID, Body: "bin"}, nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := apply(command(t, noteUpdate, notePayload{ID: xID, Body: "two"}, map[Ref]int64{noteRef(x): 1}),
		command(t, noteDelete, noteIDPayload{ID: trashedID}, map[Ref]int64{noteRef(trashed): 1})); err != nil {
		t.Fatal(err)
	}

	stale := command(t, noteUpdate, notePayload{ID: xID, Body: "late"}, map[Ref]int64{noteRef(x): 1})
	fresh := command(t, noteUpdate, notePayload{ID: trashedID, Body: "fine"}, map[Ref]int64{noteRef(trashed): 2})
	missing := command(t, noteUpdate, notePayload{ID: goneID, Body: "nowhere"},
		map[Ref]int64{noteRef(gone): 3, noteRef(trashed): 1})
	_, err := apply(stale, fresh, missing)
	var conflict *RevisionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("Apply: %v, want a revision conflict", err)
	}
	encode := func(s noteState) json.RawMessage {
		b, _ := json.Marshal(s)
		return b
	}
	xNow := encode(noteState{ID: xID, Body: "two", Rev: 2})
	trashedNow := encode(noteState{ID: trashedID, Body: "bin", Rev: 2, Deleted: true})
	want := []Conflict{
		{Command: 0, CommandID: stale.ID, Type: noteUpdate, Ref: noteRef(x), Expected: 1, Exists: true, Current: 2,
			State: xNow},
		{Command: 2, CommandID: missing.ID, Type: noteUpdate, Ref: noteRef(trashed), Expected: 1, Exists: true,
			Current: 2, State: trashedNow},
		{Command: 2, CommandID: missing.ID, Type: noteUpdate, Ref: noteRef(gone), Expected: 3},
	}
	// A command's entities are in order of their IDs, and UUIDv7s made in one
	// millisecond are in no order of their making.
	if bytes.Compare(gone[:], trashed[:]) < 0 {
		want[1], want[2] = want[2], want[1]
	}
	if len(conflict.Conflicts) != len(want) {
		t.Fatalf("the conflicts: %+v, want %+v", conflict.Conflicts, want)
	}
	for i, c := range conflict.Conflicts {
		if c.Command != want[i].Command || c.CommandID != want[i].CommandID || c.Type != want[i].Type ||
			c.Ref != want[i].Ref || c.Expected != want[i].Expected || c.Exists != want[i].Exists ||
			c.Current != want[i].Current || (want[i].State == nil) != (c.State == nil) ||
			(c.State != nil && !sameJSON(t, c.State, string(want[i].State))) {
			t.Errorf("conflict %d: %+v (%s), want %+v (%s)", i, c, c.State, want[i], want[i].State)
		}
	}
	if got := w.written(t); got.seq != 2 || got.notes != 2 {
		t.Errorf("the refused batch left %+v", got)
	}
	var body string
	if err := w.admin.QueryRow(ctx, "SELECT body FROM note WHERE id = $1", trashed).Scan(&body); err != nil ||
		body != "bin" {
		t.Errorf("the refused batch's first command left the body %q (%v)", body, err)
	}

	// Two commands that expect the revision that x has before the batch both
	// apply, although the first gives it another.
	res, err := apply(command(t, noteUpdate, notePayload{ID: xID, Body: "three"}, map[Ref]int64{noteRef(x): 2}),
		command(t, noteUpdate, notePayload{ID: xID, Body: "four"}, map[Ref]int64{noteRef(x): 2}))
	if err != nil {
		t.Fatal(err)
	}
	if want := []Revision{{noteRef(x), 4}}; !slices.Equal(res.Revisions, want) {
		t.Errorf("the revisions: %+v, want %+v", res.Revisions, want)
	}

	// A note that the batch creates has no revision before it.
	z, zID := newNote()
	_, err = apply(command(t, noteCreate, notePayload{ID: zID, Body: "new"}, nil),
		command(t, noteUpdate, notePayload{ID: zID, Body: "newer"}, map[Ref]int64{noteRef(z): 1}))
	if !errors.As(err, &conflict) || len(conflict.Conflicts) != 1 || conflict.Conflicts[0].Exists ||
		conflict.Conflicts[0].Ref != noteRef(z) {
		t.Errorf("Apply: %v, want a conflict on the new note, which did not exist", err)
	}
}

// TestAuthorisation checks that each command's operation is authorised by
// the actor's role that its scope names (PRD §5.5, §5.6): a member whose role
// does not allow it is refused with ErrInsufficientRole, as is a guest with a
// grant on the project for an operation of the workspace, where a guest has
// no role; that a user whose effective role on the project is none is told
// that it does not exist; that an instance admin acts as an owner only where
// the policy lets them; and that a refused batch changes nothing.
func TestAuthorisation(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	p := w.pipeline(t, nil)
	x, xID := newNote()
	if _, err := p.Apply(ctx, w.as(ownerUser), w.project,
		[]Command{command(t, noteCreate, notePayload{ID: xID, Body: "x"}, nil)}); err != nil {
		t.Fatal(err)
	}
	access := w.pipeline(t, func(o *Options) { o.Policy = role.Policy{InstanceAdminAccess: true} })
	tests := []struct {
		user string
		p    *Pipeline
		// create and delete are what a batch that creates a note, and one
		// that deletes x, which needs owner or admin, end with.
		create, delete error
	}{
		{user: ownerUser, p: p},
		{user: adminUser, p: p},
		{user: editorUser, p: p, delete: ErrInsufficientRole},
		{user: commenterUser, p: p, delete: ErrInsufficientRole},
		{user: viewerUser, p: p, delete: ErrInsufficientRole},
		// A grant gives a role on the project, but none in the workspace.
		{user: grantedViewerUser, p: p, delete: ErrInsufficientRole},
		{user: grantedGuestUser, p: p, create: ErrInsufficientRole, delete: ErrInsufficientRole},
		{user: guestUser, p: p, create: ErrNotFound, delete: ErrNotFound},
		{user: outsiderUser, p: p, create: ErrNotFound, delete: ErrNotFound},
		{user: instanceAdminUser, p: p, create: ErrNotFound, delete: ErrNotFound},
		{user: instanceAdminUser, p: access},
	}
	for _, tt := range tests {
		name := tt.user
		if tt.p == access {
			name += " with access"
		}
		t.Run(name, func(t *testing.T) {
			before := w.written(t)
			_, id := newNote()
			_, err := tt.p.Apply(ctx, w.as(tt.user), w.project,
				[]Command{command(t, noteCreate, notePayload{ID: id, Body: "made"}, nil)})
			if !errors.Is(err, tt.create) || (tt.create == nil) != (err == nil) {
				t.Errorf("creating a note: %v, want %v", err, tt.create)
			}
			if tt.create != nil {
				if after := w.written(t); after != before {
					t.Errorf("the refused batch wrote %+v, where there was %+v", after, before)
				}
			}
			before = w.written(t)
			_, err = tt.p.Apply(ctx, w.as(tt.user), w.project, []Command{command(t, noteDelete,
				noteIDPayload{ID: xID}, nil), command(t, noteUpdate, notePayload{ID: xID, Body: "undeleted"}, nil)})
			if !errors.Is(err, tt.delete) || (tt.delete == nil) != (err == nil) {
				t.Errorf("deleting the note: %v, want %v", err, tt.delete)
			}
			if tt.delete != nil {
				if after := w.written(t); after != before {
					t.Errorf("the refused batch wrote %+v, where there was %+v", after, before)
				}
			} else {
				// The note is back for the next user to delete.
				w.exec(t, "UPDATE note SET deleted_at = NULL WHERE id = $1", x)
			}
		})
	}
}

// TestTokenBatch checks that a batch applied with an API token is held to the
// token (PRD FR-AUTH-04, §5.6): one of another workspace, or narrowed to
// another project, is told that the project does not exist (AC3), one
// without the scope of each of the batch's commands, project:write for the
// notes, snapshot:write for publishing, is refused with ErrInsufficientScope,
// which names the scope that it lacks, whatever its owner's role (AC2), and
// one that holds them does no more than its owner's role allows; and that a
// refused batch changes nothing.
//
// Verifies: FR-AUTH-04/AC2, FR-AUTH-04/AC3
func TestTokenBatch(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	p := w.pipeline(t, nil)
	x, xID := newNote()
	if _, err := p.Apply(ctx, w.as(ownerUser), w.project,
		[]Command{command(t, noteCreate, notePayload{ID: xID, Body: "x"}, nil)}); err != nil {
		t.Fatal(err)
	}
	write := []scope.Scope{scope.ProjectWrite}
	for _, tt := range []struct {
		name  string
		user  string
		token scope.Token
		// create and delete are what a batch that creates a note, and one
		// that deletes x, which needs owner or admin, end with.
		create, delete error
	}{
		{name: "the owner's token of the workspace", user: ownerUser,
			token: scope.Token{WorkspaceID: w.ws, Scopes: write}},
		{name: "the owner's token narrowed to the project", user: ownerUser,
			token: scope.Token{WorkspaceID: w.ws, ProjectID: w.project, Scopes: write}},
		{name: "every scope but project:write", user: ownerUser,
			token: scope.Token{WorkspaceID: w.ws, Scopes: []scope.Scope{scope.ProjectRead, scope.SnapshotRead,
				scope.SnapshotWrite, scope.ExportRead}},
			create: ErrInsufficientScope, delete: ErrInsufficientScope},
		{name: "narrowed to another project", user: ownerUser,
			token:  scope.Token{WorkspaceID: w.ws, ProjectID: ids.New(), Scopes: write},
			create: ErrNotFound, delete: ErrNotFound},
		{name: "of another workspace", user: ownerUser, token: scope.Token{WorkspaceID: ids.New(), Scopes: write},
			create: ErrNotFound, delete: ErrNotFound},
		{name: "an editor's token", user: editorUser, token: scope.Token{WorkspaceID: w.ws, Scopes: write},
			delete: ErrInsufficientRole},
		{name: "a viewer's token without project:write", user: viewerUser,
			token:  scope.Token{WorkspaceID: w.ws, Scopes: []scope.Scope{scope.ProjectRead}},
			create: ErrInsufficientScope, delete: ErrInsufficientScope},
		{name: "a guest's token", user: guestUser, token: scope.Token{WorkspaceID: w.ws, Scopes: write},
			create: ErrNotFound, delete: ErrNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tt.token.ID = ids.New()
			actor := Actor{UserID: w.users[tt.user], Token: &tt.token}
			before := w.written(t)
			_, id := newNote()
			_, err := p.Apply(ctx, actor, w.project, []Command{command(t, noteCreate,
				notePayload{ID: id, Body: "made"}, nil)})
			if !errors.Is(err, tt.create) || (tt.create == nil) != (err == nil) {
				t.Errorf("creating a note: %v, want %v", err, tt.create)
			}
			if tt.create != nil {
				if after := w.written(t); after != before {
					t.Errorf("the refused batch wrote %+v, where there was %+v", after, before)
				}
			}
			before = w.written(t)
			_, err = p.Apply(ctx, actor, w.project, []Command{command(t, noteDelete, noteIDPayload{ID: xID}, nil),
				command(t, noteUpdate, notePayload{ID: xID, Body: "undeleted"}, nil)})
			if !errors.Is(err, tt.delete) || (tt.delete == nil) != (err == nil) {
				t.Errorf("deleting the note: %v, want %v", err, tt.delete)
			}
			if tt.delete != nil {
				if after := w.written(t); after != before {
					t.Errorf("the refused batch wrote %+v, where there was %+v", after, before)
				}
			} else {
				w.exec(t, "UPDATE note SET deleted_at = NULL WHERE id = $1", x)
			}
		})
	}

	// Each command needs its own scope, which another does not imply.
	for _, tt := range []struct {
		held []scope.Scope
		// lacks is the scope that the batch is refused for, if any.
		lacks scope.Scope
	}{
		{write, scope.SnapshotWrite},
		{[]scope.Scope{scope.SnapshotWrite}, scope.ProjectWrite},
		{[]scope.Scope{scope.ProjectWrite, scope.SnapshotWrite}, ""},
	} {
		actor := Actor{UserID: w.users[ownerUser], Token: &scope.Token{ID: ids.New(), WorkspaceID: w.ws,
			Scopes: tt.held}}
		before := w.written(t)
		_, err := p.Apply(ctx, actor, w.project, []Command{
			command(t, noteUpdate, notePayload{ID: xID, Body: "published"}, nil),
			command(t, notePublish, noteIDPayload{ID: xID}, nil)})
		var insufficient *scope.InsufficientError
		switch {
		case tt.lacks == "" && err != nil:
			t.Errorf("publishing with %v: %v", tt.held, err)
		case tt.lacks == "":
		case !errors.As(err, &insufficient) || !errors.Is(err, ErrInsufficientScope) || insufficient.Scope != tt.lacks:
			t.Errorf("publishing with %v: %v, want it refused for want of %s", tt.held, err, tt.lacks)
		default:
			if after := w.written(t); after != before {
				t.Errorf("the batch refused for want of %s wrote %+v, where there was %+v", tt.lacks, after, before)
			}
		}
	}
}

// TestRoles checks that the roles that authorise a batch are the actor's
// effective role on the project, which a grant replaces, and their role in
// the workspace, which no grant changes (PRD §5.5).
func TestRoles(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	p := w.pipeline(t, nil)
	access := w.pipeline(t, func(o *Options) { o.Policy = role.Policy{InstanceAdminAccess: true} })
	for _, tt := range []struct {
		user                 string
		p                    *Pipeline
		project, inWorkspace role.Role
	}{
		{user: ownerUser, p: p, project: role.Owner, inWorkspace: role.Owner},
		{user: editorUser, p: p, project: role.Editor, inWorkspace: role.Editor},
		{user: grantedViewerUser, p: p, project: role.Editor, inWorkspace: role.Viewer},
		{user: grantedGuestUser, p: p, project: role.Editor, inWorkspace: role.None},
		{user: guestUser, p: p},
		{user: outsiderUser, p: p},
		{user: instanceAdminUser, p: p},
		{user: instanceAdminUser, p: access, project: role.Owner, inWorkspace: role.Owner},
	} {
		var got map[authz.Scope]role.Role
		err := w.db.InTx(ctx, database.Scope{WorkspaceID: w.ws, UserID: w.users[tt.user]}, func(tx pgx.Tx) error {
			var err error
			got, err = tt.p.roles(ctx, queries.New(tx), w.users[tt.user], w.ws, w.project)
			return err
		})
		if tt.project == "" {
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("%s: %v, %v, want ErrNotFound", tt.user, got, err)
			}
			continue
		}
		if err != nil || got[authz.Project] != tt.project || got[authz.Workspace] != tt.inWorkspace {
			t.Errorf("%s: %v, %v, want %s on the project and %s in the workspace", tt.user, got, err, tt.project,
				tt.inWorkspace)
		}
	}
}

// TestNotFound checks that a batch of a project that does not exist, of
// another workspace's project, or of a project of a workspace whose deletion
// is pending, which blocks every access to it at once, its owners' too
// (FR-TEN-05 AC1), is told that the project does not exist, even when the
// deletion is requested once the batch has begun: between its two
// transactions, while it waits for the project's lock, or once it holds the
// lock, before a read that only an active workspace answers, as of its plan.
func TestNotFound(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	p := w.pipeline(t, nil)
	other := ids.New()
	otherProject := ids.New()
	w.exec(t, "INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Other', $2, 'unlimited')", other,
		"other-"+other.String()[:8])
	w.exec(t, "INSERT INTO project (id, workspace_id, name) VALUES ($1, $2, 'Harbour')", otherProject, other)
	deleting, deletingProject := ids.New(), ids.New()
	w.exec(t, "INSERT INTO workspace (id, name, slug, plan_key, status, deleted_at) "+
		"VALUES ($1, 'Deleting', $2, 'unlimited', 'pending_deletion', now())", deleting, "deleting-"+deleting.String()[:8])
	w.exec(t, "INSERT INTO project (id, workspace_id, name) VALUES ($1, $2, 'Harbour')", deletingProject, deleting)
	w.exec(t, "INSERT INTO membership (id, workspace_id, user_id, role) VALUES ($1, $2, $3, 'owner')", ids.New(),
		deleting, w.users[ownerUser])
	for name, project := range map[string]ids.UUID{"no project": ids.New(), "no ID": {},
		"another workspace's project": otherProject, "a project of a workspace being deleted": deletingProject} {
		_, id := newNote()
		_, err := p.Apply(ctx, w.as(ownerUser), project,
			[]Command{command(t, noteCreate, notePayload{ID: id, Body: "lost"}, nil)})
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v, want ErrNotFound", name, err)
		}
	}

	// A deletion requested once the pipeline has found the project's
	// workspace, before the batch's transaction begins, refuses the batch
	// too, rather than letting it apply, or, with plans enforced, fail on the
	// read of the plan of a workspace that is no longer active.
	late, lateProject := ids.New(), ids.New()
	w.exec(t, "INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Late', $2, 'unlimited')", late,
		"late-"+late.String()[:8])
	w.exec(t, "INSERT INTO project (id, workspace_id, name) VALUES ($1, $2, 'Harbour')", lateProject, late)
	w.exec(t, "INSERT INTO membership (id, workspace_id, user_id, role) VALUES ($1, $2, $3, 'owner')", ids.New(), late,
		w.users[ownerUser])
	db := &betweenDB{DB: w.db, between: func() {
		w.exec(t, "UPDATE workspace SET status = 'pending_deletion', deleted_at = now() WHERE id = $1", late)
	}}
	lately := New(db, Options{Registry: noteRegistry(), Jobs: w.queue,
		Entitlements: entitlements.New(entitlements.Options{Enforce: true, Meter: fixedMeter{}})})
	_, id := newNote()
	if _, err := lately.Apply(ctx, w.as(ownerUser), lateProject,
		[]Command{command(t, noteCreate, notePayload{ID: id, Body: "late"}, nil)}); !errors.Is(err, ErrNotFound) {
		t.Errorf("a deletion requested meanwhile: %v, want ErrNotFound", err)
	}
	if db.calls != 2 {
		t.Errorf("the pipeline opened %d transactions, want 2", db.calls)
	}

	// So does a deletion requested while the batch waits for the project's
	// lock, after its transaction has read the workspace and the roles, even
	// with the project archived meanwhile, which would refuse the batch with
	// ErrProjectArchived in an active workspace.
	waiting, waitingProject := ids.New(), ids.New()
	w.exec(t, "INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Waiting', $2, 'unlimited')", waiting,
		"waiting-"+waiting.String()[:8])
	w.exec(t, "INSERT INTO project (id, workspace_id, name) VALUES ($1, $2, 'Harbour')", waitingProject, waiting)
	w.exec(t, "INSERT INTO membership (id, workspace_id, user_id, role) VALUES ($1, $2, $3, 'owner')", ids.New(),
		waiting, w.users[ownerUser])
	holder := w.d.Admin(t)
	hold := func(sql string, args ...any) {
		t.Helper()
		if _, err := holder.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	hold("BEGIN")
	hold("SELECT seq FROM project WHERE id = $1 FOR NO KEY UPDATE", waitingProject)
	_, id = newNote()
	batch := []Command{command(t, noteCreate, notePayload{ID: id, Body: "waited"}, nil)}
	done := make(chan error, 1)
	go func() {
		_, err := p.Apply(ctx, w.as(ownerUser), waitingProject, batch)
		done <- err
	}()
	if !w.waitsForALock(t) {
		hold("ROLLBACK")
		t.Fatalf("the batch does not wait for the project's lock: %v", <-done)
	}
	hold("UPDATE workspace SET status = 'pending_deletion', deleted_at = now() WHERE id = $1", waiting)
	hold("UPDATE project SET archived_at = now() WHERE id = $1", waitingProject)
	hold("COMMIT")
	if err := <-done; !errors.Is(err, ErrNotFound) {
		t.Errorf("a deletion requested while the batch waits for the lock: %v, want ErrNotFound", err)
	}

	// And so does a deletion requested once the batch, which holds the lock,
	// has read the workspace's status again, right before it reads the plan,
	// which only an active workspace has.
	planned, plannedProject := ids.New(), ids.New()
	w.exec(t, "INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Planned', $2, 'unlimited')", planned,
		"planned-"+planned.String()[:8])
	w.exec(t, "INSERT INTO project (id, workspace_id, name) VALUES ($1, $2, 'Harbour')", plannedProject, planned)
	w.exec(t, "INSERT INTO membership (id, workspace_id, user_id, role) VALUES ($1, $2, $3, 'owner')", ids.New(),
		planned, w.users[ownerUser])
	read := false
	before := &beforeDB{DB: w.db, before: func(sql string) {
		if !read && strings.Contains(sql, "SELECT plan_key FROM workspace") {
			read = true
			w.exec(t, "UPDATE workspace SET status = 'pending_deletion', deleted_at = now() WHERE id = $1", planned)
		}
	}}
	enforced := New(before, Options{Registry: noteRegistry(), Jobs: w.queue,
		Entitlements: entitlements.New(entitlements.Options{Enforce: true, Meter: fixedMeter{}})})
	_, id = newNote()
	if _, err := enforced.Apply(ctx, w.as(ownerUser), plannedProject,
		[]Command{command(t, noteCreate, notePayload{ID: id, Body: "planned"}, nil)}); !errors.Is(err, ErrNotFound) {
		t.Errorf("a deletion requested right before the read of the plan: %v, want ErrNotFound", err)
	}
	if !read {
		t.Error("the batch did not read the workspace's plan")
	}
	if got := w.written(t); got != (written{}) {
		t.Errorf("the refused batches wrote %+v", got)
	}
}

// TestProjectStates checks the batches of an archived project and of one in
// the trash (FR-PROJ-01): an archived project refuses every batch with a
// command that does not apply to an archived project with
// ErrProjectArchived, to a role that its operation allows, and a project in
// the trash refuses every batch with a command that does not apply there as
// one that does not exist, whatever the actor's role, so that a refusal of
// the role does not tell that it exists, but either applies a batch with a
// command that unarchives the project, or restores it, whatever its other
// commands; a project whose time in the trash has passed refuses every batch
// as one that does not exist; and a batch that waits for the lock of
// a project that another transaction archives, or deletes to the trash,
// meanwhile is refused too.
//
// Verifies: FR-PROJ-01/AC2
func TestProjectStates(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	p := w.pipeline(t, nil)
	_, note := newNote()
	if _, err := p.Apply(ctx, w.as(ownerUser), w.project,
		[]Command{command(t, noteCreate, notePayload{ID: note, Body: "kept"}, nil)}); err != nil {
		t.Fatal(err)
	}
	update := command(t, noteUpdate, notePayload{ID: note, Body: "changed"}, nil)
	touch := command(t, noteTouch, noteIDPayload{ID: note}, nil)
	revive := command(t, noteRevive, noteIDPayload{ID: note}, nil)
	remove := command(t, noteDelete, noteIDPayload{ID: note}, nil)
	try := func(what, user string, want error, cmds ...Command) {
		t.Helper()
		for i := range cmds {
			cmds[i].ID = ids.New()
		}
		if _, err := p.Apply(ctx, w.as(user), w.project, cmds); !errors.Is(err, want) {
			t.Errorf("%s, as %s: %v, want %v", what, user, err, want)
		}
	}

	w.exec(t, "UPDATE project SET archived_at = now() WHERE id = $1", w.project)
	before := w.written(t)
	try("change an archived project", ownerUser, ErrProjectArchived, update)
	try("change an archived project", viewerUser, ErrProjectArchived, update)
	try("delete a note of an archived project", viewerUser, ErrInsufficientRole, remove)
	try("touch and change an archived project", ownerUser, ErrProjectArchived, touch, update)
	if after := w.written(t); after != before {
		t.Errorf("the refused batches wrote %+v, where there was %+v", after, before)
	}
	try("touch an archived project", viewerUser, nil, touch)
	// A batch that unarchives the project applies whatever its other
	// commands, as the undo of one that archived it with them does.
	try("change and unarchive an archived project", ownerUser, nil, update,
		command(t, noteUnarchive, noteIDPayload{ID: note}, nil))
	if after := w.written(t); after.seq != before.seq+2 {
		t.Errorf("the batches of an archived project wrote %+v, where there was %+v: want two, the touch and the "+
			"change", after, before)
	}

	w.exec(t, "UPDATE project SET deleted_at = now(), hard_delete_after = now() + interval '1 day' WHERE id = $1",
		w.project)
	before = w.written(t)
	try("change a project in the trash", ownerUser, ErrNotFound, update)
	try("touch a project in the trash", ownerUser, ErrNotFound, touch)
	try("delete a note of a project in the trash", viewerUser, ErrNotFound, remove)
	try("revive and change a project in the trash", ownerUser, ErrNotFound, revive, update)
	if after := w.written(t); after != before {
		t.Errorf("the refused batches wrote %+v, where there was %+v", after, before)
	}
	try("revive a project in the trash", ownerUser, nil, revive)
	try("revive a project in the trash", outsiderUser, ErrNotFound, revive)
	// A batch that restores the project applies whatever its other commands,
	// as the undo of one that deleted it with them does, but for the
	// project's archival: this one is archived too, so a batch that changes
	// it also unarchives it.
	restore := command(t, noteUntrash, noteIDPayload{ID: note}, nil)
	unarchive := command(t, noteUnarchive, noteIDPayload{ID: note}, nil)
	try("change and restore an archived project in the trash", ownerUser, ErrProjectArchived, update, restore)
	try("change, restore, and unarchive an archived project in the trash", outsiderUser, ErrNotFound, update,
		restore, unarchive)
	try("change, restore, and unarchive an archived project in the trash", ownerUser, nil, update, restore,
		unarchive)

	w.exec(t, "UPDATE project SET hard_delete_after = now() - interval '1 second' WHERE id = $1", w.project)
	try("revive a project whose time in the trash has passed", ownerUser, ErrNotFound, revive)

	// A project that another transaction archives, or deletes to the trash,
	// while a batch waits for its lock refuses the batch, as the project's
	// state once the batch holds the lock says.
	holder := w.d.Admin(t)
	hold := func(sql string, args ...any) {
		t.Helper()
		if _, err := holder.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	for _, c := range []struct {
		what, change string
		want         error
	}{
		{"archived", "archived_at = now()", ErrProjectArchived},
		{"deleted to the trash", "deleted_at = now(), hard_delete_after = now() + interval '1 day'", ErrNotFound},
	} {
		w.exec(t, "UPDATE project SET archived_at = NULL, deleted_at = NULL, hard_delete_after = NULL "+
			"WHERE id = $1", w.project)
		hold("BEGIN")
		hold("UPDATE project SET "+c.change+" WHERE id = $1", w.project)
		done := make(chan error, 1)
		batch := []Command{update}
		batch[0].ID = ids.New()
		go func() {
			_, err := p.Apply(ctx, w.as(ownerUser), w.project, batch)
			done <- err
		}()
		if !w.waitsForALock(t) {
			hold("ROLLBACK")
			t.Fatalf("the batch does not wait for the project's lock: %v", <-done)
		}
		hold("COMMIT")
		if err := <-done; !errors.Is(err, c.want) {
			t.Errorf("a batch that waited while the project was %s: %v, want %v", c.what, err, c.want)
		}
	}
}

// TestRolesAreNotLocked checks that a batch locks neither its actor's
// membership nor their grant on the project: removing the member, which
// locks both FOR UPDATE, does not wait for the member's batch, even while the
// batch waits for the project's lock, where a lock FOR SHARE would let the
// member's overlapping batches put the removal off for as long as they kept
// coming. The batch, which its roles authorised before the removal, still
// applies, ordered before it, and the member's next batch is told that the
// project does not exist.
func TestRolesAreNotLocked(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	p := w.pipeline(t, nil)
	holder, remover := w.d.Admin(t), w.d.Admin(t)
	run := func(c *pgx.Conn, sql string, args ...any) error {
		_, err := c.Exec(ctx, sql, args...)
		return err
	}
	if err := run(holder, "BEGIN"); err != nil {
		t.Fatal(err)
	}
	if err := run(holder, "SELECT seq FROM project WHERE id = $1 FOR NO KEY UPDATE", w.project); err != nil {
		t.Fatal(err)
	}
	// The granted viewer has a membership and a grant, which the batch reads.
	_, id := newNote()
	batch := []Command{command(t, noteCreate, notePayload{ID: id, Body: "in flight"}, nil)}
	done := make(chan error, 1)
	go func() {
		_, err := p.Apply(ctx, w.as(grantedViewerUser), w.project, batch)
		done <- err
	}()
	if !w.waitsForALock(t) {
		_ = run(holder, "ROLLBACK")
		t.Fatalf("the batch does not wait for the project's lock: %v", <-done)
	}
	// Deleting the membership deletes the grant through its foreign key.
	err := run(remover, "SET lock_timeout = '5s'")
	if err == nil {
		err = run(remover, "DELETE FROM membership WHERE workspace_id = $1 AND user_id = $2", w.ws,
			w.users[grantedViewerUser])
	}
	if commit := run(holder, "COMMIT"); commit != nil {
		t.Fatal(commit)
	}
	if err != nil {
		t.Errorf("removing the member while their batch waits for the project's lock: %v", err)
	}
	if err := <-done; err != nil {
		t.Errorf("the batch that the member's roles authorised before the removal: %v", err)
	}
	_, id = newNote()
	if _, err := p.Apply(ctx, w.as(grantedViewerUser), w.project,
		[]Command{command(t, noteCreate, notePayload{ID: id, Body: "removed"}, nil)}); !errors.Is(err, ErrNotFound) {
		t.Errorf("a batch of the removed member: %v, want ErrNotFound", err)
	}
}

// TestIsolation tries the pipeline's queries on another workspace's rows,
// from a transaction of one workspace, where row-level security must keep
// them from reading its project grants, from locking its projects or setting
// their seqs, and from adding to its operation log and its applied commands
// (NFR-SEC-01). The membership is read with MembershipOf, a query of package
// workspace, whose isolation test tries it.
//
// Verifies: NFR-SEC-01
func TestIsolation(t *testing.T) {
	t.Parallel()
	f := isolationtest.New(t, dbtest.New(t), isolationtest.Tables)
	ctx := context.Background()
	// column reads, as the superuser, a column of the row of a table that
	// the fixture added to the workspace.
	column := func(w *isolationtest.Workspace, table, name string) ids.UUID {
		var id ids.UUID
		if err := f.Admin().QueryRow(ctx, "SELECT "+name+" FROM "+table+" WHERE id = $1", w.Rows[table]).
			Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	found := func(id ids.UUID, err error) ([]ids.UUID, error) {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return []ids.UUID{id}, err
	}
	f.Check(t, isolationtest.Op{
		Name: "a project grant's role",
		Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) ([]ids.UUID, error) {
			_, err := queries.New(tx).ProjectGrantRole(ctx, queries.ProjectGrantRoleParams{
				WorkspaceID: nullUUID(target.ID), MembershipID: nullUUID(target.Rows["membership"]),
				ProjectID: nullUUID(column(target, "project_grant", "project_id"))})
			return found(target.Rows["project_grant"], err)
		},
	})
	f.Check(t, isolationtest.Op{
		Name: "a project's seq, locked",
		Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) ([]ids.UUID, error) {
			_, err := queries.New(tx).LockProjectSeq(ctx, queries.LockProjectSeqParams{
				WorkspaceID: nullUUID(target.ID), ID: nullUUID(target.Rows["project"])})
			return found(target.Rows["project"], err)
		},
	})
	f.Check(t, isolationtest.Op{
		Name:  "a project's new seq",
		Blind: true,
		Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) ([]ids.UUID, error) {
			return nil, queries.New(tx).SetProjectSeq(ctx, queries.SetProjectSeqParams{Seq: 1,
				WorkspaceID: nullUUID(target.ID), ID: nullUUID(target.Rows["project"])})
		},
	})
	f.Check(t, isolationtest.Op{
		Name:   "a new operation",
		Denied: true,
		Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) ([]ids.UUID, error) {
			id := ids.New()
			_, err := queries.New(tx).InsertOperation(ctx, queries.InsertOperationParams{ID: nullUUID(id),
				WorkspaceID: nullUUID(target.ID), ProjectID: nullUUID(target.Rows["project"]), Seq: 1,
				CommandIds: []pgtype.UUID{nullUUID(ids.New())}, ActorUserID: nullUUID(target.UserID),
				Type: string(noteCreate), Payload: []byte("[]"), EntityRefs: []byte("[]")})
			return []ids.UUID{id}, err
		},
	})
	f.Check(t, isolationtest.Op{
		Name:   "a new applied command",
		Denied: true,
		Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) ([]ids.UUID, error) {
			id := ids.New()
			return []ids.UUID{id}, queries.New(tx).InsertAppliedCommands(ctx, queries.InsertAppliedCommandsParams{
				WorkspaceID: nullUUID(target.ID), ProjectID: nullUUID(target.Rows["project"]), Seq: 1,
				CommandIds: []pgtype.UUID{nullUUID(id)}})
		},
	})
}

// TestLimits checks that the limits of the workspace's plan are checked for
// what the whole batch adds, so that a batch whose commands would each fit,
// but not all of them, is refused, and changes nothing.
func TestLimits(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	p := w.pipeline(t, func(o *Options) {
		o.Entitlements = entitlements.New(entitlements.Options{Caps: entitlements.Caps{entitlements.Projects: 3},
			Meter: fixedMeter{entitlements.Projects: 1}})
	})
	creates := func(n int) []Command {
		var cmds []Command
		for range n {
			_, id := newNote()
			cmds = append(cmds, command(t, noteCreate, notePayload{ID: id, Body: "counted"}, nil))
		}
		return cmds
	}
	if _, err := p.Apply(ctx, w.as(ownerUser), w.project, creates(2)); err != nil {
		t.Fatalf("a batch that reaches the limit: %v", err)
	}
	before := w.written(t)
	_, err := p.Apply(ctx, w.as(ownerUser), w.project, creates(3))
	var limit *entitlements.LimitError
	if !errors.As(err, &limit) || limit.Key != entitlements.Projects || limit.Usage != 1 || limit.Limit != 3 {
		t.Fatalf("a batch past the limit: %v, want a limit error of 1 of 3 projects", err)
	}
	if after := w.written(t); after != before {
		t.Errorf("the refused batch wrote %+v, where there was %+v", after, before)
	}
}

// TestBatchAdds checks that what a batch's commands add to a limit is summed
// without wrapping around: a sum past what an int64 holds stays at
// math.MaxInt64, whatever the commands after it take away, so that the limit
// still refuses it, rather than wrap around to an amount that adds nothing.
func TestBatchAdds(t *testing.T) {
	projects := &spec{limits: []entitlements.LimitKey{entitlements.Projects}}
	both := &spec{limits: []entitlements.LimitKey{entitlements.StorageBytes, entitlements.Projects}}
	// adding is a command of the spec s that adds adds to its limits.
	type adding struct {
		s    *spec
		adds []int64
	}
	for _, tt := range []struct {
		name string
		cmds []adding
		keys []entitlements.LimitKey
		want map[entitlements.LimitKey]int64
	}{
		{"none", []adding{{&spec{}, nil}}, nil, map[entitlements.LimitKey]int64{}},
		{"summed", []adding{{projects, []int64{2}}, {both, []int64{5, 3}}, {projects, []int64{-1}}},
			[]entitlements.LimitKey{entitlements.Projects, entitlements.StorageBytes},
			map[entitlements.LimitKey]int64{entitlements.Projects: 4, entitlements.StorageBytes: 5}},
		{"past the most", []adding{{projects, []int64{math.MaxInt64 - 1}}, {projects, []int64{2}},
			{projects, []int64{-5}}},
			[]entitlements.LimitKey{entitlements.Projects},
			map[entitlements.LimitKey]int64{entitlements.Projects: math.MaxInt64}},
		{"past the least", []adding{{projects, []int64{math.MinInt64 + 1}}, {projects, []int64{-2}},
			{projects, []int64{3}}},
			[]entitlements.LimitKey{entitlements.Projects},
			map[entitlements.LimitKey]int64{entitlements.Projects: math.MinInt64 + 3}},
	} {
		cmds, adds := make([]decoded, len(tt.cmds)), make([][]int64, len(tt.cmds))
		for i, c := range tt.cmds {
			cmds[i], adds[i] = decoded{spec: c.s}, c.adds
		}
		keys, sums := batchAdds(cmds, adds)
		if !slices.Equal(keys, tt.keys) || !maps.Equal(sums, tt.want) {
			t.Errorf("%s: %v, %v, want %v, %v", tt.name, keys, sums, tt.keys, tt.want)
		}
	}
}

// TestUndo checks that a batch with a command that cannot be undone has no
// inverse, for which none of its commands' inverses is computed, and that a
// command whose inverse could not be applied, with a step of a type that is
// not registered, or whose payload its schema or its Check refuses, refuses a
// batch that can be undone, with an error that does not pass for a problem of
// the batch's payloads, which are valid.
func TestUndo(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	p := w.pipeline(t, nil)
	x, xID := newNote()
	if _, err := p.Apply(ctx, w.as(ownerUser), w.project, []Command{
		command(t, noteCreate, notePayload{ID: xID, Body: "x"}, nil),
		command(t, noteDelete, noteIDPayload{ID: xID}, nil),
	}); err != nil {
		t.Fatal(err)
	}
	if o := w.operation(t, 1); o.inverse != nil {
		t.Errorf("a batch that cannot be undone has the inverse %s", o.inverse)
	}
	before := w.written(t)
	for _, step := range []Type{"note.unknown", noteCreate, noteUpdate} {
		_, err := p.Apply(ctx, w.as(ownerUser), w.project,
			[]Command{command(t, noteBadInverse, badInversePayload{ID: xID, Step: step}, map[Ref]int64{noteRef(x): 2})})
		var fields input.FieldErrors
		switch {
		case err == nil:
			t.Errorf("a command undone by %s applies", step)
		case errors.As(err, &fields):
			t.Errorf("a command undone by %s is refused with the problems %v, as though the batch were not valid",
				step, fields)
		}
	}
	if after := w.written(t); after != before {
		t.Errorf("the refused batches wrote %+v, where there was %+v", after, before)
	}

	// The batch cannot be undone, so the inverse that could not be applied
	// is not computed, and does not refuse it.
	if _, err := p.Apply(ctx, w.as(ownerUser), w.project, []Command{
		command(t, noteBadInverse, badInversePayload{ID: xID, Step: "note.unknown"}, nil),
		command(t, noteDelete, noteIDPayload{ID: xID}, nil),
	}); err != nil {
		t.Fatalf("a batch that cannot be undone, with an inverse that could not be applied: %v", err)
	}
	if o := w.operation(t, 2); o.inverse != nil {
		t.Errorf("a batch that cannot be undone has the inverse %s", o.inverse)
	}
}

// TestUndoBound checks that the pipeline refuses a command whose inverse
// would take its batch's undo past the pipeline's bound, the payloads of its
// commands' inverses' steps as JSON encodes them for the operation log,
// escapes and all, and the revisions that they expect, at the field that its
// type's UndoField names, or at the payload, though the command's own inverse
// fits, and that it refuses one whose Inverse stops with ErrUndoTooLarge
// alike, once Tx.UndoRoom tells it the room that the inverses of the commands
// before it leave, with what they expect, that a batch whose undo takes the
// bound exactly applies, and that the batch that undoes another is bounded
// alike, by its own inverse.
func TestUndoBound(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	x, xID := newNote()
	_, yID := newNote()
	// The log escapes <, as \u003c, which then takes six bytes.
	body := strings.Repeat("<", 50)
	if _, err := w.pipeline(t, nil).Apply(ctx, w.as(ownerUser), w.project, []Command{
		command(t, noteCreate, notePayload{ID: xID, Body: body}, nil),
		command(t, noteCreate, notePayload{ID: yID, Body: body}, nil),
	}); err != nil {
		t.Fatal(err)
	}
	bounded := func(n int) *Pipeline { return w.pipeline(t, func(o *Options) { o.MaxUndoBytes = n }) }
	update := func(id string) Command { return command(t, noteUpdate, notePayload{ID: id, Body: "y"}, nil) }
	undo, err := json.Marshal(notePayload{ID: xID, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	one := len(undo)
	before := w.written(t)
	var r *RefusedError
	if _, err := bounded(2*one-1).Apply(ctx, w.as(ownerUser), w.project, []Command{update(xID),
		update(yID)}); !errors.As(err, &r) || r.Command != 1 || r.Field != "body" || r.Reason != nil {
		t.Errorf("a batch whose undo would take the bound by a byte: %v, want a refusal of command 1 at body", err)
	}
	room := func(n int) Command {
		return command(t, noteBadInverse, badInversePayload{ID: xID, Step: noteDelete, Room: n}, nil)
	}
	deletion, err := json.Marshal(noteIDPayload{ID: xID})
	if err != nil {
		t.Fatal(err)
	}
	// The update's undo expects the note's revision, which the bound counts
	// once the update has applied, before the next command's inverse.
	expected := ExpectedRevBytes(noteRef(x))
	if _, err := bounded(one+expected-1).Apply(ctx, w.as(ownerUser), w.project,
		[]Command{update(xID)}); !errors.As(err, &r) || r.Command != 0 || r.Field != "body" {
		t.Errorf("a batch whose undo's expected revision would take the bound by a byte: %v, want a refusal of "+
			"command 0 at body", err)
	}
	if _, err := bounded(one+expected+len(deletion)).Apply(ctx, w.as(ownerUser), w.project, []Command{update(xID),
		room(len(deletion) + 1)}); !errors.As(err, &r) || r.Command != 1 || r.Field != "" {
		t.Errorf("a batch whose second command's inverse stops for want of room: %v, want a refusal of command 1 "+
			"at the payload", err)
	}
	if after := w.written(t); after != before {
		t.Errorf("the refused batches wrote %+v, where there was %+v", after, before)
	}
	res, err := bounded(one+expected+len(deletion)).Apply(ctx, w.as(ownerUser), w.project,
		[]Command{update(xID), room(len(deletion))})
	if err != nil {
		t.Fatalf("a batch whose undo takes the bound exactly: %v", err)
	}
	// The second command changed nothing, so the undo holds none of its
	// steps, which the bound counted all the same while it applied
	// (TestUndoOfNoChange).
	if o := w.operation(t, res.Seq); !sameJSON(t, o.inverse, `[{"type": "note.update", "payload": {"id": "`+xID+
		`", "body": "`+body+`"}, "expected_rev": {"`+xID+`": 2}}]`) {
		t.Errorf("the batch's inverse is %s", o.inverse)
	}

	// The batch that undoes another is bounded alike, by its own inverse,
	// which writes again what the other wrote: a change whose payload passes
	// the bound, over a small value, applies, but its undo is refused.
	if _, err := w.pipeline(t, nil).Apply(ctx, w.as(ownerUser), w.project, []Command{update(yID)}); err != nil {
		t.Fatal(err)
	}
	if _, err := bounded(one-1).Apply(ctx, w.as(ownerUser), w.project, []Command{
		command(t, noteUpdate, notePayload{ID: yID, Body: body}, nil)}); err != nil {
		t.Fatalf("a change whose undo is small: %v", err)
	}
	if _, err := bounded(one-1).Apply(ctx, w.as(ownerUser), w.project,
		[]Command{update(yID)}); !errors.As(err, &r) || r.Command != 0 || r.Field != "body" {
		t.Errorf("the undo of a change whose payload passes the bound: %v, want a refusal of command 0 at body", err)
	}
}

// TestUndoOfAStoredValue checks that the pipeline asks a type's CheckNew of
// every command of a batch, which lists its problems after Check's, with the
// batch's others, but not of the step of an inverse, which writes back a
// stored value that its rules may refuse, as a row that the pipeline did not
// write may hold, so that a change of such a value applies, with an inverse
// that the operation log keeps, and the undo that sends the step is refused
// for it, as a batch that is not valid.
func TestUndoOfAStoredValue(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	p := w.pipeline(t, nil)
	x, xID := newNote()
	if _, err := p.Apply(ctx, w.as(ownerUser), w.project,
		[]Command{command(t, noteCreate, notePayload{ID: xID, Body: "x"}, nil)}); err != nil {
		t.Fatal(err)
	}
	_, err := p.Apply(ctx, w.as(ownerUser), w.project, []Command{
		command(t, noteUpdate, notePayload{ID: "nd_nope", Body: "bell\a"}, nil),
		command(t, noteUpdate, notePayload{ID: xID, Body: "y"}, map[Ref]int64{noteRef(x): 0}),
	})
	var invalid *InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("a batch with a body that CheckNew refuses: %v", err)
	}
	var got []string
	for _, pr := range invalid.Problems {
		got = append(got, fmt.Sprintf("%d %s", pr.Command, pr.Field))
	}
	if want := []string{"0 payload.id", "0 payload.body", "1 expected_rev." + ids.Encode(ids.Node, x)}; !slices.Equal(
		got, want) {
		t.Errorf("the batch's problems are at %q, want %q", got, want)
	}

	w.exec(t, "UPDATE note SET body = 'bell' || chr(7) WHERE id = $1", x)
	before := w.written(t)
	res, err := p.Apply(ctx, w.as(ownerUser), w.project,
		[]Command{command(t, noteUpdate, notePayload{ID: xID, Body: "y"}, nil)})
	if err != nil {
		t.Fatalf("a change of a stored body that CheckNew refuses: %v", err)
	}
	if o := w.operation(t, res.Seq); !sameJSON(t, o.inverse,
		`[{"type": "note.update", "payload": {"id": "`+xID+`", "body": "bell\u0007"}, "expected_rev": {"`+xID+
			`": 2}}]`) {
		t.Errorf("the change's inverse is %s", o.inverse)
	}
	var steps []struct {
		Type    Type            `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(w.operation(t, res.Seq).inverse, &steps); err != nil || len(steps) != 1 {
		t.Fatalf("the inverse's steps: %v, %v", steps, err)
	}
	after := w.written(t)
	_, err = p.Apply(ctx, w.as(ownerUser), w.project,
		[]Command{{ID: ids.New(), Type: steps[0].Type, Payload: steps[0].Payload}})
	if !errors.As(err, &invalid) || len(invalid.Problems) != 1 || invalid.Problems[0].Field != "payload.body" {
		t.Errorf("the undo of the change: %v", err)
	}
	if got := w.written(t); got != after || after.seq != before.seq+1 {
		t.Errorf("the change and its refused undo wrote %+v, then %+v, where there was %+v", after, got, before)
	}
}

// TestChanged checks that a command that reports a change of an entity of a
// kind that it does not touch, or a revision that the entity had already, or
// none, refuses its batch, as does one whose inverse reports a change, which
// only Apply may.
func TestChanged(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	p := w.pipeline(t, nil)
	x, xID := newNote()
	if _, err := p.Apply(ctx, w.as(ownerUser), w.project,
		[]Command{command(t, noteCreate, notePayload{ID: xID, Body: "x"}, nil)}); err != nil {
		t.Fatal(err)
	}
	// The batch has seen the note's revision, which it expects.
	for _, mode := range []string{"kind", "stale", "zero"} {
		_, err := p.Apply(ctx, w.as(ownerUser), w.project,
			[]Command{command(t, noteMisreport, misreportPayload{ID: xID, Mode: mode}, map[Ref]int64{noteRef(x): 1})})
		if err == nil {
			t.Errorf("a command that misreports its change (%s) applies", mode)
		}
	}
	// The inverse's step could be applied, so only the change that it
	// reports refuses the batch.
	if _, err := p.Apply(ctx, w.as(ownerUser), w.project, []Command{command(t, noteBadInverse,
		badInversePayload{ID: xID, Step: noteDelete, Changes: true}, map[Ref]int64{noteRef(x): 1})}); err == nil {
		t.Error("a command whose inverse reports a change applies")
	}
	if got := w.written(t); got.seq != 1 {
		t.Errorf("the refused batches left the seq %d", got.seq)
	}
}

// deltas returns the workspace's deltas of usage that no rollup has folded,
// each as its key and its delta.
func (w *world) deltas(t *testing.T) []string {
	t.Helper()
	rows, err := w.admin.Query(context.Background(),
		"SELECT format('%s %s', key, delta) FROM usage_delta WHERE workspace_id = $1 ORDER BY key", w.ws)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// meter returns a command that adds delta to the usage of key.
func meter(t *testing.T, key usage.Key, delta int64) Command {
	t.Helper()
	return command(t, noteMeter, meterPayload{Key: string(key), Delta: delta}, nil)
}

// TestUsage checks that what a batch's commands add to the workspace's usage,
// then or once every command of the batch has applied, is recorded with the
// batch, as one delta of each key that it changes, and not at all for a
// batch that does not apply, and that a command may add to no limit of the
// plans that it does not declare, nor outside its Apply, and a batch no more
// to a limit than its commands declared, which is all that the limit was
// checked for (FR-ENT-02).
func TestUsage(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	p := w.pipeline(t, nil)
	_, id := newNote()
	// undeclared adds delta to the usage of key, declaring that it adds
	// nothing to the projects limit.
	undeclared := func(key usage.Key, delta int64) Command {
		return command(t, noteMeter, meterPayload{Key: string(key), Delta: delta, Undeclared: true}, nil)
	}
	// later adds delta to the usage of key once every command of the batch
	// has applied.
	later := func(key usage.Key, delta int64) Command {
		return command(t, noteMeter, meterPayload{Key: string(key), Delta: delta, Later: true}, nil)
	}
	for _, b := range []struct {
		name  string
		batch []Command
		fails string
	}{
		{"one delta of each key", []Command{meter(t, usage.Nodes, 2), meter(t, usage.Projects, 1),
			meter(t, usage.Nodes, 3), meter(t, usage.StorageBytes, -5)}, ""},
		{"deltas that add up to nothing", []Command{meter(t, usage.Boards, 2), meter(t, usage.Boards, -2)}, ""},
		{"a refused batch", []Command{meter(t, usage.Nodes, 4),
			command(t, noteFail, notePayload{ID: id, Body: "x"}, nil)}, "refuses"},
		{"a limit that the command does not declare", []Command{meter(t, usage.StorageBytes, 1)}, "does not declare"},
		{"a key that is not one", []Command{meter(t, "retired_things", 1)}, "not a key of usage"},
		{"more than 64 bits", []Command{meter(t, usage.Nodes, math.MaxInt64), meter(t, usage.Nodes, 1)},
			"does not fit in 64 bits"},
		{"more to a limit than declared", []Command{meter(t, usage.Projects, 1), undeclared(usage.Projects, 1)},
			"the batch adds 2 to projects, but its commands declared 1"},
		{"to a limit, what the batch takes away", []Command{meter(t, usage.Projects, -1),
			undeclared(usage.Projects, 1)}, ""},
		{"once the batch has applied", []Command{later(usage.Boards, 2), meter(t, usage.Boards, 3)}, ""},
		{"once the batch has applied, to a limit that the command does not declare",
			[]Command{later(usage.StorageBytes, 1)}, "does not declare"},
	} {
		_, err := p.Apply(ctx, w.as(editorUser), w.project, b.batch)
		if b.fails == "" && err != nil || b.fails != "" && (err == nil || !strings.Contains(err.Error(), b.fails)) {
			t.Errorf("%s: %v, want %q", b.name, err, b.fails)
		}
	}
	if got, want := w.deltas(t), []string{"boards 5", "nodes 5", "projects 1", "storage_bytes -5"}; !slices.Equal(got,
		want) {
		t.Errorf("the batches recorded the deltas %q, want %q", got, want)
	}
	if err := (&Tx{}).AddUsage(usage.Nodes, 1); err == nil {
		t.Error("AddUsage outside a command's Apply succeeded")
	}
	if err := (&Tx{}).AddUsageOnceApplied(usage.Nodes, func(context.Context) (int64, error) {
		return 1, nil
	}); err == nil {
		t.Error("AddUsageOnceApplied outside a command's Apply succeeded")
	}
}

// TestEnqueueNeedsAQueue checks that a command that enqueues a job, in a
// pipeline without a job queue, refuses its batch, which then changes
// nothing, rather than apply without the job.
func TestEnqueueNeedsAQueue(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	p := w.pipeline(t, func(o *Options) { o.Jobs = nil })
	_, id := newNote()
	_, err := p.Apply(ctx, w.as(ownerUser), w.project,
		[]Command{command(t, noteCreate, notePayload{ID: id, Body: "queued"}, nil)})
	if err == nil || !strings.Contains(err.Error(), "no job queue") {
		t.Errorf("Apply: %v, want the batch refused for want of a job queue", err)
	}
	if got := w.written(t); got != (written{}) {
		t.Errorf("the refused batch wrote %+v", got)
	}
}

// TestConcurrentBatches checks that batches applied to one project at once
// are applied one at a time, each with a seq of its own, without a gap.
func TestConcurrentBatches(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	p := w.pipeline(t, nil)
	const n = 8
	seqs := make([]int64, n)
	// The batches are made here, because command may call t.Fatal, which
	// only the test's goroutine may.
	batches := make([][]Command, n)
	for i := range batches {
		_, id := newNote()
		batches[i] = []Command{command(t, noteCreate, notePayload{ID: id, Body: "racing"}, nil)}
	}
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			res, err := p.Apply(ctx, w.as(editorUser), w.project, batches[i])
			if err != nil {
				t.Error(err)
			}
			seqs[i] = res.Seq
		})
	}
	wg.Wait()
	slices.Sort(seqs)
	for i, s := range seqs {
		if s != int64(i+1) {
			t.Fatalf("the seqs %v, want 1 to %d", seqs, n)
		}
	}
	if got := w.written(t); got.seq != n || got.operations != n {
		t.Errorf("the batches left %+v", got)
	}
}

// gather returns the value of each sample of the pipeline's metrics, by the
// metric's name and its labels, as name{label="value",…}; a histogram's value
// is its count of observations.
func gather(t *testing.T, p *Pipeline) map[string]float64 {
	t.Helper()
	reg := prometheus.NewRegistry()
	reg.MustRegister(p.Collector())
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]float64{}
	for _, f := range families {
		for _, m := range f.GetMetric() {
			key := f.GetName() + "{"
			for i, l := range m.GetLabel() {
				if i > 0 {
					key += ","
				}
				key += l.GetName() + `="` + l.GetValue() + `"`
			}
			key += "}"
			switch f.GetType() {
			case dto.MetricType_COUNTER:
				values[key] = m.GetCounter().GetValue()
			case dto.MetricType_HISTOGRAM:
				values[key] = float64(m.GetHistogram().GetSampleCount())
			}
		}
	}
	return values
}

// TestMetrics checks that the pipeline counts the batches by their results,
// the commands that it applied by their types, and the batches refused with
// revision_conflict by the types of the commands with stale revisions, and
// that every series starts at zero.
func TestMetrics(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	p := w.pipeline(t, nil)
	if got := gather(t, p); got[`storycreep_command_batches_total{result="revision_conflict"}`] != 0 ||
		len(got) != len(results)+2*len(noteRegistry().Types()) {
		t.Errorf("the metrics before any batch: %v", got)
	}
	x, xID := newNote()
	owner := w.as(ownerUser)
	batches := [][]Command{
		{command(t, noteCreate, notePayload{ID: xID, Body: "x"}, nil),
			command(t, noteUpdate, notePayload{ID: xID, Body: "y"}, nil)},
		{command(t, noteUpdate, notePayload{ID: xID, Body: "z"}, map[Ref]int64{noteRef(x): 1})},
		{{ID: ids.New(), Type: "note.unknown"}},
	}
	for _, b := range batches {
		_, _ = p.Apply(ctx, owner, w.project, b)
	}
	_, _ = p.Apply(ctx, w.as(outsiderUser), w.project, batches[0])
	_, _ = p.Apply(ctx, w.as(viewerUser), w.project, []Command{command(t, noteDelete, noteIDPayload{ID: xID}, nil)})
	_, _ = p.Apply(ctx, owner, w.project, []Command{command(t, noteFail, notePayload{ID: xID, Body: "f"}, nil)})
	got := gather(t, p)
	for key, want := range map[string]float64{
		`storycreep_command_batches_total{result="applied"}`:              1,
		`storycreep_command_batches_total{result="revision_conflict"}`:    1,
		`storycreep_command_batches_total{result="invalid"}`:              1,
		`storycreep_command_batches_total{result="not_found"}`:            1,
		`storycreep_command_batches_total{result="denied"}`:               1,
		`storycreep_command_batches_total{result="rejected"}`:             1,
		`storycreep_command_batches_total{result="over_limit"}`:           0,
		`storycreep_command_batches_total{result="archived"}`:             0,
		`storycreep_commands_applied_total{type="note.create"}`:           1,
		`storycreep_commands_applied_total{type="note.update"}`:           1,
		`storycreep_commands_applied_total{type="note.delete"}`:           0,
		`storycreep_command_revision_conflicts_total{type="note.update"}`: 1,
		`storycreep_command_revision_conflicts_total{type="note.create"}`: 0,
		`storycreep_command_batch_duration_seconds{result="applied"}`:     1,
		`storycreep_command_batch_duration_seconds{result="rejected"}`:    1,
	} {
		if got[key] != want {
			t.Errorf("%s = %v, want %v", key, got[key], want)
		}
	}
}
