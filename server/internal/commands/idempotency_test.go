package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// records returns how many rows of idempotency records the world's database
// holds, and of them how many are the rows of batches' first commands.
func (w *world) records(t *testing.T) (rows, batches int) {
	t.Helper()
	return w.count(t, "SELECT count(*) FROM idempotency_record"),
		w.count(t, "SELECT count(*) FROM idempotency_record WHERE command_id = batch_command_id")
}

// age makes the idempotency record of the batch whose first command is first
// as old as interval, a PostgreSQL interval such as '30 days'.
func (w *world) age(t *testing.T, first ids.UUID, interval string) {
	t.Helper()
	w.exec(t, "UPDATE idempotency_record SET created_at = now() - $2::interval WHERE batch_command_id = $1", first,
		interval)
}

// sameResult reports whether a and b are the same Result, nil and empty
// revisions apart.
func sameResult(a, b Result) bool {
	return a.Status == b.Status && a.Seq == b.Seq && slices.Equal(a.Revisions, b.Revisions)
}

// TestReplay checks that a batch sent again while its idempotency record is
// kept is answered as it was the first time, whatever changed since, and
// applies nothing again: the operation log has the one entry, the Notifier
// is told once, and the metrics count the second as replayed.
//
// Verifies: FR-API-03/AC1
func TestReplay(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	p := w.pipeline(t, nil)
	a, aID := newNote()
	batch := []Command{
		command(t, noteCreate, notePayload{ID: aID, Body: "first"}, nil),
		command(t, noteUpdate, notePayload{ID: aID, Body: "second"}, nil),
	}
	first, err := p.Apply(ctx, w.as(ownerUser), w.project, batch)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Result{Status: Applied, Seq: 1, Revisions: []Revision{{noteRef(a), 2}}}); !sameResult(first, want) {
		t.Fatalf("Apply = %+v, want %+v", first, want)
	}
	if rows, batches := w.records(t); rows != 2 || batches != 1 {
		t.Errorf("the batch left %d rows of %d records, want 2 rows of 1", rows, batches)
	}
	// The note changes since, which the replay does not show.
	if _, err := p.Apply(ctx, w.as(ownerUser), w.project,
		[]Command{command(t, noteUpdate, notePayload{ID: aID, Body: "third"}, nil)}); err != nil {
		t.Fatal(err)
	}
	before := w.written(t)
	again, err := p.Apply(ctx, w.as(ownerUser), w.project, batch)
	if err != nil {
		t.Fatalf("the replay: %v", err)
	}
	if !sameResult(again, first) {
		t.Errorf("the replay = %+v, want %+v", again, first)
	}
	if after := w.written(t); after != before {
		t.Errorf("the replay wrote %+v, where there was %+v", after, before)
	}
	if n := w.count(t, "SELECT count(*) FROM operation WHERE $1 = ANY(command_ids)", batch[0].ID); n != 1 {
		t.Errorf("the batch's commands are in %d operations, want 1", n)
	}
	if got := w.notes.told(); len(got) != 2 {
		t.Errorf("the Notifier was told %v, want the two batches that applied", got)
	}
	m := gather(t, p)
	if m[`storycreep_command_batches_total{result="replayed"}`] != 1 ||
		m[`storycreep_command_batches_total{result="applied"}`] != 2 ||
		m[`storycreep_commands_applied_total{type="note.create"}`] != 1 {
		t.Errorf("the metrics: %v", m)
	}
}

