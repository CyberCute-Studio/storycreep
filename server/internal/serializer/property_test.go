package serializer_test

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/serializer"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// TestDeterministic checks, of random drafts, as a property of the
// serializer, that a draft serializes to the same bytes, file by file,
// whatever the order of its rows, as a database may read them, and however
// often it is serialized, as Go orders a map's keys at random each time; that
// each file is laid out as Layout lays it out, and reads back with
// published.Decode; that the reference runtime loads the snapshot; and that a
// release is refused exactly when a draft's files stand in for something, or
// the draft has a jumper without a target, which a draft's may lack and a
// release's may not, and that the reference runtime loads a release that is
// not refused.
//
// Verifies: FR-SNAP-03/AC1
func TestDeterministic(t *testing.T) {
	sch := newSchemas(t)
	standIns, untargeted := 0, 0
	for seed := range uint64(300) {
		r := rand.New(rand.NewPCG(seed, 76))
		d := randomDraft(t, r)
		first, err := serializer.Serialize(d, goldenDraftStamp)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		for range 3 {
			again, err := serializer.Serialize(shuffled(d, r), goldenDraftStamp)
			if err != nil {
				t.Fatalf("seed %d: shuffled: %v", seed, err)
			}
			if len(again.Files) != len(first.Files) || !slices.Equal(again.StandIns, first.StandIns) {
				t.Fatalf("seed %d: shuffled, the files or the stand-ins differ", seed)
			}
			for p, data := range first.Files {
				if !bytes.Equal(again.Files[p], data) {
					t.Fatalf("seed %d: shuffled, %s is\n%s\nwant\n%s", seed, p, again.Files[p], data)
				}
			}
		}
		for p, data := range first.Files {
			laid, err := serializer.Layout(data)
			if err != nil || !bytes.Equal(laid, data) {
				t.Fatalf("seed %d: %s is not laid out as Layout lays it out: %v", seed, p, err)
			}
		}
		if seed%10 == 0 {
			sch.check(t, first.Files)
		} else if _, err := loadSnapshot(first.Files); err != nil {
			t.Fatalf("seed %d: the reference runtime does not load the snapshot: %v", seed, err)
		}
		// The draft's jumpers without a target, which a release refuses.
		var none []serializer.StandIn
		for _, n := range d.Nodes {
			if n.Type == published.NodeJumper && n.TargetNode == "" {
				none = append(none, serializer.StandIn{Kind: serializer.StandInTarget, Board: n.Board, Node: n.ID,
					Entity: n.ID, Reason: serializer.ReasonNone})
			}
		}
		release, err := serializer.Serialize(d, goldenStamp)
		var unpublishable *serializer.UnpublishableError
		switch refused := len(first.StandIns) > 0 || len(none) > 0; {
		case !refused && err != nil:
			t.Fatalf("seed %d: the release is refused: %v", seed, err)
		case !refused:
			if _, err := loadSnapshot(release.Files); err != nil {
				t.Fatalf("seed %d: the reference runtime does not load the release: %v", seed, err)
			}
		case !errors.As(err, &unpublishable):
			t.Fatalf("seed %d: the release is %v, want an *UnpublishableError", seed, err)
		case len(unpublishable.StandIns) != len(first.StandIns)+len(none):
			t.Fatalf("seed %d: the release's refusal lists %v, want the draft's stand-ins %v and its jumpers "+
				"without a target %v", seed, unpublishable.StandIns, first.StandIns, none)
		}
		if len(first.StandIns) > 0 {
			standIns++
		}
		if len(none) > 0 {
			untargeted++
		}
	}
	if standIns == 0 || standIns == 300 {
		t.Errorf("%d of the drafts need a stand-in, which tests only one side of the property", standIns)
	}
	if untargeted == 0 {
		t.Error("no draft has a jumper without a target, which a release refuses")
	}
}

