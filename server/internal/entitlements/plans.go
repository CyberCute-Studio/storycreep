package entitlements

import (
	"bytes"
	"cmp"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// planFile is the plan file, plans.json: the one declarative file that the
// plans live in, which the server, the pricing page, and the tests read
// (PRD FR-ENT-01 AC3). Nothing else names the numbers in it (AC4).
//
//go:embed plans.json
var planFile []byte

// LimitKey names a limit of the plans, as the plan file does.
type LimitKey string

// The limits that the server checks. The plan file's other limit,
// snapshot_retention, has no constant: it is inert until the requirement
// that enforces it lands (FR-SNAP-06), and Limit, Usage, and Check refuse it
// (FR-ENT-01 AC1).
const (
	// Members is how many members a workspace has. It is what hosted mode
	// bills by (ADR-010).
	Members LimitKey = "members"
	// Projects is how many projects a workspace has.
	Projects LimitKey = "projects"
	// StorageBytes is how many bytes a workspace's stored files take up.
	StorageBytes LimitKey = "storage_bytes"
)

// limitKeys are the limits that the server checks, which are the plan
// file's limits that are not inert.
var limitKeys = []LimitKey{Members, Projects, StorageBytes}

// LimitKeys returns the limits that the server checks, which are the plan
// file's limits that are not inert.
func LimitKeys() []LimitKey {
	return slices.Clone(limitKeys)
}

// Feature names a feature flag of the plans, as the plan file does.
type Feature string

// The feature flags (ADR-010).
const (
	// Collaboration is working on a project together in real time.
	Collaboration Feature = "collaboration"
	// SSO is signing in with the single sign-on of an identity provider.
	SSO Feature = "sso"
	// AuditExport is exporting the workspace's audit log.
	AuditExport Feature = "audit_export"
)

// features are the feature flags that the plan file must declare.
var features = []Feature{Collaboration, SSO, AuditExport}

// The plans that the server names itself.
const (
	// Unlimited is the plan of every workspace while entitlements are not
	// enforced, as in self-hosted mode: it limits nothing and has every
	// feature (FR-ENT-01 AC2).
	Unlimited = "unlimited"
	// Free is the plan of a new workspace while they are.
	Free = "free"
)

// maxQuota is the largest number that the plan file or a cap may give a
// limit, 2^53 - 1: the largest whole number that every JSON client, as a
// JavaScript one, reads exactly, so that the limit that an answer of
// limit_exceeded carries is the one that applies.
const maxQuota int64 = 1<<53 - 1

// A Quota is how much of a limit a workspace may use: at most a number, or
// any amount.
type Quota struct {
	max       int64
	unlimited bool
}

// NoLimit is the Quota that allows any amount.
var NoLimit = Quota{unlimited: true}

// AtMost returns the Quota that allows at most n, which is at least 0.
func AtMost(n int64) Quota {
	return Quota{max: max(n, 0)}
}

// Max returns the most that q allows, and false if q allows any amount.
func (q Quota) Max() (int64, bool) {
	return q.max, !q.unlimited
}

// Allows reports whether a workspace that uses used of a limit may add
// adding more under q. Adding nothing, or less, is always allowed, so a
// workspace past its quota, as after its plan changed, keeps what it has.
func (q Quota) Allows(used, adding int64) bool {
	if q.unlimited || adding <= 0 {
		return true
	}
	// Neither side overflows, because max and used are at least 0.
	return adding <= q.max-max(used, 0)
}

// Warns reports whether a workspace that uses used of a limit uses at least
// 80% of q, which is the soft warning of FR-ENT-03. A quota of any amount
// never warns.
func (q Quota) Warns(used int64) bool {
	// max - max/5 is 80% of max, rounded up, without the overflow of
	// used*5 >= max*4.
	return !q.unlimited && used >= q.max-q.max/5
}

// min returns the smaller of q and r.
func (q Quota) min(r Quota) Quota {
	switch {
	case q.unlimited:
		return r
	case r.unlimited || q.max <= r.max:
		return q
	}
	return r
}

// String returns q as the plan file writes it: a number, or unlimited.
func (q Quota) String() string {
	if q.unlimited {
		return Unlimited
	}
	return strconv.FormatInt(q.max, 10)
}

// UnmarshalJSON reads a limit of a plan: a whole number from 0 to maxQuota,
// or the string "unlimited".
func (q *Quota) UnmarshalJSON(b []byte) error {
	if string(b) == `"`+Unlimited+`"` {
		*q = NoLimit
		return nil
	}
	// A JSON number's only sign is a minus, which strconv accepts and n < 0
	// refuses; strconv refuses a fraction or an exponent itself.
	n, err := strconv.ParseInt(string(b), 10, 64)
	if err != nil || n < 0 || n > maxQuota {
		return fmt.Errorf(`a limit is a whole number from 0 to %d, or "%s", not %s`, maxQuota, Unlimited, b)
	}
	*q = AtMost(n)
	return nil
}

// A Plan is what a plan allows: a Quota of each limit, and whether it has
// each feature.
type Plan struct {
	Key      string
	limits   map[LimitKey]Quota
	features map[Feature]bool
}

// Quota returns the plan's quota of key, which is one of the plan file's
// limits, an inert one included.
func (p Plan) Quota(key LimitKey) Quota {
	return p.limits[key]
}

// Has reports whether the plan has the feature.
func (p Plan) Has(f Feature) bool {
	return p.features[f]
}

// A Limit is a limit that the plan file declares.
type Limit struct {
	Key LimitKey
	// Billable marks the limit that hosted mode bills by.
	Billable bool
	// InertUntil names the requirement that enforces the limit, while no
	// code checks it, or is "" for a limit that the server checks (FR-ENT-01
	// AC1).
	InertUntil string
}

// A PlanFile is a plan file's plans, and the limits and features that they
// define, in the order of the file.
type PlanFile struct {
	limits   []Limit
	features []Feature
	plans    []Plan
}

// Limits returns the file's limits, the inert ones included.
func (f *PlanFile) Limits() []Limit {
	return slices.Clone(f.limits)
}

// Features returns the file's feature flags.
func (f *PlanFile) Features() []Feature {
	return slices.Clone(f.features)
}

// Plans returns the file's plans.
func (f *PlanFile) Plans() []Plan {
	return slices.Clone(f.plans)
}

// Plan returns the plan whose key is key, and false if the file has none.
func (f *PlanFile) Plan(key string) (Plan, bool) {
	i := slices.IndexFunc(f.plans, func(p Plan) bool { return p.Key == key })
	if i < 0 {
		return Plan{}, false
	}
	return f.plans[i], true
}

// Default returns the plans of plans.json.
func Default() *PlanFile {
	return defaultPlans()
}

// defaultPlans loads plans.json once. A plan file that Load refuses panics,
// which TestPlanFile rules out.
var defaultPlans = sync.OnceValue(func() *PlanFile {
	f, err := Load(planFile)
	if err != nil {
		panic("entitlements: plans.json: " + err.Error())
	}
	return f
})

// fileJSON is the plan file, as JSON.
type fileJSON struct {
	// Comment is the file's comment, which nothing reads.
	Comment string `json:"$comment"`
	Limits  []struct {
		Key        LimitKey `json:"key"`
		Billable   bool     `json:"billable"`
		InertUntil string   `json:"inert_until"`
	} `json:"limits"`
	Features []Feature `json:"features"`
	Plans    []struct {
		Key      string             `json:"key"`
		Limits   map[LimitKey]Quota `json:"limits"`
		Features map[Feature]bool   `json:"features"`
	} `json:"plans"`
}

// Load reads a plan file, or explains why it is not one that the server can
// use. A plan file is one JSON object, with no field that fileJSON lacks, no
// key twice in an object, no key that is not lowercase, and no null. Its
// limits that are not inert are exactly the ones that the server checks, and
// its features the ones it knows. Every plan gives every limit a quota and
// says whether it has every feature, and allows at least one member, the
// owner, and the file has an unlimited plan, which limits nothing and has
// every feature, and a free plan.
func Load(data []byte) (*PlanFile, error) {
	if err := strictJSON(data); err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var in fileJSON
	if err := d.Decode(&in); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errors.New("more than one JSON value")
	}

	f := &PlanFile{}
	var active []LimitKey
	for i, l := range in.Limits {
		if !wellFormedKey(string(l.Key)) {
			return nil, fmt.Errorf("limits[%d]: the key %q is not lowercase letters, digits, and _, starting with a "+
				"letter", i, l.Key)
		}
		if slices.ContainsFunc(f.limits, func(o Limit) bool { return o.Key == l.Key }) {
			return nil, fmt.Errorf("limits[%d]: %s is declared twice", i, l.Key)
		}
		f.limits = append(f.limits, Limit{Key: l.Key, Billable: l.Billable, InertUntil: l.InertUntil})
		if l.InertUntil == "" {
			active = append(active, l.Key)
		}
	}
	if !sameSet(active, limitKeys) {
		return nil, fmt.Errorf("the limits that are not inert are %s, but the server checks %s", list(active, ""),
			list(limitKeys, ""))
	}
	for i, feature := range in.Features {
		if !wellFormedKey(string(feature)) {
			return nil, fmt.Errorf("features[%d]: the key %q is not lowercase letters, digits, and _, starting with "+
				"a letter", i, feature)
		}
		if slices.Contains(f.features, feature) {
			return nil, fmt.Errorf("features[%d]: %s is declared twice", i, feature)
		}
		f.features = append(f.features, feature)
	}
	if !sameSet(f.features, features) {
		return nil, fmt.Errorf("the features are %s, but the server knows %s", list(f.features, ""),
			list(features, ""))
	}

	for i, p := range in.Plans {
		if !wellFormedKey(p.Key) {
			return nil, fmt.Errorf("plans[%d]: the key %q is not lowercase letters, digits, and _, starting with a "+
				"letter", i, p.Key)
		}
		if _, ok := f.Plan(p.Key); ok {
			return nil, fmt.Errorf("plans[%d]: %s is declared twice", i, p.Key)
		}
		declared := make([]LimitKey, len(f.limits))
		for j, l := range f.limits {
			declared[j] = l.Key
		}
		if keys := slices.Collect(maps.Keys(p.Limits)); !sameSet(keys, declared) {
			return nil, fmt.Errorf("plans[%d] (%s): the limits are %s, but the file declares %s", i, p.Key,
				list(keys, ""), list(declared, ""))
		}
		if keys := slices.Collect(maps.Keys(p.Features)); !sameSet(keys, f.features) {
			return nil, fmt.Errorf("plans[%d] (%s): the features are %s, but the file declares %s", i, p.Key,
				list(keys, ""), list(f.features, ""))
		}
		// A new workspace's owner is its first member, whom the members limit
		// counts (package workspace), so a plan without one could create no
		// workspace, nor give an account its personal one.
		if !p.Limits[Members].Allows(0, 1) {
			return nil, fmt.Errorf("plans[%d] (%s): %s is 0, but every workspace has its owner, who is a member", i,
				p.Key, Members)
		}
		f.plans = append(f.plans, Plan{Key: p.Key, limits: p.Limits, features: p.Features})
	}

	unlimited, ok := f.Plan(Unlimited)
	if !ok {
		return nil, fmt.Errorf("there is no %s plan, which every workspace has while entitlements are not enforced",
			Unlimited)
	}
	for _, l := range f.limits {
		if q := unlimited.Quota(l.Key); q != NoLimit {
			return nil, fmt.Errorf("the %s plan limits %s to %s", Unlimited, l.Key, q)
		}
	}
	for _, feature := range f.features {
		if !unlimited.Has(feature) {
			return nil, fmt.Errorf("the %s plan lacks %s", Unlimited, feature)
		}
	}
	if _, ok := f.Plan(Free); !ok {
		return nil, fmt.Errorf("there is no %s plan, which a new workspace has while entitlements are enforced", Free)
	}
	return f, nil
}

