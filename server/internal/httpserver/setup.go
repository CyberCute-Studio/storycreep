package httpserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/account"
	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/ratelimit"
	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
	"github.com/CyberCute-Studio/storycreep/server/internal/setup"
	"github.com/CyberCute-Studio/storycreep/server/internal/workspace"
)

// setupPath is the path of first-run setup's routes (PRD FR-ADMIN-02).
const setupPath = "/api/v1/setup"

// setupBody is the body of GET /api/v1/setup.
type setupBody struct {
	SignupPolicyFixed bool   `json:"signup_policy_fixed" doc:"Whether the configuration sets the signup policy, with STORYCREEP_SIGNUP, or because the instance is hosted, which then overrides the policy that setup records, for as long as it sets it. A setup screen offers no choice of policy then."`
	SignupPolicy      string `json:"signup_policy" enum:"closed,invite,open" doc:"The signup policy that the configuration sets, if it sets one, or else the one that setup records when the request names none: closed."`
}

type setupOutput struct {
	Body setupBody
}

// setupOwnerBody is the first owner's account in the body of POST
// /api/v1/setup. The limits are the account package's, as in registerBody.
type setupOwnerBody struct {
	Email       string `json:"email" format:"idn-email" maxLength:"254" doc:"The owner's email address, which signs in, as for POST /api/v1/auth/register"`
	DisplayName string `json:"display_name" maxLength:"100" doc:"The owner's display name, as for POST /api/v1/auth/register"`
	Password    string `json:"password" maxLength:"256" doc:"The owner's password: at least 12 characters, and not one of the most common passwords, or the answer is weak_password."`
}

// setupWorkspaceBody is the first workspace in the body of POST
// /api/v1/setup.
type setupWorkspaceBody struct {
	Name string `json:"name" maxLength:"100" doc:"The workspace's name, as for POST /api/v1/workspaces"`
	Slug string `json:"slug,omitempty" doc:"The workspace's slug, as for POST /api/v1/workspaces, with the same errors. Without it, the slug is derived from the name."`
}

// completeSetupBody is the body of POST /api/v1/setup.
type completeSetupBody struct {
	SetupCode    string             `json:"setup_code" maxLength:"64" doc:"The setup code, which the server writes to its log while setup is pending, as 7K2M-Q9XD-4RTB-H8VW. Case, hyphens, and spaces do not matter, and O is read as 0, and I and L as 1."`
	Owner        setupOwnerBody     `json:"owner" doc:"The first owner's account, which is an instance admin"`
	Workspace    setupWorkspaceBody `json:"workspace" doc:"The first workspace, which the owner owns, and which is the owner's personal workspace. Its default locale is STORYCREEP_DEFAULT_LOCALE."`
	SignupPolicy string             `json:"signup_policy,omitempty" enum:"closed,invite,open" doc:"Who can register an account once setup has completed: closed (no one), invite (people holding an invitation), or open (anyone). Without it, closed. STORYCREEP_SIGNUP overrides it for as long as it is set, as GET /api/v1/setup says."`
}

type completeSetupInput struct {
	Body completeSetupBody
}

// completedSetupBody is the body of the answer of POST /api/v1/setup.
type completedSetupBody struct {
	OwnerID   string          `json:"owner_id" doc:"The ID of the owner's account, which signs in with POST /api/v1/auth/login"`
	Workspace WorkspaceFields `json:"workspace" doc:"The first workspace"`
}

type completedSetupOutput struct {
	RateLimitOutput
	Body completedSetupBody
}

// setupDoc says, in the description of an operation of setup, when it
// answers 404.
const setupDoc = " Once setup has completed, the answer is 404 not_found, for everyone, and stays so."

