package snapshot

import (
	"cmp"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// MaxVersionNumber is the greatest number of each part of a version, which
// nine digits hold, and an integer column of the database too.
const MaxVersionNumber = 999_999_999

// VersionPattern is the grammar of a version, MAJOR.MINOR.PATCH, each a
// number from 0 to MaxVersionNumber without a leading zero, as the publish
// request's schema and the database's check of snapshot.version take it
// (migration 00035): change all three at once.
const VersionPattern = `^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`

var versionPattern = regexp.MustCompile(VersionPattern)

// Version is a snapshot's version, which its author chooses (PRD FR-SNAP-01,
// §7.3): a label for people, whose numbers order a project's versions, each
// new one greater than those of the project's snapshots that are building
// or ready.
type Version struct {
	Major, Minor, Patch int32
}

// FirstVersion is the version that the server proposes for a project's
// first snapshot (plan note N85).
var FirstVersion = Version{Minor: 1}

// ParseVersion returns the version that s spells, or an error if s is not of
// the grammar VersionPattern.
func ParseVersion(s string) (Version, error) {
	if !versionPattern.MatchString(s) {
		return Version{}, fmt.Errorf("snapshot: %q is not a version MAJOR.MINOR.PATCH, each a number from 0 to %d "+
			"without a leading zero", s, MaxVersionNumber)
	}
	var parts [3]int32
	for i, p := range strings.Split(s, ".") {
		n, err := strconv.ParseInt(p, 10, 32)
		if err != nil {
			return Version{}, fmt.Errorf("snapshot: %q: %w", s, err)
		}
		parts[i] = int32(n)
	}
	return Version{Major: parts[0], Minor: parts[1], Patch: parts[2]}, nil
}

// String returns the version as MAJOR.MINOR.PATCH.
func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// Compare returns -1, 0, or +1 as v is less than, equal to, or greater than
// w, by its major number, then its minor, then its patch.
func (v Version) Compare(w Version) int {
	return cmp.Or(cmp.Compare(v.Major, w.Major), cmp.Compare(v.Minor, w.Minor), cmp.Compare(v.Patch, w.Patch))
}

// Next returns the version that the server proposes after v, a patch bump
// (§7.3), or, for a patch that is MaxVersionNumber already, a minor bump, or a
// major bump after that, and false for the greatest version, after which
// there is none.
func (v Version) Next() (Version, bool) {
	switch {
	case v.Patch < MaxVersionNumber:
		return Version{Major: v.Major, Minor: v.Minor, Patch: v.Patch + 1}, true
	case v.Minor < MaxVersionNumber:
		return Version{Major: v.Major, Minor: v.Minor + 1}, true
	case v.Major < MaxVersionNumber:
		return Version{Major: v.Major + 1}, true
	}
	return Version{}, false
}
