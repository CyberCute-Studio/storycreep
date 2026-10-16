package api

// Capability is what a caller must hold to use a route (PRD NFR-SEC-02).
// Every route declares one when it is registered, and a test walks the
// routers and fails on a route that did not. The capabilities of PRD §5.6
// join as the routes that need them are added (plan item 20).
type Capability string

const (
	// Public routes need no credentials: the health and version endpoints,
	// registering an account, and signing in.
	Public Capability = "public"
	// ScrapeMetrics is reading /metrics. Whether it needs a credential
	// depends on the MetricsEndpoint capability of the configuration.
	ScrapeMetrics Capability = "metrics.scrape"
	// OwnAccount is the caller's own account: who is signed in, the
	// account's sessions and password, and signing out. It needs a signed-in
	// caller, whom the Mux's Authenticator finds.
	OwnAccount Capability = "account.own"
)

// capabilities are the declared capabilities, each with whether it needs an
// authenticated caller (Authenticated).
var capabilities = map[Capability]bool{
	Public:        false,
	ScrapeMetrics: false,
	OwnAccount:    true,
}

// Known reports whether c is a declared capability.
func (c Capability) Known() bool {
	_, ok := capabilities[c]
	return ok
}

// Authenticated reports whether a route that requires c needs an
// authenticated caller, whom the Mux's Authenticator finds before the route's
// operation runs (Register). ScrapeMetrics does not: /metrics checks its
// bearer token itself.
func (c Capability) Authenticated() bool {
	return capabilities[c]
}
