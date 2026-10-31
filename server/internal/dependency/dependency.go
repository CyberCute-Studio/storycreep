// Package dependency keeps the index of what a project's content reads and
// writes (PRD FR-DEP-01, §5.3 expression_ref, plan note N53), and answers
// the usage queries of a variable and of a component from it (FR-DEP-01
// AC2, FR-COMP-04): which sites read a variable, and which write it, and
// which nodes have a component as their speaker, which texts mention it, and
// which sites read or write its attributes.
//
// A site is an expression of the project (package expression): a node's
// on-enter or on-exit script, a connection's condition or on-traverse
// script, or a branch output's condition; or the rich text of a node, a
// connection, or a component (package richtext): a node's body, a
// connection's label, or a component's description. The index holds a row
// of each thing that a site names, with how it uses it (Use). An
// expression's rows are what its AST names (Uses), which package expression
// writes as it compiles the expression (IndexExpressions), so that they are
// always what the expression compiles to, and an expression that does not
// compile names nothing. A text's rows are what its value references and
// mentions name (FR-TEXT-02, FR-TEXT-03), which the derivation that Register
// registers with the pipeline writes once every command of a batch has
// applied, for each node, connection, and component that the batch may have
// changed the text of. Both are written in the batch's transaction, so the
// index is always what its sites name, which a recomputation from scratch
// gives (FR-DEP-01 AC1, dependencytest.Drift).
//
// An attribute is named by its component and its definition together, so
// that elara.trust and mira.trust, of one definition, are different
// targets, as are elara.trust and elara.mood (FR-DEP-01 AC4). A site's rows
// are its own whether it is in the trash or not, and the usage queries pass
// over a site in the trash, as everything that reads the content does.
package dependency

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
)

// Kind is the kind of what a site names.
type Kind string

// The kinds of what a site names: a variable; an attribute of a component,
// by the component and its definition; a node, which an expression names in
// a node reference, as visited() reads; and a component that a text
// mentions.
const (
	Variable  Kind = "variable"
	Attribute Kind = "attribute"
	Node      Kind = "node"
	Component Kind = "component"
)

// Access is how a site uses what it names.
type Access string

// The accesses: a read, of a variable's or an attribute's value, of whether
// a node was visited, or of a mentioned component's name; and a write, of a
// variable or an attribute that a script assigns.
const (
	Read  Access = "read"
	Write Access = "write"
)

// Use is something that a site names, and how it uses it.
type Use struct {
	Kind Kind
	// Target is the UUID of the variable, the node, or the component, the
	// attribute's among them.
	Target ids.UUID
	// Attribute is the UUID of an attribute's definition, and zero for
	// another kind.
	Attribute ids.UUID
	Access    Access
}

// compare orders uses by their kinds, their targets, their attributes, and
// their accesses, in which order a site's uses are listed.
func compare(a, b Use) int {
	return cmp.Or(cmp.Compare(a.Kind, b.Kind), bytes.Compare(a.Target[:], b.Target[:]),
		bytes.Compare(a.Attribute[:], b.Attribute[:]), cmp.Compare(a.Access, b.Access))
}

// sorted returns uses sorted (compare), each once.
func sorted(uses []Use) []Use {
	slices.SortFunc(uses, compare)
	return slices.Compact(uses)
}

