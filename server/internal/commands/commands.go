// Package commands is the command pipeline (PRD §5.7, §10.2, §10.4, ADR-004,
// ADR-006): the one write path of a project's content. Every change of
// content is a typed command, declared once in a Registry with its payload's
// type, the operation of the permission matrix that it needs, the limits of
// the plan that it adds to, its inverse, and the kinds of entity that it
// touches, and the web app, the API, the importer, and the offline replay
// all send the same commands, so that one validation and one authorisation
// hold them all.
//
// A batch of commands is applied to one project, atomically, by Apply, in
// the order of the steps of §10.2: its commands are decoded strictly and
// validated against their payloads' schemas, before the database is read;
// the actor's roles are resolved (§5.5), and each command's operation is
// authorised by the role that its scope names; the project's row is locked,
// so that the project's batches are applied one at a time; a batch of an
// archived project, which is read-only, or of one in the trash, is refused
// unless each of its commands applies there, as unarchiving and restoring
// the project do (FR-PROJ-01), or one of them unarchives it, or restores it,
// respectively, which lets the others apply too, as the undo of a batch that
// archived or deleted the project with others needs (a project both archived
// and in the trash needs both); the workspace's limits are checked
// for what the batch adds (FR-ENT-01); every expected
// revision of the batch's commands is checked against the project as it is
// before the batch, which is what the client authored the batch against, and
// a stale one refuses the whole batch with the entities' current state
// (FR-API-03 AC3); and the commands are applied, in order, in one
// transaction. A batch that applies is appended to the project's operation
// log with the project's next seq, its inverse, which its Result holds as its
// undo (Result.Undo), and the entities that it changed, and each of its
// commands is recorded in applied_command, which
// keeps it recognised for as long as the project exists. The audit entries
// and the jobs of its commands, and what they add to the workspace's usage,
// are written in the same transaction (Tx.Audit, Tx.Enqueue, Tx.AddUsage),
// and once it commits, the Notifier is told its seq (§10.2 step 7). A batch
// that does not apply changes nothing of the project at all (FR-API-03 AC2):
// at most, its refusal is remembered, in its idempotency record.
//
// A batch applied with an API token is authorised by its owner's roles, as
// any other, and by the token on top (FR-AUTH-04, §5.6): a token that does
// not cover the project, as one of another workspace, or one narrowed to
// another project, is told that the project does not exist, before anything
// of it is read (AC3), and one that does not hold the scope of every command
// of the batch (Spec.Scope), as project:write for a change of the draft, is
// refused with a *scope.InsufficientError that names the scope (AC2),
// whatever the owner's role.
//
// A batch applied with an API token with a command that only a session may
// apply (Spec.SessionOnly), as deleting the project, which FR-AUTH-04 makes
// session-only, is refused with ErrSessionRequired, whatever its scopes. A
// token's refusal is recorded in the audit log, as token.use_denied, once a
// minute at most for each token, as the refusals of the routes are
// (NFR-SEC-05).
//
// A command's ID makes it idempotent (FR-API-03). Every batch that a client
// sent (Parse) that applies, or that its project's state refuses before any
// of its commands applies, which is archived, past a limit, or of stale
// revisions, has an idempotency record of its answer, which the pruning job
// keeps for RecordRetention (Prune); every command of a batch that applies,
// the server's own too, has a row of applied_command, which is kept for as
// long as the project exists. A batch sent again, once its actor is
// authorised and the project's lock is held, before the project's state or
// the workspace's limits are checked (§10.2 step 4), is answered from them,
// and nothing applies again: as it was answered, if it is the batch of a
// record, with the same commands in the same order; as AlreadyApplied, if all
// of its commands were applied otherwise; and with an
// *IdempotencyMismatchError if only some of them were. A refusal that is not
// recorded, as of a batch that is not valid, or of an actor that may not
// apply it, is checked anew when the batch is sent again. A command's payload
// is not compared with the one that its ID was applied with: the ID is the
// command.
//
// The API's command endpoint sends the pipeline its clients' batches, in the
// JSON that Parse reads, and the API's OpenAPI document describes every type
// of command, with its payload's schema (Registry.Document).
package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/jobs"
	"github.com/CyberCute-Studio/storycreep/server/internal/role"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
)

var (
	// ErrNotFound is returned for a batch of a project that does not exist,
	// or on which the actor's effective role is none, as it is for a user who
	// is not a member of its workspace, or a guest without a grant on it
	// (§5.5), so that nothing tells them that it exists (FR-TEN-04 AC6), for
	// one of a project in the trash with a command that does not apply there
	// (Spec.Trashed), unless the batch restores the project (Spec.Restores),
	// and for one of a project whose workspace's deletion is
	// pending, which blocks every access to the workspace at once (FR-TEN-05
	// AC1).
	ErrNotFound = errors.New("commands: no such project, or the user has no role on it")
	// ErrProjectArchived is returned for a batch of an archived project with
	// a command that does not apply to an archived project (Spec.Archived),
	// which is read-only (FR-PROJ-01 AC2), unless the batch unarchives the
	// project (Spec.Unarchives).
	ErrProjectArchived = errors.New("commands: the project is archived")
	// ErrInsufficientRole is returned for a batch with a command whose
	// operation the actor's role does not allow (§5.6), and ErrOwnerRequired
	// for one whose operation only an owner may do, as package authz decides.
	ErrInsufficientRole = authz.ErrInsufficientRole
	ErrOwnerRequired    = authz.ErrOwnerRequired
	// ErrInsufficientScope is returned, as a *scope.InsufficientError that
	// names the scope, for a batch applied with an API token that does not
	// hold the scope of one of its commands (Spec.Scope, FR-AUTH-04 AC2).
	ErrInsufficientScope = scope.ErrInsufficientScope
	// ErrSessionRequired is returned for a batch applied with an API token
	// with a command that no token may apply, whatever its scopes
	// (Spec.SessionOnly, FR-AUTH-04), as deleting the project. The API
	// answers it with 403 session_required.
	ErrSessionRequired = errors.New("commands: only a session may apply the command, not an API token")
)

// Ref names an entity: its kind and its ID.
type Ref struct {
	Kind ids.Kind
	ID   ids.UUID
}

// Revision is an entity's revision.
type Revision struct {
	Ref
	Rev int64
}

// Command is a command of a batch, as a client sends it (FR-API-03).
type Command struct {
	// ID is the client's ID of the command, unique within the project, by
	// which a command that was applied stays recognised.
	ID      ids.UUID
	Type    Type
	Payload json.RawMessage
	// ExpectedRev holds, for each entity that the client authored the
	// command against, the revision that it had then (ADR-006). It may name
	// any entity that the command changes or refers to, of a kind that it
	// touches (Spec.Touches), and the batch applies only if every one of them
	// still has that revision before the batch: a revision is the one that
	// the client read, not one that an earlier command of the batch gives,
	// and an entity that the batch creates has none to expect.
	ExpectedRev map[Ref]int64

	// sent marks a command that Parse read from a client's JSON, with an ID
	// that the client made, and may send again, as when the answer was lost.
	// Only a batch of such commands has an idempotency record of its answer,
	// applied or refused (refusalOf): the batches of the server's own, as
	// package project's, whose IDs it makes anew for each, nobody sends
	// again, so they write none, and their refusals commit nothing.
	sent bool
	// keys are the keys of ExpectedRev as the client wrote them, by entity,
	// for a command that Parse read, so that a problem of one is located where
	// the client wrote it, as nd_01hq8z… in lowercase, which names the same
	// entity as nd_01HQ8Z….
	keys map[Ref]string
}

