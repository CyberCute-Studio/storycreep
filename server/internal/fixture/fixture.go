// Package fixture generates the reference projects of PRD §9.1, small,
// medium, large, and dense, from a seed (NFR-Q-06, plan note N52), for the
// server's benchmarks and for manual testing.
//
// A fixture is the batches of commands that build its project, in order, as
// a client would send them to the command pipeline: nothing of it is written
// around the pipeline (PRD §10.4). Generate makes them from a size and a
// seed alone, so the same size and seed give the same batches, byte for
// byte, on every machine: every ID, a command's and an entity's, is a UUIDv7
// whose milliseconds count up from a fixed instant and whose random bits the
// seed's own source makes, never the clock's or the system's. A project built
// from them is the same project, but for what the server makes of its own, as
// the IDs of its expressions and of its default component types, and the
// times and authors of its rows.
//
// Since the IDs are the seed's, a fixture builds one project of an instance:
// its entities' IDs, which no other entity of the instance may have (§5.2),
// are taken once it applies. Another project of the same instance takes
// another seed. A batch sent again, as after an interrupted upload, is
// answered as already applied, by its commands' IDs (FR-API-03), so an
// upload that stopped part way resumes where it stopped (Send).
//
// Each fixture has its project's own component types, Cast and Prop, rather
// than the default ones, whose IDs the server makes when it creates the
// project, so that no batch depends on what the server made. Every expression
// of a fixture compiles, and every text's values and mentions name what
// exists, and no jumper targets a jumper, so that a fixture is a project as
// an author leaves it, which breaks none of the rules of FR-DEP-03 whose
// findings are errors, though it may warn, as of a node that no choice leads
// to.
package fixture

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"

	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/component"
	"github.com/CyberCute-Studio/storycreep/server/internal/connection"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/node"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/variable"
)

// Size names a reference fixture (PRD §9.1).
type Size string

// The reference fixtures of PRD §9.1.
const (
	Small  Size = "small"
	Medium Size = "medium"
	Large  Size = "large"
	Dense  Size = "dense"
)

// Sizes returns the sizes of the reference fixtures, smallest first, and
// dense, which is medium on a single board, last.
func Sizes() []Size { return []Size{Small, Medium, Large, Dense} }

// ParseSize returns the size named s.
func ParseSize(s string) (Size, error) {
	if size := Size(s); slices.Contains(Sizes(), size) {
		return size, nil
	}
	return "", fmt.Errorf("fixture: no size %q, which is one of small, medium, large, and dense", s)
}

// Spec is what a fixture of a size holds: the counts of PRD §9.1, its
// boards, nodes, and variables, and its components, which §9.1 does not
// count.
type Spec struct {
	Boards     int
	Nodes      int
	Variables  int
	Components int
}

// Spec returns what a fixture of the size holds. small has 30 variables, a
// tenth as many as medium, as it has a tenth of its nodes; dense is medium's
// nodes, variables, and components, on one board.
func (s Size) Spec() Spec {
	switch s {
	case Small:
		return Spec{Boards: 3, Nodes: 200, Variables: 30, Components: 6}
	case Medium:
		return Spec{Boards: 20, Nodes: 2_000, Variables: 300, Components: 30}
	case Large:
		return Spec{Boards: 100, Nodes: 10_000, Variables: 1_000, Components: 100}
	case Dense:
		return Spec{Boards: 1, Nodes: 2_000, Variables: 300, Components: 30}
	}
	return Spec{}
}

// MaxBatch is the most commands that a batch of a fixture holds: half of what
// a client's batch may hold (1,000), so that no batch's transaction holds the
// project's lock for long, and each batch's undo is far from its bounds.
const MaxBatch = 500

// Fixture is the project of a size and a seed: the batches of commands that
// build it, in the order that they apply in.
type Fixture struct {
	Size Size
	Seed uint64
	// Name is the name of a project made for the fixture.
	Name string
	// Batches are the commands that build the project, each batch of at
	// most MaxBatch, in order: the folders, the component types and their
	// attributes, and the boards; the variables; the components; each
	// board's nodes, branch outputs, connections, and entry node, a board at
	// a time, in batches of their own; and last, the jumpers' targets and the
	// project's entry, once every node that they name exists.
	Batches [][]commands.Command
}

