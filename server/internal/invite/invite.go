// Package invite invites people to a workspace, and lets them accept (PRD
// FR-TEN-02, FR-AUTH-03).
//
// An owner or admin of a workspace invites an email address, or whoever holds
// a link, with a role, and only an owner invites someone as owner (§5.6,
// FR-TEN-03 AC4). An invitation lasts 14 days (Lifetime). It is accepted
// once, and until then its admins see it, send it anew, which gives it a new
// link, in place of the old one, and 14 days more, and revoke it. Accepting
// an invitation that has expired, or been revoked, is refused (AC1), and so
// is accepting an invitation to an address with an account of another
// address, while a link invitation may be accepted by any account (AC3). An
// address has one open invitation of a workspace at most: one that has been
// neither accepted nor revoked. Removing a member revokes the workspace's
// open invitations that the member made, those whose link the member was
// last given as text, as by sending one anew, and those to the member's
// address (workspace.Service.RemoveMember), so that no link that they hold
// lets them back in.
//
// The link holds the invitation's secret, made as a session's is, and the
// database holds only its SHA-256 hash, so that whoever reads the database
// cannot use it. An instance that sends email (package email) emails the
// link of an invitation to an address, by a job, which makes the secret when
// it sends it, so that neither the request nor the job's arguments hold the
// secret, as an emailed link of package account does; the link lasts its 14
// days from then. A workspace emails an address one invitation in
// ResendInterval at most: the job of an invitation made or sent anew sooner
// after the last email to its address waits until ResendInterval has passed,
// and only the job of the latest request to email an invitation sends
// anything, so that sending an invitation anew again and again cannot flood
// an inbox, even when the jobs of the earlier requests are retried, as once
// an SMTP server that was down takes email again. The instance also emails
// a mailbox 10 invitations an hour at most, from all workspaces together,
// counting the addresses that differ only in a +tag, or, at Gmail, in dots,
// as one (ratelimit.InviteEmail, ratelimit.Mailbox), so that an account
// that owns many workspaces, or invites many addresses of one mailbox,
// cannot flood an inbox either. The email of an invitation over that limit
// is not sent: the invitation stays without a link, as one not emailed, and
// its admins send it anew later. No answer says why, so that inviting an
// address does not tell a workspace that others invited it, although an
// account that fills the limit with its own workspaces' invitations can
// tell from how many of them are emailed that others used some of it, as
// any limit of a mailbox alone lets whoever fills it tell.
// The email names no one and nothing but the instance and the role, never
// the workspace or the inviting user, whose names their users chose, so that
// an account cannot put text of its own in email that the instance sends to
// any address it likes. An instance that sends no email gives the link of
// every invitation, as text, to the admin who makes it or sends it anew
// (FR-AUTH-03 AC1), and so does every instance for a link invitation, which
// has no address to email: an invitation grants only access to the
// workspace, which its admins control anyway.
//
// A signed-in user accepts an invitation with its link (Accept), and joins
// the workspace with its role. Someone without an account registers one with
// the link instead (Register), which accepts the invitation in the
// transaction that adds the account, under the invite and open signup
// policies; under closed, nobody registers, and only an account that exists
// already accepts an invitation. Registering answers the same whether or not
// the address has an account already, as registering without an invitation
// does (FR-AUTH-01 AC1): it checks the invitation and hashes the password
// either way, but only a new account joins the workspace, and an address
// with an account leaves the invitation open, for the account to accept
// once it signs in.
package invite

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/account"
	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/email"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/ratelimit"
	"github.com/CyberCute-Studio/storycreep/server/internal/role"
	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
	"github.com/CyberCute-Studio/storycreep/server/internal/workspace"
)

// Lifetime is how long an invitation lasts from when it is made, sent anew,
// or emailed (FR-TEN-02).
const Lifetime = 14 * 24 * time.Hour

// ResendInterval is the least time between two emails of a workspace's
// invitations to one address, as between two emails of one kind of link to
// an account (account.ResendInterval).
const ResendInterval = account.ResendInterval

