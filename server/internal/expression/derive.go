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
		Touches: ownerKinds, Derive: derive})
}

// nameKinds are the kinds of entity whose changes may change what the
// project's expressions can name, and the near matches that an error
// suggests: their keys, their types, their scopes, and whether they are in
// the trash. The nodes, which an expression names only by their IDs (node
// references), and the boards, whose deletions and restorations change what
// expressions can name of the variables and the nodes that they hold, are
// read apart (derive).
var nameKinds = []ids.Kind{ids.Variable, ids.Component, ids.AttributeDef, ids.ComponentType}

// ownerKinds are the kinds of the owners of expressions, whose commands alone
// write sources.
var ownerKinds = []ids.Kind{ids.Node, ids.Connection, ids.BranchOutput}

// neutral are the types of command that change an entity of one of
// nameKinds, or a node, but neither what the project's expressions can name
// nor what a name of theirs resolves to: a board's creation, which holds
// neither variables nor nodes yet, and its name, description, colour, slug,
// place, and entry node; a node's update and its move, which leave as they
// are its type, which no command changes, and whether it is in the trash,
// all that a node reference reads; a variable's description, default,
// reset_on_enter, and place; a component's name, description, image, values,
// and place; an attribute definition's name, default, and place, which no
// near match depends on, as they come nearest first and then by their bytes;
// and a component type's creation, which holds neither definitions nor
// components yet, and its name, its key, which no expression names, and its
// icon. derive passes over a change that commands of these types alone made,
// so that a board's rename, or a variable's description, does not compile
// again every expression that names what it holds, or names it, nor every one
// that does not compile; it still compiles the sources that such a command
// wrote, as a node's update does its scripts. A type that is not here is
// taken to change what expressions can name, so that a new type of command
// compiles too much, never too little, until it is added here. The types are
// spelt out, rather than named by the constants of their packages, which
// import this one, or will, as package variable's rename will rewrite the
// sources that name the variable (plan item 47); TestNeutral checks that
// each is a registered type.
var neutral = []commands.Type{
	"board.create", "board.rename", "board.describe", "board.set_color", "board.set_slug", "board.move",
	"board.set_entry_node", "node.update", "node.move",
	"variable.describe", "variable.set_default", "variable.set_reset_on_enter", "variable.move",
	"component.rename", "component.describe", "component.set_image", "component.set_attribute", "component.move",
	"attribute.rename", "attribute.set_default", "attribute.move",
	"component_type.create", "component_type.rename", "component_type.set_key", "component_type.set_icon",
}

