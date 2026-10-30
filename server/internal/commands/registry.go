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
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
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

// derivationForm is the form of a derivation's name (Derivation.Name), which
// no Type has, since it has no dot.
var derivationForm = regexp.MustCompile(`^[a-z]+(_[a-z]+)*$`)

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
	// SessionOnly says that no API token may apply the command, whatever its
	// scopes, as deleting and restoring the project, which FR-AUTH-04 makes
	// session-only, so that a leaked token cannot do through a batch what no
	// route lets it do. A batch with such a command applied with a token is
	// refused with ErrSessionRequired.
	SessionOnly bool
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
	// because an archived project is read-only (PRD FR-PROJ-01 AC2), unless
	// one of its commands unarchives the project (Unarchives).
	Archived bool
	// Trashed says that the command applies to a project in the trash too, as
	// restoring it does. The pipeline refuses a batch of a project in the
	// trash with any other command with ErrNotFound, as one of a project that
	// does not exist, whoever sends it (FR-PROJ-01 AC3), unless one of its
	// commands restores the project (Restores). A project in the trash may be
	// archived too, so a command with Trashed needs Archived.
	Trashed bool
	// Unarchives says that the command takes the project out of its archive,
	// as unarchiving it does, and Restores that it takes it out of the
	// trash, as restoring it does. A batch of an archived project with a
	// command that Unarchives is not refused for the project's archival, and
	// one of a project in the trash with a command that Restores not for the
	// trash, whatever their other commands, as a batch of its own that
	// unarchived or restored the project first would not be, so that the undo
	// of a batch that archived or deleted the project with other commands,
	// which unarchives or restores it, and undoes the others, which the
	// pipeline checks against the project as it is before the batch, applies
	// (PRD FR-EDIT-08). A project both archived and in the trash is archived
	// still once restored, so a batch of it that restores it applies other
	// commands only if it unarchives it too, or they apply to an archived
	// project (Archived). What a command that Restores counts of a key once
	// a batch of a project in the trash has applied is all that the batch
	// adds to the key (Tx.AddUsageOnceApplied). A command that Unarchives applies to an
	// archived project, and one that Restores to one in the trash, so
	// Unarchives needs Archived, and Restores Trashed.
	Unarchives, Restores bool
	// Check returns what is wrong with a payload that its schema cannot say,
	// such as an identifier of the wrong type, with each field's name in the
	// payload, as name or items[2].id, or nil. It reads nothing but p. The
	// pipeline asks it of every command of a batch, and of every step of an
	// inverse (checkSteps), so a rule that a value which a step writes back
	// from the database may break belongs in CheckNew instead.
	Check func(p P) input.FieldErrors
	// CheckNew returns what else is wrong with a payload as Check does: the
	// rules of a value that a command writes, which a stored value that the
	// step of an inverse writes back may break, as the text rules of package
	// input, which a change of input.IsControl or of Go's Unicode tables
	// tightens, or which a row that the pipeline did not write may break. The
	// pipeline asks it of every command of a batch, after Check, whatever
	// Check finds, and lists its problems after Check's, but not of a step, so
	// that a stored value that breaks it does not fail every change of it with
	// its inverse: the undo that sends the step back is refused for it then,
	// as any batch is. A step's payload still meets its schema, so a stored
	// value that a schema bounds, as by maxLength, is held to it by the
	// database too. It reads nothing but p.
	CheckNew func(p P) input.FieldErrors
	// Inverse returns the commands that undo the command, in the order to
	// apply them (§10.4). The pipeline calls it in the batch's transaction
	// once it has checked the command's revisions, right before Apply, so
	// that it reads the state that Apply changes, unless a command of the
	// batch is NonUndoable, which leaves the batch without an inverse. It
	// only reads, so it reports no change (Tx.Changed). The pipeline refuses
	// the command, at UndoField, once its steps would take the batch's undo
	// past the pipeline's bound (Options.MaxUndoBytes), unless it changes
	// nothing, whose steps the batch's undo leaves out (Result.Undo). An
	// Inverse whose steps grow with the project stops before (Tx.UndoRoom,
	// ErrUndoTooLarge), once they hold some of a part of the project that the
	// command changes, so that it does not refuse a command that changes
	// nothing either. Exactly one of Inverse and NonUndoable is set.
	Inverse func(ctx context.Context, tx *Tx, p P) ([]Step, error)
	// UndoField is the field of the payload, named as in its JSON, at which
	// the pipeline refuses the command when its inverse would take the
	// batch's undo past the pipeline's bound (Options.MaxUndoBytes): the
	// field whose change makes the inverse hold a part of the project that
	// grows with it, as an attribute's retype's type, whose inverse holds the
	// values that the retype loses, or "" for the payload itself, as for any
	// other command.
	UndoField string
	// NonUndoable marks a command without a meaningful inverse, whose batch
	// cannot be undone.
	NonUndoable bool
	// Apply applies the command in the batch's transaction, and reports each
	// entity that it changes with Tx.Changed, and what it adds to the
	// workspace's usage with Tx.AddUsage. An error refuses the whole batch,
	// which then changes nothing. A command whose Apply reports no change
	// changed nothing, and the batch's undo holds none of its inverse's
	// steps (Result.Undo).
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
// the type's payload does. The pipeline gives it the revisions that it
// expects, those of the entities of the kinds that its type touches that its
// command changed, as the batch left them, each once, as it gives them to
// every other step of the command's inverse whose type touches their kinds
// (Result.Undo), and the client that sends the undo gives it its command ID
// (plan item 37).
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