// key returns the key of the entity ref in c's ExpectedRev, as the client
// wrote it, or as the API writes the entity's ID if no client did.
func (c Command) key(ref Ref) string {
	if k, ok := c.keys[ref]; ok {
		return k
	}
	return encodeRef(ref)
}

// Actor is who applies a batch.
type Actor struct {
	UserID ids.UUID
	// Token is the API token that the user acts with, and what it restricts
	// them to, or nil for a session (FR-AUTH-04).
	Token *scope.Token
	// Client is what the audit log records of the client.
	Client audit.Client
}

// Result is what a batch that applied did.
type Result struct {
	// Status is Applied, or AlreadyApplied for a batch whose commands were
	// applied before, once the record of the batch that applied them is
	// pruned, or grouped otherwise (FR-API-03).
	Status Status
	// Seq is the batch's seq: the project's sequence number after it. That
	// of a batch AlreadyApplied is the latest seq of the batches that applied
	// its commands.
	Seq int64
	// Revisions are the entities that the batch changed, with their
	// revisions after it, in the order of their first change. Those of a
	// batch AlreadyApplied are the entities that the batches that applied its
	// commands changed, with their current revisions, but for those that no
	// longer exist.
	Revisions []Revision
	// NotReconnected are the entities that refer to what the batch restored
	// from the project's trash that it could not reconnect, because what else
	// they refer to is in the trash still, as the connections of a restored
	// node, or of a node of a restored board, whose node at the other end is
	// (PRD FR-PROJ-06 AC1): once every command of the batch has applied, so
	// that a batch that restores both of a connection's nodes leaves it out,
	// each once. A batch AlreadyApplied has none,
	// since the record of what the batches that applied its commands left is
	// pruned with their answers.
	NotReconnected []Unreconnected
	// Undo is the batch's undo (PRD FR-EDIT-08, plan item 37): its inverse as
	// the operation log keeps it, the commands that undo its commands, from its
	// last command's to its first's, each with the revisions that it expects,
	// those of the entities that its command changed, as the batch left them,
	// each entity once: one that several of the batch's commands changed is
	// expected by the undo of the first of them alone, as the batch that
	// undoes it is checked whole against the entities as they are before it.
	// A command of the batch that changed nothing (Tx.Changed) has nothing to
	// undo, and none of its inverse's steps is in the undo: they would expect
	// no revision, and write back what the command found over what another
	// user has changed since, as the undo of a board's rename to the name
	// that it has would rename it back over another user's rename.
	// A client undoes the batch by sending them back as a batch, each with a
	// command ID of its own, which applies only if none of those entities has
	// changed since, and is refused whole with a *RevisionConflictError
	// otherwise, so that an undo never overwrites newer work on what the batch
	// changed. It may still change what the batch did not, whose revisions it
	// does not expect, as its commands would at any time: the undo of a
	// creation, a deletion, takes to the trash with what it deletes what has
	// been put in it since, as the nodes that another user added to a board
	// that the batch created; the undo of a deletion, a restore, brings back
	// the folders that what it restores is in, and a component's type, which
	// may have gone to the trash since; and the undo of a change of an enum's
	// options or of an attribute definition's type gives the default to a
	// value that a component was given since, of an option or a type that the
	// undo takes away, as those commands do to any value that they cannot
	// keep. Its own undo sets each of them back (plan note N45). The batch
	// that undoes another has an undo too, which redoes it. An entity that the
	// client's own undo brings back is at a new revision, so the client moves
	// the expectations of the rest of its stack from the revision that the
	// entity had before the batch that it undid to that one: the revision that
	// the client's store holds once it has every batch of the project before
	// that one's Seq, so that a store that had not yet heard of another
	// user's batch that came between, when the answer came, catches up
	// first, rather than move to the new revision an entry of its stack that
	// expects the entity as it was before that user's change, whose undo
	// would then overwrite it. Undo is nil for a batch that cannot be undone,
	// with a command of a type that is NonUndoable, or answered from its
	// idempotency record once the operation log no longer holds its
	// operation, as one that compaction dropped (PRD FR-GDPR-03), and for a
	// batch AlreadyApplied, whose commands' batches each have an undo of
	// their own, and empty for a batch whose commands' inverses have no
	// steps, or whose commands changed nothing, which undoes nothing.
	Undo []UndoCommand
}

// UndoCommand is a command of a batch's undo (Result.Undo): its type, its
// payload, as the operation log keeps it, and the revisions that it expects,
// as a Command's ExpectedRev.
type UndoCommand struct {
	Type        Type
	Payload     json.RawMessage
	ExpectedRev map[Ref]int64
}

// RevisionConflictError is a batch with commands whose expected revisions
// of entities are not the entities' revisions any more (PRD §5.7, ADR-006,
// FR-API-03 AC3). It applies nothing. The API answers it with 409
// revision_conflict.
type RevisionConflictError struct {
	// Conflicts are every command's entities whose revisions are not the
	// expected ones, in the order of the commands, and of each command's
	// entities by their kinds and IDs. An entity that several commands
	// expect a revision of that it does not have is in it once for each, each
	// with the entity's one State, which they share, so that the API answers
	// it, and the batch's idempotency record keeps it, once.
	Conflicts []Conflict
}

func (e *RevisionConflictError) Error() string {
	return fmt.Sprintf("commands: the batch's commands expect %d revisions that their entities no longer have",
		len(e.Conflicts))
}

// Conflict is an entity whose revision is not the one that a command
// expected.
type Conflict struct {
	// Command is the index of the command in the batch, and CommandID and
	// Type are its.
	Command   int
	CommandID ids.UUID
	Type      Type
	Ref
	Expected int64
	// Exists says whether the entity still exists. One that does has the
	// revision Current, and the state State, encoded as JSON as its kind's
	// Entity.States gives it. One that does not, as one that was purged from
	// the trash, has neither: the client's "target deleted" (FR-OFF-04 AC5).
	// An entity in the trash still exists.
	Exists  bool
	Current int64
	State   json.RawMessage
}

