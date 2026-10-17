// Package entitlements decides what a workspace's plan allows: how much of
// each limit it may use, and which features it has (PRD FR-ENT-01,
// FR-ENT-03, ADR-010).
//
// The plans live in one declarative file, plans.json, which the server
// embeds, and which the pricing page and the tests read too (FR-ENT-01 AC3).
// Its limits are members, which hosted mode bills by, projects,
// storage_bytes, and snapshot_retention, which no code checks until FR-SNAP-06
// (AC1). Its feature flags are collaboration, sso, and audit_export (AC4).
// Boards, nodes, and API requests are metered (FR-ENT-02), but no plan limits
// them (ADR-010). The numbers in the file are placeholders, and nothing else
// names them, so setting them needs no change of code.
//
// While entitlements are enforced, as in hosted mode, a workspace has the
// plan that its row names. Otherwise, as in self-hosted mode, every
// workspace has the unlimited plan, which limits nothing and has every
// feature (AC2), and the instance may cap the limits of every workspace
// instead (Caps).
//
// Every site that enforces a limit calls Check once, in the transaction of
// the change that would add to the workspace's usage, and gets a *LimitError
// when the change would take the workspace past its quota, which the API
// answers with 409 limit_exceeded (FR-ENT-03). The site is the only one for
// its limit (FR-ENT-01 AC1).
package entitlements

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// ErrInert is returned for a limit that the plan file marks inert, which no
// code checks until the requirement that enforces it lands (FR-ENT-01 AC1).
var ErrInert = errors.New("entitlements: the limit is inert, so nothing may check it")

// LimitError is the error of a change that would take a workspace past its
// quota of a limit (FR-ENT-03). The API answers it with 409 limit_exceeded.
type LimitError struct {
	Key LimitKey
	// Usage is how much of the limit the workspace used before the change,
	// and Limit the most that it may use.
	Usage, Limit int64
	// UpgradeURL is the page that offers the plans, where the instance bills
	// for them and another plan allows more of the limit, or "" if
	// upgrading would not raise it, as in self-hosted mode, which never
	// offers an upgrade (FR-ENT-03 AC2).
	UpgradeURL string
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("entitlements: the change would take the workspace past its limit of %d %s, of which it uses "+
		"%d", e.Limit, e.Key, e.Usage)
}

// A Meter reads how much of a limit a workspace uses (FR-ENT-02), in the
// transaction of the change that would add to it. The transaction alone does
// not keep two changes that run at once from both passing Check against the
// same usage, so for a limit that FR-ENT-02 counts exactly, as members and
// projects, Usage also serializes the workspace's changes of that limit until
// the transaction ends, as with a per-workspace advisory lock. A limit that
// it counts approximately, storage_bytes, may be passed by the changes that
// are in flight at once.
type Meter interface {
	Usage(ctx context.Context, tx pgx.Tx, workspaceID ids.UUID, key LimitKey) (int64, error)
}

// Usage is how much of a limit a workspace uses, against its quota.
type Usage struct {
	Key   LimitKey
	Used  int64
	Quota Quota
	// Warning is set when the workspace uses at least 80% of a quota that is
	// a number, which is the soft warning of FR-ENT-03.
	Warning bool
}

// Options configure a Service.
type Options struct {
	// Enforce gives each workspace the plan that its row names, as the
	// EnforceEntitlements capability does. Without it, every workspace has
	// the unlimited plan.
	Enforce bool
	// Caps cap the limits of every workspace, whatever its plan allows.
	Caps Caps
	// UpgradeURL is the page that offers the plans, which a LimitError links
	// to where another plan would raise the limit, or "" for none, as where
	// the instance does not bill for plans (FR-ENT-03 AC2).
	UpgradeURL string
	// Meter reads the workspaces' usage. Without one, Usage fails for every
	// limit, and Check for a limit that has a quota.
	Meter Meter
	// Plans are the plans, or nil for plans.json's (Default).
	Plans *PlanFile
}

// Service answers what the workspaces' plans allow.
type Service struct {
	enforce bool
	caps    Caps
	upgrade string
	meter   Meter
	plans   *PlanFile
}

// New returns a Service with opts.
func New(opts Options) *Service {
	plans := opts.Plans
	if plans == nil {
		plans = Default()
	}
	return &Service{enforce: opts.Enforce, caps: opts.Caps, upgrade: opts.UpgradeURL, meter: opts.Meter,
		plans: plans}
}

// NewWorkspacePlan returns the plan of a new workspace: the free plan while
// entitlements are enforced, and the unlimited plan otherwise.
func (s *Service) NewWorkspacePlan() string {
	if s.enforce {
		return Free
	}
	return Unlimited
}

