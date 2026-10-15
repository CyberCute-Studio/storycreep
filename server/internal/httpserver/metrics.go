package httpserver

import (
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// metrics are the server's Prometheus metrics (PRD FR-ADMIN-05). Plan item
// 115 adds the rest of NFR-OBS-02.
type metrics struct {
	registry *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

func newMetrics() *metrics {
	m := &metrics{
		registry: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "storycreep_http_requests_total",
			Help: "HTTP requests answered, by route, method, and status.",
		}, []string{"route", "method", "code"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "storycreep_http_request_duration_seconds",
			Help:    "How long HTTP requests took to answer, by route and method.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route", "method"}),
	}
	m.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.requests,
		m.duration,
	)
	return m
}

// unmatchedRoute is the route label of the requests that no route matched.
// Labelling them by path would let any client add series without end.
const unmatchedRoute = "unmatched"

// labelMethods are the methods that are their own label value. Any other is
// "other", for the same reason.
var labelMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
	http.MethodOptions,
}

// observe counts a request that the route answered with status after
// elapsed.
func (m *metrics) observe(method, route string, status int, elapsed time.Duration) {
	if route == "" {
		route = unmatchedRoute
	}
	if !slices.Contains(labelMethods, method) {
		method = "other"
	}
	m.requests.WithLabelValues(route, method, strconv.Itoa(status)).Inc()
	m.duration.WithLabelValues(route, method).Observe(elapsed.Seconds())
}