// TestReplayAfterPruning checks that the pruning job deletes the idempotency
// records older than RecordRetention, whole, and keeps the younger ones, and
// that a batch sent again 30 days after it applied, once its record is
// pruned, is answered as AlreadyApplied, with its original seq and the
// current revisions of the entities that it changed, and creates nothing.
//
// Verifies: FR-API-03/AC4
func TestReplayAfterPruning(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	p := w.pipeline(t, nil)
	a, aID := newNote()
	b, bID := newNote()
	create := []Command{command(t, noteCreate, notePayload{ID: aID, Body: "old"}, nil),
		command(t, noteCreate, notePayload{ID: bID, Body: "old too"}, nil)}
	if _, err := p.Apply(ctx, w.as(ownerUser), w.project, create); err != nil {
		t.Fatal(err)
	}
	update := []Command{command(t, noteUpdate, notePayload{ID: aID, Body: "new"}, nil)}
	if _, err := p.Apply(ctx, w.as(ownerUser), w.project, update); err != nil {
		t.Fatal(err)
	}
	w.age(t, create[0].ID, "30 days")
	// A record just inside the retention is kept.
	w.age(t, update[0].ID, "23 hours 59 minutes")
	pruned, err := Prune(ctx, w.db)
	if err != nil {
		t.Fatal(err)
	}
	if rows, batches := w.records(t); pruned != 1 || rows != 1 || batches != 1 {
		t.Fatalf("Prune pruned %d batches, and left %d rows of %d records, want 1 pruned, and 1 row of 1 left",
			pruned, rows, batches)
	}
	if n := w.count(t, "SELECT count(*) FROM idempotency_record WHERE command_id = $1", update[0].ID); n != 1 {
		t.Errorf("the younger batch's record is gone")
	}

	before := w.written(t)
	res, err := p.Apply(ctx, w.as(ownerUser), w.project, create)
	if err != nil {
		t.Fatalf("the late replay: %v", err)
	}
	want := Result{Status: AlreadyApplied, Seq: 1, Revisions: []Revision{{noteRef(a), 2}, {noteRef(b), 1}}}
	if !sameResult(res, want) {
		t.Errorf("the late replay = %+v, want %+v", res, want)
	}
	if after := w.written(t); after != before {
		t.Errorf("the late replay wrote %+v, where there was %+v", after, before)
	}
	// An entity that no longer exists is left out.
	w.exec(t, "DELETE FROM note WHERE id = $1", b)
	if res, err := p.Apply(ctx, w.as(ownerUser), w.project, create); err != nil ||
		!sameResult(res, Result{Status: AlreadyApplied, Seq: 1, Revisions: []Revision{{noteRef(a), 2}}}) {
		t.Errorf("the late replay of a batch whose note is gone = %+v, %v", res, err)
	}
	// A second run finds nothing more to prune.
	if pruned, err := Prune(ctx, w.db); err != nil || pruned != 0 {
		t.Errorf("Prune again = %d, %v, want 0", pruned, err)
	}
}

// TestReplayAtLimit checks that a batch sent again is answered before the
// workspace's limits are checked, so that sending again a create that took
// the last of a limit is answered with its original success, while its
// record is kept and once it is pruned, rather than with a limit error.
//
// Verifies: FR-API-03/AC5
func TestReplayAtLimit(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	limited := func(used int64) *Pipeline {
		return w.pipeline(t, func(o *Options) {
			o.Entitlements = entitlements.New(entitlements.Options{Caps: entitlements.Caps{entitlements.Projects: 1},
				Meter: fixedMeter{entitlements.Projects: used}})
		})
	}
	a, aID := newNote()
	create := []Command{command(t, noteCreate, notePayload{ID: aID, Body: "the last"}, nil)}
	first, err := limited(0).Apply(ctx, w.as(ownerUser), w.project, create)
	if err != nil {
		t.Fatal(err)
	}
	// The create took the last of the limit.
	full := limited(1)
	_, newID := newNote()
	var limit *entitlements.LimitError
	if _, err := full.Apply(ctx, w.as(ownerUser), w.project,
		[]Command{command(t, noteCreate, notePayload{ID: newID, Body: "one too many"}, nil)}); !errors.As(err, &limit) {
		t.Fatalf("a new create at the limit: %v, want a limit error", err)
	}
	if res, err := full.Apply(ctx, w.as(ownerUser), w.project, create); err != nil || !sameResult(res, first) {
		t.Errorf("the replay at the limit = %+v, %v, want %+v", res, err, first)
	}
	w.age(t, create[0].ID, "2 days")
	if _, err := Prune(ctx, w.db); err != nil {
		t.Fatal(err)
	}
	res, err := full.Apply(ctx, w.as(ownerUser), w.project, create)
	if want := (Result{Status: AlreadyApplied, Seq: 1, Revisions: []Revision{{noteRef(a), 1}}}); err != nil ||
		!sameResult(res, want) {
		t.Errorf("the late replay at the limit = %+v, %v, want %+v", res, err, want)
	}
	if n := w.count(t, "SELECT count(*) FROM note"); n != 1 {
		t.Errorf("there are %d notes, want 1", n)
	}
}

