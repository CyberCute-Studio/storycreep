package entitlements

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Caps are the caps that an instance puts on the limits of every workspace,
// whatever the workspace's plan allows (STORYCREEP_WORKSPACE_LIMITS, PRD
// FR-ENT-01): the most of a limit that any workspace may use. A limit
// without a cap is as the workspace's plan has it.
type Caps map[LimitKey]int64

// ParseCaps reads caps written as limits and whole numbers from 1 to
// maxQuota, joined by = and separated by commas, with white space around
// each, as in "members=20, projects=50". Each limit is one that the server
// checks, and is named once.
func ParseCaps(s string) (Caps, error) {
	caps := Caps{}
	for i, entry := range strings.Split(s, ",") {
		key, value, ok := strings.Cut(entry, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || key == "" || value == "" {
			return nil, fmt.Errorf("entry %d is not a limit and a whole number joined by =, such as members=20", i+1)
		}
		l := LimitKey(key)
		if !slices.Contains(limitKeys, l) {
			if spec, ok := Default().limit(l); ok && spec.InertUntil != "" {
				return nil, fmt.Errorf("entry %d names %s, which nothing checks yet, so it cannot be capped", i+1, l)
			}
			return nil, fmt.Errorf("entry %d names no limit; the limits are %s", i+1, list(limitKeys, "or"))
		}
		if _, ok := caps[l]; ok {
			return nil, fmt.Errorf("entry %d names %s again", i+1, l)
		}
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < 1 || n > maxQuota || strings.ContainsAny(value, "+-") {
			return nil, fmt.Errorf("entry %d caps %s at a value that is not a whole number from 1 to %d", i+1, l,
				maxQuota)
		}
		caps[l] = n
	}
	return caps, nil
}

// String returns c as ParseCaps reads it, in the order of the limits that
// the server checks (LimitKeys).
func (c Caps) String() string {
	var entries []string
	for _, l := range limitKeys {
		if n, ok := c[l]; ok {
			entries = append(entries, string(l)+"="+strconv.FormatInt(n, 10))
		}
	}
	return strings.Join(entries, ", ")
}

// limit returns the limit of the file whose key is key, and false if it has
// none.
func (f *PlanFile) limit(key LimitKey) (Limit, bool) {
	i := slices.IndexFunc(f.limits, func(l Limit) bool { return l.Key == key })
	if i < 0 {
		return Limit{}, false
	}
	return f.limits[i], true
}
