package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// Status says how a batch's Result came about.
type Status string

const (
	// Applied is a batch that applied, or one sent again while its
	// idempotency record is kept, which is answered as it was then.
	Applied Status = "applied"
	// AlreadyApplied is a batch whose commands were applied, sent again once
	// the record of the batch that applied them has been pruned, or grouped
	// otherwise than they were applied in (FR-API-03).
	AlreadyApplied Status = "already_applied"
)

// IdempotencyMismatchError is a batch with commands that were applied and
// commands that were not, as only a client that grouped its commands anew
// sends, because a batch applies whole or not at all (PRD FR-API-03). It
// applies nothing. The API answers it with 422 idempotency_mismatch.
type IdempotencyMismatchError struct {
	// Applied are the batch's commands that were applied, in the order of the
	// batch.
	Applied []ids.UUID
}

func (e *IdempotencyMismatchError) Error() string {
	return fmt.Sprintf("commands: %d of the batch's commands were applied already, and the others were not",
		len(e.Applied))
}

// The answer to a batch, as idempotency_record keeps it (PRD §5.3).
type (
	// answer is a batch's answer: the Result of a batch that applied, or what
	// refused one.
	answer struct {
		Seq            int64                 `json:"seq,omitempty"`
		Revisions      []logRef              `json:"revisions,omitempty"`
		NotReconnected []storedUnreconnected `json:"not_reconnected,omitempty"`
		Refusal        *refusal              `json:"refusal,omitempty"`
	}
	// storedUnreconnected is an Unreconnected: an entity that the batch could
	// not reconnect, with the entities that it waits for.
	storedUnreconnected struct {
		Kind       string      `json:"kind"`
		ID         string      `json:"id"`
		WaitingFor []storedRef `json:"waiting_for"`
	}
	// storedRef is an entity, by its kind and its UUID.
	storedRef struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	}
	// refusal is the refusal of a batch that the batch and the state of its
	// project decided (remembered): its reason, with the conflicts of a
	// revision conflict and the states of their entities, and the limit of a
	// limit's.
	refusal struct {
		Reason    string           `json:"reason"`
		Conflicts []storedConflict `json:"conflicts,omitempty"`
		States    []storedState    `json:"states,omitempty"`
		Limit     *storedLimit     `json:"limit,omitempty"`
	}
	// storedConflict is a Conflict, without its State, which the refusal's
	// States hold, once for each entity, however many commands expected a
	// revision of it, so that a batch of many commands that expect one
	// entity's revision does not keep the entity's state once for each.
	storedConflict struct {
		Command   int    `json:"command"`
		CommandID string `json:"command_id"`
		Type      Type   `json:"type"`
		Kind      string `json:"kind"`
		ID        string `json:"id"`
		Expected  int64  `json:"expected"`
		Exists    bool   `json:"exists"`
		Current   int64  `json:"current,omitempty"`
	}
	// storedState is the state of an entity of a revision conflict that still
	// exists, as its conflicts encoded it, kept as a string so that the record
	// gives it back as it was, where jsonb would order the keys of an object
	// its own way.
	storedState struct {
		Kind  string `json:"kind"`
		ID    string `json:"id"`
		State string `json:"state"`
	}
	// storedLimit is an *entitlements.LimitError.
	storedLimit struct {
		Key        entitlements.LimitKey `json:"key"`
		Usage      int64                 `json:"usage"`
		Limit      int64                 `json:"limit"`
		UpgradeURL string                `json:"upgrade_url,omitempty"`
	}
)

// The reasons of a remembered refusal, as the records keep them, which
// outlive a release of the server for as long as the records are kept, so
// that they are literals of their own, which renaming a metric's label or an
// API's code leaves as they are.
const (
	reasonArchived         = "archived"
	reasonLimit            = "limit"
	reasonRevisionConflict = "stale_revisions"
)