// TestRegroupedBatch checks a batch whose commands were applied grouped
// otherwise: one whose commands were all applied is answered as
// AlreadyApplied, as is the batch sent again once its record is pruned, and
// one with commands that were applied and one that was not is refused with
// an *IdempotencyMismatchError that lists the applied ones, and applies
// nothing.
//
// Verifies: FR-API-03/AC6
func TestRegroupedBatch(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	p := w.pipeline(t, nil)
	a, aID := newNote()
	b, bID := newNote()
	c1 := command(t, noteCreate, notePayload{ID: aID, Body: "a"}, nil)
	c2 := command(t, noteCreate, notePayload{ID: bID, Body: "b"}, nil)
	if _, err := p.Apply(ctx, w.as(ownerUser), w.project, []Command{c1, c2}); err != nil {
		t.Fatal(err)
	}
	both := []Revision{{noteRef(a), 1}, {noteRef(b), 1}}
	for _, regrouped := range [][]Command{{c2, c1}, {c1}, {c2}} {
		res, err := p.Apply(ctx, w.as(ownerUser), w.project, regrouped)
		if err != nil || !sameResult(res, Result{Status: AlreadyApplied, Seq: 1, Revisions: both}) {
			t.Errorf("a batch of %d of the applied commands = %+v, %v, want them already applied", len(regrouped),
				res, err)
		}
	}
	w.age(t, c1.ID, "30 days")
	if _, err := Prune(ctx, w.db); err != nil {
		t.Fatal(err)
	}
	before := w.written(t)
	res, err := p.Apply(ctx, w.as(ownerUser), w.project, []Command{c1, c2})
	if err != nil || !sameResult(res, Result{Status: AlreadyApplied, Seq: 1, Revisions: both}) {
		t.Errorf("the batch sent again once its record is pruned = %+v, %v, want it already applied", res, err)
	}

	_, cID := newNote()
	c3 := command(t, noteCreate, notePayload{ID: cID, Body: "c"}, nil)
	_, err = p.Apply(ctx, w.as(ownerUser), w.project, []Command{c3, c1})
	var mismatch *IdempotencyMismatchError
	if !errors.As(err, &mismatch) || !slices.Equal(mismatch.Applied, []ids.UUID{c1.ID}) {
		t.Fatalf("a batch of an applied command and a new one: %v, want a mismatch that lists %s", err, c1.ID)
	}
	if after := w.written(t); after != before {
		t.Errorf("the regrouped batches wrote %+v, where there was %+v", after, before)
	}
	if rows, _ := w.records(t); rows != 0 {
		t.Errorf("the refused batch left %d rows of idempotency records", rows)
	}
	if got := gather(t, p)[`storycreep_command_batches_total{result="idempotency_mismatch"}`]; got != 1 {
		t.Errorf("the mismatches counted: %v, want 1", got)
	}
	// The new command, sent in a batch of its own, applies.
	if res, err := p.Apply(ctx, w.as(ownerUser), w.project, []Command{c3}); err != nil || res.Status != Applied ||
		res.Seq != 2 {
		t.Errorf("the new command alone = %+v, %v", res, err)
	}
}

