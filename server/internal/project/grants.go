package project

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/role"
)

// Grants (PRD FR-TEN-04, §5.5): a membership can carry a grant on a project
// of its workspace, whose role, admin, editor, commenter, or viewer
// (role.Grants), replaces the membership's role on that project, raising or
// lowering it, and is the only way that a guest acts on a project. A grant
// authorises only the operations on its project that the project authorises
// (P in §5.6): every operation of the workspace (W) is authorised by the
// role in the workspace, which no grant changes (role.Policy.Workspace), so
// that a guest's or a viewer's admin grant on a project lets them neither
// delete it nor manage its grants (AC4). The owner role is the workspace's
// alone, so a grant of it is refused (ErrOwnerNotGrantable, AC5).
//
// An owner or admin of the workspace lists, sets, and removes the grants
// ("Manage project grants", W), and only an owner sets or removes an owner's
// grant, which replaces their role on the project, as only an owner demotes
// an owner (ErrOwnerRequired, FR-TEN-03 AC4). The grants of an archived
// project change too, because they are who may act on it, not its content.
// The grants of a project in the trash are kept, and apply again once it is
// restored, but nothing lists or changes them meanwhile, as nothing finds the
// project. Removing a membership removes its grants, and purging a project
// removes the grants on it, by the cascade of their foreign keys.
//
// A change of a grant holds the lock of the workspace, which every change of
// a membership's role and every removal of one holds too (package
// workspace), so that the grants of one workspace change one at a time, and
// the member keeps the role that the change was checked against until it
// commits.

var (
	// ErrOwnerRequired is returned for setting or removing an owner's grant,
	// asked for by an admin: only an owner of the workspace may (§5.6,
	// FR-TEN-03 AC4), as package authz decides.
	ErrOwnerRequired = authz.ErrOwnerRequired
	// ErrOwnerNotGrantable is returned for a grant asked for with the owner
	// role, which is a role of the workspace alone (FR-TEN-04 AC5).
	ErrOwnerNotGrantable = errors.New("project: a project grant cannot have the owner role")
)

// Grant is a membership's grant on a project (FR-TEN-04).
type Grant struct {
	ProjectID, MembershipID ids.UUID
	// UserID is the member's account.
	UserID ids.UUID
	// Role is the member's role on the project, one of role.Grants.
	Role                 role.Role
	CreatedAt, UpdatedAt time.Time
}

// grantOf returns the grant g of the member userID.
func grantOf(g queries.ProjectGrant, userID pgtype.UUID) Grant {
	return Grant{ProjectID: g.ProjectID.Bytes, MembershipID: g.MembershipID.Bytes, UserID: userID.Bytes,
		Role: role.Role(g.Role), CreatedAt: g.CreatedAt.Time, UpdatedAt: g.UpdatedAt.Time}
}

// manageGrants authorises the user userID, in q's transaction, a transaction
// of the workspace workspaceID, to manage the grants on its project
// projectID, and returns the role that they act with in the workspace. It
// returns ErrNotFound if the workspace does not exist, the project does not
// exist or is in the trash, or the user's effective role on the project is
// none, as the project's other routes do, so that the answer does not tell
// that the project exists, and the error of authz.Check unless the role in
// the workspace allows authz.ManageProjectGrants. If lock is set, it locks
// the workspace, and the user's membership, until the transaction ends, for
// a change of a grant.
func (s *Service) manageGrants(ctx context.Context, q *queries.Queries, userID, workspaceID, projectID ids.UUID,
	lock bool) (role.Role, error) {
	if lock {
		if _, err := q.LockWorkspace(ctx, nullUUID(workspaceID)); errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		} else if err != nil {
			return "", fmt.Errorf("project: lock the workspace: %w", err)
		}
	} else if _, err := activeWorkspace(ctx, q, workspaceID); err != nil {
		return "", err
	}
	if _, err := q.Project(ctx, queries.ProjectParams{WorkspaceID: nullUUID(workspaceID),
		ID: nullUUID(projectID)}); errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	} else if err != nil {
		return "", fmt.Errorf("project: read the project: %w", err)
	}
	a, err := s.access(ctx, q, userID, workspaceID, lock)
	if err != nil {
		return "", err
	}
	effective, err := s.effective(ctx, q, a, workspaceID, projectID)
	switch {
	case err != nil:
		return "", err
	case effective == role.None:
		return "", ErrNotFound
	}
	r := s.policy.Workspace(a.admin, a.member)
	if err := authz.Check(r, authz.ManageProjectGrants); err != nil {
		return "", err
	}
	return r, nil
}

