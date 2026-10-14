package config

// Capabilities are the behaviours that differ between the deployment modes
// (PRD §10.3). They are resolved once, from STORYCREEP_MODE and the keys that
// refine it, and every mode-dependent code path reads them instead of the
// mode.
type Capabilities struct {
	// PublicSite serves the marketing pages and documentation
	// (STORYCREEP_PUBLIC_SITE).
	PublicSite bool
	// Signup is who can register an account (STORYCREEP_SIGNUP).
	Signup SignupPolicy
	// EnforceEntitlements applies plan limits. Without it, every workspace
	// has the unlimited plan.
	EnforceEntitlements bool
	// Billing turns on the billing integration.
	Billing bool
	// EmailRequired means outgoing email must be configured, so links are
	// never shown as copyable text instead.
	EmailRequired bool
	// BlobDriver is where assets and archives are stored.
	BlobDriver BlobDriver
	// MetricsEndpoint is how /metrics is exposed.
	MetricsEndpoint MetricsEndpoint
	// InstanceAdminUI is who can use the instance-admin area.
	InstanceAdminUI InstanceAdminUI
	// SSO is how single sign-on is offered.
	SSO SSO
}

// SignupPolicy is who can register an account.
type SignupPolicy string

const (
	SignupClosed SignupPolicy = "closed"
	SignupInvite SignupPolicy = "invite"
	SignupOpen   SignupPolicy = "open"
)

// BlobDriver is a blob storage backend.
type BlobDriver string

const (
	BlobLocal BlobDriver = "local"
	BlobS3    BlobDriver = "s3"
)

// MetricsEndpoint is how /metrics is exposed.
type MetricsEndpoint string

const (
	// MetricsUnauthenticated serves metrics without authentication, on the
	// admin port.
	MetricsUnauthenticated MetricsEndpoint = "unauthenticated"
	// MetricsProtected serves metrics only to authorised scrapers.
	MetricsProtected MetricsEndpoint = "protected"
)

// InstanceAdminUI is who can use the instance-admin area.
type InstanceAdminUI string

const (
	// InstanceAdminOn opens the area to the instance's administrators.
	InstanceAdminOn InstanceAdminUI = "on"
	// InstanceAdminOperatorOnly restricts it to operator accounts.
	InstanceAdminOperatorOnly InstanceAdminUI = "operator-only"
)

// SSO is how single sign-on is offered.
type SSO string

const (
	SSOOff         SSO = "off"
	SSOPlanFeature SSO = "plan-feature"
)

// modeCapabilities returns the capabilities of a mode before any key refines
// them. This is the table of PRD §10.3.
func modeCapabilities(m mode) Capabilities {
	if m == modeHosted {
		return Capabilities{
			PublicSite:          true,
			Signup:              SignupOpen,
			EnforceEntitlements: true,
			Billing:             true,
			EmailRequired:       true,
			BlobDriver:          BlobS3,
			MetricsEndpoint:     MetricsProtected,
			InstanceAdminUI:     InstanceAdminOperatorOnly,
			SSO:                 SSOPlanFeature,
		}
	}
	return Capabilities{
		PublicSite:          false,
		Signup:              SignupClosed,
		EnforceEntitlements: false,
		Billing:             false,
		EmailRequired:       false,
		BlobDriver:          BlobLocal,
		MetricsEndpoint:     MetricsUnauthenticated,
		InstanceAdminUI:     InstanceAdminOn,
		SSO:                 SSOOff,
	}
}

// Capability is one capability's name and value, for display.
type Capability struct {
	Name  string
	Value string
}

// Describe lists the capabilities in the order of PRD §10.3.
func (c Capabilities) Describe() []Capability {
	return []Capability{
		{"PublicSite", onOff(c.PublicSite)},
		{"Signup", string(c.Signup)},
		{"EnforceEntitlements", onOff(c.EnforceEntitlements)},
		{"Billing", onOff(c.Billing)},
		{"EmailRequired", onOff(c.EmailRequired)},
		{"BlobDriver", string(c.BlobDriver)},
		{"MetricsEndpoint", string(c.MetricsEndpoint)},
		{"InstanceAdminUI", string(c.InstanceAdminUI)},
		{"SSO", string(c.SSO)},
	}
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
