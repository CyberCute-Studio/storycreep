package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"maps"
	"math"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/jobs"
	"github.com/CyberCute-Studio/storycreep/server/internal/role"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
)

// Tx is the transaction that a batch is applied in, as the functions of a
// command and of an entity see it: the queries' DBTX, which runs statements
// in it, with the batch's actor and project, and what the pipeline does in
// it for the commands. Everything that a command does through it commits
// exactly when the batch does. It has no method that commits or rolls back,
// which is the pipeline's to do once every command has applied, and a
// command runs no statement through Exec that ends the transaction either,
// as COMMIT or ROLLBACK.
type Tx struct {
	tx       pgx.Tx
	pipeline *Pipeline
	actor    Actor
	// roles are the roles that the actor acts with, by the scopes of the
	// operations that they authorise, which the pipeline authorised the
	// batch's commands by (Role).
	roles     map[authz.Scope]role.Role
	project   ids.UUID
	workspace ids.UUID
	// current is the command that the pipeline applies, or nil outside a
	// command's Apply, as while it reads the revisions or computes an
	// inverse, which only read, so that only Apply reports changes.
	current *spec
	// changed are the entities that the batch's commands changed, with their
	// latest revisions, in the order of their first change, and at their
	// indexes. revs are the revisions that the batch has seen of each entity,
	// read by the pipeline or reported by a command. by are the types of the
	// commands, and the names of the derivations, that changed each entity of
	// changed, at its index, each once, which a derivation reads (Change).
	changed []Revision
	by      [][]Type
	at      map[Ref]int
	revs    map[Ref]int64
	// steps are the steps of the inverse of the command that applies, none
	// for a batch that cannot be undone, and expecting the indexes of those
	// of them whose types touch each kind, which the pipeline sets before the
	// command's Apply: the command's undo expects the revision of each entity
	// of those kinds that the command changes, which Changed gives to each of
	// those steps, and expected holds the entities that the command's undo
	// expects, so that each step expects each once, however many times the
	// command reports it. The undo of each of the batch's commands that
	// changes an entity expects it, however many of them change it, and so
	// does each step of one command's undo whose type touches its kind, as
	// each deletion of the folders that a restore brought back expects all of
	// them, so that a part of the batch's undo sent as a batch of its own, as
	// one whose own undo would pass the bounds, expects every entity that it
	// writes back, wherever the client splits the undo, whose steps do not
	// say which command they undo (plan item 37).
	// changes is how many changes the command has reported (Changed): the
	// pipeline leaves out of the batch's undo the steps of a command that
	// reported none, which changed nothing, and has nothing to undo.
	steps     []logStep
	expecting map[ids.Kind][]int
	expected  map[Ref]struct{}
	changes   int
	// undoBytes is how many bytes the inverses that the pipeline has computed
	// of the batch's commands take, as the operation log keeps them: the
	// payloads of their steps (LogBytes), and the revisions that they expect
	// (ExpectedRevBytes), which Changed counts as the commands report them,
	// and stops a command for, with ErrUndoTooLarge, as soon as it is more
	// than maxUndo (UndoRoom). undoSteps is how many steps they have, which
	// Changed stops a command for once it is more than maxUndoSteps, alike.
	// The pipeline counts a command's steps before its Apply, and takes them
	// out again once it has reported no change, so both leave out the inverse
	// of a command that changed nothing, which the batch's undo leaves out.
	undoBytes, maxUndo      int
	undoSteps, maxUndoSteps int
	// usage is what the batch's commands add to each key of the workspace's
	// usage, and usageKeys the keys in the order of their first report.
	// checked is what the batch's commands declared that they add to each
	// limit (Spec.Limits), which is what the pipeline checked the limit for.
	usage     map[usage.Key]int64
	usageKeys []usage.Key
	checked   map[entitlements.LimitKey]int64
	// counts are what the batch's commands add to the workspace's usage once
	// every command of the batch has applied (AddUsageOnceApplied), in the
	// order of their reports, and trashed says that the batch's project was in
	// the workspace's trash before the batch, which a batch applies to only if
	// a command of it restores the project (Spec.Restores).
	counts  []usageCount
	trashed bool
	// restored are the entities that the batch's commands restored from the
	// project's trash (Restored), by their kinds, each once, in the order of
	// their first report, and reported the same entities as a set, which
	// keeps each once however many the batch restores.
	restored map[ids.Kind][]ids.UUID
	reported map[Ref]struct{}
	// left are the names of the derivations that the batch's commands left
	// work for (Leave).
	left map[string]bool
	// carried is what the inverse of the command that applies computed for
	// its Apply (Carry), which the pipeline clears before each command's
	// inverse and once its Apply returns.
	carried any
	// kept is what packages keep of the project for the rest of the batch
	// (Keep), by their keys.
	kept map[any]any
}

