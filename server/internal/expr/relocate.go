package expr

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Named is what a name of a source names (Names): a variable, a component's
// attribute, or a node.
type Named struct {
	// Variable is the ID of the variable that a variable's name names, or
	// that <key>.<attribute> names, as no component, where a variable of the
	// board or a global one has the key (§5.8.3).
	Variable string
	// Component is the ID of the component whose attribute an attribute's
	// name names, Attribute the ID of its attribute definition, or "" for an
	// attribute that the component does not have, and AttributeKey the key
	// that the name writes.
	Component, Attribute, AttributeKey string
	// Node is the ID of the node that a node reference names, whether or not
	// the project has it.
	Node string
}

// Names returns what the names of the source, of the context ctx, of an
// expression of the board whose ID is board, or of none for "", name in the
// project p (§5.8.3), in the order of the source: each variable's name that
// names a variable, each attribute's name whose component's name names a
// component, each <key>.<attribute> whose key a variable of the board or a
// global one has, which names that variable, as no component, and each node
// reference, as package clipboard declares what a copied expression names, so
// that a paste reads each name as it was read where it was copied, and writes
// <key>.<attribute> of such a variable with the key of the variable that it
// resolves to, which keeps it from naming a component of the key (Relocate).
// A name that names nothing is left out, and a source that does not parse
// names nothing.
func Names(source string, ctx Context, p *Project, board string) ([]Named, error) {
	if p == nil {
		return nil, fmt.Errorf("%w: names need a project", ErrProject)
	}
	n, err := Parse(source, ctx)
	var e *Error
	switch {
	case errors.As(err, &e):
		return nil, nil
	case err != nil:
		return nil, err
	}
	var out []Named
	for _, m := range relocated(n) {
		switch m := m.(type) {
		case *Var:
			if v := p.lookupVariable(board, m.Scope, m.Key); v != nil {
				out = append(out, Named{Variable: v.ID})
			}
		case *Attr:
			c := p.lookupComponent(board, m.Explicit, m.ComponentKey)
			switch {
			case c != nil:
				named := Named{Component: c.ID, AttributeKey: m.AttributeKey}
				if a := c.attributes[m.AttributeKey]; a != nil {
					named.Attribute = a.ID
				}
				out = append(out, named)
			case !m.Explicit:
				// A variable that has the key names the name, as no
				// component, where it shadows one (lookupComponent).
				if v := p.lookupVariable(board, ScopeNone, m.ComponentKey); v != nil {
					out = append(out, Named{Variable: v.ID})
				}
			}
		case *NodeRef:
			out = append(out, Named{Node: m.Ref})
		}
	}
	return out, nil
}

// Moves says what the names of a source that Relocate moves name where it
// moves to: the ID of each variable and of each component that a name names
// where the source is, by that entity's ID, of the entity that the name is to
// name where the source goes, or "" for none; the ID of each attribute
// definition that a name of a component's attribute names where the source
// is, by the IDs of its component and of itself there (AttributeRef), of the
// definition of the component that Components gives whose key the name is to
// write, or "" for none, which keeps the key that the name writes; and the ID
// of each node that a node reference names, of the node that it is to name
// instead. An entity that Moves leaves out is one of none, and a node that it
// leaves out one that the reference names still.
type Moves struct {
	Variables, Components, Attributes, Nodes map[string]string
}

// AttributeRef returns the key of Moves.Attributes of the attribute definition
// whose ID is definition of the component whose ID is component, each in its
// canonical spelling: the two joined by a slash, as package clipboard keys
// what each component's attribute definitions resolve to, since the
// components of a type share its definitions, but may resolve to components
// of different types.
func AttributeRef(component, definition string) string {
	return component + "/" + definition
}

