package expr

import (
	"slices"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// The entities of the project that relocateProjects pastes into, which has
// boards C and D, a global rep and a global trust, board C's x and trust, and
// a component vera of the attributes of renameAttrs.
var (
	boardC      = testID(ids.Board, 21)
	boardD      = testID(ids.Board, 22)
	varToRep    = testID(ids.Variable, 21) // global
	varToTrust  = testID(ids.Variable, 22) // global
	varCX       = testID(ids.Variable, 23) // board C's x
	varCTrust   = testID(ids.Variable, 24) // board C's trust
	cmpVera     = testID(ids.Component, 21)
	nodeA       = testID(ids.Node, 11)
	nodeB       = testID(ids.Node, 12)
	nodeCopy    = testID(ids.Node, 21)
	relocateTos = []Variable{
		{ID: varToRep, Key: "rep", Type: value.Int},
		{ID: varToTrust, Key: "trust", Type: value.Int},
		{ID: varCX, Key: "x", Type: value.Int, Board: boardC},
		{ID: varCTrust, Key: "trust", Type: value.Int, Board: boardC},
	}
)

// relocateProjects returns the project of renameVars and renameComps, which a
// copied source is of, at board B, and the project that relocateTos and vera
// make, which it is pasted into.
func relocateProjects(t *testing.T) (from, to *Project) {
	t.Helper()
	from, err := NewProject(renameVars, renameComps, []BoardNode{{ID: nodeA}, {ID: nodeB}})
	if err != nil {
		t.Fatal(err)
	}
	to, err = NewProject(relocateTos, []Component{{ID: cmpVera, Key: "vera", Attributes: renameAttrs}},
		[]BoardNode{{ID: nodeCopy}})
	if err != nil {
		t.Fatal(err)
	}
	return from, to
}

// TestRelocate checks that a pasted source names, at its new board, what the
// paste resolved each of its names to, in the shortest name that names it
// there, and an entity that the paste resolved to none explicitly, so that it
// names nothing of another scope or kind meanwhile; that a node reference to
// a copied node names the copy; and that every other name stays as written.
func TestRelocate(t *testing.T) {
	t.Parallel()
	from, to := relocateProjects(t)
	moves := Moves{
		Variables:  map[string]string{varRep: varToRep, varX: varCX, varBTrust: ""},
		Components: map[string]string{cmpElaraR: cmpVera},
		Nodes:      map[string]string{nodeA: nodeCopy},
	}
	for _, c := range []struct {
		name, board, source, want string
		ctx                       Context
	}{
		{name: "a global resolved to a global of its key stays bare", board: boardD, source: "rep > 1",
			want: "rep > 1"},
		{name: "a global resolved to a board's variable is bare where that board's own", board: boardC,
			source: "x > 1", want: "x > 1"},
		{name: "a board's variable resolved to none is written board.", board: boardC, source: "trust > 1",
			want: "board.trust > 1"},
		{name: "a qualified name is written as the shortest", board: boardD, source: "global.rep == 1",
			want: "rep == 1"},
		{name: "a component's attribute takes its new component's key", board: boardD, ctx: ContextScript,
			source: "elara.trust += 1\nelara.mood = \"calm\"", want: "vera.trust += 1\nvera.mood = \"calm\""},
		{name: "a component resolved to none is written cmp.", board: boardD, source: "mira.trust > 1",
			want: "cmp.mira.trust > 1"},
		{name: "a node reference to a copied node names the copy", board: boardD,
			source: `visited(node("` + nodeA + `"))`, want: `visited(node("` + nodeCopy + `"))`},
		{name: "a node reference to another node stays", board: boardD, source: `visited(node("` + nodeB + `"))`,
			want: `visited(node("` + nodeB + `"))`},
		{name: "a name that names nothing stays", board: boardD, source: "nobody > 1", want: "nobody > 1"},
		{name: "a source that does not parse stays", board: boardD, source: "rep >", want: "rep >"},
		{name: "white space between a name's parts stays", board: boardD, source: "elara . trust > 1",
			want: "vera . trust > 1"},
	} {
		if c.ctx == "" {
			c.ctx = ContextCondition
		}
		t.Run(c.name, func(t *testing.T) {
			got, err := Relocate(c.source, c.ctx, from, boardB, to, c.board, moves)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("Relocate(%q) = %q, want %q", c.source, got, c.want)
			}
		})
	}
	// An attribute's name takes the key of the definition that moves gives
	// it, as one renamed since the copy has, and else keeps its own.
	faith := slices.Clone(renameAttrs)
	faith[0].Key = "faith"
	renamed, err := NewProject(relocateTos, []Component{{ID: cmpVera, Key: "vera", Attributes: faith}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	attributes := Moves{Components: map[string]string{cmpElaraR: cmpVera},
		Attributes: map[string]string{AttributeRef(cmpElaraR, atrTrustR): atrTrustR}}
	if got, err := Relocate("elara.trust > 1 && elara.mood == \"calm\"", ContextCondition, from, boardB, renamed,
		boardD, attributes); err != nil || got != "vera.faith > 1 && vera.mood == \"calm\"" {
		t.Errorf("Relocate of an attribute renamed since = %q, %v, want vera.faith and vera.mood", got, err)
	}
	// <key>.<attribute> that a variable of the key names, as no component,
	// where it was copied is written with the key of the variable that it
	// resolves to, rather than name a component of its own key, and stays as
	// it is where it resolves to none, rather than name the component that
	// its component's name resolves to.
	shadow, err := NewProject([]Variable{{ID: varRep, Key: "elara", Type: value.Int}}, renameComps, nil)
	if err != nil {
		t.Fatal(err)
	}
	shadowed := Moves{Variables: map[string]string{varRep: varToRep},
		Components: map[string]string{cmpElaraR: cmpVera}}
	if got, err := Relocate("elara.trust > 1 && cmp.elara.trust > 1", ContextCondition, shadow, boardB, to, boardD,
		shadowed); err != nil || got != "rep.trust > 1 && vera.trust > 1" {
		t.Errorf("Relocate of a variable's <key>.<attribute> = %q, %v, want rep.trust and vera.trust", got, err)
	}
	shadowed.Variables[varRep] = ""
	if got, err := Relocate("elara.trust > 1", ContextCondition, shadow, boardB, to, boardD, shadowed); err != nil ||
		got != "elara.trust > 1" {
		t.Errorf("Relocate of a variable's <key>.<attribute> that resolves to none = %q, %v, want it as it is",
			got, err)
	}
	// An entity that moves gives, but the project that the paste goes to
	// lacks, is the caller's mistake.
	if _, err := Relocate("rep > 1", ContextCondition, from, boardB, to, boardC,
		Moves{Variables: map[string]string{varRep: varBTrust}}); err == nil {
		t.Error("a move to a variable that the project lacks is not an error")
	}
	if _, err := Relocate("rep > 1", ContextCondition, from, boardB, to, boardD,
		Moves{Variables: map[string]string{varRep: varCX}}); err == nil {
		t.Error("a move to a variable of another board is not an error")
	}
	lacking := Moves{Components: map[string]string{cmpElaraR: cmpVera},
		Attributes: map[string]string{AttributeRef(cmpElaraR, atrTrustR): testID(ids.AttributeDef, 99)}}
	if _, err := Relocate("elara.trust > 1", ContextCondition, from, boardB, to, boardD, lacking); err == nil {
		t.Error("a move to an attribute definition that the component lacks is not an error")
	}
}

// TestNamesOf checks that Names lists what each name of a source names, in the
// order of the source, and leaves out the names that name nothing.
func TestNamesOf(t *testing.T) {
	t.Parallel()
	from, _ := relocateProjects(t)
	got, err := Names(`trust > rep && elara.trust > 1 && nobody.x > 0 && cmp.mira.cheer == 1 && `+
		`visited(node("`+nodeA+`"))`, ContextCondition, from, boardB)
	if err != nil {
		t.Fatal(err)
	}
	want := []Named{{Variable: varBTrust}, {Variable: varRep},
		{Component: cmpElaraR, Attribute: atrTrustR, AttributeKey: "trust"},
		{Component: cmpMira, AttributeKey: "cheer"}, {Node: nodeA}}
	if !slices.Equal(got, want) {
		t.Errorf("Names = %+v, want %+v", got, want)
	}
	if got, err := Names("trust >", ContextCondition, from, boardB); err != nil || got != nil {
		t.Errorf("Names of a source that does not parse = %v, %v", got, err)
	}
	// <key>.<attribute> whose key a variable has names the variable, as no
	// component, and cmp.<key>.<attribute> the component.
	shadow, err := NewProject([]Variable{{ID: varRep, Key: "elara", Type: value.Int}}, renameComps, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err = Names("elara.trust > 1 && cmp.elara.trust > 1", ContextCondition, shadow, boardB)
	want = []Named{{Variable: varRep}, {Component: cmpElaraR, Attribute: atrTrustR, AttributeKey: "trust"}}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("Names of a shadowed component's attribute = %+v, %v, want %+v", got, err, want)
	}
}