// derive compiles the expressions of the batch's project that changed leaves
// to compile: those whose sources its commands wrote, which have neither an
// AST nor an error yet; those whose sources name, by their IDs or by their
// keys, an entity that it changed, or a variable, a component, an attribute
// definition, or a node of a type that it changed; those of the other boards
// of a board that it deleted to the trash or restored whose sources name the
// keys of the board's variables or its nodes, whose deletion takes them out
// of what those expressions can name, and whose restoration brings them back
// (BoardExpressionsToCompile); and, if it changed a variable, a component, a
// definition, or a type, every one that does not compile but names
// something, whose error, or the near matches that it suggests, may change
// with any of them; but for a change that the commands of neutral types alone
// made, which changes none of them. An expression of a board in the trash is
// compiled as it would be once the board is restored (projects), so that a
// board's deletion and its restoration change none of the board's own
// expressions, which they do not compile, nor the near matches of another
// board's, which name its own board's variables and the global ones alone.
// So an expression is always what compiling its source against the project
// as it is gives, whatever batches led there.
func derive(ctx context.Context, tx *commands.Tx, changed []commands.Change) error {
	q := queries.New(tx)
	ws, project := nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID())
	byKind := map[ids.Kind][]pgtype.UUID{}
	done := map[commands.Ref]bool{}
	for _, c := range changed {
		done[c.Ref] = true
		if slices.ContainsFunc(c.By, func(t commands.Type) bool { return !slices.Contains(neutral, t) }) {
			byKind[c.Kind] = append(byKind[c.Kind], nullUUID(c.ID))
		}
	}
	names := slices.ContainsFunc(nameKinds, func(k ids.Kind) bool { return len(byKind[k]) > 0 })
	refs, keys, boards := append([]pgtype.UUID{}, byKind[ids.Node]...), []string{}, byKind[ids.Board]
	// A batch that wrote no source (leave), changed no node but as the
	// commands of neutral types do, which a node reference reads nothing of,
	// and changed nothing else that expressions can name leaves nothing to
	// compile, as one that only moves nodes, or reorders connections: it
	// reads nothing.
	if !tx.Left(Name) && len(refs) == 0 && !names && len(boards) == 0 {
		return nil
	}
	if names {
		rows, err := q.ChangedNames(ctx, queries.ChangedNamesParams{WorkspaceID: ws, ProjectID: project,
			VariableIds: list(byKind[ids.Variable]), ComponentIds: list(byKind[ids.Component]),
			TypeIds: list(byKind[ids.ComponentType]), AttributeIds: list(byKind[ids.AttributeDef])})
		if err != nil {
			return fmt.Errorf("expression: read what the batch changed of what expressions name: %w", err)
		}
		for _, row := range rows {
			refs, keys = append(refs, row.ID), append(keys, row.Key)
		}
	}
	rows, err := toCompile(ctx, q, ws, project, queries.ExpressionsToCompileParams{WorkspaceID: ws,
		ProjectID: project, Refs: refs, Keys: keys, Names: names}, boards)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	parsed := make([]parse, len(rows))
	var nodes, trashed []pgtype.UUID
	seen := map[pgtype.UUID]bool{}
	for i, row := range rows {
		parsed[i] = parseSource(row.Source, expr.Context(row.Context))
		if parsed[i].err != nil {
			continue
		}
		nodes = append(nodes, parsed[i].nodes...)
		if row.BoardDeleted && !seen[row.BoardID] {
			seen[row.BoardID] = true
			trashed = append(trashed, row.BoardID)
		}
	}
	// Only a source that parses is checked against what the project's
	// expressions can name, so a batch whose sources all fail to parse, as a
	// half-typed script saved as it is typed, reads none of it.
	var ps projects
	if slices.ContainsFunc(parsed, func(s parse) bool { return s.err == nil }) {
		if ps, err = loadProjects(ctx, q, ws, project, nodes, trashed); err != nil {
			return err
		}
	}
	compiled := make([]compiledRow, len(rows))
	for i, row := range rows {
		c, err := parsed[i].compile(ps.of(row.BoardID), ids.Encode(ids.Board, row.BoardID.Bytes))
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
	// owner is deleted to the trash itself, which nothing changes meanwhile,
	// so that the undo of its deletion, which expects the revision that the
	// deletion left, still applies: its expressions are compiled as the
	// others are, and are current when its restoration brings it back at a
	// new revision. An owner that is in the trash only with its board, its
	// node, or its output is changed as one that is not (touchOwners).
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
// of those deleted to the trash themselves (touchOwners), as the batch's
// actor's change, and reports each.
func touch(ctx context.Context, tx *commands.Tx, touched map[ids.Kind][]pgtype.UUID) error {
	for _, kind := range ownerKinds {
		if len(touched[kind]) == 0 {
			continue
		}
		revs, err := touchOwners(ctx, tx, kind, touched[kind])
		if err != nil {
			return err
		}
		for _, r := range revs {
			if err := tx.Changed(kind, r.ID, r.Rev); err != nil {
				return err
			}
		}
	}
	return nil
}

// Touch increments the revision of the owner o of the batch's project, which
// is not in the trash, whose expressions a command changed (Set, SetFields),
// and returns its new revision, which the command reports as its change of o:
// a command whose only change of o is of its expressions, as a branch
// output's update of its condition, calls it once Set reports a change, and
// one that may change o's other fields too reports o's change with
// OwnerRevision, which calls it if they are as they were.
func Touch(ctx context.Context, tx *commands.Tx, o Owner) (int64, error) {
	revs, err := touchOwners(ctx, tx, o.Kind, []pgtype.UUID{o.ID})
	switch {
	case err != nil:
		return 0, err
	case len(revs) != 1:
		return 0, fmt.Errorf("expression: changing the revision of the %s changed %d rows", o.Kind, len(revs))
	}
	return revs[0].Rev, nil
}

// touchOwners increments the revisions of the owners of the kind whose UUIDs
// are owners, of the batch's project, as the batch's actor's change, but of
// those deleted to the trash themselves, which nothing changes meanwhile, and
// returns the new revisions of those that it changed, in the order of their
// UUIDs, in which the batch's answer lists them. One that is in the trash
// only with its board, its node, or its output is changed, as one that is not
// in the trash is: no deletion of its own expects its revision, and the
// restoration of what it is in the trash with gives it none, so that it
// would come back changed at the revision that a client holds it at.
func touchOwners(ctx context.Context, tx *commands.Tx, kind ids.Kind, owners []pgtype.UUID) (
	[]commands.Revision, error) {
	q := queries.New(tx)
	ws, project, by := nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID()), nullUUID(tx.Actor().UserID)
	var revs []commands.Revision
	add := func(id pgtype.UUID, rev int64) {
		revs = append(revs, commands.Revision{Ref: commands.Ref{Kind: kind, ID: id.Bytes}, Rev: rev})
	}
	switch kind {
	case ids.Node:
		rows, err := q.TouchNodes(ctx, queries.TouchNodesParams{UpdatedBy: by, WorkspaceID: ws, ProjectID: project,
			Ids: owners})
		if err != nil {
			return nil, fmt.Errorf("expression: change the nodes' revisions: %w", err)
		}
		for _, r := range rows {
			add(r.ID, r.Rev)
		}
	case ids.Connection:
		rows, err := q.TouchConnections(ctx, queries.TouchConnectionsParams{UpdatedBy: by, WorkspaceID: ws,
			ProjectID: project, Ids: owners})
		if err != nil {
			return nil, fmt.Errorf("expression: change the connections' revisions: %w", err)
		}
		for _, r := range rows {
			add(r.ID, r.Rev)
		}
	case ids.BranchOutput:
		rows, err := q.TouchBranchOutputs(ctx, queries.TouchBranchOutputsParams{UpdatedBy: by, WorkspaceID: ws,
			ProjectID: project, Ids: owners})
		if err != nil {
			return nil, fmt.Errorf("expression: change the outputs' revisions: %w", err)
		}
		for _, r := range rows {
			add(r.ID, r.Rev)
		}
	default:
		return nil, fmt.Errorf("expression: a %s has no expressions", kind)
	}
	// The rows come back in no order.
	slices.SortFunc(revs, func(a, b commands.Revision) int { return bytes.Compare(a.ID[:], b.ID[:]) })
	return revs, nil
}

