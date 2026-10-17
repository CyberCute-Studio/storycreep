package workspace

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/role"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
)

// Members (PRD FR-TEN-02, FR-TEN-03): every member of a workspace with a
// role in it, which a guest's is not (§5.5), lists its members, with their
// display names and roles, guests included, and its owners and admins see
// their email addresses too, which managing them needs. An owner or admin
// removes a member, themselves included, and changes a member's role, for
// the roles up to admin, and only an owner grants the owner role, or demotes
// or removes an owner (§5.6, as package authz decides), and never so that
// the workspace has no owner whose account is active (FR-TEN-02 AC2).
// Removing a member revokes the workspace's open invitations that the member
// made, those whose link the member was last given as text, as by sending
// one anew, and those to the member's address, so that no link that they
// hold lets them back in. Changing a member's role revokes those that the
// member made or was last given the link of whose role the new role may not
// give (authz.Ungrantable), so that no link that they hold gives what their
// role no longer may. A user joins a workspace by accepting an invitation
// (package invite, Join), or through the operator (Grant).
//
// Every membership counts towards the workspace's members (FR-ENT-01,
// FR-ENT-02), guests' included, and addMembership is the one site that
// enforces the members limit of the workspace's plan: creating a workspace,
// which adds its owner, Grant, and Join all add a membership through it.
// Inviting someone does not check the limit, so that a workspace at its limit
// can still invite, as after a member leaves; accepting does, and so does
// registering with a link invitation's link (CheckRoom).