// Kind is whom an invitation invites.
type Kind string

// The kinds of invitation.
const (
	// KindEmail invites an email address, which the account that accepts it
	// must have (FR-TEN-02 AC3).
	KindEmail Kind = "email"
	// KindLink invites whoever holds its link, with any account.
	KindLink Kind = "link"
)

// The errors of invitations. An operation on a workspace returns the errors
// of package workspace too: workspace.ErrNotFound, ErrInsufficientRole,
// ErrOwnerRequired, and ErrAlreadyMember.
var (
	// ErrNotFound is returned for an invitation that the workspace does not
	// have.
	ErrNotFound = errors.New("invite: the workspace has no such invitation")
	// ErrExists is returned by Create for an address that has an open
	// invitation of the workspace already, whether or not it has expired.
	ErrExists = errors.New("invite: the address has an open invitation of the workspace already")
	// ErrTokenInvalid is returned for a link whose token belongs to no
	// invitation, or no longer does, as one that sending the invitation anew
	// replaced, or whose workspace no longer exists.
	ErrTokenInvalid = errors.New("invite: the link's token is not valid")
	// ErrExpired is returned for an invitation that has expired (FR-TEN-02
	// AC1).
	ErrExpired = errors.New("invite: the invitation has expired")
	// ErrRevoked is returned for an invitation that has been revoked
	// (FR-TEN-02 AC1).
	ErrRevoked = errors.New("invite: the invitation has been revoked")
	// ErrAccepted is returned for an invitation that has been accepted.
	ErrAccepted = errors.New("invite: the invitation has been accepted")
	// ErrEmailMismatch is returned for an invitation to another address than
	// the one of the account that would accept it (FR-TEN-02 AC3).
	ErrEmailMismatch = errors.New("invite: the invitation is to another email address")
)