// refusalOf returns the refusal that remembers err, or nil if err is not one
// that the pipeline remembers: a refusal that the batch's commands and the
// state of its project decide, before any command applies, which sending the
// batch again would meet again until the project changes, as ErrProjectArchived,
// an *entitlements.LimitError, and a *RevisionConflictError (FR-API-03). A
// batch that is not valid, of a project that is not found, or refused by the
// actor's role or token, by a command, or by a failure is not remembered, and
// is checked anew when it is sent again, which is safe, because a refusal
// changes nothing.
func refusalOf(err error) *refusal {
	var limit *entitlements.LimitError
	var conflict *RevisionConflictError
	switch {
	case errors.Is(err, ErrProjectArchived):
		return &refusal{Reason: reasonArchived}
	case errors.As(err, &limit):
		return &refusal{Reason: reasonLimit, Limit: &storedLimit{Key: limit.Key, Usage: limit.Usage,
			Limit: limit.Limit, UpgradeURL: limit.UpgradeURL}}
	case errors.As(err, &conflict):
		r := &refusal{Reason: reasonRevisionConflict, Conflicts: make([]storedConflict, len(conflict.Conflicts))}
		stored := map[Ref]bool{}
		for i, c := range conflict.Conflicts {
			kind, id := c.Kind.String(), c.ID.String()
			r.Conflicts[i] = storedConflict{Command: c.Command, CommandID: c.CommandID.String(), Type: c.Type,
				Kind: kind, ID: id, Expected: c.Expected, Exists: c.Exists, Current: c.Current}
			// Every conflict of an entity has its one state (checkRevs).
			if c.Exists && !stored[c.Ref] {
				stored[c.Ref] = true
				r.States = append(r.States, storedState{Kind: kind, ID: id, State: string(c.State)})
			}
		}
		return r
	}
	return nil
}

// err returns the error that r remembers.
func (r *refusal) err() (error, error) {
	switch r.Reason {
	case reasonArchived:
		return ErrProjectArchived, nil
	case reasonLimit:
		if r.Limit == nil {
			return nil, errors.New("a limit's refusal without its limit")
		}
		return &entitlements.LimitError{Key: r.Limit.Key, Usage: r.Limit.Usage, Limit: r.Limit.Limit,
			UpgradeURL: r.Limit.UpgradeURL}, nil
	case reasonRevisionConflict:
		// Each entity's state is decoded once, and its conflicts share it,
		// as those of the refusal that the record remembers did.
		states := make(map[Ref]json.RawMessage, len(r.States))
		for i, s := range r.States {
			ref, err := parseRef(s.Kind, s.ID)
			if err != nil {
				return nil, fmt.Errorf("the entity of state %d: %w", i, err)
			}
			states[ref] = json.RawMessage(s.State)
		}
		conflict := &RevisionConflictError{Conflicts: make([]Conflict, len(r.Conflicts))}
		for i, c := range r.Conflicts {
			commandID, err := ids.ParseUUID(c.CommandID)
			if err != nil {
				return nil, fmt.Errorf("the command ID of conflict %d: %w", i, err)
			}
			ref, err := parseRef(c.Kind, c.ID)
			if err != nil {
				return nil, fmt.Errorf("the entity of conflict %d: %w", i, err)
			}
			conflict.Conflicts[i] = Conflict{Command: c.Command, CommandID: commandID, Type: c.Type, Ref: ref,
				Expected: c.Expected, Exists: c.Exists, Current: c.Current}
			if c.Exists {
				state, ok := states[ref]
				if !ok {
					return nil, fmt.Errorf("conflict %d is of an entity that exists, without its state", i)
				}
				conflict.Conflicts[i].State = state
			}
		}
		return conflict, nil
	}
	return nil, fmt.Errorf("the refusal's reason %q is not one", r.Reason)
}

// kindsByName are the kinds of entity, by their names, as ids.Kind.String
// names them.
var kindsByName = func() map[string]ids.Kind {
	m := map[string]ids.Kind{}
	for _, k := range ids.Kinds() {
		m[k.String()] = k
	}
	return m
}()

// parseRef returns the entity of the kind named kind, as ids.Kind.String
// names it, with the UUID id, as logRef holds them.
func parseRef(kind, id string) (Ref, error) {
	k, ok := kindsByName[kind]
	if !ok {
		return Ref{}, fmt.Errorf("%q is not a kind of entity", kind)
	}
	u, err := ids.ParseUUID(id)
	if err != nil {
		return Ref{}, err
	}
	return Ref{Kind: k, ID: u}, nil
}

// replay is the answer to a batch whose commands were sent before: the
// Result of a batch that applied, or the error that refused it, which its
// idempotency record remembers.
type replay struct {
	res Result
	err error
}

