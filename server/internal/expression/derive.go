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
	"github.com/CyberCute-Studio/storycreep/server/internal/dependency"
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
// references), and the boards are read apart (derive): all that expressions
// read of a board is whether it is in the trash, which changes what the
// other boards' expressions can name of the variables and the nodes that it
// holds, so a change of a board that commands of neutral types alone did not
// make is read as its deletion to the trash or its restoration, which
// TestNeutral holds every type of command of a board to, or to neutral.
var nameKinds = []ids.Kind{ids.Variable, ids.Component, ids.AttributeDef, ids.ComponentType}

// ownerKinds are the kinds of the owners of expressions, whose commands alone
// write sources.
var ownerKinds = []ids.Kind{ids.Node, ids.Connection, ids.BranchOutput}

// neutral are the types of command that change an entity of one of
// nameKinds, a board, or a node, but neither what the project's expressions
// can name nor what a name of theirs resolves to: a board's creation, which
// holds neither variables nor nodes yet, and its name, description, colour,
// slug, place, and entry node; a node's update and its move, which leave as
// they are its type, which no command changes, and whether it is in the
// trash, all that a node reference reads; a variable's description, default,
// reset_on_enter, and place; a component's name, description, image, values,
// and place; an attribute definition's name, default, and place, which no
// near match depends on, as they come nearest first and then by their bytes;
// a component type's creation, which holds neither definitions nor
// components yet, and its name, its key, which no expression names, and its
// icon; and a mute of the validation's findings and its unmute, which change
// their owner, a node, a variable, or a board among them, as a part of it
// that no expression reads (package validation). derive passes over a change
// that commands of these types alone made, so that a board's rename, or a
// variable's description, does not compile again every expression that names
// what it holds, or names it, nor every one that does not compile; it still
// compiles the sources that such a command wrote, as a node's update does its
// scripts. A type that is not here is taken to change what expressions can
// name, so that a new type of command compiles too much, never too little,
// until it is added here, and one of a board to delete it to the trash or
// restore it (nameKinds). The types are spelt out, rather than named by the
// constants of their packages, which import this one, as package variable's
// rename rewrites the sources that name the variable (Rewrite); TestNeutral
// checks that each is a registered type.
var neutral = []commands.Type{
	"board.create", "board.rename", "board.describe", "board.set_color", "board.set_slug", "board.move",
	"board.set_entry_node", "node.update", "node.move",
	"variable.describe", "variable.set_default", "variable.set_reset_on_enter", "variable.move",
	"component.rename", "component.describe", "component.set_image", "component.set_attribute", "component.move",
	"attribute.rename", "attribute.set_default", "attribute.move",
	"component_type.create", "component_type.rename", "component_type.set_key", "component_type.set_icon",
	"validation.mute", "validation.unmute",
}

// rewriting are the types of command that change a node only as they rewrite
// the sources of its expressions (ApplyRename), which the derivation compiles
// as it does every source that a command wrote, and leave as they are its
// type and whether it is in the trash, all that a node reference reads: a
// variable's rename and a component's change of key, which change what the
// project's expressions can name all the same, by the entity that they
// rename. derive passes over a change of a node that commands of these types
// and of neutral ones alone made, so that a rename that rewrites 500 nodes'
// scripts does not compile again every expression that names one of those
// nodes. They are package dependency's list, whose derivation passes over
// them too, so that one list says it for both (dependency.SourceRewrites).
var rewriting = dependency.SourceRewrites