// shuffled returns a copy of d with each of its slices in a random order,
// and each component's values in a new map.
func shuffled(d *serializer.Draft, r *rand.Rand) *serializer.Draft {
	out := *d
	shuffle := func(n int, swap func(i, j int)) { r.Shuffle(n, swap) }
	out.Boards = slices.Clone(d.Boards)
	shuffle(len(out.Boards), func(i, j int) { out.Boards[i], out.Boards[j] = out.Boards[j], out.Boards[i] })
	out.Nodes = slices.Clone(d.Nodes)
	shuffle(len(out.Nodes), func(i, j int) { out.Nodes[i], out.Nodes[j] = out.Nodes[j], out.Nodes[i] })
	out.Outputs = slices.Clone(d.Outputs)
	shuffle(len(out.Outputs), func(i, j int) { out.Outputs[i], out.Outputs[j] = out.Outputs[j], out.Outputs[i] })
	out.Connections = slices.Clone(d.Connections)
	shuffle(len(out.Connections), func(i, j int) {
		out.Connections[i], out.Connections[j] = out.Connections[j], out.Connections[i]
	})
	out.Variables = slices.Clone(d.Variables)
	shuffle(len(out.Variables), func(i, j int) { out.Variables[i], out.Variables[j] = out.Variables[j], out.Variables[i] })
	out.ComponentTypes = slices.Clone(d.ComponentTypes)
	shuffle(len(out.ComponentTypes), func(i, j int) {
		out.ComponentTypes[i], out.ComponentTypes[j] = out.ComponentTypes[j], out.ComponentTypes[i]
	})
	out.Attributes = slices.Clone(d.Attributes)
	shuffle(len(out.Attributes), func(i, j int) {
		out.Attributes[i], out.Attributes[j] = out.Attributes[j], out.Attributes[i]
	})
	out.Components = slices.Clone(d.Components)
	for i := range out.Components {
		values := map[string]value.Value{}
		for k, v := range out.Components[i].Values {
			values[k] = v
		}
		out.Components[i].Values = values
	}
	shuffle(len(out.Components), func(i, j int) {
		out.Components[i], out.Components[j] = out.Components[j], out.Components[i]
	})
	return &out
}

// idMaker makes a draft's random IDs, each other than any that it made
// before, as no two entities of an instance have one ID (PRD §5.2).
type idMaker struct {
	r    *rand.Rand
	made map[ids.UUID]bool
}

// id returns a new random ID of the kind k.
func (m *idMaker) id(k ids.Kind) string {
	for {
		var u ids.UUID
		u[0] = 0x01
		u[14], u[15] = byte(m.r.IntN(256)), byte(m.r.IntN(256))
		if !m.made[u] {
			m.made[u] = true
			return ids.Encode(k, u)
		}
	}
}

// randomKey returns a key of package orderkey from a few, so that siblings
// tie, which their IDs then order.
func randomKey(r *rand.Rand) string {
	return []string{"1", "8", "8V", "V", "z"}[r.IntN(5)]
}

// floats are a float's ways of being written, as the database or a payload
// may write it, which Serialize writes in their shortest form.
var floats = []string{"0", "-0", "-0.0", "0.1", "2.50", "1e21", "1E21", "0.0000001", "1e-7", "123456789.125",
	"-3.5e2", "1.7976931348623157e308", "5e-324"}