// Uses returns what the AST n names, each once, in order (compare): an AST
// that compiled, whose names Check resolved, or that expr.Decode read of an
// expression that compiled, whose names hold their refs. A condition reads
// every name that it holds. A script writes the target of each assignment,
// and reads it too when the assignment is one of +=, -=, *=, and /=, which
// reads the value that it updates, and reads every name in an assignment's
// value and in a call's arguments, as visited(node("nd_…")) reads the node.
// It returns an error for a name without its ref, or of a ref of another
// kind, which no AST that compiled holds.
func Uses(n expr.Node) ([]Use, error) {
	// The target of each assignment, and whether the assignment reads it.
	targets := map[expr.Node]bool{}
	expr.Walk(n, func(n expr.Node) {
		if a, ok := n.(*expr.Assign); ok {
			targets[a.Target] = a.Op != expr.OpAssign
		}
	})
	var uses []Use
	var err error
	add := func(kind Kind, ref, attribute string, written, read bool) {
		u := Use{Kind: kind}
		var e error
		switch kind {
		case Variable:
			u.Target, e = ids.Decode(ids.Variable, ref)
		case Node:
			u.Target, e = ids.Decode(ids.Node, ref)
		default:
			if u.Target, e = ids.Decode(ids.Component, ref); e == nil {
				u.Attribute, e = ids.Decode(ids.AttributeDef, attribute)
			}
		}
		if e != nil {
			err = errors.Join(err, fmt.Errorf("dependency: a %s of the AST is not resolved: %w", kind, e))
			return
		}
		if written {
			u.Access = Write
			uses = append(uses, u)
		}
		if read {
			u.Access = Read
			uses = append(uses, u)
		}
	}
	expr.Walk(n, func(n expr.Node) {
		reads, target := targets[n]
		switch n := n.(type) {
		case *expr.Var:
			add(Variable, n.Ref, "", target, reads || !target)
		case *expr.Attr:
			add(Attribute, n.Ref, n.Attribute, target, reads || !target)
		case *expr.NodeRef:
			add(Node, n.Ref, "", false, true)
		}
	})
	if err != nil {
		return nil, err
	}
	return sorted(uses), nil
}

// TextUses returns what the document doc names, each once, in order
// (compare): the variable, or the component's attribute, of each value
// reference, and the component of each mention, each read. It returns an
// error for an ID that is not one, which no document that richtext.Parse
// returned holds.
func TextUses(doc richtext.Doc) ([]Use, error) {
	var uses []Use
	var err error
	var blocks func([]richtext.Block)
	blocks = func(bs []richtext.Block) {
		for _, b := range bs {
			switch b := b.(type) {
			case *richtext.Paragraph:
				for _, in := range b.Inlines {
					var u Use
					var e error
					switch in := in.(type) {
					case *richtext.Mention:
						u, e = textUse("mention", in.Component, "")
					case *richtext.Value:
						u, e = textUse("valueRef", in.Ref, in.Attribute)
					default:
						continue
					}
					err = errors.Join(err, e)
					uses = append(uses, u)
				}
			case *richtext.List:
				for _, item := range b.Items {
					blocks(item.Blocks)
				}
			}
		}
	}
	blocks(doc.Blocks)
	if err != nil {
		return nil, err
	}
	return sorted(uses), nil
}

// textUse returns the use of a text's inline node of the type, a mention or a
// value reference, of the ID ref and, for a value reference to an attribute,
// of the attribute definition's ID attribute.
func textUse(typ, ref, attribute string) (Use, error) {
	u := Use{Access: Read}
	var err error
	switch {
	case typ == "mention":
		u.Kind = Component
		u.Target, err = ids.Decode(ids.Component, ref)
	case attribute == "":
		u.Kind = Variable
		u.Target, err = ids.Decode(ids.Variable, ref)
	default:
		u.Kind = Attribute
		if u.Target, err = ids.Decode(ids.Component, ref); err == nil {
			u.Attribute, err = ids.Decode(ids.AttributeDef, attribute)
		}
	}
	if err != nil {
		return Use{}, fmt.Errorf("dependency: a text's %s: %w", typ, err)
	}
	return u, nil
}

