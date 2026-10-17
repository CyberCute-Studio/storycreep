package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
)

func init() {
	// A payload's field whose name differs from the schema's only in case is
	// unknown, and a required field written so is missing, as in the API's
	// request bodies (PRD FR-API-01 AC2), which package api sets the same.
	huma.ValidateStrictCasing = true
}

// Type names a type of command, as node.create: the kind of entity that it
// is about and what it does to it, each lowercase words joined by
// underscores, with a dot between them. A type, once released, keeps its
// meaning and its payload's schema, because clients send it, and the
// operation log and the offline queues keep it (PRD §5.7, FR-OFF-03).
type Type string

// typeForm is the form of a Type.
var typeForm = regexp.MustCompile(`^[a-z]+(_[a-z]+)*\.[a-z]+(_[a-z]+)*$`)

// batchType is the type of an operation of several commands, which no
// command's Type is, because it has no dot.
const batchType = "batch"

// Spec declares a type of command whose payload is a P, which is a struct
// that encoding/json decodes the payload's JSON object into (PRD §10.4).
type Spec[P any] struct {
	Type Type
	// Capability is the operation of the permission matrix that the command
	// needs (PRD §5.6), which the actor's role must allow: their effective
	// role on the project for a project-scoped operation, and their role in
	// the workspace for a workspace-scoped one (§5.5).
	Capability authz.Operation
	// Touches are the kinds of entity that the command changes or refers to,
	// each registered with RegisterEntity. A command's ExpectedRev may name
	// an entity of these kinds only, and Apply reports a change of these
	// kinds only (Tx.Changed).
	Touches []ids.Kind
	// Limits are the limits of the workspace's plan that the command adds to,
	// which the pipeline checks before it applies the batch (FR-ENT-01).
	Limits []Limit[P]
	// Check returns what is wrong with a payload that its schema cannot say,
	// such as an identifier of the wrong type, with each field's name in the
	// payload, as name or items[2].id, or nil. It reads nothing but p.
	Check func(p P) input.FieldErrors
	// Inverse returns the commands that undo the command, in the order to
	// apply them (§10.4). The pipeline calls it in the batch's transaction
	// once it has checked the command's revisions, right before Apply, so
	// that it reads the state that Apply changes. Exactly one of Inverse and
	// NonUndoable is set.
	Inverse func(ctx context.Context, tx *Tx, p P) ([]Step, error)
	// NonUndoable marks a command without a meaningful inverse, whose batch
	// cannot be undone.
	NonUndoable bool
	// Apply applies the command in the batch's transaction, and reports each
	// entity that it changes with Tx.Changed. An error refuses the whole
	// batch, which then changes nothing.
	Apply func(ctx context.Context, tx *Tx, p P) error
}

// Limit is a limit of the workspace's plan that a command adds to.
type Limit[P any] struct {
	Key entitlements.LimitKey
	// Adds returns how much of the limit the command with the payload p
	// adds, or nil for 1.
	Adds func(p P) int64
}

// Step is a command of an inverse: its type and its payload, which encodes as
// the type's payload does. The command ID and the revisions are the undo's
// to give (plan item 37).
type Step struct {
	Type    Type
	Payload any
}

// Entity registers a kind of entity that commands change, whose rows carry a
// revision, rev, which every change increments (PRD §5.7, ADR-006). Its
// functions read the entity of the batch's project in the batch's
// transaction. The pipeline applies a project's batches one at a time, under
// a lock of the project's row, so what they read stays as it is until a
// command of the batch changes it.
type Entity struct {
	Kind ids.Kind
	// Rev returns the entity's revision, and false if the project has no
	// such entity, as when it never existed or has been purged. An entity in
	// the trash still exists.
	Rev func(ctx context.Context, tx *Tx, id ids.UUID) (rev int64, found bool, err error)
	// State returns the entity as it is now, as the API shows it, which a
	// client that sent a stale revision is answered with
	// (RevisionConflictError). It encodes as JSON.
	State func(ctx context.Context, tx *Tx, id ids.UUID) (any, error)
}