// Count returns how many commands of the type typ the fixture holds.
func (f *Fixture) Count(typ commands.Type) int {
	n := 0
	for _, b := range f.Batches {
		for _, c := range b {
			if c.Type == typ {
				n++
			}
		}
	}
	return n
}

// Apply applies the fixture's batches to the project, in order, through the
// pipeline, as actor, and stops at the first that does not apply. The
// project is a new one, as Fixture's doc says.
func (f *Fixture) Apply(ctx context.Context, p *commands.Pipeline, actor commands.Actor, projectID ids.UUID) error {
	for i, b := range f.Batches {
		if _, err := p.Apply(ctx, actor, projectID, b); err != nil {
			return fmt.Errorf("fixture: batch %d of %d: %w", i+1, len(f.Batches), err)
		}
	}
	return nil
}

// Body returns the body of a request of the batch to the command endpoint,
// POST /api/v1/projects/{project_id}/commands, as a client sends it:
// {"commands": [{"command_id", "type", "payload"}, …]}.
func Body(batch []commands.Command) []byte {
	type wire struct {
		ID      string          `json:"command_id"`
		Type    commands.Type   `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	body := struct {
		Commands []wire `json:"commands"`
	}{Commands: make([]wire, len(batch))}
	for i, c := range batch {
		body.Commands[i] = wire{ID: c.ID.String(), Type: c.Type, Payload: c.Payload}
	}
	return encode(body)
}

// encode returns the JSON of v, without HTML escaping, so that a condition's
// > is written as it is, and without a newline after it. Every value that
// the package encodes is of its own types, and a document that richtext
// accepts, so encoding never fails; if it did, the generator's types would be
// wrong, which no input can make them.
func encode(v any) []byte {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		panic(fmt.Sprintf("fixture: encode %T: %v", v, err))
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}

// epoch is the millisecond of the first ID of every fixture,
// 2026-01-01T00:00:00Z.
const epoch = 1_767_225_600_000

// Generate returns the fixture of the size and the seed.
func Generate(size Size, seed uint64) (*Fixture, error) {
	if _, err := ParseSize(string(size)); err != nil {
		return nil, err
	}
	g := &gen{
		spec: size.Spec(),
		// Two sources, so that the IDs do not move when what the content
		// draws from its source changes, and the other way round.
		r:   rand.New(rand.NewPCG(seed, 0x5eed_0001)),
		idr: rand.New(rand.NewPCG(seed, 0x5eed_0002)),
		ms:  epoch,
	}
	g.setup()
	g.variables()
	g.components()
	for b := range g.boards {
		g.board(b)
	}
	g.links()
	return &Fixture{Size: size, Seed: seed, Name: fmt.Sprintf("Fixture %s, seed %d", size, seed),
		Batches: g.batches}, nil
}

// gen is a fixture as it is generated: the batches so far, and what they
// created, which later commands name.
type gen struct {
	spec    Spec
	r       *rand.Rand
	idr     *rand.Rand
	ms      int64
	batches [][]commands.Command
	cur     []commands.Command

	variableFolders  []ids.UUID
	componentFolders []ids.UUID
	boards           []boardOf
	// globals are the global variables, which any board's expressions read.
	globals            []variableOf
	castType, propType ids.UUID
	cast               []componentOf
	props              []componentOf
	// The attributes of Cast, trust, mood, and alive, which are runtime
	// attributes, and bio, and of Prop, count, a runtime attribute, and
	// label.
	trust, mood, alive, bio, count, label ids.UUID
	// jumpers are the jumpers, whose targets the last batch sets.
	jumpers []jumperOf
}

type boardOf struct {
	id ids.UUID
	// locals are the board's own variables, which only its expressions read.
	locals []variableOf
	nodes  []ids.UUID
	// visitable are the board's nodes that visited() may name: every node
	// but a jumper, which no expression names (node_ref_jumper, §5.8.3).
	visitable []ids.UUID
}

type variableOf struct {
	id       ids.UUID
	key, typ string
}

type componentOf struct {
	id  ids.UUID
	key string
}

type jumperOf struct {
	id    ids.UUID
	board int
	// before is how many of its board's visitable nodes come before it, of
	// which a loop back targets one, since a jumper targets no jumper
	// (jumper_invalid, FR-DEP-03).
	before int
}

// uuid returns a new ID: a UUIDv7, as a client makes one, of the millisecond
// after the last ID's, and of random bits of the seed's source.
func (g *gen) uuid() ids.UUID {
	g.ms++
	var r [10]byte
	for i := range r {
		r[i] = byte(g.idr.UintN(256))
	}
	return ids.NewV7(g.ms, r)
}

// add adds a command of the type and payload to the current batch, which it
// ends once it holds MaxBatch.
func (g *gen) add(typ commands.Type, payload any) {
	g.cur = append(g.cur, commands.Command{ID: g.uuid(), Type: typ, Payload: encode(payload)})
	if len(g.cur) == MaxBatch {
		g.flush()
	}
}

// flush ends the current batch.
func (g *gen) flush() {
	if len(g.cur) > 0 {
		g.batches = append(g.batches, g.cur)
		g.cur = nil
	}
}

// The payloads, as the command types' own define them, of the fields that a
// fixture sets.
type (
	folderCreate struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
		Name string `json:"name"`
	}
	boardCreate struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		Color       string `json:"color,omitempty"`
		FolderID    string `json:"folder_id,omitempty"`
	}
	typeCreate struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Key  string `json:"key"`
		Icon string `json:"icon"`
	}
	attributeCreate struct {
		ID              string   `json:"id"`
		ComponentTypeID string   `json:"component_type_id"`
		Name            string   `json:"name"`
		Key             string   `json:"key"`
		Type            string   `json:"type"`
		Options         []string `json:"options,omitempty"`
		Default         any      `json:"default,omitempty"`
		Runtime         bool     `json:"runtime,omitempty"`
	}
	variableCreate struct {
		ID           string `json:"id"`
		Key          string `json:"key"`
		Type         string `json:"type"`
		Scope        string `json:"scope"`
		BoardID      string `json:"board_id,omitempty"`
		Default      any    `json:"default"`
		Description  string `json:"description,omitempty"`
		ResetOnEnter bool   `json:"reset_on_enter,omitempty"`
		FolderID     string `json:"folder_id,omitempty"`
	}
	componentCreate struct {
		ID              string         `json:"id"`
		ComponentTypeID string         `json:"component_type_id"`
		Name            string         `json:"name"`
		Key             string         `json:"key"`
		Description     *richtext.Doc  `json:"description,omitempty"`
		FolderID        string         `json:"folder_id,omitempty"`
		Attributes      map[string]any `json:"attributes,omitempty"`
	}
	position struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	}
	nodeCreate struct {
		ID                 string        `json:"id"`
		BoardID            string        `json:"board_id"`
		Type               string        `json:"type"`
		Kind               string        `json:"kind,omitempty"`
		Title              string        `json:"title,omitempty"`
		Text               *richtext.Doc `json:"text,omitempty"`
		SpeakerComponentID string        `json:"speaker_component_id,omitempty"`
		Color              string        `json:"color,omitempty"`
		Tags               []string      `json:"tags,omitempty"`
		Position           position      `json:"position"`
		IsEnding           bool          `json:"is_ending,omitempty"`
		OnEnter            string        `json:"on_enter,omitempty"`
		OnExit             string        `json:"on_exit,omitempty"`
	}
	outputCreate struct {
		ID        string `json:"id"`
		NodeID    string `json:"node_id"`
		Else      bool   `json:"else,omitempty"`
		Condition string `json:"condition,omitempty"`
	}
	connectionCreate struct {
		ID         string        `json:"id"`
		FromNodeID string        `json:"from_node_id"`
		FromPort   string        `json:"from_port,omitempty"`
		ToNodeID   string        `json:"to_node_id"`
		Label      *richtext.Doc `json:"label,omitempty"`
		Condition  string        `json:"condition,omitempty"`
		OnTraverse string        `json:"on_traverse,omitempty"`
	}
	entrySet struct {
		ID     string `json:"id"`
		NodeID string `json:"node_id"`
	}
	target struct {
		BoardID string `json:"board_id"`
		NodeID  string `json:"node_id"`
	}
	targetSet struct {
		ID     string `json:"id"`
		Target target `json:"target"`
	}
	settingsSet struct {
		Entry target `json:"entry"`
	}
)

// Folders of each kind hold this many of their entities: a board folder
// holds 10 boards, as an act of chapters.
const (
	boardsPerFolder     = 10
	variablesPerFolder  = 50
	componentsPerFolder = 25
)

// The kinds of variable, as often as a project has them, out of 20.
var variableTypes = []struct {
	typ  string
	in20 int
}{{"bool", 7}, {"int", 6}, {"float", 2}, {"string", 5}}

// moods are the options of Cast's mood, an enum attribute.
var moods = []string{"calm", "tense", "angry"}

// colors are the colours that a fixture gives some boards and nodes.
var colors = []string{"red", "orange", "yellow", "green", "teal", "blue", "purple", "pink", "gray"}

// labels begin the labels of an element's choices, each its own.
var labels = []string{"Ask about", "Leave with", "Wait for"}

// tags are the tags that a fixture gives some nodes.
var tags = []string{"main", "side", "quest", "lore", "combat", "romance", "secret", "tutorial"}

// setup adds the first batch: the folders, the component types with their
// attributes, and the boards.
func (g *gen) setup() {
	folders := func(kind string, n int, name string) []ids.UUID {
		out := make([]ids.UUID, n)
		for i := range out {
			out[i] = g.uuid()
			g.add(board.CreateFolder, folderCreate{ID: ids.Encode(ids.Folder, out[i]), Kind: kind,
				Name: fmt.Sprintf("%s %d", name, i+1)})
		}
		return out
	}
	boardFolders := folders("board", g.spec.Boards/boardsPerFolder, "Act")
	g.variableFolders = folders("variable", g.spec.Variables/variablesPerFolder, "Set")
	g.componentFolders = folders("component", g.spec.Components/componentsPerFolder, "Group")

	cast, prop := g.uuid(), g.uuid()
	g.add(component.CreateType, typeCreate{ID: ids.Encode(ids.ComponentType, cast), Name: "Cast", Key: "cast",
		Icon: "person"})
	g.add(component.CreateType, typeCreate{ID: ids.Encode(ids.ComponentType, prop), Name: "Prop", Key: "prop",
		Icon: "item"})
	g.castType, g.propType = cast, prop
	attr := func(typ ids.UUID, a attributeCreate) ids.UUID {
		id := g.uuid()
		a.ID, a.ComponentTypeID = ids.Encode(ids.AttributeDef, id), ids.Encode(ids.ComponentType, typ)
		g.add(component.CreateAttribute, a)
		return id
	}
	g.trust = attr(cast, attributeCreate{Name: "Trust", Key: "trust", Type: "int", Runtime: true})
	g.mood = attr(cast, attributeCreate{Name: "Mood", Key: "mood", Type: "enum", Options: moods, Default: moods[0],
		Runtime: true})
	g.alive = attr(cast, attributeCreate{Name: "Alive", Key: "alive", Type: "bool", Default: true, Runtime: true})
	g.bio = attr(cast, attributeCreate{Name: "Biography", Key: "bio", Type: "text"})
	g.count = attr(prop, attributeCreate{Name: "Count", Key: "count", Type: "int", Default: 1, Runtime: true})
	g.label = attr(prop, attributeCreate{Name: "Label", Key: "label", Type: "string"})

	g.boards = make([]boardOf, g.spec.Boards)
	for b := range g.boards {
		g.boards[b].id = g.uuid()
		p := boardCreate{ID: ids.Encode(ids.Board, g.boards[b].id),
			Name: fmt.Sprintf("Chapter %d: The %s", b+1, title(g.noun()))}
		if g.chance(50) {
			p.Description = g.sentence()
		}
		if g.chance(30) {
			p.Color = pick(g.r, colors)
		}
		if len(boardFolders) > 0 {
			p.FolderID = ids.Encode(ids.Folder, boardFolders[min(b/boardsPerFolder, len(boardFolders)-1)])
		}
		g.add(board.Create, p)
	}
	g.flush()
}

// variables adds the batches of the variables: seven in ten of them global,
// the others of a board each, in turn, half of those reset on entering it.
func (g *gen) variables() {
	for i := range g.spec.Variables {
		v := variableOf{id: g.uuid(), key: fmt.Sprintf("%s_%d", g.noun(), i+1),
			typ: g.variableType()}
		p := variableCreate{ID: ids.Encode(ids.Variable, v.id), Key: v.key, Type: v.typ, Scope: "global",
			Default: g.value(v.typ)}
		if g.chance(30) {
			b := i % len(g.boards)
			p.Scope, p.BoardID = "board", ids.Encode(ids.Board, g.boards[b].id)
			p.ResetOnEnter = g.chance(50)
			g.boards[b].locals = append(g.boards[b].locals, v)
		} else {
			g.globals = append(g.globals, v)
		}
		if g.chance(30) {
			p.Description = g.sentence()
		}
		if len(g.variableFolders) > 0 {
			p.FolderID = ids.Encode(ids.Folder, g.variableFolders[i%len(g.variableFolders)])
		}
		g.add(variable.Create, p)
	}
	g.flush()
}

// variableType returns a random type of a variable, as often as
// variableTypes says.
func (g *gen) variableType() string {
	n := g.r.IntN(20)
	for _, t := range variableTypes {
		if n < t.in20 {
			return t.typ
		}
		n -= t.in20
	}
	panic("fixture: variableTypes do not add up to 20")
}

// value returns a random value of the type. A float has a fraction, an odd
// number of eighths, so that its JSON reads as a float whatever reads it.
func (g *gen) value(typ string) any {
	switch typ {
	case "bool":
		return g.chance(20)
	case "int":
		return g.r.IntN(10)
	case "float":
		return float64(g.r.IntN(40))/4 + 0.125
	}
	return g.noun()
}

// components adds the batch of the components: two in three of them Cast,
// the others Prop.
func (g *gen) components() {
	for i := range g.spec.Components {
		c := componentOf{id: g.uuid()}
		p := componentCreate{ID: ids.Encode(ids.Component, c.id)}
		if i%3 != 2 {
			name := pick(g.r, names)
			c.key = fmt.Sprintf("%s_%d", name, i+1)
			p.ComponentTypeID, p.Name = ids.Encode(ids.ComponentType, g.castType), title(name)+" "+title(g.noun())
			p.Attributes = map[string]any{
				ids.Encode(ids.AttributeDef, g.trust): g.r.IntN(6),
				ids.Encode(ids.AttributeDef, g.mood):  pick(g.r, moods),
				ids.Encode(ids.AttributeDef, g.bio):   g.sentence() + "\n" + g.sentence(),
			}
			g.cast = append(g.cast, c)
		} else {
			// A prop's key is its noun and its number, as anchor3, and a
			// variable's its noun, an underscore, and its number, as
			// anchor_3, so that no variable has a component's key, which it
			// would shadow (PRD §5.8.3).
			noun := g.noun()
			c.key = fmt.Sprintf("%s%d", noun, i+1)
			p.ComponentTypeID, p.Name = ids.Encode(ids.ComponentType, g.propType), "The "+title(noun)
			p.Attributes = map[string]any{
				ids.Encode(ids.AttributeDef, g.count): 1 + g.r.IntN(5),
				ids.Encode(ids.AttributeDef, g.label): title(g.noun()),
			}
			g.props = append(g.props, c)
		}
		p.Key = c.key
		if g.chance(50) {
			p.Description = paragraph(g.sentence())
		}
		if len(g.componentFolders) > 0 {
			p.FolderID = ids.Encode(ids.Folder, g.componentFolders[i%len(g.componentFolders)])
		}
		g.add(component.Create, p)
	}
	g.flush()
}

// The kinds of node, by what a node of a board is.
const (
	element = iota
	branchNode
	jumperNode
	ending
)

// board adds the batches of the board b: its share of the nodes, laid out
// on a grid, the branches' outputs, the connections, and its entry node, its
// first node. Its first node is an element, and its last an ending; between
// them, of 100 nodes, 12 are branches, 5 jumpers, and 3 endings, and the
// others elements, three in ten of them narration and the others dialogue,
// with a speaker. An element has one choice, the next node, or, one time in
// three, two or three, each with a label, some with a condition; a branch has
// two or three outputs, the last of most of them an else, each to a node of
// the next few; a jumper continues at a node that the last batch sets; and
// an ending has none.
func (g *gen) board(b int) {
	n := g.spec.Nodes / g.spec.Boards
	if b < g.spec.Nodes%g.spec.Boards {
		n++
	}
	bd := &g.boards[b]
	bd.nodes = make([]ids.UUID, n)
	kinds := make([]int, n)
	for i := range n {
		bd.nodes[i] = g.uuid()
		switch roll := g.r.IntN(100); {
		case i == 0:
			kinds[i] = element
		case i == n-1, roll < 3:
			kinds[i] = ending
		case roll < 15:
			kinds[i] = branchNode
		case roll < 20:
			kinds[i] = jumperNode
		default:
			kinds[i] = element
		}
	}
	// A grid of about 3:2, as a board laid out by hand fills the screen.
	cols := int(math.Ceil(math.Sqrt(float64(n) * 1.5)))
	type out struct {
		port string
		cond bool
	}
	outs := make([][]out, n)
	for i, id := range bd.nodes {
		p := nodeCreate{ID: ids.Encode(ids.Node, id), BoardID: ids.Encode(ids.Board, bd.id),
			Position: position{X: float64(i%cols) * 320, Y: float64(i/cols) * 200}}
		if g.chance(30) {
			p.Title = "The " + title(g.noun())
		}
		if g.chance(10) {
			p.Color = pick(g.r, colors)
		}
		if g.chance(20) {
			p.Tags = []string{pick(g.r, tags)}
			if t := pick(g.r, tags); g.chance(30) && t != p.Tags[0] {
				p.Tags = append(p.Tags, t)
			}
		}
		switch kinds[i] {
		case element, ending:
			p.Type, p.Kind = "element", "dialogue"
			if g.chance(30) || len(g.cast) == 0 {
				p.Kind = "narration"
			} else {
				p.SpeakerComponentID = ids.Encode(ids.Component, pick(g.r, g.cast).id)
			}
			p.Text = g.text(b)
			p.IsEnding = kinds[i] == ending
			if g.chance(15) {
				p.OnEnter = g.script(b)
			}
			if g.chance(5) {
				p.OnExit = g.script(b)
			}
			if !p.IsEnding {
				choices := 1
				if g.chance(33) {
					choices = 2 + g.r.IntN(2)
				}
				for range choices {
					outs[i] = append(outs[i], out{cond: choices > 1 && g.chance(20)})
				}
			}
		case branchNode:
			p.Type = "branch"
			if g.chance(10) {
				p.OnEnter = g.script(b)
			}
		case jumperNode:
			p.Type = "jumper"
			g.jumpers = append(g.jumpers, jumperOf{id: id, board: b, before: len(bd.visitable)})
		}
		if kinds[i] != jumperNode {
			bd.visitable = append(bd.visitable, id)
		}
		g.add(node.Create, p)
	}
	for i, id := range bd.nodes {
		if kinds[i] != branchNode {
			continue
		}
		k := 2 + g.r.IntN(2)
		for j := range k {
			o := outputCreate{ID: ids.Encode(ids.BranchOutput, g.uuid()), NodeID: ids.Encode(ids.Node, id)}
			if j == k-1 && g.chance(70) {
				o.Else = true
			} else {
				o.Condition = g.condition(b)
			}
			g.add(node.CreateOutput, o)
			outs[i] = append(outs[i], out{port: o.ID})
		}
	}
	for i, id := range bd.nodes {
		// Each choice or output goes to a node of the next few, each to
		// another, or to the board's last node, an ending, past the end.
		to := i + 1
		for k, o := range outs[i] {
			c := connectionCreate{ID: ids.Encode(ids.Connection, g.uuid()), FromNodeID: ids.Encode(ids.Node, id),
				FromPort: o.port, ToNodeID: ids.Encode(ids.Node, bd.nodes[min(to, n-1)])}
			if o.port == "" && len(outs[i]) > 1 {
				// Each choice's label is its own (choice_duplicate_label).
				c.Label = paragraph(labels[k] + " the " + g.noun())
			}
			if o.cond {
				c.Condition = g.condition(b)
			}
			if g.chance(5) {
				c.OnTraverse = g.script(b)
			}
			g.add(connection.Create, c)
			to += 1 + g.r.IntN(3)
		}
	}
	g.add(node.SetBoardEntry, entrySet{ID: ids.Encode(ids.Board, bd.id), NodeID: ids.Encode(ids.Node, bd.nodes[0])})
	g.flush()
}

// links adds the last batch: each jumper's target, six times in ten the
// entry node of the next board, or of the first after the last, and
// otherwise an earlier node of its own board that is no jumper, as a loop
// back, and the project's entry, the first board's entry node.
func (g *gen) links() {
	for _, j := range g.jumpers {
		t := target{BoardID: ids.Encode(ids.Board, g.boards[j.board].id),
			NodeID: ids.Encode(ids.Node, g.boards[j.board].visitable[g.r.IntN(j.before)])}
		if len(g.boards) > 1 && g.chance(60) {
			next := g.boards[(j.board+1)%len(g.boards)]
			t = target{BoardID: ids.Encode(ids.Board, next.id), NodeID: ids.Encode(ids.Node, next.nodes[0])}
		}
		g.add(node.Update, targetSet{ID: ids.Encode(ids.Node, j.id), Target: t})
	}
	g.add(project.UpdateSettings, settingsSet{Entry: target{BoardID: ids.Encode(ids.Board, g.boards[0].id),
		NodeID: ids.Encode(ids.Node, g.boards[0].nodes[0])}})
	g.flush()
}

// readable returns a random variable that the board b's expressions read:
// one of its own, or a global one.
func (g *gen) readable(b int) variableOf {
	locals := g.boards[b].locals
	if len(locals) > 0 && g.chance(30) || len(g.globals) == 0 {
		return pick(g.r, locals)
	}
	return pick(g.r, g.globals)
}

// condition returns the source of a condition of the board b, which
// compiles: of a variable that it reads, of a runtime attribute of a
// component, whether an earlier node was visited, or two of them.
func (g *gen) condition(b int) string {
	switch roll := g.r.IntN(10); {
	case roll < 5:
		return g.variableCondition(b)
	case roll < 7:
		return g.attributeCondition()
	case roll < 8:
		return g.visitCondition(b)
	case roll < 9:
		return g.variableCondition(b) + " and " + g.attributeCondition()
	}
	return g.variableCondition(b) + " or not " + g.visitCondition(b)
}

func (g *gen) variableCondition(b int) string {
	v := g.readable(b)
	switch v.typ {
	case "bool":
		if g.chance(30) {
			return "not " + v.key
		}
		return v.key
	case "int":
		return v.key + " >= " + strconv.Itoa(g.r.IntN(6))
	case "float":
		return v.key + " < " + strconv.Itoa(g.r.IntN(10)) + ".5"
	}
	return v.key + ` == "` + g.noun() + `"`
}

func (g *gen) attributeCondition() string {
	if len(g.props) > 0 && g.chance(25) || len(g.cast) == 0 {
		return pick(g.r, g.props).key + ".count > 0"
	}
	c := pick(g.r, g.cast).key
	switch g.r.IntN(3) {
	case 0:
		return c + ".trust >= " + strconv.Itoa(1+g.r.IntN(4))
	case 1:
		return c + `.mood == "` + pick(g.r, moods) + `"`
	}
	return c + ".alive"
}

// visitCondition returns whether a node that is no jumper was visited: one
// of the board b's own, or, three times in ten, of the board before it.
func (g *gen) visitCondition(b int) string {
	nodes := g.boards[b].visitable
	if b > 0 && g.chance(30) {
		nodes = g.boards[b-1].visitable
	}
	return `visited(node("` + ids.Encode(ids.Node, pick(g.r, nodes)) + `"))`
}

// script returns the source of a script of the board b, which compiles: one
// or two statements, each an assignment to a variable that the board reads,
// or to a runtime attribute of a component, or a signal.
func (g *gen) script(b int) string {
	stmts := make([]string, 1+g.r.IntN(2))
	for i := range stmts {
		stmts[i] = g.statement(b)
	}
	return strings.Join(stmts, "; ")
}

// statement returns a statement of a script of the board b: six times in ten
// an assignment to a variable that it reads, two times to a runtime attribute
// of the Cast, once to Prop's, and otherwise a signal.
func (g *gen) statement(b int) string {
	switch roll := g.r.IntN(10); {
	case roll < 6:
		v := g.readable(b)
		switch v.typ {
		case "bool":
			return v.key + " = " + []string{"true", "false", "not " + v.key}[g.r.IntN(3)]
		case "int":
			return v.key + " += " + strconv.Itoa(1+g.r.IntN(3))
		case "float":
			return v.key + " += 0.5"
		}
		return v.key + ` = "` + g.noun() + `"`
	case roll < 8 && len(g.cast) > 0:
		c := pick(g.r, g.cast).key
		switch g.r.IntN(3) {
		case 0:
			return c + ".trust += 1"
		case 1:
			return c + `.mood = "` + pick(g.r, moods) + `"`
		}
		return c + ".alive = " + []string{"true", "false"}[g.r.IntN(2)]
	case roll < 9 && len(g.props) > 0:
		return pick(g.r, g.props).key + ".count -= 1"
	}
	return `signal("` + g.noun() + `")`
}

// text returns an element's text: one to three sentences, two in ten of them
// showing a value, of a variable that the board b reads or of a runtime
// attribute, and some mentioning a component.
func (g *gen) text(b int) *richtext.Doc {
	var inlines []richtext.Inline
	var pending strings.Builder
	push := func(in richtext.Inline) {
		if pending.Len() > 0 {
			inlines = append(inlines, &richtext.Text{Text: pending.String()})
			pending.Reset()
		}
		inlines = append(inlines, in)
	}
	for s := range 1 + g.r.IntN(3) {
		if s > 0 {
			pending.WriteString(" ")
		}
		pending.WriteString(g.sentence())
		switch roll := g.r.IntN(100); {
		case roll < 20:
			pending.WriteString(" It reads ")
			push(g.valueRef(b))
			pending.WriteString(".")
		case roll < 35 && len(g.cast) > 0:
			pending.WriteString(" Ask ")
			push(&richtext.Mention{Component: ids.Encode(ids.Component, pick(g.r, g.cast).id)})
			pending.WriteString(".")
		}
	}
	if pending.Len() > 0 {
		inlines = append(inlines, &richtext.Text{Text: pending.String()})
	}
	return &richtext.Doc{Blocks: []richtext.Block{&richtext.Paragraph{Inlines: inlines}}}
}

// valueRef returns a reference to a value that the board b's text shows: a
// variable that it reads, or a runtime attribute of a component.
func (g *gen) valueRef(b int) *richtext.Value {
	if g.chance(25) && len(g.cast) > 0 {
		attr := []ids.UUID{g.trust, g.mood, g.alive}[g.r.IntN(3)]
		return &richtext.Value{Ref: ids.Encode(ids.Component, pick(g.r, g.cast).id),
			Attribute: ids.Encode(ids.AttributeDef, attr)}
	}
	return &richtext.Value{Ref: ids.Encode(ids.Variable, g.readable(b).id)}
}

// sentence returns a sentence of six to twelve words.
func (g *gen) sentence() string {
	words := make([]string, 6+g.r.IntN(7))
	for i := range words {
		words[i] = pick(g.r, prose)
	}
	words[0] = title(words[0])
	return strings.Join(words, " ") + "."
}

func (g *gen) noun() string { return pick(g.r, nouns) }

// chance returns true percent times in 100.
func (g *gen) chance(percent int) bool { return g.r.IntN(100) < percent }

// pick returns a random element of xs, which is not empty.
func pick[T any](r *rand.Rand, xs []T) T { return xs[r.IntN(len(xs))] }

// title returns s with its first letter in upper case. s is ASCII.
func title(s string) string { return strings.ToUpper(s[:1]) + s[1:] }

// paragraph returns a document of one paragraph of the text.
func paragraph(text string) *richtext.Doc {
	return &richtext.Doc{Blocks: []richtext.Block{&richtext.Paragraph{
		Inlines: []richtext.Inline{&richtext.Text{Text: text}}}}}
}
