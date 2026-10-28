package expression

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// Name is the name of the derivation that compiles the project's expressions
// (commands.Derivation), which a command that sets a source checks that the
// registry has (Set).
const Name = "expression"

// Register registers in r the derivation that compiles, once every command
// of a batch has applied, the expressions whose sources the batch's commands
// wrote, and those that what the batch changed may compile otherwise, and
// increments the revision of each owner of an expression that it changes
// that the batch did not change already. The owners are nodes, connections,
// and branch outputs, so r has them registered already (node.Register,
// connection.Register).
func Register(r *commands.Registry) {
	commands.RegisterDerivation(r, commands.Derivation{Name: Name,
		Touches: []ids.Kind{ids.Node, ids.Connection, ids.BranchOutput}, Derive: derive})
}

// nameKinds are the kinds of entity whose changes may change what the
// project's expressions can name, but for the nodes, which an expression
// names only by their IDs (node references): their keys, their types, their
// scopes, whether they are in the trash, and, of a board, its variables and
// nodes, which are in the trash with it.
var nameKinds = []ids.Kind{ids.Variable, ids.Component, ids.AttributeDef, ids.ComponentType, ids.Board}

// derive compiles the expressions of the batch's project that changed leaves
// to compile: those whose sources its commands wrote, which have neither an
// AST nor an error yet; those whose sources name, by their IDs or by their
// keys, an entity that it changed, or a variable, a component, an attribute
// definition, or a node of a type or a board that it changed; and, if it
// changed a variable, a component, a definition, a type, or a board, every
// one that does not compile but names something, whose error, or the near
// matches that it suggests, may change with any of them. So an expression is
// always what compiling its source against the project as it is gives,
// whatever batches led there.
func derive(ctx context.Context, tx *commands.Tx, changed []commands.Revision) error {
	q := queries.New(tx)
	ws, project := nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID())
	byKind := map[ids.Kind][]pgtype.UUID{}
	done := map[commands.Ref]bool{}
	for _, c := range changed {
		byKind[c.Kind] = append(byKind[c.Kind], nullUUID(c.ID))
		done[c.Ref] = true
	}
	names := slices.ContainsFunc(nameKinds, func(k ids.Kind) bool { return len(byKind[k]) > 0 })
	refs, keys := append([]pgtype.UUID{}, byKind[ids.Node]...), []string{}
	if names {
		rows, err := q.ChangedNames(ctx, queries.ChangedNamesParams{WorkspaceID: ws, ProjectID: project,
			VariableIds: list(byKind[ids.Variable]), BoardIds: list(byKind[ids.Board]),
			ComponentIds: list(byKind[ids.Component]), TypeIds: list(byKind[ids.ComponentType]),
			AttributeIds: list(byKind[ids.AttributeDef])})
		if err != nil {
			return fmt.Errorf("expression: read what the batch changed of what expressions name: %w", err)
		}
		for _, row := range rows {
			refs = append(refs, row.ID)
			if row.Key.Valid {
				keys = append(keys, row.Key.String)
			}
		}
	}
	rows, err := q.ExpressionsToCompile(ctx, queries.ExpressionsToCompileParams{WorkspaceID: ws, ProjectID: project,
		Refs: refs, Keys: keys, Names: names})
	if err != nil {
		return fmt.Errorf("expression: read the expressions to compile: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	parsed := make([]parse, len(rows))
	var nodes []pgtype.UUID
	for i, row := range rows {
		parsed[i] = parseSource(row.Source, expr.Context(row.Context))
		nodes = append(nodes, parsed[i].nodes...)
	}
	p, err := loadProject(ctx, q, ws, project, nodes)
	if err != nil {
		return err
	}
	compiled := make([]compiledRow, len(rows))
	for i, row := range rows {
		c, err := parsed[i].compile(p, ids.Encode(ids.Board, row.BoardID.Bytes))
		if err != nil {
			return fmt.Errorf("expression: compile the expression %s: %w", ids.UUID(row.ID.Bytes), err)
		}
		c.ID = ids.UUID(row.ID.Bytes).String()
		compiled[i] = c
	}
	data, err := json.Marshal(compiled)
	if err != nil {
		return fmt.Errorf("expression: encode what the expressions compile to: %w", err)
	}
	owners, err := q.CompileExpressions(ctx, queries.CompileExpressionsParams{WorkspaceID: ws, ProjectID: project,
		Rows: data})
	if err != nil {
		return fmt.Errorf("expression: store what the expressions compile to: %w", err)
	}
	// An expression is part of its owner, so a change of it that the API
	// shows is a change of the owner, unless a command of the batch changed
	// the owner already, whose revision the batch's answer gives then, or the
	// owner is in the trash, which nothing changes meanwhile, so that the
	// undo of its deletion, which expects the revision that the deletion
	// left, still applies: its expressions are compiled as the others are,
	// and are current when it comes back.
	touched := map[ids.Kind][]pgtype.UUID{}
	for _, o := range owners {
		if !o.Shown {
			continue
		}
		kind, id := ids.BranchOutput, o.BranchOutputID
		switch {
		case o.NodeID.Valid:
			kind, id = ids.Node, o.NodeID
		case o.ConnectionID.Valid:
			kind, id = ids.Connection, o.ConnectionID
		}
		ref := commands.Ref{Kind: kind, ID: id.Bytes}
		if !done[ref] {
			done[ref] = true
			touched[kind] = append(touched[kind], id)
		}
	}
	return touch(ctx, tx, touched)
}

// touch increments the revisions of the owners touched, by their kinds, but
// of those in the trash, as the batch's actor's change, and reports each.
func touch(ctx context.Context, tx *commands.Tx, touched map[ids.Kind][]pgtype.UUID) error {
	q := queries.New(tx)
	ws, project, by := nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID()), nullUUID(tx.Actor().UserID)
	type revision struct {
		id  pgtype.UUID
		rev int64
	}
	for _, kind := range []ids.Kind{ids.Node, ids.Connection, ids.BranchOutput} {
		owners := touched[kind]
		if len(owners) == 0 {
			continue
		}
		var revs []revision
		switch kind {
		case ids.Node:
			rows, err := q.TouchNodes(ctx, queries.TouchNodesParams{UpdatedBy: by, WorkspaceID: ws, ProjectID: project,
				Ids: owners})
			if err != nil {
				return fmt.Errorf("expression: change the nodes' revisions: %w", err)
			}
			for _, r := range rows {
				revs = append(revs, revision{r.ID, r.Rev})
			}
		case ids.Connection:
			rows, err := q.TouchConnections(ctx, queries.TouchConnectionsParams{UpdatedBy: by, WorkspaceID: ws,
				ProjectID: project, Ids: owners})
			if err != nil {
				return fmt.Errorf("expression: change the connections' revisions: %w", err)
			}
			for _, r := range rows {
				revs = append(revs, revision{r.ID, r.Rev})
			}
		default:
			rows, err := q.TouchBranchOutputs(ctx, queries.TouchBranchOutputsParams{UpdatedBy: by, WorkspaceID: ws,
				ProjectID: project, Ids: owners})
			if err != nil {
				return fmt.Errorf("expression: change the outputs' revisions: %w", err)
			}
			for _, r := range rows {
				revs = append(revs, revision{r.ID, r.Rev})
			}
		}
		// The rows come back in no order, and the batch's answer lists its
		// changes in the order that they are reported.
		slices.SortFunc(revs, func(a, b revision) int { return bytes.Compare(a.id.Bytes[:], b.id.Bytes[:]) })
		for _, r := range revs {
			if err := tx.Changed(kind, r.id.Bytes, r.rev); err != nil {
				return err
			}
		}
	}
	return nil
}

