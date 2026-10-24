package httpserver

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/danielgtaylor/huma/v2"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/CyberCute-Studio/storycreep/server/internal/account"
	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/apitoken"
	"github.com/CyberCute-Studio/storycreep/server/internal/asset"
	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/component"
	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/connection"
	"github.com/CyberCute-Studio/storycreep/server/internal/email"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/invite"
	"github.com/CyberCute-Studio/storycreep/server/internal/node"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/ratelimit"
	"github.com/CyberCute-Studio/storycreep/server/internal/role"
	"github.com/CyberCute-Studio/storycreep/server/internal/setup"
	"github.com/CyberCute-Studio/storycreep/server/internal/trash"
	"github.com/CyberCute-Studio/storycreep/server/internal/variable"
	"github.com/CyberCute-Studio/storycreep/server/internal/version"
	"github.com/CyberCute-Studio/storycreep/server/internal/workspace"
)

// server holds the handlers of the two listeners.
type server struct {
	log     *slog.Logger
	ready   *readiness
	metrics *metrics
	// main serves everything but /metrics, and admin serves /metrics. admin
	// is nil if there are no metrics to serve, and metricsOff says why.
	main, admin *api.Mux
	metricsOff  string
	// accounts keeps the accounts and their sessions.
	accounts *account.Service
	// entitlements answers what the workspaces' plans allow.
	entitlements *entitlements.Service
	// workspaces keeps the workspaces.
	workspaces *workspace.Service
	// invites keeps the workspaces' invitations.
	invites *invite.Service
	// commands applies the batches of commands that change the projects'
	// content (package commands).
	commands *commands.Pipeline
	// projects keeps the projects.
	projects *project.Service
	// boards reads the projects' boards and folders, which their commands
	// change (package board).
	boards *board.Service
	// variables reads the projects' variables, which their commands change
	// (package variable).
	variables *variable.Service
	// components reads the projects' component types and components, which
	// their commands change (package component).
	components *component.Service
	// nodes reads the projects' nodes, which their commands change (package
	// node).
	nodes *node.Service
	// connections reads the projects' connections, which their commands
	// change (package connection).
	connections *connection.Service
	// trash lists the projects' trash, from which their commands restore
	// what it holds (package trash).
	trash *trash.Service
	// assets keeps the projects' assets, and their files.
	assets *asset.Service
	// tokens keeps the API tokens, and authenticates the requests made with
	// them.
	tokens *apitoken.Service
	// setups completes first-run setup.
	setups *setup.Service
	// unattended is the configuration's unattended first-run setup, or nil
	// if it has none.
	unattended *config.UnattendedSetup
	// limits counts the requests of the rate limits.
	limits *ratelimit.Limiter
	// email sends the instance's email.
	email *email.Service
	// queue is the job queue, once Run has opened it.
	queue *queueRef
}

