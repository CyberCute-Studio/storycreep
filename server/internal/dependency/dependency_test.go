package dependency_test

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/component"
	"github.com/CyberCute-Studio/storycreep/server/internal/connection"
	"github.com/CyberCute-Studio/storycreep/server/internal/dependency"
	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/expression"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/node"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/validation"
	"github.com/CyberCute-Studio/storycreep/server/internal/variable"
)

// world is what the expressions of the tests name: the global variables
// met_elara and gold, the board variable tide of the board harbour, the
// components elara and mira, each with the runtime attributes trust and
// mood, and the node keeper.
type world struct {
	metElara, gold, tide, elara, mira, trust, mood, keeper, harbour ids.UUID
	p                                                               *expr.Project
}

func newWorld(t *testing.T) world {
	t.Helper()
	w := world{metElara: ids.New(), gold: ids.New(), tide: ids.New(), elara: ids.New(), mira: ids.New(),
		trust: ids.New(), mood: ids.New(), keeper: ids.New(), harbour: ids.New()}
	attributes := []expr.Attribute{
		{ID: ids.Encode(ids.AttributeDef, w.trust), Key: "trust", Type: "int", Runtime: true},
		{ID: ids.Encode(ids.AttributeDef, w.mood), Key: "mood", Type: "string", Runtime: true}}
	var err error
	w.p, err = expr.NewProject([]expr.Variable{
		{ID: ids.Encode(ids.Variable, w.metElara), Key: "met_elara", Type: "bool"},
		{ID: ids.Encode(ids.Variable, w.gold), Key: "gold", Type: "int"},
		{ID: ids.Encode(ids.Variable, w.tide), Key: "tide", Type: "int", Board: ids.Encode(ids.Board, w.harbour)},
	}, []expr.Component{
		{ID: ids.Encode(ids.Component, w.elara), Key: "elara", Attributes: attributes},
		{ID: ids.Encode(ids.Component, w.mira), Key: "mira", Attributes: attributes},
	}, []expr.BoardNode{{ID: ids.Encode(ids.Node, w.keeper)}})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// line returns the use u as a line that names what it uses by the world's
// names.
func (w world) line(u dependency.Use) string {
	names := map[ids.UUID]string{w.metElara: "met_elara", w.gold: "gold", w.tide: "tide", w.elara: "elara",
		w.mira: "mira", w.trust: "trust", w.mood: "mood", w.keeper: "keeper"}
	s := fmt.Sprintf("%s %s %s", u.Access, u.Kind, names[u.Target])
	if u.Attribute != (ids.UUID{}) {
		s += "." + names[u.Attribute]
	}
	return s
}

func (w world) lines(uses []dependency.Use) []string {
	out := make([]string, len(uses))
	for i, u := range uses {
		out[i] = w.line(u)
	}
	slices.Sort(out)
	return out
}

// TestUses checks what an AST names, and how: a condition reads each name
// that it holds; a script writes the target of an assignment, and reads it
// too when the assignment updates it, and reads the names of its values and
// of its calls' arguments; and a node reference is a read of the node. An
// attribute is named by its component and its definition, so that elara.trust,
// mira.trust, and elara.mood are three targets, and each use is listed once,
// however often the source names it.
func TestUses(t *testing.T) {
	w := newWorld(t)
	keeper := ids.Encode(ids.Node, w.keeper)
	for _, tt := range []struct {
		source string
		ctx    expr.Context
		want   []string
	}{
		{"met_elara == true && elara.trust >= mira.trust", expr.ContextCondition,
			[]string{"read attribute elara.trust", "read attribute mira.trust", "read variable met_elara"}},
		{`visited(node("` + keeper + `")) || visits(node("` + keeper + `")) > gold`, expr.ContextCondition,
			[]string{"read node keeper", "read variable gold"}},
		{"elara.trust = 3", expr.ContextScript, []string{"write attribute elara.trust"}},
		{"elara.trust += 1", expr.ContextScript, []string{"read attribute elara.trust", "write attribute elara.trust"}},
		{"gold = gold + tide\nelara.mood = \"calm\"", expr.ContextScript,
			[]string{"read variable gold", "read variable tide", "write attribute elara.mood", "write variable gold"}},
		{"gold -= 1\ngold *= 2\ngold = 0", expr.ContextScript, []string{"read variable gold", "write variable gold"}},
		{"gold /= 2", expr.ContextScript, []string{"read variable gold", "write variable gold"}},
		{"gold %= 3", expr.ContextScript, []string{"read variable gold", "write variable gold"}},
		{"gold = 3", expr.ContextScript, []string{"write variable gold"}},
		{`signal("seen", mira.mood)` + "\nmet_elara = true", expr.ContextScript,
			[]string{"read attribute mira.mood", "write variable met_elara"}},
		{"", expr.ContextScript, []string{}},
		{"true", expr.ContextCondition, []string{}},
	} {
		t.Run(tt.source, func(t *testing.T) {
			n, err := expr.Compile(tt.source, tt.ctx, w.p, ids.Encode(ids.Board, w.harbour))
			if err != nil {
				t.Fatal(err)
			}
			uses, err := dependency.Uses(n)
			if err != nil {
				t.Fatal(err)
			}
			if got := w.lines(uses); !slices.Equal(got, tt.want) {
				t.Errorf("%q uses %q, want %q", tt.source, got, tt.want)
			}
			// The AST that the expression is stored as names the same.
			data, err := expr.Encode(n)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := expr.Decode(data, tt.ctx)
			if err != nil {
				t.Fatal(err)
			}
			if again, err := dependency.Uses(decoded); err != nil || !slices.Equal(again, uses) {
				t.Errorf("the stored AST uses %v (%v), want %v", again, err, uses)
			}
		})
	}
	// An AST whose names are not resolved, as Parse returns it, is no AST of
	// an expression that compiled.
	n, err := expr.Parse("gold > 1", expr.ContextCondition)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dependency.Uses(n); err == nil {
		t.Error("an AST whose names are not resolved has uses")
	}
}

// TestTextUses checks what a text names: each value reference's variable, or
// component and attribute, and each mention's component, read, each once,
// however deeply the lists that hold them nest.
func TestTextUses(t *testing.T) {
	w := newWorld(t)
	doc := fmt.Sprintf(`{"type":"doc","content":[{"type":"paragraph","content":[`+
		`{"type":"valueRef","attrs":{"ref":%[1]q}},{"type":"mention","attrs":{"id":%[2]q}}]},`+
		`{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[`+
		`{"type":"valueRef","attrs":{"ref":%[2]q,"attribute":%[3]q,"decimals":1}},`+
		`{"type":"text","text":"valueRef"},{"type":"mention","attrs":{"id":%[2]q}}]},`+
		`{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[`+
		`{"type":"valueRef","attrs":{"ref":%[1]q},"marks":[{"type":"bold"}]}]}]}]}]}]}]}`,
		ids.Encode(ids.Variable, w.gold), ids.Encode(ids.Component, w.mira), ids.Encode(ids.AttributeDef, w.trust))
	parsed, err := richtext.Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	uses, err := dependency.TextUses(*parsed)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"read attribute mira.trust", "read component mira", "read variable gold"}
	if got := w.lines(uses); !slices.Equal(got, want) {
		t.Errorf("the text uses %q, want %q", got, want)
	}
	if uses, err := dependency.TextUses(richtext.Doc{}); err != nil || len(uses) != 0 {
		t.Errorf("an empty text uses %v (%v)", uses, err)
	}
}

