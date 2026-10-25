package expr

import (
	"cmp"
	"slices"
	"strings"
)

// MaxSuggestions is the most near matches that an Error suggests.
const MaxSuggestions = 3

// near returns the candidates that are near name, nearest first, at most
// MaxSuggestions of them, as an Error suggests them, so that every
// implementation suggests the same: those whose distance from name, with
// their ASCII letters in lower case, is at most a third of name's length, and
// at least 1, but less than name's length, so that a name of one character is
// near only itself in another case; ordered by their distance, and then by
// their bytes, each once. Name and candidates are identifiers, of ASCII alone.
// The distance is the optimal string alignment distance: the fewest
// insertions, deletions, and substitutions of a character, and swaps of two
// adjacent ones, that make one the other, no character changed twice.
func near(name string, candidates []string) []string {
	limit := min(max(1, len(name)/3), len(name)-1)
	type match struct {
		s string
		d int
	}
	var matches []match
	lower := strings.ToLower(name)
	for _, c := range candidates {
		// The distance is at least the difference of the lengths.
		if abs(len(c)-len(name)) > limit {
			continue
		}
		if d := distance(lower, strings.ToLower(c), limit); d <= limit {
			matches = append(matches, match{c, d})
		}
	}
	slices.SortFunc(matches, func(a, b match) int {
		return cmp.Or(cmp.Compare(a.d, b.d), strings.Compare(a.s, b.s))
	})
	matches = slices.CompactFunc(matches, func(a, b match) bool { return a.s == b.s })
	var out []string
	for _, m := range matches[:min(len(matches), MaxSuggestions)] {
		out = append(out, m.s)
	}
	return out
}

// abs returns the absolute value of x.
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// distance returns the optimal string alignment distance of a and b, or a
// distance above limit once it is certain to be more than limit.
func distance(a, b string, limit int) int {
	// rows are three rows of the distances of a's prefixes from b's: the
	// one before the previous one, the previous one, and the current one.
	var rows [3][]int
	for i := range rows {
		rows[i] = make([]int, len(b)+1)
	}
	for j := range rows[1] {
		rows[1][j] = j
	}
	for i := 1; i <= len(a); i++ {
		prev2, prev, cur := rows[0], rows[1], rows[2]
		cur[0] = i
		least := cur[0]
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d := min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				d = min(d, prev2[j-2]+1)
			}
			cur[j] = d
			least = min(least, d)
		}
		if least > limit {
			return limit + 1
		}
		rows[0], rows[1], rows[2] = prev, cur, prev2
	}
	return rows[1][len(b)]
}