// DB runs a transaction of the application login, as *database.DB does.
type DB interface {
	InTx(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error
}

// Options configure a Service.
type Options struct {
	// Workspaces resolves the roles that users act with in workspaces, and
	// Accounts registers the accounts that invitations create. Both must be
	// set.
	Workspaces *workspace.Service
	Accounts   *account.Service
	// Email sends the instance's email, or is nil for an instance that sends
	// none, as in tests.
	Email *email.Service
	// Queue enqueues the jobs that email invitations, in the transaction of
	// the change that needs them, as *jobs.Queue does, and Limits counts the
	// invitations that the jobs email each mailbox (ratelimit.InviteEmail).
	// Both must be set where Email is.
	Queue  account.Enqueuer
	Limits *ratelimit.Limiter
	// PublicURL is the instance's URL, which links open.
	PublicURL config.PublicURL
}

// Service keeps the invitations.
type Service struct {
	db         DB
	workspaces *workspace.Service
	accounts   *account.Service
	email      *email.Service
	queue      account.Enqueuer
	limits     *ratelimit.Limiter
	publicURL  config.PublicURL
}

// New returns a Service that keeps the invitations in db, with opts.
func New(db DB, opts Options) *Service {
	return &Service{db: db, workspaces: opts.Workspaces, accounts: opts.Accounts, email: opts.Email,
		queue: opts.Queue, limits: opts.Limits, publicURL: opts.PublicURL}
}

// Invite is an invitation, as its workspace's owners and admins see it.
type Invite struct {
	ID ids.UUID
	// Email is the invited address, as accounts hold it, or "" for a link
	// invitation.
	Email string
	Role  role.Role
	// ExpiresAt is when it expires, and Expired says that it has.
	ExpiresAt time.Time
	Expired   bool
	// SentAt is when the instance emailed its link, or zero if it did not
	// email the link that it has now.
	SentAt time.Time
	// CreatedBy is the user who made it, or zero if the account was deleted.
	CreatedBy ids.UUID
	CreatedAt time.Time
	// URL is its link, which Create and Resend give as text when the
	// instance does not email it, and is not set otherwise. It opens the web
	// app's page that accepts the invitation, with the secret in its
	// fragment, which a browser does not send to the server.
	URL secret.Value
}

// Kind returns whom the invitation invites.
func (i Invite) Kind() Kind {
	return kindOf(i.Email)
}

// kindOf returns the kind of an invitation to the address email, or of a
// link invitation if it is "".
func kindOf(email string) Kind {
	if email == "" {
		return KindLink
	}
	return KindEmail
}

// Request is what an invitation is made with.
type Request struct {
	// Email is the address to invite, or "" for a link invitation.
	Email string
	// Role is the role that the invitation gives.
	Role role.Role
}

// roles are the roles that an invitation may give: those of a membership.
var roles = []role.Role{role.Owner, role.Admin, role.Editor, role.Commenter, role.Viewer, role.Guest}

// manage returns the role that the user userID acts with in the workspace id,
// whose transaction tx is, which must be owner or admin to manage its
// invitations, or the error of package workspace that refuses them:
// ErrNotFound if the workspace does not exist, or the user's role in it is
// none, and ErrInsufficientRole otherwise. It locks the user's membership
// until tx ends (workspace.Service.Authorize) unless the operation only
// reads.
func (s *Service) manage(ctx context.Context, tx pgx.Tx, userID, id ids.UUID, reads bool) (role.Role, error) {
	authorize := s.workspaces.Authorize
	if reads {
		authorize = s.workspaces.Role
	}
	r, err := authorize(ctx, tx, userID, id)
	switch {
	case err != nil:
		return "", err
	case !r.AtLeast(role.Admin):
		return "", workspace.ErrInsufficientRole
	}
	return r, nil
}

// configured reports whether the instance sends email, and so emails the
// link of an invitation to an address, rather than give it as text.
func (s *Service) configured(ctx context.Context) (bool, error) {
	if s.email == nil {
		return false, nil
	}
	return s.email.Configured(ctx)
}

// Create invites the address in.Email, or whoever holds the link if it is "",
// to the workspace id with the role in.Role, as the user userID asks, as
// client, and records that in the audit log. On an instance that sends
// email, an invitation to an address is emailed, by a job that the
// transaction enqueues, which waits until ResendInterval has passed since
// the workspace last emailed the address; otherwise the invitation's URL is
// its link, which the user gives the invited person. It returns
// workspace.ErrNotFound if the workspace does not exist, or the user's role
// in it is none, workspace.ErrInsufficientRole unless the role is owner or
// admin, workspace.ErrOwnerRequired for an invitation as owner unless it is
// owner, then input.FieldErrors if the address or the role is not one that
// an invitation takes, workspace.ErrAlreadyMember if the address belongs to
// a member of the workspace, and ErrExists if it has an open invitation of
// the workspace, and invites no one then.
func (s *Service) Create(ctx context.Context, userID, id ids.UUID, in Request, client audit.Client) (Invite, error) {
	configured, err := s.configured(ctx)
	if err != nil {
		return Invite{}, err
	}
	var inv Invite
	err = s.db.InTx(ctx, database.Scope{WorkspaceID: id, UserID: userID}, func(tx pgx.Tx) error {
		r, err := s.manage(ctx, tx, userID, id, false)
		if err != nil {
			return err
		}
		if in.Role == role.Owner && r != role.Owner {
			return workspace.ErrOwnerRequired
		}
		if inv, err = in.check(); err != nil {
			return err
		}
		emailed := configured && inv.Email != ""
		q := queries.New(tx)
		if err := refuseMember(ctx, q, id, inv.Email); err != nil {
			return err
		}
		l := s.newLink(&inv, userID, emailed)
		inv.ID, inv.CreatedBy = ids.New(), userID
		row, err := q.InsertInvite(ctx, queries.InsertInviteParams{ID: nullUUID(inv.ID), WorkspaceID: nullUUID(id),
			Email: pgtype.Text{String: inv.Email, Valid: inv.Email != ""}, Role: string(inv.Role), TokenHash: l.hash,
			Lifetime: database.Interval(Lifetime), CreatedBy: nullUUID(userID), LinkHolder: nullUUID(l.holder),
			EmailRequest: nullUUID(l.request)})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrExists
		}
		if err != nil {
			return fmt.Errorf("invite: add the invitation: %w", err)
		}
		inv.ExpiresAt, inv.CreatedAt = row.ExpiresAt.Time, row.CreatedAt.Time
		if emailed {
			if err := s.enqueue(ctx, tx, id, inv.ID, l.request, inv.Email); err != nil {
				return err
			}
		}
		return record(ctx, tx, audit.ActionInviteCreate, id, userID, inv, emailed, client)
	})
	if err != nil {
		return Invite{}, err
	}
	return inv, nil
}

