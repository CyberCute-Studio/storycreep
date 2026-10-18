package httpserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/apitoken"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
)

// API tokens on the wire (PRD FR-AUTH-04, NFR-SEC-05). A member makes a
// token of the whole workspace with the routes of the workspace's tokens, and
// one narrowed to a project with the routes of the project's, which a guest
// with a grant on the project reaches too, because the project's routes, and
// not the workspace's, show the project to them (§5.5). The routes of tokens
// are session-only, so that a token can make no other.
//
// A request made with a token sends it in its Authorization header, as the
// credential of the Bearer scheme (authenticator), on a route that takes one
// (api.TokenScope).

// SessionCSRFInput is the CSRF token of a request that changes something, on
// a route that takes an API token too: a request made with the session
// cookie needs it, as the authenticator checks, and one made with a token
// does not, so the header is optional. Its name is exported so that huma
// reads it where it is embedded.
type SessionCSRFInput struct {
	CSRFToken string `header:"X-CSRF-Token" doc:"The session's CSRF token, csrf_token, which signing in and GET /api/v1/auth/session return: a request made with the session cookie without it is answered with 403 csrf_failed, and a request made with an API token needs none"`
}

// tokenAuthDoc says, in the description of an operation that takes an API
// token that holds the scope s, what the operation needs of a request.
func tokenAuthDoc(s scope.Scope) string {
	return " It needs a signed-in user: the session cookie that POST /api/v1/auth/login sets, or an API token that " +
		"holds the scope " + string(s) + ", or a scope that implies it, as Authorization: Bearer <token>; " +
		"otherwise the answer is 401 unauthenticated, or 403 insufficient_scope for a token without the scope, " +
		"each with the Bearer scheme's challenge in its WWW-Authenticate header (RFC 6750). A request made with a " +
		"token does no more than the token's owner's role allows at the time."
}

// tokenChangeDoc says, in the description of an operation that changes
// something and takes an API token that holds the scope s, what it needs of a
// request.
func tokenChangeDoc(s scope.Scope) string {
	return tokenAuthDoc(s) + " It changes something, so a request made with the session cookie also needs the " +
		"session's CSRF token in the X-CSRF-Token header, or the answer is 403 csrf_failed; a request made with a " +
		"token needs none."
}

// tokenNotFoundDoc says, in the description of an operation on a project
// that takes an API token, that a token answers a project that it does not
// cover as one that does not exist.
const tokenNotFoundDoc = " So is a project that the request's API token does not cover: one of another workspace, " +
	"or one other than the project that the token is narrowed to (PRD FR-AUTH-04 AC3)."

// APITokenBody is an API token, as the API shows it, without its secret.
// Its name is exported so that huma reads it where it is embedded
// (CreatedAPITokenBody).
type APITokenBody struct {
	ID          string     `json:"id" doc:"The token's ID"`
	WorkspaceID string     `json:"workspace_id" doc:"The ID of the workspace that the token belongs to, and acts in alone"`
	ProjectID   string     `json:"project_id,omitempty" doc:"The ID of the project that the token is narrowed to, on which alone it acts; a token without it acts on every project of its workspace"`
	UserID      string     `json:"user_id" doc:"The ID of the account that made the token and owns it: a request made with the token does no more than the owner's role allows at the time"`
	Name        string     `json:"name" doc:"The token's name"`
	Scopes      []string   `json:"scopes" nullable:"false" enum:"snapshot:read,project:read,project:write,snapshot:write,export:read" doc:"The scopes that the token holds: snapshot:read reads the published snapshots; project:read reads the draft too, and implies snapshot:read; project:write changes the draft, and implies project:read; snapshot:write publishes snapshots, and implies snapshot:read; and export:read is the authoring-data export, which no other scope implies"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty" doc:"When the token expires, after which every request made with it is answered with 401 unauthenticated; a token without it does not expire"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty" doc:"When a request was last made with the token, to the minute; a token without it has not been used"`
	CreatedAt   time.Time  `json:"created_at" doc:"When the token was made"`
}

func newAPITokenBody(t apitoken.Token) APITokenBody {
	b := APITokenBody{ID: ids.Encode(ids.APIToken, t.ID), WorkspaceID: ids.Encode(ids.Workspace, t.WorkspaceID),
		UserID: ids.Encode(ids.User, t.UserID), Name: t.Name, Scopes: scope.Names(t.Scopes),
		ExpiresAt: t.ExpiresAt, LastUsedAt: t.LastUsedAt, CreatedAt: t.CreatedAt}
	if t.ProjectID != (ids.UUID{}) {
		b.ProjectID = ids.Encode(ids.Project, t.ProjectID)
	}
	return b
}

