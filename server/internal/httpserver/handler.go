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

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/version"
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
}

func newServer(cfg *config.Config, log *slog.Logger) *server {
	s := &server{
		log:     log,
		ready:   &readiness{log: log},
		metrics: newMetrics(),
	}
	s.main = api.NewMux(api.MuxOptions{
		Log: log,
		Middleware: []func(http.Handler) http.Handler{
			requestID,
			s.observe(s.metrics),
			s.recoverPanics,
			securityHeaders(cfg.PublicURL.HTTPS()),
			noindexApp,
			s.ready.gate,
		},
	})
	s.registerOperational(s.main)
	registerDocument(s.main)

	switch {
	case cfg.AdminAddr == "":
		s.metricsOff = "STORYCREEP_ADMIN_ADDR is off"
	case cfg.Capabilities.MetricsEndpoint == config.MetricsProtected && !cfg.MetricsToken.IsSet():
		s.metricsOff = "the MetricsEndpoint capability is protected, and STORYCREEP_METRICS_TOKEN is not set"
	default:
		s.admin = api.NewMux(api.MuxOptions{
			Log: log,
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
	Version    string `json:"version" doc:"The release version, or (devel) for a build from a checkout"`
	Commit     string `json:"commit,omitempty" doc:"The revision of the source the binary was built from"`
	CommitTime string `json:"commit_time,omitempty" doc:"The time of that revision, in RFC 3339"`
	GoVersion  string `json:"go_version" doc:"The version of Go the binary was built with"`
}

type versionOutput struct {
	Body versionBody
}

// registerOperational registers /healthz, /readyz, and /version (PRD
// FR-ADMIN-05).
func (s *server) registerOperational(m *api.Mux) {
	api.Register(m, api.Public, huma.Operation{
		OperationID: "getHealth",
		Method:      http.MethodGet,
		Path:        "/healthz",
		Summary:     "Report that the process is alive",
		Tags:        []string{"Operations"},
	}, func(context.Context, *struct{}) (*statusOutput, error) {
		return &statusOutput{Body: statusBody{Status: "ok"}}, nil
	})

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
	})

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
	})
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

// requireBearer passes on only the requests that carry token as their bearer
// token (RFC 6750), and answers the others with unauthenticated. With no
// token, it passes on every request.
func requireBearer(token []byte, next http.Handler) http.Handler {
	if len(token) == 0 {
		return next
	}
	want := sha256.Sum256(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, got, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") {
			w.Header().Set("WWW-Authenticate", `Bearer realm="metrics"`)
			api.WriteError(w, r, api.NewError(api.CodeUnauthenticated))
			return
		}
		// Comparing hashes takes the same time whatever the token's length.
		sum := sha256.Sum256([]byte(strings.TrimSpace(got)))
		if subtle.ConstantTimeCompare(sum[:], want[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="metrics", error="invalid_token"`)
			api.WriteError(w, r, api.NewError(api.CodeUnauthenticated))
			return
		}
		next.ServeHTTP(w, r)
	})
}
