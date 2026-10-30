package expr

import (
	"slices"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// The entities of renameProject.
var (
	boardA     = testID(ids.Board, 11)
	boardB     = testID(ids.Board, 12)
	varRep     = testID(ids.Variable, 11) // global
	varX       = testID(ids.Variable, 12) // global
	varBTrust  = testID(ids.Variable, 13) // board B's trust
	cmpElaraR  = testID(ids.Component, 11)
	cmpMira    = testID(ids.Component, 12)
	atrTrustR  = testID(ids.AttributeDef, 11)
	atrMoodR   = testID(ids.AttributeDef, 12)
	renameVars = []Variable{
		{ID: varRep, Key: "rep", Type: value.Int},
		{ID: varX, Key: "x", Type: value.Int},
		{ID: varBTrust, Key: "trust", Type: value.Int, Board: boardB},
	}
	renameAttrs = []Attribute{
		{ID: atrTrustR, Key: "trust", Type: value.Int, Runtime: true},
		{ID: atrMoodR, Key: "mood", Type: value.Enum, Runtime: true, Options: []string{"calm", "angry"}},
	}
	renameComps = []Component{{ID: cmpElaraR, Key: "elara", Attributes: renameAttrs},
		{ID: cmpMira, Key: "mira", Attributes: renameAttrs}}
)

// renameProjects returns the project of renameVars and renameComps, and the
// project after the rename of the variable or the component whose ID is id to
// the key.
func renameProjects(t *testing.T, id, key string) (before, after *Project) {
	t.Helper()
	before, err := NewProject(renameVars, renameComps, nil)
	if err != nil {
		t.Fatal(err)
	}
	vs, cs := slices.Clone(renameVars), slices.Clone(renameComps)
	for i := range vs {
		if vs[i].ID == id {
			vs[i].Key = key
		}
	}
	for i := range cs {
		if cs[i].ID == id {
			cs[i].Key = key
		}
	}
	if after, err = NewProject(vs, cs, nil); err != nil {
		t.Fatal(err)
	}
	return before, after
}

// TestRewrite checks that a rename rewrites each name of the renamed entity in
// the shortest form that names it from the expression's board, and qualifies
// each name of another entity that the new key would take, leaving the rest
// of the source as it is, so that the rewritten source names what the source
// named. Its first cases are FR-VAR-03 AC4's fixture.
//
// Verifies: FR-VAR-03/AC4
func TestRewrite(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, id, key, board, source, want string
		ctx                                Context
		qualified                          int
	}{
		{name: "a global's bare key on a board of its new key is qualified", id: varRep, key: "trust", board: boardB,
			source: "rep >= 3", want: "global.trust >= 3"},
		{name: "a global's bare key elsewhere is bare", id: varRep, key: "trust", board: boardA, source: "rep >= 3",
			want: "trust >= 3"},
		{name: "a board's own variable of the new key is unchanged", id: varRep, key: "trust", board: boardB,
			source: "trust > 1 && rep >= 3", want: "trust > 1 && global.trust >= 3"},
		{name: "the reverse: a board's variable renamed to a global's key qualifies the global", id: varBTrust,
			key: "x", board: boardB, source: "x > 1 && trust > 2", want: "global.x > 1 && x > 2", qualified: 1},
		{name: "a qualified name of the renamed variable is written as the shortest", id: varRep, key: "trust",
			board: boardA, source: "global.rep == 1", want: "trust == 1"},
		{name: "board. of a board's variable is written bare", id: varBTrust, key: "t2", board: boardB,
			source: "board.trust + 1 > 2", want: "t2 + 1 > 2"},
		{name: "a script's assignments are rewritten", id: varRep, key: "trust", board: boardA, ctx: ContextScript,
			source: "rep += 1\nx = rep * 2", want: "trust += 1\nx = trust * 2"},
		{name: "comments and strings are not names", id: varRep, key: "trust", board: boardA,
			source: "rep > 1 // rep\n|| \"rep\" == \"rep\"", want: "trust > 1 // rep\n|| \"rep\" == \"rep\""},
		{name: "a name that names nothing stays", id: varRep, key: "trust", board: boardA, source: "trust > rep",
			want: "trust > trust"},
		{name: "a source that does not parse stays", id: varRep, key: "trust", board: boardA, source: "rep >",
			want: "rep >"},
		{name: "a component's attribute is renamed", id: cmpElaraR, key: "ela", board: boardA, ctx: ContextScript,
			source: "elara.trust += 1\nelara.mood = \"calm\"", want: "ela.trust += 1\nela.mood = \"calm\""},
		{name: "cmp. of an unshadowed component is written bare", id: cmpElaraR, key: "ela", board: boardA,
			source: "cmp.elara.trust > 1", want: "ela.trust > 1"},
		{name: "a component renamed to a variable's key is written with cmp.", id: cmpElaraR, key: "x",
			board: boardA, source: "elara.trust > 1", want: "cmp.x.trust > 1"},
		{name: "a component renamed to a board's variable's key is written with cmp. on that board", id: cmpElaraR,
			key: "trust", board: boardB, source: "elara.trust > 1", want: "cmp.trust.trust > 1"},
		{name: "a component renamed to a board's variable's key is bare elsewhere", id: cmpElaraR, key: "trust",
			board: boardA, source: "elara.trust > 1", want: "trust.trust > 1"},
		{name: "a variable renamed to a component's key qualifies the component's attributes", id: varRep,
			key: "mira", board: boardA, source: "mira.trust > rep", want: "cmp.mira.trust > mira", qualified: 1},
		{name: "another component of the same definition is not renamed", id: cmpElaraR, key: "ela", board: boardA,
			source: "mira.trust > elara.trust", want: "mira.trust > ela.trust"},
		{name: "a rename that changes no name changes nothing", id: varX, key: "y", board: boardA,
			source: "rep > 1", want: "rep > 1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ctx := c.ctx
			if ctx == "" {
				ctx = ContextCondition
			}
			before, after := renameProjects(t, c.id, c.key)
			got, edits, err := Rewrite(c.source, ctx, before, after, c.board, c.id)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("Rewrite(%q) = %q, want %q", c.source, got, c.want)
			}
			qualified := 0
			for _, e := range edits {
				if e.Qualified {
					qualified++
				}
				if e.Qualified == (e.Ref == c.id) {
					t.Errorf("the edit %+v is qualified exactly when it names the renamed entity", e)
				}
			}
			if qualified != c.qualified {
				t.Errorf("Rewrite(%q) qualified %d names, want %d: %+v", c.source, qualified, c.qualified, edits)
			}
			if (got == c.source) != (len(edits) == 0) {
				t.Errorf("Rewrite(%q) returned %d edits for %q", c.source, len(edits), got)
			}
		})
	}
}