// idempotent returns the answer to the batch of the commands cmds, whose IDs
// are commandIDs, in t, its transaction, which holds the project's lock, if
// the batch was sent before (FR-API-03), or nil if it is to be checked and
// applied as a new batch. A batch whose commands are those of a batch whose
// idempotency record is kept, in the same order, is answered as that batch
// was, applied or refused. Any other batch whose commands were all applied is
// answered with AlreadyApplied; one with commands that were applied and
// commands that were not is refused with an *IdempotencyMismatchError, which
// idempotent returns as its error, having written nothing; and one with none
// that were applied is new: its commands' records, of refused batches, are
// deleted, so that sending one of those batches again checks it again too. A
// new batch, whose commands have neither, costs one read.
func (p *Pipeline) idempotent(ctx context.Context, q *queries.Queries, t *Tx, cmds []decoded,
	commandIDs []pgtype.UUID) (*replay, error) {
	ws, project := nullUUID(t.workspace), nullUUID(t.project)
	states, err := q.CommandStates(ctx, queries.CommandStatesParams{WorkspaceID: ws, ProjectID: project,
		CommandIds: commandIDs})
	if err != nil {
		return nil, fmt.Errorf("commands: read the batch's commands' idempotency: %w", err)
	}
	seqs := make(map[ids.UUID]int64, len(states))
	recorded, first := false, false
	for _, s := range states {
		if s.Seq.Valid {
			seqs[s.CommandID.Bytes] = s.Seq.Int64
		}
		if s.BatchCommandID.Valid {
			recorded = true
			// The batch's first command is the first of a recorded batch.
			first = first || s.CommandID == commandIDs[0] && s.BatchCommandID == commandIDs[0]
		}
	}
	if first {
		rec, err := q.BatchRecord(ctx, queries.BatchRecordParams{WorkspaceID: ws, ProjectID: project,
			CommandID: commandIDs[0]})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// The pruning job, which does not wait for the project's lock,
			// deleted it since.
		case err != nil:
			return nil, fmt.Errorf("commands: read the batch's idempotency record: %w", err)
		case slices.Equal(rec.CommandIds, commandIDs):
			r, err := stored(rec.Response)
			if err != nil {
				return nil, fmt.Errorf("commands: the idempotency record of the batch of %s: %w", cmds[0].ID, err)
			}
			// The record keeps the seq of a batch that applied, whose
			// operation keeps the batch's undo.
			if r.err == nil {
				if r.res.Undo, err = undoAt(ctx, q, t, r.res.Seq); err != nil {
					return nil, err
				}
			}
			return r, nil
		}
	}
	if len(seqs) == 0 {
		if recorded {
			if err := q.DeleteBatchRecords(ctx, queries.DeleteBatchRecordsParams{WorkspaceID: ws,
				ProjectID: project, CommandIds: commandIDs}); err != nil {
				return nil, fmt.Errorf("commands: delete the idempotency records of the batch's commands: %w", err)
			}
		}
		return nil, nil
	}
	if len(seqs) < len(cmds) {
		mismatch := &IdempotencyMismatchError{}
		for _, c := range cmds {
			if _, ok := seqs[c.ID]; ok {
				mismatch.Applied = append(mismatch.Applied, c.ID)
			}
		}
		return nil, mismatch
	}
	res, err := p.alreadyApplied(ctx, q, t, seqs)
	if err != nil {
		return nil, err
	}
	return &replay{res: res}, nil
}

// stored returns the answer that the idempotency record response holds.
func stored(response []byte) (*replay, error) {
	var a answer
	if err := json.Unmarshal(response, &a); err != nil {
		return nil, err
	}
	if a.Refusal != nil {
		err, problem := a.Refusal.err()
		if problem != nil {
			return nil, problem
		}
		return &replay{err: err}, nil
	}
	res := Result{Status: Applied, Seq: a.Seq, Revisions: make([]Revision, len(a.Revisions)),
		NotReconnected: make([]Unreconnected, len(a.NotReconnected))}
	for i, r := range a.Revisions {
		ref, err := parseRef(r.Kind, r.ID)
		if err != nil {
			return nil, fmt.Errorf("revision %d: %w", i, err)
		}
		res.Revisions[i] = Revision{Ref: ref, Rev: r.Rev}
	}
	for i, u := range a.NotReconnected {
		ref, err := parseRef(u.Kind, u.ID)
		if err != nil {
			return nil, fmt.Errorf("the entity not reconnected %d: %w", i, err)
		}
		res.NotReconnected[i] = Unreconnected{Ref: ref, WaitingFor: make([]Ref, len(u.WaitingFor))}
		for j, w := range u.WaitingFor {
			if res.NotReconnected[i].WaitingFor[j], err = parseRef(w.Kind, w.ID); err != nil {
				return nil, fmt.Errorf("what the entity not reconnected %d waits for: %w", i, err)
			}
		}
	}
	return &replay{res: res}, nil
}