// link is what a request that gives an invitation a new link records of it.
type link struct {
	// hash is the SHA-256 hash of the secret of a link given as text, and
	// holder the user who is given it, the invitation's link holder, whose
	// removal from the workspace revokes the invitation
	// (workspace.Service.RemoveMember), or neither for a link that is
	// emailed.
	hash   []byte
	holder ids.UUID
	// request is the request to email the link, whose job alone emails it
	// (send), or zero for a link given as text.
	request ids.UUID
}

// newLink gives the invitation inv a new link, as its URL, which the user
// userID is given as text, unless emailed says that the instance emails it,
// and returns what the invitation records of it.
func (s *Service) newLink(inv *Invite, userID ids.UUID, emailed bool) link {
	if emailed {
		return link{request: ids.New()}
	}
	value, hash := account.NewSecret()
	inv.URL = secret.New(linkURL(s.publicURL, value))
	return link{hash: hash, holder: userID}
}

// refuseMember returns workspace.ErrAlreadyMember if the address email
// belongs to a member of the workspace id, whose transaction q's is, and nil
// if it does not, or is "", as for a link invitation.
func refuseMember(ctx context.Context, q *queries.Queries, id ids.UUID, email string) error {
	if email == "" {
		return nil
	}
	member, err := q.AddressIsMember(ctx, queries.AddressIsMemberParams{WorkspaceID: nullUUID(id), Email: email})
	switch {
	case err != nil:
		return fmt.Errorf("invite: read whether the address is a member's: %w", err)
	case member:
		return workspace.ErrAlreadyMember
	}
	return nil
}

// check returns the invitation that the request makes, with its address as
// accounts hold it, or input.FieldErrors if the address or the role is not
// one that an invitation takes.
func (in Request) check() (Invite, error) {
	var errs input.FieldErrors
	inv := Invite{Role: in.Role}
	if in.Email != "" {
		inv.Email = account.CanonicalEmail(in.Email)
		if msg := account.CheckEmail(inv.Email); msg != "" {
			errs = append(errs, input.FieldError{Field: "email", Message: msg})
		}
	}
	if !slices.Contains(roles, in.Role) {
		errs = append(errs, input.FieldError{Field: "role",
			Message: "is not one of owner, admin, editor, commenter, viewer, and guest"})
	}
	if len(errs) > 0 {
		return Invite{}, errs
	}
	return inv, nil
}

// record records the event action of the invitation inv of the workspace id,
// which the user userID made, as client, emailed saying whether the instance
// emails its link.
func record(ctx context.Context, tx pgx.Tx, action audit.Action, id, userID ids.UUID, inv Invite, emailed bool,
	client audit.Client) error {
	metadata := map[string]any{"role": inv.Role, "kind": inv.Kind()}
	if action != audit.ActionInviteRevoke {
		metadata["emailed"] = emailed
	}
	return audit.Record(ctx, tx, audit.Entry{Action: action, WorkspaceID: id, ActorUserID: userID,
		SubjectID: inv.ID, Result: audit.ResultSuccess, IP: client.IP, UserAgent: client.UserAgent,
		Metadata: metadata})
}

