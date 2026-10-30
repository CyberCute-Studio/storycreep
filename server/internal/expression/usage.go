package expression

import (
	"context"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/dependency"
	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/ident"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// Subject is a variable or a component of the batch's project whose usages
// InUse finds, as expressions name it: Kind is ids.Variable or ids.Component,
// ID its UUID, Key its key, and Board the board of a board's variable, zero
// for a global variable and a component.
//
// Absent says that the project does not hold the subject outside its trash
// as InUse reads it, as a variable that a create has yet to add, or one in
// the trash that a restore brings back: InUse finds what would use it if the
// project held it, a variable of the type Type, or a component of the
// component type ComponentType, with the attribute definitions that are not
// deleted themselves, in the trash with the type or not, as restoring the
// component restores its type. An absent subject that the project could not
// hold, as one whose key or ID another entity of its scope has, which the
// command that would add it refuses, is used by nothing.
type Subject struct {
	Kind          ids.Kind
	ID            ids.UUID
	Key           string
	Board         ids.UUID
	Absent        bool
	Type          string
	ComponentType ids.UUID
}

// InUse returns the sites of the batch's project outside its trash that use
// the subject, as the batch's commands before the one that calls it leave the
// project (FR-VAR-04, FR-COMP-04): the expressions that, compiled at their
// owners' boards against the project as it stands, compile and read or write
// the subject, a variable or a component's attribute, as the usage lists would
// list them once the batch had compiled them (dependency.Uses), and the texts
// whose value references show it or that mention the component
// (dependency.TextsUsing), and, for a component, the nodes whose speaker it is
// (dependency.Speakers). An expression is read only if its names
// (expression_name) hold the subject's key or its UUID, or the batch has yet
// to compile it and its source holds the key, as any source that names the
// subject does, as dependency.Usages lists the others as their compilation
// did; and an expression that does not compile uses nothing, as the usage
// lists say.
func InUse(ctx context.Context, tx *commands.Tx, s Subject) ([]dependency.Found, error) {
	q := queries.New(tx)
	ws, project := nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID())
	found, err := expressionsUsing(ctx, q, ws, project, s)
	if err != nil {
		return nil, err
	}
	texts, err := dependency.TextsUsing(ctx, tx, s.Kind, s.ID)
	if err != nil {
		return nil, err
	}
	found = append(found, texts...)
	if s.Kind == ids.Component {
		speakers, err := dependency.Speakers(ctx, q, ws, project, s.ID)
		if err != nil {
			return nil, err
		}
		found = append(found, speakers...)
	}
	return found, nil
}

// Used reports whether sites of the batch's project use the subject s
// (InUse), as the inverse of a command that adds s to the project asks, whose
// deletion undoes it, forced exactly then (FR-VAR-04).
func Used(ctx context.Context, tx *commands.Tx, s Subject) (bool, error) {
	found, err := InUse(ctx, tx, s)
	return len(found) > 0, err
}

// GuardDeletion returns nil if the command that deletes the subject s, which
// the project holds outside its trash, may delete it: if no site of the
// project uses it, as the batch's commands before the command leave the
// project (InUse), or if the payload's field force is set, which the actor's
// role must allow (dependency.CheckForce). It refuses the batch at force with
// a *dependency.InUseError otherwise (FR-VAR-04 AC1, FR-COMP-04 AC1). A
// forced deletion leaves the expressions that named s to the derivation,
// which compiles them again, invalid, as each of them names what no longer
// resolves (FR-VAR-04 AC2).
func GuardDeletion(ctx context.Context, tx *commands.Tx, s Subject, force bool) error {
	found, err := InUse(ctx, tx, s)
	switch {
	case err != nil || len(found) == 0:
		return err
	case force:
		return dependency.CheckForce(ctx, tx, "force")
	}
	refusal, err := dependency.Refusal(ctx, queries.New(tx), nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID()),
		s.Kind, found)
	if err != nil {
		return err
	}
	return commands.Refuse("force", fmt.Sprintf("is not set, but %d sites of the project use the %s, which its "+
		"deletion would leave naming what does not resolve: force the deletion to delete it all the same",
		refusal.Count, s.Kind), refusal)
}

