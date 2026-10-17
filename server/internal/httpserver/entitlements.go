package httpserver

import (
	"errors"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/i18n"
)

// pricingPath is the public site's page that offers the plans (PRD
// FR-PUB-07), which an answer of limit_exceeded links to where upgrading the
// workspace's plan would raise the limit.
const pricingPath = "pricing"

// entitlementOptions returns the options of the server's entitlements (PRD
// FR-ENT-01) under cfg: each workspace's plan while the EnforceEntitlements
// capability is on, and the unlimited plan otherwise, capped by
// STORYCREEP_WORKSPACE_LIMITS. An answer of limit_exceeded links to the
// pricing page only where the instance bills for plans and serves that page,
// so a self-hosted instance never offers an upgrade (FR-ENT-03 AC2). No
// enforcement site checks a limit yet, so the options have no meter: usage
// metering (FR-ENT-02) adds one with the first.
func entitlementOptions(cfg *config.Config) entitlements.Options {
	opts := entitlements.Options{
		Enforce: cfg.Capabilities.EnforceEntitlements,
		Caps:    cfg.WorkspaceLimits,
	}
	if cfg.Capabilities.Billing && cfg.Capabilities.PublicSite {
		opts.UpgradeURL = cfg.PublicURL.URL().JoinPath(pricingPath).String()
	}
	return opts
}

// limitExceededError returns the error that the API answers err with if err
// is an *entitlements.LimitError, or err itself otherwise: 409
// limit_exceeded, whose details name the limit, as key, and give the
// workspace's usage and limit (FR-ENT-03 AC1), and the upgrade link, as
// upgrade_url, if the error has one. It is the only code that answers with
// limit_exceeded, so every such answer carries them; the error mapper of a
// route whose operation checks a limit calls it.
func limitExceededError(err error) error {
	var limit *entitlements.LimitError
	if !errors.As(err, &limit) {
		return err
	}
	details := map[string]any{"key": limit.Key, "usage": limit.Usage, "limit": limit.Limit}
	if limit.UpgradeURL != "" {
		details["upgrade_url"] = limit.UpgradeURL
	}
	return &api.Error{
		Code: api.CodeLimitExceeded,
		Message: i18n.Message{Key: i18n.K("error.limit_exceeded.key"),
			Args: i18n.Args{"key": string(limit.Key), "limit": limit.Limit}},
		Details: details,
	}
}
