package httpserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"golang.org/x/text/language"

	"github.com/CyberCute-Studio/storycreep/server/internal/account"
	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/i18n"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// readyDB is the server's database, once the database step of startup has
// opened it. A request reaches it only once the server is ready, because the
// readiness gate refuses the API's requests until then.
type readyDB struct {
	ready *readiness
}

// errNotOpened is what the server's database answers before the database
// step of startup has opened it.
var errNotOpened = errors.New("the database step of startup has not finished")

// opened returns the server's database, or errNotOpened before the database
// step of startup has opened it.
func (d readyDB) opened() (*database.DB, error) {
	db := d.ready.db.Load()
	if db == nil {
		return nil, errNotOpened
	}
	return db, nil
}

func (d readyDB) InTx(ctx context.Context, scope database.Scope, fn func(pgx.Tx) error) error {
	db, err := d.opened()
	if err != nil {
		return err
	}
	return db.InTx(ctx, scope, fn)
}

func (d readyDB) InSnapshot(ctx context.Context, scope database.Scope, fn func(pgx.Tx) error) error {
	db, err := d.opened()
	if err != nil {
		return err
	}
	return db.InSnapshot(ctx, scope, fn)
}

func (d readyDB) Dedicated(ctx context.Context) (*pgx.Conn, error) {
	db, err := d.opened()
	if err != nil {
		return nil, err
	}
	return db.Dedicated(ctx)
}

func (d readyDB) Notify(ctx context.Context, channel, payload string) error {
	db, err := d.opened()
	if err != nil {
		return err
	}
	return db.Notify(ctx, channel, payload)
}

// registerBody is the body of POST /api/v1/auth/register.
type registerBody struct {
	// The format is idn-email, because JSON Schema's email is an address in
	// ASCII (RFC 5321), and the account package takes addresses outside ASCII
	// too (RFC 6531).
	Email string `json:"email" format:"idn-email" maxLength:"254" doc:"The account's email address, which signs in: an address alone, as jo@example.com, of at most 254 bytes, without spaces or characters that are not printed. Addresses compare without regard to case or to how their characters are composed."`
	// The limits are the account package's.
	DisplayName string `json:"display_name" maxLength:"100" doc:"The name that other people see, of 1 to 100 characters, not all of them blank, and without control characters, bidirectional controls and line separators among them. White space around it is removed."`
	Password    string `json:"password" maxLength:"256" doc:"The password: at least 12 characters, and not one of the most common passwords, or the answer is weak_password."`
}

type registerInput struct {
	Body registerBody
}

// registerAccounts registers the routes of accounts (PRD FR-AUTH-01).
func registerAccounts(m *api.Mux, accounts *account.Service) {
	api.Register(m, api.Public, huma.Operation{
		OperationID: "registerAccount",
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/register",
		Summary:     "Register an account",
		Description: "Creates an account with an email address, a display name, and a password, if the instance's " +
			"signup policy lets anyone register: the policy that STORYCREEP_SIGNUP sets, or else the one that " +
			"first-run setup chose. Until setup has completed, the answer is 403 setup_required, whatever the " +
			"policy. Under closed the answer is 403 signup_closed, and under invite 403 invite_required, because " +
			"only an invitation creates an account then; each comes whatever the request holds. An email " +
			"address or a display name that its field does not allow is answered with 422 validation_failed. " +
			"The password must have at least 12 characters, counted as Unicode code points once it is in NFKC, " +
			"and must not be one of the 10,000 most common passwords, compared without regard to case, or the " +
			"answer is 422 weak_password. Otherwise the answer is 202 without a body, whether or not the address " +
			"has an account already, which then stays as it was, so that the answer does not tell whether an " +
			"address has an account. On an instance that sends email, as GET /api/v1/instance says, a new account " +
			"is emailed a link that verifies its address, which POST /api/v1/auth/verify-email uses, and an " +
			"address that has an account already is emailed nothing.",
		Tags:          []string{"Authentication"},
		DefaultStatus: http.StatusAccepted,
		// The policy is checked before the body is read, so that a
		// registration is refused whatever it holds (FR-AUTH-01 AC3), and
		// before the rate limit, which a registration that is refused anyway
		// need not count.
		Middlewares: huma.Middlewares{api.Guard(func(ctx context.Context) error {
			return accountError(accounts.CheckSignup(ctx))
		})},
	}, func(ctx context.Context, in *registerInput) (*struct{}, error) {
		err := accounts.Register(ctx, account.Registration{
			Email:       in.Body.Email,
			DisplayName: in.Body.DisplayName,
			Password:    secret.New(in.Body.Password),
		})
		if err != nil {
			return nil, accountError(err)
		}
		return &struct{}{}, nil
	}, api.RateLimit(api.BucketAuth))
}