// DB runs a transaction of the application login, as *database.DB does.
type DB interface {
	InTx(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error
}

// Enqueuer enqueues jobs in a transaction, as *jobs.Queue does.
type Enqueuer interface {
	Enqueue(ctx context.Context, tx pgx.Tx, args jobs.Args, opts *jobs.EnqueueOptions) (jobs.Enqueued, error)
}

// Notifier is told of each batch that applies, once it has committed, with
// its project and its seq (§10.2 step 7). The real-time hub (plan item 52)
// tells the project's sockets of it; until then, nothing is told. Its
// context carries the values of Apply's, but not its cancellation, because
// the batch has committed whatever becomes of the request that sent it, as
// when its client goes away at that moment.
type Notifier interface {
	Committed(ctx context.Context, projectID ids.UUID, seq int64)
}

// Options configure a Pipeline.
type Options struct {
	// Registry holds the types of command, or nil for none.
	Registry *Registry
	// Policy resolves the roles.
	Policy role.Policy
	// Entitlements check the workspaces' limits. It is required, because a
	// pipeline without it would let every command past its workspace's plan.
	Entitlements *entitlements.Service
	// Jobs is the job queue of the commands' jobs (Tx.Enqueue), or nil for
	// none.
	Jobs Enqueuer
	// Notifier is told of the batches that apply, or nil for nothing.
	Notifier Notifier
	// MaxUndoBytes is the most bytes that a batch's inverse may take, the
	// payloads of its steps as the operation log writes them (LogBytes) and
	// the revisions that they expect (ExpectedRevBytes), or 0 for
	// DefaultMaxUndoBytes, which a test lowers.
	MaxUndoBytes int
	// MaxUndoSteps is the most steps that a batch's inverse may have, or 0 for
	// DefaultMaxUndoSteps, which a test lowers.
	MaxUndoSteps int
}

// DefaultMaxUndoBytes is the most bytes that a batch's inverse may take, as
// the operation log keeps it and a batch's answer holds it (Result.Undo): the
// payloads of its steps, each as the log writes it (LogBytes), and the
// revisions that they expect, each counted as its entity's ID and as many
// digits as a revision can have (ExpectedRevBytes). It is 4 MiB, so that the
// batch that undoes it, which a client sends as the inverse's commands, each
// with its command ID and its expected revisions (plan item 37), fits in a
// request's body, of at most 5 MiB (api.DefaultMaxBodyBytes), with a mebibyte
// for the rest of the commands: their IDs, types, and keys. The pipeline
// refuses a command whose inverse, or the revisions that it expects of what
// the command changes, as its Apply reports them, would take its batch's undo
// past it, whatever its commands' inverses hold, but for a command that
// changes nothing, whose inverse the undo leaves out, and the bound does not
// count once it has applied. Each revision is counted once, however many of
// the batch's commands change its entity, as the undo expects it once. The
// batch that undoes another is a batch as any other, whose own inverse writes
// again what the other's commands wrote, so the pipeline refuses it alike
// once that passes the bound: a batch whose commands write more than the
// bound, as several large descriptions over small ones, applies, but is
// undone only in smaller batches.
const DefaultMaxUndoBytes = 4 << 20

// DefaultMaxUndoSteps is the most steps that a batch's inverse may have, the
// most commands of a batch that a client sends through the API, so that the
// batch that undoes it can be sent: one command may have an inverse of
// several steps, as a board's restoration, whose undo deletes again the
// folders that it brought back. It holds every batch that can be undone,
// whichever caller applies it, as DefaultMaxUndoBytes does; a batch that
// cannot be undone, as an import (PRD FR-EDIT-08 AC1), has no undo to bound.
// The batch that undoes another is bounded alike, by its own undo, which may
// have more steps than the other had commands, as when the folders of the
// boards whose deletion it undoes have gone to the trash since, which their
// restorations bring back and their undo deletes again, so the pipeline
// refuses it once that passes the bound, and it is sent in smaller batches,
// as one whose undo takes more bytes than the bound is.
const DefaultMaxUndoSteps = 1000

// revisionDigits is the most digits that a revision, an int64 above 0, has.
const revisionDigits = 19

// ExpectedRevBytes returns how many bytes the bound of a batch's undo
// (Options.MaxUndoBytes) counts for a revision of the entity ref that a step
// of the undo expects: the most that it takes as JSON encodes it in the
// step's expected_rev, its ID, as a key, and the revision, with a comma. It
// counts the ID as ids.Encode writes it, its kind's prefix, an underscore,
// and its body, without encoding it, as Tx.Changed counts one for each entity
// that a command changes, which may be every component of a type. The kind
// of ref is an entity type, as every kind that a command touches is
// (ids.Kind.Prefix).
func ExpectedRevBytes(ref Ref) int {
	return len(ref.Kind.Prefix()) + len("_") + ids.BodyLength + len(`"":,`) + revisionDigits
}

// LogBytes returns how many bytes the bound of a batch's undo
// (Options.MaxUndoBytes) counts for raw, the payload of a step of the undo as
// encoding/json encodes it: at least as many as the payload takes as the
// operation log's jsonb writes it, compact, which is what the batch's answer,
// and a replay of it, hold (Result.Undo). jsonb keeps a number as a numeric,
// which it writes without an exponent, with every digit of its value, so a
// float that encoding/json writes with one, below 1e-6 or from 1e21, takes
// more there, as 1e+300, of 301 digits, or 5e-324, of 326 characters, which
// LogBytes counts. jsonb writes nothing else longer than encoding/json does,
// which escapes some characters of a string, as <, that jsonb writes as they
// are.
func LogBytes(raw []byte) int {
	n := len(raw)
	for i := 0; i < len(raw); i++ {
		switch c := raw[i]; {
		case c == '"':
			// A string ends at the first quote that no backslash escapes.
			for i++; i < len(raw) && raw[i] != '"'; i++ {
				if raw[i] == '\\' {
					i++
				}
			}
		case c == '-' || '0' <= c && c <= '9':
			j := i + 1
			for j < len(raw) && strings.IndexByte("+-.0123456789Ee", raw[j]) >= 0 {
				j++
			}
			n += positionalLen(raw[i:j]) - (j - i)
			i = j - 1
		}
	}
	return n
}

// positionalLen returns how many characters the JSON number num takes once
// jsonb has written it without an exponent: its sign, and its digits, with the
// zeros that its exponent stands for, and its point, if it has a fraction, or
// as many as it takes already if it has no exponent.
func positionalLen(num []byte) int {
	at := bytes.IndexAny(num, "Ee")
	if at < 0 {
		return len(num)
	}
	exp, err := strconv.Atoi(string(num[at+1:]))
	if err != nil {
		// No exponent that a double has, which encoding/json writes.
		return len(num)
	}
	mantissa, sign := num[:at], 0
	if mantissa[0] == '-' {
		mantissa, sign = mantissa[1:], 1
	}
	whole, fraction, _ := bytes.Cut(mantissa, []byte("."))
	digits := len(whole) + len(fraction)
	// point is how many digits the number has before its point, once it is
	// written without an exponent.
	switch point := len(whole) + exp; {
	case point >= digits:
		return sign + point
	case point > 0:
		return sign + digits + len(".")
	default:
		return sign + len("0.") - point + digits
	}
}

// ErrUndoTooLarge is the error of an Inverse that stops before its steps take
// more than Tx.UndoRoom leaves, and of Tx.Changed once the command's inverse,
// or the revisions that its undo expects, take more, which an Apply returns
// as it returns Changed's every error, which the pipeline refuses the command
// for as it refuses one whose inverse's steps it finds take more: at the
// payload's field that the command's Spec.UndoField names.
var ErrUndoTooLarge = errors.New("commands: the inverse would take its batch's undo past the pipeline's bound")

// Pipeline applies batches of commands.
type Pipeline struct {
	db       DB
	registry *Registry
	policy   role.Policy
	ent      *entitlements.Service
	jobs     Enqueuer
	notifier Notifier
	metrics  *metrics
	// maxUndo is Options.MaxUndoBytes, or DefaultMaxUndoBytes, and
	// maxUndoSteps Options.MaxUndoSteps, or DefaultMaxUndoSteps.
	maxUndo, maxUndoSteps int
}

// New returns a Pipeline that applies the commands of opts.Registry in db. It
// panics if opts.Entitlements is nil.
func New(db DB, opts Options) *Pipeline {
	if opts.Entitlements == nil {
		panic("commands: a pipeline needs the entitlements that check its commands' limits")
	}
	reg := opts.Registry
	if reg == nil {
		reg = NewRegistry()
	}
	maxUndo := opts.MaxUndoBytes
	if maxUndo <= 0 {
		maxUndo = DefaultMaxUndoBytes
	}
	maxUndoSteps := opts.MaxUndoSteps
	if maxUndoSteps <= 0 {
		maxUndoSteps = DefaultMaxUndoSteps
	}
	return &Pipeline{db: db, registry: reg, policy: opts.Policy, ent: opts.Entitlements, jobs: opts.Jobs,
		notifier: opts.Notifier, metrics: newMetrics(reg.Types()), maxUndo: maxUndo, maxUndoSteps: maxUndoSteps}
}

// Collector returns the pipeline's metrics (PRD NFR-OBS-02).
func (p *Pipeline) Collector() prometheus.Collector {
	return p.metrics
}

// Apply applies the batch of commands to the project projectID, as the actor
// asks, atomically, and returns what it did (FR-API-03). It returns an
// *InvalidError if the batch is not valid, as when a command expects a
// revision of another project; ErrNotFound if the project does not
// exist, its workspace's deletion is pending, the actor's effective role on
// it is none, or it is in the trash and a command does not apply there
// (Spec.Trashed) and none restores it (Spec.Restores), or the actor's API
// token does not cover the project;
// ErrSessionRequired if the actor acts with an API token, and a command is
// one that only a session may apply (Spec.SessionOnly); ErrInsufficientScope,
// as a *scope.InsufficientError, if the token does not hold a command's
// scope (Spec.Scope); ErrInsufficientRole or ErrOwnerRequired if the actor's
// role does not allow a command's operation; an *IdempotencyMismatchError if
// some of the batch's commands were applied before, and others were not;
// ErrProjectArchived if the project is archived and a command does not apply
// to it then (Spec.Archived) and none unarchives it (Spec.Unarchives); an
// *entitlements.LimitError if the batch would
// take the workspace past a limit of its plan; a *RevisionConflictError if a
// command's expected revision is stale; a *RefusedError if a command refuses
// the batch for what the project's state makes of its payload (Refuse); and
// an error that wraps a command's own if a command fails. A batch that was
// sent before is answered as it was, and applies nothing again (FR-API-03):
// with the Result that it had, or with the error that refused it, if its
// idempotency record remembers that refusal, without checking it again. Whatever Apply returns
// but a Result, it changes nothing of the project: at most, for a batch that
// a client sent (Parse) that the project's state or the limits refuse, it
// records that refusal as the batch's idempotency record, and for one that
// the actor's API token may not apply, the refusal in the audit log.
func (p *Pipeline) Apply(ctx context.Context, actor Actor, projectID ids.UUID, batch []Command) (Result, error) {
	start := time.Now()
	cmds, err := p.registry.decode(batch, projectID)
	var res Result
	replayed := false
	if err == nil {
		res, replayed, err = p.apply(ctx, actor, projectID, cmds)
	}
	p.metrics.observe(cmds, err, replayed, time.Since(start))
	if err != nil {
		return Result{}, err
	}
	if p.notifier != nil && !replayed {
		p.notifier.Committed(context.WithoutCancel(ctx), projectID, res.Seq)
	}
	return res, nil
}

// apply applies the decoded commands cmds, and returns what they did, and
// whether the batch was answered as it was before (replayed), which applied
// nothing.
func (p *Pipeline) apply(ctx context.Context, actor Actor, projectID ids.UUID, cmds []decoded) (Result, bool,
	error) {
	workspaceID, err := p.actorWorkspace(ctx, actor, projectID)
	if err != nil {
		return Result{}, false, err
	}
	commandIDs := make([]pgtype.UUID, len(cmds))
	for i, c := range cmds {
		commandIDs[i] = nullUUID(c.ID)
	}
	// sent says that a client sent the batch (Parse), and may send it again,
	// so that its answer is worth an idempotency record: the batches of the
	// server's own, as package project's, whose IDs it makes anew for each,
	// nobody sends again.
	sent := slices.ContainsFunc(cmds, func(c decoded) bool { return c.sent })
	var res Result
	// replayed is set for a batch that was sent before, which is answered as
	// it was, and refused for a batch that is refused by a transaction that
	// commits nonetheless, which writes nothing of the project: the refusal
	// of an API token, which the audit log records, one that the pipeline
	// remembers, which its idempotency record does, or a remembered refusal
	// answered again.
	var replayed bool
	var refused error
	err = p.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID, UserID: actor.UserID}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		// workspaceOf, or the authentication of the actor's API token, found
		// the workspace active in a transaction of its own, so the batch's
		// reads it again, before the roles, so that a deletion requested in
		// between refuses the batch as well, and with 404, whatever the
		// actor's role would allow (FR-TEN-05 AC1). The same read finds
		// whether the project is in the trash, where it is not found by a
		// batch that may not apply there, before the actor's role is, so that
		// a refusal of the role does not tell that it exists, and finds no
		// project of another workspace than a token's.
		deleted, err := q.ProjectInTrash(ctx, queries.ProjectInTrashParams{WorkspaceID: nullUUID(workspaceID),
			ID: nullUUID(projectID)})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrNotFound
		case err != nil:
			return fmt.Errorf("commands: read the project: %w", err)
		case deleted && !admitted(cmds, trashed, restores):
			return ErrNotFound
		}
		roles, err := p.roles(ctx, q, actor.UserID, workspaceID, projectID)
		if err != nil {
			return err
		}
		if denied := tokenDenial(actor.Token, cmds); denied != nil {
			// The refusal is audited, as the authentication of a token
			// audits the refusals of the routes (NFR-SEC-05), and changes
			// nothing else, so its transaction commits.
			if err := denyToken(ctx, q, tx, workspaceID, actor, denied); err != nil {
				return err
			}
			refused = denied
			return nil
		}
		for _, c := range cmds {
			if err := authz.Check(roles[c.spec.capability.Scope()], c.spec.capability); err != nil {
				return err
			}
		}
		// The lock is taken once the actor is authorised, so that nobody
		// without a role on the project can hold up its batches. It is the
		// only row that the batch locks before its commands apply: roles says
		// why the membership and the grant that authorise the batch are not.
		locked, err := q.LockProjectSeq(ctx, queries.LockProjectSeqParams{WorkspaceID: nullUUID(workspaceID),
			ID: nullUUID(projectID)})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("commands: lock the project: %w", err)
		}
		// The batch may have waited for the lock for as long as the project's
		// other batches took, and a deletion requested meanwhile refuses it
		// too, rather than letting it apply, with 404 whatever the project's
		// state, which is read below (FR-TEN-05 AC1). The read is a statement
		// of its own, which sees what committed while the batch waited, as the
		// lock's own statement does not.
		if err := activeWorkspace(ctx, q, workspaceID); err != nil {
			return err
		}
		// The project may have changed while the batch waited for the lock,
		// as when a batch before it deleted it. A project in the trash is not
		// found even by a batch that was sent before (FR-PROJ-01 AC3).
		if locked.Deleted && !admitted(cmds, trashed, restores) {
			return ErrNotFound
		}
		t := &Tx{tx: tx, pipeline: p, actor: actor, project: projectID, workspace: workspaceID, at: map[Ref]int{},
			revs: map[Ref]int64{}, expecting: map[ids.Kind]int{}, expected: map[Ref]struct{}{}, maxUndo: p.maxUndo,
			maxUndoSteps: p.maxUndoSteps, usage: map[usage.Key]int64{}, trashed: locked.Deleted}
		// A batch that was sent before is answered as it was, whatever has
		// changed since, before the project's state and the workspace's limits
		// are checked (§10.2 step 4), so that a batch that applied is never
		// applied again, nor refused because of what it did, as a create that
		// took the last of a limit. The project's lock keeps a batch that is
		// sent again while it applies from applying too: it waits for the
		// first, and finds it applied.
		// An *IdempotencyMismatchError, which answers no batch as it was, is
		// an error of idempotent's, which writes nothing before it.
		r, err := p.idempotent(ctx, q, t, cmds, commandIDs)
		switch {
		case err != nil:
			return err
		case r != nil:
			replayed, res, refused = true, r.res, r.err
			return nil
		}
		err = p.check(ctx, t, cmds, locked.Archived)
		if r := refusalOf(err); r != nil && sent {
			// A refusal of the checks, which come before any command writes
			// anything, of a batch that a client may send again, is
			// remembered in the batch's idempotency record, which commits.
			if err := remember(ctx, q, t, commandIDs, answer{Refusal: r}); err != nil {
				return err
			}
			refused = err
			return nil
		}
		if err == nil {
			res, err = p.applyLocked(ctx, q, t, cmds, commandIDs, sent, locked.Seq)
		}
		// Each later read is a statement of its own too, so a deletion
		// requested once the read above is done is seen by the next read that
		// only an active workspace answers, as the read of its plan with plans
		// enforced, which then finds no row, and would fail the batch as a 500.
		// A batch that fails for want of a row is refused as above if its
		// workspace is no longer active.
		if errors.Is(err, pgx.ErrNoRows) && errors.Is(activeWorkspace(ctx, q, workspaceID), ErrNotFound) {
			return ErrNotFound
		}
		return err
	})
	switch {
	case err != nil:
		return Result{}, false, err
	case refused != nil:
		return Result{}, replayed, refused
	}
	return res, replayed, nil
}