// TestInlineUse checks that an inline node of a text, as a text's JSON holds
// it, is read as TextUses reads it, and that anything else is refused.
func TestInlineUse(t *testing.T) {
	w := newWorld(t)
	for _, tt := range []struct {
		inline string
		want   string
	}{
		{fmt.Sprintf(`{"type":"mention","attrs":{"id":%q}}`, ids.Encode(ids.Component, w.elara)),
			"read component elara"},
		{fmt.Sprintf(`{"type":"valueRef","attrs":{"ref":%q},"marks":[]}`, ids.Encode(ids.Variable, w.tide)),
			"read variable tide"},
		{fmt.Sprintf(`{"type":"valueRef","attrs":{"ref":%q,"attribute":%q}}`, ids.Encode(ids.Component, w.elara),
			ids.Encode(ids.AttributeDef, w.mood)), "read attribute elara.mood"},
		{`{"type":"text","text":"x"}`, ""},
		{fmt.Sprintf(`{"type":"mention","attrs":{"id":%q}}`, ids.Encode(ids.Variable, w.tide)), ""},
		{`{"type":"valueRef","attrs":{"ref":"var_x"}}`, ""},
		{`[]`, ""},
	} {
		u, err := dependency.InlineUse([]byte(tt.inline))
		switch {
		case tt.want == "" && err == nil:
			t.Errorf("%s is read as %s", tt.inline, w.line(u))
		case tt.want != "" && (err != nil || w.line(u) != tt.want):
			t.Errorf("%s is read as %s (%v), want %s", tt.inline, w.line(u), err, tt.want)
		}
	}
}

// TestTextNeutral checks that each type of command that the derivation of the
// texts' index passes over is a registered type, or the name of the
// derivation that compiles expressions, whose changes of the owners of
// expressions are of their revisions alone.
func TestTextNeutral(t *testing.T) {
	r := commands.NewRegistry()
	project.Register(r)
	board.Register(r)
	variable.Register(r)
	component.Register(r)
	node.Register(r)
	connection.Register(r)
	expression.Register(r)
	dependency.Register(r)
	validation.Register(r)
	types := r.Types()
	for _, typ := range dependency.TextNeutral() {
		if !slices.Contains(types, typ) && typ != commands.Type(expression.Name) {
			t.Errorf("textNeutral lists %s, which is neither a registered type of command nor %s", typ,
				expression.Name)
		}
	}
}

// TestGraphEnums checks the enums that the graph's API declares against the
// values that the graph gives: an edge's kinds, in their order, and a
// contribution's end's sites, which are a usage's sites on a board, all but
// a node's speaker, which is no site of the index, and a component's
// description, which is on no board, and the node itself.
func TestGraphEnums(t *testing.T) {
	enum := func(v any, field string) []string {
		t.Helper()
		f, ok := reflect.TypeOf(v).FieldByName(field)
		if !ok {
			t.Fatalf("%T has no field %s", v, field)
		}
		return strings.Split(f.Tag.Get("enum"), ",")
	}
	var kinds []string
	for _, k := range dependency.EdgeKinds() {
		kinds = append(kinds, string(k))
	}
	if got := enum(dependency.Edge{}, "Kind"); !slices.Equal(got, kinds) {
		t.Errorf("the enum of an edge's kind is %q, want %q", got, kinds)
	}
	sites := []string{dependency.SiteNode}
	for _, s := range enum(dependency.Usage{}, "Site") {
		if s != dependency.SiteNodeSpeaker && s != dependency.SiteComponentDescription {
			sites = append(sites, s)
		}
	}
	if got := enum(dependency.End{}, "Site"); !slices.Equal(got, sites) {
		t.Errorf("the enum of a contribution's end's site is %q, want %q", got, sites)
	}
}