// TestRememberedRefusal checks that a batch that its project's state
// refuses, archived, past a limit, or of stale revisions, is refused the
// same way when it is sent again while its record is kept, even once the
// state would let it apply, and that a batch with its commands grouped
// otherwise is checked anew, and forgets the record; and that the refusals
// of an actor's role and of a command are not remembered.
func TestRememberedRefusal(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	p := w.pipeline(t, nil)
	a, aID := newNote()
	if _, err := p.Apply(ctx, w.as(ownerUser), w.project,
		[]Command{command(t, noteCreate, notePayload{ID: aID, Body: "a"}, nil)}); err != nil {
		t.Fatal(err)
	}

	t.Run("archived", func(t *testing.T) {
		w.exec(t, "UPDATE project SET archived_at = now() WHERE id = $1", w.project)
		update := command(t, noteUpdate, notePayload{ID: aID, Body: "while archived"}, nil)
		if _, err := p.Apply(ctx, w.as(ownerUser), w.project, []Command{update}); !errors.Is(err,
			ErrProjectArchived) {
			t.Fatalf("a change of an archived project: %v", err)
		}
		w.exec(t, "UPDATE project SET archived_at = NULL WHERE id = $1", w.project)
		if _, err := p.Apply(ctx, w.as(ownerUser), w.project, []Command{update}); !errors.Is(err,
			ErrProjectArchived) {
			t.Errorf("the change sent again once the project is unarchived: %v, want it refused as it was", err)
		}
		// Grouped otherwise, the command is checked anew, and applies.
		touch := command(t, noteTouch, noteIDPayload{ID: aID}, nil)
		if _, err := p.Apply(ctx, w.as(ownerUser), w.project, []Command{update, touch}); err != nil {
			t.Fatalf("the change grouped otherwise: %v", err)
		}
		if n := w.count(t, "SELECT count(*) FROM idempotency_record WHERE command_id = $1 AND response IS NULL",
			touch.ID); n != 1 {
			t.Errorf("the applied batch's record of its second command: %d rows", n)
		}
		res, err := p.Apply(ctx, w.as(ownerUser), w.project, []Command{update})
		if err != nil || res.Status != AlreadyApplied {
			t.Errorf("the refused batch sent again once its command applied = %+v, %v, want it already applied",
				res, err)
		}
	})

	t.Run("limit", func(t *testing.T) {
		full := w.pipeline(t, func(o *Options) {
			o.Entitlements = entitlements.New(entitlements.Options{Caps: entitlements.Caps{entitlements.Projects: 1},
				Meter: fixedMeter{entitlements.Projects: 1}})
		})
		_, id := newNote()
		create := []Command{command(t, noteCreate, notePayload{ID: id, Body: "past the limit"}, nil)}
		_, err := full.Apply(ctx, w.as(ownerUser), w.project, create)
		var limit *entitlements.LimitError
		if !errors.As(err, &limit) {
			t.Fatalf("a create past the limit: %v", err)
		}
		// With room again, the batch sent again is refused as it was, with
		// the usage and the limit that refused it.
		_, again := p.Apply(ctx, w.as(ownerUser), w.project, create)
		var limitAgain *entitlements.LimitError
		if !errors.As(again, &limitAgain) || *limitAgain != *limit {
			t.Errorf("the create sent again with room: %v, want %v", again, err)
		}
		w.age(t, create[0].ID, "25 hours")
		if _, err := Prune(ctx, w.db); err != nil {
			t.Fatal(err)
		}
		if res, err := p.Apply(ctx, w.as(ownerUser), w.project, create); err != nil || res.Status != Applied {
			t.Errorf("the create sent again once its record is pruned = %+v, %v, want it applied", res, err)
		}
	})

	t.Run("revision conflict", func(t *testing.T) {
		stale := command(t, noteUpdate, notePayload{ID: aID, Body: "stale"}, map[Ref]int64{noteRef(a): 1})
		_, err := p.Apply(ctx, w.as(ownerUser), w.project, []Command{stale})
		var conflict *RevisionConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("a stale batch: %v", err)
		}
		// The note changes again, which the refusal sent again does not show.
		if _, err := p.Apply(ctx, w.as(ownerUser), w.project,
			[]Command{command(t, noteUpdate, notePayload{ID: aID, Body: "newer"}, nil)}); err != nil {
			t.Fatal(err)
		}
		_, again := p.Apply(ctx, w.as(ownerUser), w.project, []Command{stale})
		var conflictAgain *RevisionConflictError
		if !errors.As(again, &conflictAgain) || !reflect.DeepEqual(conflictAgain, conflict) {
			t.Errorf("the stale batch sent again: %#v, want %#v", again, conflict)
		}
		if got := gather(t, p)[`storycreep_command_revision_conflicts_total{type="note.update"}`]; got != 1 {
			t.Errorf("the conflicts counted: %v, want the first alone", got)
		}
	})

	t.Run("not remembered", func(t *testing.T) {
		_, rowsBefore := w.records(t)
		del := []Command{command(t, noteDelete, noteIDPayload{ID: aID}, nil)}
		if _, err := p.Apply(ctx, w.as(viewerUser), w.project, del); !errors.Is(err, ErrInsufficientRole) {
			t.Fatalf("a viewer's batch: %v", err)
		}
		_, failID := newNote()
		fail := []Command{command(t, noteFail, notePayload{ID: failID, Body: "f"}, nil)}
		if _, err := p.Apply(ctx, w.as(ownerUser), w.project, fail); !errors.Is(err, errRefused) {
			t.Fatalf("a refusing command: %v", err)
		}
		if _, rows := w.records(t); rows != rowsBefore {
			t.Errorf("the refusals left %d records, where there were %d", rows, rowsBefore)
		}
		// The viewer's batch, sent by an owner, applies.
		if res, err := p.Apply(ctx, w.as(ownerUser), w.project, del); err != nil || res.Status != Applied {
			t.Errorf("the viewer's batch sent by an owner = %+v, %v", res, err)
		}
	})
}