// tokenDenial returns why the API token, nil for a session, may not apply the
// commands cmds, or nil if it may: ErrSessionRequired for a command that only
// a session may apply (Spec.SessionOnly), whatever its scopes, and a
// *scope.InsufficientError for one whose scope it does not hold (Spec.Scope),
// whatever its owner's role allows.
func tokenDenial(token *scope.Token, cmds []decoded) error {
	if token == nil {
		return nil
	}
	if slices.ContainsFunc(cmds, func(c decoded) bool { return c.spec.sessionOnly }) {
		return ErrSessionRequired
	}
	for _, c := range cmds {
		if !token.Allows(c.spec.scope) {
			return &scope.InsufficientError{Scope: c.spec.scope}
		}
	}
	return nil
}

// denyToken records in tx, of the workspace workspaceID, that the actor's API
// token is refused a batch, because of denied, which tokenDenial returned, as
// token.use_denied in the audit log, as package apitoken records the refusals
// of the routes: once a minute at most for each token, so that a client that
// repeats a refused batch does not flood the log (NFR-SEC-05).
func denyToken(ctx context.Context, q *queries.Queries, tx pgx.Tx, workspaceID ids.UUID, actor Actor,
	denied error) error {
	n, err := q.DenyToken(ctx, queries.DenyTokenParams{WorkspaceID: nullUUID(workspaceID),
		ID: nullUUID(actor.Token.ID)})
	if err != nil {
		return fmt.Errorf("commands: record the token's refusal: %w", err)
	}
	if n == 0 {
		// A refusal of the token was recorded less than a minute ago.
		return nil
	}
	metadata := map[string]any{"reason": "session_required"}
	var insufficient *scope.InsufficientError
	if errors.As(denied, &insufficient) {
		metadata = map[string]any{"reason": "insufficient_scope", "scope": insufficient.Scope}
	}
	return audit.Record(ctx, tx, audit.Entry{Action: audit.ActionTokenUseDenied, WorkspaceID: workspaceID,
		ActorUserID: actor.UserID, ActorTokenID: actor.Token.ID, SubjectID: actor.Token.ID,
		Result: audit.ResultDenied, IP: actor.Client.IP, UserAgent: actor.Client.UserAgent, Metadata: metadata})
}