// Touch increments the revision of the owner o of the batch's project, whose
// expression the command changed alone, of its fields, and returns its new
// revision, as a command's Apply does once Set reports a change and nothing
// else of the owner changes.
func Touch(ctx context.Context, tx *commands.Tx, o Owner) (int64, error) {
	q := queries.New(tx)
	ws, project, by := nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID()), nullUUID(tx.Actor().UserID)
	ownerIDs := []pgtype.UUID{o.ID}
	var revs []int64
	switch o.Kind {
	case ids.Node:
		rows, err := q.TouchNodes(ctx, queries.TouchNodesParams{UpdatedBy: by, WorkspaceID: ws, ProjectID: project,
			Ids: ownerIDs})
		for _, r := range rows {
			revs = append(revs, r.Rev)
		}
		if err != nil {
			return 0, fmt.Errorf("expression: change the node's revision: %w", err)
		}
	case ids.Connection:
		rows, err := q.TouchConnections(ctx, queries.TouchConnectionsParams{UpdatedBy: by, WorkspaceID: ws,
			ProjectID: project, Ids: ownerIDs})
		for _, r := range rows {
			revs = append(revs, r.Rev)
		}
		if err != nil {
			return 0, fmt.Errorf("expression: change the connection's revision: %w", err)
		}
	case ids.BranchOutput:
		rows, err := q.TouchBranchOutputs(ctx, queries.TouchBranchOutputsParams{UpdatedBy: by, WorkspaceID: ws,
			ProjectID: project, Ids: ownerIDs})
		for _, r := range rows {
			revs = append(revs, r.Rev)
		}
		if err != nil {
			return 0, fmt.Errorf("expression: change the output's revision: %w", err)
		}
	default:
		return 0, fmt.Errorf("expression: a %s has no expressions", o.Kind)
	}
	if len(revs) != 1 {
		return 0, fmt.Errorf("expression: changing the revision of the %s changed %d rows", o.Kind, len(revs))
	}
	return revs[0], nil
}

