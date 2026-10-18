// Package apitoken keeps the API tokens (PRD FR-AUTH-04, NFR-SEC-05): a
// workspace's members make them for content and release automation, such as
// a CI job or the Godot addon, and every request made with one is
// authenticated here.
//
// A token belongs to one workspace, and may be narrowed to one of its
// projects, on which alone it then acts, and holds a set of scopes (package
// scope). Its secret starts with Prefix, so that a secret scanner finds a
// token that was committed or pasted by mistake, and the database holds only
// its SHA-256 hash: creating the token returns the secret, and nothing ever
// returns it again (AC1). A token may expire, and its owner, or an owner or
// admin of its workspace, revokes it. Removing its owner's membership
// revokes it too (package workspace), so that the token fails from the next
// request (AC4), and does not work again if the owner rejoins.
//
// A token never does more than its owner's role allows at the time of use:
// a request made with it is authorised by the owner's role as it is then,
// as a request made with a session is, and by the token's scopes and the
// project that it covers on top (scope.Token). Authenticate checks the
// token itself, on every request, from the database, and records each
// refusal of a token that exists in the audit log (FR-TEN-07), and the
// token's last use at most once a minute, so that a busy token does not
// write on every request (AC5).
//
// Managing tokens (§5.6): a member whose role in the workspace is owner,
// admin, or editor makes, lists, and revokes their own tokens of the whole
// workspace, and a member whose effective role on a project is one of those
// their own tokens narrowed to it, which a guest's grant can give them. An
// owner or admin of the workspace lists and revokes every member's tokens,
// which no grant lets anyone else do (FR-TEN-04 AC4). The routes of tokens
// are session-only, so that a token can make no other.
package apitoken

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/account"
	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/role"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// Prefix starts the secret of every token (FR-AUTH-04).
const Prefix = "scp_"

// MaxNameLength is the most characters that a token's name may have.
const MaxNameLength = 100

var (
	// ErrNotFound is returned for a workspace or a project that does not
	// exist, a project in the trash, one on which the user's role is none,
	// as it is for a user who is not a member of its workspace (§5.5), and a
	// token that is not an active token of the workspace or the project.
	ErrNotFound = errors.New("apitoken: no such token, project, or workspace, or the user has no role there")
	// ErrInsufficientRole is returned when the user's role does not allow
	// the operation (§5.6), as package authz decides.
	ErrInsufficientRole = authz.ErrInsufficientRole
	// ErrInvalid is returned by Authenticate for a secret that is no token's,
	// and for a token that is revoked or expired, whose owner's account is
	// not active, or whose owner is no longer a member of its workspace,
	// which the API answers with 401 unauthenticated.
	ErrInvalid = errors.New("apitoken: the API token is not valid")
	// ErrSessionRequired is returned by Authenticate for a valid token on a
	// route that takes none (FR-AUTH-04), which the API answers with 403
	// session_required.
	ErrSessionRequired = errors.New("apitoken: the route takes no API token")
	// ErrInsufficientScope is returned by Authenticate for a valid token
	// without the scope that the route needs (AC2).
	ErrInsufficientScope = scope.ErrInsufficientScope
)

// Token is an API token as its owner and the workspace's admins see it,
// without its secret.
type Token struct {
	ID, WorkspaceID ids.UUID
	// UserID is the token's owner, who made it, whose role it never
	// exceeds.
	UserID ids.UUID
	// ProjectID is the project that the token is narrowed to, or zero for a
	// token of the whole workspace.
	ProjectID ids.UUID
	Name      string
	Scopes    []scope.Scope
	// ExpiresAt is when the token expires, or nil if it does not, and
	// LastUsedAt when a request was last made with it, to the minute, or
	// nil if none was.
	ExpiresAt, LastUsedAt *time.Time
	CreatedAt             time.Time
}

// Issued is a token with its secret, which only creating it returns.
type Issued struct {
	Token
	Secret secret.Value
}

// Spec is what a token is made with.
type Spec struct {
	Name string
	// Scopes are the scopes that the token holds, at least one.
	Scopes []scope.Scope
	// ExpiresAt is when the token expires, or zero for a token that does
	// not.
	ExpiresAt time.Time
}

// Credential is who makes a request with a valid token, and what the token
// restricts them to.
type Credential struct {
	// UserID is the token's owner.
	UserID ids.UUID
	Token  scope.Token
}