// Reconnection registers how the entities of a kind that refer to entities of
// another, as a connection refers to its nodes, come back when what they
// refer to is restored from the project's trash, "reconnecting references
// where targets still exist" (PRD FR-PROJ-06): the trash's
// reference-reconnection hook. A command that restores an entity reports it
// (Tx.Restored), and once every command of its batch has applied, the
// pipeline asks the reconnection of each kind that refers to it which of its
// entities are still not reconnected, which the batch's Result lists
// (Result.NotReconnected, AC1), each once, however many reconnections leave
// it, as a connection of a node that the batch restores with the node's
// board.
type Reconnection struct {
	// Restored is the kind of entity whose restoration reconnects what refers
	// to it, and Kind the kind of entity that refers to it.
	Restored, Kind ids.Kind
	// Left returns the entities of Kind that refer to the entities of Restored
	// whose IDs are among restored, which the batch restored, and that are
	// still not reconnected once every command of the batch has applied, each
	// with the entities that it waits for, in the order of their IDs. An
	// entity restored and deleted again by the batch refers to nothing that
	// came back, so it leaves nothing. It runs in the batch's transaction, and
	// only reads.
	Left func(ctx context.Context, tx *Tx, restored []ids.UUID) ([]Unreconnected, error)
}

// Unreconnected is an entity that refers to what a batch restored from the
// project's trash, which the batch could not reconnect, because it refers to
// other entities that are in the trash still, WaitingFor, as a connection of
// a restored node whose node at the other end is. It comes back once they do.
type Unreconnected struct {
	Ref
	WaitingFor []Ref
}