// toCompile returns the expressions to compile of the project: those that
// ExpressionsToCompile returns of p, and those of the other boards of each of
// the boards whose deletions to the trash or restorations change what those
// expressions can name of them (BoardExpressionsToCompile), each once, in the
// order of their IDs.
func toCompile(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID,
	p queries.ExpressionsToCompileParams, boards []pgtype.UUID) ([]queries.ExpressionsToCompileRow, error) {
	rows, err := q.ExpressionsToCompile(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("expression: read the expressions to compile: %w", err)
	}
	if len(boards) == 0 {
		return rows, nil
	}
	for _, b := range boards {
		names, err := q.BoardNames(ctx, queries.BoardNamesParams{WorkspaceID: ws, ProjectID: project, BoardID: b})
		if err != nil {
			return nil, fmt.Errorf("expression: read what a board holds that expressions name: %w", err)
		}
		refs, keys := []pgtype.UUID{}, []string{}
		for _, n := range names {
			if n.Key.Valid {
				keys = append(keys, n.Key.String)
			} else {
				refs = append(refs, n.ID)
			}
		}
		if len(refs) == 0 && len(keys) == 0 {
			continue
		}
		more, err := q.BoardExpressionsToCompile(ctx, queries.BoardExpressionsToCompileParams{WorkspaceID: ws,
			ProjectID: project, BoardID: b, Refs: refs, Keys: keys})
		if err != nil {
			return nil, fmt.Errorf("expression: read the expressions that name what a board holds: %w", err)
		}
		for _, m := range more {
			rows = append(rows, queries.ExpressionsToCompileRow(m))
		}
	}
	slices.SortFunc(rows, func(a, b queries.ExpressionsToCompileRow) int {
		return bytes.Compare(a.ID.Bytes[:], b.ID.Bytes[:])
	})
	return slices.CompactFunc(rows, func(a, b queries.ExpressionsToCompileRow) bool { return a.ID == b.ID }), nil
}