func newServer(cfg *config.Config, log *slog.Logger) *server {
	s := &server{
		log:     log,
		ready:   &readiness{log: log},
		metrics: newMetrics(),
	}
	s.entitlements = entitlements.New(EntitlementOptions(cfg))
	s.workspaces = workspace.New(readyDB{s.ready}, workspace.Options{
		Policy:        role.Policy{InstanceAdminAccess: cfg.Capabilities.InstanceAdminAccess},
		Entitlements:  s.entitlements,
		PlanKey:       s.entitlements.NewWorkspacePlan(),
		DefaultLocale: cfg.DefaultLocale,
		// An instance that always sends email can require a verified
		// address (FR-AUTH-03).
		RequireVerifiedEmail: cfg.Capabilities.EmailRequired,
	})
	registry := commands.NewRegistry()
	project.Register(registry)
	board.Register(registry)
	variable.Register(registry)
	component.Register(registry)
	node.Register(registry)
	connection.Register(registry)
	s.queue = &queueRef{}
	s.commands = commands.New(readyDB{s.ready}, commands.Options{
		Registry:     registry,
		Policy:       role.Policy{InstanceAdminAccess: cfg.Capabilities.InstanceAdminAccess},
		Entitlements: s.entitlements,
		Jobs:         s.queue,
	})
	s.metrics.registry.MustRegister(s.commands.Collector())
	s.projects = project.New(readyDB{s.ready}, project.Options{
		Policy:       role.Policy{InstanceAdminAccess: cfg.Capabilities.InstanceAdminAccess},
		Entitlements: s.entitlements,
		Pipeline:     s.commands,
		Seed:         component.Seed,
	})
	s.boards = board.New(readyDB{s.ready}, board.Options{Projects: s.projects})
	s.variables = variable.New(readyDB{s.ready}, variable.Options{Projects: s.projects})
	s.components = component.New(readyDB{s.ready}, component.Options{Projects: s.projects})
	s.nodes = node.New(readyDB{s.ready}, node.Options{Projects: s.projects})
	s.connections = connection.New(readyDB{s.ready}, connection.Options{Projects: s.projects})
	s.trash = trash.New(readyDB{s.ready}, trash.Options{Projects: s.projects})
	s.assets = asset.New(readyDB{s.ready}, asset.Options{
		Projects:     s.projects,
		Entitlements: s.entitlements,
		Storage:      asset.NewStorage(readyDB{s.ready}, asset.StorageOptions{Configured: cfg.AssetStorage}),
		Queue:        s.queue,
		MaxBytes:     cfg.AssetMaxBytes,
		Log:          log,
	})
	s.tokens = apitoken.New(readyDB{s.ready}, apitoken.Options{
		Policy: role.Policy{InstanceAdminAccess: cfg.Capabilities.InstanceAdminAccess},
	})
	s.limits = ratelimit.New(readyDB{s.ready}, ratelimit.DefaultPolicy())
	s.email = email.New(readyDB{s.ready}, email.Options{SMTP: cfg.SMTP, Hostname: cfg.PublicURL.URL().Hostname()})
	s.accounts = account.New(readyDB{s.ready}, account.Options{
		Signup:      cfg.Capabilities.Signup,
		IdleTimeout: cfg.SessionIdleTimeout,
		Lifetime:    cfg.SessionLifetime,
		Limits:      s.limits,
		// The first sign-in gives the account its personal workspace
		// (FR-TEN-01).
		Authenticated: s.workspaces.EnsurePersonal,
		Email:         s.email,
		Queue:         s.queue,
		PublicURL:     cfg.PublicURL,
		// Where a workspace needs a verified address, the verification gives
		// the account the personal workspace that its first sign-in could
		// not. The address is verified, and the link used, by then, so a
		// failure to give the workspace is logged rather than answered, which
		// would tell the user that the link failed when a second use of it
		// can only be refused, and the account's next sign-in gives the
		// workspace instead.
		Verified: func(ctx context.Context, userID ids.UUID, client account.Client) error {
			if err := s.workspaces.EmailVerified(ctx, userID, client); err != nil {
				s.log.ErrorContext(ctx, "the verified account's personal workspace could not be created, so its "+
					"next sign-in creates it", "error", err)
			}
			return nil
		},
	})
	s.invites = invite.New(readyDB{s.ready}, invite.Options{
		Workspaces: s.workspaces,
		Accounts:   s.accounts,
		Email:      s.email,
		Queue:      s.queue,
		Limits:     s.limits,
		PublicURL:  cfg.PublicURL,
	})
	s.setups = setup.New(readyDB{s.ready}, setup.Options{
		Signup:        cfg.Capabilities.Signup,
		SMTPFixed:     s.email.Fixed(),
		StorageFixed:  s.assets.Storage().Fixed(),
		DefaultLocale: cfg.DefaultLocale,
		Workspaces:    s.workspaces,
		Hasher:        s.accounts.Hasher(),
	})
	s.unattended = cfg.Setup
	cookie := newSessionCookie(cfg.PublicURL)
	s.main = api.NewMux(api.MuxOptions{
		Log:           log,
		DefaultLocale: cfg.DefaultLocale,
		Middleware: []func(http.Handler) http.Handler{
			requestID,
			s.observe(s.metrics),
			s.recoverPanics,
			securityHeaders(cfg.PublicURL.HTTPS()),
			noindexApp,
			refuseCrossSite,
			s.ready.gate,
			withClient(cfg.TrustedProxies),
		},
		Authenticate: authenticator(s.accounts, s.tokens, cookie),
		RateLimit:    limiter{s},
	})
	s.registerOperational(s.main)
	registerAccounts(s.main, s.accounts)
	registerSessions(s.main, s.accounts, cookie)
	registerWorkspaces(s.main, s.workspaces)
	registerUsage(s.main, s.workspaces)
	registerProjects(s.main, s.projects)
	registerCommands(s.main, s.commands)
	registerBoards(s.main, s.boards)
	registerVariables(s.main, s.variables)
	registerComponents(s.main, s.components)
	registerNodes(s.main, s.nodes)
	registerConnections(s.main, s.connections)
	registerTrash(s.main, s.trash)
	registerAssets(s.main, s.assets)
	registerGrants(s.main, s.projects)
	registerTokens(s.main, s.tokens)
	registerInvites(s.main, s.workspaces, s.invites, s.accounts)
	registerSetup(s.main, s.setups)
	registerEmail(s.main, s.accounts)
	registerDocument(s.main)
	registerPages(s.main, cfg.Capabilities.PublicSite)

	switch {
	case cfg.AdminAddr == "":
		s.metricsOff = "STORYCREEP_ADMIN_ADDR is off"
	case cfg.Capabilities.MetricsEndpoint == config.MetricsProtected && !cfg.MetricsToken.IsSet():
		s.metricsOff = "the MetricsEndpoint capability is protected, and STORYCREEP_METRICS_TOKEN is not set"
	default:
		s.admin = api.NewMux(api.MuxOptions{
			Log:           log,
			DefaultLocale: cfg.DefaultLocale,
			Middleware: []func(http.Handler) http.Handler{
				requestID,
				s.observe(nil),
				s.recoverPanics,
				securityHeaders(false),
			},
		})
		var token []byte
		if cfg.Capabilities.MetricsEndpoint == config.MetricsProtected {
			token = []byte(cfg.MetricsToken.Reveal())
		}
		s.admin.Handle(api.ScrapeMetrics, http.MethodGet, "/metrics", requireBearer(token,
			promhttp.HandlerFor(s.metrics.registry, promhttp.HandlerOpts{
				ErrorLog:      slog.NewLogLogger(log.Handler(), slog.LevelError),
				ErrorHandling: promhttp.ContinueOnError,
			})))
	}
	return s
}

