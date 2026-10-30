package commands

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
)

// noteLeave changes nothing, and leaves work for the derivation that its
// payload's body names (Tx.Leave).
const noteLeave Type = "note.leave"

// derived is what a derivation was given once: what the batch had changed,
// and whether a command of it left the derivation work (Tx.Left).
type derived struct {
	changed []Change
	left    bool
}

// derivingRegistry returns a registry of the notes' commands with noteLeave,
// and two derivations of the notes, first and second, in that order, each of
// which records in seen what it was given, and of which first, once a
// command left it work, changes the note bump.
func derivingRegistry(bump ids.UUID, seen map[string][]derived) *Registry {
	r := noteRegistry()
	Register(r, Spec[notePayload]{
		Type: noteLeave, Capability: authz.ListMembers, Scope: scope.ProjectWrite, Touches: []ids.Kind{ids.Node},
		Inverse: func(context.Context, *Tx, notePayload) ([]Step, error) { return nil, nil },
		Apply:   func(_ context.Context, tx *Tx, p notePayload) error { return tx.Leave(p.Body) },
	})
	for _, name := range []string{"first", "second"} {
		RegisterDerivation(r, Derivation{Name: name, Touches: []ids.Kind{ids.Node},
			Derive: func(ctx context.Context, tx *Tx, changed []Change) error {
				seen[name] = append(seen[name], derived{changed: changed, left: tx.Left(name)})
				if name == "first" && tx.Left(name) {
					return setNote(ctx, tx, bump, "body = body || '!'")
				}
				return nil
			}})
	}
	return r
}

// TestDerivation checks the pipeline's derivation hook: once every command
// of a batch has applied, each derivation, in the order of their
// registration, is given what the batch has changed, each entity with the
// types of the commands that changed it, and the names of the derivations
// before it that did, and whether a command left it work (Tx.Leave); what a
// derivation changes, the batch's answer lists, but no step of the batch's
// undo expects; and a command that leaves work for a derivation that the
// registry lacks fails the batch, which writes nothing.
func TestDerivation(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	a, aID := newNote()
	b, bID := newNote()
	seen := map[string][]derived{}
	p := w.pipeline(t, func(o *Options) { o.Registry = derivingRegistry(b, seen) })
	if _, err := p.Apply(ctx, w.as(ownerUser), w.project, []Command{
		command(t, noteCreate, notePayload{ID: aID, Body: "a"}, nil),
		command(t, noteCreate, notePayload{ID: bID, Body: "b"}, nil),
		command(t, noteUpdate, notePayload{ID: aID, Body: "aa"}, nil)}); err != nil {
		t.Fatal(err)
	}
	want := []Change{{Revision: Revision{Ref: noteRef(a), Rev: 2}, By: []Type{noteCreate, noteUpdate}},
		{Revision: Revision{Ref: noteRef(b), Rev: 1}, By: []Type{noteCreate}}}
	for _, name := range []string{"first", "second"} {
		if got := seen[name]; len(got) != 1 || got[0].left || !reflect.DeepEqual(got[0].changed, want) {
			t.Errorf("%s was given %+v, want %+v, and no work left", name, got, want)
		}
	}

	clear(seen)
	res, err := p.Apply(ctx, w.as(ownerUser), w.project, []Command{
		command(t, noteUpdate, notePayload{ID: aID, Body: "aaa"}, map[Ref]int64{noteRef(a): 2}),
		command(t, noteLeave, notePayload{ID: aID, Body: "first"}, nil)})
	if err != nil {
		t.Fatal(err)
	}
	first := []Change{{Revision: Revision{Ref: noteRef(a), Rev: 3}, By: []Type{noteUpdate}}}
	if got := seen["first"]; len(got) != 1 || !got[0].left || !reflect.DeepEqual(got[0].changed, first) {
		t.Errorf("first was given %+v, want %+v, and work left", got, first)
	}
	second := append(slices.Clone(first), Change{Revision: Revision{Ref: noteRef(b), Rev: 2}, By: []Type{"first"}})
	if got := seen["second"]; len(got) != 1 || got[0].left || !reflect.DeepEqual(got[0].changed, second) {
		t.Errorf("second was given %+v, want %+v, and no work left", got, second)
	}
	if want := []Revision{{Ref: noteRef(a), Rev: 3}, {Ref: noteRef(b), Rev: 2}}; !reflect.DeepEqual(res.Revisions,
		want) {
		t.Errorf("the batch listed %v, want %v", res.Revisions, want)
	}
	if len(res.Undo) != 1 || !reflect.DeepEqual(res.Undo[0].ExpectedRev, map[Ref]int64{noteRef(a): 3}) {
		t.Errorf("the batch's undo is %+v, want the update's alone, which expects the note that it changed", res.Undo)
	}

	before := w.written(t)
	if _, err := p.Apply(ctx, w.as(ownerUser), w.project, []Command{command(t, noteLeave,
		notePayload{ID: aID, Body: "third"}, nil)}); err == nil || !strings.Contains(err.Error(), "no derivation") {
		t.Errorf("a command that leaves work for a derivation that the registry lacks: %v", err)
	}
	if got := w.written(t); got != before {
		t.Errorf("the failed batch left %+v, want %+v", got, before)
	}
}