// List returns at most limit of the open invitations of the workspace id,
// expired or not, in the order of their IDs: the first, if after is zero, or
// else those after the invitation after, for the user userID. It returns
// workspace.ErrNotFound if the workspace does not exist, or the user's role
// in it is none, and workspace.ErrInsufficientRole unless the role is owner
// or admin.
func (s *Service) List(ctx context.Context, userID, id, after ids.UUID, limit int) ([]Invite, error) {
	var out []Invite
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: id, UserID: userID}, func(tx pgx.Tx) error {
		if _, err := s.manage(ctx, tx, userID, id, true); err != nil {
			return err
		}
		rows, err := queries.New(tx).OpenInvites(ctx, queries.OpenInvitesParams{WorkspaceID: nullUUID(id),
			After: nullUUID(after), MaxRows: int32(min(limit, 1<<20))})
		if err != nil {
			return fmt.Errorf("invite: list the invitations: %w", err)
		}
		for _, r := range rows {
			out = append(out, Invite{ID: r.ID.Bytes, Email: r.Email.String, Role: role.Role(r.Role),
				ExpiresAt: r.ExpiresAt.Time, Expired: r.Expired, SentAt: r.SentAt.Time, CreatedBy: r.CreatedBy.Bytes,
				CreatedAt: r.CreatedAt.Time})
		}
		return nil
	})
	return out, err
}

// lock returns the invitation inviteID of the workspace id, whose
// transaction tx is, and locks it, or ErrNotFound if there is none, and then
// ErrAccepted if it has been accepted. revoked says whether it has been
// revoked. The invitation has neither its expiry nor when it was emailed,
// which the change that locks it replaces.
func lock(ctx context.Context, tx pgx.Tx, id, inviteID ids.UUID) (inv Invite, revoked bool, err error) {
	row, err := queries.New(tx).LockInvite(ctx, queries.LockInviteParams{WorkspaceID: nullUUID(id),
		ID: nullUUID(inviteID)})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Invite{}, false, ErrNotFound
	case err != nil:
		return Invite{}, false, fmt.Errorf("invite: read the invitation: %w", err)
	case row.Accepted:
		return Invite{}, false, ErrAccepted
	}
	return Invite{ID: row.ID.Bytes, Email: row.Email.String, Role: role.Role(row.Role),
		CreatedBy: row.CreatedBy.Bytes, CreatedAt: row.CreatedAt.Time}, row.Revoked, nil
}

// Resend sends the invitation inviteID of the workspace id anew, as the user
// userID asks, as client, and records that in the audit log: it gives it a
// new link, in place of its old one, which no longer works, and makes it
// expire Lifetime from now, and emails the new link, or gives it as the
// invitation's URL, as Create does. It returns the errors of Create's
// workspace and role, then ErrNotFound if the workspace has no such
// invitation, ErrAccepted if it has been accepted, ErrRevoked if it has been
// revoked, and workspace.ErrAlreadyMember if its address belongs to a member
// of the workspace by now, and changes nothing then.
func (s *Service) Resend(ctx context.Context, userID, id, inviteID ids.UUID, client audit.Client) (Invite, error) {
	configured, err := s.configured(ctx)
	if err != nil {
		return Invite{}, err
	}
	var inv Invite
	err = s.db.InTx(ctx, database.Scope{WorkspaceID: id, UserID: userID}, func(tx pgx.Tx) error {
		r, err := s.manage(ctx, tx, userID, id, false)
		if err != nil {
			return err
		}
		var revoked bool
		if inv, revoked, err = lock(ctx, tx, id, inviteID); err != nil {
			return err
		}
		switch {
		case revoked:
			return ErrRevoked
		case inv.Role == role.Owner && r != role.Owner:
			return workspace.ErrOwnerRequired
		}
		if err := refuseMember(ctx, queries.New(tx), id, inv.Email); err != nil {
			return err
		}
		emailed := configured && inv.Email != ""
		l := s.newLink(&inv, userID, emailed)
		expires, err := queries.New(tx).RenewInvite(ctx, queries.RenewInviteParams{TokenHash: l.hash,
			LinkHolder: nullUUID(l.holder), EmailRequest: nullUUID(l.request), Lifetime: database.Interval(Lifetime),
			WorkspaceID: nullUUID(id), ID: nullUUID(inviteID)})
		if err != nil {
			return fmt.Errorf("invite: renew the invitation: %w", err)
		}
		// The new link was not emailed yet, so the invitation has no SentAt.
		inv.ExpiresAt = expires.Time
		if emailed {
			if err := s.enqueue(ctx, tx, id, inv.ID, l.request, inv.Email); err != nil {
				return err
			}
		}
		return record(ctx, tx, audit.ActionInviteResend, id, userID, inv, emailed, client)
	})
	if err != nil {
		return Invite{}, err
	}
	return inv, nil
}