// statusBody is the body of /healthz and /readyz.
type statusBody struct {
	Status string `json:"status" enum:"ok,ready" doc:"ok for /healthz, and ready for /readyz"`
}

type statusOutput struct {
	Body statusBody
}

// versionBody is the body of /version.
type versionBody struct {
	Version    string `json:"version" doc:"The release version, or (devel) for any other build, such as the main branch's image"`
	Commit     string `json:"commit,omitempty" doc:"The revision of the source the binary was built from"`
	CommitTime string `json:"commit_time,omitempty" doc:"The time of that revision, in RFC 3339"`
	GoVersion  string `json:"go_version" doc:"The version of Go the binary was built with"`
}

type versionOutput struct {
	Body versionBody
}

// registerOperational registers /healthz, /readyz, and /version (PRD
// FR-ADMIN-05), which no rate limit counts, because they answer without the
// database, in which the limits count: the probes must answer while it is
// being migrated, or does not answer.
func (s *server) registerOperational(m *api.Mux) {
	api.Register(m, api.Public, huma.Operation{
		OperationID: "getHealth",
		Method:      http.MethodGet,
		Path:        "/healthz",
		Summary:     "Report that the process is alive",
		Tags:        []string{"Operations"},
	}, func(context.Context, *struct{}) (*statusOutput, error) {
		return &statusOutput{Body: statusBody{Status: "ok"}}, nil
	}, api.Unlimited())

	api.Register(m, api.Public, huma.Operation{
		OperationID: "getReadiness",
		Method:      http.MethodGet,
		Path:        "/readyz",
		Summary:     "Report whether the server can serve",
		Description: "Answers 200 when the server has finished starting, the database has every migration " +
			"applied, and the database answers. Otherwise it answers 503 with not_ready, and details.reason " +
			"says why.",
		Tags: []string{"Operations"},
	}, func(ctx context.Context, _ *struct{}) (*statusOutput, error) {
		if err := s.ready.notReady(ctx, true); err != nil {
			return nil, err
		}
		return &statusOutput{Body: statusBody{Status: "ready"}}, nil
	}, api.Unlimited())

	api.Register(m, api.Public, huma.Operation{
		OperationID: "getVersion",
		Method:      http.MethodGet,
		Path:        "/version",
		Summary:     "Describe the server's build",
		Tags:        []string{"Operations"},
	}, func(context.Context, *struct{}) (*versionOutput, error) {
		info := version.Get()
		return &versionOutput{Body: versionBody{
			Version:    info.Version,
			Commit:     info.Commit,
			CommitTime: info.CommitTime,
			GoVersion:  info.GoVersion,
		}}, nil
	}, api.Unlimited())
}

