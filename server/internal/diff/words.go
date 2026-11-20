package diff

import (
	"slices"
	"strings"

	"github.com/CyberCute-Studio/storycreep/server/internal/semantics"
)

// The word-level diff of two plain texts, which a rich text's FieldChange
// holds for display (PRD §7.4). Its every step is specified, as
// schema/diff-corpus/README.md says, so that the Godot addon's diff gives the
// same runs (FR-GODOT-03 AC7):
//
//  1. Each text is split into tokens: each run of white space, of the code
//     points of Unicode's White_Space as semantics.IsWhiteSpace has them, and
//     each run of the other code points, in order.
//  2. The tokens that the two texts start with alike, and then those that
//     what is left of them ends with alike, are equal.
//  3. Between them, the shortest edit script of the tokens is the one that
//     Myers's greedy algorithm finds (editScript), going forward, if it needs
//     at most MaxEdits tokens deleted or inserted; otherwise every token
//     between them is deleted, and every token of the other text's inserted.
//  4. The script's tokens, in order, make runs: each run of equal tokens is an
//     equal run, and the tokens between two equal runs a change, its deleted
//     tokens' text and its inserted tokens' text, in their order.
//  5. Going from the first run to the last, an equal run of white space alone
//     that a change comes right before and right after joins the two changes
//     and itself into one, which deletes the first change's deleted text, the
//     white space, and the second's, and inserts the first's inserted text,
//     the white space, and the second's, so that a phrase rewritten word by
//     word reads as one change.
//  6. Each equal run is written as one equal WordRun, and each change as a
//     delete WordRun of its deleted text, if it has any, and then an insert
//     WordRun of its inserted text, if it has any.

// MaxEdits is the most tokens that Words deletes and inserts between the two
// texts' shared start and end by their shortest edit script; a text that more
// tokens tell apart is a change of everything between them, which bounds
// Myers's algorithm's time to that of MaxEdits steps over the two texts'
// tokens.
const MaxEdits = 500

// Words returns the word-level diff of before into after, two plain texts,
// as the steps above make it: its equal and delete runs, in order, are
// before, and its equal and insert runs after. It has no run of two texts
// that are both empty.
func Words(before, after string) []WordRun {
	a, b := tokens(before), tokens(after)
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}
	middleA, middleB := a[prefix:len(a)-suffix], b[prefix:len(b)-suffix]
	script, ok := editScript(middleA, middleB)
	if !ok {
		script = nil
		for _, t := range middleA {
			script = append(script, edit{op: Delete, token: t})
		}
		for _, t := range middleB {
			script = append(script, edit{op: Insert, token: t})
		}
	}
	var all []edit
	for _, t := range a[:prefix] {
		all = append(all, edit{op: Equal, token: t})
	}
	all = append(all, script...)
	for _, t := range a[len(a)-suffix:] {
		all = append(all, edit{op: Equal, token: t})
	}
	return runsOf(joined(regionsOf(all)))
}

// tokens returns the tokens of s: each run of white space, and each run of
// the other code points, in order.
func tokens(s string) []string {
	var out []string
	start, space := 0, false
	for i, r := range s {
		ws := semantics.IsWhiteSpace(r)
		if i > start && ws != space {
			out = append(out, s[start:i])
			start = i
		}
		space = ws
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// edit is a step of an edit script: a token that both texts have, Equal, that
// the first has and the second does not, Delete, or the reverse, Insert.
type edit struct {
	op, token string
}

// editScript returns the shortest edit script of a into b, the one that
// Myers's greedy algorithm finds going forward ("An O(ND) Difference
// Algorithm and Its Variations", 1986, as its figure 2 has it), and true, or
// false if it needs more than MaxEdits tokens deleted or inserted.
//
// For each number of edits d from 0, and each diagonal k from -d to d by 2,
// it extends the furthest path of diagonal k + 1 down, inserting a token of
// b, where k is -d, or where k is not d and that path is further along a
// than diagonal k - 1's, and otherwise the path of diagonal k - 1 right,
// deleting a token of a, and then along every token that a and b share
// there. The script is the path that reaches the end of both first, read back
// from its end through the paths that each d extended, each step's shared
// tokens after its edit.
func editScript(a, b []string) ([]edit, bool) {
	n, m := len(a), len(b)
	most := min(n+m, MaxEdits)
	offset := most + 1
	v := make([]int, 2*most+3)
	var trace [][]int
	for d := 0; d <= most; d++ {
		trace = append(trace, slices.Clone(v))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1]
			} else {
				x = v[offset+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[offset+k] = x
			if x >= n && y >= m {
				return backtrack(trace, a, b, offset), true
			}
		}
	}
	return nil, false
}

// backtrack returns the edit script of a into b whose paths trace holds, the
// furthest point of each diagonal before each number of edits, from the end
// of both back to their start, in the order of a and b.
func backtrack(trace [][]int, a, b []string, offset int) []edit {
	x, y := len(a), len(b)
	var script []edit
	for d := len(trace) - 1; d >= 0; d-- {
		v := trace[d]
		k := x - y
		prev := k - 1
		if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
			prev = k + 1
		}
		prevX := v[offset+prev]
		prevY := prevX - prev
		for x > prevX && y > prevY {
			script = append(script, edit{op: Equal, token: a[x-1]})
			x--
			y--
		}
		if d > 0 {
			if x == prevX {
				script = append(script, edit{op: Insert, token: b[y-1]})
			} else {
				script = append(script, edit{op: Delete, token: a[x-1]})
			}
		}
		x, y = prevX, prevY
	}
	slices.Reverse(script)
	return script
}

// region is a run of equal tokens, the text of which is equal, or a change,
// whose deleted tokens' text is deleted and inserted tokens' text inserted.
type region struct {
	change                   bool
	equal, deleted, inserted string
}

// regionsOf returns the regions of the script, in order: each run of equal
// tokens, and each run of the tokens between two of them.
func regionsOf(script []edit) []region {
	var out []region
	for _, e := range script {
		change := e.op != Equal
		if len(out) == 0 || out[len(out)-1].change != change {
			out = append(out, region{change: change})
		}
		r := &out[len(out)-1]
		switch e.op {
		case Equal:
			r.equal += e.token
		case Delete:
			r.deleted += e.token
		default:
			r.inserted += e.token
		}
	}
	return out
}

// joined returns rs with each equal region of white space alone that a change
// comes right before and right after joined, with the two changes, into one
// change, going from the first region to the last (step 5).
func joined(rs []region) []region {
	var out []region
	for _, r := range rs {
		if n := len(out); r.change && n >= 2 && !out[n-1].change && out[n-2].change && blank(out[n-1].equal) {
			ws := out[n-1].equal
			out = out[:n-1]
			prev := &out[n-2]
			prev.deleted += ws + r.deleted
			prev.inserted += ws + r.inserted
			continue
		}
		out = append(out, r)
	}
	return out
}

// blank reports whether s is white space alone.
func blank(s string) bool {
	return strings.TrimFunc(s, semantics.IsWhiteSpace) == ""
}

// runsOf returns the runs of rs: an equal run of each equal region, and a
// delete run of each change's deleted text, if it has any, and an insert run
// of its inserted text, if it has any (step 6).
func runsOf(rs []region) []WordRun {
	out := []WordRun{}
	for _, r := range rs {
		if !r.change {
			out = append(out, WordRun{Op: Equal, Text: r.equal})
			continue
		}
		if r.deleted != "" {
			out = append(out, WordRun{Op: Delete, Text: r.deleted})
		}
		if r.inserted != "" {
			out = append(out, WordRun{Op: Insert, Text: r.inserted})
		}
	}
	return out
}