// Derivation registers what the pipeline derives of a project's content once
// every command of a batch has applied, in the batch's transaction, from what
// the batch's commands changed, as package expression compiles the sources
// that the commands wrote, and those that a change of what they name may
// compile otherwise (plan note N51): what depends on the batch as a whole,
// rather than on each command, so that a batch that creates a variable after
// the condition that reads it compiles the condition against it. What it
// changes is part of the batch, and commits exactly when the batch does.
type Derivation struct {
	// Name names the derivation, as a command that leaves work for it does
	// (Tx.Leave): lowercase words joined by underscores, without the dot of
	// a command's type, so that a Change, whose By holds the names of the
	// derivations among the types of the commands, never holds a name that
	// is a command's type too.
	Name string
	// Touches are the kinds of entity whose revisions Derive may change, as
	// a command's are (Spec.Touches): none for a derivation that changes no
	// entity's revision, as the index of what the texts name (package
	// dependency), which is no part of what the API shows of an entity.
	Touches []ids.Kind
	// Derive derives what the batch's commands' changes, changed, the
	// entities that they changed with their latest revisions, in the order of
	// their first change, each with the types of the commands that changed it,
	// leave to derive, and reports each entity that it changes with its new
	// revision (Tx.Changed), which the batch's Result lists, as it lists a
	// command's changes, but which the batch's undo does not expect, since the
	// undo's own commands derive it again. It runs once every command has
	// applied, in the order of the registrations, each derivation seeing what
	// those before it changed, as changes of the types that are their names.
	// The types let it pass over a change that cannot change what it derives,
	// as package expression passes over a board's rename, which no expression
	// names, rather than derive again all that the board holds, and Tx.Left
	// says whether a command left it work of its own.
	Derive func(ctx context.Context, tx *Tx, changed []Change) error
}

// Change is an entity that a batch changed, with its latest revision, as a
// derivation reads it (Derivation.Derive): By are the types of the commands
// that changed it, and the names of the derivations that did, each once, in
// the order of their first change of it.
type Change struct {
	Revision
	By []Type
}

// Registry holds the types of command, and the kinds of entity that they
// change (PRD §10.4): the one write path of a project's content. Register
// the kinds of entity, the types, the reconnections, and the derivations
// before New.
type Registry struct {
	specs    map[Type]*spec
	entities map[ids.Kind]Entity
	// reconnections are the reconnections of the kinds of entity that refer
	// to others, in the order of their registration.
	reconnections []Reconnection
	// derivations are the derivations, in the order of their registration,
	// each as a spec of no type of command, whose touches are its.
	derivations []derivation
	// schemas are the payloads' schemas, as the API's OpenAPI document writes
	// them, which validate the payloads.
	schemas huma.Registry
}