// DB runs a transaction of the application login, as *database.DB does.
type DB interface {
	InTx(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error
}

// Options configure a Service.
type Options struct {
	// Policy resolves the roles.
	Policy role.Policy
}

// Service keeps the API tokens.
type Service struct {
	db     DB
	policy role.Policy
}

// New returns a Service that keeps the tokens in db, with opts.
func New(db DB, opts Options) *Service {
	return &Service{db: db, policy: opts.Policy}
}

// CreateWorkspaceToken makes a token of the whole workspace workspaceID for
// the user userID, who asks for it, as client, with spec, and records that
// in the audit log, in one transaction, and returns it with its secret. The
// name loses the white space around it, and the scopes are kept once each.
// It returns ErrNotFound if the workspace does not exist or the user's role
// in it is none, ErrInsufficientRole unless the role is owner, admin, or
// editor (§5.6), and then input.FieldErrors if spec is not valid, and makes
// nothing then.
func (s *Service) CreateWorkspaceToken(ctx context.Context, userID, workspaceID ids.UUID, spec Spec,
	client audit.Client) (Issued, error) {
	var out Issued
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID, UserID: userID}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		if err := activeWorkspace(ctx, q, workspaceID); err != nil {
			return err
		}
		// The membership is locked, so that its removal, which revokes the
		// member's tokens, waits for this one, and revokes it too.
		a, err := s.access(ctx, q, userID, workspaceID, true)
		if err != nil {
			return err
		}
		r := s.policy.Workspace(a.admin, a.member)
		if r == role.None {
			return ErrNotFound
		}
		if err := authz.Check(r, authz.ManageOwnWorkspaceTokens); err != nil {
			return err
		}
		checked, err := check(spec)
		if err != nil {
			return err
		}
		out, err = insert(ctx, tx, userID, workspaceID, ids.UUID{}, checked, userID, client)
		return err
	})
	if err != nil {
		return Issued{}, err
	}
	return out, nil
}

// CreateProjectToken makes a token narrowed to the project projectID for the
// user userID, as CreateWorkspaceToken makes one of the whole workspace. It
// returns ErrNotFound if the project does not exist, is in the trash, or the
// user's effective role on it is none, ErrInsufficientRole unless the role is
// owner, admin, or editor (§5.6), and then input.FieldErrors if spec is not
// valid.
func (s *Service) CreateProjectToken(ctx context.Context, userID, projectID ids.UUID, spec Spec,
	client audit.Client) (Issued, error) {
	workspaceID, err := s.workspaceOf(ctx, userID, projectID)
	if err != nil {
		return Issued{}, err
	}
	var out Issued
	err = s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID, UserID: userID}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		roles, err := s.projectRoles(ctx, q, userID, workspaceID, projectID, true)
		if err != nil {
			return err
		}
		if err := authz.Check(roles.effective, authz.ManageOwnProjectTokens); err != nil {
			return err
		}
		checked, err := check(spec)
		if err != nil {
			return err
		}
		out, err = insert(ctx, tx, userID, workspaceID, projectID, checked, userID, client)
		return err
	})
	if err != nil {
		return Issued{}, err
	}
	return out, nil
}

// Issue makes a token for the user userID in the workspace workspaceID,
// narrowed to its project projectID unless that is zero, with spec, for
// storycreep create-token, which the operator runs (FR-ADMIN-06), and
// records that in the audit log without an actor, as the operator's other
// commands do. The operator acts for themselves, so no role of the user's is
// checked here: every use of the token is held to the user's role at the
// time. It returns ErrNotFound if the workspace does not exist, the user is
// not a member of it, or the project does not exist in it or is in the
// trash, and input.FieldErrors if spec is not valid, and makes nothing then.
func (s *Service) Issue(ctx context.Context, userID, workspaceID, projectID ids.UUID, spec Spec) (Issued, error) {
	checked, err := check(spec)
	if err != nil {
		return Issued{}, err
	}
	var out Issued
	err = s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID, UserID: userID}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		if err := activeWorkspace(ctx, q, workspaceID); err != nil {
			return err
		}
		if projectID != (ids.UUID{}) {
			if err := liveProject(ctx, q, workspaceID, projectID); err != nil {
				return err
			}
		}
		if _, err := q.LockMembershipOf(ctx, queries.LockMembershipOfParams{WorkspaceID: nullUUID(workspaceID),
			UserID: nullUUID(userID)}); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return fmt.Errorf("apitoken: read the membership: %w", err)
		}
		out, err = insert(ctx, tx, userID, workspaceID, projectID, checked, ids.UUID{}, audit.Client{})
		return err
	})
	if err != nil {
		return Issued{}, err
	}
	return out, nil
}