// Revoke revokes the invitation inviteID of the workspace id, as the user
// userID asks, as client, and records that in the audit log, unless it has
// been revoked already, which it leaves as it is. Its link is refused then
// (ErrRevoked). It returns the errors of List, then ErrNotFound if the
// workspace has no such invitation, and ErrAccepted if it has been accepted.
func (s *Service) Revoke(ctx context.Context, userID, id, inviteID ids.UUID, client audit.Client) error {
	return s.db.InTx(ctx, database.Scope{WorkspaceID: id, UserID: userID}, func(tx pgx.Tx) error {
		if _, err := s.manage(ctx, tx, userID, id, false); err != nil {
			return err
		}
		inv, revoked, err := lock(ctx, tx, id, inviteID)
		if err != nil || revoked {
			return err
		}
		if err := queries.New(tx).RevokeInvite(ctx, queries.RevokeInviteParams{WorkspaceID: nullUUID(id),
			ID: nullUUID(inviteID)}); err != nil {
			return fmt.Errorf("invite: revoke the invitation: %w", err)
		}
		return record(ctx, tx, audit.ActionInviteRevoke, id, userID, inv, false, client)
	})
}

// Preview is what the holder of an invitation's link sees of it.
type Preview struct {
	// WorkspaceName is the name of the workspace that it invites to.
	WorkspaceName string
	Role          role.Role
	// Email is the invited address, or "" for a link invitation.
	Email     string
	ExpiresAt time.Time
}

// Kind returns whom the invitation invites.
func (p Preview) Kind() Kind {
	return kindOf(p.Email)
}

// held is an invitation that a link's token found, in a transaction of its
// workspace.
type held struct {
	workspaceID ids.UUID
	row         queries.LockInviteByHashRow
}

// workspaceOf returns the workspace of the invitation whose secret is value,
// and the secret's hash, or ErrTokenInvalid if there is none, through the
// context bootstrap (§5.4), which a transaction of the user userID, or of no
// one, calls before it knows the workspace.
func (s *Service) workspaceOf(ctx context.Context, userID ids.UUID, value secret.Value) (ids.UUID, []byte, error) {
	hash, ok := account.SecretHash(value)
	if !ok {
		return ids.UUID{}, nil, ErrTokenInvalid
	}
	var id ids.UUID
	err := s.db.InTx(ctx, database.Scope{UserID: userID}, func(tx pgx.Tx) error {
		row, err := queries.New(tx).ResolveInvite(ctx, hash)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrTokenInvalid
		case err != nil:
			return fmt.Errorf("invite: find the workspace of the link's invitation: %w", err)
		}
		id = row.Bytes
		return nil
	})
	return id, hash, err
}

// hold returns the invitation of the workspace id whose secret's hash is
// hash, locked until tx ends, a transaction of the workspace, with whether
// the account with the address address may accept it, or "" for none, or
// the error that using it is answered with: ErrTokenInvalid, ErrRevoked,
// ErrAccepted, or ErrExpired, in that order. An invitation that has been
// accepted is returned with ErrAccepted, with the account that accepted it
// in its row's AcceptedBy, so that the caller can tell its own acceptance.
func hold(ctx context.Context, tx pgx.Tx, id ids.UUID, hash []byte, address string) (held, error) {
	row, err := queries.New(tx).LockInviteByHash(ctx, queries.LockInviteByHashParams{
		Email: pgtype.Text{String: address, Valid: address != ""}, WorkspaceID: nullUUID(id), TokenHash: hash})
	h := held{workspaceID: id, row: row}
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// The link was replaced, or the workspace's deletion is pending.
		return h, ErrTokenInvalid
	case err != nil:
		return h, fmt.Errorf("invite: read the invitation: %w", err)
	case row.Revoked:
		return h, ErrRevoked
	case row.Accepted:
		return h, ErrAccepted
	case row.Expired:
		return h, ErrExpired
	}
	return h, nil
}

