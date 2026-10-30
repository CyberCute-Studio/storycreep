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
	// base is the Project that With added this one's names to, or nil for
	// one that NewProject returns. A Project that With returns shares base's
	// globals and components, and its other maps hold what With added alone,
	// so that a read of a board's variables, of a key's boards, of a variable
	// by its ID, or of a node goes on to base's (ofBoard, boardKeys,
	// elsewhere, variable, node).
	base *Project
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
// The Project holds what it adds alone, and reads the rest in p (base), so
// that a Project of each of many boards, each with names of its own, as the
// expressions of a board in the trash see the board's own variables and
// nodes (package expression), costs what each board adds rather than a copy
// of p, and two Projects that add to one share nothing that either adds.
func (p *Project) With(board string, variables []Variable, nodes []BoardNode) (*Project, error) {
	if !ids.IsCanonical(ids.Board, board) {
		return nil, fmt.Errorf("%w: %q is not the ID of a board in its canonical spelling", ErrProject, board)
	}
	// q shares p's globals, which it adds none to, as every variable that it
	// adds is of the board, and p's components, which With adds none to.
	q := &Project{globals: p.globals, components: p.components, componentIDs: p.componentIDs,
		boards: make(map[string]map[string]*Variable), boardsOf: make(map[string][]string),
		variables: make(map[string]*Variable), nodes: make(map[string]bool), base: p}
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

// id returns an error that wraps ErrProject unless s is the ID of an entity
// of the kind k, in its canonical spelling, that is not the ID of a variable,
// a component, or a node of p.
func (p *Project) id(k ids.Kind, s string) error {
	if !ids.IsCanonical(k, s) {
		return fmt.Errorf("%w: %q is not the ID of a %s in its canonical spelling", ErrProject, s, k)
	}
	_, node := p.node(s)
	if p.variable(s) != nil || p.componentIDs[s] != nil || node {
		return fmt.Errorf("%w: %s is the ID of two entities", ErrProject, s)
	}
	return nil
}

// addVariable adds the variable v to p's own maps, or returns an error that
// wraps ErrProject: to p.globals if it is global, which only NewProject adds,
// as a Project that With returns shares its base's.
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
	if v.Board != "" && !ids.IsCanonical(ids.Board, v.Board) {
		return fmt.Errorf("%w: %q is not the ID of a board in its canonical spelling", ErrProject, v.Board)
	}
	if v.Board == "" && p.global(v.Key) != nil || v.Board != "" && p.ofBoard(v.Board, v.Key) != nil {
		return fmt.Errorf("%w: two variables of one scope have the key %s", ErrProject, v.Key)
	}
	if v.Board == "" {
		p.globals[v.Key] = &v
	} else {
		if p.boards[v.Board] == nil {
			p.boards[v.Board] = make(map[string]*Variable)
		}
		p.boards[v.Board][v.Key] = &v
		p.boardsOf[v.Key] = append(p.boardsOf[v.Key], v.Board)
	}
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
	for ; p != nil; p = p.base {
		if v := p.boards[board][key]; v != nil {
			return v
		}
	}
	return nil
}

// boardKeys returns the keys of the variables of the board whose ID is board,
// in no order.
func (p *Project) boardKeys(board string) []string {
	var keys []string
	for ; p != nil; p = p.base {
		for key := range p.boards[board] {
			keys = append(keys, key)
		}
	}
	return keys
}

// elsewhere reports whether a board other than the one whose ID is board has
// a variable of the key.
func (p *Project) elsewhere(board, key string) bool {
	for ; p != nil; p = p.base {
		if slices.ContainsFunc(p.boardsOf[key], func(b string) bool { return b != board }) {
			return true
		}
	}
	return false
}

// variable returns the variable whose ID is id, or nil.
func (p *Project) variable(id string) *Variable {
	for ; p != nil; p = p.base {
		if v := p.variables[id]; v != nil {
			return v
		}
	}
	return nil
}

// component returns the component of the key, or nil.
func (p *Project) component(key string) *component {
	return p.components[key]
}

// lookupVariable returns the variable that a variable's name, written with
// the qualifier scope before the key, names at the board whose ID is board
// (§5.8.3), or nil: global.<key> a global variable, board.<key> one of the
// board, and a bare key the board's, or else a global one. Check resolves a
// variable by it, and Rewrite writes a name that it resolves alike.
func (p *Project) lookupVariable(board string, scope Scope, key string) *Variable {
	switch scope {
	case ScopeGlobal:
		return p.global(key)
	case ScopeBoard:
		return p.ofBoard(board, key)
	}
	if v := p.ofBoard(board, key); v != nil {
		return v
	}
	return p.global(key)
}

// lookupComponent returns the component whose attribute an attribute's name
// names at the board whose ID is board (§5.8.3), or nil: cmp.<component>
// names the component of the key whatever else has it, as explicit says,
// and a bare <component> names it unless a variable of the board or a global
// one has the key (shadowed), which the name names then, as no component.
func (p *Project) lookupComponent(board string, explicit bool, key string) *component {
	if !explicit && p.shadowed(board, key) {
		return nil
	}
	return p.component(key)
}

// shadowed reports whether a variable of the board or a global one has the
// key, which a bare key, and <key>.<attribute>, names rather than a component
// of the key.
func (p *Project) shadowed(board, key string) bool {
	return p.ofBoard(board, key) != nil || p.global(key) != nil
}

// node returns whether the project has the node whose ID is id, and whether
// it is a jumper.
func (p *Project) node(id string) (jumper, ok bool) {
	for ; p != nil; p = p.base {
		if jumper, ok = p.nodes[id]; ok {
			return jumper, ok
		}
	}
	return false, false
}