// CreatedAPITokenBody is a token that was just made, with its secret.
type CreatedAPITokenBody struct {
	APITokenBody
	Secret string `json:"secret" doc:"The token's secret, which starts with scp_, and which a request sends as Authorization: Bearer <secret>. This is the only answer that holds it: the server keeps only its SHA-256 hash, so keep it now, as a secret."`
}

type createdTokenOutput struct {
	Body CreatedAPITokenBody
}

type tokensOutput struct {
	Body api.Page[APITokenBody]
}

// createTokenBody is the body of the requests that make a token.
type createTokenBody struct {
	Name      string     `json:"name" maxLength:"100" doc:"The token's name, which says what it is for, of 1 to 100 characters, not all of them blank, and without control characters, bidirectional controls, and line separators among them. White space around it is removed."`
	Scopes    []string   `json:"scopes" nullable:"false" minItems:"1" enum:"snapshot:read,project:read,project:write,snapshot:write,export:read" doc:"The scopes that the token holds, one at least, each kept once: snapshot:read reads the published snapshots; project:read reads the draft too, and implies snapshot:read; project:write changes the draft, and implies project:read; snapshot:write publishes snapshots, and implies snapshot:read; and export:read is the authoring-data export, which no other scope implies. A token that must not see unreleased work, such as a CI job's that only pulls releases, holds snapshot:read alone."`
	ExpiresAt *time.Time `json:"expires_at,omitempty" doc:"When the token expires, in RFC 3339, which must be in the future, and which the server keeps to the microsecond, as the answer gives it; without it, the token does not expire"`
}

// spec returns the token that b asks for.
func (b createTokenBody) spec() apitoken.Spec {
	s := apitoken.Spec{Name: b.Name, Scopes: make([]scope.Scope, len(b.Scopes)), ExpiresAt: b.ExpiresAt}
	for i, name := range b.Scopes {
		s.Scopes[i] = scope.Scope(name)
	}
	return s
}

type workspaceTokensInput struct {
	WorkspaceID string `path:"workspace_id" doc:"The workspace's ID"`
	api.PageParams
}

type projectTokensInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
	api.PageParams
}

type createWorkspaceTokenInput struct {
	CSRFInput
	WorkspaceID string `path:"workspace_id" doc:"The workspace's ID"`
	Body        createTokenBody
}

type createProjectTokenInput struct {
	CSRFInput
	ProjectID string `path:"project_id" doc:"The project's ID"`
	Body      createTokenBody
}

type revokeWorkspaceTokenInput struct {
	CSRFInput
	WorkspaceID string `path:"workspace_id" doc:"The workspace's ID"`
	TokenID     string `path:"token_id" doc:"The token's ID"`
}

type revokeProjectTokenInput struct {
	CSRFInput
	ProjectID string `path:"project_id" doc:"The project's ID"`
	TokenID   string `path:"token_id" doc:"The token's ID"`
}

// tokenSessionOnlyDoc says, in the description of an operation of tokens,
// why it is session-only, as sessionAuthDoc says that it is. It names what it
// explains, because csrfDoc's sentence comes between them in the
// descriptions of the operations that change something.
const tokenSessionOnlyDoc = " It refuses every API token so that no token makes, lists, or revokes another."

// createTokenDoc says, in the description of an operation that makes a
// token, what it does with it.
const createTokenDoc = " The answer holds the token's secret, which no other answer ever holds: the server keeps " +
	"only its SHA-256 hash. A request made with the token does no more than its owner's role allows at the time, " +
	"and fails from the next request once the token expires or is revoked, or once its owner's membership is " +
	"removed, which revokes it. A name or scopes that their field does not allow, or an expiry that is not in the " +
	"future, are answered with 422 validation_failed."

