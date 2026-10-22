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
// the project do (FR-PROJ-01); the workspace's limits are checked for what
// the batch adds (FR-ENT-01); every expected
// revision of the batch's commands is checked against the project as it is
// before the batch, which is what the client authored the batch against, and
// a stale one refuses the whole batch with the entities' current state
// (FR-API-03 AC3); and the commands are applied, in order, in one
// transaction. A batch that applies is appended to the project's operation
// log with the project's next seq, its inverse, and the entities that it
// changed, and each of its commands is recorded in applied_command, which
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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
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
	// (Spec.Trashed), and for one of a project whose workspace's deletion is
	// pending, which blocks every access to the workspace at once (FR-TEN-05
	// AC1).
	ErrNotFound = errors.New("commands: no such project, or the user has no role on it")
	// ErrProjectArchived is returned for a batch of an archived project with
	// a command that does not apply to an archived project (Spec.Archived),
	// which is read-only (FR-PROJ-01 AC2).
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
}

// Pipeline applies batches of commands.
type Pipeline struct {
	db       DB
	registry *Registry
	policy   role.Policy
	ent      *entitlements.Service
	jobs     Enqueuer
	notifier Notifier
	metrics  *metrics
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
	return &Pipeline{db: db, registry: reg, policy: opts.Policy, ent: opts.Entitlements, jobs: opts.Jobs,
		notifier: opts.Notifier, metrics: newMetrics(reg.Types())}
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
// (Spec.Trashed), or the actor's API token does not cover the project;
// ErrSessionRequired if the actor acts with an API token, and a command is
// one that only a session may apply (Spec.SessionOnly); ErrInsufficientScope,
// as a *scope.InsufficientError, if the token does not hold a command's
// scope (Spec.Scope); ErrInsufficientRole or ErrOwnerRequired if the actor's
// role does not allow a command's operation; an *IdempotencyMismatchError if
// some of the batch's commands were applied before, and others were not;
// ErrProjectArchived if the project is archived and a command does not apply
// to it then (Spec.Archived); an *entitlements.LimitError if the batch would
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
		case deleted && !allApply(cmds, trashed):
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
		if locked.Deleted && !allApply(cmds, trashed) {
			return ErrNotFound
		}
		t := &Tx{tx: tx, pipeline: p, actor: actor, project: projectID, workspace: workspaceID, at: map[Ref]int{},
			revs: map[Ref]int64{}, usage: map[usage.Key]int64{}}
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
			if refused, ok := refusedBy(err, i, c); ok {
				return Result{}, refused
			}
			if err != nil {
				return Result{}, fmt.Errorf("commands: the inverse of command %d (%s): %w", i, c.spec.typ, err)
			}
		}
		// Only Apply reports changes (Tx.Changed): neither the reads of the
		// revisions nor an inverse, which only reads.
		t.current = c.spec
		err := c.spec.apply(ctx, t, c.payload)
		t.current = nil
		if refused, ok := refusedBy(err, i, c); ok {
			return Result{}, refused
		}
		if err != nil {
			return Result{}, fmt.Errorf("commands: command %d (%s): %w", i, c.spec.typ, err)
		}
	}
	if err := t.recordUsage(ctx); err != nil {
		return Result{}, err
	}
	// Revisions is never nil, as that of a batch answered from its record is
	// not, so that the batch's answer is the same either way.
	res := Result{Status: Applied, Seq: seq + 1, Revisions: append([]Revision{}, t.changed...)}
	if err := p.record(ctx, q, t, cmds, commandIDs, sent, res, inverses, undoable); err != nil {
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
	if archivedProject && !allApply(cmds, archived) {
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
// trash, and to an archived one (Spec.Trashed, Spec.Archived).
func trashed(c decoded) bool  { return c.spec.trashed }
func archived(c decoded) bool { return c.spec.archived }

// allApply reports whether every command of cmds applies, as applies says.
func allApply(cmds []decoded, applies func(decoded) bool) bool {
	return !slices.ContainsFunc(cmds, func(c decoded) bool { return !applies(c) })
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
	// checkSteps encoded it to check it.
	logStep struct {
		Type    Type            `json:"type"`
		Payload json.RawMessage `json:"payload"`
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
// was answered with as its idempotency record, and sets the project's seq.
// inverses are the commands' inverses, and undoable says whether every
// command has one.
func (p *Pipeline) record(ctx context.Context, q *queries.Queries, t *Tx, cmds []decoded, commandIDs []pgtype.UUID,
	sent bool, res Result, inverses [][]logStep, undoable bool) error {
	payload := make([]logCommand, len(cmds))
	for i, c := range cmds {
		payload[i] = logCommand{CommandID: c.ID.String(), Type: c.spec.typ, Payload: c.encoded}
	}
	typ := batchType
	if len(cmds) == 1 {
		typ = string(cmds[0].spec.typ)
	}
	// Undoing the batch undoes its commands from the last to the first.
	var inverse []byte
	if undoable {
		steps := []logStep{}
		for i := len(inverses) - 1; i >= 0; i-- {
			steps = append(steps, inverses[i]...)
		}
		var err error
		if inverse, err = json.Marshal(steps); err != nil {
			return fmt.Errorf("commands: encode the inverse: %w", err)
		}
	}
	refs := make([]logRef, len(res.Revisions))
	for i, r := range res.Revisions {
		refs[i] = logRef{Kind: r.Kind.String(), ID: r.ID.String(), Rev: r.Rev}
	}
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("commands: encode the payload: %w", err)
	}
	encodedRefs, err := json.Marshal(refs)
	if err != nil {
		return fmt.Errorf("commands: encode the entity refs: %w", err)
	}
	ws, project := nullUUID(t.workspace), nullUUID(t.project)
	if err := q.InsertOperation(ctx, queries.InsertOperationParams{ID: nullUUID(ids.New()), WorkspaceID: ws,
		ProjectID: project, Seq: res.Seq, CommandIds: commandIDs, ActorUserID: nullUUID(t.actor.UserID),
		ActorTokenID: nullUUID(t.actor.Token.TokenID()), Type: typ, Payload: encodedPayload, Inverse: inverse,
		EntityRefs: encodedRefs}); err != nil {
		return fmt.Errorf("commands: append the operation: %w", err)
	}
	if err := q.InsertAppliedCommands(ctx, queries.InsertAppliedCommandsParams{WorkspaceID: ws, ProjectID: project,
		Seq: res.Seq, CommandIds: commandIDs}); err != nil {
		return fmt.Errorf("commands: record the applied commands: %w", err)
	}
	// A batch of the server's own, which nobody sends again, is recognised
	// by its applied commands alone.
	if sent {
		if err := remember(ctx, q, t, commandIDs, answer{Seq: res.Seq, Revisions: refs}); err != nil {
			return err
		}
	}
	if err := q.SetProjectSeq(ctx, queries.SetProjectSeqParams{Seq: res.Seq, WorkspaceID: ws,
		ID: project}); err != nil {
		return fmt.Errorf("commands: set the project's seq: %w", err)
	}
	return nil
}

// nullUUID returns id as a uuid column, null if id is zero.
func nullUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != (ids.UUID{})}
}