// applyLocked applies the decoded commands cmds, whose IDs are commandIDs,
// and which a client sent if sent is set, in t, the batch's transaction, once
// it holds the lock of the project, whose seq is seq, and check has found
// nothing that refuses them: it applies the commands, and records the batch.
func (p *Pipeline) applyLocked(ctx context.Context, q *queries.Queries, t *Tx, cmds []decoded,
	commandIDs []pgtype.UUID, sent bool, seq int64) (Result, error) {
	// A batch with a command that cannot be undone has no inverse, so none of
	// its commands' inverses is computed, only to be dropped.
	inverses := make([][]logStep, len(cmds))
	undoable := !slices.ContainsFunc(cmds, func(c decoded) bool { return c.spec.inverse == nil })
	for i, c := range cmds {
		if undoable {
			steps, err := c.spec.inverse(ctx, t, c.payload)
			if err == nil {
				inverses[i], err = p.registry.checkSteps(steps)
			}
			// The batch's undo is its commands' inverses, which the bounds
			// hold whole, whatever a command's inverse holds: each command's
			// steps add to what those of the commands before it take. The
			// bounds are checked once the command has applied, below, when
			// the pipeline knows whether the undo holds its steps.
			if err == nil {
				for _, st := range inverses[i] {
					t.undoBytes += LogBytes(st.Payload)
				}
				t.undoSteps += len(inverses[i])
			}
			if errors.Is(err, ErrUndoTooLarge) {
				return Result{}, undoRefusal(i, c, t.maxUndo)
			}
			if refused, ok := refusedBy(err, i, c); ok {
				return Result{}, refused
			}
			if err != nil {
				return Result{}, fmt.Errorf("commands: the inverse of command %d (%s): %w", i, c.spec.typ, err)
			}
		}
		// Only Apply reports changes (Tx.Changed): neither the reads of the
		// revisions nor an inverse, which only reads. The command's undo
		// expects the revisions of what it changes of the kinds that the
		// steps of its inverse touch, which the bound holds too, as a client
		// sends them back, so Changed counts each as Apply reports it, and
		// stops Apply as soon as the undo is past the bound.
		// The revision of each entity that the command changes is expected by
		// the first step of its inverse whose type touches the entity's kind,
		// as a command may expect a revision only of an entity that it
		// touches (Tx.Changed), unless the undo of a command before it in the
		// batch expects it already (t.expected, which spans the batch). The
		// check of an undo's revisions is the batch's, against the entities
		// as they are before it, so an entity is expected once, however many
		// of the batch's commands change it, and the bound counts it once, and
		// the step that expects one matters only to the conflict that names
		// it. An entity of a kind that none of the steps touches is one that
		// the undo of the command does not write, as no step's Apply may
		// report a change of a kind that its type does not touch, so none of
		// them expects it. t.steps is inverses[i] itself, not a copy, so what
		// Changed gives its steps is what record writes to the log.
		t.current, t.steps, t.changes = c.spec, inverses[i], 0
		clear(t.expecting)
		for j, st := range inverses[i] {
			for _, kind := range st.touches {
				if _, ok := t.expecting[kind]; !ok {
					t.expecting[kind] = j
				}
			}
		}
		err := c.spec.apply(ctx, t, c.payload)
		t.current, t.steps = nil, nil
		if errors.Is(err, ErrUndoTooLarge) {
			return Result{}, undoRefusal(i, c, t.maxUndo)
		}
		if refused, ok := refusedBy(err, i, c); ok {
			return Result{}, refused
		}
		if err != nil {
			return Result{}, fmt.Errorf("commands: command %d (%s): %w", i, c.spec.typ, err)
		}
		// A command that reported no change changed nothing, and has nothing
		// to undo. Its inverse's steps would write back what the command
		// found, which it left as it was, and expect no revision, as it
		// changed none, so that, sent back once another user had changed
		// what they write, they would overwrite that newer work, as the undo
		// of a board's rename to the name that it has would rename it back
		// over another user's rename since (PRD FR-EDIT-08). The batch's undo
		// leaves them out, and the bounds give back the room that they took.
		// Only Changed gives a step a revision to expect, so those steps
		// expect none, and every entity that the batch's undo expects
		// (t.expected) is expected by a step that the undo holds.
		if t.changes == 0 && len(inverses[i]) > 0 {
			for _, st := range inverses[i] {
				t.undoBytes -= LogBytes(st.Payload)
			}
			t.undoSteps -= len(inverses[i])
			inverses[i] = []logStep{}
		}
		// The bounds hold the batch's undo as the command has left it, so
		// that a batch is not refused for the steps of a command that
		// changed nothing, which its undo leaves out, wherever the command
		// is in the batch.
		if t.undoBytes > t.maxUndo {
			return Result{}, undoRefusal(i, c, t.maxUndo)
		}
		if t.undoSteps > t.maxUndoSteps {
			return Result{}, undoStepsRefusal(i, c, t.maxUndoSteps)
		}
	}
	if err := t.addCounts(ctx); err != nil {
		return Result{}, err
	}
	if err := t.recordUsage(ctx); err != nil {
		return Result{}, err
	}
	notReconnected, err := t.notReconnected(ctx)
	if err != nil {
		return Result{}, err
	}
	// Revisions is never nil, as that of a batch answered from its record is
	// not, so that the batch's answer is the same either way.
	res := Result{Status: Applied, Seq: seq + 1, Revisions: append([]Revision{}, t.changed...),
		NotReconnected: notReconnected}
	if res.Undo, err = p.record(ctx, q, t, cmds, commandIDs, sent, res, inverses, undoable); err != nil {
		return Result{}, err
	}
	return res, nil
}

