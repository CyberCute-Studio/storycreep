package serializer_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/serializer"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// hashHex returns sha256: and the lowercase hexadecimal SHA-256 of data.
func hashHex(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// mn returns the ID of the kind k whose body is the mnemonic, letters and
// digits of Crockford's base 32, after zeros, as the conformance suite names
// its entities, so that a draft's IDs read readably and sort as they read.
func mn(k ids.Kind, mnemonic string) string {
	id := k.Prefix() + "_" + strings.Repeat("0", ids.BodyLength-len(mnemonic)) + mnemonic
	if !ids.IsCanonical(k, id) {
		panic(fmt.Sprintf("%q makes no %s ID in its canonical spelling", mnemonic, k))
	}
	return id
}

// raw returns a value whose JSON is s, as the database holds it.
func raw(s string) value.Value {
	return value.FromJSON([]byte(s))
}

// compiler compiles a draft's expressions as the server does, against what
// the draft's expressions can name.
type compiler struct {
	t *testing.T
	p *expr.Project
}

// newCompiler returns a compiler of the draft's expressions, which names its
// variables, its components with their types' definitions, and its nodes.
func newCompiler(t *testing.T, d *serializer.Draft) *compiler {
	t.Helper()
	var vars []expr.Variable
	for _, v := range d.Variables {
		vars = append(vars, expr.Variable{ID: v.ID, Key: v.Key, Type: v.Type, Board: v.Board})
	}
	defs := map[string][]expr.Attribute{}
	for _, a := range d.Attributes {
		defs[a.ComponentType] = append(defs[a.ComponentType], expr.Attribute{ID: a.ID, Key: a.Key, Type: a.Type,
			Runtime: a.Runtime, Options: a.Options})
	}
	var comps []expr.Component
	for _, c := range d.Components {
		comps = append(comps, expr.Component{ID: c.ID, Key: c.Key, Attributes: defs[c.ComponentType]})
	}
	var nodes []expr.BoardNode
	for _, n := range d.Nodes {
		nodes = append(nodes, expr.BoardNode{ID: n.ID, Jumper: n.Type == published.NodeJumper})
	}
	p, err := expr.NewProject(vars, comps, nodes)
	if err != nil {
		t.Fatal(err)
	}
	return &compiler{t: t, p: p}
}

// expression returns the expression of source, compiled in ctx at the
// board, with its AST, or without one and with its error's code where it
// does not compile.
func (c *compiler) expression(source string, ctx expr.Context, board string) *serializer.Expression {
	c.t.Helper()
	n, err := expr.Compile(source, ctx, c.p, board)
	var e *expr.Error
	switch {
	case errors.As(err, &e):
		return &serializer.Expression{Source: source, Error: string(e.Code)}
	case err != nil:
		c.t.Fatalf("%q: %v", source, err)
	}
	return &serializer.Expression{Source: source, AST: n}
}

func (c *compiler) condition(source, board string) *serializer.Expression {
	c.t.Helper()
	return c.expression(source, expr.ContextCondition, board)
}

func (c *compiler) script(source, board string) *serializer.Expression {
	c.t.Helper()
	return c.expression(source, expr.ContextScript, board)
}

// text returns a document of the paragraphs, each of its inline nodes.
func text(paragraphs ...[]richtext.Inline) *richtext.Doc {
	d := &richtext.Doc{}
	for _, p := range paragraphs {
		d.Blocks = append(d.Blocks, &richtext.Paragraph{Inlines: p})
	}
	return d
}

func plain(s string) *richtext.Text { return &richtext.Text{Text: s} }

// The golden draft's stamps: a release's, and the draft channel's, at a
// moment that is not a whole second, which the manifest holds to the second.
var (
	goldenGenerator  = published.GeneratorInfo{Name: "storycreep", Version: "golden"}
	goldenMoment     = time.Date(2026, 11, 19, 12, 34, 56, 789_000_000, time.UTC)
	goldenStamp      = serializer.Stamp{Generator: goldenGenerator, At: goldenMoment, Release: &serializer.Release{ID: mn(ids.Snapshot, "G1"), Version: "1.4.0", Note: `Golden <note> & "quotes"`}}
	goldenDraftStamp = serializer.Stamp{Generator: goldenGenerator, At: goldenMoment}
)

// goldenDraft returns a draft of what §6.2's rules are about (TestGolden).
func goldenDraft(t *testing.T) *serializer.Draft {
	t.Helper()
	b1, b2, b3 := mn(ids.Board, "B1"), mn(ids.Board, "B2"), mn(ids.Board, "B3")
	n := func(m string) string { return mn(ids.Node, m) }
	cast, prop := mn(ids.ComponentType, "T1"), mn(ids.ComponentType, "T2")
	trust, mood, bio, count := mn(ids.AttributeDef, "A1"), mn(ids.AttributeDef, "A2"), mn(ids.AttributeDef, "A3"),
		mn(ids.AttributeDef, "A4")
	elara, mira := mn(ids.Component, "C1"), mn(ids.Component, "C2")
	luck, gate := mn(ids.Variable, "V2"), mn(ids.Variable, "V9")
	d := &serializer.Draft{
		Project: serializer.Project{ID: mn(ids.Project, "GD"), Name: `Harbour <Tales> & "Sea" 🐚`,
			Slug: "harbour-tales", EntryBoard: b1},
		Boards: []serializer.Board{
			{ID: b2, Slug: "keepers-gate", Name: `Keeper's "Gate" \ back`, EntryNode: n("N10")},
			{ID: b3, Slug: "empty", Name: "Empty"},
			{ID: b1, Slug: "harbour-intro", Name: "Harbour Intro\u2028Part one", EntryNode: n("N1")},
		},
		Variables: []serializer.Variable{
			{ID: mn(ids.Variable, "V1"), Key: "met_elara", Scope: published.ScopeGlobal, Type: value.Bool,
				Default: raw("true"), Description: "Met <Elara> & co.\tTab"},
			{ID: luck, Key: "luck", Scope: published.ScopeGlobal, Type: value.Float, Default: raw("0.1")},
			{ID: mn(ids.Variable, "V3"), Key: "big", Scope: published.ScopeGlobal, Type: value.Float, Default: raw("1e21")},
			{ID: mn(ids.Variable, "V4"), Key: "tiny", Scope: published.ScopeGlobal, Type: value.Float,
				Default: raw("0.0000001")},
			{ID: mn(ids.Variable, "V5"), Key: "negzero", Scope: published.ScopeGlobal, Type: value.Float,
				Default: raw("-0.0")},
			{ID: mn(ids.Variable, "V6"), Key: "coins", Scope: published.ScopeGlobal, Type: value.Int, Default: raw("3.0")},
			{ID: mn(ids.Variable, "V7"), Key: "visits_here", Scope: published.ScopeBoard, Board: b1, Type: value.Int,
				Default: raw("0"), ResetOnEnter: true},
			{ID: mn(ids.Variable, "V8"), Key: "motto", Scope: published.ScopeGlobal, Type: value.String,
				Default: raw(`"say \"hi\"\u2028\u2029 <b>&\u0007"`)},
			{ID: gate, Key: "gate_word", Scope: published.ScopeBoard, Board: b2, Type: value.String,
				Default: raw(`"open"`)},
		},
		ComponentTypes: []serializer.ComponentType{{ID: prop, Key: "prop", Name: "Prop"},
			{ID: cast, Key: "cast", Name: "Cast"}},
		Attributes: []serializer.Attribute{
			{ID: mood, ComponentType: cast, Order: "V", Key: "mood", Name: "Mood", Type: value.Enum,
				Options: []string{"tense", "calm"}, Default: raw(`"calm"`), Runtime: true},
			{ID: trust, ComponentType: cast, Order: "8", Key: "trust", Name: "Trust", Type: value.Int, Default: raw("0"),
				Runtime: true},
			{ID: bio, ComponentType: cast, Order: "G", Key: "bio", Name: "Biography", Type: value.Text, Default: raw(`""`)},
			{ID: count, ComponentType: prop, Order: "V", Key: "count", Name: "Count", Type: value.Int, Default: raw("1"),
				Runtime: true},
		},
		Components: []serializer.Component{
			{ID: mira, ComponentType: cast, Key: "mira", Name: "Mira", Values: map[string]value.Value{trust: raw("5.0")}},
			{ID: elara, ComponentType: cast, Key: "elara", Name: "Elara <the Keeper>",
				Image: mn(ids.Asset, "AS1"), Values: map[string]value.Value{trust: raw("2"), mood: raw(`"tense"`),
					bio: raw(`"Keeper of\nthe light"`)}},
		},
	}
	two := 2
	zero := 0
	d.Nodes = []serializer.Node{
		{ID: n("N5"), Board: b1, Type: published.NodeElement, Kind: "dialogue", Speaker: mira, X: 0, Y: 0},
		{ID: n("N1"), Board: b1, Type: published.NodeElement, Kind: "dialogue", Speaker: elara, Title: "Arrival",
			Tags: []string{"zeta", "Alpha"}, X: 13, Y: -5, Text: &richtext.Doc{Blocks: []richtext.Block{
				&richtext.Paragraph{Inlines: []richtext.Inline{
					&richtext.Text{Text: "Hello", Marks: richtext.Marks{Bold: true, Link: "https://example.com/a?b=1&c=<2>"}},
					plain(" "), &richtext.Mention{Component: elara}, plain(", trust "),
					&richtext.Value{Ref: elara, Attribute: trust, Decimals: &zero}, &richtext.Break{}, plain("luck "),
					&richtext.Value{Ref: luck, Decimals: &two}}},
				&richtext.Paragraph{Inlines: []richtext.Inline{plain("Second")}},
				&richtext.List{Start: 1, Items: []richtext.Item{
					{Blocks: []richtext.Block{&richtext.Paragraph{Inlines: []richtext.Inline{plain("one")}}}},
					{Blocks: []richtext.Block{&richtext.Paragraph{Inlines: []richtext.Inline{plain("two")}}}}}},
			}}},
		{ID: n("N2"), Board: b1, Type: published.NodeBranch, X: 99999999999, Y: 24000},
		{ID: n("N3"), Board: b1, Type: published.NodeElement, Kind: "narration", IsEnding: true, X: -100000000000,
			Y: 1, Text: text([]richtext.Inline{plain(`The end <3 & "bye"`)})},
		{ID: n("N4"), Board: b1, Type: published.NodeJumper, Title: "To the gate", TargetBoard: b2,
			TargetNode: n("N10"), X: 150, Y: 250},
		{ID: n("N10"), Board: b2, Type: published.NodeElement, Kind: "narration", IsEnding: true,
			Text: text([]richtext.Inline{plain("Say "), &richtext.Value{Ref: gate}, plain(", met: "),
				&richtext.Value{Ref: mn(ids.Variable, "V1")}})},
		{ID: n("N11"), Board: b2, Type: published.NodeJumper, TargetBoard: b1, TargetNode: n("N1")},
	}
	d.Outputs = []serializer.Output{
		{ID: mn(ids.BranchOutput, "Q3"), Node: n("N2"), Kind: published.OutputElse},
		{ID: mn(ids.BranchOutput, "Q1"), Node: n("N2"), Kind: published.OutputElseIf, Order: "V"},
		{ID: mn(ids.BranchOutput, "Q2"), Node: n("N2"), Kind: published.OutputIf, Order: "8"},
	}
	k := func(m string) string { return mn(ids.Connection, m) }
	d.Connections = []serializer.Connection{
		{ID: k("K1"), Board: b1, From: n("N1"), To: n("N2"), Order: "V", Label: text([]richtext.Inline{plain("Go on")})},
		{ID: k("K3"), Board: b1, From: n("N1"), To: n("N4"), Order: "8", Label: text([]richtext.Inline{plain("Jump")})},
		{ID: k("K2"), Board: b1, From: n("N1"), To: n("N3"), Order: "8", Label: text([]richtext.Inline{plain("Leave")})},
		{ID: k("K4"), Board: b1, From: n("N2"), FromOutput: mn(ids.BranchOutput, "Q2"), To: n("N3"), Order: "V"},
		{ID: k("K5"), Board: b1, From: n("N2"), FromOutput: mn(ids.BranchOutput, "Q1"), To: n("N5"), Order: "V"},
		{ID: k("K6"), Board: b1, From: n("N2"), FromOutput: mn(ids.BranchOutput, "Q3"), To: n("N4"), Order: "V"},
		{ID: k("K7"), Board: b1, From: n("N5"), To: n("N3"), Order: "V", Label: text([]richtext.Inline{})},
	}
	c := newCompiler(t, d)
	for i := range d.Nodes {
		switch d.Nodes[i].ID {
		case n("N1"):
			d.Nodes[i].OnEnter = c.script("coins += 1\nsignal(\"door_open\")", b1)
			d.Nodes[i].OnExit = c.script(`signal("bell", coins * 1.5)`, b1)
		case n("N2"):
			d.Nodes[i].OnEnter = c.script(`luck = luck + 0.0000001`, b1)
		}
	}
	for i := range d.Outputs {
		switch d.Outputs[i].ID {
		case mn(ids.BranchOutput, "Q1"):
			d.Outputs[i].Condition = c.condition("luck > 0.5", b1)
		case mn(ids.BranchOutput, "Q2"):
			d.Outputs[i].Condition = c.condition("met_elara and elara.trust >= 2 and elara.mood == \"tense\"", b1)
		}
	}
	for i := range d.Connections {
		switch d.Connections[i].ID {
		case k("K1"):
			d.Connections[i].Condition = c.condition("visits_here < 3 and not visited(node(\""+n("N3")+"\"))", b1)
		case k("K2"):
			d.Connections[i].OnTraverse = c.script("visits_here = 0\nsignal(\"bell\")", b1)
		}
	}
	return d
}

// standInDraft returns a draft that holds what no snapshot may, of every
// kind of stand-in, and what Serialize stands in for of it, in its order.
func standInDraft(t *testing.T) (*serializer.Draft, []serializer.StandIn) {
	t.Helper()
	b1, b2 := mn(ids.Board, "B1"), mn(ids.Board, "B2")
	gone := mn(ids.Board, "B9")
	n := func(m string) string { return mn(ids.Node, m) }
	k := func(m string) string { return mn(ids.Connection, m) }
	cast := mn(ids.ComponentType, "T1")
	trust, bio := mn(ids.AttributeDef, "A1"), mn(ids.AttributeDef, "A3")
	elara, ghost := mn(ids.Component, "C1"), mn(ids.Component, "C9")
	local2, lost := mn(ids.Variable, "V2"), mn(ids.Variable, "V9")
	d := &serializer.Draft{
		Project: serializer.Project{ID: mn(ids.Project, "S1"), Name: "Stand-ins", Slug: "stand-ins",
			EntryBoard: b2, EntryNode: n("N1")},
		Boards: []serializer.Board{{ID: b1, Slug: "one", Name: "One", EntryNode: n("N9")},
			{ID: b2, Slug: "two", Name: "Two", EntryNode: n("N20")}},
		Variables: []serializer.Variable{
			{ID: mn(ids.Variable, "V1"), Key: "gold", Scope: published.ScopeGlobal, Type: value.Int, Default: raw("0")},
			{ID: local2, Key: "local", Scope: published.ScopeBoard, Board: b2, Type: value.Int, Default: raw("0")},
			{ID: mn(ids.Variable, "V3"), Key: "orphan", Scope: published.ScopeBoard, Board: gone, Type: value.Int,
				Default: raw("0")},
		},
		ComponentTypes: []serializer.ComponentType{{ID: cast, Key: "cast", Name: "Cast"}},
		Attributes: []serializer.Attribute{
			{ID: trust, ComponentType: cast, Order: "8", Key: "trust", Name: "Trust", Type: value.Int, Default: raw("0"),
				Runtime: true},
			{ID: bio, ComponentType: cast, Order: "V", Key: "bio", Name: "Bio", Type: value.Text, Default: raw(`""`)},
		},
		Components: []serializer.Component{{ID: elara, ComponentType: cast, Key: "elara", Name: "Elara"}},
		Nodes: []serializer.Node{
			{ID: n("N1"), Board: b1, Type: published.NodeElement, Kind: "dialogue", Speaker: ghost,
				Text: text([]richtext.Inline{plain("Hi "), &richtext.Mention{Component: ghost}, plain(" and "),
					&richtext.Value{Ref: lost}, plain(", "), &richtext.Value{Ref: local2}, plain(" "),
					&richtext.Value{Ref: elara, Attribute: bio}, plain("!"), &richtext.Value{Ref: elara, Attribute: trust}})},
			{ID: n("N2"), Board: b1, Type: published.NodeBranch},
			{ID: n("N3"), Board: b1, Type: published.NodeJumper, TargetBoard: b1, TargetNode: n("N8")},
			{ID: n("N4"), Board: b1, Type: published.NodeJumper, TargetBoard: b1, TargetNode: n("N3")},
			{ID: n("N5"), Board: b1, Type: published.NodeJumper, TargetBoard: b1, TargetNode: n("N20")},
			{ID: n("N6"), Board: b1, Type: published.NodeBranch},
			{ID: n("N20"), Board: b2, Type: published.NodeElement, Kind: "narration", IsEnding: true},
			// A node of a board that the draft does not hold, which no read
			// gives, but which Serialize passes over all the same.
			{ID: n("N30"), Board: gone, Type: published.NodeElement, Kind: "narration"},
		},
		Outputs: []serializer.Output{
			{ID: mn(ids.BranchOutput, "Q1"), Node: n("N2"), Kind: published.OutputIf, Order: "8"},
			{ID: mn(ids.BranchOutput, "Q2"), Node: n("N2"), Kind: published.OutputElseIf, Order: "V"},
			// A branch's else without a conditional output, which it then
			// has not, so that the connection that leaves it leaves no port.
			{ID: mn(ids.BranchOutput, "Q3"), Node: n("N6"), Kind: published.OutputElse},
		},
		Connections: []serializer.Connection{
			{ID: k("K1"), Board: b1, From: n("N1"), To: n("N2"), Order: "V"},
			{ID: k("K2"), Board: b1, From: n("N3"), To: n("N1"), Order: "V"},
			{ID: k("K3"), Board: b1, From: n("N1"), To: n("N20"), Order: "W"},
			{ID: k("K4"), Board: b1, From: n("N6"), FromOutput: mn(ids.BranchOutput, "Q3"), To: n("N1"), Order: "V"},
			{ID: k("K5"), Board: b1, From: n("N2"), FromOutput: mn(ids.BranchOutput, "Q1"), To: n("N1"), Order: "V"},
		},
	}
	c := newCompiler(t, d)
	d.Nodes[0].OnEnter = c.script("gold +=", b1)
	d.Nodes[0].OnExit = c.script("gold += 1", b1)
	d.Nodes[1].OnExit = c.script("nothing = 1", b1)
	d.Outputs[1].Condition = c.condition("gold >", b1)
	d.Outputs[0].Condition = &serializer.Expression{Source: ""}
	d.Connections[0].Condition = c.condition("gold < ", b1)
	d.Connections[4].OnTraverse = c.script("gold = \"x\"", b1)
	for _, e := range []*serializer.Expression{d.Nodes[0].OnEnter, d.Nodes[1].OnExit, d.Outputs[1].Condition,
		d.Connections[0].Condition, d.Connections[4].OnTraverse} {
		if e.AST != nil {
			t.Fatalf("%q compiles", e.Source)
		}
	}
	code := func(e *serializer.Expression) string { return e.Error }
	want := []serializer.StandIn{
		{Kind: serializer.StandInSpeaker, Board: b1, Node: n("N1"), Entity: n("N1"), Ref: ghost},
		{Kind: serializer.StandInReference, Board: b1, Node: n("N1"), Entity: n("N1"), Field: serializer.FieldText,
			Ref: ghost, Reason: serializer.ReasonGone},
		{Kind: serializer.StandInReference, Board: b1, Node: n("N1"), Entity: n("N1"), Field: serializer.FieldText,
			Ref: lost, Reason: serializer.ReasonGone},
		{Kind: serializer.StandInReference, Board: b1, Node: n("N1"), Entity: n("N1"), Field: serializer.FieldText,
			Ref: local2, Reason: serializer.ReasonBoard},
		{Kind: serializer.StandInReference, Board: b1, Node: n("N1"), Entity: n("N1"), Field: serializer.FieldText,
			Ref: elara, Reason: serializer.ReasonRuntime},
		{Kind: serializer.StandInExpression, Board: b1, Node: n("N1"), Entity: n("N1"), Owner: serializer.OwnerNode,
			Field: serializer.FieldOnEnter, Source: "gold +=", Code: code(d.Nodes[0].OnEnter)},
		{Kind: serializer.StandInExpression, Board: b1, Node: n("N2"), Entity: n("N2"), Owner: serializer.OwnerNode,
			Field: serializer.FieldOnExit, Source: "nothing = 1", Code: code(d.Nodes[1].OnExit)},
		{Kind: serializer.StandInExpression, Board: b1, Node: n("N2"), Entity: mn(ids.BranchOutput, "Q1"),
			Owner: serializer.OwnerOutput, Field: serializer.FieldCondition},
		{Kind: serializer.StandInExpression, Board: b1, Node: n("N2"), Entity: mn(ids.BranchOutput, "Q2"),
			Owner: serializer.OwnerOutput, Field: serializer.FieldCondition, Source: "gold >",
			Code: code(d.Outputs[1].Condition)},
		{Kind: serializer.StandInTarget, Board: b1, Node: n("N3"), Entity: n("N3"), Ref: n("N8"),
			Reason: serializer.ReasonGone},
		{Kind: serializer.StandInTarget, Board: b1, Node: n("N4"), Entity: n("N4"), Ref: n("N3"),
			Reason: serializer.ReasonJumper},
		{Kind: serializer.StandInTarget, Board: b1, Node: n("N5"), Entity: n("N5"), Ref: n("N20"),
			Reason: serializer.ReasonBoard},
		{Kind: serializer.StandInExpression, Board: b1, Node: n("N1"), Entity: k("K1"), Owner: serializer.OwnerChoice,
			Field: serializer.FieldCondition, Source: "gold < ", Code: code(d.Connections[0].Condition)},
		{Kind: serializer.StandInConnection, Board: b1, Node: n("N3"), Entity: k("K2"), Reason: serializer.ReasonPort},
		{Kind: serializer.StandInConnection, Board: b1, Node: n("N1"), Entity: k("K3"), Reason: serializer.ReasonTarget},
		{Kind: serializer.StandInConnection, Board: b1, Node: n("N6"), Entity: k("K4"), Reason: serializer.ReasonPort},
		{Kind: serializer.StandInExpression, Board: b1, Node: n("N2"), Entity: k("K5"),
			Owner: serializer.OwnerConnection, Field: serializer.FieldOnTraverse, Source: `gold = "x"`,
			Code: code(d.Connections[4].OnTraverse)},
		{Kind: serializer.StandInBoardEntry, Board: b1, Entity: b1, Ref: n("N9")},
		{Kind: serializer.StandInEntry, Board: b2, Entity: mn(ids.Project, "S1"), Ref: n("N1"),
			Reason: serializer.ReasonBoard},
	}
	return d, want
}

// checkStandInFiles checks what the stand-in draft's files hold where it
// stood in: the texts on either side of what it left out of a text joined,
// a condition that does not compile, and an output without one, as false,
// with its source, a script that does not compile left out, and the
// speaker, the targets, the connections, the board's entry, and the
// project's entry left out.
func checkStandInFiles(t *testing.T, files map[string][]byte) {
	t.Helper()
	var one published.Board
	decode(t, files, published.BoardPath("one"), &one)
	el := one.Nodes[0].(*published.Element)
	if el.Speaker != "" || el.OnEnter != nil || el.OnExit == nil {
		t.Errorf("N1 has the speaker %q, onEnter %v, and onExit %v", el.Speaker, el.OnEnter, el.OnExit)
	}
	if got := richtext.PlainText(el.Text.Segments); got != "Hi  and ,  !{elara.trust}" {
		t.Errorf("N1's text is %q", got)
	}
	if segs := el.Text.Segments; len(segs) != 2 || segs[0].Value != "Hi  and ,  !" {
		t.Errorf("N1's texts are not joined: %+v", segs)
	}
	br := one.Nodes[1].(*published.Branch)
	if br.OnExit != nil || len(br.Outputs) != 2 {
		t.Fatalf("N2 has onExit %v and %d outputs", br.OnExit, len(br.Outputs))
	}
	for i, source := range []string{"", "gold >"} {
		c := br.Outputs[i].Condition
		if lit, ok := c.AST.(*expr.Lit); !ok || lit.Type != value.Bool || lit.Bool || c.Source != source {
			t.Errorf("output %d's condition is %#v of %q, want false of %q", i, c.AST, c.Source, source)
		}
	}
	for _, i := range []int{2, 3, 4} {
		if j := one.Nodes[i].(*published.Jumper); j.Target != nil {
			t.Errorf("%s has the target %v", j.ID, j.Target)
		}
	}
	if b := one.Nodes[5].(*published.Branch); len(b.Outputs) != 0 {
		t.Errorf("N6 has the outputs %v, want none for an else alone", b.Outputs)
	}
	var got []string
	for _, c := range one.Connections {
		got = append(got, c.ID)
	}
	if want := []string{mn(ids.Connection, "K1"), mn(ids.Connection, "K5")}; !slices.Equal(got, want) {
		t.Errorf("the connections are %v, want %v", got, want)
	}
	if c := one.Connections[0].Condition; c == nil || c.Source != "gold < " {
		t.Errorf("K1's condition is %v", c)
	} else if lit, ok := c.AST.(*expr.Lit); !ok || lit.Bool {
		t.Errorf("K1's condition is %#v, want false", c.AST)
	}
	if one.Connections[1].OnTraverse != nil {
		t.Errorf("K5 has onTraverse %v", one.Connections[1].OnTraverse)
	}
	if one.Board.EntryNodeID != "" {
		t.Errorf("one has the entry node %s", one.Board.EntryNodeID)
	}
	var m published.Manifest
	decode(t, files, published.ManifestPath, &m)
	if m.Entry != nil || m.Counts.Nodes != 7 || m.Counts.Variables != 2 || m.Counts.Connections != 2 {
		t.Errorf("the manifest has the entry %v and the counts %+v", m.Entry, m.Counts)
	}
	if _, ok := m.Snapshot.(*published.DraftSnapshot); !ok {
		t.Errorf("the manifest's snapshot is %T, want a draft's", m.Snapshot)
	}
	data, err := json.Marshal(m.Boards)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), mn(ids.Node, "N9")) {
		t.Errorf("the index names the entry node that is gone: %s", data)
	}
}