var (
	// ErrOwnerRequired is returned for an operation that only an owner of
	// the workspace may do, asked for by an admin: granting the owner role,
	// by a role change or an invitation, or demoting or removing an owner
	// (§5.6, FR-TEN-03 AC4), as package authz decides.
	ErrOwnerRequired = authz.ErrOwnerRequired
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
// workspace allows authz.ManageMembers: owner or admin. It returns
// ErrNotFound if the workspace does not exist, or the user's role in it is
// none.
func (s *Service) Members(ctx context.Context, userID, id, after ids.UUID, limit int) ([]Member, error) {
	var out []Member
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: id, UserID: userID}, func(tx pgx.Tx) error {
		r, err := s.Role(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		if err := authz.Check(r, authz.ListMembers); err != nil {
			return err
		}
		addresses := authz.Allows(r, authz.ManageMembers)
		rows, err := queries.New(tx).WorkspaceMembers(ctx, queries.WorkspaceMembersParams{WorkspaceID: nullUUID(id),
			After: nullUUID(after), MaxRows: int32(min(limit, 1<<20))})
		if err != nil {
			return fmt.Errorf("workspace: list the members: %w", err)
		}
		for _, row := range rows {
			m := Member{MembershipID: row.ID.Bytes, UserID: row.UserID.Bytes, Role: role.Role(row.Role),
				DisplayName: row.DisplayName, JoinedAt: row.JoinedAt.Time}
			if addresses {
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

// manageMember locks the workspace id, whose transaction q's is, and the
// membership of the user userID, whose role must allow authz.ManageMembers,
// and returns that role, for an operation on a member that the user asks
// for. The workspace's lock serializes the changes of its owners, so that two
// cannot each remove or demote one of its last two owners. It returns
// ErrNotFound if the workspace does not exist or the user's role in it is
// none, and ErrInsufficientRole if the role does not allow it.
func (s *Service) manageMember(ctx context.Context, q *queries.Queries, userID, id ids.UUID) (role.Role, error) {
	if _, err := q.LockWorkspace(ctx, nullUUID(id)); errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	} else if err != nil {
		return "", fmt.Errorf("workspace: read the workspace: %w", err)
	}
	member, err := q.LockMembershipRole(ctx, queries.LockMembershipRoleParams{WorkspaceID: nullUUID(id),
		UserID: nullUUID(userID)})
	r, err := s.resolve(ctx, q, userID, member, err)
	if err != nil {
		return "", err
	}
	if err := authz.Check(r, authz.ManageMembers); err != nil {
		return "", err
	}
	return r, nil
}

// lockTarget returns the member and the role of the membership membershipID
// of the workspace id, whose transaction q's is, and locks it, or
// ErrNotFound if the workspace has no such membership.
func lockTarget(ctx context.Context, q *queries.Queries, id, membershipID ids.UUID) (
	queries.LockMembershipByIDRow, error) {
	target, err := q.LockMembershipByID(ctx, queries.LockMembershipByIDParams{WorkspaceID: nullUUID(id),
		ID: nullUUID(membershipID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return target, ErrNotFound
	}
	if err != nil {
		return target, fmt.Errorf("workspace: read the membership: %w", err)
	}
	return target, nil
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
		r, err := s.manageMember(ctx, q, userID, id)
		if err != nil {
			return err
		}
		target, err := lockTarget(ctx, q, id, membershipID)
		if err != nil {
			return err
		}
		if role.Role(target.Role) == role.Owner {
			if err := authz.Check(r, authz.ManageOwners); err != nil {
				return err
			}
			if err := keepOwner(ctx, q, id, membershipID); err != nil {
				return err
			}
		}
		// The invitations are revoked while the membership still exists, and
		// only then is it deleted. An acceptance by the member locks its
		// invitation first and then adds a membership, which conflicts with
		// theirs: while theirs is only locked, the acceptance is refused at
		// once (ErrAlreadyMember) and lets the invitation go, but once it is
		// deleted, the acceptance would wait for this transaction, which
		// would wait for the invitation, and the two would deadlock.
		revoked, err := q.RevokeMemberInvites(ctx, queries.RevokeMemberInvitesParams{WorkspaceID: nullUUID(id),
			Roles: roleNames(role.Memberships()), UserID: target.UserID, Addressed: true})
		if err != nil {
			return fmt.Errorf("workspace: revoke the member's invitations: %w", err)
		}
		if err := q.DeleteMembership(ctx, queries.DeleteMembershipParams{WorkspaceID: nullUUID(id),
			ID: nullUUID(membershipID)}); err != nil {
			return fmt.Errorf("workspace: remove the membership: %w", err)
		}
		if err := usage.Record(ctx, tx, id, usage.Members, -1); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Entry{Action: audit.ActionMembershipRemove, WorkspaceID: id,
			ActorUserID: userID, SubjectID: membershipID, Result: audit.ResultSuccess, IP: client.IP,
			UserAgent: client.UserAgent, Metadata: map[string]any{"role": target.Role,
				"user_id": ids.UUID(target.UserID.Bytes).String()}}); err != nil {
			return err
		}
		return recordRevoked(ctx, tx, id, userID, membershipID, client, revoked)
	})
}

// ChangeRole changes the role of the membership membershipID of the
// workspace id to r, which the user userID asks for, as client, and returns
// the member, with their email address. It revokes the workspace's open
// invitations, expired or not, that the member made or was last given the
// link of as text, as by sending one anew, whose role r may not give
// (authz.Ungrantable): every one, if r does not manage members, and those as
// owner, if r is admin, so that no link that they hold gives what their role
// no longer may. It records all of that in the audit log. A membership that
// has r already stays as it is. It returns ErrNotFound if the workspace does
// not exist, the user's role in it is none, or it has no such membership,
// ErrInsufficientRole unless the user's role is owner or admin,
// input.FieldErrors if r is not a role of a membership (role.Memberships),
// ErrOwnerRequired if r is owner or the membership is an owner's, and the
// user's role is admin (§5.6, FR-TEN-03 AC4), and ErrLastOwner if the
// workspace would have no other owner whose account is active (FR-TEN-02
// AC2), and changes nothing then. An owner or admin may change their own
// role, as long as the workspace keeps an owner.
func (s *Service) ChangeRole(ctx context.Context, userID, id, membershipID ids.UUID, r role.Role,
	client audit.Client) (Member, error) {
	var m Member
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: id, UserID: userID}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		actor, err := s.manageMember(ctx, q, userID, id)
		if err != nil {
			return err
		}
		if !slices.Contains(role.Memberships(), r) {
			return input.FieldErrors{{Field: "role",
				Message: "is not one of owner, admin, editor, commenter, viewer, and guest"}}
		}
		target, err := lockTarget(ctx, q, id, membershipID)
		if err != nil {
			return err
		}
		previous := role.Role(target.Role)
		if err := authz.CheckGrant(actor, r); err != nil {
			return err
		}
		if previous == role.Owner {
			if err := authz.Check(actor, authz.ManageOwners); err != nil {
				return err
			}
		}
		if err := setRole(ctx, tx, roleChange{workspaceID: id, membershipID: membershipID,
			userID: target.UserID.Bytes, previous: previous, role: r, actor: userID, client: client}); err != nil {
			return err
		}
		row, err := q.WorkspaceMember(ctx, queries.WorkspaceMemberParams{WorkspaceID: nullUUID(id),
			ID: nullUUID(membershipID)})
		if err != nil {
			return fmt.Errorf("workspace: read the member: %w", err)
		}
		m = Member{MembershipID: row.ID.Bytes, UserID: row.UserID.Bytes, Role: role.Role(row.Role),
			DisplayName: row.DisplayName, Email: row.Email, JoinedAt: row.JoinedAt.Time}
		return nil
	})
	if err != nil {
		return Member{}, err
	}
	return m, nil
}