// Grants returns at most limit of the grants on the project projectID, each
// with its member, in the order of their memberships' IDs: the first, if
// after is zero, or else those after the membership after, for the user
// userID. It returns ErrNotFound if the project does not exist, is in the
// trash, or the user's effective role on it is none, and ErrInsufficientRole
// unless their role in its workspace is owner or admin (§5.6).
func (s *Service) Grants(ctx context.Context, userID, projectID, after ids.UUID, limit int) ([]Grant, error) {
	workspaceID, err := s.workspaceOf(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	var out []Grant
	err = s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID, UserID: userID}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		if _, err := s.manageGrants(ctx, q, userID, workspaceID, projectID, false); err != nil {
			return err
		}
		rows, err := q.ProjectGrants(ctx, queries.ProjectGrantsParams{WorkspaceID: nullUUID(workspaceID),
			ProjectID: nullUUID(projectID), After: nullUUID(after), MaxRows: int32(min(limit, 1<<20))})
		if err != nil {
			return fmt.Errorf("project: list the project's grants: %w", err)
		}
		for _, row := range rows {
			out = append(out, grantOf(row.ProjectGrant, row.UserID))
		}
		return nil
	})
	return out, err
}

// MemberGrants returns at most limit of the grants of the membership
// membershipID of the workspace workspaceID on its projects that are not in
// the trash, in the order of the projects' IDs: the first, if after is zero,
// or else those after the project after, for the user userID. It returns
// ErrNotFound if the workspace does not exist or the user's role in it is
// none, ErrInsufficientRole unless the role is owner or admin (§5.6), and
// then ErrNotFound if the workspace has no such membership.
func (s *Service) MemberGrants(ctx context.Context, userID, workspaceID, membershipID, after ids.UUID, limit int) (
	[]Grant, error) {
	var out []Grant
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID, UserID: userID}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		if _, err := activeWorkspace(ctx, q, workspaceID); err != nil {
			return err
		}
		a, err := s.access(ctx, q, userID, workspaceID, false)
		if err != nil {
			return err
		}
		r := s.policy.Workspace(a.admin, a.member)
		if r == role.None {
			return ErrNotFound
		}
		if err := authz.Check(r, authz.ManageProjectGrants); err != nil {
			return err
		}
		if _, err := q.WorkspaceMember(ctx, queries.WorkspaceMemberParams{WorkspaceID: nullUUID(workspaceID),
			ID: nullUUID(membershipID)}); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return fmt.Errorf("project: read the membership: %w", err)
		}
		rows, err := q.MembershipGrants(ctx, queries.MembershipGrantsParams{WorkspaceID: nullUUID(workspaceID),
			MembershipID: nullUUID(membershipID), After: nullUUID(after), MaxRows: int32(min(limit, 1<<20))})
		if err != nil {
			return fmt.Errorf("project: list the member's grants: %w", err)
		}
		for _, row := range rows {
			out = append(out, grantOf(row.ProjectGrant, row.UserID))
		}
		return nil
	})
	return out, err
}