// Registry holds the types of command, and the kinds of entity that they
// change (PRD §10.4): the one write path of a project's content. Register
// the kinds of entity and the types before New.
type Registry struct {
	specs    map[Type]*spec
	entities map[ids.Kind]Entity
	// schemas are the payloads' schemas, as the API's OpenAPI document writes
	// them, which validate the payloads.
	schemas huma.Registry
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{specs: map[Type]*spec{}, entities: map[ids.Kind]Entity{},
		schemas: huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)}
}

// Types returns the registered types of command, in order.
func (r *Registry) Types() []Type {
	types := make([]Type, 0, len(r.specs))
	for t := range r.specs {
		types = append(types, t)
	}
	slices.Sort(types)
	return types
}

// RegisterEntity registers the kind of entity e. It panics if e lacks a
// function, or if its kind is not one or is registered already.
func RegisterEntity(r *Registry, e Entity) {
	switch {
	case !validKind(e.Kind):
		panic(fmt.Sprintf("commands: %v is not a kind of entity", e.Kind))
	case e.Rev == nil || e.State == nil:
		panic(fmt.Sprintf("commands: the entity %s needs both Rev and State", e.Kind))
	}
	if _, ok := r.entities[e.Kind]; ok {
		panic(fmt.Sprintf("commands: the entity %s is registered already", e.Kind))
	}
	r.entities[e.Kind] = e
}

// spec is a registered type of command, whose payload's Go type the
// functions hide.
type spec struct {
	typ        Type
	capability authz.Operation
	touches    []ids.Kind
	schema     *huma.Schema
	limits     []entitlements.LimitKey
	// decode decodes a payload that its schema allows, checks it, and
	// returns it with how much it adds to each limit.
	decode func(raw []byte) (payload any, adds []int64, problems input.FieldErrors)
	// inverse is nil for a command that cannot be undone.
	inverse func(ctx context.Context, tx *Tx, payload any) ([]Step, error)
	apply   func(ctx context.Context, tx *Tx, payload any) error
}

// Register registers the type of command s. It panics if s is not a
// declaration that the pipeline can apply: a Type of the wrong form or one
// that is registered already, a Capability that is not an operation of the
// permission matrix, no Touches, a touched kind that is not registered or
// twice, a limit that the plan file does not check or twice, both Inverse and
// NonUndoable or neither, no Apply, or a payload that is not a struct.
func Register[P any](r *Registry, s Spec[P]) {
	fail := func(format string, args ...any) {
		panic(fmt.Sprintf("commands: %s: ", s.Type) + fmt.Sprintf(format, args...))
	}
	pt := reflect.TypeFor[P]()
	switch {
	case !typeForm.MatchString(string(s.Type)):
		fail("a type is two groups of lowercase words joined by underscores, with a dot between them")
	case r.specs[s.Type] != nil:
		fail("the type is registered already")
	case !s.Capability.Known():
		fail("%q is not an operation of the permission matrix", s.Capability)
	case len(s.Touches) == 0:
		fail("a command touches at least one kind of entity")
	case (s.Inverse != nil) == s.NonUndoable:
		fail("exactly one of Inverse and NonUndoable is set")
	case s.Apply == nil:
		fail("the command has no Apply")
	case pt.Kind() != reflect.Struct:
		fail("the payload is a %s, not a struct", pt)
	}
	for i, k := range s.Touches {
		if _, ok := r.entities[k]; !ok {
			fail("the entity %s is not registered", k)
		}
		if slices.Contains(s.Touches[:i], k) {
			fail("the entity %s is touched twice", k)
		}
	}
	keys := make([]entitlements.LimitKey, len(s.Limits))
	for i, l := range s.Limits {
		if !slices.Contains(entitlements.LimitKeys(), l.Key) {
			fail("%q is not a limit that the plan file checks", l.Key)
		}
		if slices.Contains(keys[:i], l.Key) {
			fail("the limit %s is added to twice", l.Key)
		}
		keys[i] = l.Key
	}
	sp := &spec{typ: s.Type, capability: s.Capability, touches: slices.Clone(s.Touches),
		schema: r.schemas.Schema(pt, true, ""), limits: keys}
	sp.decode = func(raw []byte) (any, []int64, input.FieldErrors) {
		var p P
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if err := d.Decode(&p); err != nil {
			// The schema allows the payload, so this is only a value that
			// the schema cannot describe, such as an integer out of its
			// type's range.
			return nil, nil, input.FieldErrors{{Message: "does not decode: " + err.Error()}}
		}
		if s.Check != nil {
			if problems := s.Check(p); len(problems) > 0 {
				return nil, nil, problems
			}
		}
		adds := make([]int64, len(s.Limits))
		for i, l := range s.Limits {
			adds[i] = 1
			if l.Adds != nil {
				adds[i] = l.Adds(p)
			}
		}
		return p, adds, nil
	}
	if s.Inverse != nil {
		sp.inverse = func(ctx context.Context, tx *Tx, payload any) ([]Step, error) {
			return s.Inverse(ctx, tx, payload.(P))
		}
	}
	sp.apply = func(ctx context.Context, tx *Tx, payload any) error { return s.Apply(ctx, tx, payload.(P)) }
	r.specs[s.Type] = sp
}