// InlineUse returns the use of a text's inline node, a mention or a value
// reference, as its JSON, which TextReferences reads of the stored text,
// holds it, as package validation reads the texts too.
func InlineUse(raw []byte) (Use, error) {
	var in struct {
		Type  string `json:"type"`
		Attrs struct {
			ID        string `json:"id"`
			Ref       string `json:"ref"`
			Attribute string `json:"attribute"`
		} `json:"attrs"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return Use{}, fmt.Errorf("dependency: read a text's reference: %w", err)
	}
	switch in.Type {
	case "mention":
		return textUse(in.Type, in.Attrs.ID, "")
	case "valueRef":
		return textUse(in.Type, in.Attrs.Ref, in.Attrs.Attribute)
	}
	return Use{}, fmt.Errorf("dependency: a text's reference is a %q", in.Type)
}

// Site is a site of the index (the package's doc): an expression, of the
// board, or the text of a node or a connection, of the board, or of a
// component, of none, with what it names.
type Site struct {
	// Expression, Node, Connection, and Component name the site: exactly
	// one of them.
	Expression, Node, Connection, Component ids.UUID
	Board                                   ids.UUID
	Uses                                    []Use
}

// row is a row of the index, as InsertReferences and DeleteReferences read
// it: each UUID in its text form, and a column that is null left out.
type row struct {
	Expression string `json:"expression_id,omitempty"`
	Node       string `json:"node_id,omitempty"`
	Connection string `json:"connection_id,omitempty"`
	Component  string `json:"component_id,omitempty"`
	Board      string `json:"board_id,omitempty"`
	Kind       Kind   `json:"target_kind"`
	Target     string `json:"target_id"`
	Attribute  string `json:"attribute_def_id,omitempty"`
	Access     Access `json:"access"`
}

// rows returns the rows of the index of sites.
func rows(sites []Site) []row {
	var out []row
	text := func(id ids.UUID) string {
		if id == (ids.UUID{}) {
			return ""
		}
		return id.String()
	}
	for _, s := range sites {
		for _, u := range s.Uses {
			out = append(out, row{Expression: text(s.Expression), Node: text(s.Node), Connection: text(s.Connection),
				Component: text(s.Component), Board: text(s.Board), Kind: u.Kind, Target: u.Target.String(),
				Attribute: text(u.Attribute), Access: u.Access})
		}
	}
	return out
}

// replace replaces the rows of the index of the project's sites that del
// names by their IDs, the expressions' and the texts' of the nodes, the
// connections, and the components, with the rows of sites, theirs: it
// deletes the rows that the sites no longer name (DeleteReferences), and
// adds those that they did not name before (InsertReferences), so that a
// site that names what it named, as a text whose edit kept its references,
// or an expression whose new AST names what the old one did, writes
// nothing.
func replace(ctx context.Context, q *queries.Queries, del queries.DeleteReferencesParams, sites []Site) error {
	rs := rows(sites)
	if rs == nil {
		rs = []row{}
	}
	data, err := json.Marshal(rs)
	if err != nil {
		return fmt.Errorf("dependency: encode the index's rows: %w", err)
	}
	del.Rows = data
	if err := q.DeleteReferences(ctx, del); err != nil {
		return fmt.Errorf("dependency: delete the rows of the index that the sites no longer name: %w", err)
	}
	if len(rs) == 0 {
		return nil
	}
	if err := q.InsertReferences(ctx, queries.InsertReferencesParams{WorkspaceID: del.WorkspaceID,
		ProjectID: del.ProjectID, Rows: data}); err != nil {
		return fmt.Errorf("dependency: add the index's rows: %w", err)
	}
	return nil
}

// IndexExpressions replaces the rows of the index of the expressions of the
// project that sites lists, each an expression's (Site.Expression) of its
// board, with what it now names (Uses), or none for one that does not
// compile, as package expression calls it, in a batch's transaction, for
// each expression whose compilation changed, which the cascade of an
// expression's deletion leaves none of.
func IndexExpressions(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, sites []Site) error {
	if len(sites) == 0 {
		return nil
	}
	exprs := make([]pgtype.UUID, len(sites))
	for i, s := range sites {
		if s.Expression == (ids.UUID{}) || s.Node != (ids.UUID{}) || s.Connection != (ids.UUID{}) ||
			s.Component != (ids.UUID{}) {
			return errors.New("dependency: IndexExpressions indexes expressions alone")
		}
		exprs[i] = nullUUID(s.Expression)
	}
	return replace(ctx, q, queries.DeleteReferencesParams{WorkspaceID: ws, ProjectID: project, ExpressionIds: exprs,
		NodeIds: list(nil), ConnectionIds: list(nil), ComponentIds: list(nil)}, sites)
}

// nullUUID returns id as a uuid column, null if id is zero.
func nullUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != (ids.UUID{})}
}

// list returns l, or an empty list for nil, which a query reads as an array
// of no element rather than as null.
func list(l []pgtype.UUID) []pgtype.UUID {
	if l == nil {
		return []pgtype.UUID{}
	}
	return l
}
