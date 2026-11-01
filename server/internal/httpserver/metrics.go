package httpserver

import (
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
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

// observe counts a request that the route answered with status after
// elapsed. A method that no route takes (api.Methods) is labelled "other",
// for the same reason as an unmatched path. A request that switched
// protocols, as a WebSocket of the real-time channel does, is counted, but
// its duration, which is the socket's life, minutes or hours of it, rather
// than how long the server took to answer, is not observed, so that it does
// not skew the latency of the requests.
func (m *metrics) observe(method, route string, status int, elapsed time.Duration) {
	if route == "" {
		route = unmatchedRoute
	}
	if !slices.Contains(api.Methods, method) {
		method = "other"
	}
	m.requests.WithLabelValues(route, method, strconv.Itoa(status)).Inc()
	if status != http.StatusSwitchingProtocols {
		m.duration.WithLabelValues(route, method).Observe(elapsed.Seconds())
	}
}
