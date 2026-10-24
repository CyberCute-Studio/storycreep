package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// wireStep is a command of a batch's undo as the API answers it, which a
// test compares with the operation log's inverse.
type wireStep struct {
	Type        Type             `json:"type"`
	Payload     json.RawMessage  `json:"payload"`
	ExpectedRev map[string]int64 `json:"expected_rev,omitempty"`
}

// wireUndo returns the undo as the operation log keeps it.
func wireUndo(undo []UndoCommand) []wireStep {
	out := make([]wireStep, len(undo))
	for i, c := range undo {
		out[i] = wireStep{Type: c.Type, Payload: c.Payload}
		for ref, rev := range c.ExpectedRev {
			if out[i].ExpectedRev == nil {
				out[i].ExpectedRev = map[string]int64{}
			}
			out[i].ExpectedRev[encodeRef(ref)] = rev
		}
	}
	return out
}

// undoBatch returns the batch that sends the undo back, as a client sends it:
// each command with an ID of its own, which a client may send again, and the
// revisions that the undo expects.
func undoBatch(undo []UndoCommand) []Command {
	out := make([]Command, len(undo))
	for i, c := range undo {
		out[i] = Command{ID: ids.New(), Type: c.Type, Payload: c.Payload, ExpectedRev: c.ExpectedRev, sent: true}
	}
	return out
}

// body returns the body and the revision of the note id, as the superuser
// reads them.
func (w *world) body(t *testing.T, id ids.UUID) (string, int64) {
	t.Helper()
	var body string
	var rev int64
	if err := w.admin.QueryRow(context.Background(), "SELECT body, rev FROM note WHERE id = $1", id).Scan(&body,
		&rev); err != nil {
		t.Fatal(err)
	}
	return body, rev
}

