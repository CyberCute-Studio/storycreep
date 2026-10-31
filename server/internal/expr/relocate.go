package expr

import (
	"errors"
	"fmt"
	"slices"
)

// Named is what a name of a source names (Names): a variable, a component's
// attribute, or a node.
type Named struct {
	// Variable is the ID of the variable that a variable's name names.
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
// component, and each node reference, as package clipboard declares what a
// copied expression names. A name that names nothing is left out, and a
// source that does not parse names nothing.
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
			if c := p.lookupComponent(board, m.Explicit, m.ComponentKey); c != nil {
				named := Named{Component: c.ID, AttributeKey: m.AttributeKey}
				if a := c.attributes[m.AttributeKey]; a != nil {
					named.Attribute = a.ID
				}
				out = append(out, named)
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
// name where the source goes, or "" for none, and the ID of each node that a
// node reference names, of the node that it is to name instead. An entity
// that Moves leaves out is one of none, and a node that it leaves out one
// that the reference names still.
type Moves struct {
	Variables, Components, Nodes map[string]string
}

// Relocate returns the source, of the context ctx, of an expression of the
// board fromBoard of the project from, rewritten for the board toBoard of
// the project to, which may be another project, as package clipboard pastes
// it (PRD FR-EDIT-07): each name that names an entity in from at fromBoard,
// a variable or a component's attribute, is written as the shortest name that
// names the entity that moves gives it in to at toBoard, as Rewrite writes a
// renamed entity's, or, where moves gives it none, explicitly, as global.<key>
// or board.<key> of a variable, as it is global or of a board, and as
// cmp.<component>.<attribute> of an attribute, so that it names an entity of
// that key alone, once to has one, and never one of another scope or kind
// that has the key; each node reference to a node that moves gives another
// is written as a reference to that one, as node("<ID>"); and every other
// name stays as the source writes it, as one that names nothing in from. A
// name written with white space or comments between its parts keeps them, as
// respell says, but a node reference is written whole. It returns the source
// as it is if it does not parse, or if no name of it changes.
//
// It returns an error, the caller's mistake or its own, never the source's,
// if to lacks an entity that moves gives, or if the rewritten source does not
// parse, or its names do not name in to what moves gives.
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
	type edit struct {
		r       Range
		written string
	}
	var edits []edit
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
		if _, isNode := m.(*NodeRef); !isNode {
			written = respell(old, written)
		}
		if old != written {
			edits = append(edits, edit{r: r, written: written})
		}
	}
	if len(edits) == 0 {
		return source, nil
	}
	out := make([]rune, 0, len(runes))
	at := 0
	for _, ed := range edits {
		out = append(append(out, runes[at:ed.r.Start]...), []rune(ed.written)...)
		at = ed.r.End
	}
	rewritten := string(append(out, runes[at:]...))
	got, err := Parse(rewritten, ctx)
	if err != nil {
		return "", fmt.Errorf("expr: the source that a paste rewrote does not parse: %w", err)
	}
	names = relocated(got)
	if len(names) != len(want) {
		return "", fmt.Errorf("expr: the source that a paste rewrote has %d names, where it had %d", len(names),
			len(want))
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
			return "", "", nil
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
		return to.attributeName(toBoard, t.Key, m.AttributeKey), t.ID + "." + m.AttributeKey, nil
	case *NodeRef:
		target, ok := moves.Nodes[m.Ref]
		if !ok || target == "" {
			return "", "", nil
		}
		return `node("` + target + `")`, target, nil
	}
	return "", "", nil
}

// movedRef returns what the name m, a *Var, an *Attr, or a *NodeRef, names in
// p at the board, as moved writes what it is to name: a variable's ID, a
// component's ID and the attribute's key after a dot, or the node's ID, or ""
// for a name that names nothing.
func movedRef(m Node, p *Project, board string) string {
	if n, ok := m.(*NodeRef); ok {
		return n.Ref
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