// registerSetup registers the routes of first-run setup (PRD FR-ADMIN-02).
func registerSetup(m *api.Mux, setups *setup.Service, limits *ratelimit.Limiter) {
	// pending answers 404 once setup has completed, before the operation
	// reads the request.
	pending := api.Guard(func(ctx context.Context) error {
		st, err := setups.Status(ctx)
		switch {
		case err != nil:
			return err
		case st.Completed:
			return api.NewError(api.CodeNotFound)
		}
		return nil
	})

	api.Register(m, api.Public, huma.Operation{
		OperationID: "getSetup",
		Method:      http.MethodGet,
		Path:        setupPath,
		Summary:     "Get what first-run setup asks for",
		Description: "Tells a setup screen that the instance's first-run setup is pending, and whether it can " +
			"choose the signup policy." + setupDoc,
		Tags:        []string{"Setup"},
		Middlewares: huma.Middlewares{pending},
	}, func(ctx context.Context, _ *struct{}) (*setupOutput, error) {
		st, err := setups.Status(ctx)
		if err != nil {
			return nil, err
		}
		if st.Completed {
			return nil, api.NewError(api.CodeNotFound)
		}
		return &setupOutput{Body: setupBody{SignupPolicyFixed: st.SignupFixed, SignupPolicy: string(st.Signup)}}, nil
	})

	api.Register(m, api.Public, huma.Operation{
		OperationID: "completeSetup",
		Method:      http.MethodPost,
		Path:        setupPath,
		Summary:     "Complete first-run setup",
		Description: "Completes the instance's first-run setup, once: creates the first owner's account, which is " +
			"an instance admin, and the first workspace, which the owner owns, and records the signup policy. " +
			"Until then, POST /api/v1/auth/register answers 403 setup_required, whatever the policy. The setup " +
			"code, which the server writes to its log while setup is pending, must be the instance's, or the " +
			"answer is 403 invalid_setup_code. A field that the request does not allow, or an email address " +
			"that has an account, as one that storycreep create-user made, is answered with 422 " +
			"validation_failed. Of two setups at once, one completes, and the other is answered with 404 " +
			"not_found. The owner then signs in with POST /api/v1/auth/login." + setupDoc + authLimitDoc,
		Tags:          []string{"Setup"},
		DefaultStatus: http.StatusCreated,
		// The completed check comes first, so that the answer is 404 for
		// everyone once setup has completed, whatever the request holds.
		Middlewares: huma.Middlewares{pending, rateLimit(limits, ratelimit.Auth)},
	}, func(ctx context.Context, in *completeSetupInput) (*completedSetupOutput, error) {
		res, err := setups.Complete(ctx, setup.Request{
			Code: in.Body.SetupCode,
			Owner: account.Registration{Email: in.Body.Owner.Email, DisplayName: in.Body.Owner.DisplayName,
				Password: secret.New(in.Body.Owner.Password)},
			WorkspaceName: in.Body.Workspace.Name,
			WorkspaceSlug: in.Body.Workspace.Slug,
			Signup:        config.SignupPolicy(in.Body.SignupPolicy),
		}, clientFrom(ctx))
		if err != nil {
			return nil, setupError(err)
		}
		return &completedSetupOutput{Body: completedSetupBody{OwnerID: ids.Encode(ids.User, res.OwnerID),
			Workspace: newWorkspaceFields(res.Workspace)}}, nil
	})
}

// setupError returns the error that the API answers err with, an error of
// package setup, or of the packages whose errors it returns, or err itself
// if it is none of them.
func setupError(err error) error {
	var fields input.FieldErrors
	var weak *account.PasswordError
	var slug *workspace.SlugError
	switch {
	case errors.Is(err, setup.ErrCompleted):
		return api.NewError(api.CodeNotFound)
	case errors.Is(err, setup.ErrInvalidCode):
		return api.NewError(api.CodeInvalidSetupCode)
	case errors.As(err, &fields):
		return fieldErrors(fields)
	case errors.As(err, &weak):
		return weakPasswordError(weak, "body.owner.password")
	case errors.As(err, &slug):
		return slugInvalidError(slug, "body.workspace.slug")
	case errors.Is(err, workspace.ErrSlugTaken):
		return slugTakenError("body.workspace.slug")
	}
	return err
}
