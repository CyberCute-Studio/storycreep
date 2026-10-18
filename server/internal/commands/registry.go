package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
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
// Neither P nor a type that it holds has a field with a default, or resolves
// itself (huma.Resolver), as the type of a request's body of the API may,
// because the API gives a body those, but a payload means what its JSON says,
// and Check checks it.
type Spec[P any] struct {
	Type Type
	// Capability is the operation of the permission matrix that the command
	// needs (PRD §5.6), which the actor's role must allow: their effective
	// role on the project for a project-scoped operation, and their role in
	// the workspace for a workspace-scoped one (§5.5).
	Capability authz.Operation
	// Scope is the scope that an API token needs for the command (PRD
	// FR-AUTH-04), whatever its owner's role allows: project:write for a
	// change of the draft, as every command of a project's content is, and
	// snapshot:write for publishing a snapshot. A batch applied with a token
	// that does not hold every command's scope is refused with an
	// *scope.InsufficientError that names the first that it lacks.
	Scope scope.Scope
	// Touches are the kinds of entity that the command changes or refers to,
	// each registered with RegisterEntity. A command's ExpectedRev may name
	// an entity of these kinds only, and Apply reports a change of these
	// kinds only (Tx.Changed).
	Touches []ids.Kind
	// Limits are the limits of the workspace's plan that the command adds to,
	// which the pipeline checks before it applies the batch (FR-ENT-01). Its
	// Apply reports what it adds to them, as to any key of usage, with
	// Tx.AddUsage, which refuses to add to a limit that is not here, and the
	// pipeline refuses a batch that adds more to a limit than its commands'
	// Adds say, which is all that it checked the limit for.
	Limits []Limit[P]
	// Archived says that the command applies to an archived project too, as
	// unarchiving, deleting, and restoring it do. The pipeline refuses a batch
	// of an archived project with any other command with ErrProjectArchived,
	// because an archived project is read-only (PRD FR-PROJ-01 AC2).
	Archived bool
	// Trashed says that the command applies to a project in the trash too, as
	// restoring it does. The pipeline refuses a batch of a project in the
	// trash with any other command with ErrNotFound, as one of a project that
	// does not exist, whoever sends it (FR-PROJ-01 AC3). A project in the
	// trash may be archived too, so a command with Trashed needs Archived.
	Trashed bool
	// Check returns what is wrong with a payload that its schema cannot say,
	// such as an identifier of the wrong type, with each field's name in the
	// payload, as name or items[2].id, or nil. It reads nothing but p.
	Check func(p P) input.FieldErrors
	// Inverse returns the commands that undo the command, in the order to
	// apply them (§10.4). The pipeline calls it in the batch's transaction
	// once it has checked the command's revisions, right before Apply, so
	// that it reads the state that Apply changes, unless a command of the
	// batch is NonUndoable, which leaves the batch without an inverse. It
	// only reads, so it reports no change (Tx.Changed). Exactly one of
	// Inverse and NonUndoable is set.
	Inverse func(ctx context.Context, tx *Tx, p P) ([]Step, error)
	// NonUndoable marks a command without a meaningful inverse, whose batch
	// cannot be undone.
	NonUndoable bool
	// Apply applies the command in the batch's transaction, and reports each
	// entity that it changes with Tx.Changed, and what it adds to the
	// workspace's usage with Tx.AddUsage. An error refuses the whole batch,
	// which then changes nothing.
	Apply func(ctx context.Context, tx *Tx, p P) error
}