// usageCount is what the command spec adds to the workspace's usage of key
// once every command of its batch has applied: what count returns then
// (AddUsageOnceApplied).
type usageCount struct {
	spec  *spec
	key   usage.Key
	count func(ctx context.Context) (int64, error)
}

// Exec runs a statement in the batch's transaction.
func (t *Tx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return t.tx.Exec(ctx, sql, args...)
}

// Query runs a query in the batch's transaction.
func (t *Tx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return t.tx.Query(ctx, sql, args...)
}

// QueryRow runs a query that returns at most one row in the batch's
// transaction.
func (t *Tx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return t.tx.QueryRow(ctx, sql, args...)
}

// Role returns the role that the batch's actor acts with for an operation of
// the scope s (§5.5), which the pipeline authorised the batch's commands by
// (Spec.Capability): their effective role on the project for a
// project-scoped operation, and their role in its workspace for a
// workspace-scoped one, or role.None for a scope that the pipeline did not
// resolve, which allows nothing. A command whose payload asks for more than
// its type's operation allows, as a deletion that overrides what uses the
// entity, checks the actor's role for it with package authz.
func (t *Tx) Role(s authz.Scope) role.Role {
	if r, ok := t.roles[s]; ok {
		return r
	}
	return role.None
}

// Actor returns who applies the batch.
func (t *Tx) Actor() Actor {
	return t.actor
}

// ProjectID returns the project that the batch is applied to.
func (t *Tx) ProjectID() ids.UUID {
	return t.project
}

// WorkspaceID returns the workspace of the batch's project, which the
// transaction is scoped to (database.Scope).
func (t *Tx) WorkspaceID() ids.UUID {
	return t.workspace
}

// UndoRoom returns how many bytes the payloads of the steps of the inverse
// that the pipeline computes may take, as the operation log writes them
// (LogBytes), before they take the batch's undo, with the inverses of its
// commands before and the revisions that those expect, past the pipeline's
// bound (Options.MaxUndoBytes), which the pipeline refuses the command for as
// soon as its Apply reports a change (Changed), and not at all if it changes
// nothing, whose inverse the batch's undo leaves out. An
// Inverse whose steps hold a part of the project that grows with it, as the
// values that an attribute's retype loses, reads it to stop with
// ErrUndoTooLarge once what it holds takes more, rather than hold all of it
// only to be refused, and only once it holds some of that part, which the
// command changes: the steps of a command that changes nothing hold none of
// it, and the batch's undo leaves them out, however much they take, so an
// Inverse that stopped for them would refuse what the pipeline does not.
func (t *Tx) UndoRoom() int {
	return t.maxUndo - t.undoBytes
}

