package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/account"
	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/i18n"
	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// readyDB is the server's database, once the database step of startup has
// opened it. A request reaches it only once the server is ready, because the
// readiness gate refuses the API's requests until then.
type readyDB struct {
	ready *readiness
}

func (d readyDB) InTx(ctx context.Context, scope database.Scope, fn func(pgx.Tx) error) error {
	db := d.ready.db.Load()
	if db == nil {
		return errors.New("the database step of startup has not finished")
	}
	return db.InTx(ctx, scope, fn)
}

// registerBody is the body of POST /api/v1/auth/register.
type registerBody struct {
	Email string `json:"email" format:"email" maxLength:"254" doc:"The account's email address, which signs in: an address alone, as jo@example.com, of at most 254 bytes. Addresses compare without regard to case."`
	// The limits are the account package's.
	DisplayName string `json:"display_name" maxLength:"100" doc:"The name that other people see, of 1 to 100 characters, without control characters. White space around it is removed."`
	Password    string `json:"password" maxLength:"256" doc:"The password: at least 12 characters, and not one of the most common passwords, or the answer is weak_password."`
}

type registerInput struct {
	Body registerBody
}

// acceptedOutput is an answer of 202 without a body.
type acceptedOutput struct{}

// registerAccounts registers the routes of accounts (PRD FR-AUTH-01).
func registerAccounts(m *api.Mux, accounts *account.Service) {
	api.Register(m, api.Public, huma.Operation{
		OperationID: "registerAccount",
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/register",
		Summary:     "Register an account",
		Description: "Creates an account with an email address, a display name, and a password, if the instance's " +
			"signup policy, STORYCREEP_SIGNUP, lets anyone register. Under closed the answer is 403 signup_closed, " +
			"and under invite 403 invite_required, because only an invitation creates an account then; either " +
			"comes whatever the request holds. The password must have at least 12 characters, counted as Unicode " +
			"code points once it is in NFKC, and must not be one of the 10,000 most common passwords, compared " +
			"without regard to case, or the answer is 422 weak_password. Otherwise the answer is 202 without a " +
			"body, whether or not the address has an account already, which then stays as it was, so that the " +
			"answer does not tell whether an address has an account.",
		Tags:          []string{"Authentication"},
		DefaultStatus: http.StatusAccepted,
		// The policy is checked before the body is read, so that a
		// registration is refused whatever it holds (FR-AUTH-01 AC3).
		Middlewares: huma.Middlewares{api.Guard(func(ctx context.Context) error {
			return accountError(accounts.CheckSignup(ctx))
		})},
	}, func(ctx context.Context, in *registerInput) (*acceptedOutput, error) {
		err := accounts.Register(ctx, account.Registration{
			Email:       in.Body.Email,
			DisplayName: in.Body.DisplayName,
			Password:    secret.New(in.Body.Password),
		})
		if err != nil {
			return nil, accountError(err)
		}
		return &acceptedOutput{}, nil
	})
}

// accountError returns the error that the API answers err with, an error
// of package account, or err itself if it is none of them.
func accountError(err error) error {
	var fields account.FieldErrors
	var weak *account.PasswordError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, account.ErrSignupClosed):
		return api.NewError(api.CodeSignupClosed)
	case errors.Is(err, account.ErrInviteRequired):
		return api.NewError(api.CodeInviteRequired)
	case errors.As(err, &fields):
		problems := make([]api.Problem, len(fields))
		for i, f := range fields {
			problems[i] = api.Problem{Location: "body." + f.Field, Message: f.Message}
		}
		return &api.Error{Code: api.CodeValidationFailed, Details: map[string]any{"errors": problems}}
	case errors.As(err, &weak):
		msg := i18n.Message{Key: i18n.K("error.weak_password.common")}
		problem := "is one of the most common passwords"
		if weak.Reason == account.PasswordTooShort {
			msg = i18n.Message{Key: i18n.K("error.weak_password.too_short"),
				Args: i18n.Args{"min": account.MinPasswordLength}}
			problem = fmt.Sprintf("has fewer than %d characters", account.MinPasswordLength)
		}
		return &api.Error{Code: api.CodeWeakPassword, Message: msg, Details: map[string]any{
			"reason": weak.Reason,
			"errors": []api.Problem{{Location: "body.password", Message: problem}},
		}}
	}
	return err
}