// Limit is a limit of the workspace's plan that a command adds to.
type Limit[P any] struct {
	Key entitlements.LimitKey
	// Adds returns how much of the limit the command with the payload p adds,
	// or nil for 1. The pipeline calls it in the batch's transaction, once it
	// holds the project's lock, before any command of the batch applies, so
	// that it reads the project as the batch finds it, as when unarchiving a
	// project adds to the projects only if the project is archived. It only
	// reads, so it reports no change (Tx.Changed).
	Adds func(ctx context.Context, tx *Tx, p P) (int64, error)
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
// functions read the entities of the batch's project in the batch's
// transaction. The pipeline applies a project's batches one at a time, under
// a lock of the project's row, so what they read stays as it is until a
// command of the batch changes it.
type Entity struct {
	Kind ids.Kind
	// Revs returns, by their IDs, the revisions of the project's entities of
	// the kind whose IDs are among entities, and leaves out an ID that the
	// project has no such entity of, as when it never existed or has been
	// purged. An entity in the trash still exists. The pipeline asks once for
	// every entity of the kind that a batch expects a revision of, so that a
	// batch over many entities costs one query of each kind.
	Revs func(ctx context.Context, tx *Tx, entities []ids.UUID) (map[ids.UUID]int64, error)
	// States returns, by their IDs, the project's entities of the kind whose
	// IDs are among entities, each as it is now, as the API shows it, which a
	// client that sent a stale revision is answered with
	// (RevisionConflictError). Each encodes as JSON. As with Revs, the
	// pipeline asks once for every entity of the kind that a batch expects a
	// stale revision of, each of which Revs has just found.
	States func(ctx context.Context, tx *Tx, entities []ids.UUID) (map[ids.UUID]any, error)
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
		schemas: huma.NewMapRegistry("#/components/schemas/", schemaName)}
}

// schemaName names the schema of a payload, and of each struct that it
// holds, as huma.DefaultSchemaNamer does, after the Go type, but with the
// path of the type's package before it, so that the types of two packages
// that share a name, as the CreatePayload of two kinds of entity, each have a
// schema of their own, where huma's registry would refuse the second with a
// panic. The registry only validates payloads, so the names are never shown.
func schemaName(t reflect.Type, hint string) string {
	name := huma.DefaultSchemaNamer(t, hint)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.PkgPath() == "" {
		return name
	}
	return strings.Map(func(r rune) rune {
		if 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' {
			return r
		}
		return '_'
	}, t.PkgPath()) + "_" + name
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
	case e.Revs == nil || e.States == nil:
		panic(fmt.Sprintf("commands: the entity %s needs both Revs and States", e.Kind))
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
	scope      scope.Scope
	touches    []ids.Kind
	// payload is the Go type of the payload, and schema its schema, which
	// validates it.
	payload reflect.Type
	schema  *huma.Schema
	limits  []entitlements.LimitKey
	// adds return how much the command adds to each of limits, or nil for 1.
	adds []func(ctx context.Context, tx *Tx, payload any) (int64, error)
	// archived and trashed are Spec.Archived and Spec.Trashed.
	archived, trashed bool
	// decode decodes a payload that its schema allows, checks it, and
	// returns it.
	decode func(raw []byte) (payload any, problems input.FieldErrors)
	// inverse is nil for a command that cannot be undone.
	inverse func(ctx context.Context, tx *Tx, payload any) ([]Step, error)
	apply   func(ctx context.Context, tx *Tx, payload any) error
}

