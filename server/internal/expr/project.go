package expr

import (
	"errors"
	"fmt"
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
// NewProject returns one, which Check reads and never changes, so that one
// Project serves every expression of the project that is checked against it,
// at once too. The zero Project names nothing.
type Project struct {
	globals map[string]*Variable
	// boards are each board's variables, by its ID, and then by their keys.
	boards map[string]map[string]*Variable
	// boardsOf are the boards that have a variable of each key.
	boardsOf   map[string][]string
	components map[string]*component
	// nodes say of each node, by its ID, whether it is a jumper.
	nodes map[string]bool
}

// component is a Component and its attributes by their keys.
type component struct {
	Component
	attributes map[string]*Attribute
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
	}
	// seen are the IDs of the variables, the components, and the nodes, and
	// definitions the attribute definitions by theirs, which the components
	// of a type share.
	seen := make(map[string]bool)
	definitions := make(map[string]*Attribute)
	id := func(k ids.Kind, s string) error {
		if !canonical(k, s) {
			return fmt.Errorf("%w: %q is not the ID of a %s in its canonical spelling", ErrProject, s, k)
		}
		if seen[s] {
			return fmt.Errorf("%w: %s is the ID of two entities", ErrProject, s)
		}
		seen[s] = true
		return nil
	}
	for _, v := range variables {
		if err := id(ids.Variable, v.ID); err != nil {
			return nil, err
		}
		if err := key(v.Key); err != nil {
			return nil, err
		}
		if !slices.Contains(variableTypes, v.Type) {
			return nil, fmt.Errorf("%w: %q is no type of a variable", ErrProject, v.Type)
		}
		scope := p.globals
		if v.Board != "" {
			if !canonical(ids.Board, v.Board) {
				return nil, fmt.Errorf("%w: %q is not the ID of a board in its canonical spelling", ErrProject,
					v.Board)
			}
			if p.boards[v.Board] == nil {
				p.boards[v.Board] = make(map[string]*Variable)
			}
			scope = p.boards[v.Board]
			p.boardsOf[v.Key] = append(p.boardsOf[v.Key], v.Board)
		}
		if scope[v.Key] != nil {
			return nil, fmt.Errorf("%w: two variables of one scope have the key %s", ErrProject, v.Key)
		}
		scope[v.Key] = &v
	}
	for _, c := range components {
		if err := id(ids.Component, c.ID); err != nil {
			return nil, err
		}
		if err := key(c.Key); err != nil {
			return nil, err
		}
		if p.components[c.Key] != nil {
			return nil, fmt.Errorf("%w: two components have the key %s", ErrProject, c.Key)
		}
		e := &component{Component: Component{ID: c.ID, Key: c.Key, Attributes: slices.Clone(c.Attributes)},
			attributes: make(map[string]*Attribute)}
		listed := make(map[string]bool)
		for i := range e.Attributes {
			a := &e.Attributes[i]
			if !canonical(ids.AttributeDef, a.ID) {
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
		}
		p.components[c.Key] = e
	}
	for _, n := range nodes {
		if err := id(ids.Node, n.ID); err != nil {
			return nil, err
		}
		p.nodes[n.ID] = n.Jumper
	}
	return p, nil
}

// variableTypes are the types of a variable (FR-VAR-01).
var variableTypes = []string{value.Bool, value.Int, value.Float, value.String}

// canonical reports whether s is the ID of an entity of the kind k in its
// canonical spelling, as ids.Encode writes it.
func canonical(k ids.Kind, s string) bool {
	u, err := ids.Decode(k, s)
	return err == nil && ids.Encode(k, u) == s
}

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
