package commands

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
)

// noteRestore reports that it restored a note, as its payload's mode says:
// the note, a board, which it does not touch, or the note from its Inverse,
// which reports nothing.
const noteRestore Type = "note.restore"

// restorePayload is noteRestore's payload.
type restorePayload struct {
	ID   string `json:"id"`
	Mode string `json:"mode,omitempty" enum:"board,inverse"`
}

// reconnectingRegistry returns a registry of the notes' commands with
// noteRestore, and a reconnection of the notes to the notes that leaves each
// restored note waiting for the note waiting, or, if foreign is set, a board,
// with the restored notes that each call of its Left was asked about in
// asked.
func reconnectingRegistry(waiting ids.UUID, foreign bool, asked *[][]ids.UUID) *Registry {
	r := noteRegistry()
	Register(r, Spec[restorePayload]{
		Type: noteRestore, Capability: authz.ListMembers, Scope: scope.ProjectWrite, Touches: []ids.Kind{ids.Node},
		Check: func(p restorePayload) input.FieldErrors { return checkNoteID(p.ID) },
		Inverse: func(_ context.Context, tx *Tx, p restorePayload) ([]Step, error) {
			if p.Mode == "inverse" {
				return nil, tx.Restored(ids.Node, noteUUID(p.ID))
			}
			return []Step{}, nil
		},
		Apply: func(_ context.Context, tx *Tx, p restorePayload) error {
			if p.Mode == "board" {
				return tx.Restored(ids.Board, noteUUID(p.ID))
			}
			return tx.Restored(ids.Node, noteUUID(p.ID))
		},
	})
	RegisterReconnection(r, Reconnection{Restored: ids.Node, Kind: ids.Node,
		Left: func(_ context.Context, _ *Tx, restored []ids.UUID) ([]Unreconnected, error) {
			*asked = append(*asked, slices.Clone(restored))
			out := []Unreconnected{}
			for _, id := range restored {
				ref := noteRef(id)
				if foreign {
					ref.Kind = ids.Board
				}
				out = append(out, Unreconnected{Ref: ref, WaitingFor: []Ref{noteRef(waiting)}})
			}
			return out, nil
		}})
	return r
}

// TestReconnection checks the trash's reference-reconnection hook: once every
// command of a batch has applied, the pipeline asks each reconnection of a
// kind that refers to a kind that the batch restored (Tx.Restored), once,
// about every entity of it that the batch restored, and the batch's Result
// lists what it left, which the batch's idempotency record keeps; a batch
// that restores nothing asks nothing. Restored is refused outside Apply and
// for a kind that the command does not touch, and a reconnection that leaves
// an entity of another kind than its own fails the batch.
//
// Verifies: FR-PROJ-06/AC1
func TestReconnection(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	waiting := ids.New()
	var asked [][]ids.UUID
	p := w.pipeline(t, func(o *Options) { o.Registry = reconnectingRegistry(waiting, false, &asked) })
	a, aID := newNote()
	b, bID := newNote()
	raw := make([]json.RawMessage, 3)
	for i, id := range []string{aID, bID, aID} {
		var err error
		if raw[i], err = json.Marshal(map[string]any{"command_id": ids.New().String(), "type": noteRestore,
			"payload": restorePayload{ID: id}}); err != nil {
			t.Fatal(err)
		}
	}
	batch, err := p.Parse(w.project, raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Apply(ctx, w.as(ownerUser), w.project, batch)
	if err != nil {
		t.Fatal(err)
	}
	want := []Unreconnected{{Ref: noteRef(a), WaitingFor: []Ref{noteRef(waiting)}},
		{Ref: noteRef(b), WaitingFor: []Ref{noteRef(waiting)}}}
	if !reflect.DeepEqual(asked, [][]ids.UUID{{a, b}}) || !reflect.DeepEqual(res.NotReconnected, want) {
		t.Errorf("the reconnection was asked about %v, and the batch left %v, want %v once and %v", asked,
			res.NotReconnected, []ids.UUID{a, b}, want)
	}
	again, err := p.Apply(ctx, w.as(ownerUser), w.project, batch)
	if err != nil || !reflect.DeepEqual(again.NotReconnected, want) || len(asked) != 1 {
		t.Errorf("the batch sent again left %v (%v), asking %v, want %v from its record", again.NotReconnected, err,
			asked, want)
	}
	res, err = p.Apply(ctx, w.as(ownerUser), w.project, []Command{command(t, noteTouch, noteIDPayload{ID: aID},
		nil)})
	if err != nil || res.NotReconnected == nil || len(res.NotReconnected) != 0 || len(asked) != 1 {
		t.Errorf("a batch that restores nothing left %#v (%v), asking %v", res.NotReconnected, err, asked)
	}

	for mode, problem := range map[string]string{"inverse": "outside a command's Apply", "board": "does not touch"} {
		_, err := p.Apply(ctx, w.as(ownerUser), w.project, []Command{command(t, noteRestore,
			restorePayload{ID: aID, Mode: mode}, nil)})
		if err == nil || !strings.Contains(err.Error(), problem) {
			t.Errorf("a restoration reported %s: %v, want an error that it is %s", mode, err, problem)
		}
	}
	foreign := w.pipeline(t, func(o *Options) { o.Registry = reconnectingRegistry(waiting, true, &asked) })
	if _, err := foreign.Apply(ctx, w.as(ownerUser), w.project, []Command{command(t, noteRestore,
		restorePayload{ID: aID}, nil)}); err == nil || !strings.Contains(err.Error(), "not of its kind") {
		t.Errorf("a reconnection that leaves a board of the notes: %v", err)
	}
}

// TestRegisterReconnectionRefusesBadOnes checks that a reconnection is
// registered only with its Left, between registered kinds, and once.
func TestRegisterReconnectionRefusesBadOnes(t *testing.T) {
	left := func(context.Context, *Tx, []ids.UUID) ([]Unreconnected, error) { return nil, nil }
	for name, rc := range map[string]Reconnection{
		"no Left":                       {Restored: ids.Node, Kind: ids.Node},
		"an unregistered restored kind": {Restored: ids.Board, Kind: ids.Node, Left: left},
		"an unregistered referrer":      {Restored: ids.Node, Kind: ids.Connection, Left: left},
	} {
		if !panics(func() { RegisterReconnection(noteRegistry(), rc) }) {
			t.Errorf("a reconnection with %s is registered", name)
		}
	}
	r := noteRegistry()
	RegisterReconnection(r, Reconnection{Restored: ids.Node, Kind: ids.Node, Left: left})
	if !panics(func() { RegisterReconnection(r, Reconnection{Restored: ids.Node, Kind: ids.Node, Left: left}) }) {
		t.Error("a reconnection is registered twice")
	}
}
