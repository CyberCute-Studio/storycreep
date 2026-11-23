package commands

import (
	"context"
	"errors"
	"maps"
	"math"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
)

// populating is the type that what Populate's fn applies is reported by. It
// is registered as no command, so that no client can send it, and it is no
// type of the operation log, which an import does not write to.
const populating Type = "project.populate"

// Populate gives the project projectID, which the transaction tx has just
// created in the workspace workspaceID, the content that fn applies, as parts
// (Tx.ApplyPart) of a batch of its own, so that an imported project's content
// goes through the commands that any other content does, with their checks
// (PRD §10.4, FR-DATA-01), in the one transaction that creates the project, so
// that an import is all or nothing. Once fn returns, it runs the pipeline's
// derivations, which compile the expressions and write the indexes, and
// records what the parts added to the workspace's usage.
//
// The actor needs the workspace's authz.ManageProjects, which creating a
// project needs, and which stands for the capability of every part. The batch
// has no inverse, as an import cannot be undone (FR-EDIT-08 AC1), and writes
// nothing to the operation log: a new project's content is where its log
// starts, as what a project is seeded with is. A part that adds to a limit of
// the plans is refused, as nothing checked it, so the caller checks those.
func (p *Pipeline) Populate(ctx context.Context, tx pgx.Tx, actor Actor, workspaceID, projectID ids.UUID,
	fn func(t *Tx) error) error {
	roles, err := p.roles(ctx, queries.New(tx), actor.UserID, workspaceID, projectID)
	if err != nil {
		return err
	}
	if err := authz.Check(roles[authz.Workspace], authz.ManageProjects); err != nil {
		return err
	}
	t := &Tx{tx: tx, pipeline: p, actor: actor, roles: roles, project: projectID, workspace: workspaceID,
		at: map[Ref]int{}, revs: map[Ref]int64{}, expecting: map[ids.Kind][]int{}, expected: map[Ref]struct{}{},
		maxUndo: math.MaxInt, maxUndoSteps: math.MaxInt, usage: map[usage.Key]int64{},
		checked: map[entitlements.LimitKey]int64{}}
	t.current = &spec{typ: populating, capability: authz.ManageProjects, populates: true,
		touches: slices.Sorted(maps.Keys(p.registry.entities))}
	err = fn(t)
	t.current = nil
	if err != nil {
		return err
	}
	if err := t.derive(ctx); err != nil {
		return err
	}
	if err := t.addCounts(ctx); err != nil {
		return err
	}
	return t.recordUsage(ctx)
}

// PartRefusal returns the field and the message of a part's refusal, which
// ApplyPart returns for what is wrong with the part's payload, or with the
// project that it applies to, and true, or false for any other error.
func PartRefusal(err error) (field, message string, ok bool) {
	var r *commandRefusal
	if !errors.As(err, &r) {
		return "", "", false
	}
	return r.field, r.message, true
}

// PartReason returns the reason of a part's refusal, or nil: the error that
// the part refused its payload for (Refuse), as a key that another entity
// has.
func PartReason(err error) error {
	var r *commandRefusal
	if !errors.As(err, &r) {
		return nil
	}
	return r.reason
}
