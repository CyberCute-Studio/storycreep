package httpserver

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/account"
	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/jobs"
	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// Email links on the wire (PRD FR-AUTH-03): asking for a link that resets a
// password or verifies an email address, which the instance emails if it
// sends email, using a link, and, on an instance that sends no email, an
// instance admin's taking one as text. A link opens a page of the web app,
// with its token in the URL's fragment, which the page sends to the API.

// queueRef is the job queue, once Run has opened it, which the services
// enqueue their jobs on (account.Enqueuer). A request reaches it only once
// the server is ready, which it is after the queue opens.
type queueRef struct {
	queue atomic.Pointer[jobs.Queue]
}

func (r *queueRef) Enqueue(ctx context.Context, tx pgx.Tx, args jobs.Args, opts *jobs.EnqueueOptions) (jobs.Enqueued,
	error) {
	q := r.queue.Load()
	if q == nil {
		return jobs.Enqueued{}, errors.New("the job queue is not open")
	}
	return q.Enqueue(ctx, tx, args, opts)
}

// instanceBody is the body of GET /api/v1/instance.
type instanceBody struct {
	EmailConfigured bool `json:"email_configured" doc:"Whether the instance sends email, through the SMTP server of STORYCREEP_SMTP_URL or of first-run setup. Without it, a page says that email is not configured: the links that reset a password and verify an email address are not emailed, and an instance admin gives them out as text."`
}

type instanceOutput struct {
	Body instanceBody
}

// passwordResetBody is the body of POST /api/v1/auth/password-reset.
type passwordResetBody struct {
	Email string `json:"email" maxLength:"254" doc:"The email address of the account whose password to reset, in any case"`
}

type passwordResetInput struct {
	Body passwordResetBody
}

// resetPasswordBody is the body of POST /api/v1/auth/password-reset/confirm.
type resetPasswordBody struct {
	Token       string `json:"token" maxLength:"64" doc:"The token of the link that resets the password: what follows token= in the fragment of the URL that the link opens"`
	NewPassword string `json:"new_password" maxLength:"256" doc:"The new password: at least 12 characters, and not one of the most common passwords, or the answer is weak_password"`
}

type resetPasswordInput struct {
	Body resetPasswordBody
}

// verifyEmailBody is the body of POST /api/v1/auth/verify-email.
type verifyEmailBody struct {
	Token string `json:"token" maxLength:"64" doc:"The token of the link that verifies the email address: what follows token= in the fragment of the URL that the link opens"`
}

type verifyEmailInput struct {
	Body verifyEmailBody
}

// issueLinkBody is the body of the routes that issue a link as text.
type issueLinkBody struct {
	Email string `json:"email" maxLength:"254" doc:"The email address of the account, in any case"`
}

type issueLinkInput struct {
	CSRFInput
	Body issueLinkBody
}

// linkBody is a link that an instance admin takes as text.
type linkBody struct {
	URL       string    `json:"url" doc:"The link, which opens the web app's page that uses it, with its token in the URL's fragment. Whoever holds it can use it, so give it only to the account's user."`
	ExpiresAt time.Time `json:"expires_at" doc:"When the link expires"`
}

type linkOutput struct {
	Body linkBody
}

// The descriptions of the errors of a link, in an operation's description.
const tokenErrorsDoc = " A token that does not exist, of another kind of link, or of an account that can no longer " +
	"use it, is answered with 400 token_invalid, a link that has been used with 400 token_used, and one that " +
	"has expired with 400 token_expired."

// noEmailDoc says, in the description of an operation that emails a link,
// what an instance that sends no email answers.
const noEmailDoc = " An instance that sends no email answers 403 email_not_configured, whatever the request " +
	"holds, as GET /api/v1/instance says it will."

// adminLinkDoc describes the routes that issue a link as text.
const adminLinkDoc = " It is for an instance that sends no email, whose links an instance admin gives out " +
	"instead, to the account's user alone, because whoever holds a link can use it; an instance that sends email " +
	"answers 403 email_configured. A user who is not an instance admin, such as a workspace's owner or admin, is " +
	"answered with 403 instance_admin_required, whatever the request holds. An address that belongs to no " +
	"active account is answered with 404 not_found. The audit log records the issue."