// checkTouches panics if a registered type of command touches a kind of
// entity that is not registered (RegisterEntity), as New checks of the
// registry that it makes a pipeline of.
func (r *Registry) checkTouches() {
	for _, typ := range slices.Sorted(maps.Keys(r.specs)) {
		for _, k := range r.specs[typ].touches {
			if _, ok := r.entities[k]; !ok {
				panic(fmt.Sprintf("commands: %s: the entity %s is not registered", typ, k))
			}
		}
	}
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{specs: map[Type]*spec{}, entities: map[ids.Kind]Entity{},
		schemas: huma.NewMapRegistry(SchemaPrefix, schemaName)}
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

// Undoable returns the registered types of command that have an inverse
// (Spec.Inverse), in order. Each of the others is marked as having none
// (Spec.NonUndoable), as Register makes sure, and its batches cannot be
// undone (PRD FR-EDIT-08 AC1).
func (r *Registry) Undoable() []Type {
	return slices.DeleteFunc(r.Types(), func(t Type) bool { return r.specs[t].inverse == nil })
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

// RegisterReconnection registers the reconnection rc of the entities of its
// kind that refer to its restored kind. It panics if rc lacks Left, if either
// of its kinds is not registered (RegisterEntity), or if the reconnection of
// its kind to its restored kind is registered already.
func RegisterReconnection(r *Registry, rc Reconnection) {
	for _, k := range []ids.Kind{rc.Restored, rc.Kind} {
		if _, ok := r.entities[k]; !ok {
			panic(fmt.Sprintf("commands: the reconnection of %s to %s: the entity %s is not registered", rc.Kind,
				rc.Restored, k))
		}
	}
	switch {
	case rc.Left == nil:
		panic(fmt.Sprintf("commands: the reconnection of %s to %s has no Left", rc.Kind, rc.Restored))
	case slices.ContainsFunc(r.reconnections, func(o Reconnection) bool {
		return o.Restored == rc.Restored && o.Kind == rc.Kind
	}):
		panic(fmt.Sprintf("commands: the reconnection of %s to %s is registered already", rc.Kind, rc.Restored))
	}
	r.reconnections = append(r.reconnections, rc)
}

// derivation is a registered Derivation, and the spec that Tx.Changed reads
// while it derives, of its name and its touches.
type derivation struct {
	Derivation
	spec *spec
}

// RegisterDerivation registers the derivation d. It panics if d lacks a name
// or Derive, if its name is not of its form (Derivation.Name), as one of a
// command's type is not, whether the type is registered yet or not, if it
// touches a kind that is not registered (RegisterEntity), or one twice, or if
// a derivation of its name is registered already.
func RegisterDerivation(r *Registry, d Derivation) {
	fail := func(format string, args ...any) {
		panic(fmt.Sprintf("commands: the derivation %q: ", d.Name) + fmt.Sprintf(format, args...))
	}
	switch {
	case d.Name == "":
		fail("a derivation needs a name")
	case !derivationForm.MatchString(d.Name):
		fail("a derivation's name is lowercase words joined by underscores, without the dot of a command's type")
	case d.Derive == nil:
		fail("a derivation needs Derive")
	case slices.ContainsFunc(r.derivations, func(o derivation) bool { return o.Name == d.Name }):
		fail("a derivation of the name is registered already")
	}
	for i, k := range d.Touches {
		if _, ok := r.entities[k]; !ok {
			fail("the entity %s is not registered", k)
		}
		if slices.Contains(d.Touches[:i], k) {
			fail("it touches %s twice", k)
		}
	}
	r.derivations = append(r.derivations, derivation{Derivation: d,
		spec: &spec{typ: Type(d.Name), touches: slices.Clone(d.Touches)}})
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
	// archived, trashed, unarchives, restores, and sessionOnly are
	// Spec.Archived, Spec.Trashed, Spec.Unarchives, Spec.Restores, and
	// Spec.SessionOnly, and undoField is Spec.UndoField.
	archived, trashed, unarchives, restores, sessionOnly bool
	undoField                                            string
	// decode decodes a payload that its schema allows, checks it, and
	// returns it: with Check, and with CheckNew too unless it is a step's.
	decode func(raw []byte, step bool) (payload any, problems input.FieldErrors)
	// inverse is nil for a command that cannot be undone.
	inverse func(ctx context.Context, tx *Tx, payload any) ([]Step, error)
	apply   func(ctx context.Context, tx *Tx, payload any) error
}

// Register registers the type of command s. It panics if s is not a
// declaration that the pipeline can apply: a Type of the wrong form or one
// that is registered already, a Capability that is not an operation of the
// permission matrix, or is authz.PublishSnapshot, whose row of the matrix a
// project's publish_min_role raises, which the pipeline would not read, so
// that a command that needs it checks authz.CheckPublish in its Apply, as a
// forced deletion does, a Scope that is not a scope, no Touches, a kind
// touched twice, a limit that the plan file does not check or twice, both Inverse and NonUndoable or neither, an UndoField without
// Inverse or that is no field of the payload's, Trashed without Archived, no
// Apply, or a payload that is not a struct, or that has a field with a
// default or a type that resolves itself, or that holds a rich-text document
// other than as a field of its own. A touched kind that is not registered
// (RegisterEntity) is refused once the registry makes a pipeline (New), so
// that two packages each of whose commands touch the other's kinds, as a
// node's speaker is a component, and a change of a component's key rewrites
// the expressions of nodes, register in either order.
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
	case s.Capability == authz.PublishSnapshot:
		fail("%q is held to a project's publish_min_role, which the pipeline does not read: check "+
			"authz.CheckPublish in Apply instead", s.Capability)
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
	case s.Unarchives && !s.Archived:
		fail("a command that unarchives the project applies to an archived one (Archived)")
	case s.Restores && !s.Trashed:
		fail("a command that restores the project applies to one in the trash (Trashed)")
	case s.Apply == nil:
		fail("the command has no Apply")
	case pt.Kind() != reflect.Struct:
		fail("the payload is a %s, not a struct", pt)
	}
	if problem := unapplied(pt); problem != "" {
		fail("%s", problem)
	}
	switch {
	case s.UndoField != "" && s.Inverse == nil:
		fail("a command without an inverse has no UndoField")
	case s.UndoField != "" && !slices.Contains(jsonFields(pt), s.UndoField):
		fail("the UndoField %q is not a field of the payload", s.UndoField)
	}
	if at := nestedDoc(pt); at != "" {
		fail("the payload's %s holds a rich-text document, which a payload holds only as a field of its own, a "+
			"richtext.Doc or a pointer to one, so that a document that Parse refuses is answered at its field", at)
	}
	for i, k := range s.Touches {
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
		trashed: s.Trashed, unarchives: s.Unarchives, restores: s.Restores, sessionOnly: s.SessionOnly,
		undoField: s.UndoField}
	for i, l := range s.Limits {
		if l.Adds != nil {
			sp.adds[i] = func(ctx context.Context, tx *Tx, payload any) (int64, error) {
				return l.Adds(ctx, tx, payload.(P))
			}
		}
	}
	docs := docFields(pt)
	sp.decode = func(raw []byte, step bool) (any, input.FieldErrors) {
		var p P
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if err := d.Decode(&p); err != nil {
			// A rich-text document that its schema allows but Parse refuses,
			// as one with lists nested too deep, is answered at its field,
			// joined with where in it the value that is wrong is (plan note
			// N40), with the error's message, which holds its pointer.
			var doc *richtext.Error
			if errors.As(err, &doc) {
				return nil, input.FieldErrors{{Field: docField(raw, docs, doc), Message: err.Error()}}
			}
			// The schema allows the payload, so this is only a value that
			// the schema cannot describe, such as an integer out of its
			// type's range.
			return nil, input.FieldErrors{{Message: "does not decode: " + err.Error()}}
		}
		var problems input.FieldErrors
		if s.Check != nil {
			problems = s.Check(p)
		}
		if s.CheckNew != nil && !step {
			problems = append(problems, s.CheckNew(p)...)
		}
		if len(problems) > 0 {
			return nil, problems
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

// docFields returns the names, in a payload's JSON, of the fields of the
// payload's type t that hold a rich-text document, a richtext.Doc or a
// pointer to one, in their order.
func docFields(t reflect.Type) []string {
	var out []string
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Type == reflect.TypeFor[richtext.Doc]() || f.Type == reflect.TypeFor[*richtext.Doc]() {
			out = append(out, jsonName(f))
		}
	}
	return out
}

// jsonFields returns the names, in a payload's JSON, of the exported fields
// of the payload's type t that encoding/json encodes, in their order.
func jsonFields(t reflect.Type) []string {
	var out []string
	for i := range t.NumField() {
		if f := t.Field(i); f.IsExported() && f.Tag.Get("json") != "-" {
			out = append(out, jsonName(f))
		}
	}
	return out
}

// jsonName returns the name of the field f in its struct's JSON.
func jsonName(f reflect.StructField) string {
	if name, _, _ := strings.Cut(f.Tag.Get("json"), ","); name != "" {
		return name
	}
	return f.Name
}

// docField returns the location within the payload raw of the value that err
// says is wrong in one of its documents, the fields named docs: the first of
// them that Parse refuses, in the order of the payload's fields, in which the
// decoder decodes them, and stops at the first that fails, an occurrence of a
// field that the payload holds twice among them, joined with where in it the
// value is, or "" for the payload itself if none of them is, as when the
// payload is not an object. Only a refused payload is read again, so a
// payload that applies is parsed once.
func docField(raw []byte, docs []string, err *richtext.Error) string {
	d := json.NewDecoder(bytes.NewReader(raw))
	if t, e := d.Token(); e != nil || t != json.Delim('{') {
		return ""
	}
	for d.More() {
		t, e := d.Token()
		if e != nil {
			return ""
		}
		var doc json.RawMessage
		if d.Decode(&doc) != nil {
			return ""
		}
		if name, _ := t.(string); slices.Contains(docs, name) && string(doc) != "null" {
			if _, parseErr := richtext.Parse(doc); parseErr != nil {
				return name + err.Location()
			}
		}
	}
	return ""
}

// nestedDoc returns where the payload's type t holds a rich-text document
// other than as a field of its own, a richtext.Doc or a pointer to one, as in
// a slice, a map, or a struct that a field holds, or "" if it holds none
// there: docField finds a document that Parse refuses only among t's own
// fields. It walks the types as unapplied does.
func nestedDoc(t reflect.Type) string {
	doc := reflect.TypeFor[richtext.Doc]()
	seen := map[reflect.Type]bool{}
	var walk func(t reflect.Type, at string) string
	walk = func(t reflect.Type, at string) string {
		for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array ||
			t.Kind() == reflect.Map {
			t = t.Elem()
		}
		switch {
		case t == doc:
			return at
		case seen[t] || t.Kind() != reflect.Struct:
			return ""
		}
		seen[t] = true
		for i := range t.NumField() {
			if f := t.Field(i); f.IsExported() {
				if found := walk(f.Type, at+"."+f.Name); found != "" {
					return found
				}
			}
		}
		return ""
	}
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() || f.Type == doc || f.Type == reflect.PointerTo(doc) {
			continue
		}
		if at := walk(f.Type, t.Name()+"."+f.Name); at != "" {
			return at
		}
	}
	return ""
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
// whose payload does not match its type's schema, Check, or CheckNew, or
// holds a NUL character, which no text may hold, or whose ExpectedRev names
// an entity of a kind that the command does not touch, or a revision that no
// entity has. The API answers it with validation_failed.
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

// decode checks the batch, which is applied to the project projectID, and
// returns its commands decoded, or an *InvalidError that lists every problem
// of the batch.
func (r *Registry) decode(batch []Command, projectID ids.UUID) ([]decoded, error) {
	out, problems := r.check(batch, nil, projectID)
	if len(problems) > 0 {
		return nil, &InvalidError{Problems: problems}
	}
	return out, nil
}

// check checks the batch, which is applied to the project projectID, but for
// its commands that skip names, which another check found unreadable, and
// returns its commands decoded, and every problem of the batch, in the order
// of its commands. Of the projects, a command may expect a revision of the
// batch's own alone: the check of the revisions would take another for one
// that no longer exists, which the client would show as deleted (FR-OFF-04
// AC5).
func (r *Registry) check(batch []Command, skip map[int]bool, projectID ids.UUID) ([]decoded, []Problem) {
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
				add("expected_rev."+c.key(ref), fmt.Sprintf("names a %s, which the command does not touch",
					ref.Kind))
			case ref.Kind == ids.Project && ref.ID != projectID:
				add("expected_rev."+c.key(ref), "names another project than the one that the batch is applied to")
			case c.ExpectedRev[ref] < 1:
				add("expected_rev."+c.key(ref), "is not a revision; an entity's first revision is 1")
			}
		}
		fields := r.validate(s.schema, c.Payload)
		var payload any
		if len(fields) == 0 {
			payload, fields = s.decode(c.Payload, false)
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
// so that an undo can apply it. It does not ask a step's CheckNew, whose
// rules the stored value that the step writes back may break: the undo that
// sends the step is refused for it then, as any batch is, rather than every
// change of the value now. A step that is not valid is the command's
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
			_, problems = s.decode(raw, true)
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