// Relocate returns the source, of the context ctx, of an expression of the
// board fromBoard of the project from, rewritten for the board toBoard of
// the project to, which may be another project, as package clipboard pastes
// it (PRD FR-EDIT-07): each name that names an entity in from at fromBoard,
// a variable or a component's attribute, is written as the shortest name that
// names the entity that moves gives it in to at toBoard, as Rewrite writes a
// renamed entity's, an attribute's with the key of the definition that moves
// gives it, which may have been renamed since, or else with the key that the
// name writes, or, where moves gives it none, explicitly, as global.<key>
// or board.<key> of a variable, as it is global or of a board, and as
// cmp.<component>.<attribute> of an attribute, so that it names an entity of
// that key alone, once to has one, and never one of another scope or kind
// that has the key; <key>.<attribute> whose key a variable has in from, which
// names the variable, as no component, though it has no attribute, is written
// with the key of the variable that moves gives it, so that it names no
// component of the key in to, and stays as it is where moves gives it none, as
// no name names a variable's attribute explicitly (shadowing); each node
// reference to a node that moves gives another is written as a reference to
// that one, as node("<ID>"); and every other name stays as the source writes
// it, as one that names nothing in from. A name written with white space or
// comments between its parts keeps them, as respell says, a variable's
// qualifier that it keeps for a comment naming the scope of the variable that
// it is to name (respellVariable), but a node reference is written whole. It
// returns the source as it is if it does not parse, as a source that does not
// parse names nothing, which Rewrite leaves as it is too, or if no name of it
// changes.
//
// It returns an error, the caller's mistake or its own, never the source's,
// if to lacks an entity that moves gives, or if the rewritten source does not
// parse, or the names of the source that it returns, rewritten or as it is,
// do not name in to what moves gives.
func Relocate(source string, ctx Context, from *Project, fromBoard string, to *Project, toBoard string,
	moves Moves) (string, error) {
	if from == nil || to == nil {
		return "", fmt.Errorf("%w: relocating a source needs the project that it is of and the one that it goes to",
			ErrProject)
	}
	n, err := Parse(source, ctx)
	var e *Error
	switch {
	case errors.As(err, &e):
		return source, nil
	case err != nil:
		return "", err
	}
	names := relocated(n)
	var runes []rune
	var edits []Edit
	// want is what each name of the rewritten source names in to, by its
	// index, as the names of the source are written, "" for a name that
	// names nothing in from, which is left as it is and may name anything.
	want := make([]string, len(names))
	for i, m := range names {
		written, ref, err := moved(m, from, fromBoard, to, toBoard, moves)
		if err != nil {
			return "", err
		}
		want[i] = ref
		if written == "" {
			continue
		}
		if runes == nil {
			runes = []rune(source)
		}
		r := m.Range()
		old := string(runes[r.Start:r.End])
		switch m.(type) {
		case *NodeRef:
		case *Var:
			written = respellVariable(old, written, to.variable(ref))
		default:
			written = respell(old, written)
		}
		if old != written {
			edits = append(edits, Edit{Range: r, Old: old, New: written})
		}
	}
	// The names are checked whether or not one of them changes, so that one
	// that is spelled as the source writes it names what moves gives it too,
	// or the paste fails, rather than name something else.
	rewritten := source
	if len(edits) > 0 {
		rewritten = splice(runes, edits)
		got, err := Parse(rewritten, ctx)
		if err != nil {
			return "", fmt.Errorf("expr: the source that a paste rewrote does not parse: %w", err)
		}
		if names = relocated(got); len(names) != len(want) {
			return "", fmt.Errorf("expr: the source that a paste rewrote has %d names, where it had %d", len(names),
				len(want))
		}
	}
	for i, m := range names {
		if want[i] == "" {
			continue
		}
		if now := movedRef(m, to, toBoard); now != want[i] {
			return "", fmt.Errorf("expr: a paste rewrote a name of %s as one of %q", want[i], now)
		}
	}
	return rewritten, nil
}

// moved returns the name that Relocate writes in place of the name m, a
// *Var, an *Attr, or a *NodeRef, or "" for one that it leaves as it is, and
// what the name it writes names in to at toBoard, as movedRef reads it, or ""
// for a name that names nothing in from, or that is written explicitly for
// an entity that moves gives none, which may name anything.
func moved(m Node, from *Project, fromBoard string, to *Project, toBoard string, moves Moves) (written, ref string,
	err error) {
	switch m := m.(type) {
	case *Var:
		v := from.lookupVariable(fromBoard, m.Scope, m.Key)
		if v == nil {
			return "", "", nil
		}
		target := moves.Variables[v.ID]
		if target == "" {
			if v.Board == "" {
				return "global." + v.Key, "", nil
			}
			return "board." + v.Key, "", nil
		}
		t := to.variable(target)
		if t == nil {
			return "", "", fmt.Errorf("%w: the project that a paste goes to lacks the variable %s", ErrProject, target)
		}
		if t.Board != "" && t.Board != toBoard {
			return "", "", fmt.Errorf("%w: the variable %s is of another board than the one that a paste goes to",
				ErrProject, target)
		}
		return to.variableName(toBoard, t), t.ID, nil
	case *Attr:
		c := from.lookupComponent(fromBoard, m.Explicit, m.ComponentKey)
		if c == nil {
			return shadowing(m, from, fromBoard, to, toBoard, moves)
		}
		target := moves.Components[c.ID]
		if target == "" {
			return "cmp." + c.Key + "." + m.AttributeKey, "", nil
		}
		t := to.componentIDs[target]
		if t == nil {
			return "", "", fmt.Errorf("%w: the project that a paste goes to lacks the component %s", ErrProject,
				target)
		}
		key := m.AttributeKey
		if a := c.attributes[m.AttributeKey]; a != nil {
			if def := moves.Attributes[AttributeRef(c.ID, a.ID)]; def != "" {
				d := t.byID[def]
				if d == nil {
					return "", "", fmt.Errorf("%w: the component %s that a paste goes to lacks the attribute "+
						"definition %s", ErrProject, target, def)
				}
				key = d.Key
			}
		}
		return to.attributeName(toBoard, t.Key, key), t.ID + "." + key, nil
	case *NodeRef:
		target, ok := moves.Nodes[m.Ref]
		if !ok || target == "" {
			return "", "", nil
		}
		return `node("` + target + `")`, target, nil
	}
	return "", "", nil
}