// check checks, in t, the batch's transaction, before any of its commands
// applies, that the batch applies to the project, which is archived if
// archivedProject is set (Spec.Archived), that it takes the workspace past
// none of its limits, and that every revision that its commands expect is
// the entity's. It reads, and writes nothing.
func (p *Pipeline) check(ctx context.Context, t *Tx, cmds []decoded, archivedProject bool) error {
	if archivedProject && !admitted(cmds, archived, unarchives) {
		return ErrProjectArchived
	}
	checked, err := p.checkLimits(ctx, t, cmds)
	if err != nil {
		return err
	}
	t.checked = checked
	return p.checkRevs(ctx, t, cmds)
}

// trashed and archived report whether a command applies to a project in the
// trash, and to an archived one (Spec.Trashed, Spec.Archived), and restores
// and unarchives whether it takes the project out of the trash, and out of
// its archive (Spec.Restores, Spec.Unarchives).
func trashed(c decoded) bool    { return c.spec.trashed }
func archived(c decoded) bool   { return c.spec.archived }
func restores(c decoded) bool   { return c.spec.restores }
func unarchives(c decoded) bool { return c.spec.unarchives }

// admitted reports whether the batch of the commands cmds applies to a
// project in the trash, or to an archived one: whether every command of it
// applies there, as applies says, or one of them takes the project out of
// there, as out says, which lets the others apply as a batch of its own that
// took the project out first would, as the undo of a batch that deleted or
// archived the project with other commands needs (PRD FR-EDIT-08).
func admitted(cmds []decoded, applies, out func(decoded) bool) bool {
	return !slices.ContainsFunc(cmds, func(c decoded) bool { return !applies(c) }) || slices.ContainsFunc(cmds, out)
}

// actorWorkspace returns the workspace that the actor acts in on the
// project: that of their API token (scope.Token.Workspace), which acts in no
// other, so that the batch's transaction does not find a project of another
// workspace, which it answers with ErrNotFound before anything of it is read
// but whether it exists, or else the project's (workspaceOf). It returns
// ErrNotFound as workspaceOf does, and for a project other than the one that
// the token is narrowed to.
func (p *Pipeline) actorWorkspace(ctx context.Context, actor Actor, projectID ids.UUID) (ids.UUID, error) {
	if actor.Token == nil {
		return p.workspaceOf(ctx, actor.UserID, projectID)
	}
	if ws, ok := actor.Token.Workspace(projectID); ok {
		return ws, nil
	}
	return ids.UUID{}, ErrNotFound
}

// workspaceOf returns the workspace of the project, through the context
// bootstrap (§5.4), or ErrNotFound if there is no such project, or if its
// workspace's deletion is pending (FR-TEN-05 AC1), which the batch's own
// transaction checks again, before the roles and once it holds the project's
// lock.
func (p *Pipeline) workspaceOf(ctx context.Context, userID, projectID ids.UUID) (ids.UUID, error) {
	var ws pgtype.UUID
	err := p.db.InTx(ctx, database.Scope{UserID: userID}, func(tx pgx.Tx) error {
		var err error
		ws, err = queries.New(tx).ProjectWorkspace(ctx, nullUUID(projectID))
		return err
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ids.UUID{}, ErrNotFound
	case err != nil:
		return ids.UUID{}, fmt.Errorf("commands: find the project's workspace: %w", err)
	}
	return ws.Bytes, nil
}

// activeWorkspace returns ErrNotFound unless the workspace is active, as it is
// not once its deletion is pending (FR-TEN-05 AC1).
func activeWorkspace(ctx context.Context, q *queries.Queries, workspaceID ids.UUID) error {
	if _, err := q.Workspace(ctx, nullUUID(workspaceID)); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("commands: read the workspace: %w", err)
	}
	return nil
}