// WorkspaceTokens returns at most limit of the tokens of the whole workspace
// workspaceID that are not revoked, expired ones included, that the user
// userID may manage, in the order of their IDs: the first, if after is zero,
// or else those after the token after. An owner or admin of the workspace is
// shown every member's, and an editor their own; the tokens narrowed to a
// project are its own list's (ProjectTokens). It returns ErrNotFound if the
// workspace does not exist or the user's role in it is none, and
// ErrInsufficientRole unless the role is owner, admin, or editor (§5.6).
func (s *Service) WorkspaceTokens(ctx context.Context, userID, workspaceID, after ids.UUID, limit int) ([]Token,
	error) {
	var out []Token
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID, UserID: userID}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		if err := activeWorkspace(ctx, q, workspaceID); err != nil {
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
		if err := authz.Check(r, authz.ManageOwnWorkspaceTokens); err != nil {
			return err
		}
		owner := nullUUID(userID)
		if authz.Allows(r, authz.ManageTokens) {
			owner = pgtype.UUID{}
		}
		rows, err := q.WorkspaceTokens(ctx, queries.WorkspaceTokensParams{WorkspaceID: nullUUID(workspaceID),
			UserID: owner, After: nullUUID(after), MaxRows: int32(min(limit, 1<<20))})
		if err != nil {
			return fmt.Errorf("apitoken: list the tokens: %w", err)
		}
		out = tokensOf(rows)
		return nil
	})
	return out, err
}

// ProjectTokens returns at most limit of the tokens narrowed to the project
// projectID that are not revoked, expired ones included, that the user
// userID may manage, in the order of their IDs, as WorkspaceTokens does: an
// owner or admin of the project's workspace is shown every member's, and a
// user whose effective role on the project is owner, admin, or editor their
// own. It returns ErrNotFound if the project does not exist, is in the
// trash, or the user's effective role on it is none, and ErrInsufficientRole
// if the user may manage none of its tokens (§5.6).
func (s *Service) ProjectTokens(ctx context.Context, userID, projectID, after ids.UUID, limit int) ([]Token,
	error) {
	workspaceID, err := s.workspaceOf(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	var out []Token
	err = s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID, UserID: userID}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		roles, err := s.projectRoles(ctx, q, userID, workspaceID, projectID, false)
		if err != nil {
			return err
		}
		owner := nullUUID(userID)
		if authz.Allows(roles.workspace, authz.ManageTokens) {
			owner = pgtype.UUID{}
		} else if err := authz.Check(roles.effective, authz.ManageOwnProjectTokens); err != nil {
			return err
		}
		rows, err := q.ProjectTokens(ctx, queries.ProjectTokensParams{WorkspaceID: nullUUID(workspaceID),
			ProjectID: nullUUID(projectID), UserID: owner, After: nullUUID(after), MaxRows: int32(min(limit, 1<<20))})
		if err != nil {
			return fmt.Errorf("apitoken: list the tokens: %w", err)
		}
		out = tokensOf(rows)
		return nil
	})
	return out, err
}

// RevokeWorkspaceToken revokes the token tokenID of the whole workspace
// workspaceID, which the user userID asks for, as client, and records that in
// the audit log, after which every request made with it is refused. It
// returns ErrNotFound if the workspace does not exist or the user's role in
// it is none, ErrInsufficientRole unless the role is owner, admin, or editor
// (§5.6), then ErrNotFound if the workspace has no such token of the whole
// workspace that is not revoked, and ErrInsufficientRole if the token is
// another member's and the role is editor, and revokes nothing then.
func (s *Service) RevokeWorkspaceToken(ctx context.Context, userID, workspaceID, tokenID ids.UUID,
	client audit.Client) error {
	return s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID, UserID: userID}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		if err := activeWorkspace(ctx, q, workspaceID); err != nil {
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
		if err := authz.Check(r, authz.ManageOwnWorkspaceTokens); err != nil {
			return err
		}
		t, err := lockToken(ctx, q, workspaceID, tokenID, ids.UUID{})
		if err != nil {
			return err
		}
		if t.UserID.Bytes != userID {
			if err := authz.Check(r, authz.ManageTokens); err != nil {
				return err
			}
		}
		return revoke(ctx, tx, t, userID, client)
	})
}

