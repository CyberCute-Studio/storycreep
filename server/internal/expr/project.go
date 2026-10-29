package expr

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/CyberCute-Studio/storycreep/server/internal/ident"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// Variable is a variable that a project's expressions can name (PRD
// FR-VAR-01, FR-VAR-02): a global one, or one of a board.
type Variable struct {
	// ID is the variable's ID, in its canonical spelling (ids.Encode).
	ID  string `json:"id"`
	Key string `json:"key"`
	// Type is value.Bool, value.Int, value.Float, or value.String.
	Type string `json:"type"`
	// Board is the ID of the board whose variable it is, or "" for a global
	// one.
	Board string `json:"board,omitempty"`
}

// Component is a component, whose runtime attributes a project's expressions
// can read and write (FR-COMP-02, FR-COMP-03).
type Component struct {
	// ID is the component's ID, in its canonical spelling.
	ID         string      `json:"id"`
	Key        string      `json:"key"`
	Attributes []Attribute `json:"attributes"`
}

// Attribute is one of a component's attributes, of its type's attribute
// definition (FR-COMP-01).
type Attribute struct {
	// ID is the attribute definition's ID, in its canonical spelling, which
	// each component of its type lists, with the definition's key, type,
	// runtime flag, and options, as elara.trust and mira.trust share one
	// (PRD §5.3).
	ID  string `json:"id"`
	Key string `json:"key"`
	// Type is one of value.Types(): an expression reads a value.Enum as a
	// value.String, and a value.Text, which is never Runtime, not at all.
	Type    string `json:"type"`
	Runtime bool   `json:"runtime,omitempty"`
	// Options are a value.Enum's options, which are all that an expression
	// may assign it.
	Options []string `json:"options,omitempty"`
}

// BoardNode is a node of one of a project's boards, which a node reference
// can name (§5.8.4): an element, a branch, or a jumper, which visited and
// visits do not count (§5.8.5).
type BoardNode struct {
	// ID is the node's ID, in its canonical spelling.
	ID     string `json:"id"`
	Jumper bool   `json:"jumper,omitempty"`
}

// Project is what a project's expressions can name (PRD §5.8.3): its
// variables, global and of each board, its components and their attributes,
// and its nodes, those that are not in the trash, by their keys and IDs.
// NewProject returns one, and With one of another's names and a board's,
// which Check reads and never changes, so that one Project serves every
// expression of the project that is checked against it, at once too. The
// zero Project names nothing.
type Project struct {
	globals map[string]*Variable
	// boards are each board's variables, by its ID, and then by their keys.
	boards map[string]map[string]*Variable
	// boardsOf are the boards that have a variable of each key.
	boardsOf   map[string][]string
	components map[string]*component
	// variables and componentIDs are the variables and the components by
	// their IDs, by which CheckResolved reads a name.
	variables    map[string]*Variable
	componentIDs map[string]*component
	// nodes say of each node, by its ID, whether it is a jumper.
	nodes map[string]bool
}

// component is a Component and its attributes by their keys, and by their
// definitions' IDs.
type component struct {
	Component
	attributes map[string]*Attribute
	byID       map[string]*Attribute
}

// ErrProject is NewProject's error for what is not a project's: an ID that is
// not one of its entity's, in its canonical spelling, a key that is not an
// identifier, a type that is not a variable's or an attribute's, a text
// attribute that is runtime, an enum's options that it does not have, an
// entity whose ID, or whose key in its scope, is another's, or an attribute
// definition that two components list unalike, or one component twice.
var ErrProject = errors.New("expr: not what a project has")