// TestUndoAndRedo checks that a batch's Result holds its undo, the operation
// log's inverse, each step expecting the revisions that the batch left of
// what its command changed, though another command of the batch changed it
// too, which, sent back as a batch, undoes the batch, and
// whose own Result holds the undo that redoes it; that a replay of the batch
// answers the same undo, which it reads from the log; that a batch answered as
// AlreadyApplied, and one that cannot be undone, have none; and that a
// client's own undo moves an entity that it brings back to a new revision, so
// that the next undo of its stack expects that one instead (FR-EDIT-08).
func TestUndoAndRedo(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	p := w.pipeline(t, nil)
	a, aID := newNote()
	if _, err := p.Apply(ctx, w.as(ownerUser), w.project,
		[]Command{command(t, noteCreate, notePayload{ID: aID, Body: "first"}, nil)}); err != nil {
		t.Fatal(err)
	}
	batch := []Command{
		sentCommand(t, noteUpdate, notePayload{ID: aID, Body: "second"}, map[Ref]int64{noteRef(a): 1}),
		sentCommand(t, noteUpdate, notePayload{ID: aID, Body: "third"}, nil),
	}
	res, err := p.Apply(ctx, w.as(ownerUser), w.project, batch)
	if err != nil {
		t.Fatal(err)
	}
	// Both commands change the note, which the undo of each expects, so that
	// either, sent alone, expects what it writes back (TestUndoInParts).
	want := []wireStep{
		{Type: noteUpdate, Payload: json.RawMessage(`{"id":"` + aID + `","body":"second"}`),
			ExpectedRev: map[string]int64{aID: 3}},
		{Type: noteUpdate, Payload: json.RawMessage(`{"id":"` + aID + `","body":"first"}`),
			ExpectedRev: map[string]int64{aID: 3}},
	}
	got, err := json.Marshal(wireUndo(res.Undo))
	if err != nil {
		t.Fatal(err)
	}
	if wantJSON, _ := json.Marshal(want); !sameJSON(t, got, string(wantJSON)) {
		t.Errorf("the batch's undo is %s, want %s", got, wantJSON)
	}
	if o := w.operation(t, res.Seq); !sameJSON(t, o.inverse, string(got)) {
		t.Errorf("the batch's undo is %s, but the operation log's inverse is %s", got, o.inverse)
	}
	// Each payload is compact, as the bound counts it (LogBytes), though the
	// log's jsonb writes a space after each colon and comma.
	for i, c := range res.Undo {
		var compact bytes.Buffer
		if err := json.Compact(&compact, c.Payload); err != nil || !bytes.Equal(compact.Bytes(), c.Payload) {
			t.Errorf("the undo's command %d has the payload %s, which is not compact", i, c.Payload)
		}
	}

	// The replay reads the undo from the operation log, as it was answered.
	again, err := p.Apply(ctx, w.as(ownerUser), w.project, batch)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again.Undo, res.Undo) {
		t.Errorf("the replay's undo is %+v, want %+v", again.Undo, res.Undo)
	}
	// A batch of the commands grouped otherwise was applied by a batch that
	// has an undo of its own.
	if regrouped, err := p.Apply(ctx, w.as(ownerUser), w.project, batch[1:]); err != nil ||
		regrouped.Status != AlreadyApplied || regrouped.Undo != nil {
		t.Errorf("a batch of one of the applied commands: %+v, %v, want AlreadyApplied without an undo", regrouped, err)
	}

	undone, err := p.Apply(ctx, w.as(ownerUser), w.project, undoBatch(res.Undo))
	if err != nil {
		t.Fatalf("the undo: %v", err)
	}
	if body, rev := w.body(t, a); body != "first" || rev != 5 {
		t.Errorf("once undone, the note is %q at %d, want first at 5", body, rev)
	}
	redone, err := p.Apply(ctx, w.as(ownerUser), w.project, undoBatch(undone.Undo))
	if err != nil {
		t.Fatalf("the redo: %v", err)
	}
	if body, rev := w.body(t, a); body != "third" || rev != 7 {
		t.Errorf("once redone, the note is %q at %d, want third at 7", body, rev)
	}
	if len(redone.Undo) != 2 || redone.Undo[0].ExpectedRev[noteRef(a)] != 7 ||
		redone.Undo[1].ExpectedRev[noteRef(a)] != 7 {
		t.Errorf("the redo's undo is %+v, want two steps, each of which expects the note at 7", redone.Undo)
	}

	// The note's creation is undone by its deletion, which cannot be undone,
	// so the batch of the deletion has no undo.
	created, err := p.Apply(ctx, w.as(ownerUser), w.project,
		[]Command{command(t, noteCreate, notePayload{ID: ids.Encode(ids.Node, ids.New()), Body: "x"}, nil)})
	if err != nil || len(created.Undo) != 1 || created.Undo[0].Type != noteDelete {
		t.Fatalf("a creation: %+v, %v", created, err)
	}
	if deleted, err := p.Apply(ctx, w.as(ownerUser), w.project, undoBatch(created.Undo)); err != nil ||
		deleted.Undo != nil {
		t.Errorf("the undo of the creation: %+v, %v, want no undo of its own", deleted, err)
	}

	// Two batches of the client's stack change the note: once the second is
	// undone, the note is as the first left it, at a new revision, which the
	// first's undo then expects.
	one, err := p.Apply(ctx, w.as(ownerUser), w.project,
		[]Command{command(t, noteUpdate, notePayload{ID: aID, Body: "fourth"}, map[Ref]int64{noteRef(a): 7})})
	if err != nil {
		t.Fatal(err)
	}
	two, err := p.Apply(ctx, w.as(ownerUser), w.project,
		[]Command{command(t, noteUpdate, notePayload{ID: aID, Body: "fifth"}, map[Ref]int64{noteRef(a): 8})})
	if err != nil {
		t.Fatal(err)
	}
	back, err := p.Apply(ctx, w.as(ownerUser), w.project, undoBatch(two.Undo))
	if err != nil {
		t.Fatal(err)
	}
	var conflict *RevisionConflictError
	if _, err := p.Apply(ctx, w.as(ownerUser), w.project, undoBatch(one.Undo)); !errors.As(err, &conflict) {
		t.Errorf("the first undo, unmoved: %v, want a revision conflict", err)
	}
	moved := undoBatch(one.Undo)
	if moved[0].ExpectedRev[noteRef(a)] != 8 || !slices.Equal(back.Revisions, []Revision{{noteRef(a), 10}}) {
		t.Fatalf("the first undo expects %v, and the second's undo left %v", moved[0].ExpectedRev, back.Revisions)
	}
	moved[0].ExpectedRev = map[Ref]int64{noteRef(a): 10}
	if _, err := p.Apply(ctx, w.as(ownerUser), w.project, moved); err != nil {
		t.Errorf("the first undo, moved to the revision that the second's undo left: %v", err)
	}
	if body, _ := w.body(t, a); body != "third" {
		t.Errorf("once both are undone, the note is %q, want third", body)
	}
}