// storedUnreconnectedOf returns the entities that a batch could not
// reconnect, as its idempotency record keeps them.
func storedUnreconnectedOf(left []Unreconnected) []storedUnreconnected {
	out := make([]storedUnreconnected, len(left))
	for i, u := range left {
		out[i] = storedUnreconnected{Kind: u.Kind.String(), ID: u.ID.String(),
			WaitingFor: make([]storedRef, len(u.WaitingFor))}
		for j, w := range u.WaitingFor {
			out[i].WaitingFor[j] = storedRef{Kind: w.Kind.String(), ID: w.ID.String()}
		}
	}
	return out
}

// alreadyApplied returns the answer to a batch whose commands were all
// applied, each by the batch of its seq in seqs, once the record of that
// batch is pruned, or that groups them otherwise (FR-API-03 AC4, AC6):
// AlreadyApplied, with the latest of their seqs, and the current revisions
// of the entities that their batches changed, as their operations name
// them, in the order of the batches and of each batch's changes. An entity
// that no longer exists, as one purged from the trash, is left out.
func (p *Pipeline) alreadyApplied(ctx context.Context, q *queries.Queries, t *Tx, seqs map[ids.UUID]int64) (Result,
	error) {
	batches := slices.Compact(slices.Sorted(maps.Values(seqs)))
	docs, err := q.OperationEntityRefs(ctx, queries.OperationEntityRefsParams{WorkspaceID: nullUUID(t.workspace),
		ProjectID: nullUUID(t.project), Seqs: batches})
	if err != nil {
		return Result{}, fmt.Errorf("commands: read the entities of the applied batches: %w", err)
	}
	var refs []Ref
	seen := map[Ref]bool{}
	byKind := map[ids.Kind][]ids.UUID{}
	for _, doc := range docs {
		var changed []logRef
		if err := json.Unmarshal(doc, &changed); err != nil {
			return Result{}, fmt.Errorf("commands: decode an operation's entity refs: %w", err)
		}
		for _, c := range changed {
			ref, err := parseRef(c.Kind, c.ID)
			if err != nil {
				return Result{}, fmt.Errorf("commands: an operation's entity ref: %w", err)
			}
			if seen[ref] {
				continue
			}
			seen[ref] = true
			refs = append(refs, ref)
			byKind[ref.Kind] = append(byKind[ref.Kind], ref.ID)
		}
	}
	current := map[Ref]int64{}
	for _, kind := range slices.Sorted(maps.Keys(byKind)) {
		entities := byKind[kind]
		e, ok := p.registry.entities[kind]
		if !ok {
			// No registered command changes the kind any more, so no
			// command of the batch can have changed it.
			continue
		}
		revs, err := e.Revs(ctx, t, entities)
		if err != nil {
			return Result{}, fmt.Errorf("commands: read the revisions of the %s entities: %w", kind, err)
		}
		for _, id := range entities {
			if rev, ok := revs[id]; ok {
				current[Ref{Kind: kind, ID: id}] = rev
			}
		}
	}
	res := Result{Status: AlreadyApplied, Seq: batches[len(batches)-1], Revisions: []Revision{},
		NotReconnected: []Unreconnected{}}
	for _, ref := range refs {
		if rev, ok := current[ref]; ok {
			res.Revisions = append(res.Revisions, Revision{Ref: ref, Rev: rev})
		}
	}
	return res, nil
}

// remember records a, the answer to the batch of the commands commandIDs, in
// order, as its idempotency record, in q's transaction, the batch's.
func remember(ctx context.Context, q *queries.Queries, t *Tx, commandIDs []pgtype.UUID, a answer) error {
	doc, err := json.Marshal(a)
	if err != nil {
		return fmt.Errorf("commands: encode the batch's answer: %w", err)
	}
	if err := q.InsertBatchRecord(ctx, queries.InsertBatchRecordParams{WorkspaceID: nullUUID(t.workspace),
		ProjectID: nullUUID(t.project), CommandIds: commandIDs, Response: doc}); err != nil {
		return fmt.Errorf("commands: record the batch's answer: %w", err)
	}
	return nil
}