// respellVariable returns the name written, of the variable v, which Relocate
// writes in place of old, a variable's name as the source writes it, as
// respell spells it, unless respell keeps old's qualifier, global. or board.,
// with the comment that follows it, which names the scope of the variable that
// old named where the source was copied, while v may be of the other, as a
// variable whose scope has changed since is, which a paste resolves to by its
// ID (variable.set_scope): the name is spelled then as v's of its own scope,
// global.<key> or board.<key>, which keeps the comment too, so that it names
// v, rather than nothing, or a variable of the other scope. v is nil for a
// name that names nothing in to, which respell spells.
func respellVariable(old, written string, v *Variable) string {
	spelled := respell(old, written)
	if v == nil || spelled == written || strings.Contains(written, ".") {
		return spelled
	}
	if v.Board == "" {
		return respell(old, "global."+v.Key)
	}
	return respell(old, "board."+v.Key)
}

// shadowing returns what moved returns for the name m, <key>.<attribute>,
// that names no component in from at fromBoard: where a variable of the
// board or a global one has the key, the name names that variable, as no
// component (Names), though a variable has no attribute
// (VariableNotComponent), and is written with the key of the variable that
// moves gives it, which names that variable in to at toBoard, rather than a
// component of the key that the name writes, unless it is a global variable
// that a variable of toBoard of its key hides, which the name names then, a
// variable all the same, as no name names a global variable's attribute. It
// returns "" for a name that names nothing, and for one whose variable moves
// gives none, which stays as it is, as no name names a variable's attribute
// explicitly, and names a component of its key in to where no variable there
// has the key.
func shadowing(m *Attr, from *Project, fromBoard string, to *Project, toBoard string, moves Moves) (written,
	ref string, err error) {
	if m.Explicit {
		return "", "", nil
	}
	v := from.lookupVariable(fromBoard, ScopeNone, m.ComponentKey)
	if v == nil || moves.Variables[v.ID] == "" {
		return "", "", nil
	}
	target := moves.Variables[v.ID]
	t := to.variable(target)
	switch {
	case t == nil:
		return "", "", fmt.Errorf("%w: the project that a paste goes to lacks the variable %s", ErrProject, target)
	case t.Board != "" && t.Board != toBoard:
		return "", "", fmt.Errorf("%w: the variable %s is of another board than the one that a paste goes to",
			ErrProject, target)
	}
	if to.lookupVariable(toBoard, ScopeNone, t.Key) == t {
		ref = t.ID
	}
	return t.Key + "." + m.AttributeKey, ref, nil
}

// movedRef returns what the name m, a *Var, an *Attr, or a *NodeRef, names in
// p at the board, as moved writes what it is to name: a variable's ID, of
// <key>.<attribute> whose key a variable has too (shadowing), a component's
// ID and the attribute's key after a dot, or the node's ID, or "" for a name
// that names nothing.
func movedRef(m Node, p *Project, board string) string {
	switch m := m.(type) {
	case *NodeRef:
		return m.Ref
	case *Attr:
		if !m.Explicit && p.shadowed(board, m.ComponentKey) {
			return p.lookupVariable(board, ScopeNone, m.ComponentKey).ID
		}
	}
	return nameRef(m, p, board)
}

// relocated returns the names of the AST n that Relocate may rewrite, its
// *Var, *Attr, and *NodeRef nodes, in the order of the source.
func relocated(n Node) []Node {
	var out []Node
	Walk(n, func(n Node) {
		switch n.(type) {
		case *Var, *Attr, *NodeRef:
			out = append(out, n)
		}
	})
	slices.SortFunc(out, func(a, b Node) int { return a.Range().Start - b.Range().Start })
	return out
}