// registerTokens registers the routes of API tokens (PRD FR-AUTH-04).
func registerTokens(m *api.Mux, tokens *apitoken.Service) {
	api.Register(m, api.ManageWorkspaceTokens, huma.Operation{
		OperationID: "createWorkspaceToken",
		Method:      http.MethodPost,
		Path:        "/api/v1/workspaces/{workspace_id}/tokens",
		Summary:     "Make an API token of a workspace",
		Description: "Makes an API token of the whole workspace for the signed-in user, who owns it, with the scopes " +
			"that it holds, and an expiry, if it has one. It acts on every project of the workspace, and on no other " +
			"workspace's. Only an owner, an admin, or an editor of the workspace may, and anyone else with a role in " +
			"it is answered with 403 insufficient_role. POST /api/v1/projects/{project_id}/tokens makes one narrowed " +
			"to a project." + notFoundDoc + createTokenDoc + csrfDoc + tokenSessionOnlyDoc,
		Tags:          []string{"Tokens"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *createWorkspaceTokenInput) (*createdTokenOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		ws, err := ids.Decode(ids.Workspace, in.WorkspaceID)
		if err != nil {
			return nil, err
		}
		issued, err := tokens.CreateWorkspaceToken(ctx, cur.userID, ws, in.Body.spec(), clientFrom(ctx))
		if err != nil {
			return nil, tokenError(err)
		}
		return newCreatedTokenOutput(issued), nil
	}, api.RateLimit(api.BucketAuth), api.SessionOnly())

	api.Register(m, api.ManageProjectTokens, huma.Operation{
		OperationID: "createProjectToken",
		Method:      http.MethodPost,
		Path:        "/api/v1/projects/{project_id}/tokens",
		Summary:     "Make an API token narrowed to a project",
		Description: "Makes an API token narrowed to the project for the signed-in user, who owns it, with the scopes " +
			"that it holds, and an expiry, if it has one. It acts on this project alone, and answers every other " +
			"with 404 not_found. Only a user whose role on the project is owner, admin, or editor may, as a guest " +
			"whose grant on it is, and anyone else with a role on it is answered with 403 insufficient_role." +
			projectNotFoundDoc + createTokenDoc + csrfDoc + tokenSessionOnlyDoc,
		Tags:          []string{"Tokens"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *createProjectTokenInput) (*createdTokenOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		id, err := ids.Decode(ids.Project, in.ProjectID)
		if err != nil {
			return nil, err
		}
		issued, err := tokens.CreateProjectToken(ctx, cur.userID, id, in.Body.spec(), clientFrom(ctx))
		if err != nil {
			return nil, tokenError(err)
		}
		return newCreatedTokenOutput(issued), nil
	}, api.RateLimit(api.BucketAuth), api.SessionOnly())

	api.Register(m, api.ManageWorkspaceTokens, huma.Operation{
		OperationID: "listWorkspaceTokens",
		Method:      http.MethodGet,
		Path:        "/api/v1/workspaces/{workspace_id}/tokens",
		Summary:     "List a workspace's API tokens",
		Description: "Lists the API tokens of the whole workspace that are not revoked, expired ones included, in the " +
			"order of their IDs, oldest first, without their secrets: every member's, to an owner or an admin of the " +
			"workspace, and their own, to an editor. Anyone else with a role in it is answered with 403 " +
			"insufficient_role. The tokens narrowed to a project are listed by GET " +
			"/api/v1/projects/{project_id}/tokens." + notFoundDoc + sessionAuthDoc + tokenSessionOnlyDoc,
		Tags: []string{"Tokens"},
	}, func(ctx context.Context, in *workspaceTokensInput) (*tokensOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		ws, err := ids.Decode(ids.Workspace, in.WorkspaceID)
		if err != nil {
			return nil, err
		}
		after, err := cursorID(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := tokens.WorkspaceTokens(ctx, cur.userID, ws, after, in.Fetch())
		if err != nil {
			return nil, tokenError(err)
		}
		return tokenPage(in.PageParams, list)
	}, api.RateLimit(api.BucketAuth), api.SessionOnly())

	api.Register(m, api.ManageProjectTokens, huma.Operation{
		OperationID: "listProjectTokens",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/tokens",
		Summary:     "List a project's API tokens",
		Description: "Lists the API tokens narrowed to the project that are not revoked, expired ones included, in " +
			"the order of their IDs, oldest first, without their secrets: every member's, to an owner or an admin of " +
			"the project's workspace, and their own, to a user whose role on the project is owner, admin, or editor. " +
			"Anyone else with a role on it is answered with 403 insufficient_role." + projectNotFoundDoc +
			sessionAuthDoc + tokenSessionOnlyDoc,
		Tags: []string{"Tokens"},
	}, func(ctx context.Context, in *projectTokensInput) (*tokensOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		id, err := ids.Decode(ids.Project, in.ProjectID)
		if err != nil {
			return nil, err
		}
		after, err := cursorID(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := tokens.ProjectTokens(ctx, cur.userID, id, after, in.Fetch())
		if err != nil {
			return nil, tokenError(err)
		}
		return tokenPage(in.PageParams, list)
	}, api.RateLimit(api.BucketAuth), api.SessionOnly())

	api.Register(m, api.ManageWorkspaceTokens, huma.Operation{
		OperationID: "revokeWorkspaceToken",
		Method:      http.MethodDelete,
		Path:        "/api/v1/workspaces/{workspace_id}/tokens/{token_id}",
		Summary:     "Revoke an API token of a workspace",
		Description: "Revokes an API token of the whole workspace, after which every request made with it is answered " +
			"with 401 unauthenticated. An owner or an admin of the workspace revokes any member's, and an editor " +
			"their own; anyone else with a role in it is answered with 403 insufficient_role, and an editor who names " +
			"another member's token too." + notFoundDoc + " So is a token that the workspace does not have, one " +
			"narrowed to a project, which DELETE /api/v1/projects/{project_id}/tokens/{token_id} revokes, and one " +
			"that is revoked already." + csrfDoc + tokenSessionOnlyDoc,
		Tags:          []string{"Tokens"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *revokeWorkspaceTokenInput) (*struct{}, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		ws, err := ids.Decode(ids.Workspace, in.WorkspaceID)
		if err != nil {
			return nil, err
		}
		id, err := ids.Decode(ids.APIToken, in.TokenID)
		if err != nil {
			return nil, err
		}
		if err := tokens.RevokeWorkspaceToken(ctx, cur.userID, ws, id, clientFrom(ctx)); err != nil {
			return nil, tokenError(err)
		}
		return &struct{}{}, nil
	}, api.RateLimit(api.BucketAuth), api.SessionOnly())

	api.Register(m, api.ManageProjectTokens, huma.Operation{
		OperationID: "revokeProjectToken",
		Method:      http.MethodDelete,
		Path:        "/api/v1/projects/{project_id}/tokens/{token_id}",
		Summary:     "Revoke an API token narrowed to a project",
		Description: "Revokes an API token narrowed to the project, after which every request made with it is " +
			"answered with 401 unauthenticated. An owner or an admin of the project's workspace revokes any " +
			"member's, and a user whose role on the project is owner, admin, or editor their own; anyone else with a " +
			"role on it is answered with 403 insufficient_role, and so is such a user who names another member's " +
			"token, as a guest with an admin grant on the project, which authorises nothing of the workspace (PRD " +
			"FR-TEN-04 AC4)." + projectNotFoundDoc + " So is a token that is not narrowed to the project, or that is " +
			"revoked already." + csrfDoc + tokenSessionOnlyDoc,
		Tags:          []string{"Tokens"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *revokeProjectTokenInput) (*struct{}, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		projectID, err := ids.Decode(ids.Project, in.ProjectID)
		if err != nil {
			return nil, err
		}
		id, err := ids.Decode(ids.APIToken, in.TokenID)
		if err != nil {
			return nil, err
		}
		if err := tokens.RevokeProjectToken(ctx, cur.userID, projectID, id, clientFrom(ctx)); err != nil {
			return nil, tokenError(err)
		}
		return &struct{}{}, nil
	}, api.RateLimit(api.BucketAuth), api.SessionOnly())
}

// newCreatedTokenOutput returns the answer that holds the token that was
// just made, with its secret.
func newCreatedTokenOutput(issued apitoken.Issued) *createdTokenOutput {
	return &createdTokenOutput{Body: CreatedAPITokenBody{APITokenBody: newAPITokenBody(issued.Token),
		Secret: issued.Secret.Reveal()}}
}

// tokenPage returns the page of a list of tokens that in asks for, which is
// paged by the tokens' UUIDs.
func tokenPage(in api.PageParams, list []apitoken.Token) (*tokensOutput, error) {
	page, err := api.NewPage(in, list, func(t apitoken.Token) idKey { return idKey{ID: t.ID.String()} })
	if err != nil {
		return nil, err
	}
	out := &tokensOutput{Body: api.Page[APITokenBody]{Items: make([]APITokenBody, len(page.Items)),
		NextCursor: page.NextCursor}}
	for i, t := range page.Items {
		out.Body.Items[i] = newAPITokenBody(t)
	}
	return out, nil
}

// tokenError returns the error that the API answers err with, an error of
// package apitoken, or err itself if it is none of them.
func tokenError(err error) error {
	var fields input.FieldErrors
	switch {
	case errors.Is(err, apitoken.ErrNotFound):
		return api.NewError(api.CodeNotFound)
	case errors.Is(err, apitoken.ErrInsufficientRole):
		return api.NewError(api.CodeInsufficientRole)
	case errors.As(err, &fields):
		return fieldErrors(fields)
	}
	return limitError(err)
}
