package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
)

// TestDecodeRefusesInvalidBatches checks that a batch is refused before
// anything reads the database when it is not one that the pipeline can
// apply, with every problem of every command, each at its command and field:
// no commands, a command ID that is missing or used twice, a type that is not
// registered, a payload that its schema does not allow, whose field names are
// matched exactly (PRD FR-API-01 AC2), or that its type's Check refuses, a
// payload with a NUL character, and an expected revision of an entity of a
// kind that the command does not touch, or that no entity has.
func TestDecodeRefusesInvalidBatches(t *testing.T) {
	r := noteRegistry()
	if _, err := r.decode(nil); !hasProblem(err, -1, "") {
		t.Errorf("an empty batch: %v", err)
	}

	_, id := newNote()
	same := ids.New()
	board := Ref{Kind: ids.Board, ID: ids.New()}
	note := noteRef(ids.New())
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	valid := fmt.Sprintf(`{"id": %q, "body": "fine"}`, id)
	batch := []Command{
		{ID: same, Type: noteCreate, Payload: raw(valid)},
		{ID: same, Type: noteCreate, Payload: raw(valid)},
		{Type: noteCreate, Payload: raw(valid)},
		{ID: ids.New(), Type: "note.unknown", Payload: raw(valid)},
		{ID: ids.New(), Type: noteCreate, Payload: raw(fmt.Sprintf(`{"id": %q, "body": "x", "colour": "red"}`, id))},
		{ID: ids.New(), Type: noteCreate, Payload: raw(fmt.Sprintf(`{"ID": %q, "body": "x"}`, id))},
		{ID: ids.New(), Type: noteCreate, Payload: raw(fmt.Sprintf(`{"id": %q}`, id))},
		{ID: ids.New(), Type: noteCreate, Payload: raw(fmt.Sprintf(`{"id": %q, "body": 3}`, id))},
		{ID: ids.New(), Type: noteCreate, Payload: raw(fmt.Sprintf(`{"id": %q, "body": %q}`, id,
			strings.Repeat("a", 101)))},
		{ID: ids.New(), Type: noteCreate, Payload: raw(fmt.Sprintf(`{"id": %q, "body": "x"}`,
			ids.Encode(ids.Board, ids.New())))},
		{ID: ids.New(), Type: noteCreate, Payload: raw(`null`)},
		{ID: ids.New(), Type: noteCreate},
		{ID: ids.New(), Type: noteCreate, Payload: raw(`{"id": `)},
		{ID: ids.New(), Type: noteUpdate, Payload: raw(valid), ExpectedRev: map[Ref]int64{board: 1, note: 0}},
		{ID: ids.New(), Type: noteMisreport, Payload: raw(fmt.Sprintf(`{"id": %q, "mode": "other"}`, id))},
		{ID: ids.New(), Type: noteCreate, Payload: raw(fmt.Sprintf(`{"id": %q, "body": "a\u0000b"}`, id))},
	}
	_, err := r.decode(batch)
	var invalid *InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("decode: %v, want an *InvalidError", err)
	}
	for _, want := range []struct {
		command int
		field   string
	}{
		{1, "command_id"},
		{2, "command_id"},
		{3, "type"},
		{4, "payload.colour"},
		// A field written in another case is unknown, and the field is
		// missing, which the schema reports of the object that lacks it, as
		// the API does of a request's body.
		{5, "payload.ID"},
		{5, "payload"},
		{6, "payload"},
		{7, "payload.body"},
		{8, "payload.body"},
		{9, "payload.id"},
		{10, "payload"},
		{11, "payload"},
		{12, "payload"},
		{13, "expected_rev." + encodeRef(board)},
		{13, "expected_rev." + encodeRef(note)},
		{14, "payload.mode"},
		// The schema allows a NUL character, which the operation log's jsonb
		// cannot hold.
		{15, "payload.body"},
	} {
		if !hasProblem(err, want.command, want.field) {
			t.Errorf("no problem of command %d's %s in %v", want.command, want.field, invalid.Problems)
		}
	}
	for _, p := range invalid.Problems {
		if p.Command == 0 {
			t.Errorf("a valid command has the problem %+v", p)
		}
	}

	// The first command alone is valid, and decodes as the payload it holds.
	cmds, err := r.decode(batch[:1])
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := cmds[0].payload.(notePayload); !ok || got != (notePayload{ID: id, Body: "fine"}) {
		t.Errorf("the payload decodes as %#v", cmds[0].payload)
	}
	if !sameJSON(t, cmds[0].encoded, valid) {
		t.Errorf("the command encodes as %s", cmds[0].encoded)
	}
}