// roleChange is a change of a membership's role.
type roleChange struct {
	workspaceID, membershipID ids.UUID
	// userID is the member.
	userID ids.UUID
	// previous is the membership's role, and role the one that it gets.
	previous, role role.Role
	// actor is the user who asks for the change, as client, or zero for the
	// operator (Grant).
	actor  ids.UUID
	client audit.Client
}

// setRole makes the change c in tx, a transaction of c's workspace, which
// holds the lock of the workspace, which serializes the changes of its
// owners, and of the membership. It changes the membership's role, records
// that in the audit log, and revokes the member's invitations that the new
// role may not give, as ChangeRole says, and does nothing if the membership
// has the role already. It returns ErrLastOwner if the membership is an
// owner's and the workspace would have no other owner whose account is
// active, and changes nothing then.
func setRole(ctx context.Context, tx pgx.Tx, c roleChange) error {
	if c.previous == c.role {
		return nil
	}
	q := queries.New(tx)
	if c.previous == role.Owner {
		if err := keepOwner(ctx, q, c.workspaceID, c.membershipID); err != nil {
			return err
		}
	}
	// The invitations are revoked before the membership changes, as a
	// removal revokes them before it deletes it. An acceptance by the member
	// locks its invitation first and then adds a membership, which conflicts
	// with theirs: while theirs is only locked, the acceptance is refused at
	// once (ErrAlreadyMember) and lets the invitation go, but once it is
	// changed, the acceptance would wait for this transaction, which would
	// wait for the invitation, and the two would deadlock.
	var revoked []queries.RevokeMemberInvitesRow
	if ungrantable := authz.Ungrantable(c.role); len(ungrantable) > 0 {
		var err error
		revoked, err = q.RevokeMemberInvites(ctx, queries.RevokeMemberInvitesParams{
			WorkspaceID: nullUUID(c.workspaceID), Roles: roleNames(ungrantable), UserID: nullUUID(c.userID)})
		if err != nil {
			return fmt.Errorf("workspace: revoke the member's invitations: %w", err)
		}
	}
	if err := q.UpdateMembershipRole(ctx, queries.UpdateMembershipRoleParams{Role: string(c.role),
		WorkspaceID: nullUUID(c.workspaceID), ID: nullUUID(c.membershipID)}); err != nil {
		return fmt.Errorf("workspace: change the membership's role: %w", err)
	}
	if err := audit.Record(ctx, tx, audit.Entry{Action: audit.ActionMembershipRoleChange,
		WorkspaceID: c.workspaceID, ActorUserID: c.actor, SubjectID: c.membershipID, Result: audit.ResultSuccess,
		IP: c.client.IP, UserAgent: c.client.UserAgent,
		Metadata: map[string]any{"role": c.role, "previous_role": c.previous}}); err != nil {
		return err
	}
	return recordRevoked(ctx, tx, c.workspaceID, c.actor, c.membershipID, c.client, revoked)
}

// keepOwner returns ErrLastOwner if the workspace id, whose transaction q's
// is, has no owner whose account is active besides the membership
// membershipID, which is an owner's, so that it keeps one (FR-TEN-02 AC2).
func keepOwner(ctx context.Context, q *queries.Queries, id, membershipID ids.UUID) error {
	others, err := q.CountOtherOwners(ctx, queries.CountOtherOwnersParams{WorkspaceID: nullUUID(id),
		ID: nullUUID(membershipID)})
	if err != nil {
		return fmt.Errorf("workspace: count the owners: %w", err)
	}
	if others == 0 {
		return ErrLastOwner
	}
	return nil
}

// recordRevoked records in the audit log the revocation of each invitation
// of revoked, of the workspace id, whose transaction tx is, which the
// removal or the role change of the membership membershipID revoked, as the
// user actor asked, or the operator if actor is zero, as client.
func recordRevoked(ctx context.Context, tx pgx.Tx, id, actor, membershipID ids.UUID, client audit.Client,
	revoked []queries.RevokeMemberInvitesRow) error {
	for _, inv := range revoked {
		// The kinds of invitation are package invite's: email, to an
		// address, and link, to whoever holds the link.
		kind := "link"
		if inv.Email.Valid {
			kind = "email"
		}
		if err := audit.Record(ctx, tx, audit.Entry{Action: audit.ActionInviteRevoke, WorkspaceID: id,
			ActorUserID: actor, SubjectID: inv.ID.Bytes, Result: audit.ResultSuccess, IP: client.IP,
			UserAgent: client.UserAgent, Metadata: map[string]any{"role": inv.Role, "kind": kind,
				"membership_id": membershipID.String()}}); err != nil {
			return err
		}
	}
	return nil
}