// Lookup returns what the holder of the link whose secret is value sees of
// its invitation: the workspace's name, the role, and the address, so that
// the page that accepts it can say so. It returns ErrTokenInvalid,
// ErrRevoked, ErrAccepted, or ErrExpired for an invitation that cannot be
// accepted.
func (s *Service) Lookup(ctx context.Context, value secret.Value) (Preview, error) {
	id, hash, err := s.workspaceOf(ctx, ids.UUID{}, value)
	if err != nil {
		return Preview{}, err
	}
	var p Preview
	err = s.db.InTx(ctx, database.Scope{WorkspaceID: id}, func(tx pgx.Tx) error {
		h, err := hold(ctx, tx, id, hash, "")
		if err != nil {
			return err
		}
		p = Preview{WorkspaceName: h.row.WorkspaceName, Role: role.Role(h.row.Role), Email: h.row.Email.String,
			ExpiresAt: h.row.ExpiresAt.Time}
		return nil
	})
	return p, err
}

// Accept accepts the invitation of the link whose secret is value as the
// signed-in user userID, who joins its workspace with its role, as client,
// and records that in the audit log, and returns the user's membership. It
// returns ErrTokenInvalid, ErrRevoked, ErrAccepted, or ErrExpired for an
// invitation that cannot be accepted, ErrEmailMismatch for one to another
// address than the user's account's (FR-TEN-02 AC3), and
// workspace.ErrAlreadyMember if the user is a member of the workspace
// already, which leaves the invitation open. An invitation that the user
// accepted already, as registering with it does, returns the user's
// membership again, while the user is still a member.
func (s *Service) Accept(ctx context.Context, userID ids.UUID, value secret.Value, client audit.Client) (
	workspace.Membership, error) {
	id, hash, err := s.workspaceOf(ctx, userID, value)
	if err != nil {
		return workspace.Membership{}, err
	}
	var m workspace.Membership
	err = s.db.InTx(ctx, database.Scope{WorkspaceID: id, UserID: userID}, func(tx pgx.Tx) error {
		row, err := queries.New(tx).UserEmail(ctx, nullUUID(userID))
		if errors.Is(err, pgx.ErrNoRows) {
			return account.ErrNoSession
		}
		if err != nil {
			return fmt.Errorf("invite: read the account's email address: %w", err)
		}
		h, err := hold(ctx, tx, id, hash, row.Email)
		if errors.Is(err, ErrAccepted) && h.row.AcceptedBy.Valid && ids.UUID(h.row.AcceptedBy.Bytes) == userID {
			if m, err = s.workspaces.MembershipIn(ctx, tx, id, userID); errors.Is(err, workspace.ErrNotFound) {
				// The user left the workspace since, or was removed from it.
				return ErrAccepted
			}
			return err
		}
		if err != nil {
			return err
		}
		if !h.row.EmailMatches {
			return ErrEmailMismatch
		}
		if err := s.accept(ctx, tx, h, userID, false, client); err != nil {
			return err
		}
		m, err = s.workspaces.MembershipIn(ctx, tx, id, userID)
		return err
	})
	if err != nil {
		return workspace.Membership{}, err
	}
	return m, nil
}