// TestCaptures checks that a rename captures a source exactly when a name of
// it that names nothing before the rename names something after it, which
// Rewrite leaves as it is (PRD FR-VAR-03): the renamed entity, by its new
// key, or a component whose key the renamed variable had, which shadowed it.
func TestCaptures(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, id, key, board, source string
		// back says that the rename is the one from key back to the
		// entity's key in renameProjects.
		back, want bool
	}{
		{name: "a bare key that named nothing", id: varRep, key: "loyalty", board: boardA, source: "loyalty > 1",
			want: true},
		{name: "global. of a key that named nothing", id: varRep, key: "loyalty", board: boardA,
			source: "global.loyalty > 1", want: true},
		{name: "a name of the renamed variable, which Rewrite rewrites", id: varRep, key: "loyalty", board: boardA,
			source: "rep > 1"},
		{name: "a key that names nothing after either", id: varRep, key: "loyalty", board: boardA,
			source: "fame > 1"},
		{name: "a board's own variable of the key, which a global's new key does not take", id: varRep,
			key: "trust", board: boardB, source: "trust > 1"},
		{name: "a component's attribute that named nothing", id: cmpElaraR, key: "ela", board: boardA,
			source: "ela.trust > 1", want: true},
		{name: "a component's attribute that a variable renamed away shadowed", id: varRep, key: "mira",
			board: boardA, source: "mira.trust > 1", back: true, want: true},
		{name: "a component's attribute that a variable renamed to its key shadows", id: varRep, key: "mira",
			board: boardA, source: "mira.trust > 1"},
		{name: "a source that does not parse", id: varRep, key: "loyalty", board: boardA, source: "loyalty >"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			before, after := renameProjects(t, c.id, c.key)
			if c.back {
				before, after = after, before
			}
			got, err := Captures(c.source, ContextCondition, before, after, c.board)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("Captures(%q) = %v, want %v", c.source, got, c.want)
			}
		})
	}
}

// TestRewriteKeepsAST checks that the source that a rename rewrites compiles,
// against the project after the rename, to the AST that the source compiled
// to before it, but for the names that the AST writes for debugging (PRD
// FR-VAR-03 AC2).
//
// Verifies: FR-VAR-03/AC2
func TestRewriteKeepsAST(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		id, key, board, source string
		ctx                    Context
	}{
		{varRep, "trust", boardB, "rep >= 3 && trust < 2 && elara.trust > 0", ContextCondition},
		{varBTrust, "x", boardB, "x += trust\nelara.mood = \"angry\"", ContextScript},
		{varRep, "elara", boardA, "rep > elara.trust && cmp.mira.trust < 3", ContextCondition},
		{cmpElaraR, "rep", boardB, "elara.trust = rep\nmira.trust -= 1", ContextScript},
	} {
		before, after := renameProjects(t, c.id, c.key)
		want, err := Compile(c.source, c.ctx, before, c.board)
		if err != nil {
			t.Fatalf("%q: %v", c.source, err)
		}
		rewritten, _, err := Rewrite(c.source, c.ctx, before, after, c.board, c.id)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Compile(rewritten, c.ctx, after, c.board)
		if err != nil {
			t.Fatalf("%q, rewritten as %q: %v", c.source, rewritten, err)
		}
		if w, g := refsOf(want), refsOf(got); !slices.Equal(w, g) {
			t.Errorf("%q, rewritten as %q, names %v, want %v", c.source, rewritten, g, w)
		}
	}
}

// refsOf returns the refs of the names of n, in order.
func refsOf(n Node) []string {
	var out []string
	for _, m := range namesOf(n) {
		switch m := m.(type) {
		case *Var:
			out = append(out, m.Ref)
		case *Attr:
			out = append(out, m.Ref+"/"+m.Attribute)
		}
	}
	return out
}