// openAPIPath is where the server serves its OpenAPI document (PRD
// FR-API-02).
const openAPIPath = "/api/v1/openapi.json"

type documentInput struct {
	IfNoneMatch string `header:"If-None-Match" doc:"The ETag of the copy of the document that the client has"`
}

type documentOutput struct {
	ETag         string `header:"ETag" doc:"The document's entity tag, which If-None-Match takes"`
	CacheControl string `header:"Cache-Control" doc:"private, no-cache: a client may keep a copy, and revalidates it"`
	// ContentType is the type of Body, which huma writes as it is.
	ContentType string `header:"Content-Type" hidden:"true"`
	Body        []byte
}

// registerDocument registers GET /api/v1/openapi.json, which serves the
// OpenAPI document of m's operations, this one included, as Document renders
// it (PRD FR-API-02). The document is rendered at the first request, so m's
// operations are all registered by then, and an ETag lets a client keep a
// copy until the server changes.
func registerDocument(m *api.Mux) {
	type rendered struct {
		doc  []byte
		etag string
	}
	render := sync.OnceValues(func() (rendered, error) {
		doc, err := m.Document()
		return rendered{doc: doc, etag: api.ETag(doc)}, err
	})
	api.Register(m, api.Public, huma.Operation{
		OperationID: "getOpenAPIDocument",
		Method:      http.MethodGet,
		Path:        openAPIPath,
		Summary:     "Get the API's OpenAPI document",
		Description: "The OpenAPI 3.1 document of the server's operations, this one included. It is the same " +
			"for every server of a release, and is committed to the repository as schema/api/v1/openapi.json.",
		Tags: []string{"Operations"},
		Responses: map[string]*huma.Response{
			"200": {
				Description: "OK",
				Content: map[string]*huma.MediaType{"application/json": {
					Schema: &huma.Schema{Type: huma.TypeObject, Description: "An OpenAPI 3.1 document"},
				}},
			},
			"304": api.NotModifiedResponse(),
		},
	}, func(_ context.Context, in *documentInput) (*documentOutput, error) {
		r, err := render()
		if err != nil {
			return nil, err
		}
		if api.IfNoneMatch(in.IfNoneMatch, r.etag) {
			return nil, api.NotModified(r.etag)
		}
		return &documentOutput{ETag: r.etag, CacheControl: api.CacheRevalidate, ContentType: "application/json",
			Body: r.doc}, nil
	})
}

// bearerCredential returns the credential of the Bearer scheme (RFC 6750
// §2.1) that r's Authorization header holds, without the white space around
// it, and false if the header holds none, as when it names another scheme.
func bearerCredential(r *http.Request) (string, bool) {
	scheme, credential, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	return strings.TrimSpace(credential), true
}

// requireBearer passes on only the requests that carry token as their bearer
// token (RFC 6750), and answers the others with unauthenticated. With no
// token, it passes on every request.
func requireBearer(token []byte, next http.Handler) http.Handler {
	if len(token) == 0 {
		return next
	}
	want := sha256.Sum256(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := bearerCredential(r)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="metrics"`)
			api.WriteError(w, r, api.NewError(api.CodeUnauthenticated))
			return
		}
		// Comparing hashes takes the same time whatever the token's length.
		sum := sha256.Sum256([]byte(got))
		if subtle.ConstantTimeCompare(sum[:], want[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="metrics", error="invalid_token"`)
			api.WriteError(w, r, api.NewError(api.CodeUnauthenticated))
			return
		}
		next.ServeHTTP(w, r)
	})
}
