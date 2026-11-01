package expr

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/CyberCute-Studio/storycreep/server/internal/ident"
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
// may make name the renamed entity, or one whose name its old key shadowed
// (Captures). A name that the source writes with white space or comments
// between its parts keeps them, as respell says, so that a rename loses no
// comment. It returns the source rewritten and its edits, in the order of
// their ranges, or the source as it is and no edits if it does not parse, as
// a name of it names nothing, or if no name of it changes.
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
	names := namesOf(n)
	// runes is the source's characters, which the ranges count, read once a
	// name changes.
	var runes []rune
	var edits []Edit
	for _, m := range names {
		ref, written, err := shortest(m, before, after, board, renamed)
		if err != nil {
			return "", nil, err
		}
		if written == "" {
			continue
		}
		if runes == nil {
			runes = []rune(source)
		}
		r := m.Range()
		old := string(runes[r.Start:r.End])
		if written = respell(old, written); old == written {
			continue
		}
		// The edits come in the order of the names, that of their ranges.
		edits = append(edits, Edit{Range: r, Old: old, New: written, Ref: ref, Qualified: ref != renamed})
	}
	if len(edits) == 0 {
		return source, nil, nil
	}
	rewritten := splice(runes, edits)
	if err := sameNames(names, rewritten, ctx, before, after, board); err != nil {
		return "", nil, err
	}
	return rewritten, edits, nil
}

// splice returns the source whose characters are runes with the characters of
// each edit's range written as its New, the edits in the order of their
// ranges, none of which overlaps another, as Rewrite and Relocate write the
// names that they change.
func splice(runes []rune, edits []Edit) string {
	out := make([]rune, 0, len(runes))
	at := 0
	for _, ed := range edits {
		out = append(append(out, runes[at:ed.Range.Start]...), []rune(ed.New)...)
		at = ed.Range.End
	}
	return string(append(out, runes[at:]...))
}

// respell returns the name written, the parts of a name joined by dots, as
// Rewrite writes it in place of old, the name as a source writes it, which
// may have white space and comments between its parts, around each dot,
// that a part of the source written on several lines holds: each part of old
// gives way to a part of written, with what follows it up to the next part
// as it is. A name written with more parts than old, as global.trust of rep,
// or cmp.x.trust of elara.trust, has its first parts in place of old's first
// part, and one with fewer, as trust of global.rep, or ela.trust of
// cmp.elara.trust, drops old's first parts, the qualifiers that it no longer
// needs, global., board., or cmp., with what follows each, unless that holds
// a comment, which the name keeps then with its qualifier, as global.trust,
// which names what trust names, so that a rename loses no comment. A name
// written without white space, as most are, is written as written.
func respell(old, written string) string {
	if !strings.ContainsAny(old, " \t\r\n/") {
		return written
	}
	parts, gaps := nameParts(old)
	to := strings.Split(written, ".")
	if drop := len(parts) - len(to); drop > 0 {
		if slices.ContainsFunc(gaps[:drop], func(g string) bool { return strings.Contains(g, "//") }) {
			to = append(slices.Clone(parts[:drop]), to...)
		} else {
			gaps = gaps[drop:]
		}
	}
	// to has as many parts as old has left, or more, which go first.
	first := len(to) - len(gaps)
	var b strings.Builder
	b.WriteString(strings.Join(to[:first], "."))
	for i, g := range gaps {
		b.WriteString(g)
		b.WriteString(to[first+i])
	}
	return b.String()
}

// nameParts returns the parts of the name, as a source writes it, its
// identifiers, and what is between each part and the next, a dot with the
// white space and the comments around it, of which a comment runs to the
// line break that ends it, before the next part, and may hold dots and
// words of its own.
func nameParts(name string) (parts, gaps []string) {
	after := 0
	for i := 0; i < len(name); {
		switch {
		case strings.HasPrefix(name[i:], "//"):
			if end := strings.IndexAny(name[i:], "\r\n"); end >= 0 {
				i += end
			} else {
				i = len(name)
			}
		case ident.IsPart(name[i]):
			end := i
			for end < len(name) && ident.IsPart(name[end]) {
				end++
			}
			if len(parts) > 0 {
				gaps = append(gaps, name[after:i])
			}
			parts = append(parts, name[i:end])
			i, after = end, end
		default:
			i++
		}
	}
	return parts, gaps
}

// Captures reports whether a name of the source, of the context ctx, of an
// expression of the board whose ID is board, that names nothing in the
// project before names something in the project after, which is before with
// the renamed entity's key changed alone, as Rewrite takes them: a name that
// Rewrite leaves as it is, which the rename makes name either the renamed
// entity, by its new key, as a bare key that named no variable until a
// variable is renamed to it, or an entity whose name the renamed entity's
// old key shadowed, as <key>.<attribute> of a component of the key of a
// variable renamed away from it. A source that does not parse names
// nothing, and captures nothing.
func Captures(source string, ctx Context, before, after *Project, board string) (bool, error) {
	if before == nil || after == nil {
		return false, fmt.Errorf("%w: a rename needs the project before it and after it", ErrProject)
	}
	n, err := Parse(source, ctx)
	var e *Error
	switch {
	case errors.As(err, &e):
		return false, nil
	case err != nil:
		return false, err
	}
	for _, m := range namesOf(n) {
		if nameRef(m, before, board) == "" && nameRef(m, after, board) != "" {
			return true, nil
		}
	}
	return false, nil
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
