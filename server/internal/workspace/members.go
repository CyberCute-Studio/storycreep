package workspace

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/role"
)

// Members (PRD FR-TEN-02): every member of a workspace with a role in it,
// which a guest's is not (§5.5), lists its members, with their display names
// and roles, guests included, and its owners and admins see their email
// addresses too, which managing them needs. An owner or admin removes a
// member, themselves included, and only an owner removes an owner (§5.6),
// and never the workspace's last owner whose account is active (AC2).
// Removing a member revokes the workspace's open invitations that the member
// made, those whose link the member was last given as text, as by sending
// one anew, and those to the member's address, so that no link that they
// hold lets them back in. A user joins a workspace by accepting an invitation
// (package invite, Join), or through the operator (Grant).

var (
	// ErrOwnerRequired is returned for an operation that only an owner of
	// the workspace may do, asked for by an admin: removing an owner, or
	// inviting someone as owner (§5.6, FR-TEN-03 AC4).
	ErrOwnerRequired = errors.New("workspace: only an owner of the workspace may do it")
	// ErrAlreadyMember is returned by Join for a user who is a member of the
	// workspace already.
	ErrAlreadyMember = errors.New("workspace: the user is a member of the workspace already")
)

// Member is a member of a workspace, as its member list shows them.
type Member struct {
	MembershipID, UserID ids.UUID
	// Role is the membership's role, which may be guest.
	Role        role.Role
	DisplayName string
	// Email is the member's email address, or "" if the user who lists the
	// members may not see it.
	Email    string
	JoinedAt time.Time
}

// Authorize returns the role that the user userID acts with in the workspace
// id, whose transaction tx is, and locks the user's membership against
// changes until tx ends, so that a change of the role, or the membership's
// removal, waits for the operation that the role authorises, as Update does.
// It returns ErrNotFound if the workspace does not exist, or the user's role
// in it is none.
func (s *Service) Authorize(ctx context.Context, tx pgx.Tx, userID, id ids.UUID) (role.Role, error) {
	return s.authorize(ctx, tx, userID, id, true)
}

// Role returns the role that the user userID acts with in the workspace id,
// whose transaction tx is, as Authorize does, but without locking the
// membership, for an operation that only reads, as Get does.
func (s *Service) Role(ctx context.Context, tx pgx.Tx, userID, id ids.UUID) (role.Role, error) {
	return s.authorize(ctx, tx, userID, id, false)
}

// authorize is Authorize if lock is set, and else Role.
func (s *Service) authorize(ctx context.Context, tx pgx.Tx, userID, id ids.UUID, lock bool) (role.Role, error) {
	q := queries.New(tx)
	if _, err := q.Workspace(ctx, nullUUID(id)); errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	} else if err != nil {
		return "", fmt.Errorf("workspace: read the workspace: %w", err)
	}
	if !lock {
		member, err := q.MembershipRole(ctx, queries.MembershipRoleParams{WorkspaceID: nullUUID(id),
			UserID: nullUUID(userID)})
		return s.resolve(ctx, q, userID, member, err)
	}
	member, err := q.LockMembershipRole(ctx, queries.LockMembershipRoleParams{WorkspaceID: nullUUID(id),
		UserID: nullUUID(userID)})
	return s.resolve(ctx, q, userID, member, err)
}