// RevokeProjectToken revokes the token tokenID narrowed to the project
// projectID, as RevokeWorkspaceToken revokes one of the whole workspace. It
// returns ErrNotFound if the project does not exist, is in the trash, or the
// user's effective role on it is none, ErrInsufficientRole if the user may
// manage none of its tokens (§5.6), then ErrNotFound if the project has no
// such token that is not revoked, and ErrInsufficientRole if the token is
// another member's and the user's role in the workspace is neither owner nor
// admin, as a guest's or a viewer's admin grant on the project does not make
// it (FR-TEN-04 AC4).
func (s *Service) RevokeProjectToken(ctx context.Context, userID, projectID, tokenID ids.UUID,
	client audit.Client) error {
	workspaceID, err := s.workspaceOf(ctx, userID, projectID)
	if err != nil {
		return err
	}
	return s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID, UserID: userID}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		roles, err := s.projectRoles(ctx, q, userID, workspaceID, projectID, false)
		if err != nil {
			return err
		}
		own := authz.Allows(roles.effective, authz.ManageOwnProjectTokens)
		others := authz.Allows(roles.workspace, authz.ManageTokens)
		if !own && !others {
			return authz.Check(roles.effective, authz.ManageOwnProjectTokens)
		}
		t, err := lockToken(ctx, q, workspaceID, tokenID, projectID)
		if err != nil {
			return err
		}
		if !others && t.UserID.Bytes != userID {
			return authz.Check(roles.workspace, authz.ManageTokens)
		}
		return revoke(ctx, tx, t, userID, client)
	})
}

// Authenticate returns who makes a request with the token whose secret is
// value, on a route that takes a token that holds needed, or "" for a route
// that takes none, and records the token's use now, unless its use was
// recorded less than a minute ago (FR-AUTH-04 AC5). It checks the token from
// the database, every time, so that a token that is revoked, or whose
// owner's membership is removed, fails at its next request (AC4). It returns
// ErrInvalid for a secret that is no token's, a token that is revoked or
// expired, one whose owner's account is not active, and one whose owner is
// neither a member of its workspace nor an instance admin who acts as its
// owner, then ErrSessionRequired if needed is "", and ErrInsufficientScope if
// the token does not grant needed (scope.Grants). Each refusal of a token
// that exists is recorded in the audit log, in the token's workspace
// (NFR-SEC-05).
func (s *Service) Authenticate(ctx context.Context, value secret.Value, needed scope.Scope, client audit.Client) (
	Credential, error) {
	hash, ok := hashOf(value)
	if !ok {
		return Credential{}, ErrInvalid
	}
	var found queries.ResolveTokenRow
	err := s.db.InTx(ctx, database.Scope{}, func(tx pgx.Tx) error {
		var err error
		found, err = queries.New(tx).ResolveToken(ctx, hash)
		return err
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Credential{}, ErrInvalid
	case err != nil:
		return Credential{}, fmt.Errorf("apitoken: find the token: %w", err)
	}
	id, workspaceID, userID := ids.UUID(found.TokenID.Bytes), ids.UUID(found.WorkspaceID.Bytes),
		ids.UUID(found.UserID.Bytes)
	var out Credential
	var denied error
	err = s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID, UserID: userID}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		row, err := q.TokenForUse(ctx, queries.TokenForUseParams{WorkspaceID: nullUUID(workspaceID), ID: nullUUID(id)})
		if errors.Is(err, pgx.ErrNoRows) {
			// The token was deleted since it was found, with its owner's
			// account, its project, or its workspace.
			denied = ErrInvalid
			return nil
		}
		if err != nil {
			return fmt.Errorf("apitoken: read the token: %w", err)
		}
		scopes := scopesOf(row.Scopes)
		var reason string
		switch {
		case row.Revoked:
			reason, denied = "revoked", ErrInvalid
		case row.Expired:
			reason, denied = "expired", ErrInvalid
		case row.OwnerStatus != "active":
			reason, denied = "owner_inactive", ErrInvalid
		case !row.MemberRole.Valid && !(s.policy.InstanceAdminAccess && row.IsInstanceAdmin):
			reason, denied = "owner_not_member", ErrInvalid
		case needed == "":
			reason, denied = "session_required", ErrSessionRequired
		case !scope.Grants(scopes, needed):
			reason, denied = "insufficient_scope", ErrInsufficientScope
		}
		if denied != nil {
			metadata := map[string]any{"reason": reason}
			if denied == ErrInsufficientScope {
				metadata["scope"] = needed
			}
			// The refusal changes nothing else, so the entry commits.
			return audit.Record(ctx, tx, audit.Entry{Action: audit.ActionTokenUseDenied, WorkspaceID: workspaceID,
				ActorUserID: userID, ActorTokenID: id, SubjectID: id, Result: audit.ResultDenied, IP: client.IP,
				UserAgent: client.UserAgent, Metadata: metadata})
		}
		if row.Stale {
			if err := q.TouchToken(ctx, queries.TouchTokenParams{WorkspaceID: nullUUID(workspaceID),
				ID: nullUUID(id)}); err != nil {
				return fmt.Errorf("apitoken: record the token's use: %w", err)
			}
		}
		out = Credential{UserID: userID, Token: scope.Token{ID: id, WorkspaceID: workspaceID,
			ProjectID: row.ProjectID.Bytes, Scopes: scopes}}
		return nil
	})
	switch {
	case err != nil:
		return Credential{}, err
	case denied != nil:
		return Credential{}, denied
	}
	return out, nil
}