// loadProject returns what the project's expressions can name (§5.8.3): its
// variables, its components with their attributes, and those of nodes that
// are not in its trash, which the expressions that it compiles name.
func loadProject(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, nodes []pgtype.UUID) (
	*expr.Project, error) {
	variables, err := q.ExpressionVariables(ctx, queries.ExpressionVariablesParams{WorkspaceID: ws,
		ProjectID: project})
	if err != nil {
		return nil, fmt.Errorf("expression: read the project's variables: %w", err)
	}
	vs := make([]expr.Variable, len(variables))
	for i, v := range variables {
		vs[i] = expr.Variable{ID: ids.Encode(ids.Variable, v.ID.Bytes), Key: v.Key, Type: v.Type}
		if v.BoardID.Valid {
			vs[i].Board = ids.Encode(ids.Board, v.BoardID.Bytes)
		}
	}
	attributes, err := q.ExpressionAttributes(ctx, queries.ExpressionAttributesParams{WorkspaceID: ws,
		ProjectID: project})
	if err != nil {
		return nil, fmt.Errorf("expression: read the project's attribute definitions: %w", err)
	}
	ofType := map[ids.UUID][]expr.Attribute{}
	for _, a := range attributes {
		ofType[a.ComponentTypeID.Bytes] = append(ofType[a.ComponentTypeID.Bytes], expr.Attribute{
			ID: ids.Encode(ids.AttributeDef, a.ID.Bytes), Key: a.Key, Type: a.Type, Runtime: a.IsRuntime,
			Options: a.EnumOptions})
	}
	components, err := q.ExpressionComponents(ctx, queries.ExpressionComponentsParams{WorkspaceID: ws,
		ProjectID: project})
	if err != nil {
		return nil, fmt.Errorf("expression: read the project's components: %w", err)
	}
	cs := make([]expr.Component, len(components))
	for i, c := range components {
		cs[i] = expr.Component{ID: ids.Encode(ids.Component, c.ID.Bytes), Key: c.Key,
			Attributes: ofType[c.ComponentTypeID.Bytes]}
	}
	var ns []expr.BoardNode
	if len(nodes) > 0 {
		rows, err := q.ExpressionNodes(ctx, queries.ExpressionNodesParams{WorkspaceID: ws, ProjectID: project,
			Ids: nodes})
		if err != nil {
			return nil, fmt.Errorf("expression: read the nodes that expressions name: %w", err)
		}
		ns = make([]expr.BoardNode, len(rows))
		for i, n := range rows {
			ns[i] = expr.BoardNode{ID: ids.Encode(ids.Node, n.ID.Bytes), Jumper: n.Type == "jumper"}
		}
	}
	p, err := expr.NewProject(vs, cs, ns)
	if err != nil {
		return nil, fmt.Errorf("expression: what the project's expressions can name: %w", err)
	}
	return p, nil
}

// parse is a source as it parses: its AST, or the error of its parse, with
// the keys and the nodes that its AST names.
type parse struct {
	ctx   expr.Context
	ast   expr.Node
	err   error
	keys  []string
	nodes []pgtype.UUID
}