// TestConcurrentReplays checks that a batch sent many times at once, as by a
// client that retries while its first request is in flight, applies once,
// and every other answer is the same, never an error.
func TestConcurrentReplays(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	p := w.pipeline(t, nil)
	_, id := newNote()
	batch := []Command{command(t, noteCreate, notePayload{ID: id, Body: "once"}, nil)}
	const n = 8
	results := make([]Result, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			res, err := p.Apply(ctx, w.as(editorUser), w.project, batch)
			if err != nil {
				t.Error(err)
			}
			results[i] = res
		})
	}
	wg.Wait()
	for i, res := range results {
		if !sameResult(res, results[0]) || res.Status != Applied || res.Seq != 1 {
			t.Errorf("answer %d = %+v, want %+v", i, res, results[0])
		}
	}
	if got := w.written(t); got.notes != 1 || got.operations != 1 || got.seq != 1 {
		t.Errorf("the batches left %+v", got)
	}
}

// TestParse checks that Parse reads a client's commands, and that a batch
// with commands that it cannot read is refused with every problem of the
// batch, those that Apply would find in its other commands included, in the
// order of the commands.
func TestParse(t *testing.T) {
	p := New(nil, Options{Registry: noteRegistry(), Entitlements: entitlements.New(entitlements.Options{})})
	a, aID := newNote()
	id := ids.New()
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	cmds, err := p.Parse([]json.RawMessage{raw(fmt.Sprintf(`{"command_id": %q, "type": "note.update",
		"payload": {"id": %q, "body": "x"}, "expected_rev": {%q: 3}}`, id, aID, aID))})
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 1 || cmds[0].ID != id || cmds[0].Type != noteUpdate ||
		!reflect.DeepEqual(cmds[0].ExpectedRev, map[Ref]int64{noteRef(a): 3}) {
		t.Fatalf("Parse = %+v", cmds)
	}

	upper := "ND_" + strings.ToUpper(aID[len("nd_"):])
	_, err = p.Parse([]json.RawMessage{
		raw(fmt.Sprintf(`{"command_id": %q, "type": "note.update", "payload": {"id": %q, "body": 7}}`, ids.New(),
			aID)),
		raw(`{"command_id": "not-a-uuid", "type": "note.unknown", "payload": {}}`),
		raw(`{"command_id": 7}`),
		raw(fmt.Sprintf(`{"command_id": %q, "type": "note.update", "payload": {"id": %q, "body": "y"},
			"expected_rev": {"xx_1": 1, %q: 1, %q: 2}}`, ids.New(), aID, aID, upper)),
		raw(`{"command_id": "", "type": "note.update", "payload": {"id": "nd_x", "body": ""}, "extra": 1}`),
	})
	var invalid *InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("Parse of commands it cannot read: %v", err)
	}
	type at struct {
		command int
		field   string
	}
	var got []at
	for _, pr := range invalid.Problems {
		got = append(got, at{pr.Command, pr.Field})
	}
	// The keys are read in their order, in which the uppercase spelling of the
	// note comes first.
	want := []at{{0, "payload.body"}, {1, "command_id"}, {1, "type"}, {2, ""}, {3, "expected_rev." + aID},
		{3, "expected_rev.xx_1"}, {4, ""}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the problems %+v, want them at %+v", invalid.Problems, want)
	}
}