// hashOf returns the hash that the database holds of the token whose secret
// is value, and false if value is not a secret that a token could have.
func hashOf(value secret.Value) ([]byte, bool) {
	rest, ok := strings.CutPrefix(value.Reveal(), Prefix)
	if !ok {
		return nil, false
	}
	return account.SecretHash(secret.New(rest))
}

// check returns spec as a token is made with it: its name without the white
// space around it, and its scopes once each, in the order of scope.All, or
// input.FieldErrors if a field of spec is not valid.
func check(spec Spec) (Spec, error) {
	var errs input.FieldErrors
	spec.Name = strings.TrimSpace(spec.Name)
	if msg := input.CheckName(spec.Name, MaxNameLength); msg != "" {
		errs = append(errs, input.FieldError{Field: "name", Message: msg})
	}
	var scopes []scope.Scope
	for _, s := range scope.All() {
		if slices.Contains(spec.Scopes, s) {
			scopes = append(scopes, s)
		}
	}
	switch {
	case len(spec.Scopes) == 0:
		errs = append(errs, input.FieldError{Field: "scopes", Message: "is empty; a token holds one scope at least"})
	case slices.ContainsFunc(spec.Scopes, func(s scope.Scope) bool { return !s.Known() }):
		errs = append(errs, input.FieldError{Field: "scopes", Message: "holds a value that is not a scope"})
	}
	if !spec.ExpiresAt.IsZero() && !spec.ExpiresAt.After(time.Now()) {
		errs = append(errs, input.FieldError{Field: "expires_at", Message: "is not in the future"})
	}
	if len(errs) > 0 {
		return Spec{}, errs
	}
	spec.Scopes = scopes
	return spec, nil
}

