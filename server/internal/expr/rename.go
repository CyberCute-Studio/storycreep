package expr

import (
	"errors"
	"fmt"
	"slices"
)

// Edit is a change of one name of a source that a rename of an entity's key
// makes (Rewrite): the characters that the name covers in the source, what
// they are, and what they become, with the ID of the entity that the name
// names, before the rename and after it alike.
type Edit struct {
	Range    Range
	Old, New string
	// Ref is the ID of the entity that the name names: a variable's, or the
	// component's of an attribute.
	Ref string
	// Qualified says that the name names another entity than the renamed one,
	// whose name the renamed one's new key would take from it, as a bare key
	// that a variable of the board renamed to a global's key would name, and
	// which is written qualified to keep naming its entity, as global.<key>
	// or cmp.<component>.<attribute> (PRD FR-VAR-03).
	Qualified bool
}

// Rewrite returns the source, of the context ctx, of an expression of the
// board whose ID is board, or of none for "", rewritten for the rename of the
// key of the entity whose ID is renamed, a variable or a component, from the
// project before to the project after, which is before with that key changed
// alone (PRD FR-VAR-03): each name that names the renamed entity at the
// board in before, a variable's or an attribute's of the component, is
// written as the shortest name that names it there in after, as Shortest
// says, and each name that names another entity in before, but would name
// another or none in after, as a bare key that the new key of a variable of
// the board takes from a global variable, or <key>.<attribute> that it takes
// from a component, is written as the shortest name that names its entity in
// after, which is qualified. The other names stay as the source writes them,
// so that a source that the rename leaves naming what it named is the same
// source, and so does a name that names nothing in before, which the rename
// may make name the renamed entity. It returns the source rewritten and its
// edits, in the order of their ranges, or the source as it is and no edits if
// it does not parse, as a name of it names nothing, or if no name of it
// changes.
//
// It returns an error, the caller's mistake or its own, never the source's,
// if before or after lacks the renamed entity or another that a name names,
// or if the rewritten source does not parse, or names other entities than the
// source did in before, which a rename that changes nothing but the key
// never gives.
func Rewrite(source string, ctx Context, before, after *Project, board, renamed string) (string, []Edit, error) {
	if before == nil || after == nil {
		return "", nil, fmt.Errorf("%w: a rename needs the project before it and after it", ErrProject)
	}
	n, err := Parse(source, ctx)
	var e *Error
	switch {
	case errors.As(err, &e):
		return source, nil, nil
	case err != nil:
		return "", nil, err
	}
	runes := []rune(source)
	names := namesOf(n)
	var edits []Edit
	for _, m := range names {
		ref, written, err := shortest(m, before, after, board, renamed)
		if err != nil {
			return "", nil, err
		}
		if written == "" {
			continue
		}
		r := m.Range()
		old := string(runes[r.Start:r.End])
		if old == written {
			continue
		}
		edits = append(edits, Edit{Range: r, Old: old, New: written, Ref: ref, Qualified: ref != renamed})
	}
	if len(edits) == 0 {
		return source, nil, nil
	}
	slices.SortFunc(edits, func(a, b Edit) int { return a.Range.Start - b.Range.Start })
	out := make([]rune, 0, len(runes))
	at := 0
	for _, ed := range edits {
		out = append(append(out, runes[at:ed.Range.Start]...), []rune(ed.New)...)
		at = ed.Range.End
	}
	rewritten := string(append(out, runes[at:]...))
	if err := sameNames(names, rewritten, ctx, before, after, board); err != nil {
		return "", nil, err
	}
	return rewritten, edits, nil
}

// namesOf returns the names of the AST n, its *Var and *Attr nodes, in the
// order of the source.
func namesOf(n Node) []Node {
	var out []Node
	Walk(n, func(n Node) {
		switch n.(type) {
		case *Var, *Attr:
			out = append(out, n)
		}
	})
	slices.SortFunc(out, func(a, b Node) int { return a.Range().Start - b.Range().Start })
	return out
}