// Changed reports that the command changed the entity of the kind with the
// ID, which now has the revision rev, so that the operation's entity_refs,
// and the batch's Result, name it with that revision. An entity that several
// commands of the batch change is named once, with its latest revision. It
// returns an error, which the command returns to refuse the batch, unless it
// is called from the command's Apply, which alone changes anything (not from
// an Inverse, nor from an Entity's functions), or from a derivation's Derive
// (RegisterDerivation), the command, or the derivation, touches the kind
// (Spec.Touches), and rev is above every revision of the entity that the
// batch has seen, as it is when the command incremented it; and it returns
// ErrUndoTooLarge, which the pipeline refuses the command for as one whose
// inverse is too large, once the batch's undo, with the command's inverse,
// whose steps it holds as the command changes something, and the revision
// of the entity, which each step of the command's undo whose type touches
// its kind expects once, however many times the command reports it, is past
// the pipeline's bound (Options.MaxUndoBytes), or has more steps than its
// bound of steps (Options.MaxUndoSteps), so that a command whose inverse
// takes the undo past either, or that changes more entities than its undo
// could expect, stops there, rather than change the rest of them only to be
// refused. A command that changes many entities in one statement, as package
// component's touchType increments the revisions of every component of a
// type whose attribute definitions change, has changed all of them before it
// reports the first, and is refused all the same once it reports the one
// that takes the undo past a bound, its changes rolled back with the batch,
// so it reports first what it changes alone, as those commands report the
// definition, whose report is where the bounds refuse an inverse that is too
// large by itself, before that statement runs. Changed is where the pipeline
// holds the bounds: nothing adds to the batch's undo once the command's last
// change, and the pipeline leaves out of it the inverse of a command that
// reports no change, as one that changed nothing (Result.Undo), so a command
// reports every change that it makes.
func (t *Tx) Changed(kind ids.Kind, id ids.UUID, rev int64) error {
	ref := Ref{Kind: kind, ID: id}
	switch {
	case t.current == nil:
		return errors.New("commands: Changed is called outside a command's Apply")
	case !slices.Contains(t.current.touches, kind):
		return fmt.Errorf("commands: %s changed a %s, which it does not touch", t.current.typ, kind)
	case rev < 1:
		return fmt.Errorf("commands: %s gave %s the revision %d, but an entity's first revision is 1", t.current.typ,
			encodeRef(ref), rev)
	}
	if seen, ok := t.revs[ref]; ok && rev <= seen {
		return fmt.Errorf("commands: %s gave %s the revision %d, but it had %d already", t.current.typ,
			encodeRef(ref), rev, seen)
	}
	t.revs[ref] = rev
	t.changes++
	if steps := t.expecting[kind]; len(steps) > 0 {
		if _, ok := t.expected[ref]; !ok {
			t.expected[ref] = struct{}{}
			for _, j := range steps {
				t.steps[j].expects = append(t.steps[j].expects, ref)
			}
			t.undoBytes += len(steps) * ExpectedRevBytes(ref)
		}
	}
	if t.undoBytes > t.maxUndo || t.undoSteps > t.maxUndoSteps {
		return ErrUndoTooLarge
	}
	if i, ok := t.at[ref]; ok {
		t.changed[i].Rev = rev
		if !slices.Contains(t.by[i], t.current.typ) {
			t.by[i] = append(t.by[i], t.current.typ)
		}
		return nil
	}
	t.at[ref] = len(t.changed)
	t.changed = append(t.changed, Revision{Ref: ref, Rev: rev})
	t.by = append(t.by, []Type{t.current.typ})
	return nil
}

// Restored reports that the command restored the entity of the kind with the
// ID from the project's trash, as node.restore does a node, and board.restore
// a board, so that once every command of the batch has applied, the pipeline
// asks the reconnections of the kinds that refer to the kind
// (RegisterReconnection) which of what refers to it are still not
// reconnected (Result.NotReconnected). An entity that several commands of the
// batch restore is asked about once. It returns an error, which the command
// returns to refuse the batch, unless it is called from the command's Apply,
// and the command touches the kind (Spec.Touches), as Changed does.
func (t *Tx) Restored(kind ids.Kind, id ids.UUID) error {
	switch {
	case t.current == nil:
		return errors.New("commands: Restored is called outside a command's Apply")
	case !slices.Contains(t.current.touches, kind):
		return fmt.Errorf("commands: %s restored a %s, which it does not touch", t.current.typ, kind)
	}
	ref := Ref{Kind: kind, ID: id}
	if _, ok := t.reported[ref]; ok {
		return nil
	}
	if t.restored == nil {
		t.restored, t.reported = map[ids.Kind][]ids.UUID{}, map[Ref]struct{}{}
	}
	t.reported[ref] = struct{}{}
	t.restored[kind] = append(t.restored[kind], id)
	return nil
}