// plan returns the plan of the workspace, which the transaction tx reads.
func (s *Service) plan(ctx context.Context, tx pgx.Tx, workspaceID ids.UUID) (Plan, error) {
	if !s.enforce {
		p, _ := s.plans.Plan(Unlimited)
		return p, nil
	}
	key, err := queries.New(tx).WorkspacePlan(ctx, pgtype.UUID{Bytes: workspaceID, Valid: true})
	if err != nil {
		return Plan{}, fmt.Errorf("entitlements: read the plan of workspace %s: %w", workspaceID, err)
	}
	p, ok := s.plans.Plan(key)
	if !ok {
		return Plan{}, fmt.Errorf("entitlements: workspace %s has the plan %q, which the plan file does not define",
			workspaceID, key)
	}
	return p, nil
}

// quota returns the workspace's quota of key: its plan's, or the instance's
// cap of key if that is smaller. fromPlan says whether the plan's quota is
// the one that applies, which upgrading the plan could raise.
func (s *Service) quota(ctx context.Context, tx pgx.Tx, workspaceID ids.UUID, key LimitKey) (q Quota,
	fromPlan bool, err error) {
	if err := s.checkKey(key); err != nil {
		return Quota{}, false, err
	}
	p, err := s.plan(ctx, tx, workspaceID)
	if err != nil {
		return Quota{}, false, err
	}
	q = p.Quota(key)
	if n, ok := s.caps[key]; ok {
		capped := q.min(AtMost(n))
		return capped, capped == q && q != AtMost(n), nil
	}
	return q, true, nil
}

// checkKey returns ErrInert for an inert limit, and an error for a key that
// names no limit of the plan file.
func (s *Service) checkKey(key LimitKey) error {
	l, ok := s.plans.limit(key)
	switch {
	case !ok:
		return fmt.Errorf("entitlements: %q names no limit of the plan file", key)
	case l.InertUntil != "":
		return fmt.Errorf("%w: %s, until %s", ErrInert, key, l.InertUntil)
	}
	return nil
}

// Limit returns the workspace's quota of key, which the transaction tx
// reads: its plan's, or the instance's cap of key if that is smaller.
func (s *Service) Limit(ctx context.Context, tx pgx.Tx, workspaceID ids.UUID, key LimitKey) (Quota, error) {
	q, _, err := s.quota(ctx, tx, workspaceID, key)
	return q, err
}

// Enabled reports whether the workspace's plan, which the transaction tx
// reads, has the feature.
func (s *Service) Enabled(ctx context.Context, tx pgx.Tx, workspaceID ids.UUID, f Feature) (bool, error) {
	p, err := s.plan(ctx, tx, workspaceID)
	if err != nil {
		return false, err
	}
	if _, ok := p.features[f]; !ok {
		return false, fmt.Errorf("entitlements: %q names no feature of the plan file", f)
	}
	return p.Has(f), nil
}

// Usage returns how much of key the workspace uses, as the Service's Meter
// reads it in the transaction tx, against its quota, and whether it uses
// enough of the quota for the soft warning.
func (s *Service) Usage(ctx context.Context, tx pgx.Tx, workspaceID ids.UUID, key LimitKey) (Usage, error) {
	q, _, err := s.quota(ctx, tx, workspaceID, key)
	if err != nil {
		return Usage{}, err
	}
	used, err := s.used(ctx, tx, workspaceID, key)
	if err != nil {
		return Usage{}, err
	}
	return Usage{Key: key, Used: used, Quota: q, Warning: q.Warns(used)}, nil
}

// Check returns nil if the workspace may add adding to its usage of key, in
// the transaction tx of the change that adds it, and a *LimitError if that
// would take the workspace past its quota of key (FR-ENT-03). A quota of any
// amount is not checked against usage, so it costs no read of the Meter.
func (s *Service) Check(ctx context.Context, tx pgx.Tx, workspaceID ids.UUID, key LimitKey, adding int64) error {
	q, fromPlan, err := s.quota(ctx, tx, workspaceID, key)
	if err != nil {
		return err
	}
	limit, limited := q.Max()
	if !limited || adding <= 0 {
		return nil
	}
	used, err := s.used(ctx, tx, workspaceID, key)
	if err != nil {
		return err
	}
	if q.Allows(used, adding) {
		return nil
	}
	e := &LimitError{Key: key, Usage: used, Limit: limit}
	if fromPlan && s.raisable(key, q) {
		e.UpgradeURL = s.upgrade
	}
	return e
}

// used reads the workspace's usage of key with the Meter.
func (s *Service) used(ctx context.Context, tx pgx.Tx, workspaceID ids.UUID, key LimitKey) (int64, error) {
	if s.meter == nil {
		return 0, fmt.Errorf("entitlements: no meter reads the usage of %s", key)
	}
	used, err := s.meter.Usage(ctx, tx, workspaceID, key)
	if err != nil {
		return 0, fmt.Errorf("entitlements: read the usage of %s: %w", key, err)
	}
	return used, nil
}

// raisable reports whether a plan that a workspace can be given allows more
// of key than q. The unlimited plan is not one: it is the plan of a workspace
// whose instance enforces no plans.
func (s *Service) raisable(key LimitKey, q Quota) bool {
	if s.upgrade == "" {
		return false
	}
	for _, p := range s.plans.plans {
		if p.Key != Unlimited && p.Quota(key).min(q) == q && p.Quota(key) != q {
			return true
		}
	}
	return false
}