// Problem is what is wrong with a command of a batch, or with the batch.
type Problem struct {
	// Command is the index of the command in the batch, or -1 for the batch
	// as a whole.
	Command int
	// Field is the command's field that is wrong, as command_id, type,
	// payload.name, or expected_rev.nd_01HQ8Z3XK4F7NAWR5TCQ9M2BVD, with a
	// payload's fields named as in its JSON, or "" for the batch.
	Field string
	// Message says what is wrong, in English, for programs and logs.
	Message string
}

// InvalidError is a batch that the pipeline refuses before it reads the
// database, because it is not one that it can apply: it has no commands, or
// a command whose ID is missing or used twice, whose type is not registered,
// whose payload does not match its type's schema or Check, or whose
// ExpectedRev names an entity of a kind that the command does not touch, or a
// revision that no entity has. The API answers it with validation_failed.
type InvalidError struct {
	Problems []Problem
}

func (e *InvalidError) Error() string {
	var b strings.Builder
	b.WriteString("commands: the batch is not valid:")
	for _, p := range e.Problems {
		switch {
		case p.Command < 0:
			fmt.Fprintf(&b, " the batch %s;", p.Message)
		case p.Field == "":
			fmt.Fprintf(&b, " command %d %s;", p.Command, p.Message)
		default:
			fmt.Fprintf(&b, " command %d's %s %s;", p.Command, p.Field, p.Message)
		}
	}
	return strings.TrimSuffix(b.String(), ";")
}

// decoded is a command of a batch that decode has checked.
type decoded struct {
	Command
	spec    *spec
	payload any
	// encoded is the payload as the operation log keeps it: re-encoded from
	// the decoded payload, so that it holds what the command was applied
	// with, in its fields' order.
	encoded []byte
	adds    []int64
}

