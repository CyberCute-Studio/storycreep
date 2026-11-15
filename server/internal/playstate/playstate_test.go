package playstate_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

// TestPlayStateIsolation tries the package's queries from another
// workspace's transaction, where row-level security must keep them from
// reading or changing its play states (NFR-SEC-01).
//
// Verifies: NFR-SEC-01
func TestPlayStateIsolation(t *testing.T) {
	t.Parallel()
	f := isolationtest.New(t, dbtest.New(t), isolationtest.Tables)
	uuid := func(id ids.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }
	params := func(target *isolationtest.Workspace) (pgtype.UUID, pgtype.UUID, pgtype.UUID) {
		return uuid(target.ID), uuid(target.Rows["project"]), uuid(target.Rows["play_state"])
	}
	one := func(id pgtype.UUID, err error) ([]ids.UUID, error) {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return []ids.UUID{id.Bytes}, err
	}
	many := func(found []ids.UUID, err error) ([]ids.UUID, error) { return found, err }
	for _, op := range []isolationtest.Op{
		{Name: "a play state, in the trash or not", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			ws, p, id := params(target)
			row, err := queries.New(tx).PlayStateAny(ctx, queries.PlayStateAnyParams{WorkspaceID: ws, ProjectID: p,
				ID: id})
			return one(row.ID, err)
		}},
		{Name: "a live play state", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			ws, p, id := params(target)
			row, err := queries.New(tx).PlayState(ctx, queries.PlayStateParams{WorkspaceID: ws, ProjectID: p, ID: id})
			return one(row.LivePlayState.ID, err)
		}},
		{Name: "a project's play states", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			ws, p, _ := params(target)
			rows, err := queries.New(tx).PlayStates(ctx, queries.PlayStatesParams{WorkspaceID: ws, ProjectID: p,
				MaxRows: 100})
			var found []ids.UUID
			for _, r := range rows {
				found = append(found, r.ID.Bytes)
			}
			return many(found, err)
		}},
		{Name: "the revisions of play states", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			ws, p, id := params(target)
			rows, err := queries.New(tx).PlayStateRevs(ctx, queries.PlayStateRevsParams{WorkspaceID: ws, ProjectID: p,
				Ids: []pgtype.UUID{id}})
			var found []ids.UUID
			for _, r := range rows {
				found = append(found, r.ID.Bytes)
			}
			return many(found, err)
		}},
		{Name: "rename a play state", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			ws, p, id := params(target)
			_, err := queries.New(tx).RenamePlayState(ctx, queries.RenamePlayStateParams{Name: "Isolated",
				WorkspaceID: ws, ProjectID: p, ID: id})
			return one(id, err)
		}},
		{Name: "replace a play state's state", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			ws, p, id := params(target)
			_, err := queries.New(tx).UpdatePlayState(ctx, queries.UpdatePlayStateParams{State: []byte(`{"x":1}`),
				Names: []byte(`{}`), WorkspaceID: ws, ProjectID: p, ID: id})
			return one(id, err)
		}},
		{Name: "delete a play state to the trash", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			ws, p, id := params(target)
			_, err := queries.New(tx).TrashPlayState(ctx, queries.TrashPlayStateParams{WorkspaceID: ws, ProjectID: p,
				ID: id})
			return one(id, err)
		}},
	} {
		f.Check(t, op)
	}
}