// Members returns at most limit of the members of the workspace id, in the
// order of their memberships' IDs: the first, if after is zero, or else
// those after the membership after, for the user userID. A member's email
// address is there only if the role that the user acts with in the
// workspace is owner or admin. It returns ErrNotFound if the workspace does
// not exist, or the user's role in it is none.
func (s *Service) Members(ctx context.Context, userID, id, after ids.UUID, limit int) ([]Member, error) {
	var out []Member
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: id, UserID: userID}, func(tx pgx.Tx) error {
		r, err := s.Role(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		rows, err := queries.New(tx).WorkspaceMembers(ctx, queries.WorkspaceMembersParams{WorkspaceID: nullUUID(id),
			After: nullUUID(after), MaxRows: int32(min(limit, 1<<20))})
		if err != nil {
			return fmt.Errorf("workspace: list the members: %w", err)
		}
		for _, row := range rows {
			m := Member{MembershipID: row.ID.Bytes, UserID: row.UserID.Bytes, Role: role.Role(row.Role),
				DisplayName: row.DisplayName, JoinedAt: row.JoinedAt.Time}
			if r.AtLeast(role.Admin) {
				m.Email = row.Email
			}
			out = append(out, m)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RemoveMember removes the membership membershipID from the workspace id,
// and with it the membership's project grants, and revokes the workspace's
// open invitations that the member made, whose link the member was last
// given as text, or that are to the member's address, expired or not, so
// that no link that they hold lets them back in, which the user userID asks
// for, as client, and records all of that in the audit log. It returns
// ErrNotFound if the workspace does not exist, the user's role in it is none,
// or it has no such membership, ErrInsufficientRole unless the user's role is
// owner or admin, ErrOwnerRequired if the membership is an owner's and the
// user's role is admin (§5.6), and ErrLastOwner if the workspace would have
// no other owner whose account is active (FR-TEN-02 AC2), and removes nothing
// then. An owner or admin may remove their own membership, as long as the
// workspace keeps an owner.
func (s *Service) RemoveMember(ctx context.Context, userID, id, membershipID ids.UUID, client audit.Client) error {
	return s.db.InTx(ctx, database.Scope{WorkspaceID: id, UserID: userID}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		// The lock serializes the changes of a workspace's owners, so that
		// two removals cannot each remove one of its last two owners.
		if _, err := q.LockWorkspace(ctx, nullUUID(id)); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return fmt.Errorf("workspace: read the workspace: %w", err)
		}
		member, err := q.LockMembershipRole(ctx, queries.LockMembershipRoleParams{WorkspaceID: nullUUID(id),
			UserID: nullUUID(userID)})
		r, err := s.resolve(ctx, q, userID, member, err)
		if err != nil {
			return err
		}
		if !r.AtLeast(role.Admin) {
			return ErrInsufficientRole
		}
		target, err := q.LockMembershipByID(ctx, queries.LockMembershipByIDParams{WorkspaceID: nullUUID(id),
			ID: nullUUID(membershipID)})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("workspace: read the membership: %w", err)
		}
		if role.Role(target.Role) == role.Owner {
			if r != role.Owner {
				return ErrOwnerRequired
			}
			others, err := q.CountOtherOwners(ctx, queries.CountOtherOwnersParams{WorkspaceID: nullUUID(id),
				ID: nullUUID(membershipID)})
			if err != nil {
				return fmt.Errorf("workspace: count the owners: %w", err)
			}
			if others == 0 {
				return ErrLastOwner
			}
		}
		if err := q.DeleteMembership(ctx, queries.DeleteMembershipParams{WorkspaceID: nullUUID(id),
			ID: nullUUID(membershipID)}); err != nil {
			return fmt.Errorf("workspace: remove the membership: %w", err)
		}
		if err := audit.Record(ctx, tx, audit.Entry{Action: audit.ActionMembershipRemove, WorkspaceID: id,
			ActorUserID: userID, SubjectID: membershipID, Result: audit.ResultSuccess, IP: client.IP,
			UserAgent: client.UserAgent, Metadata: map[string]any{"role": target.Role,
				"user_id": ids.UUID(target.UserID.Bytes).String()}}); err != nil {
			return err
		}
		revoked, err := q.RevokeMemberInvites(ctx, queries.RevokeMemberInvitesParams{WorkspaceID: nullUUID(id),
			UserID: target.UserID})
		if err != nil {
			return fmt.Errorf("workspace: revoke the member's invitations: %w", err)
		}
		for _, inv := range revoked {
			// The kinds of invitation are package invite's: email, to an
			// address, and link, to whoever holds the link.
			kind := "link"
			if inv.Email.Valid {
				kind = "email"
			}
			if err := audit.Record(ctx, tx, audit.Entry{Action: audit.ActionInviteRevoke, WorkspaceID: id,
				ActorUserID: userID, SubjectID: inv.ID.Bytes, Result: audit.ResultSuccess, IP: client.IP,
				UserAgent: client.UserAgent, Metadata: map[string]any{"role": inv.Role, "kind": kind,
					"membership_id": membershipID.String()}}); err != nil {
				return err
			}
		}
		return nil
	})
}

// Join makes the user userID a member of the workspace id, whose transaction
// tx is, with the role r, as their acceptance of the invitation inviteID,
// which the user invitedBy made, or a user since deleted if it is zero, does
// (package invite), and records that in the audit log, with the user as its
// actor, as client. It returns the new membership's ID, or ErrAlreadyMember
// if the user is a member of the workspace already, and adds nothing then.
// The caller checks that the workspace exists, and that the user's account
// is active.
func Join(ctx context.Context, tx pgx.Tx, id, userID ids.UUID, r role.Role, invitedBy, inviteID ids.UUID,
	client audit.Client) (ids.UUID, error) {
	mid := ids.New()
	n, err := queries.New(tx).JoinWorkspace(ctx, queries.JoinWorkspaceParams{ID: nullUUID(mid),
		WorkspaceID: nullUUID(id), UserID: nullUUID(userID), Role: string(r), InvitedBy: nullUUID(invitedBy)})
	switch {
	case err != nil:
		return ids.UUID{}, fmt.Errorf("workspace: add the membership: %w", err)
	case n == 0:
		return ids.UUID{}, ErrAlreadyMember
	}
	err = audit.Record(ctx, tx, audit.Entry{Action: audit.ActionMembershipCreate, WorkspaceID: id,
		ActorUserID: userID, SubjectID: mid, Result: audit.ResultSuccess, IP: client.IP, UserAgent: client.UserAgent,
		Metadata: map[string]any{"role": r, "invite_id": inviteID.String()}})
	if err != nil {
		return ids.UUID{}, err
	}
	return mid, nil
}

// MembershipIn returns the membership of the user userID of the workspace id,
// whose transaction tx is, with the workspace, guests' included, or
// ErrNotFound if the workspace does not exist, or the user is not a member.
func (s *Service) MembershipIn(ctx context.Context, tx pgx.Tx, id, userID ids.UUID) (Membership, error) {
	q := queries.New(tx)
	w, err := q.Workspace(ctx, nullUUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, ErrNotFound
	}
	if err != nil {
		return Membership{}, fmt.Errorf("workspace: read the workspace: %w", err)
	}
	m, err := q.MembershipOf(ctx, queries.MembershipOfParams{WorkspaceID: nullUUID(id), UserID: nullUUID(userID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, ErrNotFound
	}
	if err != nil {
		return Membership{}, fmt.Errorf("workspace: read the membership: %w", err)
	}
	return Membership{ID: m.ID.Bytes, Role: role.Role(m.Role),
		Workspace: s.workspace(w.ID, w.Name, w.Slug, w.Settings, w.CreatedAt, w.UpdatedAt)}, nil
}