// decode checks the batch, and returns its commands decoded, or an
// *InvalidError that lists every problem of the batch.
func (r *Registry) decode(batch []Command) ([]decoded, error) {
	if len(batch) == 0 {
		return nil, &InvalidError{Problems: []Problem{{Command: -1, Message: "has no commands"}}}
	}
	var problems []Problem
	out := make([]decoded, len(batch))
	first := map[ids.UUID]int{}
	for i, c := range batch {
		add := func(field, msg string) { problems = append(problems, Problem{Command: i, Field: field, Message: msg}) }
		if j, dup := first[c.ID]; dup && c.ID != (ids.UUID{}) {
			add("command_id", fmt.Sprintf("is the ID of command %d of the batch too", j))
		} else if c.ID == (ids.UUID{}) {
			add("command_id", "is missing")
		} else {
			first[c.ID] = i
		}
		s, ok := r.specs[c.Type]
		if !ok {
			add("type", "is not a type of command")
			continue
		}
		for _, ref := range sortedRefs(c.ExpectedRev) {
			field := "expected_rev." + encodeRef(ref)
			switch {
			case !slices.Contains(s.touches, ref.Kind):
				add(field, fmt.Sprintf("names a %s, which the command does not touch", ref.Kind))
			case c.ExpectedRev[ref] < 1:
				add(field, "is not a revision; an entity's first revision is 1")
			}
		}
		fields := r.validate(s.schema, c.Payload)
		var payload any
		var adds []int64
		if len(fields) == 0 {
			payload, adds, fields = s.decode(c.Payload)
		}
		for _, f := range fields {
			add(strings.TrimSuffix("payload."+f.Field, "."), f.Message)
		}
		if len(fields) > 0 {
			continue
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			add("payload", "does not encode: "+err.Error())
			continue
		}
		out[i] = decoded{Command: c, spec: s, payload: payload, encoded: encoded, adds: adds}
	}
	if len(problems) > 0 {
		return nil, &InvalidError{Problems: problems}
	}
	return out, nil
}

// validate returns what is wrong with the payload raw under the schema, with
// each field's location in the payload, as name or items[2].id, or "" for
// the payload itself.
func (r *Registry) validate(schema *huma.Schema, raw []byte) input.FieldErrors {
	var v any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &v); err != nil {
			return input.FieldErrors{{Message: "is not valid JSON"}}
		}
	}
	res := &huma.ValidateResult{}
	huma.Validate(r.schemas, schema, huma.NewPathBuffer([]byte{}, 0), huma.ModeWriteToServer, v, res)
	var out input.FieldErrors
	for _, err := range res.Errors {
		var detail *huma.ErrorDetail
		if errors.As(err, &detail) {
			out = append(out, input.FieldError{Field: detail.Location, Message: detail.Message})
		} else {
			out = append(out, input.FieldError{Message: err.Error()})
		}
	}
	return out
}

// checkSteps returns an error unless every step of an inverse is a
// registered type of command whose payload its schema and Check allow, so
// that an undo can apply it.
func (r *Registry) checkSteps(steps []Step) error {
	for i, st := range steps {
		s, ok := r.specs[st.Type]
		if !ok {
			return fmt.Errorf("step %d has the type %q, which is not registered", i, st.Type)
		}
		raw, err := json.Marshal(st.Payload)
		if err != nil {
			return fmt.Errorf("step %d (%s) does not encode: %w", i, st.Type, err)
		}
		problems := r.validate(s.schema, raw)
		if len(problems) == 0 {
			_, _, problems = s.decode(raw)
		}
		if len(problems) > 0 {
			return fmt.Errorf("step %d (%s) is not valid: %w", i, st.Type, problems)
		}
	}
	return nil
}

// sortedRefs returns the entities of revs in order of their kinds and IDs.
func sortedRefs(revs map[Ref]int64) []Ref {
	refs := make([]Ref, 0, len(revs))
	for ref := range revs {
		refs = append(refs, ref)
	}
	slices.SortFunc(refs, func(a, b Ref) int {
		if a.Kind != b.Kind {
			return int(a.Kind) - int(b.Kind)
		}
		return bytes.Compare(a.ID[:], b.ID[:])
	})
	return refs
}

// encodeRef returns the public identifier of the entity ref, or its kind's
// name and its UUID if the kind has no prefix, which a valid kind always has.
func encodeRef(ref Ref) string {
	if !validKind(ref.Kind) {
		return fmt.Sprintf("%v:%s", ref.Kind, ref.ID)
	}
	return ids.Encode(ref.Kind, ref.ID)
}

// validKind reports whether k is a kind of entity of package ids.
func validKind(k ids.Kind) bool {
	return slices.Contains(ids.Kinds(), k)
}