// TestNulAt checks that a NUL character is found wherever a payload holds it,
// in a string or in an object's key, at the location that the schema would
// give a problem there, and that an escaped backslash before u0000 is not
// one.
func TestNulAt(t *testing.T) {
	for raw, want := range map[string]string{
		`"a\u0000"`: "",
		`{"items": [{"id": "x"}, {"id": "\u0000"}]}`: "items[1].id",
		`{"by_name": {"a\u0000": 1}}`:                "by_name",
	} {
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			t.Fatal(err)
		}
		if where, ok := nulAt(v, ""); !ok || where != want {
			t.Errorf("nulAt(%s) = %q, %v, want %q", raw, where, ok, want)
		}
	}
	for _, raw := range []string{`{"a": "\\u0000"}`, `[1, true, null, "b"]`} {
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			t.Fatal(err)
		}
		if where, ok := nulAt(v, ""); ok {
			t.Errorf("nulAt(%s) finds a NUL character at %q", raw, where)
		}
	}
}

// hasProblem reports whether err is an *InvalidError with a problem of the
// command, -1 for the batch, and the field.
func hasProblem(err error, command int, field string) bool {
	var invalid *InvalidError
	if !errors.As(err, &invalid) {
		return false
	}
	return slices.ContainsFunc(invalid.Problems, func(p Problem) bool {
		return p.Command == command && p.Field == field && p.Message != ""
	})
}

// TestRegisterRefusesBadSpecs checks that Register refuses a declaration that
// the pipeline cannot apply, and RegisterEntity a kind of entity without its
// functions or registered already.
func TestRegisterRefusesBadSpecs(t *testing.T) {
	apply := func(context.Context, *Tx, notePayload) error { return nil }
	inverse := func(context.Context, *Tx, notePayload) ([]Step, error) { return nil, nil }
	good := func() Spec[notePayload] {
		return Spec[notePayload]{Type: "note.fine", Capability: authz.ListMembers, Touches: []ids.Kind{ids.Node},
			Inverse: inverse, Apply: apply}
	}
	for _, tt := range []struct {
		name   string
		change func(*Spec[notePayload])
	}{
		{"a type of the wrong form", func(s *Spec[notePayload]) { s.Type = "Note.Create" }},
		{"a type without a dot", func(s *Spec[notePayload]) { s.Type = batchType }},
		{"a registered type", func(s *Spec[notePayload]) { s.Type = noteCreate }},
		{"no capability", func(s *Spec[notePayload]) { s.Capability = "" }},
		{"an unknown capability", func(s *Spec[notePayload]) { s.Capability = "project.everything" }},
		{"nothing touched", func(s *Spec[notePayload]) { s.Touches = nil }},
		{"an unregistered entity", func(s *Spec[notePayload]) { s.Touches = []ids.Kind{ids.Board} }},
		{"an entity twice", func(s *Spec[notePayload]) { s.Touches = []ids.Kind{ids.Node, ids.Node} }},
		{"an inert limit", func(s *Spec[notePayload]) {
			s.Limits = []Limit[notePayload]{{Key: "snapshot_retention"}}
		}},
		{"a limit twice", func(s *Spec[notePayload]) {
			s.Limits = []Limit[notePayload]{{Key: entitlements.Projects}, {Key: entitlements.Projects}}
		}},
		{"both inverse and non-undoable", func(s *Spec[notePayload]) { s.NonUndoable = true }},
		{"neither inverse nor non-undoable", func(s *Spec[notePayload]) { s.Inverse = nil }},
		{"no apply", func(s *Spec[notePayload]) { s.Apply = nil }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := good()
			tt.change(&s)
			if !panics(func() { Register(noteRegistry(), s) }) {
				t.Error("Register accepts it")
			}
		})
	}
	if panics(func() { Register(noteRegistry(), good()) }) {
		t.Error("Register refuses a good declaration")
	}
	if !panics(func() {
		Register(noteRegistry(), Spec[string]{Type: "note.text", Capability: authz.ListMembers,
			Touches: []ids.Kind{ids.Node}, NonUndoable: true,
			Apply: func(context.Context, *Tx, string) error { return nil }})
	}) {
		t.Error("Register accepts a payload that is not a struct")
	}
	// What the API gives a request's body beyond its JSON, a payload never
	// gets.
	if !panics(func() { registerPayload[defaultPayload](t) }) {
		t.Error("Register accepts a payload with a default")
	}
	if !panics(func() { registerPayload[heldDefaultPayload](t) }) {
		t.Error("Register accepts a payload that holds a struct with a default")
	}
	if !panics(func() { registerPayload[heldResolverPayload](t) }) {
		t.Error("Register accepts a payload that holds a type that resolves itself")
	}

	states := func(context.Context, *Tx, []ids.UUID) (map[ids.UUID]any, error) { return nil, nil }
	revs := func(context.Context, *Tx, []ids.UUID) (map[ids.UUID]int64, error) { return nil, nil }
	for name, e := range map[string]Entity{
		"no kind":             {Revs: revs, States: states},
		"no Revs":             {Kind: ids.Board, States: states},
		"no States":           {Kind: ids.Board, Revs: revs},
		"a registered kind":   {Kind: ids.Node, Revs: revs, States: states},
		"a kind out of range": {Kind: ids.Kind(200), Revs: revs, States: states},
	} {
		if !panics(func() { RegisterEntity(noteRegistry(), e) }) {
			t.Errorf("RegisterEntity accepts %s", name)
		}
	}
}