// Register registers the type of command s. It panics if s is not a
// declaration that the pipeline can apply: a Type of the wrong form or one
// that is registered already, a Capability that is not an operation of the
// permission matrix, a Scope that is not a scope, no Touches, a touched kind
// that is not registered or twice, a limit that the plan file does not check
// or twice, both Inverse and NonUndoable or neither, Trashed without
// Archived, no Apply, or a payload that is not a struct, or that has a field
// with a default or a type that resolves itself.
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
	case !s.Scope.Known():
		fail("%q is not a scope of an API token", s.Scope)
	case len(s.Touches) == 0:
		fail("a command touches at least one kind of entity")
	case (s.Inverse != nil) == s.NonUndoable:
		fail("exactly one of Inverse and NonUndoable is set")
	case s.Trashed && !s.Archived:
		// A project in the trash may be archived too, and the pipeline would
		// refuse a batch of it with ErrProjectArchived, which tells that the
		// project exists, where any other batch of it is not found.
		fail("a command that applies to a project in the trash applies to an archived one too (Archived)")
	case s.Apply == nil:
		fail("the command has no Apply")
	case pt.Kind() != reflect.Struct:
		fail("the payload is a %s, not a struct", pt)
	}
	if problem := unapplied(pt); problem != "" {
		fail("%s", problem)
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
		if !l.Key.Checked() {
			fail("%q is not a limit that the plan file checks", l.Key)
		}
		if slices.Contains(keys[:i], l.Key) {
			fail("the limit %s is added to twice", l.Key)
		}
		keys[i] = l.Key
	}
	sp := &spec{typ: s.Type, capability: s.Capability, scope: s.Scope, touches: slices.Clone(s.Touches),
		payload: pt, schema: r.schemas.Schema(pt, true, ""), limits: keys,
		adds: make([]func(context.Context, *Tx, any) (int64, error), len(s.Limits)), archived: s.Archived,
		trashed: s.Trashed}
	for i, l := range s.Limits {
		if l.Adds != nil {
			sp.adds[i] = func(ctx context.Context, tx *Tx, payload any) (int64, error) {
				return l.Adds(ctx, tx, payload.(P))
			}
		}
	}
	sp.decode = func(raw []byte) (any, input.FieldErrors) {
		var p P
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if err := d.Decode(&p); err != nil {
			// The schema allows the payload, so this is only a value that
			// the schema cannot describe, such as an integer out of its
			// type's range.
			return nil, input.FieldErrors{{Message: "does not decode: " + err.Error()}}
		}
		if s.Check != nil {
			if problems := s.Check(p); len(problems) > 0 {
				return nil, problems
			}
		}
		return p, nil
	}
	if s.Inverse != nil {
		sp.inverse = func(ctx context.Context, tx *Tx, payload any) ([]Step, error) {
			return s.Inverse(ctx, tx, payload.(P))
		}
	}
	sp.apply = func(ctx context.Context, tx *Tx, payload any) error { return s.Apply(ctx, tx, payload.(P)) }
	r.specs[s.Type] = sp
}

// resolvers are the interfaces of a type that resolves itself, whose method
// huma calls on a request's body once the body decodes.
var resolvers = []reflect.Type{reflect.TypeFor[huma.Resolver](), reflect.TypeFor[huma.ResolverWithPath]()}

// unapplied returns what the payload's type t has that the API would apply to
// a request's body of the type, but decode does not apply to a payload, or ""
// if it has nothing: a field, of t or of a struct that t holds, with a
// default, which huma fills in where a body leaves the field out, or a type
// that resolves itself, which huma calls once a body decodes. It walks the
// types as huma does, through pointers, slices, arrays, and maps to the
// exported fields of structs.
func unapplied(t reflect.Type) string {
	seen := map[reflect.Type]bool{}
	var walk func(t reflect.Type, at string) string
	walk = func(t reflect.Type, at string) string {
		for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array ||
			t.Kind() == reflect.Map {
			t = t.Elem()
		}
		if seen[t] {
			return ""
		}
		seen[t] = true
		if slices.ContainsFunc(resolvers, reflect.PointerTo(t).Implements) {
			return fmt.Sprintf("the payload's %s is a %s, which resolves itself, as a request's body may, but a "+
				"payload never does; its Check checks it instead", at, t)
		}
		if t.Kind() != reflect.Struct {
			return ""
		}
		for i := range t.NumField() {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name := at + "." + f.Name
			if f.Tag.Get("default") != "" {
				return fmt.Sprintf("the payload's field %s has a default, which a request's body is given where it "+
					"leaves the field out, but a payload never is", name)
			}
			if problem := walk(f.Type, name); problem != "" {
				return problem
			}
		}
		return ""
	}
	return walk(t, t.Name())
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
// whose payload does not match its type's schema or Check, or holds a NUL
// character, which no text may hold, or whose ExpectedRev names an entity of
// a kind that the command does not touch, or a revision that no entity has.
// The API answers it with validation_failed.
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
	// with. The log's jsonb keeps an object's keys in an order of its own,
	// not in this encoding's.
	encoded []byte
}

// decode checks the batch, and returns its commands decoded, or an
// *InvalidError that lists every problem of the batch.
func (r *Registry) decode(batch []Command) ([]decoded, error) {
	out, problems := r.check(batch, nil)
	if len(problems) > 0 {
		return nil, &InvalidError{Problems: problems}
	}
	return out, nil
}