// projects are what the project's expressions can name (§5.8.3), as the
// expressions of each board see it. An expression of a board that is not in
// the trash sees live: the project's variables, its components with their
// attributes, and its nodes that the expressions to compile name, of those
// that are not in its trash. One of a board in the trash sees what it would
// once the board is restored: live, and the board's own variables and nodes
// that are in the trash with it alone, in trashed, by the board's UUID, so
// that the board's deletion and its restoration change nothing that its own
// expressions compile to, while the other boards' expressions see nothing
// that it holds, as an expression that names a node of the board finds no
// node, and one that names the key of a variable of the board finds no
// variable of another board of the key.
type projects struct {
	live    *expr.Project
	trashed map[pgtype.UUID]*expr.Project
}

// of returns what an expression of the board sees.
func (ps projects) of(board pgtype.UUID) *expr.Project {
	if p, ok := ps.trashed[board]; ok {
		return p
	}
	return ps.live
}

// loadProjects returns the projects of the project, whose expressions to
// compile name the nodes, and are of the boards in the trash trashed, each
// of which has its own.
func loadProjects(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, nodes, trashed []pgtype.UUID) (
	projects, error) {
	variables, err := q.ExpressionVariables(ctx, queries.ExpressionVariablesParams{WorkspaceID: ws,
		ProjectID: project})
	if err != nil {
		return projects{}, fmt.Errorf("expression: read the project's variables: %w", err)
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
		return projects{}, fmt.Errorf("expression: read the project's attribute definitions: %w", err)
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
		return projects{}, fmt.Errorf("expression: read the project's components: %w", err)
	}
	cs := make([]expr.Component, len(components))
	for i, c := range components {
		cs[i] = expr.Component{ID: ids.Encode(ids.Component, c.ID.Bytes), Key: c.Key,
			Attributes: ofType[c.ComponentTypeID.Bytes]}
	}
	// The nodes that the expressions name, those of the boards that are not
	// in the trash, and those of each board that is, there with it alone.
	var ns []expr.BoardNode
	trashedNodes := map[pgtype.UUID][]expr.BoardNode{}
	if len(nodes) > 0 {
		rows, err := q.ExpressionNodes(ctx, queries.ExpressionNodesParams{WorkspaceID: ws, ProjectID: project,
			Ids: nodes})
		if err != nil {
			return projects{}, fmt.Errorf("expression: read the nodes that expressions name: %w", err)
		}
		for _, n := range rows {
			bn := expr.BoardNode{ID: ids.Encode(ids.Node, n.ID.Bytes), Jumper: n.Type == "jumper"}
			if n.BoardDeleted {
				trashedNodes[n.BoardID] = append(trashedNodes[n.BoardID], bn)
			} else {
				ns = append(ns, bn)
			}
		}
	}
	ps := projects{trashed: make(map[pgtype.UUID]*expr.Project, len(trashed))}
	if ps.live, err = expr.NewProject(vs, cs, ns); err != nil {
		return projects{}, fmt.Errorf("expression: what the project's expressions can name: %w", err)
	}
	if len(trashed) == 0 {
		return ps, nil
	}
	rows, err := q.TrashedBoardVariables(ctx, queries.TrashedBoardVariablesParams{WorkspaceID: ws,
		ProjectID: project, BoardIds: trashed})
	if err != nil {
		return projects{}, fmt.Errorf("expression: read the variables of the boards in the trash: %w", err)
	}
	trashedVariables := map[pgtype.UUID][]expr.Variable{}
	for _, v := range rows {
		trashedVariables[v.BoardID] = append(trashedVariables[v.BoardID], expr.Variable{
			ID: ids.Encode(ids.Variable, v.ID.Bytes), Key: v.Key, Type: v.Type,
			Board: ids.Encode(ids.Board, v.BoardID.Bytes)})
	}
	for _, b := range trashed {
		p, err := ps.live.With(ids.Encode(ids.Board, b.Bytes), trashedVariables[b], trashedNodes[b])
		if err != nil {
			return projects{}, fmt.Errorf("expression: what the expressions of the board %s in the trash can name: %w",
				ids.UUID(b.Bytes), err)
		}
		ps.trashed[b] = p
	}
	return ps, nil
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
	expr.Walk(n, func(n expr.Node) {
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
		expr.Walk(s.ast, func(n expr.Node) {
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

// list returns l, or an empty list for nil, which a query reads as an array
// of no element rather than as null.
func list(l []pgtype.UUID) []pgtype.UUID {
	if l == nil {
		return []pgtype.UUID{}
	}
	return l
}