// roles returns the role that the user acts with for each scope of
// operation (§5.5): their effective role on the project for a
// project-scoped one, and their role in its workspace for a workspace-scoped
// one. It returns ErrNotFound if the effective role is none. It reads
// whether the user is an instance admin only if the policy lets an instance
// admin act as owner, because otherwise the answer changes nothing.
//
// It locks neither the user's membership nor their grant on the project, so
// a change of either that commits once it has read them, as while the batch
// waits for the project's lock, is ordered after the batch that they
// authorised. Locked FOR SHARE, they would make a removal or a role change,
// which locks the membership FOR UPDATE while it holds the workspace's row,
// wait for the member's batches, and PostgreSQL grants a share lock at once
// even while such a lock waits, so a member who kept batches in flight, one
// overlapping the next, would put off their own removal for as long as they
// sent them, and every change of the workspace's members and settings, which
// waits for the workspace's row, with it.
func (p *Pipeline) roles(ctx context.Context, q *queries.Queries, userID, workspaceID, projectID ids.UUID) (
	map[authz.Scope]role.Role, error) {
	member, grant := role.None, role.None
	m, err := q.MembershipOf(ctx, queries.MembershipOfParams{WorkspaceID: nullUUID(workspaceID),
		UserID: nullUUID(userID)})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return nil, fmt.Errorf("commands: read the membership: %w", err)
	default:
		member = role.Role(m.Role)
		g, err := q.ProjectGrantRole(ctx, queries.ProjectGrantRoleParams{WorkspaceID: nullUUID(workspaceID),
			MembershipID: m.ID, ProjectID: nullUUID(projectID)})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return nil, fmt.Errorf("commands: read the project grant: %w", err)
		default:
			grant = role.Role(g)
		}
	}
	admin := false
	if p.policy.InstanceAdminAccess {
		admin, err = q.InstanceAdmin(ctx, nullUUID(userID))
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// The account is no longer active.
			return nil, ErrNotFound
		case err != nil:
			return nil, fmt.Errorf("commands: read the account: %w", err)
		}
	}
	effective := p.policy.Effective(admin, member, grant)
	if effective == role.None {
		return nil, ErrNotFound
	}
	return map[authz.Scope]role.Role{authz.Project: effective, authz.Workspace: p.policy.Workspace(admin, member)},
		nil
}

// checkLimits checks, with the entitlements, each limit of the workspace's
// plan that the batch adds to, for what all of its commands add together, so
// that a batch whose commands each fit, but not all of them, is refused
// (FR-ENT-01), and returns what it checked each limit for, which the batch's
// usage may not pass (Tx.recordUsage). It asks each command how much it adds
// (Limit.Adds) in t, the batch's transaction, before any command applies. It
// returns an *entitlements.LimitError for the first limit, in the order of
// the commands, that the batch would take the workspace past.
func (p *Pipeline) checkLimits(ctx context.Context, t *Tx, cmds []decoded) (map[entitlements.LimitKey]int64,
	error) {
	adds := make([][]int64, len(cmds))
	for i, c := range cmds {
		adds[i] = make([]int64, len(c.spec.limits))
		for j, key := range c.spec.limits {
			adds[i][j] = 1
			if c.spec.adds[j] == nil {
				continue
			}
			var err error
			if adds[i][j], err = c.spec.adds[j](ctx, t, c.payload); err != nil {
				return nil, fmt.Errorf("commands: what command %d (%s) adds to %s: %w", i, c.spec.typ, key, err)
			}
		}
	}
	keys, sums := batchAdds(cmds, adds)
	for _, key := range keys {
		if err := p.ent.Check(ctx, t.tx, t.workspace, key, sums[key]); err != nil {
			return nil, err
		}
	}
	return sums, nil
}

// batchAdds returns the limits that the commands cmds add to, in the order of
// the commands, and how much all of them add to each, where adds[i][j] is
// what command i adds to its limit j. A sum past what an int64 holds would
// wrap around, as to an amount that adds nothing, which entitlements.Check
// allows without reading the usage, so a sum that would pass math.MaxInt64
// stays there, more than any quota, whatever the commands after it take
// away, and one that would pass math.MinInt64 is held at it.
func batchAdds(cmds []decoded, adds [][]int64) ([]entitlements.LimitKey, map[entitlements.LimitKey]int64) {
	var keys []entitlements.LimitKey
	sums := map[entitlements.LimitKey]int64{}
	for i, c := range cmds {
		for j, key := range c.spec.limits {
			sum, ok := sums[key]
			if !ok {
				keys = append(keys, key)
			}
			switch a := adds[i][j]; {
			case sum == math.MaxInt64:
			case a > 0 && sum > math.MaxInt64-a:
				sums[key] = math.MaxInt64
			case a < 0 && sum < math.MinInt64-a:
				sums[key] = math.MinInt64
			default:
				sums[key] = sum + a
			}
		}
	}
	return keys, sums
}

// checkRevs returns a *RevisionConflictError if an entity that a command of
// cmds expects a revision of does not have it, before any command applies,
// and records the revisions that it reads (Tx.Changed). It reads the
// revisions of each kind's entities with one call of the kind's Revs, and
// the states of the stale ones with one call of its States, each of which
// asks for each entity once, however many commands expect a revision of it,
// so that a batch over many entities costs a query of each kind rather than
// one of each entity, while it holds the project's lock.
func (p *Pipeline) checkRevs(ctx context.Context, t *Tx, cmds []decoded) error {
	byKind := map[ids.Kind][]ids.UUID{}
	asked := map[Ref]bool{}
	for _, c := range cmds {
		for ref := range c.ExpectedRev {
			if !asked[ref] {
				asked[ref] = true
				byKind[ref.Kind] = append(byKind[ref.Kind], ref.ID)
			}
		}
	}
	// No command has applied yet, so the revisions that the batch has seen
	// (Tx.revs) are the ones that it reads here, of the entities that exist.
	for _, kind := range slices.Sorted(maps.Keys(byKind)) {
		revs, err := p.registry.entities[kind].Revs(ctx, t, byKind[kind])
		if err != nil {
			return fmt.Errorf("commands: read the revisions of the %s entities: %w", kind, err)
		}
		// An ID that the batch did not ask for is not one of its entities.
		for _, id := range byKind[kind] {
			if rev, ok := revs[id]; ok {
				t.revs[Ref{Kind: kind, ID: id}] = rev
			}
		}
	}
	var conflicts []Conflict
	stale := map[ids.Kind][]ids.UUID{}
	states := map[Ref]json.RawMessage{}
	for i, c := range cmds {
		for _, ref := range sortedRefs(c.ExpectedRev) {
			expected := c.ExpectedRev[ref]
			rev, found := t.revs[ref]
			if found && rev == expected {
				continue
			}
			conflicts = append(conflicts, Conflict{Command: i, CommandID: c.ID, Type: c.spec.typ, Ref: ref,
				Expected: expected, Exists: found, Current: rev})
			if _, ok := states[ref]; found && !ok {
				states[ref] = nil
				stale[ref.Kind] = append(stale[ref.Kind], ref.ID)
			}
		}
	}
	if len(conflicts) == 0 {
		return nil
	}
	for _, kind := range slices.Sorted(maps.Keys(stale)) {
		got, err := p.registry.entities[kind].States(ctx, t, stale[kind])
		if err != nil {
			return fmt.Errorf("commands: read the states of the %s entities: %w", kind, err)
		}
		for _, id := range stale[kind] {
			ref := Ref{Kind: kind, ID: id}
			s, ok := got[id]
			if !ok {
				return fmt.Errorf("commands: %s has a revision but no state", encodeRef(ref))
			}
			if states[ref], err = json.Marshal(s); err != nil {
				return fmt.Errorf("commands: encode the state of %s: %w", encodeRef(ref), err)
			}
		}
	}
	for i, c := range conflicts {
		conflicts[i].State = states[c.Ref]
	}
	return &RevisionConflictError{Conflicts: conflicts}
}

