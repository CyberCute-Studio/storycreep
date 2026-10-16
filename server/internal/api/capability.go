package api

import "slices"

// Capability is what a caller must hold to use a route (PRD NFR-SEC-02).
// Every route declares one when it is registered, and a test walks the
// routers and fails on a route that did not. The capabilities of PRD §5.6
// join as the routes that need them are added (plan item 20).
type Capability string

const (
	// Public routes need no credentials: the health and version endpoints,
	// registering an account, and, later, signing in.
	Public Capability = "public"
	// ScrapeMetrics is reading /metrics. Whether it needs a credential
	// depends on the MetricsEndpoint capability of the configuration.
	ScrapeMetrics Capability = "metrics.scrape"
)

// capabilities are the declared capabilities.
var capabilities = []Capability{Public, ScrapeMetrics}

// Known reports whether c is a declared capability.
func (c Capability) Known() bool {
	return slices.Contains(capabilities, c)
}
