package apitoken_test

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

// uuid returns id as a uuid column.
func uuid(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

// idsOf returns the IDs of the tokens of a list.
func idsOf[R queries.WorkspaceTokensRow | queries.ProjectTokensRow](rows []R, err error) ([]ids.UUID, error) {
	var out []ids.UUID
	for _, row := range rows {
		out = append(out, queries.WorkspaceTokensRow(row).ID.Bytes)
	}
	return out, err
}

// TestTokensIsolation tries the queries of the API tokens from another
// workspace's transaction, where row-level security must keep them from
// reading, changing, or adding a token (NFR-SEC-01). The lookup of a token by
// its secret's hash, which a request makes before it knows the workspace, is
// the context bootstrap's, which the database's own tests hold to returning
// identifiers alone (§5.4).
//
// Verifies: NFR-SEC-01
func TestTokensIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := isolationtest.New(t, dbtest.New(t), isolationtest.Tables)
	// Each workspace has a token narrowed to its project, which the fixture
	// adds, and one of the whole workspace, which the test adds.
	wide := map[ids.UUID]ids.UUID{}
	for _, w := range []*isolationtest.Workspace{f.A, f.B} {
		id := ids.New()
		if _, err := f.Admin().Exec(ctx, `INSERT INTO api_token (id, workspace_id, user_id, name, token_hash, scopes)
			VALUES ($1, $2, $3, 'wide', $4, ARRAY['snapshot:read'])`, id, w.ID, w.UserID,
			isolationtest.TokenHash()); err != nil {
			t.Fatal(err)
		}
		wide[w.ID] = id
	}
	for _, op := range []isolationtest.Op{
		{Name: "a token for its use", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			id := target.Rows["api_token"]
			_, err := queries.New(tx).TokenForUse(ctx, queries.TokenForUseParams{WorkspaceID: uuid(target.ID),
				ID: uuid(id)})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil
			}
			return []ids.UUID{id}, err
		}},
		{Name: "a workspace's tokens", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			return idsOf(queries.New(tx).WorkspaceTokens(ctx, queries.WorkspaceTokensParams{
				WorkspaceID: uuid(target.ID), MaxRows: 10}))
		}},
		{Name: "a user's tokens of a workspace", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			return idsOf(queries.New(tx).WorkspaceTokens(ctx, queries.WorkspaceTokensParams{
				WorkspaceID: uuid(target.ID), UserID: uuid(target.UserID), MaxRows: 10}))
		}},
		{Name: "a project's tokens", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			return idsOf(queries.New(tx).ProjectTokens(ctx, queries.ProjectTokensParams{WorkspaceID: uuid(target.ID),
				ProjectID: uuid(target.Rows["project"]), MaxRows: 10}))
		}},
		{Name: "lock a token", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) ([]ids.UUID,
			error) {
			tok, err := queries.New(tx).LockToken(ctx, queries.LockTokenParams{WorkspaceID: uuid(target.ID),
				ID: uuid(target.Rows["api_token"]), ProjectID: uuid(target.Rows["project"])})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil
			}
			return []ids.UUID{tok.ID.Bytes}, err
		}},
		{Name: "record a token's use", Blind: true, Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			return nil, queries.New(tx).TouchToken(ctx, queries.TouchTokenParams{WorkspaceID: uuid(target.ID),
				ID: uuid(wide[target.ID])})
		}},
		{Name: "revoke a token", Blind: true, Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			return nil, queries.New(tx).RevokeToken(ctx, queries.RevokeTokenParams{WorkspaceID: uuid(target.ID),
				ID: uuid(target.Rows["api_token"])})
		}},
		{Name: "revoke a user's tokens", Blind: true, Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			_, err := queries.New(tx).RevokeUserTokens(ctx, queries.RevokeUserTokensParams{
				WorkspaceID: uuid(target.ID), UserID: uuid(target.UserID)})
			return nil, err
		}},
		{Name: "add a token", Denied: true, Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			id := ids.New()
			_, err := queries.New(tx).InsertToken(ctx, queries.InsertTokenParams{ID: uuid(id),
				WorkspaceID: uuid(target.ID), UserID: uuid(target.UserID), Name: "added",
				TokenHash: isolationtest.TokenHash(), Scopes: []string{"project:read"}})
			return []ids.UUID{id}, err
		}},
	} {
		f.Check(t, op)
	}
}