// defaultPayload is a payload with a default, which the API would fill in
// where a request's body leaves the field out.
type defaultPayload struct {
	Body string `json:"body,omitempty" default:"empty"`
}

// heldDefaultPayload is a payload that holds a struct with a default.
type heldDefaultPayload struct {
	Items []*defaultPayload `json:"items"`
}

// resolving is a type that resolves itself, as huma calls it on a request's
// body.
type resolving struct {
	Name string `json:"name"`
}

func (*resolving) Resolve(huma.Context) []error { return nil }

// heldResolverPayload is a payload that holds a type that resolves itself.
type heldResolverPayload struct {
	ByName map[string]resolving `json:"by_name"`
}

// registerPayload registers a type of command whose payload is a P.
func registerPayload[P any](t *testing.T) {
	t.Helper()
	Register(noteRegistry(), Spec[P]{Type: "note.payload", Capability: authz.ListMembers,
		Touches: []ids.Kind{ids.Node}, NonUndoable: true, Apply: func(context.Context, *Tx, P) error { return nil }})
}

// FieldError is a payload whose type has the name of input.FieldError, a
// type of another package.
type FieldError struct {
	Field string `json:"field"`
}

// TestRegisterKeepsPackagesApart checks that the payloads' types of two
// packages that share a name each have a schema of their own, which validates
// its own payloads, rather than the second's registration panicking as a
// duplicate of the first.
func TestRegisterKeepsPackagesApart(t *testing.T) {
	r := noteRegistry()
	theirs, ours := Type("note.theirs"), Type("note.ours")
	if panics(func() {
		Register(r, Spec[input.FieldError]{Type: theirs, Capability: authz.ListMembers, Touches: []ids.Kind{ids.Node},
			NonUndoable: true, Apply: func(context.Context, *Tx, input.FieldError) error { return nil }})
		Register(r, Spec[FieldError]{Type: ours, Capability: authz.ListMembers, Touches: []ids.Kind{ids.Node},
			NonUndoable: true, Apply: func(context.Context, *Tx, FieldError) error { return nil }})
	}) {
		t.Fatal("Register refuses a payload whose type has the name of another package's")
	}
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	if _, err := r.decode([]Command{{ID: ids.New(), Type: theirs, Payload: raw(`{"Field": "a", "Message": "b"}`)},
		{ID: ids.New(), Type: ours, Payload: raw(`{"field": "a"}`)}}); err != nil {
		t.Errorf("each payload against its own schema: %v", err)
	}
	_, err := r.decode([]Command{{ID: ids.New(), Type: ours, Payload: raw(`{"Field": "a", "Message": "b"}`)}})
	if !hasProblem(err, 0, "payload.Message") {
		t.Errorf("a payload of one type against the other's schema: %v", err)
	}
}

// panics reports whether f panics.
func panics(f func()) (panicked bool) {
	defer func() { panicked = recover() != nil }()
	f()
	return false
}

// TestNewNeedsEntitlements checks that a pipeline is not made without the
// entitlements that check its commands' limits.
func TestNewNeedsEntitlements(t *testing.T) {
	if !panics(func() { New(nil, Options{Registry: noteRegistry()}) }) {
		t.Error("New makes a pipeline without entitlements")
	}
}

// TestResult checks the labels of the metrics' results.
func TestResult(t *testing.T) {
	for err, want := range map[error]string{
		nil:                                   resultApplied,
		&InvalidError{}:                       resultInvalid,
		ErrNotFound:                           resultNotFound,
		ErrInsufficientRole:                   resultDenied,
		ErrOwnerRequired:                      resultDenied,
		&entitlements.LimitError{}:            resultOverLimit,
		&RevisionConflictError{}:              resultRevisionConflict,
		fmt.Errorf("wrapped: %w", errRefused): resultRejected,
	} {
		if got := result(err); got != want {
			t.Errorf("result(%v) = %s, want %s", err, got, want)
		}
	}
}