// strictError is a problem of a plan file that encoding/json would let
// through without a word.
type strictError string

func (e strictError) Error() string {
	return string(e)
}

// strictJSON explains why the JSON in data has what encoding/json would read
// without a word, or returns nil if it has none: an object with a key twice,
// of which encoding/json would keep the last value; a key that is not
// lowercase ASCII letters, digits, _, and $, as every key of the plan file
// is, which encoding/json would match to a field of another case, so that
// "Limits" and "limits" would be one; or a null, which it would read as false
// or "", as a feature that a plan lacks. It leaves every other problem, such
// as a syntax error, to the decoder, which reports it with its offset.
func strictJSON(data []byte) error {
	d := json.NewDecoder(bytes.NewReader(data))
	// value reads a value, the value of the key key, or of no key if key is
	// "", as an element of an array is.
	var value func(key string) error
	value = func(key string) error {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch tok {
		case nil:
			if key == "" {
				return strictError("a value is null, which a plan file never holds")
			}
			return strictError(fmt.Sprintf("the value of the key %q is null, which a plan file never holds", key))
		case json.Delim('{'):
			seen := map[string]bool{}
			for d.More() {
				tok, err := d.Token()
				if err != nil {
					return err
				}
				// The decoder has checked that a key is a string.
				key, _ := tok.(string)
				if !lowercaseKey(key) {
					return strictError(fmt.Sprintf("the key %q is not lowercase ASCII letters, digits, _, and $, "+
						"as every key of a plan file is", key))
				}
				if seen[key] {
					return strictError(fmt.Sprintf("the key %q appears more than once in an object", key))
				}
				seen[key] = true
				if err := value(key); err != nil {
					return err
				}
			}
		case json.Delim('['):
			for d.More() {
				if err := value(""); err != nil {
					return err
				}
			}
		default:
			return nil
		}
		// The closing delimiter.
		_, err = d.Token()
		return err
	}
	var strict strictError
	if err := value(""); errors.As(err, &strict) {
		return err
	}
	return nil
}