// expressionsUsing returns the expressions of the project outside its trash
// that use the subject s as InUse says.
func expressionsUsing(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, s Subject) (
	[]dependency.Found, error) {
	rows, err := q.NamingExpressions(ctx, queries.NamingExpressionsParams{WorkspaceID: ws, ProjectID: project,
		BoardID: nullUUID(s.Board), Refs: []pgtype.UUID{nullUUID(s.ID)}, Keys: []string{s.Key}, Ids: list(nil)})
	if err != nil {
		return nil, fmt.Errorf("expression: read the expressions that may use the %s: %w", s.Kind, err)
	}
	rows = slices.DeleteFunc(rows, func(r queries.NamingExpressionsRow) bool { return !r.Live })
	if len(rows) == 0 {
		return nil, nil
	}
	parsed := make([]parse, len(rows))
	var nodes []pgtype.UUID
	for i, row := range rows {
		parsed[i] = parseSource(row.Source, expr.Context(row.Context))
		nodes = append(nodes, parsed[i].nodes...)
	}
	n, err := loadNames(ctx, q, ws, project, nodes, nil)
	if err != nil {
		return nil, err
	}
	if s.Absent {
		held, err := n.with(ctx, q, ws, project, s)
		if err != nil || !held {
			return nil, err
		}
	}
	ps, err := n.build()
	if err != nil {
		return nil, err
	}
	var out []dependency.Found
	for i, row := range rows {
		c, err := parsed[i].compile(ps.live, ids.Encode(ids.Board, row.BoardID.Bytes))
		if err != nil {
			return nil, fmt.Errorf("expression: compile the expression %s: %w", ids.UUID(row.ID.Bytes), err)
		}
		o := namingOwner(row)
		site := siteOf(o.Kind, Field(row.Field))
		var connection, output ids.UUID
		switch o.Kind {
		case ids.Connection:
			connection = o.ID.Bytes
		case ids.BranchOutput:
			output = o.ID.Bytes
		}
		for _, u := range c.uses {
			if u.Target != s.ID || (s.Kind == ids.Variable) != (u.Kind == dependency.Variable) {
				continue
			}
			out = append(out, dependency.Found{Use: string(u.Access), Usage: dependency.ExpressionUsage(site,
				row.BoardID.Bytes, row.SiteNodeID.Bytes, connection, output, row.ID.Bytes, u.Attribute)})
		}
	}
	return out, nil
}

// with adds the absent subject s to the names n, and reports whether it did:
// not for a subject whose key is no identifier, or whose ID, or whose key in
// its scope, another variable or component of n has, which the command that
// would add it refuses.
func (n *names) with(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, s Subject) (bool, error) {
	if ident.Check(s.Key) != nil {
		return false, nil
	}
	id := ids.Encode(s.Kind, s.ID)
	switch s.Kind {
	case ids.Variable:
		board := ""
		if s.Board != (ids.UUID{}) {
			board = ids.Encode(ids.Board, s.Board)
		}
		if slices.ContainsFunc(n.variables, func(v expr.Variable) bool {
			return v.ID == id || v.Key == s.Key && v.Board == board
		}) {
			return false, nil
		}
		n.variables = append(slices.Clone(n.variables), expr.Variable{ID: id, Key: s.Key, Type: s.Type,
			Board: board})
	case ids.Component:
		if slices.ContainsFunc(n.components, func(c expr.Component) bool { return c.ID == id || c.Key == s.Key }) {
			return false, nil
		}
		rows, err := q.ExpressionTypeAttributes(ctx, queries.ExpressionTypeAttributesParams{WorkspaceID: ws,
			ProjectID: project, ComponentTypeID: nullUUID(s.ComponentType)})
		if err != nil {
			return false, fmt.Errorf("expression: read the attribute definitions of the component's type: %w", err)
		}
		attributes := make([]expr.Attribute, len(rows))
		for i, a := range rows {
			attributes[i] = expr.Attribute{ID: ids.Encode(ids.AttributeDef, a.ID.Bytes), Key: a.Key, Type: a.Type,
				Runtime: a.IsRuntime, Options: a.EnumOptions}
		}
		n.components = append(slices.Clone(n.components), expr.Component{ID: id, Key: s.Key, Attributes: attributes})
	default:
		return false, fmt.Errorf("expression: a %s is no subject of usages", s.Kind)
	}
	return true, nil
}

// siteOf returns the type of the site of an expression of the field of an
// owner of the kind, as a usage list names it (dependency.Usage.Site).
func siteOf(kind ids.Kind, f Field) string {
	switch kind {
	case ids.Node:
		return "node_" + string(f)
	case ids.Connection:
		return "connection_" + string(f)
	}
	return dependency.SiteBranchOutputCondition
}