// accountBody is the signed-in account, as its own user sees it.
type accountBody struct {
	ID            string `json:"id" doc:"The account's ID"`
	Email         string `json:"email" doc:"The account's email address, which signs in"`
	DisplayName   string `json:"display_name" doc:"The name that other people see"`
	Locale        string `json:"locale,omitempty" doc:"The locale that the user prefers, as a BCP 47 tag of a locale that the server has a catalog for, which the web app shows its pages in, and which the account's email is written in (PRD FR-I18N-02). Without it, the user prefers none, and the browser's languages choose, and then the instance's default."`
	EmailVerified bool   `json:"email_verified" doc:"Whether the account's email address is verified, with a link that POST /api/v1/auth/verify-email uses"`
}

func newAccountBody(p account.Profile) accountBody {
	b := accountBody{ID: ids.Encode(ids.User, p.ID), Email: p.Email, DisplayName: p.DisplayName,
		EmailVerified: p.EmailVerified}
	if p.Locale != language.Und {
		b.Locale = p.Locale.String()
	}
	return b
}

type accountOutput struct {
	Body accountBody
}

// updateAccountBody is the body of PATCH /api/v1/auth/account. A field that
// it leaves out, or sets to null, which huma takes for a field left out,
// stays as it is.
type updateAccountBody struct {
	Locale *string `json:"locale,omitempty" maxLength:"64" doc:"The locale that the user prefers, as a BCP 47 tag of a locale that the server has a catalog for, which is stored as the locale of the catalog that it asks for, as en for en-US; or the empty string for none, so that the browser's languages choose"`
}

type updateAccountInput struct {
	CSRFInput
	Body updateAccountBody
}

// registerOwnAccount registers the routes of the signed-in account's own
// settings: what its user sees of it, and the locale that the user prefers
// (PRD FR-I18N-02).
func registerOwnAccount(m *api.Mux, accounts *account.Service, cookie sessionCookie) {
	api.Register(m, api.OwnAccount, huma.Operation{
		OperationID: "getAccount",
		Method:      http.MethodGet,
		Path:        "/api/v1/auth/account",
		Summary:     "Get the signed-in account",
		Description: "Returns the signed-in account as its own user sees it: its email address, its display name, " +
			"the locale that the user prefers, if any, and whether the address is verified." + sessionAuthDoc,
		Tags: []string{"Authentication"},
	}, func(ctx context.Context, _ *struct{}) (*accountOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		p, err := accounts.Profile(ctx, cur.session.UserID)
		if errors.Is(err, account.ErrNoSession) {
			// The account is no longer active, as one suspended meanwhile.
			return nil, signedOut("", cookie)
		}
		if err != nil {
			return nil, err
		}
		return &accountOutput{Body: newAccountBody(p)}, nil
	}, api.SessionOnly())

	api.Register(m, api.OwnAccount, huma.Operation{
		OperationID: "updateAccount",
		Method:      http.MethodPatch,
		Path:        "/api/v1/auth/account",
		Summary:     "Change the signed-in account's settings",
		Description: "Changes the locale that the signed-in account's user prefers, and returns the account as " +
			"GET /api/v1/auth/account does; a field that the body leaves out, or sets to null, stays as it is. A " +
			"locale that is not a BCP 47 tag, or that no catalog of the server matches, is answered with 422 " +
			"validation_failed." + csrfDoc,
		Tags: []string{"Authentication"},
	}, func(ctx context.Context, in *updateAccountInput) (*accountOutput, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		var p account.Profile
		if in.Body.Locale != nil {
			p, err = accounts.SetLocale(ctx, cur.session.UserID, *in.Body.Locale)
		} else {
			p, err = accounts.Profile(ctx, cur.session.UserID)
		}
		var fields input.FieldErrors
		switch {
		case errors.Is(err, account.ErrNoSession):
			return nil, signedOut("", cookie)
		case errors.As(err, &fields):
			return nil, fieldErrors(fields)
		case err != nil:
			return nil, err
		}
		return &accountOutput{Body: newAccountBody(p)}, nil
	}, api.SessionOnly())
}

// accountError returns the error that the API answers err with, an error
// of package account, or of the rate limits that it applies (limitError), or
// err itself if it is none of them.
func accountError(err error) error {
	var fields input.FieldErrors
	var weak *account.PasswordError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, account.ErrSetupRequired):
		return api.NewError(api.CodeSetupRequired)
	case errors.Is(err, account.ErrSignupClosed):
		return api.NewError(api.CodeSignupClosed)
	case errors.Is(err, account.ErrInviteRequired):
		return api.NewError(api.CodeInviteRequired)
	case errors.As(err, &fields):
		return fieldErrors(fields)
	case errors.As(err, &weak):
		return weakPasswordError(weak, "body.password")
	}
	return limitError(err)
}

// weakPasswordError returns the error that the API answers weak with, for
// the password at location, such as body.password.
func weakPasswordError(weak *account.PasswordError, location string) *api.Error {
	msg := i18n.Message{Key: i18n.K("error.weak_password.common")}
	if weak.Reason == account.PasswordTooShort {
		msg = i18n.Message{Key: i18n.K("error.weak_password.too_short"),
			Args: i18n.Args{"min": account.MinPasswordLength}}
	}
	return &api.Error{Code: api.CodeWeakPassword, Message: msg, Details: map[string]any{
		"reason": weak.Reason,
		"errors": []api.Problem{{Location: location, Message: weak.Problem()}},
	}}
}