// TestUndoConflict checks that the undo of a batch whose entity another user
// has changed since is refused with a revision conflict that names the entity
// and the undo's command that expects it, with its current state, and
// changes nothing, not even the entities that nobody changed since, so that
// an undo never overwrites newer work on what its batch changed, nor applies
// in part.
//
// Verifies: FR-EDIT-08/AC4
func TestUndoConflict(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	p := w.pipeline(t, nil)
	a, aID := newNote()
	b, bID := newNote()
	if _, err := p.Apply(ctx, w.as(ownerUser), w.project, []Command{
		command(t, noteCreate, notePayload{ID: aID, Body: "a"}, nil),
		command(t, noteCreate, notePayload{ID: bID, Body: "b"}, nil),
	}); err != nil {
		t.Fatal(err)
	}
	res, err := p.Apply(ctx, w.as(ownerUser), w.project, []Command{
		command(t, noteUpdate, notePayload{ID: aID, Body: "mine"}, nil),
		command(t, noteUpdate, notePayload{ID: bID, Body: "mine"}, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Another user changes b.
	if _, err := p.Apply(ctx, w.as(editorUser), w.project,
		[]Command{command(t, noteUpdate, notePayload{ID: bID, Body: "theirs"}, nil)}); err != nil {
		t.Fatal(err)
	}
	before := w.written(t)
	undo := undoBatch(res.Undo)
	_, err = p.Apply(ctx, w.as(ownerUser), w.project, undo)
	var conflict *RevisionConflictError
	if !errors.As(err, &conflict) || len(conflict.Conflicts) != 1 {
		t.Fatalf("the undo: %v, want a revision conflict of b", err)
	}
	// The undo undoes b's change first, so its first command expects b.
	c := conflict.Conflicts[0]
	var state noteState
	if c.Command != 0 || c.CommandID != undo[0].ID || c.Ref != noteRef(b) || c.Expected != 2 || !c.Exists ||
		c.Current != 3 || json.Unmarshal(c.State, &state) != nil || state.Body != "theirs" {
		t.Errorf("the conflict: %+v, %s", c, c.State)
	}
	if after := w.written(t); after != before {
		t.Errorf("the refused undo wrote %+v, where there was %+v", after, before)
	}
	if body, rev := w.body(t, a); body != "mine" || rev != 2 {
		t.Errorf("after the refused undo, a is %q at %d, want mine at 2", body, rev)
	}
	if body, rev := w.body(t, b); body != "theirs" || rev != 3 {
		t.Errorf("after the refused undo, b is %q at %d, want theirs at 3", body, rev)
	}
}

// TestUndoInParts checks that each part of a batch's undo, sent as a batch of
// its own, as a client sends an undo whose own undo would pass the bounds,
// expects every entity that it writes back: the first part of the undo of two
// changes of a note, which undoes the second, is refused, and changes
// nothing, once another user has changed the note since, rather than write
// it back over their change; and that the parts of an undo that nobody came
// between apply in order, the second once it expects what the first left, as
// the client moves its stacks.
//
// Verifies: FR-EDIT-08/AC4
func TestUndoInParts(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	p := w.pipeline(t, nil)
	a, aID := newNote()
	if _, err := p.Apply(ctx, w.as(ownerUser), w.project,
		[]Command{command(t, noteCreate, notePayload{ID: aID, Body: "first"}, nil)}); err != nil {
		t.Fatal(err)
	}
	twice := func(one, two string) Result {
		t.Helper()
		res, err := p.Apply(ctx, w.as(ownerUser), w.project, []Command{
			command(t, noteUpdate, notePayload{ID: aID, Body: one}, nil),
			command(t, noteUpdate, notePayload{ID: aID, Body: two}, nil),
		})
		if err != nil || len(res.Undo) != 2 {
			t.Fatalf("a batch of two changes of the note: %+v, %v", res, err)
		}
		return res
	}
	res := twice("second", "third")
	// Another user changes the note.
	if _, err := p.Apply(ctx, w.as(editorUser), w.project,
		[]Command{command(t, noteUpdate, notePayload{ID: aID, Body: "theirs"}, nil)}); err != nil {
		t.Fatal(err)
	}
	before := w.written(t)
	var conflict *RevisionConflictError
	_, err := p.Apply(ctx, w.as(ownerUser), w.project, undoBatch(res.Undo[:1]))
	if !errors.As(err, &conflict) || len(conflict.Conflicts) != 1 {
		t.Fatalf("the first part of the undo, once another user changed the note: %v, want a revision conflict", err)
	}
	if c := conflict.Conflicts[0]; c.Command != 0 || c.Ref != noteRef(a) || c.Expected != 3 || c.Current != 4 {
		t.Errorf("the conflict: %+v", c)
	}
	if after := w.written(t); after != before {
		t.Errorf("the refused part wrote %+v, where there was %+v", after, before)
	}
	if body, _ := w.body(t, a); body != "theirs" {
		t.Errorf("after the refused part, the note is %q, want theirs", body)
	}

	// Nobody comes between the parts of the next batch's undo.
	res = twice("fourth", "fifth")
	first, err := p.Apply(ctx, w.as(ownerUser), w.project, undoBatch(res.Undo[:1]))
	if err != nil {
		t.Fatalf("the first part of the undo: %v", err)
	}
	if _, err := p.Apply(ctx, w.as(ownerUser), w.project, undoBatch(res.Undo[1:])); !errors.As(err, &conflict) {
		t.Errorf("the second part, unmoved: %v, want a revision conflict", err)
	}
	moved := undoBatch(res.Undo[1:])
	if moved[0].ExpectedRev[noteRef(a)] != 6 || !slices.Equal(first.Revisions, []Revision{{noteRef(a), 7}}) {
		t.Fatalf("the second part expects %v, and the first left %v", moved[0].ExpectedRev, first.Revisions)
	}
	moved[0].ExpectedRev = map[Ref]int64{noteRef(a): 7}
	if _, err := p.Apply(ctx, w.as(ownerUser), w.project, moved); err != nil {
		t.Errorf("the second part, moved to the revision that the first left: %v", err)
	}
	if body, _ := w.body(t, a); body != "theirs" {
		t.Errorf("once both parts are undone, the note is %q, want theirs", body)
	}
}

// TestUndoSteps checks that the pipeline refuses a command whose inverse
// would give its batch's undo more steps than the pipeline's bound, which a
// batch that undoes it could not send, at its payload, that Tx.Changed stops
// such a command at its first change, rather than let it change everything
// only to be refused, and that a batch whose undo has as many steps as the
// bound applies.
func TestUndoSteps(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	_, aID := newNote()
	_, bID := newNote()
	if _, err := w.pipeline(t, nil).Apply(ctx, w.as(ownerUser), w.project, []Command{
		command(t, noteCreate, notePayload{ID: aID, Body: "a"}, nil),
		command(t, noteCreate, notePayload{ID: bID, Body: "b"}, nil),
	}); err != nil {
		t.Fatal(err)
	}
	p := w.pipeline(t, func(o *Options) { o.MaxUndoSteps = 1 })
	before := w.written(t)
	var r *RefusedError
	if _, err := p.Apply(ctx, w.as(ownerUser), w.project, []Command{
		command(t, noteUpdate, notePayload{ID: aID, Body: "x"}, nil),
		command(t, noteUpdate, notePayload{ID: bID, Body: "x"}, nil),
	}); !errors.As(err, &r) || r.Command != 1 || r.Field != "" || r.Reason != nil {
		t.Errorf("a batch whose undo would have two steps: %v, want a refusal of command 1 at the payload", err)
	}
	if after := w.written(t); after != before {
		t.Errorf("the refused batch wrote %+v, where there was %+v", after, before)
	}
	if res, err := p.Apply(ctx, w.as(ownerUser), w.project,
		[]Command{command(t, noteUpdate, notePayload{ID: aID, Body: "x"}, nil)}); err != nil || len(res.Undo) != 1 {
		t.Errorf("a batch whose undo has one step: %+v, %v", res, err)
	}
	// The command whose steps take the undo past the bound stops at its first
	// change, as one whose steps take it past the bound of bytes does.
	tx := &Tx{current: &spec{typ: noteUpdate, touches: []ids.Kind{ids.Node}}, at: map[Ref]int{},
		revs: map[Ref]int64{}, maxUndo: DefaultMaxUndoBytes, undoSteps: 2, maxUndoSteps: 1}
	if err := tx.Changed(ids.Node, ids.New(), 1); !errors.Is(err, ErrUndoTooLarge) {
		t.Errorf("a change once the undo has more steps than the bound: %v, want ErrUndoTooLarge", err)
	}
}

// TestUndoOfNoChange checks that a batch's undo holds none of the steps of a
// command that changed nothing, which would expect no revision, and so write
// back what the command found over what another user has changed since; that
// the undo of a batch whose commands changed nothing is empty; that the
// bounds give back the room that such a command's steps took, so that the
// next command's inverse has it; and that they do not refuse such a command
// for steps that would take the undo past them after a command whose own
// steps the undo holds, as the undo leaves them out.
func TestUndoOfNoChange(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	x, xID := newNote()
	if _, err := w.pipeline(t, nil).Apply(ctx, w.as(ownerUser), w.project,
		[]Command{command(t, noteCreate, notePayload{ID: xID, Body: "x"}, nil)}); err != nil {
		t.Fatal(err)
	}
	deletion, err := json.Marshal(noteIDPayload{ID: xID})
	if err != nil {
		t.Fatal(err)
	}
	// noteBadInverse changes nothing, and its inverse is the note's deletion,
	// once the undo has the room that its payload says.
	nothing := func(room int) Command {
		return command(t, noteBadInverse, badInversePayload{ID: xID, Step: noteDelete, Room: room}, nil)
	}
	// The deletion's step takes all of the bound, and is all that it allows.
	p := w.pipeline(t, func(o *Options) { o.MaxUndoBytes, o.MaxUndoSteps = len(deletion), 1 })
	res, err := p.Apply(ctx, w.as(ownerUser), w.project, []Command{nothing(len(deletion)), nothing(len(deletion))})
	if err != nil || res.Undo == nil || len(res.Undo) != 0 {
		t.Fatalf("a batch whose commands changed nothing: %+v, %v, want an empty undo", res, err)
	}
	if o := w.operation(t, res.Seq); !sameJSON(t, o.inverse, `[]`) {
		t.Errorf("the inverse of a batch whose commands changed nothing is %s, want []", o.inverse)
	}

	// After a change whose undo takes all of a bound, the deletion's step
	// would take it past, but the undo leaves it out, so the batch applies.
	change, err := json.Marshal(notePayload{ID: xID, Body: "x"})
	if err != nil {
		t.Fatal(err)
	}
	undoBytes := len(change) + ExpectedRevBytes(noteRef(x))
	for i, bound := range []func(o *Options){
		func(o *Options) { o.MaxUndoBytes = undoBytes + len(deletion) - 1 },
		func(o *Options) { o.MaxUndoSteps = 1 },
	} {
		body := []string{"y", "z"}[i]
		res, err = w.pipeline(t, bound).Apply(ctx, w.as(ownerUser), w.project, []Command{
			command(t, noteUpdate, notePayload{ID: xID, Body: body}, nil), nothing(1)})
		if err != nil {
			t.Fatalf("a change and a command that changed nothing, whose steps would take the undo past bound %d: %v",
				i, err)
		}
		if len(res.Undo) != 1 || res.Undo[0].Type != noteUpdate {
			t.Errorf("the undo of a change and a command that changed nothing is %+v, want the change's alone",
				res.Undo)
		}
		if i > 0 {
			continue
		}
		got, err := json.Marshal(wireUndo(res.Undo))
		if err != nil {
			t.Fatal(err)
		}
		if want := `[{"type": "note.update", "payload": {"id": "` + xID + `", "body": "x"}, "expected_rev": {"` +
			xID + `": 2}}]`; !sameJSON(t, got, want) {
			t.Errorf("the undo of a change and a command that changed nothing is %s, want %s", got, want)
		}
	}
	if _, rev := w.body(t, x); rev != 3 {
		t.Errorf("the note is at %d, want 3", rev)
	}
}

// TestExpectedRevBytes checks that the bound counts an expected revision of
// an entity of each kind as the bytes that JSON encodes it in, in a step's
// expected_rev, with the largest revision, its key and its comma, which it
// counts without encoding the entity's ID.
func TestExpectedRevBytes(t *testing.T) {
	for _, kind := range ids.Kinds() {
		ref := Ref{Kind: kind, ID: ids.New()}
		raw, err := json.Marshal(map[string]int64{encodeRef(ref): math.MaxInt64})
		if err != nil {
			t.Fatal(err)
		}
		if got, want := ExpectedRevBytes(ref), len(raw)-len(`{}`)+len(`,`); got != want {
			t.Errorf("ExpectedRevBytes(%v) = %d, want %d, as %s takes", kind, got, want, raw)
		}
	}
}

// TestUndoable checks that the registry tells the types of command with an
// inverse from those marked as having none, which TestEveryCommandUndoable
// (package httpserver) reads of the server's registry.
func TestUndoable(t *testing.T) {
	r := noteRegistry()
	if got, want := r.Undoable(), []Type{noteBadInverse, noteCreate, noteUpdate}; !slices.Equal(got, want) {
		t.Errorf("Undoable() = %v, want %v", got, want)
	}
}

// TestLogBytes checks that the bound of a batch's undo counts a payload as
// many bytes as the operation log's jsonb writes it, compact, as a batch's
// answer holds it, or more: a float with an exponent as many as its digits
// take without one, and a string as encoding/json escapes it, which jsonb
// writes as long or shorter.
func TestLogBytes(t *testing.T) {
	w := newWorld(t)
	for _, v := range []any{
		map[string]any{"x": 1e300, "y": -5e-324, "z": 1.5e-7},
		[]any{1e21, -1.2345e+22, 1e-6, 1e-7, 123456789.125, 0, -0.5, math.MaxFloat64, math.SmallestNonzeroFloat64},
		map[string]any{"text": `"1e+300" <a> & \ é`, "n": int64(math.MaxInt64), "e": []any{}, "o": map[string]any{}},
		map[string]any{"nested": map[string]any{"values": map[string]any{"co_1": 2.5e-300, "co_2": true}}},
	} {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var text string
		if err := w.admin.QueryRow(context.Background(), "SELECT $1::jsonb::text", string(raw)).
			Scan(&text); err != nil {
			t.Fatal(err)
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, []byte(text)); err != nil {
			t.Fatal(err)
		}
		if got := LogBytes(raw); got < compact.Len() {
			t.Errorf("LogBytes(%s) = %d, but the log writes %s, of %d", raw, got, compact.Bytes(), compact.Len())
		}
		// Of numbers alone, the count is exact.
		if _, ok := v.([]any); ok && LogBytes(raw) != compact.Len() {
			t.Errorf("LogBytes(%s) = %d, want %d, as the log writes %s", raw, LogBytes(raw), compact.Len(),
				compact.Bytes())
		}
	}
}