// grantTarget returns the member and the role of the membership membershipID
// of the workspace workspaceID, whose transaction q's is, and locks it until
// the transaction ends, for a change of its grant asked for by a user whose
// role in the workspace is r. It returns ErrNotFound if the workspace has no
// such membership, and ErrOwnerRequired if it is an owner's and r is not
// owner (§5.6).
func grantTarget(ctx context.Context, q *queries.Queries, workspaceID, membershipID ids.UUID, r role.Role) (
	queries.LockMembershipByIDRow, error) {
	target, err := q.LockMembershipByID(ctx, queries.LockMembershipByIDParams{WorkspaceID: nullUUID(workspaceID),
		ID: nullUUID(membershipID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return target, ErrNotFound
	}
	if err != nil {
		return target, fmt.Errorf("project: read the membership: %w", err)
	}
	if role.Role(target.Role) == role.Owner {
		if err := authz.Check(r, authz.ManageOwners); err != nil {
			return target, err
		}
	}
	return target, nil
}

// SetGrant gives the membership membershipID the role r on the project
// projectID, which the user userID asks for, as client: it adds the
// membership's grant there, or changes the grant's role, records that in the
// audit log, and returns the grant. A grant that has r already stays as it
// is. It returns the errors of Grants, then ErrOwnerNotGrantable if r is
// owner (FR-TEN-04 AC5), input.FieldErrors if r is no other role that a
// grant can have (role.Grants), ErrNotFound if the workspace has no such
// membership, and ErrOwnerRequired if the membership is an owner's and the
// user's role in the workspace is admin, and changes nothing then.
func (s *Service) SetGrant(ctx context.Context, userID, projectID, membershipID ids.UUID, r role.Role,
	client audit.Client) (Grant, error) {
	workspaceID, err := s.workspaceOf(ctx, userID, projectID)
	if err != nil {
		return Grant{}, err
	}
	var out Grant
	err = s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID, UserID: userID}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		actor, err := s.manageGrants(ctx, q, userID, workspaceID, projectID, true)
		if err != nil {
			return err
		}
		switch {
		case r == role.Owner:
			return ErrOwnerNotGrantable
		case !slices.Contains(role.Grants(), r):
			return input.FieldErrors{{Field: "role", Message: "is not one of admin, editor, commenter, and viewer"}}
		}
		target, err := grantTarget(ctx, q, workspaceID, membershipID, actor)
		if err != nil {
			return err
		}
		previous, err := q.ProjectGrant(ctx, queries.ProjectGrantParams{WorkspaceID: nullUUID(workspaceID),
			MembershipID: nullUUID(membershipID), ProjectID: nullUUID(projectID)})
		granted := err == nil
		switch {
		case granted && role.Role(previous.ProjectGrant.Role) == r:
			out = grantOf(previous.ProjectGrant, previous.UserID)
			return nil
		case !granted && !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("project: read the grant: %w", err)
		}
		g, err := q.SetProjectGrant(ctx, queries.SetProjectGrantParams{ID: nullUUID(ids.New()),
			WorkspaceID: nullUUID(workspaceID), MembershipID: nullUUID(membershipID), ProjectID: nullUUID(projectID),
			Role: string(r)})
		if err != nil {
			return fmt.Errorf("project: set the grant: %w", err)
		}
		e := audit.Entry{Action: audit.ActionProjectGrantCreate, WorkspaceID: workspaceID, ActorUserID: userID,
			SubjectID: membershipID, Result: audit.ResultSuccess, IP: client.IP, UserAgent: client.UserAgent,
			Metadata: map[string]any{"project_id": projectID.String(), "role": r}}
		if granted {
			e.Action, e.Metadata["previous_role"] = audit.ActionProjectGrantChange, previous.ProjectGrant.Role
		}
		if err := audit.Record(ctx, tx, e); err != nil {
			return err
		}
		out = grantOf(g, target.UserID)
		return nil
	})
	if err != nil {
		return Grant{}, err
	}
	return out, nil
}

// RemoveGrant removes the grant of the membership membershipID on the
// project projectID, which the user userID asks for, as client, after which
// the member acts on the project with their role in the workspace, and
// records that in the audit log. It returns the errors of Grants, then
// ErrNotFound if the workspace has no such membership, ErrOwnerRequired if
// the membership is an owner's and the user's role in the workspace is
// admin, and ErrNotFound if the membership has no grant on the project, and
// removes nothing then.
func (s *Service) RemoveGrant(ctx context.Context, userID, projectID, membershipID ids.UUID,
	client audit.Client) error {
	workspaceID, err := s.workspaceOf(ctx, userID, projectID)
	if err != nil {
		return err
	}
	return s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID, UserID: userID}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		actor, err := s.manageGrants(ctx, q, userID, workspaceID, projectID, true)
		if err != nil {
			return err
		}
		if _, err := grantTarget(ctx, q, workspaceID, membershipID, actor); err != nil {
			return err
		}
		previous, err := q.DeleteProjectGrant(ctx, queries.DeleteProjectGrantParams{
			WorkspaceID: nullUUID(workspaceID), MembershipID: nullUUID(membershipID), ProjectID: nullUUID(projectID)})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("project: remove the grant: %w", err)
		}
		return audit.Record(ctx, tx, audit.Entry{Action: audit.ActionProjectGrantRemove, WorkspaceID: workspaceID,
			ActorUserID: userID, SubjectID: membershipID, Result: audit.ResultSuccess, IP: client.IP,
			UserAgent: client.UserAgent, Metadata: map[string]any{"project_id": projectID.String(), "role": previous}})
	})
}