// roleNames returns the roles as the database holds them.
func roleNames(roles []role.Role) []string {
	names := make([]string, len(roles))
	for i, r := range roles {
		names[i] = string(r)
	}
	return names
}

// Join makes the user userID a member of the workspace id, whose transaction
// tx is, with the role r, as their acceptance of the invitation inviteID,
// which the user invitedBy made, or a user since deleted if it is zero, does
// (package invite), and records that in the audit log, with the user as its
// actor, as client. It returns the new membership's ID, or ErrAlreadyMember
// if the user is a member of the workspace already, and then an
// *entitlements.LimitError if the workspace has as many members as its plan
// allows (FR-ENT-01), and adds nothing then. The caller checks that the
// workspace exists, and that the user's account is active.
func (s *Service) Join(ctx context.Context, tx pgx.Tx, id, userID ids.UUID, r role.Role, invitedBy,
	inviteID ids.UUID, client audit.Client) (ids.UUID, error) {
	// A member is told so, rather than that the workspace is full, whatever
	// its usage.
	_, err := queries.New(tx).MembershipOf(ctx, queries.MembershipOfParams{WorkspaceID: nullUUID(id),
		UserID: nullUUID(userID)})
	switch {
	case err == nil:
		return ids.UUID{}, ErrAlreadyMember
	case !errors.Is(err, pgx.ErrNoRows):
		return ids.UUID{}, fmt.Errorf("workspace: read the membership: %w", err)
	}
	mid, err := s.addMembership(ctx, tx, id, userID, r, invitedBy)
	if err != nil {
		return ids.UUID{}, err
	}
	err = audit.Record(ctx, tx, audit.Entry{Action: audit.ActionMembershipCreate, WorkspaceID: id,
		ActorUserID: userID, SubjectID: mid, Result: audit.ResultSuccess, IP: client.IP, UserAgent: client.UserAgent,
		Metadata: map[string]any{"role": r, "invite_id": inviteID.String()}})
	if err != nil {
		return ids.UUID{}, err
	}
	return mid, nil
}

// CheckRoom returns an *entitlements.LimitError if the workspace id, whose
// transaction tx is, has as many members as its plan allows (FR-ENT-01), and
// nil if it has room for one more. It is the members limit's one check, which
// addMembership makes before it adds a membership, and package invite before
// it registers an account with the link of a link invitation, which any
// address may register with, so that a full workspace's link, which stays
// open, adds no account. For a limit that is a number, it holds the lock of
// the workspace's members until tx ends (package usage), so a transaction
// that only asks, as package invite's, ends soon after.
func (s *Service) CheckRoom(ctx context.Context, tx pgx.Tx, id ids.UUID) error {
	return s.ent.Check(ctx, tx, id, entitlements.Members, 1)
}

// addMembership makes the user userID a member of the workspace id, whose
// transaction tx is, with the role r, invited by the user invitedBy, or by
// nobody if it is zero, and returns the new membership's ID. It is the one
// site that enforces the members limit (FR-ENT-01 AC1): it checks the limit
// first (CheckRoom), which, counted exactly, holds the lock of the
// workspace's members until tx ends (package usage), so that two memberships
// added at once cannot both pass it, and records the new member in the
// workspace's usage, so that the next check counts it (FR-ENT-02). It
// returns an *entitlements.LimitError if the workspace has as many members as
// its plan allows, and ErrAlreadyMember if the user is a member already, as
// one that a transaction added meanwhile, and adds nothing then.
func (s *Service) addMembership(ctx context.Context, tx pgx.Tx, id, userID ids.UUID, r role.Role,
	invitedBy ids.UUID) (ids.UUID, error) {
	if err := s.CheckRoom(ctx, tx, id); err != nil {
		return ids.UUID{}, err
	}
	mid := ids.New()
	n, err := queries.New(tx).JoinWorkspace(ctx, queries.JoinWorkspaceParams{ID: nullUUID(mid),
		WorkspaceID: nullUUID(id), UserID: nullUUID(userID), Role: string(r), InvitedBy: nullUUID(invitedBy)})
	switch {
	case err != nil:
		return ids.UUID{}, fmt.Errorf("workspace: add the membership: %w", err)
	case n == 0:
		return ids.UUID{}, ErrAlreadyMember
	}
	if err := usage.Record(ctx, tx, id, usage.Members, 1); err != nil {
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