// TestRegisterDerivationRefusesBadOnes checks that a derivation is registered
// only with a name of its form, which no command's type has, its Derive, and
// kinds that are registered, each once, or none, and once.
func TestRegisterDerivationRefusesBadOnes(t *testing.T) {
	derive := func(context.Context, *Tx, []Change) error { return nil }
	good := func() Derivation {
		return Derivation{Name: "note_index", Touches: []ids.Kind{ids.Node}, Derive: derive}
	}
	for _, tt := range []struct {
		name   string
		change func(*Derivation)
	}{
		{"no name", func(d *Derivation) { d.Name = "" }},
		{"the name of a command's type", func(d *Derivation) { d.Name = string(noteUpdate) }},
		{"a name of the wrong form", func(d *Derivation) { d.Name = "Note_Index" }},
		{"no Derive", func(d *Derivation) { d.Derive = nil }},
		{"an unregistered entity", func(d *Derivation) { d.Touches = []ids.Kind{ids.Board} }},
		{"an entity twice", func(d *Derivation) { d.Touches = []ids.Kind{ids.Node, ids.Node} }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := good()
			tt.change(&d)
			if !panics(func() { RegisterDerivation(noteRegistry(), d) }) {
				t.Error("RegisterDerivation accepts it")
			}
		})
	}
	r := noteRegistry()
	if panics(func() { RegisterDerivation(r, good()) }) {
		t.Error("RegisterDerivation refuses a good derivation")
	}
	if !panics(func() { RegisterDerivation(r, good()) }) {
		t.Error("RegisterDerivation registers a derivation twice")
	}
	untouching := good()
	untouching.Name, untouching.Touches = "note_text", nil
	if panics(func() { RegisterDerivation(r, untouching) }) {
		t.Error("RegisterDerivation refuses a derivation that touches nothing")
	}
}

// TestUntouchingDerivationChangesNothing checks that a derivation that touches
// no kind of entity is refused a report of a change (Tx.Changed), which
// fails its batch, so that it changes no entity's revision.
func TestUntouchingDerivationChangesNothing(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	a, aID := newNote()
	r := noteRegistry()
	RegisterDerivation(r, Derivation{Name: "note_text", Derive: func(ctx context.Context, tx *Tx, changed []Change) error {
		return tx.Changed(ids.Node, a, changed[0].Rev+1)
	}})
	p := w.pipeline(t, func(o *Options) { o.Registry = r })
	before := w.written(t)
	if _, err := p.Apply(ctx, w.as(ownerUser), w.project, []Command{
		command(t, noteCreate, notePayload{ID: aID, Body: "a"}, nil)}); err == nil ||
		!strings.Contains(err.Error(), "does not touch") {
		t.Errorf("a derivation that touches nothing reported a change: %v", err)
	}
	if got := w.written(t); got != before {
		t.Errorf("the failed batch left %+v, want %+v", got, before)
	}
}
