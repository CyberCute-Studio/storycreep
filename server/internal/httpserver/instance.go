package httpserver

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"golang.org/x/text/language"

	"github.com/CyberCute-Studio/storycreep/server/internal/account"
	"github.com/CyberCute-Studio/storycreep/server/internal/api"
)

// instanceBody is the body of GET /api/v1/instance.
type instanceBody struct {
	EmailConfigured bool   `json:"email_configured" doc:"Whether the instance sends email, through the SMTP server of STORYCREEP_SMTP_URL or of first-run setup. Without it, a page says that email is not configured: the links that reset a password and verify an email address are not emailed, and an instance admin gives them out as text."`
	SetupPending    bool   `json:"setup_pending" doc:"Whether the instance's first-run setup is pending, until which nobody signs in or registers, and a page leads to the setup screen, which GET /api/v1/setup tells what to ask for (PRD FR-ADMIN-02)"`
	SignupPolicy    string `json:"signup_policy" enum:"closed,invite,open" doc:"Who can register an account once setup has completed: closed, no one; invite, only with an invitation's link, with POST /api/v1/invites/register; or open, anyone, with POST /api/v1/auth/register. It is the policy that STORYCREEP_SIGNUP sets, or else the one that first-run setup chose, and closed while setup is pending. A page offers registration only where it lets anyone register."`
	DefaultLocale   string `json:"default_locale" doc:"The instance's default locale, as a BCP 47 tag: the locale with a catalog that STORYCREEP_DEFAULT_LOCALE asks for, as en for en-GB, which a page is shown in when neither the user's preferred locale nor the browser's languages match a catalog of the page's (PRD FR-I18N-02)"`
}

type instanceOutput struct {
	Body instanceBody
}

// registerInstance registers GET /api/v1/instance, which tells a page what
// the instance offers before anyone signs in: whether it sends email (PRD
// FR-AUTH-03), whether its first-run setup is pending (FR-ADMIN-02), who can
// register (FR-AUTH-01), and its default locale (FR-I18N-02). Each is public
// already, as other routes answer them to anyone: GET /api/v1/setup whether
// setup is pending, and registering which policy refuses it.
func registerInstance(m *api.Mux, accounts *account.Service, defaultLocale language.Tag) {
	api.Register(m, api.Public, huma.Operation{
		OperationID: "getInstance",
		Method:      http.MethodGet,
		Path:        "/api/v1/instance",
		Summary:     "Get what the instance offers",
		Description: "Says whether the instance sends email, so that a page can say that email is not " +
			"configured, and where the links that reset a password and verify an email address come from; " +
			"whether its first-run setup is pending; who can register an account; and the instance's default " +
			"locale.",
		Tags: []string{"Instance"},
	}, func(ctx context.Context, _ *struct{}) (*instanceOutput, error) {
		configured, err := accounts.EmailConfigured(ctx)
		if err != nil {
			return nil, err
		}
		policy, completed, err := accounts.Signup(ctx)
		if err != nil {
			return nil, err
		}
		return &instanceOutput{Body: instanceBody{EmailConfigured: configured, SetupPending: !completed,
			SignupPolicy: string(policy), DefaultLocale: defaultLocale.String()}}, nil
	})
}