// Leave reports that the command leaves work for the derivation of the name
// (RegisterDerivation), which the pipeline runs once every command of the
// batch has applied, as one that writes an expression's source leaves
// package expression's to compile it, so that the derivation knows that the
// batch left it work (Left), and passes over one that left it none, as one
// that only moves nodes. It returns an error, which the command returns to
// refuse the batch, unless the registry has the derivation, so that a
// registry that lacks it fails the batch rather than commit what only the
// derivation would have made whole.
func (t *Tx) Leave(name string) error {
	if !slices.ContainsFunc(t.pipeline.registry.derivations, func(d derivation) bool { return d.Name == name }) {
		return fmt.Errorf("commands: the registry has no derivation %q, which a command leaves work for: register "+
			"it (RegisterDerivation)", name)
	}
	if t.left == nil {
		t.left = map[string]bool{}
	}
	t.left[name] = true
	return nil
}

// Left reports whether a command of the batch left work for the derivation
// of the name (Leave).
func (t *Tx) Left(name string) bool {
	return t.left[name]
}

// Carry keeps v, which a command's Inverse computed of the project, for the
// command's Apply, which the pipeline calls next, on the project as the
// inverse found it, so that Apply need not compute it again (Carried), as a
// rename's inverse plans the sources that its Apply writes. The pipeline drops
// it before the next command, and computes no inverse of a batch that cannot
// be undone, so an Apply computes what Carried does not hold itself.
func (t *Tx) Carry(v any) {
	t.carried = v
}

// Carried returns what the inverse of the command that applies kept for its
// Apply (Carry), or nil.
func (t *Tx) Carried() any {
	return t.carried
}

