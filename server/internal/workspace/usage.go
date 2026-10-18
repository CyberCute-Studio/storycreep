package workspace

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
)

// Usage is how much of a key a workspace uses (PRD FR-ENT-02), and, for a
// key that names a limit of the plans, its quota of the limit.
type Usage struct {
	Key  usage.Key
	Used int64
	// Limited says that the key names a limit of the plans, whose quota is
	// Quota, and that Warning is set when the workspace uses at least 80% of
	// a quota that is a number (FR-ENT-03). A key that the plans only meter,
	// as nodes, is not limited.
	Limited bool
	Quota   entitlements.Quota
	Warning bool
}

// Usage returns how much of each key of usage the workspace id uses, in the
// order of usage.Keys, with its quota of each that names a limit, which its
// settings screen and the invitation dialog show (FR-ENT-02 AC2, FR-ENT-03).
// Only an owner or an admin of the workspace may read it, as its settings
// (§5.6). It returns ErrNotFound if the workspace does not exist or the user
// userID's role in it is none, and ErrInsufficientRole unless the role is
// owner or admin. It reads the usage of every key in one statement, without
// a lock, so that reading it never waits for a change of the workspace.
func (s *Service) Usage(ctx context.Context, userID, id ids.UUID) ([]Usage, error) {
	var out []Usage
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: id, UserID: userID}, func(tx pgx.Tx) error {
		r, err := s.Role(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		if err := authz.Check(r, authz.ManageWorkspaceSettings); err != nil {
			return err
		}
		used, err := usage.Read(ctx, tx, id)
		if err != nil {
			return err
		}
		for _, key := range usage.Keys() {
			u := Usage{Key: key, Used: used[key]}
			if l := entitlements.LimitKey(key); l.Checked() {
				if u.Quota, err = s.ent.Limit(ctx, tx, id, l); err != nil {
					return err
				}
				u.Limited, u.Warning = true, u.Quota.Warns(u.Used)
			}
			out = append(out, u)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