// registerEmail registers the routes of email links (PRD FR-AUTH-03), and
// GET /api/v1/instance.
func registerEmail(m *api.Mux, accounts *account.Service) {
	// emailConfigured refuses a request on an instance that sends no email,
	// before the operation reads its body.
	emailConfigured := api.Guard(func(ctx context.Context) error {
		configured, err := accounts.EmailConfigured(ctx)
		switch {
		case err != nil:
			return err
		case !configured:
			return api.NewError(api.CodeEmailNotConfigured)
		}
		return nil
	})
	// instanceAdmin refuses a user who is not an instance admin, and a
	// request on an instance that sends email, before the operation reads
	// its body.
	instanceAdmin := api.Guard(func(ctx context.Context) error {
		cur, err := currentFrom(ctx)
		if err != nil {
			return err
		}
		if err := accounts.CheckInstanceAdmin(ctx, cur.session.UserID); err != nil {
			return linkError(err, "")
		}
		configured, err := accounts.EmailConfigured(ctx)
		switch {
		case err != nil:
			return err
		case configured:
			return api.NewError(api.CodeEmailConfigured)
		}
		return nil
	})

	api.Register(m, api.Public, huma.Operation{
		OperationID: "getInstance",
		Method:      http.MethodGet,
		Path:        "/api/v1/instance",
		Summary:     "Get what the instance offers",
		Description: "Says whether the instance sends email, so that a page can say that email is not " +
			"configured, and where the links that reset a password and verify an email address come from.",
		Tags: []string{"Instance"},
	}, func(ctx context.Context, _ *struct{}) (*instanceOutput, error) {
		configured, err := accounts.EmailConfigured(ctx)
		if err != nil {
			return nil, err
		}
		return &instanceOutput{Body: instanceBody{EmailConfigured: configured}}, nil
	})

	api.Register(m, api.Public, huma.Operation{
		OperationID: "requestPasswordReset",
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/password-reset",
		Summary:     "Ask for a link that resets a password",
		Description: "Emails a link that resets the password of the active account that the email address " +
			"belongs to, which lasts an hour, and is used once with POST /api/v1/auth/password-reset/confirm. The " +
			"answer is 202 without a body, as soon, whether or not the address belongs to an account, so that it " +
			"does not tell whether it does; an address of no account is emailed nothing. Nor is an account that was " +
			"emailed such a link in the last 10 minutes that still works, so that asking again and again does not " +
			"flood its inbox: the answer is the same, and the earlier link keeps working." + noEmailDoc,
		Tags:          []string{"Authentication"},
		DefaultStatus: http.StatusAccepted,
		Middlewares:   huma.Middlewares{emailConfigured},
	}, func(ctx context.Context, in *passwordResetInput) (*struct{}, error) {
		if err := accounts.RequestPasswordReset(ctx, in.Body.Email); err != nil {
			return nil, linkError(err, "")
		}
		return &struct{}{}, nil
	}, api.RateLimit(api.BucketAuth))

	api.Register(m, api.Public, huma.Operation{
		OperationID: "resetPassword",
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/password-reset/confirm",
		Summary:     "Reset a password with a link",
		Description: "Replaces the password of the account of a link that resets it, and uses the link, which " +
			"works once. It signs the account out everywhere: every session of the account is revoked, and the " +
			"user then signs in with the new password. The account's other reset links stop working too." +
			tokenErrorsDoc + " The new password must have at least 12 characters, counted as Unicode code points " +
			"once it is in NFKC, and must not be one of the 10,000 most common passwords, or the answer is 422 " +
			"weak_password, which leaves the link as it was.",
		Tags:          []string{"Authentication"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *resetPasswordInput) (*struct{}, error) {
		err := accounts.ResetPassword(ctx, secret.New(in.Body.Token), secret.New(in.Body.NewPassword), clientFrom(ctx))
		if err != nil {
			return nil, linkError(err, "body.new_password")
		}
		return &struct{}{}, nil
	}, api.RateLimit(api.BucketAuth))

	api.Register(m, api.Public, huma.Operation{
		OperationID: "verifyEmail",
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/verify-email",
		Summary:     "Verify an email address with a link",
		Description: "Records that the email address of the account of a link that verifies it is verified, and " +
			"uses the link, which works once, and only while the address is still the account's. Where the " +
			"instance requires a verified address before a workspace can be created, as hosted mode does, it " +
			"also gives the account its personal workspace, if it has had none; should that fail, the answer is " +
			"still 204, because the address is verified, and the account's next sign-in gives the workspace." +
			tokenErrorsDoc,
		Tags:          []string{"Authentication"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *verifyEmailInput) (*struct{}, error) {
		if err := accounts.VerifyEmail(ctx, secret.New(in.Body.Token), clientFrom(ctx)); err != nil {
			return nil, linkError(err, "")
		}
		return &struct{}{}, nil
	}, api.RateLimit(api.BucketAuth))

	api.Register(m, api.OwnAccount, huma.Operation{
		OperationID: "requestEmailVerification",
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/email-verification",
		Summary:     "Ask for a link that verifies the account's email address",
		Description: "Emails a link that verifies the signed-in account's email address, which lasts 24 hours, " +
			"and is used once with POST /api/v1/auth/verify-email. An address that is verified already is emailed " +
			"nothing, and nor is one that was emailed such a link in the last 10 minutes that still works: the " +
			"answer is the same, and the earlier link keeps working. Registering emails one too." + noEmailDoc +
			csrfDoc,
		Tags:          []string{"Authentication"},
		DefaultStatus: http.StatusAccepted,
		Middlewares:   huma.Middlewares{emailConfigured},
	}, func(ctx context.Context, _ *CSRFInput) (*struct{}, error) {
		cur, err := currentFrom(ctx)
		if err != nil {
			return nil, err
		}
		if err := accounts.RequestVerification(ctx, cur.session.UserID); err != nil {
			return nil, linkError(err, "")
		}
		return &struct{}{}, nil
	}, api.RateLimit(api.BucketAuth), api.SessionOnly())

	for _, l := range []struct {
		id, path, summary, doc string
		kind                   account.LinkKind
	}{
		{"issuePasswordResetLink", "/api/v1/admin/password-reset-links", "Take a link that resets a password",
			"Returns a link that resets the password of the active account that the email address belongs to, " +
				"which lasts an hour, and is used once with POST /api/v1/auth/password-reset/confirm.",
			account.LinkPasswordReset},
		{"issueVerificationLink", "/api/v1/admin/verification-links", "Take a link that verifies an email address",
			"Returns a link that verifies the email address of the active account that it belongs to, which lasts " +
				"24 hours, and is used once with POST /api/v1/auth/verify-email.",
			account.LinkVerification},
	} {
		api.Register(m, api.ManageInstance, huma.Operation{
			OperationID:   l.id,
			Method:        http.MethodPost,
			Path:          l.path,
			Summary:       l.summary,
			Description:   l.doc + adminLinkDoc + csrfDoc,
			Tags:          []string{"Instance administration"},
			DefaultStatus: http.StatusCreated,
			Middlewares:   huma.Middlewares{instanceAdmin},
		}, func(ctx context.Context, in *issueLinkInput) (*linkOutput, error) {
			cur, err := currentFrom(ctx)
			if err != nil {
				return nil, err
			}
			link, err := accounts.IssueLink(ctx, cur.session.UserID, l.kind, in.Body.Email, clientFrom(ctx))
			if err != nil {
				return nil, linkError(err, "")
			}
			return &linkOutput{Body: linkBody{URL: link.URL.Reveal(), ExpiresAt: link.ExpiresAt}}, nil
		}, api.RateLimit(api.BucketAuth), api.SessionOnly())
	}
}

// linkError returns the error that the API answers err with, an error of
// the links of package account, or of the rate limits that they apply
// (limitError), or err itself if it is none of them. passwordLocation is
// where a password that breaks the policy is, such as body.new_password.
func linkError(err error, passwordLocation string) error {
	var weak *account.PasswordError
	switch {
	case errors.Is(err, account.ErrEmailNotConfigured):
		return api.NewError(api.CodeEmailNotConfigured)
	case errors.Is(err, account.ErrEmailConfigured):
		return api.NewError(api.CodeEmailConfigured)
	case errors.Is(err, account.ErrNotInstanceAdmin):
		return api.NewError(api.CodeInstanceAdminRequired)
	case errors.Is(err, account.ErrTokenInvalid):
		return api.NewError(api.CodeTokenInvalid)
	case errors.Is(err, account.ErrTokenUsed):
		return api.NewError(api.CodeTokenUsed)
	case errors.Is(err, account.ErrTokenExpired):
		return api.NewError(api.CodeTokenExpired)
	case errors.Is(err, account.ErrNoAccount), errors.Is(err, account.ErrNotActive):
		return api.NewError(api.CodeNotFound)
	case errors.Is(err, account.ErrNoSession):
		return api.NewError(api.CodeUnauthenticated)
	case passwordLocation != "" && errors.As(err, &weak):
		return weakPasswordError(weak, passwordLocation)
	}
	return limitError(err)
}