// lowercaseKey reports whether key is lowercase ASCII letters, digits, _, and
// $, as the fields of fileJSON and every key of a plan are, so that only one
// spelling of it matches a field.
func lowercaseKey(key string) bool {
	return key != "" && !strings.ContainsFunc(key, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '$'
	})
}

// wellFormedKey reports whether key is lowercase ASCII letters, digits, and
// _, starting with a letter, as every key of the plan file is: a
// lowercaseKey without $ that starts with a letter.
func wellFormedKey(key string) bool {
	return lowercaseKey(key) && key[0] >= 'a' && key[0] <= 'z' && !strings.Contains(key, "$")
}

// sameSet reports whether a and b hold the same elements, each as many
// times.
func sameSet[T cmp.Ordered](a, b []T) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// list writes keys for an error, sorted, so that a map's keys read the same
// each time, and separated by commas, with the conjunction conj, such as
// "or", before the last if conj is not "", as in "members, projects, or
// storage_bytes". It writes no keys as "none".
func list[T ~string](keys []T, conj string) string {
	s := make([]string, len(keys))
	for i, k := range keys {
		s[i] = string(k)
	}
	slices.Sort(s)
	switch {
	case len(s) == 0:
		return "none"
	case conj == "" || len(s) == 1:
		return strings.Join(s, ", ")
	case len(s) == 2:
		return s[0] + " " + conj + " " + s[1]
	}
	return strings.Join(s[:len(s)-1], ", ") + ", " + conj + " " + s[len(s)-1]
}