// shortest returns the ID of the entity that the name m, a *Var or an *Attr,
// names at the board in before, and the name that Rewrite writes it as, the
// shortest that names it in after, if m names the renamed entity, or names
// another that it would not name in after; and "" for a name that Rewrite
// leaves as it is.
func shortest(m Node, before, after *Project, board, renamed string) (ref, written string, err error) {
	switch m := m.(type) {
	case *Var:
		v := before.lookupVariable(board, m.Scope, m.Key)
		if v == nil {
			return "", "", nil
		}
		to := after.variable(v.ID)
		if to == nil {
			return "", "", fmt.Errorf("%w: the project after the rename lacks the variable %s", ErrProject, v.ID)
		}
		if v.ID != renamed && after.lookupVariable(board, m.Scope, m.Key) == to {
			return "", "", nil
		}
		return v.ID, after.variableName(board, to), nil
	case *Attr:
		c := before.lookupComponent(board, m.Explicit, m.ComponentKey)
		if c == nil {
			return "", "", nil
		}
		to := after.componentIDs[c.ID]
		if to == nil {
			return "", "", fmt.Errorf("%w: the project after the rename lacks the component %s", ErrProject, c.ID)
		}
		if c.ID != renamed && after.lookupComponent(board, m.Explicit, m.ComponentKey) == to {
			return "", "", nil
		}
		return c.ID, after.attributeName(board, to.Key, m.AttributeKey), nil
	}
	return "", "", nil
}

// sameNames returns an error unless the source rewritten, of the context ctx,
// parses, and its names name in after what the names of the source, names,
// named in before at the board: each the same entity, or, for one that named
// nothing, which Rewrite left as it is, anything.
func sameNames(names []Node, rewritten string, ctx Context, before, after *Project, board string) error {
	n, err := Parse(rewritten, ctx)
	if err != nil {
		return fmt.Errorf("expr: the source that a rename rewrote does not parse: %w", err)
	}
	got := namesOf(n)
	if len(got) != len(names) {
		return fmt.Errorf("expr: the source that a rename rewrote has %d names, where it had %d", len(got), len(names))
	}
	for i, m := range names {
		was, now := nameRef(m, before, board), nameRef(got[i], after, board)
		if was != "" && was != now {
			return fmt.Errorf("expr: a rename rewrote a name of %s as one of %q", was, now)
		}
	}
	return nil
}

// nameRef returns the ID of the entity that the name m names at the board
// in p, with an attribute's key after a component's ID, or "" if it names
// none.
func nameRef(m Node, p *Project, board string) string {
	switch m := m.(type) {
	case *Var:
		if v := p.lookupVariable(board, m.Scope, m.Key); v != nil {
			return v.ID
		}
	case *Attr:
		if c := p.lookupComponent(board, m.Explicit, m.ComponentKey); c != nil {
			return c.ID + "." + m.AttributeKey
		}
	}
	return ""
}

// variableName returns the shortest name that names the variable v at the
// board whose ID is board (§5.8.3, FR-VAR-03): its bare key, unless v is a
// global variable whose key a variable of the board has too, which the bare
// key names, and global.<key> names v then. A variable of another board has
// no name at the board, and its key is returned.
func (p *Project) variableName(board string, v *Variable) string {
	if v.Board == "" && p.ofBoard(board, v.Key) != nil {
		return "global." + v.Key
	}
	return v.Key
}

// attributeName returns the shortest name that names the attribute of the key
// attribute of the component of the key component at the board whose ID is
// board (§5.8.3, FR-VAR-03): <component>.<attribute>, unless a variable of
// the board or a global one has the component's key, which that name names
// then, and cmp.<component>.<attribute> names the attribute.
func (p *Project) attributeName(board, component, attribute string) string {
	if p.shadowed(board, component) {
		return "cmp." + component + "." + attribute
	}
	return component + "." + attribute
}