// accept makes the user userID a member of the workspace of the invitation h
// with its role, records that the user accepted it, as client, newAccount
// saying whether the acceptance created the user's account, and records both
// in the audit log, in tx, a transaction of the workspace.
func (s *Service) accept(ctx context.Context, tx pgx.Tx, h held, userID ids.UUID, newAccount bool,
	client audit.Client) error {
	inviteID := ids.UUID(h.row.ID.Bytes)
	r := role.Role(h.row.Role)
	mid, err := workspace.Join(ctx, tx, h.workspaceID, userID, r, h.row.CreatedBy.Bytes, inviteID, client)
	if err != nil {
		return err
	}
	if err := queries.New(tx).AcceptInvite(ctx, queries.AcceptInviteParams{AcceptedBy: nullUUID(userID),
		WorkspaceID: nullUUID(h.workspaceID), ID: h.row.ID}); err != nil {
		return fmt.Errorf("invite: accept the invitation: %w", err)
	}
	return audit.Record(ctx, tx, audit.Entry{Action: audit.ActionInviteAccept, WorkspaceID: h.workspaceID,
		ActorUserID: userID, SubjectID: inviteID, Result: audit.ResultSuccess, IP: client.IP,
		UserAgent: client.UserAgent, Metadata: map[string]any{"role": r, "kind": kindOf(h.row.Email.String),
			"membership_id": mid.String(), "new_account": newAccount}})
}

// Register registers an account with the link whose secret is value, as
// client, and accepts the invitation as the new account, in the transaction
// that adds it, as account.Service.RegisterInvited does. reg.Email may be
// left empty for an invitation to an address, which is then the address of
// the account; otherwise it must be that address, as citext compares them,
// or anything for a link invitation. If the address has an account already,
// Register adds no account, leaves the invitation open, and returns nil all
// the same, once it has checked the invitation and hashed the password as
// for a new account, so that its caller answers the same (FR-AUTH-01 AC1);
// the account accepts the invitation once it signs in. It
// returns account.ErrSetupRequired or account.ErrSignupClosed unless the
// signup policy lets an invitation create an account
// (account.Service.CheckInvitedSignup), then ErrTokenInvalid, ErrRevoked,
// ErrAccepted, or ErrExpired for an invitation that cannot be accepted,
// ErrEmailMismatch for one to another address than reg.Email, and the
// errors of RegisterInvited for a registration that is not valid, as one
// whose address registration refuses, whatever the invitation's, and adds
// nothing then.
func (s *Service) Register(ctx context.Context, value secret.Value, reg account.Registration,
	client audit.Client) error {
	if err := s.accounts.CheckInvitedSignup(ctx); err != nil {
		return err
	}
	id, hash, err := s.workspaceOf(ctx, ids.UUID{}, value)
	if err != nil {
		return err
	}
	address := account.CanonicalEmail(reg.Email)
	// An address that registration refuses is no invitation's either, and
	// RegisterInvited answers it as Register does, with input.FieldErrors,
	// rather than as another address than the invitation's.
	malformed := reg.Email != "" && account.CheckEmail(address) != ""
	if malformed {
		address = ""
	}
	err = s.db.InTx(ctx, database.Scope{WorkspaceID: id}, func(tx pgx.Tx) error {
		h, err := hold(ctx, tx, id, hash, address)
		switch {
		case err != nil:
			return err
		case reg.Email == "" && h.row.Email.Valid:
			reg.Email = h.row.Email.String
		case !h.row.EmailMatches && !malformed:
			return ErrEmailMismatch
		}
		return nil
	})
	if err != nil {
		return err
	}
	return s.accounts.RegisterInvited(ctx, reg, id, func(ctx context.Context, tx pgx.Tx, userID ids.UUID) error {
		// The invitation may have changed since it was checked, and the
		// account is new, so it is a member of no workspace yet.
		h, err := hold(ctx, tx, id, hash, account.CanonicalEmail(reg.Email))
		switch {
		case err != nil:
			return err
		case !h.row.EmailMatches:
			return ErrEmailMismatch
		}
		return s.accept(ctx, tx, h, userID, true, client)
	})
}

// nullUUID returns id as a uuid column, null if id is zero.
func nullUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != (ids.UUID{})}
}