// parseSource parses source in the context ctx.
func parseSource(source string, ctx expr.Context) parse {
	n, err := expr.Parse(source, ctx)
	out := parse{ctx: ctx, ast: n, err: err}
	if err != nil {
		return out
	}
	walk(n, func(n expr.Node) {
		switch n := n.(type) {
		case *expr.Var:
			out.keys = append(out.keys, n.Key)
		case *expr.Attr:
			out.keys = append(out.keys, n.ComponentKey, n.AttributeKey)
		case *expr.NodeRef:
			// The parser writes a node reference's ID in its canonical
			// spelling.
			if u, err := ids.Decode(ids.Node, n.Ref); err == nil {
				out.nodes = append(out.nodes, nullUUID(u))
			}
		}
	})
	return out
}

// compiledRow is what compiling an expression's source gives, as
// CompileExpressions reads it: its AST and the AST's version, or its error,
// and the UUIDs and the keys that it names.
type compiledRow struct {
	ID         string          `json:"id"`
	AST        json.RawMessage `json:"ast"`
	ASTVersion *int            `json:"ast_version"`
	Error      *expr.Error     `json:"error"`
	Refs       []string        `json:"refs"`
	Keys       []string        `json:"keys"`
}

// compile checks the parsed source against the project p at the board, and
// returns what compiling it gives. Its error is a mistake of the parser's or
// of the checker's, never the source's, whose error the row holds.
func (s parse) compile(p *expr.Project, board string) (compiledRow, error) {
	out := compiledRow{Refs: []string{}, Keys: []string{}}
	if s.err != nil {
		var e *expr.Error
		if !errors.As(s.err, &e) {
			return out, s.err
		}
		out.Error = e
		return out, nil
	}
	refs := slices.Clone(s.nodes)
	err := expr.Check(s.ast, s.ctx, p, board)
	var e *expr.Error
	switch {
	case errors.As(err, &e):
		out.Error = e
	case err != nil:
		return out, err
	default:
		walk(s.ast, func(n expr.Node) {
			switch n := n.(type) {
			case *expr.Var:
				refs = appendID(refs, ids.Variable, n.Ref)
			case *expr.Attr:
				refs = appendID(refs, ids.Component, n.Ref)
				refs = appendID(refs, ids.AttributeDef, n.Attribute)
			}
		})
		if out.AST, err = expr.Encode(s.ast); err != nil {
			return out, err
		}
		v := expr.VersionOf(s.ast)
		out.ASTVersion = &v
	}
	slices.SortFunc(refs, func(a, b pgtype.UUID) int { return bytes.Compare(a.Bytes[:], b.Bytes[:]) })
	refs = slices.Compact(refs)
	for _, r := range refs {
		out.Refs = append(out.Refs, ids.UUID(r.Bytes).String())
	}
	if len(s.keys) > 0 {
		out.Keys = slices.Compact(slices.Sorted(slices.Values(s.keys)))
	}
	return out, nil
}

// appendID appends the UUID of id, an ID of the kind that a resolved name
// holds, to refs.
func appendID(refs []pgtype.UUID, kind ids.Kind, id string) []pgtype.UUID {
	if u, err := ids.Decode(kind, id); err == nil {
		refs = append(refs, nullUUID(u))
	}
	return refs
}

// walk calls f for n and each node under it, each node before its children,
// and the children in order. It recurses as deep as the AST is, which Parse
// bounds by its nodes, at most expr.MaxNodes.
func walk(n expr.Node, f func(expr.Node)) {
	if n == nil {
		return
	}
	f(n)
	switch n := n.(type) {
	case *expr.Unary:
		walk(n.Operand, f)
	case *expr.Binary:
		walk(n.Left, f)
		walk(n.Right, f)
	case *expr.Call:
		for _, a := range n.Args {
			walk(a, f)
		}
	case *expr.Assign:
		walk(n.Target, f)
		walk(n.Value, f)
	case *expr.Script:
		for _, s := range n.Statements {
			walk(s, f)
		}
	}
}

// list returns l, or an empty list for nil, which a query reads as an array
// of no element rather than as null.
func list(l []pgtype.UUID) []pgtype.UUID {
	if l == nil {
		return []pgtype.UUID{}
	}
	return l
}