// TestDocument checks that Document names a schema of a command of each type
// and of its payload after the type, and that the schema of any command is
// one of them, which a command of the type matches, and one that its
// payload's schema refuses does not, as the pipeline refuses it.
func TestDocument(t *testing.T) {
	reg := noteRegistry()
	doc := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	union := reg.Document(doc)
	if len(union.OneOf) != len(reg.Types()) {
		t.Fatalf("the schema of a command has %d members, want %d", len(union.OneOf), len(reg.Types()))
	}
	if union.OneOf[0].Ref != "#/components/schemas/NoteBadInverseCommand" {
		t.Errorf("the first member is %s", union.OneOf[0].Ref)
	}
	cmd := doc.Map()["NoteCreateCommand"]
	if cmd == nil || doc.Map()["NoteCreatePayload"] == nil {
		t.Fatalf("the schemas: %v", slices.Sorted(func(yield func(string) bool) {
			for k := range doc.Map() {
				if !yield(k) {
					return
				}
			}
		}))
	}
	if !reflect.DeepEqual(cmd.Properties["type"].Enum, []any{"note.create"}) ||
		!slices.Equal(cmd.Required, []string{"command_id", "type", "payload"}) {
		t.Errorf("the schema of note.create: %+v", cmd)
	}
	_, aID := newNote()
	validate := func(v string) int {
		var x any
		if err := json.Unmarshal([]byte(v), &x); err != nil {
			t.Fatal(err)
		}
		res := &huma.ValidateResult{}
		huma.Validate(doc, union, huma.NewPathBuffer([]byte{}, 0), huma.ModeWriteToServer, x, res)
		return len(res.Errors)
	}
	good := fmt.Sprintf(`{"command_id": %q, "type": "note.create", "payload": {"id": %q, "body": "b"},
		"expected_rev": {%q: 1}}`, ids.New(), aID, aID)
	if n := validate(good); n != 0 {
		t.Errorf("a valid command has %d problems", n)
	}
	for _, bad := range []string{
		fmt.Sprintf(`{"command_id": %q, "type": "note.create", "payload": {"id": %q, "body": %q}}`, ids.New(), aID,
			strings.Repeat("x", 101)),
		fmt.Sprintf(`{"command_id": %q, "type": "note.create", "payload": {"id": %q}}`, ids.New(), aID),
		fmt.Sprintf(`{"command_id": %q, "type": "note.create", "payload": {"id": %q, "body": "b"}, "x": 1}`,
			ids.New(), aID),
		fmt.Sprintf(`{"command_id": %q, "type": "note.create", "payload": {"id": %q, "body": "b"},
			"expected_rev": {%q: 0}}`, ids.New(), aID, aID),
	} {
		if validate(bad) == 0 {
			t.Errorf("%s is valid", bad)
		}
	}
	defer func() {
		if recover() == nil {
			t.Error("Document called twice on one registry does not panic")
		}
	}()
	reg.Document(doc)
}