// insert adds a token of the workspace workspaceID for the user owner,
// narrowed to the project projectID unless it is zero, with spec, which
// check returned, and records that in the audit log with the actor, or none
// if it is zero, in tx, and returns the token with its secret.
func insert(ctx context.Context, tx pgx.Tx, owner, workspaceID, projectID ids.UUID, spec Spec, actor ids.UUID,
	client audit.Client) (Issued, error) {
	value, hash := account.NewSecret()
	id := ids.New()
	var expires pgtype.Timestamptz
	if !spec.ExpiresAt.IsZero() {
		expires = pgtype.Timestamptz{Time: spec.ExpiresAt, Valid: true}
	}
	created, err := queries.New(tx).InsertToken(ctx, queries.InsertTokenParams{ID: nullUUID(id),
		WorkspaceID: nullUUID(workspaceID), UserID: nullUUID(owner), ProjectID: nullUUID(projectID), Name: spec.Name,
		TokenHash: hash, Scopes: names(spec.Scopes), ExpiresAt: expires})
	if err != nil {
		return Issued{}, fmt.Errorf("apitoken: add the token: %w", err)
	}
	metadata := map[string]any{"user_id": owner.String(), "scopes": names(spec.Scopes)}
	if projectID != (ids.UUID{}) {
		metadata["project_id"] = projectID.String()
	}
	if expires.Valid {
		metadata["expires_at"] = spec.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	if err := audit.Record(ctx, tx, audit.Entry{Action: audit.ActionTokenCreate, WorkspaceID: workspaceID,
		ActorUserID: actor, SubjectID: id, Result: audit.ResultSuccess, IP: client.IP, UserAgent: client.UserAgent,
		Metadata: metadata}); err != nil {
		return Issued{}, err
	}
	t := Token{ID: id, WorkspaceID: workspaceID, UserID: owner, ProjectID: projectID, Name: spec.Name,
		Scopes: spec.Scopes, ExpiresAt: timeOf(expires), CreatedAt: created.Time}
	return Issued{Token: t, Secret: secret.New(Prefix + value.Reveal())}, nil
}

// lockToken returns the token tokenID of the workspace workspaceID that is
// not revoked, narrowed to the project projectID, or of the whole workspace
// if it is zero, and locks it until q's transaction ends, or ErrNotFound if
// there is no such token.
func lockToken(ctx context.Context, q *queries.Queries, workspaceID, tokenID, projectID ids.UUID) (queries.ApiToken,
	error) {
	t, err := q.LockToken(ctx, queries.LockTokenParams{WorkspaceID: nullUUID(workspaceID), ID: nullUUID(tokenID),
		ProjectID: nullUUID(projectID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	if err != nil {
		return t, fmt.Errorf("apitoken: read the token: %w", err)
	}
	return t, nil
}

// revoke revokes the token t, which lockToken locked, which the user actor
// asks for, as client, and records that in the audit log, in tx.
func revoke(ctx context.Context, tx pgx.Tx, t queries.ApiToken, actor ids.UUID, client audit.Client) error {
	if err := queries.New(tx).RevokeToken(ctx, queries.RevokeTokenParams{WorkspaceID: t.WorkspaceID,
		ID: t.ID}); err != nil {
		return fmt.Errorf("apitoken: revoke the token: %w", err)
	}
	metadata := map[string]any{"user_id": ids.UUID(t.UserID.Bytes).String()}
	if t.ProjectID.Valid {
		metadata["project_id"] = ids.UUID(t.ProjectID.Bytes).String()
	}
	return audit.Record(ctx, tx, audit.Entry{Action: audit.ActionTokenRevoke, WorkspaceID: t.WorkspaceID.Bytes,
		ActorUserID: actor, SubjectID: t.ID.Bytes, Result: audit.ResultSuccess, IP: client.IP,
		UserAgent: client.UserAgent, Metadata: metadata})
}

// access is what the database holds about a user that their roles in a
// workspace are resolved from (§5.5).
type access struct {
	// admin is whether the user is an instance admin who acts as owner, as
	// they do only where the policy lets them.
	admin bool
	// membership is the user's membership of the workspace, zero if there is
	// none, and member its role, role.None then.
	membership ids.UUID
	member     role.Role
}

// access reads, in q's transaction, a transaction of the workspace
// workspaceID, what the user userID's roles are resolved from, as package
// project does. If lock is set, it locks the user's membership FOR SHARE
// until the transaction ends. It returns ErrNotFound if the policy lets an
// instance admin act as owner and the account is no longer active.
func (s *Service) access(ctx context.Context, q *queries.Queries, userID, workspaceID ids.UUID, lock bool) (access,
	error) {
	a := access{member: role.None}
	var m queries.MembershipOfRow
	var err error
	if lock {
		var locked queries.LockMembershipOfRow
		locked, err = q.LockMembershipOf(ctx, queries.LockMembershipOfParams{WorkspaceID: nullUUID(workspaceID),
			UserID: nullUUID(userID)})
		m = queries.MembershipOfRow(locked)
	} else {
		m, err = q.MembershipOf(ctx, queries.MembershipOfParams{WorkspaceID: nullUUID(workspaceID),
			UserID: nullUUID(userID)})
	}
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return access{}, fmt.Errorf("apitoken: read the membership: %w", err)
	default:
		a.membership, a.member = m.ID.Bytes, role.Role(m.Role)
	}
	if s.policy.InstanceAdminAccess {
		a.admin, err = q.InstanceAdmin(ctx, nullUUID(userID))
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return access{}, ErrNotFound
		case err != nil:
			return access{}, fmt.Errorf("apitoken: read the account: %w", err)
		}
	}
	return a, nil
}

// roles are the roles that a user acts with on a project, for the operations
// that the project authorises and those that its workspace does.
type roles struct {
	effective, workspace role.Role
}

// projectRoles returns the roles of the user userID on the project projectID
// of the workspace workspaceID, read in q's transaction, a transaction of the
// workspace, locking the user's membership if lock is set (access). It
// returns ErrNotFound if the workspace does not exist, the project does not
// exist or is in the trash, or the user's effective role on it is none.
func (s *Service) projectRoles(ctx context.Context, q *queries.Queries, userID, workspaceID, projectID ids.UUID,
	lock bool) (roles, error) {
	if err := activeWorkspace(ctx, q, workspaceID); err != nil {
		return roles{}, err
	}
	if err := liveProject(ctx, q, workspaceID, projectID); err != nil {
		return roles{}, err
	}
	a, err := s.access(ctx, q, userID, workspaceID, lock)
	if err != nil {
		return roles{}, err
	}
	grant := role.None
	if a.membership != (ids.UUID{}) {
		g, err := q.ProjectGrantRole(ctx, queries.ProjectGrantRoleParams{WorkspaceID: nullUUID(workspaceID),
			MembershipID: nullUUID(a.membership), ProjectID: nullUUID(projectID)})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return roles{}, fmt.Errorf("apitoken: read the project grant: %w", err)
		default:
			grant = role.Role(g)
		}
	}
	r := roles{effective: s.policy.Effective(a.admin, a.member, grant), workspace: s.policy.Workspace(a.admin, a.member)}
	if r.effective == role.None {
		return roles{}, ErrNotFound
	}
	return r, nil
}

// workspaceOf returns the workspace of the project projectID, through the
// context bootstrap (§5.4), or ErrNotFound if there is no such project, or
// its workspace's deletion is pending.
func (s *Service) workspaceOf(ctx context.Context, userID, projectID ids.UUID) (ids.UUID, error) {
	var ws pgtype.UUID
	err := s.db.InTx(ctx, database.Scope{UserID: userID}, func(tx pgx.Tx) error {
		var err error
		ws, err = queries.New(tx).ProjectWorkspace(ctx, nullUUID(projectID))
		return err
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ids.UUID{}, ErrNotFound
	case err != nil:
		return ids.UUID{}, fmt.Errorf("apitoken: find the project's workspace: %w", err)
	}
	return ws.Bytes, nil
}

// activeWorkspace returns ErrNotFound unless the workspace workspaceID, read
// in q's transaction, exists and its deletion is not pending.
func activeWorkspace(ctx context.Context, q *queries.Queries, workspaceID ids.UUID) error {
	if _, err := q.Workspace(ctx, nullUUID(workspaceID)); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("apitoken: read the workspace: %w", err)
	}
	return nil
}

// liveProject returns ErrNotFound unless the workspace workspaceID, whose
// transaction q's is, has the project projectID, and it is not in the trash.
func liveProject(ctx context.Context, q *queries.Queries, workspaceID, projectID ids.UUID) error {
	if _, err := q.Project(ctx, queries.ProjectParams{WorkspaceID: nullUUID(workspaceID),
		ID: nullUUID(projectID)}); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("apitoken: read the project: %w", err)
	}
	return nil
}

// tokensOf returns the tokens that rows hold.
func tokensOf(rows []queries.ApiToken) []Token {
	out := make([]Token, len(rows))
	for i, t := range rows {
		out[i] = Token{ID: t.ID.Bytes, WorkspaceID: t.WorkspaceID.Bytes, UserID: t.UserID.Bytes,
			ProjectID: t.ProjectID.Bytes, Name: t.Name, Scopes: scopesOf(t.Scopes), ExpiresAt: timeOf(t.ExpiresAt),
			LastUsedAt: timeOf(t.LastUsedAt), CreatedAt: t.CreatedAt.Time}
	}
	return out
}

// scopesOf returns the scopes that the database holds as names.
func scopesOf(names []string) []scope.Scope {
	out := make([]scope.Scope, len(names))
	for i, n := range names {
		out[i] = scope.Scope(n)
	}
	return out
}

// names returns the names of the scopes, as the database holds them.
func names(scopes []scope.Scope) []string {
	out := make([]string, len(scopes))
	for i, s := range scopes {
		out[i] = string(s)
	}
	return out
}

// timeOf returns the time of the column t, or nil if it is null.
func timeOf(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}

// nullUUID returns id as a uuid column, null if id is zero.
func nullUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != (ids.UUID{})}
}