// randomDraft returns a random draft of r: a few boards, of each type of
// node, outputs, connections, variables, component types and their
// definitions, and components, whose texts, expressions, and references
// now and then name what the draft does not hold, or do not compile.
func randomDraft(t *testing.T, r *rand.Rand) *serializer.Draft {
	t.Helper()
	m := &idMaker{r: r, made: map[ids.UUID]bool{}}
	randomID := func(_ *rand.Rand, k ids.Kind) string { return m.id(k) }
	d := &serializer.Draft{Project: serializer.Project{ID: randomID(r, ids.Project), Name: "Random <&>",
		Slug: "random"}}
	chance := func(percent int) bool { return r.IntN(100) < percent }
	for i := range 1 + r.IntN(3) {
		d.Boards = append(d.Boards, serializer.Board{ID: randomID(r, ids.Board), Slug: fmt.Sprintf("board-%d", i),
			Name: fmt.Sprintf("Board %d\u2028", i)})
	}
	pickBoard := func() string { return d.Boards[r.IntN(len(d.Boards))].ID }
	// Variables, with keys unique among the project's.
	types := []string{value.Bool, value.Int, value.Float, value.String}
	for i := range r.IntN(6) {
		v := serializer.Variable{ID: randomID(r, ids.Variable), Key: fmt.Sprintf("v%d", i),
			Scope: published.ScopeGlobal, Type: types[r.IntN(len(types))]}
		if chance(30) {
			v.Scope, v.Board, v.ResetOnEnter = published.ScopeBoard, pickBoard(), chance(50)
		}
		switch v.Type {
		case value.Bool:
			v.Default = raw("true")
		case value.Int:
			v.Default = raw([]string{"0", "3.0", "-7", "30e-1", "9007199254740991"}[r.IntN(5)])
		case value.Float:
			v.Default = raw(floats[r.IntN(len(floats))])
		default:
			v.Default = raw(`"<b>&\u2028</b>"`)
		}
		if chance(30) {
			v.Description = "A <description> & more"
		}
		d.Variables = append(d.Variables, v)
	}
	// Component types, their definitions, and components.
	for i := range r.IntN(3) {
		ct := serializer.ComponentType{ID: randomID(r, ids.ComponentType), Key: fmt.Sprintf("t%d", i),
			Name: fmt.Sprintf("Type %d", i)}
		d.ComponentTypes = append(d.ComponentTypes, ct)
		for j := range r.IntN(4) {
			a := serializer.Attribute{ID: randomID(r, ids.AttributeDef), ComponentType: ct.ID, Order: randomKey(r),
				Key: fmt.Sprintf("a%d", j), Name: fmt.Sprintf("Attribute %d", j), Runtime: chance(60)}
			switch r.IntN(4) {
			case 0:
				a.Type, a.Default = value.Int, raw("2.0")
			case 1:
				a.Type, a.Default = value.Float, raw(floats[r.IntN(len(floats))])
			case 2:
				a.Type, a.Options, a.Default = value.Enum, []string{"b", "a"}, raw(`"a"`)
			default:
				a.Type, a.Default, a.Runtime = value.Text, raw(`"line\nline"`), false
			}
			d.Attributes = append(d.Attributes, a)
		}
	}
	for i := range r.IntN(4) {
		if len(d.ComponentTypes) == 0 {
			break
		}
		c := serializer.Component{ID: randomID(r, ids.Component), Key: fmt.Sprintf("c%d", i),
			Name: fmt.Sprintf("Component <%d>", i), Values: map[string]value.Value{}}
		c.ComponentType = d.ComponentTypes[r.IntN(len(d.ComponentTypes))].ID
		for _, a := range d.Attributes {
			if a.ComponentType == c.ComponentType && chance(60) && a.Type == value.Float {
				c.Values[a.ID] = raw(floats[r.IntN(len(floats))])
			}
		}
		if chance(30) {
			c.Image = randomID(r, ids.Asset)
		}
		d.Components = append(d.Components, c)
	}
	// Nodes, of each board, with outputs.
	for _, b := range d.Boards {
		for range r.IntN(7) {
			n := serializer.Node{ID: randomID(r, ids.Node), Board: b.ID, X: r.Int64N(2e11) - 1e11,
				Y: r.Int64N(2e11) - 1e11}
			if chance(30) {
				n.Title = "Title <&>"
			}
			switch r.IntN(4) {
			case 0:
				n.Type = published.NodeBranch
				for range r.IntN(4) {
					o := serializer.Output{ID: randomID(r, ids.BranchOutput), Node: n.ID,
						Kind: []string{published.OutputIf, published.OutputElseIf, published.OutputElse}[r.IntN(3)]}
					if o.Kind != published.OutputElse {
						o.Order = randomKey(r)
					}
					d.Outputs = append(d.Outputs, o)
				}
			case 1:
				n.Type = published.NodeJumper
			default:
				n.Type, n.Kind, n.IsEnding = published.NodeElement, []string{"dialogue", "narration"}[r.IntN(2)], chance(20)
				if chance(50) {
					n.Tags = []string{"b", "a"}
				}
				if chance(20) {
					n.Cover = randomID(r, ids.Asset)
				}
			}
			d.Nodes = append(d.Nodes, n)
		}
	}
	// What a node, a text, or a connection refers to: mostly what the draft
	// holds, now and then what it does not.
	pickNode := func() (string, string) {
		if len(d.Nodes) == 0 || chance(10) {
			return "", randomID(r, ids.Node)
		}
		n := d.Nodes[r.IntN(len(d.Nodes))]
		return n.Board, n.ID
	}
	pickComponent := func() string {
		if len(d.Components) == 0 || chance(10) {
			return randomID(r, ids.Component)
		}
		return d.Components[r.IntN(len(d.Components))].ID
	}
	inline := func() richtext.Inline {
		switch r.IntN(5) {
		case 0:
			return &richtext.Mention{Component: pickComponent()}
		case 1:
			if len(d.Variables) > 0 && chance(90) {
				return &richtext.Value{Ref: d.Variables[r.IntN(len(d.Variables))].ID}
			}
			return &richtext.Value{Ref: randomID(r, ids.Variable)}
		case 2:
			for _, a := range d.Attributes {
				for _, c := range d.Components {
					if c.ComponentType == a.ComponentType && a.Type != value.Text && chance(50) {
						return &richtext.Value{Ref: c.ID, Attribute: a.ID}
					}
				}
			}
			return &richtext.Break{}
		}
		return &richtext.Text{Text: "word <&> ", Marks: richtext.Marks{Italic: chance(30)}}
	}
	doc := func() *richtext.Doc {
		if chance(20) {
			return nil
		}
		p := &richtext.Paragraph{}
		for range r.IntN(5) {
			p.Inlines = append(p.Inlines, inline())
		}
		return &richtext.Doc{Blocks: []richtext.Block{p}}
	}
	for i := range d.Nodes {
		n := &d.Nodes[i]
		switch n.Type {
		case published.NodeElement:
			n.Text = doc()
			if chance(50) {
				n.Speaker = pickComponent()
			}
		case published.NodeJumper:
			if chance(80) {
				n.TargetBoard, n.TargetNode = pickNode()
				if n.TargetBoard == "" {
					n.TargetBoard = pickBoard()
				}
			}
		}
	}
	var ports [][3]string
	for _, n := range d.Nodes {
		switch n.Type {
		case published.NodeElement:
			ports = append(ports, [3]string{n.Board, n.ID, ""})
		case published.NodeBranch:
			for _, o := range d.Outputs {
				if o.Node == n.ID {
					ports = append(ports, [3]string{n.Board, n.ID, o.ID})
				}
			}
		default:
			if chance(10) {
				ports = append(ports, [3]string{n.Board, n.ID, ""})
			}
		}
	}
	for range r.IntN(10) {
		if len(ports) == 0 {
			break
		}
		p := ports[r.IntN(len(ports))]
		_, to := pickNode()
		d.Connections = append(d.Connections, serializer.Connection{ID: randomID(r, ids.Connection), Board: p[0],
			From: p[1], FromOutput: p[2], To: to, Order: randomKey(r), Label: doc()})
	}
	for _, b := range d.Boards {
		if chance(70) {
			_, d.Boards[slices.IndexFunc(d.Boards, func(x serializer.Board) bool { return x.ID == b.ID })].EntryNode =
				pickNode()
		}
	}
	if chance(70) {
		d.Project.EntryBoard, d.Project.EntryNode = pickNode()
		if chance(30) {
			d.Project.EntryNode = ""
			d.Project.EntryBoard = pickBoard()
		}
	}
	// Expressions, compiled as the server compiles them, now and then of a
	// source that does not compile.
	c := newCompiler(t, d)
	condition := func(board string) *serializer.Expression {
		sources := []string{"true", "false or true", "1 + 2 > 2.5"}
		for _, v := range d.Variables {
			if v.Type == value.Int && (v.Scope == published.ScopeGlobal || v.Board == board) {
				sources = append(sources, v.Key+" > 1")
			}
		}
		if chance(10) {
			return c.condition("1 >", board)
		}
		return c.condition(sources[r.IntN(len(sources))], board)
	}
	script := func(board string) *serializer.Expression {
		sources := []string{`signal("s` + fmt.Sprint(r.IntN(3)) + `")`, `signal("<&>", 1.5)`}
		for _, v := range d.Variables {
			if v.Type == value.Int && (v.Scope == published.ScopeGlobal || v.Board == board) {
				sources = append(sources, v.Key+" += 1")
			}
		}
		if chance(10) {
			return c.script("x +=", board)
		}
		return c.script(strings.Join([]string{sources[r.IntN(len(sources))], sources[r.IntN(len(sources))]}, "\n"),
			board)
	}
	for i := range d.Nodes {
		n := &d.Nodes[i]
		if n.Type != published.NodeJumper && chance(40) {
			n.OnEnter = script(n.Board)
		}
		if n.Type != published.NodeJumper && chance(20) {
			n.OnExit = script(n.Board)
		}
	}
	boardOf := map[string]string{}
	for _, n := range d.Nodes {
		boardOf[n.ID] = n.Board
	}
	for i := range d.Outputs {
		if o := &d.Outputs[i]; o.Kind != published.OutputElse && chance(90) {
			o.Condition = condition(boardOf[o.Node])
		}
	}
	for i := range d.Connections {
		cn := &d.Connections[i]
		if chance(30) {
			cn.Condition = condition(cn.Board)
		}
		if chance(20) {
			cn.OnTraverse = script(cn.Board)
		}
	}
	return d
}