// NewProject returns the Project of the variables, the components, and the
// nodes, or an error that wraps ErrProject. It copies what it reads, so that
// a change to them afterwards does not change the Project.
func NewProject(variables []Variable, components []Component, nodes []BoardNode) (*Project, error) {
	p := &Project{
		globals:    make(map[string]*Variable),
		boards:     make(map[string]map[string]*Variable),
		boardsOf:   make(map[string][]string),
		components: make(map[string]*component),
		nodes:      make(map[string]bool),

		variables:    make(map[string]*Variable),
		componentIDs: make(map[string]*component),
	}
	for _, v := range variables {
		if err := p.addVariable(v); err != nil {
			return nil, err
		}
	}
	// definitions are the attribute definitions by their IDs, which the
	// components of a type share.
	definitions := make(map[string]*Attribute)
	for _, c := range components {
		if err := p.id(ids.Component, c.ID); err != nil {
			return nil, err
		}
		if err := key(c.Key); err != nil {
			return nil, err
		}
		if p.components[c.Key] != nil {
			return nil, fmt.Errorf("%w: two components have the key %s", ErrProject, c.Key)
		}
		e := &component{Component: Component{ID: c.ID, Key: c.Key, Attributes: slices.Clone(c.Attributes)},
			attributes: make(map[string]*Attribute), byID: make(map[string]*Attribute)}
		listed := make(map[string]bool)
		for i := range e.Attributes {
			a := &e.Attributes[i]
			if !ids.IsCanonical(ids.AttributeDef, a.ID) {
				return nil, fmt.Errorf("%w: %q is not the ID of a %s in its canonical spelling", ErrProject, a.ID,
					ids.AttributeDef)
			}
			if listed[a.ID] {
				return nil, fmt.Errorf("%w: %s lists the attribute definition %s twice", ErrProject, c.Key, a.ID)
			}
			listed[a.ID] = true
			if err := key(a.Key); err != nil {
				return nil, err
			}
			if !slices.Contains(value.Types(), a.Type) {
				return nil, fmt.Errorf("%w: %q is no type of an attribute", ErrProject, a.Type)
			}
			if a.Type == value.Text && a.Runtime {
				return nil, fmt.Errorf("%w: the attribute %s.%s is a text, which is never runtime", ErrProject,
					c.Key, a.Key)
			}
			if (a.Type == value.Enum) != (len(a.Options) > 0) {
				return nil, fmt.Errorf("%w: the attribute %s.%s has options and is no enum, or is one without them",
					ErrProject, c.Key, a.Key)
			}
			if e.attributes[a.Key] != nil {
				return nil, fmt.Errorf("%w: two attributes of %s have the key %s", ErrProject, c.Key, a.Key)
			}
			// The components of a type share its definitions, each of which
			// is one entity, of one key, type, runtime flag, and options.
			if d, ok := definitions[a.ID]; ok && (d.Key != a.Key || d.Type != a.Type || d.Runtime != a.Runtime ||
				!slices.Equal(d.Options, a.Options)) {
				return nil, fmt.Errorf("%w: two components list the attribute definition %s unalike", ErrProject,
					a.ID)
			}
			a.Options = slices.Clone(a.Options)
			definitions[a.ID] = a
			e.attributes[a.Key] = a
			e.byID[a.ID] = a
		}
		p.components[c.Key] = e
		p.componentIDs[c.ID] = e
	}
	for _, n := range nodes {
		if err := p.addNode(n); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// With returns the Project of p's names and the variables of the board whose
// ID is board, each of which is of that board, and the nodes, as NewProject
// returns of them all, or an error that wraps ErrProject; p stays as it is.
// It copies the maps that the additions change, of p's variables and nodes
// by their IDs, its boards' scopes, and the boards of each key, but neither
// the scopes of the other boards nor the components and their attributes,
// which NewProject builds, so that a Project of each of many boards, each
// with names of its own, as the expressions of a board in the trash see the
// board's own variables and nodes (package expression), costs a copy of
// those maps a board rather than a Project's construction.
func (p *Project) With(board string, variables []Variable, nodes []BoardNode) (*Project, error) {
	if !ids.IsCanonical(ids.Board, board) {
		return nil, fmt.Errorf("%w: %q is not the ID of a board in its canonical spelling", ErrProject, board)
	}
	q := &Project{globals: p.globals, components: p.components, componentIDs: p.componentIDs,
		boards: cloned(p.boards), boardsOf: make(map[string][]string, len(p.boardsOf)),
		variables: cloned(p.variables), nodes: cloned(p.nodes)}
	// The board's own variables are added to a copy of its scope, and none
	// to another's, which q shares with p, and q appends a board to a slice of
	// its own of a key's boards, never into the array that p's slice holds, as
	// two Projects that add to p would append into one.
	if scope := p.boards[board]; scope != nil {
		q.boards[board] = maps.Clone(scope)
	}
	for key, boards := range p.boardsOf {
		q.boardsOf[key] = slices.Clip(boards)
	}
	for _, v := range variables {
		if v.Board != board {
			return nil, fmt.Errorf("%w: the variable %s is not of the board %s", ErrProject, v.ID, board)
		}
		if err := q.addVariable(v); err != nil {
			return nil, err
		}
	}
	for _, n := range nodes {
		if err := q.addNode(n); err != nil {
			return nil, err
		}
	}
	return q, nil
}

// cloned returns a copy of m, which a Project that With returns adds to, of
// a map even for a nil m, as the zero Project's are.
func cloned[M ~map[K]V, K comparable, V any](m M) M {
	c := make(M, len(m))
	maps.Copy(c, m)
	return c
}

// id returns an error that wraps ErrProject unless s is the ID of an entity
// of the kind k, in its canonical spelling, that is not the ID of a variable,
// a component, or a node of p.
func (p *Project) id(k ids.Kind, s string) error {
	if !ids.IsCanonical(k, s) {
		return fmt.Errorf("%w: %q is not the ID of a %s in its canonical spelling", ErrProject, s, k)
	}
	_, node := p.nodes[s]
	if p.variables[s] != nil || p.componentIDs[s] != nil || node {
		return fmt.Errorf("%w: %s is the ID of two entities", ErrProject, s)
	}
	return nil
}

// addVariable adds the variable v to p, or returns an error that wraps
// ErrProject. It changes p's maps, the scope of v's board in p.boards, and
// the slice of v's key's boards in p.boardsOf, which it appends v's board
// to, so a Project that With returns has copies of its own of those.
func (p *Project) addVariable(v Variable) error {
	if err := p.id(ids.Variable, v.ID); err != nil {
		return err
	}
	if err := key(v.Key); err != nil {
		return err
	}
	if !slices.Contains(variableTypes, v.Type) {
		return fmt.Errorf("%w: %q is no type of a variable", ErrProject, v.Type)
	}
	scope := p.globals
	if v.Board != "" {
		if !ids.IsCanonical(ids.Board, v.Board) {
			return fmt.Errorf("%w: %q is not the ID of a board in its canonical spelling", ErrProject, v.Board)
		}
		if p.boards[v.Board] == nil {
			p.boards[v.Board] = make(map[string]*Variable)
		}
		scope = p.boards[v.Board]
	}
	if scope[v.Key] != nil {
		return fmt.Errorf("%w: two variables of one scope have the key %s", ErrProject, v.Key)
	}
	if v.Board != "" {
		p.boardsOf[v.Key] = append(p.boardsOf[v.Key], v.Board)
	}
	scope[v.Key] = &v
	p.variables[v.ID] = &v
	return nil
}

// addNode adds the node n to p, or returns an error that wraps ErrProject.
func (p *Project) addNode(n BoardNode) error {
	if err := p.id(ids.Node, n.ID); err != nil {
		return err
	}
	p.nodes[n.ID] = n.Jumper
	return nil
}

// variableTypes are the types of a variable (FR-VAR-01).
var variableTypes = []string{value.Bool, value.Int, value.Float, value.String}

// key returns an error that wraps ErrProject unless s is an identifier that is
// not a reserved word, as a key is (§5.8.1).
func key(s string) error {
	if err := ident.Check(s); err != nil {
		return fmt.Errorf("%w: %q is no key: %v", ErrProject, s, err)
	}
	return nil
}

// global returns the global variable of the key, or nil.
func (p *Project) global(key string) *Variable {
	return p.globals[key]
}

// ofBoard returns the variable of the key of the board whose ID is board, or
// nil, as for the board "", which has none.
func (p *Project) ofBoard(board, key string) *Variable {
	return p.boards[board][key]
}

// elsewhere reports whether a board other than the one whose ID is board has
// a variable of the key.
func (p *Project) elsewhere(board, key string) bool {
	return slices.ContainsFunc(p.boardsOf[key], func(b string) bool { return b != board })
}

// component returns the component of the key, or nil.
func (p *Project) component(key string) *component {
	return p.components[key]
}

// node returns whether the project has the node whose ID is id, and whether
// it is a jumper.
func (p *Project) node(id string) (jumper, ok bool) {
	jumper, ok = p.nodes[id]
	return jumper, ok
}