// TestIdempotencyIsolation tries the queries of the idempotency records and
// of the operations' entities on another workspace's rows, from a
// transaction of one workspace, where row-level security must keep them from
// reading, adding, deleting, or pruning its records, and from reading its
// applied commands and its operations (NFR-SEC-01).
//
// Verifies: NFR-SEC-01
func TestIdempotencyIsolation(t *testing.T) {
	t.Parallel()
	f := isolationtest.New(t, dbtest.New(t), isolationtest.Tables)
	record := func(w *isolationtest.Workspace) (pgtype.UUID, pgtype.UUID) {
		return nullUUID(w.Rows["project"]), nullUUID(w.Rows["idempotency_record"])
	}
	f.Check(t, isolationtest.Op{
		Name: "a batch's idempotency record",
		Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) ([]ids.UUID, error) {
			project, cmd := record(target)
			_, err := queries.New(tx).BatchRecord(ctx, queries.BatchRecordParams{WorkspaceID: nullUUID(target.ID),
				ProjectID: project, CommandID: cmd})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil
			}
			return []ids.UUID{target.Rows["idempotency_record"]}, err
		},
	})
	f.Check(t, isolationtest.Op{
		Name: "the applied commands",
		Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) ([]ids.UUID, error) {
			rows, err := queries.New(tx).AppliedCommands(ctx, queries.AppliedCommandsParams{
				WorkspaceID: nullUUID(target.ID), ProjectID: nullUUID(target.Rows["project"]),
				CommandIds: []pgtype.UUID{nullUUID(target.Rows["applied_command"])}})
			var out []ids.UUID
			for _, r := range rows {
				out = append(out, r.CommandID.Bytes)
			}
			return out, err
		},
	})
	f.Check(t, isolationtest.Op{
		Name: "the entities of operations",
		Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) ([]ids.UUID, error) {
			var seq int64
			if err := f.Admin().QueryRow(ctx, "SELECT seq FROM operation WHERE id = $1",
				target.Rows["operation"]).Scan(&seq); err != nil {
				return nil, err
			}
			docs, err := queries.New(tx).OperationEntityRefs(ctx, queries.OperationEntityRefsParams{
				WorkspaceID: nullUUID(target.ID), ProjectID: nullUUID(target.Rows["project"]), Seqs: []int64{seq}})
			if len(docs) > 0 {
				return []ids.UUID{target.Rows["operation"]}, err
			}
			return nil, err
		},
	})
	f.Check(t, isolationtest.Op{
		Name:   "a new idempotency record",
		Denied: true,
		Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) ([]ids.UUID, error) {
			id := ids.New()
			return []ids.UUID{id}, queries.New(tx).InsertBatchRecord(ctx, queries.InsertBatchRecordParams{
				WorkspaceID: nullUUID(target.ID), ProjectID: nullUUID(target.Rows["project"]),
				CommandIds: []pgtype.UUID{nullUUID(id)}, Response: []byte("{}")})
		},
	})
	f.Check(t, isolationtest.Op{
		Name:  "the deletion of batches' idempotency records",
		Blind: true,
		Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) ([]ids.UUID, error) {
			project, cmd := record(target)
			return nil, queries.New(tx).DeleteBatchRecords(ctx, queries.DeleteBatchRecordsParams{
				WorkspaceID: nullUUID(target.ID), ProjectID: project, CommandIds: []pgtype.UUID{cmd}})
		},
	})
	f.Check(t, isolationtest.Op{
		Name:  "the pruning of idempotency records",
		Blind: true,
		Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) ([]ids.UUID, error) {
			// Every record is older than no time at all.
			_, err := queries.New(tx).PruneBatchRecords(ctx, queries.PruneBatchRecordsParams{
				WorkspaceID: nullUUID(target.ID), Retention: pgtype.Interval{Valid: true}, MaxRows: 10})
			return nil, err
		},
	})
}

// TestPruneEveryWorkspace checks that Prune prunes the records of every
// workspace, each in a transaction of its own, which row-level security
// scopes to it.
func TestPruneEveryWorkspace(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	other := ids.New()
	otherProject := ids.New()
	w.exec(t, "INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Other', $2, 'unlimited')", other,
		"other-"+other.String()[:8])
	w.exec(t, "INSERT INTO project (id, workspace_id, name) VALUES ($1, $2, 'Other')", otherProject, other)
	for _, at := range []struct{ ws, project ids.UUID }{{w.ws, w.project}, {other, otherProject}} {
		first, second := ids.New(), ids.New()
		w.exec(t, `INSERT INTO idempotency_record (workspace_id, project_id, command_id, batch_command_id,
			command_ids, response, created_at) VALUES ($1, $2, $3, $3, ARRAY[$3, $4]::uuid[], '{"seq": 1}',
			now() - interval '2 days'), ($1, $2, $4, $3, NULL, NULL, now() - interval '2 days')`,
			at.ws, at.project, first, second)
	}
	pruned, err := Prune(ctx, w.db)
	if err != nil {
		t.Fatal(err)
	}
	if rows, _ := w.records(t); pruned != 2 || rows != 0 {
		t.Errorf("Prune pruned %d batches, and left %d rows, want 2, and none", pruned, rows)
	}
}