// derive compiles the expressions of the batch's project that changed leaves
// to compile: those whose sources its commands wrote, which have neither an
// AST nor an error yet; those whose sources name, by their IDs or by their
// keys, an entity that it changed, or a component or an attribute definition
// of a type that it changed; those of the other boards of a board that it
// deleted to the trash or restored whose sources name the board's nodes, or
// do not compile and name the keys of its variables, whose deletion takes
// them out of what those expressions can name, and whose restoration brings
// them back (boardExpressions); and, if it changed a variable, a component, a
// definition, or a type, every one that does not compile but names
// something, whose error, or the near matches that it suggests, may change
// with any of them; but for a change that the commands of neutral types alone
// made, which changes none of them, or, of a node, those of neutral and of
// rewriting types alone. An expression of a board in the trash is
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
		if slices.ContainsFunc(c.By, func(t commands.Type) bool {
			return !slices.Contains(neutral, t) && (c.Kind != ids.Node || !slices.Contains(rewriting, t))
		}) {
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
	toCompile, err := boardExpressions(ctx, q, ws, project, boards)
	if err != nil {
		return err
	}
	rows, err := q.ExpressionsToCompile(ctx, queries.ExpressionsToCompileParams{WorkspaceID: ws, ProjectID: project,
		Refs: refs, Keys: keys, Ids: toCompile, Names: names})
	if err != nil {
		return fmt.Errorf("expression: read the expressions to compile: %w", err)
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
	at := make(map[ids.UUID]int, len(rows))
	for i, row := range rows {
		at[row.ID.Bytes] = i
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
	// What the expressions whose compilations changed, as the API shows them,
	// name is indexed as their ASTs now name it (package dependency), which
	// is what it was for the others, whose ASTs are as they were.
	var sites []dependency.Site
	for _, o := range owners {
		if o.Shown {
			i := at[o.ID.Bytes]
			sites = append(sites, dependency.Site{Expression: o.ID.Bytes, Board: rows[i].BoardID.Bytes,
				Uses: compiled[i].uses})
		}
	}
	if err := dependency.IndexExpressions(ctx, q, ws, project, sites); err != nil {
		return err
	}
	// The names of the expressions whose refs or keys changed, or that had
	// yet to compile, are written as their refs and keys now are, by which
	// a later batch finds those that name what it changes.
	var renamed []pgtype.UUID
	for _, o := range owners {
		if o.Renamed {
			renamed = append(renamed, o.ID)
		}
	}
	if len(renamed) > 0 {
		if err := q.IndexExpressionNames(ctx, queries.IndexExpressionNamesParams{WorkspaceID: ws,
			ProjectID: project, Ids: renamed}); err != nil {
			return fmt.Errorf("expression: write the names of the expressions: %w", err)
		}
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
		kind, id := ownerRef(o.NodeID, o.ConnectionID, o.BranchOutputID)
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

// boardExpressions returns the UUIDs of the project's expressions that the
// deletions to the trash, or the restorations, of the boards change: those
// that name a node of a board other than their own among them, or that do
// not compile and name the key of a variable of one, which its deletion takes
// out of what they can name, and its restoration brings back; none for no
// boards. One that compiles names its own board's variables and the global
// ones alone, and one of a board among them sees what the board holds as it
// did, an expression of a board in the trash seeing what is there with it
// alone (projects).
func boardExpressions(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, boards []pgtype.UUID) (
	[]pgtype.UUID, error) {
	if len(boards) == 0 {
		return []pgtype.UUID{}, nil
	}
	names, err := q.BoardNames(ctx, queries.BoardNamesParams{WorkspaceID: ws, ProjectID: project, BoardIds: boards})
	if err != nil {
		return nil, fmt.Errorf("expression: read what the boards hold that expressions name: %w", err)
	}
	// The board of each node, and the boards of each key, which several may
	// have a variable of.
	nodeBoard, keyBoards := map[pgtype.UUID]pgtype.UUID{}, map[string][]pgtype.UUID{}
	refs, keys := []pgtype.UUID{}, []string{}
	for _, n := range names {
		if !n.Key.Valid {
			nodeBoard[n.ID] = n.BoardID
			refs = append(refs, n.ID)
			continue
		}
		if keyBoards[n.Key.String] == nil {
			keys = append(keys, n.Key.String)
		}
		keyBoards[n.Key.String] = append(keyBoards[n.Key.String], n.BoardID)
	}
	if len(refs) == 0 && len(keys) == 0 {
		return []pgtype.UUID{}, nil
	}
	rows, err := q.BoardExpressionsToCompile(ctx, queries.BoardExpressionsToCompileParams{WorkspaceID: ws,
		ProjectID: project, Refs: refs, Keys: keys, BoardIds: boards})
	if err != nil {
		return nil, fmt.Errorf("expression: read the expressions that name what the boards hold: %w", err)
	}
	// Each row is a name of an expression: a node's UUID, or the key of a
	// variable, which the query reads of the expressions that do not compile
	// alone.
	changed, seen := []pgtype.UUID{}, map[pgtype.UUID]bool{}
	for _, r := range rows {
		other := func(b pgtype.UUID) bool { return b != r.BoardID }
		if seen[r.ExpressionID] {
			continue
		}
		if b, ok := nodeBoard[r.Ref]; r.Ref.Valid && ok && other(b) ||
			r.Key.Valid && slices.ContainsFunc(keyBoards[r.Key.String], other) {
			seen[r.ExpressionID] = true
			changed = append(changed, r.ExpressionID)
		}
	}
	return changed, nil
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
	n, err := loadNames(ctx, q, ws, project, nodes, trashed)
	if err != nil {
		return projects{}, err
	}
	return n.build()
}

// Live returns what the expressions of the project's boards that are not in
// the trash can name, of the project's variables, components, and nodes that
// are not in the trash, those of its nodes among nodes alone, which its
// expressions name, as the derivation reads them (§5.8.3), as package
// clipboard reads what a copied expression names and what a pasted one is to
// name. It reads with q, in a transaction of the workspace ws.
func Live(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, nodes []pgtype.UUID) (*expr.Project,
	error) {
	ps, err := loadProjects(ctx, q, ws, project, nodes, nil)
	if err != nil {
		return nil, err
	}
	return ps.live, nil
}

// names are what the project's expressions can name, as loadNames reads them
// and build makes projects of: the variables, the components with their
// attributes, and the nodes that the expressions to compile name, of those
// that are not in the project's trash, and the variables and those nodes of
// each board in the trash trashed that are there with it alone. ofType are
// the attributes of each component type that is not in the trash, by the
// type's UUID.
type names struct {
	variables        []expr.Variable
	components       []expr.Component
	nodes            []expr.BoardNode
	ofType           map[ids.UUID][]expr.Attribute
	trashed          []pgtype.UUID
	trashedVariables map[pgtype.UUID][]expr.Variable
	trashedNodes     map[pgtype.UUID][]expr.BoardNode
}

// loadNames returns the names of the project, whose expressions to compile
// name the nodes, and are of the boards in the trash trashed.
func loadNames(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, nodes, trashed []pgtype.UUID) (
	names, error) {
	variables, err := q.ExpressionVariables(ctx, queries.ExpressionVariablesParams{WorkspaceID: ws,
		ProjectID: project})
	if err != nil {
		return names{}, fmt.Errorf("expression: read the project's variables: %w", err)
	}
	n := names{variables: make([]expr.Variable, len(variables)), ofType: map[ids.UUID][]expr.Attribute{},
		trashed: trashed, trashedVariables: map[pgtype.UUID][]expr.Variable{},
		trashedNodes: map[pgtype.UUID][]expr.BoardNode{}}
	for i, v := range variables {
		n.variables[i] = expr.Variable{ID: ids.Encode(ids.Variable, v.ID.Bytes), Key: v.Key, Type: v.Type}
		if v.BoardID.Valid {
			n.variables[i].Board = ids.Encode(ids.Board, v.BoardID.Bytes)
		}
	}
	attributes, err := q.ExpressionAttributes(ctx, queries.ExpressionAttributesParams{WorkspaceID: ws,
		ProjectID: project})
	if err != nil {
		return names{}, fmt.Errorf("expression: read the project's attribute definitions: %w", err)
	}
	for _, a := range attributes {
		n.ofType[a.ComponentTypeID.Bytes] = append(n.ofType[a.ComponentTypeID.Bytes], expr.Attribute{
			ID: ids.Encode(ids.AttributeDef, a.ID.Bytes), Key: a.Key, Type: a.Type, Runtime: a.IsRuntime,
			Options: a.EnumOptions})
	}
	components, err := q.ExpressionComponents(ctx, queries.ExpressionComponentsParams{WorkspaceID: ws,
		ProjectID: project})
	if err != nil {
		return names{}, fmt.Errorf("expression: read the project's components: %w", err)
	}
	n.components = make([]expr.Component, len(components))
	for i, c := range components {
		n.components[i] = expr.Component{ID: ids.Encode(ids.Component, c.ID.Bytes), Key: c.Key,
			Attributes: n.ofType[c.ComponentTypeID.Bytes]}
	}
	// The nodes that the expressions name, those of the boards that are not
	// in the trash, and those of each board in the trash trashed, there with
	// it alone.
	if len(nodes) > 0 {
		rows, err := q.ExpressionNodes(ctx, queries.ExpressionNodesParams{WorkspaceID: ws, ProjectID: project,
			Ids: nodes, BoardIds: list(trashed)})
		if err != nil {
			return names{}, fmt.Errorf("expression: read the nodes that expressions name: %w", err)
		}
		for _, r := range rows {
			bn := expr.BoardNode{ID: ids.Encode(ids.Node, r.ID.Bytes), Jumper: r.Type == "jumper"}
			if r.BoardDeleted {
				n.trashedNodes[r.BoardID] = append(n.trashedNodes[r.BoardID], bn)
			} else {
				n.nodes = append(n.nodes, bn)
			}
		}
	}
	if len(trashed) == 0 {
		return n, nil
	}
	if n.trashedVariables, err = trashedVariables(ctx, q, ws, project, trashed); err != nil {
		return names{}, err
	}
	return n, nil
}

// trashedVariables returns the variables of the project's boards in the
// trash trashed that are there with them alone, by their boards' UUIDs.
func trashedVariables(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, trashed []pgtype.UUID) (
	map[pgtype.UUID][]expr.Variable, error) {
	rows, err := q.TrashedBoardVariables(ctx, queries.TrashedBoardVariablesParams{WorkspaceID: ws,
		ProjectID: project, BoardIds: trashed})
	if err != nil {
		return nil, fmt.Errorf("expression: read the variables of the boards in the trash: %w", err)
	}
	out := map[pgtype.UUID][]expr.Variable{}
	for _, v := range rows {
		out[v.BoardID] = append(out[v.BoardID], expr.Variable{ID: ids.Encode(ids.Variable, v.ID.Bytes), Key: v.Key,
			Type: v.Type, Board: ids.Encode(ids.Board, v.BoardID.Bytes)})
	}
	return out, nil
}

// build returns the projects of the names.
func (n names) build() (projects, error) {
	ps := projects{trashed: make(map[pgtype.UUID]*expr.Project, len(n.trashed))}
	var err error
	if ps.live, err = expr.NewProject(n.variables, n.components, n.nodes); err != nil {
		return projects{}, fmt.Errorf("expression: what the project's expressions can name: %w", err)
	}
	for _, b := range n.trashed {
		p, err := ps.live.With(ids.Encode(ids.Board, b.Bytes), n.trashedVariables[b], n.trashedNodes[b])
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
// and the UUIDs and the keys that it names; and what its AST names, with how
// it uses it, which the index of what the content names holds (package
// dependency), and of which one that does not compile names nothing.
type compiledRow struct {
	ID         string          `json:"id"`
	AST        json.RawMessage `json:"ast"`
	ASTVersion *int            `json:"ast_version"`
	Error      *expr.Error     `json:"error"`
	Refs       []string        `json:"refs"`
	Keys       []string        `json:"keys"`
	uses       []dependency.Use
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
		if out.uses, err = dependency.Uses(s.ast); err != nil {
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