// The operation log's JSON (PRD §5.3).
type (
	// logCommand is a command of the operation's payload.
	logCommand struct {
		CommandID string          `json:"command_id"`
		Type      Type            `json:"type"`
		Payload   json.RawMessage `json:"payload"`
	}
	// logStep is a command of the operation's inverse, with its payload as
	// checkSteps encoded it to check it, and the revisions that it expects,
	// by the public IDs of their entities, as a client sends them (plan item
	// 37), if it expects any.
	logStep struct {
		Type        Type             `json:"type"`
		Payload     json.RawMessage  `json:"payload"`
		ExpectedRev map[string]int64 `json:"expected_rev,omitempty"`
		// touches are the kinds of entity that the step's type touches, and
		// expects the entities whose revisions after the batch it expects,
		// which Tx.Changed gives it as its command reports them, but for
		// those that a step of a command before it in the batch expects.
		touches []ids.Kind
		expects []Ref
	}
	// logRef is an entity of the operation's entity_refs, with its revision
	// after the batch.
	logRef struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
		Rev  int64  `json:"rev"`
	}
)

// record appends the batch of the commands cmds, whose IDs are commandIDs,
// which res says what they did, to the project's operation log, records each
// command as applied, and, if a client sent the batch (sent), what the batch
// was answered with as its idempotency record, and sets the project's seq. It
// returns the batch's undo (Result.Undo), as the log keeps it, so that a
// replay of the batch, which reads it from the log, answers it alike.
// inverses are the commands' inverses, and undoable says whether every
// command has one.
func (p *Pipeline) record(ctx context.Context, q *queries.Queries, t *Tx, cmds []decoded, commandIDs []pgtype.UUID,
	sent bool, res Result, inverses [][]logStep, undoable bool) ([]UndoCommand, error) {
	payload := make([]logCommand, len(cmds))
	for i, c := range cmds {
		payload[i] = logCommand{CommandID: c.ID.String(), Type: c.spec.typ, Payload: c.encoded}
	}
	typ := batchType
	if len(cmds) == 1 {
		typ = string(cmds[0].spec.typ)
	}
	// Undoing the batch undoes its commands from the last to the first, each
	// step expecting the revisions that the batch left of what it expects.
	var inverse []byte
	if undoable {
		steps := []logStep{}
		for i := len(inverses) - 1; i >= 0; i-- {
			for _, st := range inverses[i] {
				if len(st.expects) > 0 {
					st.ExpectedRev = make(map[string]int64, len(st.expects))
					for _, ref := range st.expects {
						st.ExpectedRev[encodeRef(ref)] = t.revs[ref]
					}
				}
				steps = append(steps, st)
			}
		}
		var err error
		if inverse, err = json.Marshal(steps); err != nil {
			return nil, fmt.Errorf("commands: encode the inverse: %w", err)
		}
	}
	refs := make([]logRef, len(res.Revisions))
	for i, r := range res.Revisions {
		refs[i] = logRef{Kind: r.Kind.String(), ID: r.ID.String(), Rev: r.Rev}
	}
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("commands: encode the payload: %w", err)
	}
	encodedRefs, err := json.Marshal(refs)
	if err != nil {
		return nil, fmt.Errorf("commands: encode the entity refs: %w", err)
	}
	ws, project := nullUUID(t.workspace), nullUUID(t.project)
	stored, err := q.InsertOperation(ctx, queries.InsertOperationParams{ID: nullUUID(ids.New()), WorkspaceID: ws,
		ProjectID: project, Seq: res.Seq, CommandIds: commandIDs, ActorUserID: nullUUID(t.actor.UserID),
		ActorTokenID: nullUUID(t.actor.Token.TokenID()), Type: typ, Payload: encodedPayload, Inverse: inverse,
		EntityRefs: encodedRefs})
	if err != nil {
		return nil, fmt.Errorf("commands: append the operation: %w", err)
	}
	undo, err := undoOf(stored)
	if err != nil {
		return nil, fmt.Errorf("commands: the operation's inverse: %w", err)
	}
	if err := q.InsertAppliedCommands(ctx, queries.InsertAppliedCommandsParams{WorkspaceID: ws, ProjectID: project,
		Seq: res.Seq, CommandIds: commandIDs}); err != nil {
		return nil, fmt.Errorf("commands: record the applied commands: %w", err)
	}
	// A batch of the server's own, which nobody sends again, is recognised
	// by its applied commands alone. The record keeps the batch's seq, whose
	// operation keeps its undo, which a replay reads from there (undoAt).
	if sent {
		if err := remember(ctx, q, t, commandIDs, answer{Seq: res.Seq, Revisions: refs,
			NotReconnected: storedUnreconnectedOf(res.NotReconnected)}); err != nil {
			return nil, err
		}
	}
	if err := q.SetProjectSeq(ctx, queries.SetProjectSeqParams{Seq: res.Seq, WorkspaceID: ws,
		ID: project}); err != nil {
		return nil, fmt.Errorf("commands: set the project's seq: %w", err)
	}
	return undo, nil
}

// undoOf returns the undo (Result.Undo) that inverse, an operation's inverse
// as the operation log keeps it, holds, or nil if it is null, as for a batch
// that cannot be undone. The log's jsonb keeps an object's keys in an order of
// its own, so a payload read from it is the same whenever it is read, which a
// batch's answer and a replay of it both read, and which a client sends back as
// it is. jsonb writes a space after each colon and comma, which undoOf drops,
// so that each payload is compact, as LogBytes counts it.
func undoOf(inverse []byte) ([]UndoCommand, error) {
	if inverse == nil {
		return nil, nil
	}
	var steps []logStep
	if err := json.Unmarshal(inverse, &steps); err != nil {
		return nil, err
	}
	undo := make([]UndoCommand, len(steps))
	for i, st := range steps {
		var payload bytes.Buffer
		payload.Grow(len(st.Payload))
		if err := json.Compact(&payload, st.Payload); err != nil {
			return nil, fmt.Errorf("step %d's payload: %w", i, err)
		}
		undo[i] = UndoCommand{Type: st.Type, Payload: payload.Bytes(),
			ExpectedRev: make(map[Ref]int64, len(st.ExpectedRev))}
		for key, rev := range st.ExpectedRev {
			kind, id, err := ids.DecodeAny(key)
			if err != nil {
				return nil, fmt.Errorf("step %d expects the revision of %q: %w", i, key, err)
			}
			undo[i].ExpectedRev[Ref{Kind: kind, ID: id}] = rev
		}
	}
	return undo, nil
}

// undoAt returns the undo of the project's operation of the seq, which t, its
// transaction, reads from the operation log, as the batch that the operation
// applied was answered with. An operation that the log no longer holds, as
// one that compaction dropped (PRD FR-GDPR-03), has none.
func undoAt(ctx context.Context, q *queries.Queries, t *Tx, seq int64) ([]UndoCommand, error) {
	inverse, err := q.OperationInverse(ctx, queries.OperationInverseParams{WorkspaceID: nullUUID(t.workspace),
		ProjectID: nullUUID(t.project), Seq: seq})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("commands: read the batch's undo: %w", err)
	}
	undo, err := undoOf(inverse)
	if err != nil {
		return nil, fmt.Errorf("commands: the inverse of the operation %d: %w", seq, err)
	}
	return undo, nil
}

// nullUUID returns id as a uuid column, null if id is zero.
func nullUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != (ids.UUID{})}
}
