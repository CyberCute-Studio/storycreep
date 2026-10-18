package project_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// uuid returns id as a uuid column.
func uuid(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

// TestGrantsIsolation tries the queries of the project grants from another
// workspace's transaction, where row-level security must keep them from
// reading, changing, or adding a grant, or locking a membership
// (NFR-SEC-01).
//
// Verifies: NFR-SEC-01
func TestGrantsIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := isolationtest.New(t, dbtest.New(t), isolationtest.Tables)
	// grant returns the membership, the member, and the project of the
	// workspace's grant.
	grant := func(w *isolationtest.Workspace) (membership, user, project ids.UUID) {
		if err := f.Admin().QueryRow(ctx, `SELECT g.membership_id, m.user_id, g.project_id FROM project_grant g
			JOIN membership m ON m.id = g.membership_id WHERE g.id = $1`, w.Rows["project_grant"]).Scan(&membership,
			&user, &project); err != nil {
			t.Fatal(err)
		}
		return membership, user, project
	}
	// grants returns the IDs of the grants of rows.
	grants := func(rows []queries.ProjectGrant, err error) ([]ids.UUID, error) {
		var out []ids.UUID
		for _, g := range rows {
			out = append(out, g.ID.Bytes)
		}
		return out, err
	}
	for _, op := range []isolationtest.Op{
		{Name: "a grant", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) ([]ids.UUID,
			error) {
			m, _, p := grant(target)
			row, err := queries.New(tx).ProjectGrant(ctx, queries.ProjectGrantParams{WorkspaceID: uuid(target.ID),
				MembershipID: uuid(m), ProjectID: uuid(p)})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil
			}
			return []ids.UUID{row.ProjectGrant.ID.Bytes}, err
		}},
		{Name: "a project's grants", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			_, _, p := grant(target)
			rows, err := queries.New(tx).ProjectGrants(ctx, queries.ProjectGrantsParams{WorkspaceID: uuid(target.ID),
				ProjectID: uuid(p), MaxRows: 10})
			var out []queries.ProjectGrant
			for _, r := range rows {
				out = append(out, r.ProjectGrant)
			}
			return grants(out, err)
		}},
		{Name: "a membership's grants", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			m, _, _ := grant(target)
			rows, err := queries.New(tx).MembershipGrants(ctx, queries.MembershipGrantsParams{
				WorkspaceID: uuid(target.ID), MembershipID: uuid(m), MaxRows: 10})
			var out []queries.ProjectGrant
			for _, r := range rows {
				out = append(out, r.ProjectGrant)
			}
			return grants(out, err)
		}},
		{Name: "lock a user's membership", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			_, u, _ := grant(target)
			row, err := queries.New(tx).LockMembershipOf(ctx, queries.LockMembershipOfParams{
				WorkspaceID: uuid(target.ID), UserID: uuid(u)})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil
			}
			return []ids.UUID{row.ID.Bytes}, err
		}},
		// On its own workspace's grant, it changes the grant's role; on
		// another's, row-level security refuses the row that it would add.
		{Name: "set a grant", Denied: true, Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			m, _, p := grant(target)
			g, err := queries.New(tx).SetProjectGrant(ctx, queries.SetProjectGrantParams{ID: uuid(ids.New()),
				WorkspaceID: uuid(target.ID), MembershipID: uuid(m), ProjectID: uuid(p), Role: "admin"})
			return []ids.UUID{g.ID.Bytes}, err
		}},
		{Name: "remove a grant", Blind: true, Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			m, _, p := grant(target)
			_, err := queries.New(tx).DeleteProjectGrant(ctx, queries.DeleteProjectGrantParams{
				WorkspaceID: uuid(target.ID), MembershipID: uuid(m), ProjectID: uuid(p)})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil
			}
			return nil, err
		}},
	} {
		f.Check(t, op)
	}
}