// check checks the batch, but for its commands that skip names, which
// another check found unreadable, and returns its commands decoded, and
// every problem of the batch, in the order of its commands.
func (r *Registry) check(batch []Command, skip map[int]bool) ([]decoded, []Problem) {
	if len(batch) == 0 {
		return nil, []Problem{{Command: -1, Message: "has no commands"}}
	}
	var problems []Problem
	out := make([]decoded, len(batch))
	first := map[ids.UUID]int{}
	for i, c := range batch {
		if skip[i] {
			continue
		}
		add := func(field, msg string) { problems = append(problems, Problem{Command: i, Field: field, Message: msg}) }
		switch j, dup := first[c.ID]; {
		case c.ID == (ids.UUID{}):
			add("command_id", "is missing")
		case dup:
			add("command_id", fmt.Sprintf("is the ID of command %d of the batch too", j))
		default:
			first[c.ID] = i
		}
		s, ok := r.specs[c.Type]
		if !ok {
			add("type", "is not a type of command")
			continue
		}
		// An entity's identifier is encoded only for the field of a problem,
		// which a valid expected revision has none of.
		for _, ref := range sortedRefs(c.ExpectedRev) {
			switch {
			case !slices.Contains(s.touches, ref.Kind):
				add("expected_rev."+encodeRef(ref), fmt.Sprintf("names a %s, which the command does not touch",
					ref.Kind))
			case c.ExpectedRev[ref] < 1:
				add("expected_rev."+encodeRef(ref), "is not a revision; an entity's first revision is 1")
			}
		}
		fields := r.validate(s.schema, c.Payload)
		var payload any
		if len(fields) == 0 {
			payload, fields = s.decode(c.Payload)
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
		out[i] = decoded{Command: c, spec: s, payload: payload, encoded: encoded}
	}
	return out, problems
}

// validate returns what is wrong with the payload raw under the schema, with
// each field's location in the payload, as name or items[2].id, or "" for
// the payload itself, and a string that holds a NUL character, which the
// schema allows, but neither the operation log's jsonb nor a text column can
// hold, so that the batch is refused as invalid rather than fail on its
// write.
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
	if at, ok := nulAt(v, ""); ok {
		out = append(out, input.FieldError{Field: at, Message: "holds a NUL character, which no text may hold"})
	}
	return out
}

// nulAt returns where in the JSON value v, which is at, as name or
// items[2].id, a string holds a NUL character, or the object whose key does,
// or ok false if none does.
func nulAt(v any, at string) (where string, ok bool) {
	switch v := v.(type) {
	case string:
		return at, strings.ContainsRune(v, 0)
	case []any:
		for i, e := range v {
			if where, ok := nulAt(e, fmt.Sprintf("%s[%d]", at, i)); ok {
				return where, true
			}
		}
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(v)) {
			if strings.ContainsRune(k, 0) {
				return at, true
			}
			field := k
			if at != "" {
				field = at + "." + k
			}
			if where, ok := nulAt(v[k], field); ok {
				return where, true
			}
		}
	}
	return "", false
}

// checkSteps returns the steps of an inverse as the operation log keeps
// them, each with its payload encoded once, or an error unless every step
// is a registered type of command whose payload its schema and Check allow,
// so that an undo can apply it. A step that is not valid is the command's
// failure, not the batch's, whose payloads are valid, so its error does not
// wrap the step's input.FieldErrors, which a caller would take for problems
// of the payloads that it sent.
func (r *Registry) checkSteps(steps []Step) ([]logStep, error) {
	out := make([]logStep, len(steps))
	for i, st := range steps {
		s, ok := r.specs[st.Type]
		if !ok {
			return nil, fmt.Errorf("step %d has the type %q, which is not registered", i, st.Type)
		}
		raw, err := json.Marshal(st.Payload)
		if err != nil {
			return nil, fmt.Errorf("step %d (%s) does not encode: %w", i, st.Type, err)
		}
		problems := r.validate(s.schema, raw)
		if len(problems) == 0 {
			_, problems = s.decode(raw)
		}
		if len(problems) > 0 {
			return nil, fmt.Errorf("step %d (%s) is not valid: %v", i, st.Type, problems)
		}
		out[i] = logStep{Type: st.Type, Payload: raw}
	}
	return out, nil
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
