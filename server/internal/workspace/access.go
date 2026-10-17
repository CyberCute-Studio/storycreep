package workspace

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/role"
)

// The end of instance-admin access (PRD §5.5, FR-TEN-02 AC4). While the
// InstanceAdminAccess capability is on, an instance admin acts as the owner
// of every workspace (role.Policy.Workspace), and so can make invitations,
// and take their links as text, that their own role in a workspace, by
// their membership, may not give: an owner's, or any, where they are not a
// member. When that access ends, those invitations are revoked, as a
// demotion revokes a member's (ChangeRole), so that no link that they hold
// gives what they may no longer: for an account that the operator's
// grant-role makes no longer an instance admin, and for every instance admin
// when a server starts with the capability off after a server of the
// instance last started with it on (RecordAccess).
//
// The invitations of every workspace are revoked, each workspace in a
// transaction of its own, which row-level security needs, after the
// workspaces are listed from the workspace table, which has none (§5.4). An
// error leaves the workspaces before it done, and running the revocation
// again, which revokes nothing twice, does the rest.

// workspacePage is how many workspaces RevokeAccessInvites lists at a time.
const workspacePage = 100

// RevokeAccessInvites revokes, in every workspace, the open invitations,
// expired or not, that one of the users made, or was last given the link of
// as text, whose role the role that the user has there without instance
// admin access may not give (authz.Ungrantable): every one where they are
// not a member, or are a guest, those as owner where they are an admin, and
// so on. It records each revocation in the audit log without an actor, with
// the user as its metadata's user_id, and returns how many it revoked. The
// users are no longer instance admins, or the capability is off, so that
// none of them makes such an invitation again.
func (s *Service) RevokeAccessInvites(ctx context.Context, users []ids.UUID) (int, error) {
	if len(users) == 0 {
		return 0, nil
	}
	revoked := 0
	var after ids.UUID
	for {
		var page []pgtype.UUID
		err := s.db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
			var err error
			page, err = queries.New(tx).WorkspacesAfter(ctx, queries.WorkspacesAfterParams{After: nullUUID(after),
				MaxRows: workspacePage})
			return err
		})
		if err != nil {
			return revoked, fmt.Errorf("workspace: list the workspaces: %w", err)
		}
		for _, w := range page {
			n, err := s.revokeAccessInvitesIn(ctx, w.Bytes, users)
			revoked += n
			if err != nil {
				return revoked, err
			}
		}
		if len(page) < workspacePage {
			return revoked, nil
		}
		after = page[len(page)-1].Bytes
	}
}

// revokeAccessInvitesIn is RevokeAccessInvites in the workspace id, in a
// transaction of its own.
func (s *Service) revokeAccessInvitesIn(ctx context.Context, id ids.UUID, users []ids.UUID) (int, error) {
	revoked := 0
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: id}, func(tx pgx.Tx) error {
		revoked = 0
		q := queries.New(tx)
		for _, user := range users {
			member, err := q.MembershipRole(ctx, queries.MembershipRoleParams{WorkspaceID: nullUUID(id),
				UserID: nullUUID(user)})
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				member = string(role.None)
			case err != nil:
				return fmt.Errorf("workspace: read the membership: %w", err)
			}
			// The role without instance admin access, by the membership alone,
			// which, for an owner, may give every invitation's.
			ungrantable := authz.Ungrantable(role.Policy{}.Workspace(false, role.Role(member)))
			if len(ungrantable) == 0 {
				continue
			}
			rows, err := q.RevokeMemberInvites(ctx, queries.RevokeMemberInvitesParams{WorkspaceID: nullUUID(id),
				Roles: roleNames(ungrantable), UserID: nullUUID(user)})
			if err != nil {
				return fmt.Errorf("workspace: revoke the invitations: %w", err)
			}
			if err := recordRevoked(ctx, tx, id, ids.UUID{}, audit.Client{}, rows,
				map[string]any{"user_id": user.String()}); err != nil {
				return err
			}
			revoked += len(rows)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return revoked, nil
}

// RecordAccess records the InstanceAdminAccess capability that the server
// starts with, the Service's policy's, for the server's start. If it is off,
// and a server of the instance last started with it on, or none has recorded
// what it started with, as before the release that records it, it first
// revokes every instance admin's invitations that RevokeAccessInvites would,
// and returns how many it revoked. Only then does it record that the
// capability is off, so that a start that fails to revoke them all leaves
// the next start to try again. A server that runs with the capability on
// while another starts with it off, as in a rolling restart that turns it
// off, or one that starts with it on during the other's revocation, whose
// record the other's then replaces, can make such invitations after that
// start revoked the others, and no later start revokes them; the operator's
// grant-role revokes them, for an account that it makes no longer an
// instance admin.
func (s *Service) RecordAccess(ctx context.Context) (int, error) {
	if s.policy.InstanceAdminAccess {
		err := s.db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
			return queries.New(tx).SetInstanceAdminAccess(ctx, pgtype.Bool{Bool: true, Valid: true})
		})
		if err != nil {
			return 0, fmt.Errorf("workspace: record the instance admin access: %w", err)
		}
		return 0, nil
	}
	var last pgtype.Bool
	var admins []ids.UUID
	err := s.db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		var err error
		if last, err = q.InstanceAdminAccess(ctx); err != nil || (last.Valid && !last.Bool) {
			return err
		}
		rows, err := q.InstanceAdmins(ctx)
		for _, r := range rows {
			admins = append(admins, r.Bytes)
		}
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("workspace: read the instance admin access: %w", err)
	}
	if last.Valid && !last.Bool {
		return 0, nil
	}
	revoked, err := s.RevokeAccessInvites(ctx, admins)
	if err != nil {
		return revoked, err
	}
	err = s.db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		return queries.New(tx).SetInstanceAdminAccess(ctx, pgtype.Bool{Valid: true})
	})
	if err != nil {
		return revoked, fmt.Errorf("workspace: record the instance admin access: %w", err)
	}
	return revoked, nil
}