// ApplyPart applies, from the Apply of the command that applies, a command of
// the registered type typ with the payload, as a part of that command, as
// package clipboard's paste applies node.create, branch_output.create, and
// connection.create for what it pastes, so that a command that does what
// several of another type would does it through their Apply, with their
// checks, rather than a copy of them. The part's payload is held to its
// type's schema, Check, and CheckNew, as a command of a batch's is, and its
// Apply runs as the command's own: what it reports (Changed, Restored,
// AddUsage, Leave) is the command's, by the command's type, which the
// derivations read, and the command's undo is its own Inverse's steps, which
// undo the parts too, since the parts' inverses are not computed. A part
// needs nothing that the batch was not checked for: its type needs the
// command's capability and scope, touches only kinds that the command
// touches, adds to no limit that the command does not declare, and needs a
// session only if the command does; and it can be undone if the command can
// (Spec.NonUndoable), since a batch can be undone by its commands' types
// alone, and its undo would otherwise leave what the part did as it is. It
// returns an error, which the command returns, otherwise, and a refusal of
// the part, of its payload as its schema or its checks find it or of its
// Apply (Refuse), as the command's refusal at the field of the command's
// payload that at returns for the part's field, as name or items[2].id, or
// "" for the part's payload itself, so that the client is answered where its
// payload holds what is wrong. at is nil for a part whose fields are the
// command's payload's own.
func (t *Tx) ApplyPart(ctx context.Context, typ Type, payload any, at func(field string) string) error {
	if t.current == nil {
		return errors.New("commands: ApplyPart is called outside a command's Apply")
	}
	if at == nil {
		at = func(field string) string { return field }
	}
	parent := t.current
	part, ok := t.pipeline.registry.specs[typ]
	switch {
	case !ok:
		return fmt.Errorf("commands: %s applies a part of the type %s, which is not registered", parent.typ, typ)
	case part == parent:
		return fmt.Errorf("commands: %s applies a part of its own type", parent.typ)
	case part.capability != parent.capability || part.scope != parent.scope:
		return fmt.Errorf("commands: %s applies a part of the type %s, which needs %s and %s, where the command "+
			"was authorised for %s and %s", parent.typ, typ, part.capability, part.scope, parent.capability,
			parent.scope)
	case part.sessionOnly && !parent.sessionOnly:
		return fmt.Errorf("commands: %s applies a part of the type %s, which only a session may apply", parent.typ,
			typ)
	case part.inverse == nil && parent.inverse != nil:
		return fmt.Errorf("commands: %s applies a part of the type %s, which cannot be undone, where the command "+
			"can", parent.typ, typ)
	}
	for _, k := range part.touches {
		if !slices.Contains(parent.touches, k) {
			return fmt.Errorf("commands: %s applies a part of the type %s, which touches a %s, which the command "+
				"does not touch", parent.typ, typ, k)
		}
	}
	for _, l := range part.limits {
		if !slices.Contains(parent.limits, l) {
			return fmt.Errorf("commands: %s applies a part of the type %s, which adds to the limit %s, which the "+
				"command does not declare", parent.typ, typ, l)
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("commands: encode a part of the type %s: %w", typ, err)
	}
	problems := t.pipeline.registry.validate(part.schema, raw)
	var decoded any
	if len(problems) == 0 {
		decoded, problems = part.decode(raw, false)
	}
	if len(problems) > 0 {
		return Refuse(at(problems[0].Field), problems[0].Message, nil)
	}
	// What the command's inverse carried for its Apply is the command's, not
	// the part's (Carry), which has no inverse computed.
	carried := t.carried
	t.carried = nil
	err = part.apply(ctx, t, decoded)
	t.carried = carried
	var r *commandRefusal
	if errors.As(err, &r) {
		return &commandRefusal{field: at(r.field), message: r.message, reason: r.reason}
	}
	return err
}

// Keep keeps v under key for the rest of the batch, for the inverses and the
// Applies of the commands after the one that keeps it (Kept): what a package
// read of the project that later commands would read again, as package
// dependency keeps what each text that the batch has changed names, with the
// revision that it read it at, so that it reads a text again only once a
// command has changed it since (Changes). key is a value of a type of the
// package's own, as a context's key is, so that no two packages' keys are
// equal. What a batch keeps goes with it.
func (t *Tx) Keep(key, v any) {
	if t.kept == nil {
		t.kept = map[any]any{}
	}
	t.kept[key] = v
}

// Kept returns what the batch keeps under key (Keep), or nil.
func (t *Tx) Kept(key any) any {
	return t.kept[key]
}

// Changes yields the entities that the batch has changed so far, with their
// latest revisions, in the order of their first change, each with the types
// of the commands that changed it, as a derivation reads them
// (Derivation.Derive): for a command's inverse or Apply that reads what the
// derivations write only once every command has applied, as the index of
// what the texts name, and needs to know which of it the batch's commands
// before it may have changed, which it reads anew. It copies nothing, so
// that the commands of a batch that each read the changes, as 300 creations
// of variables after 2,000 of nodes, take no more than the reading: a
// change's By is the batch's own, which the caller reads as it is yielded,
// and neither changes nor keeps.
func (t *Tx) Changes() iter.Seq[Change] {
	return func(yield func(Change) bool) {
		for i, c := range t.changed {
			if !yield(Change{Revision: c, By: slices.Clip(t.by[i])}) {
				return
			}
		}
	}
}

// derive runs the registry's derivations, in the order of their
// registration, once every command of the batch has applied, each with what
// the batch has changed so far, its commands and the derivations before it,
// and the types of those that changed each entity. A derivation reports its
// changes as a command does (Changed), as one that touches its kinds, but no
// step of the batch's undo expects them.
func (t *Tx) derive(ctx context.Context) error {
	for _, d := range t.pipeline.registry.derivations {
		t.current, t.steps = d.spec, nil
		clear(t.expecting)
		clear(t.expected)
		changed := make([]Change, len(t.changed))
		for i, c := range t.changed {
			changed[i] = Change{Revision: c, By: slices.Clone(t.by[i])}
		}
		err := d.Derive(ctx, t, changed)
		t.current = nil
		if err != nil {
			return fmt.Errorf("commands: the derivation %s: %w", d.Name, err)
		}
	}
	return nil
}

// notReconnected returns what refers to the entities that the batch's
// commands restored (Restored) that the batch could not reconnect, once every
// command of the batch has applied, as each reconnection of a kind that
// refers to a restored kind finds it (Reconnection.Left): in the order of the
// restored kinds, and of the reconnections of each kind's referrers, as they
// were registered, each entity once, as the first reconnection that leaves it
// leaves it, so that a connection of a node that a batch restores with the
// node's board is listed once. It is never nil, as the Result of a batch
// answered from its record is not.
func (t *Tx) notReconnected(ctx context.Context) ([]Unreconnected, error) {
	out := []Unreconnected{}
	listed := map[Ref]struct{}{}
	for _, kind := range slices.Sorted(maps.Keys(t.restored)) {
		for _, rc := range t.pipeline.registry.reconnections {
			if rc.Restored != kind {
				continue
			}
			left, err := rc.Left(ctx, t, t.restored[kind])
			if err != nil {
				return nil, fmt.Errorf("commands: what the restored %s entities leave of %s: %w", kind, rc.Kind, err)
			}
			for _, u := range left {
				if u.Kind != rc.Kind {
					return nil, fmt.Errorf("commands: the reconnection of %s to %s left %s, which is not of its kind",
						rc.Kind, kind, encodeRef(u.Ref))
				}
				if _, ok := listed[u.Ref]; ok {
					continue
				}
				listed[u.Ref] = struct{}{}
				out = append(out, u)
			}
		}
	}
	return out, nil
}

// AddUsage reports that the command adds delta to the workspace's usage of
// key (package usage, PRD FR-ENT-02), or takes -delta from it, as creating a
// node adds 1 to nodes, and deleting a board with its nodes takes them. Once
// every command of the batch has applied, the pipeline records what they add
// to each key together, as one delta, in the batch's transaction, so that it
// commits exactly when the batch does. It returns an error, which the command
// returns to refuse the batch, unless it is called from the command's Apply,
// or if it adds to a key that names a limit of the plans that the command
// does not declare (Spec.Limits), so that the pipeline checked it before the
// batch applied (FR-ENT-01). The pipeline checked the limit for what the
// batch's commands declared that they add to it (Limit.Adds), so it refuses
// a batch whose commands add more to it than that (recordUsage).
func (t *Tx) AddUsage(key usage.Key, delta int64) error {
	switch {
	case t.current == nil:
		return errors.New("commands: AddUsage is called outside a command's Apply")
	case !key.Known():
		return fmt.Errorf("commands: %s adds to %q, which is not a key of usage", t.current.typ, key)
	case delta > 0 && entitlements.LimitKey(key).Checked() &&
		!slices.Contains(t.current.limits, entitlements.LimitKey(key)):
		return fmt.Errorf("commands: %s adds to %s, a limit of the plans that it does not declare, so nothing "+
			"checked it", t.current.typ, key)
	}
	sum, seen := t.usage[key]
	if delta > 0 && sum > math.MaxInt64-delta || delta < 0 && sum < math.MinInt64-delta {
		return fmt.Errorf("commands: what the batch adds to %s does not fit in 64 bits", key)
	}
	if !seen {
		t.usageKeys = append(t.usageKeys, key)
	}
	t.usage[key] = sum + delta
	return nil
}

// AddUsageOnceApplied reports that the command adds to the workspace's usage
// of key what count returns once every command of the batch has applied, as
// AddUsage adds a delta, for a delta that depends on what the batch's later
// commands do: as deleting a project takes its boards from the workspace's,
// among them those that a later command of the batch creates or restores,
// whose own deltas add them, so that the batch's deltas add up to the same
// whatever the order of its commands. The pipeline calls count in the batch's
// transaction, after the last command's Apply, and adds what it returns as
// the command's AddUsage would, which may refuse the batch then. A batch of a
// project in the trash, which applies only because a command of it restores
// the project (Spec.Restores), adds to a key that such a command counts so
// what the counts return alone, and what its commands added to the key with
// AddUsage counts for nothing: the project added nothing to the key before
// the batch, as it was in the trash, and its restoration counts what the
// project adds to it once the batch has applied, which those deltas, of
// changes that they count as of a project not in the trash, are part of
// already. It returns an error, which the command returns to refuse the
// batch, unless it is called from the command's Apply.
func (t *Tx) AddUsageOnceApplied(key usage.Key, count func(ctx context.Context) (int64, error)) error {
	if t.current == nil {
		return errors.New("commands: AddUsageOnceApplied is called outside a command's Apply")
	}
	t.counts = append(t.counts, usageCount{spec: t.current, key: key, count: count})
	return nil
}

// addCounts adds to the workspace's usage what each count that the batch's
// commands reported (AddUsageOnceApplied) returns, once every command of the
// batch has applied, as the command that reported it would add it, and, in a
// batch of a project in the trash, drops what the commands added with
// AddUsage to each key that a command that restores the project counts so.
func (t *Tx) addCounts(ctx context.Context) error {
	if t.trashed {
		for _, c := range t.counts {
			// A key that no command added to has no sum, which AddUsage below
			// takes for its first report.
			if _, added := t.usage[c.key]; added && c.spec.restores {
				t.usage[c.key] = 0
			}
		}
	}
	for _, c := range t.counts {
		delta, err := c.count(ctx)
		if err != nil {
			return fmt.Errorf("commands: what %s adds to %s: %w", c.spec.typ, c.key, err)
		}
		t.current = c.spec
		err = t.AddUsage(c.key, delta)
		t.current = nil
		if err != nil {
			return err
		}
	}
	return nil
}

// recordUsage records what the batch's commands added to each key of the
// workspace's usage (AddUsage), in the order of the keys' first reports, as
// one delta of each key, unless they add up to nothing. It refuses the batch
// if its commands added more to a limit than they declared (Spec.Limits),
// which is all that the pipeline checked the limit for, as with a command
// whose Limit.Adds says 0, which Check allows without reading the usage, let
// alone holding the lock of a key counted exactly (FR-ENT-01).
func (t *Tx) recordUsage(ctx context.Context) error {
	for _, key := range t.usageKeys {
		limit := entitlements.LimitKey(key)
		if added := t.usage[key]; added > 0 && limit.Checked() && added > t.checked[limit] {
			return fmt.Errorf("commands: the batch adds %d to %s, but its commands declared %d, which is all that "+
				"the limit was checked for", added, key, t.checked[limit])
		}
		if err := usage.Record(ctx, t.tx, t.workspace, key, t.usage[key]); err != nil {
			return fmt.Errorf("commands: %w", err)
		}
	}
	return nil
}

// Enqueue enqueues a job in the batch's transaction (package jobs), so that
// the job exists, and runs, only if the batch commits. It returns an error if
// the pipeline has no job queue.
func (t *Tx) Enqueue(ctx context.Context, args jobs.Args, opts *jobs.EnqueueOptions) (jobs.Enqueued, error) {
	if t.pipeline.jobs == nil {
		return jobs.Enqueued{}, fmt.Errorf("commands: the pipeline has no job queue to enqueue %s in", args.Kind())
	}
	return t.pipeline.jobs.Enqueue(ctx, t.tx, args, opts)
}

// Audit records e in the audit log in the batch's transaction (package
// audit), so that the entry commits exactly when the batch does (PRD
// FR-TEN-07 AC3), as an event of the batch's workspace whose actor is the
// batch's: its WorkspaceID, ActorUserID, ActorTokenID, IP, and UserAgent are
// the batch's, whatever e holds.
func (t *Tx) Audit(ctx context.Context, e audit.Entry) error {
	e.WorkspaceID = t.workspace
	e.ActorUserID, e.ActorTokenID = t.actor.UserID, t.actor.Token.TokenID()
	e.IP, e.UserAgent = t.actor.Client.IP, t.actor.Client.UserAgent
	return audit.Record(ctx, t.tx, e)
}
