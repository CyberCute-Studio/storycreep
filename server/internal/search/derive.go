package search

import (
	"context"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// Name is the name of the derivation that writes the documents of what a
// batch changed (commands.Derivation).
const Name = "search_entries"

// Register registers in r the derivation that writes, once every command of
// a batch has applied, the documents of the entities that the batch may have
// changed the document of: each node, connection, variable, and component
// that a command of the batch changed, and each component of a type that a
// definition that a command changed is of, as deleting a definition, which
// changes neither its type nor the components, takes their values of it out
// of their documents; but for the changes that commands of neutral types
// alone made. It changes no entity's revision, as the index is no part of
// what the API shows of one. Register it after expression.Register, whose
// derivation's changes of the owners of expressions it passes over.
func Register(r *commands.Registry) {
	commands.RegisterDerivation(r, commands.Derivation{Name: Name, Derive: derive})
}

// neutral are the types of command that change a node, a connection, a
// variable, a component, or an attribute definition, but nothing that a
// document holds (NewDocument): a move, a deletion to the trash or a
// restoration, which the search reads of the entities themselves, a change
// of a field that no document holds, as a variable's default or a
// definition's key, and the mutes of the validation's findings, with the
// name of the derivation that compiles expressions (expression.Name, which
// this package does not import), which changes their owners' revisions
// alone. A definition's move is neutral, since a component's document holds
// its values in the order of their definitions' IDs, not of their places.
// derive passes over a change that these alone made, so that moving a
// selection of 2,000 nodes reads none of them. A type that is not here is
// taken to change documents, so that a new type of command indexes too
// much, never too little, until it is added here. TestNeutral checks that
// each is a registered type, or the derivation's name.
var neutral = []commands.Type{
	"expression",
	"node.move", "node.delete", "node.restore", "board.set_entry_node", "branch_output.create",
	"connection.reconnect", "connection.reorder", "connection.delete", "connection.restore",
	"variable.set_default", "variable.retype", "variable.set_scope", "variable.set_reset_on_enter",
	"variable.move", "variable.delete", "variable.restore",
	"component.set_image", "component.move", "component.delete", "component.restore",
	"attribute.rename", "attribute.set_key", "attribute.set_default", "attribute.set_runtime", "attribute.move",
	"validation.mute", "validation.unmute",
}

// neutralTypes holds the types of neutral, which derive looks the type of
// each command of a change up in.
var neutralTypes = func() map[commands.Type]bool {
	m := make(map[commands.Type]bool, len(neutral))
	for _, t := range neutral {
		m[t] = true
	}
	return m
}()

// derive writes the documents of the entities of changed whose documents
// the batch's commands may have changed, as Register says.
func derive(ctx context.Context, tx *commands.Tx, changed []commands.Change) error {
	var sel selection
	for _, c := range changed {
		if slices.ContainsFunc(c.By, func(t commands.Type) bool { return !neutralTypes[t] }) {
			sel.add(c.Kind, c.ID)
		}
	}
	if sel.empty() {
		return nil
	}
	_, err := index(ctx, queries.New(tx), tx.WorkspaceID(), tx.ProjectID(), sel)
	return err
}

// selection is the entities whose documents index writes: the nodes, the
// connections, the variables, and the components, and the components of the
// types that the definitions are of.
type selection struct {
	nodes, connections, variables, components, attributes []pgtype.UUID
}

// add adds the entity id of the kind k to s, if it is of a kind that has a
// document, or is a definition, whose type's components have.
func (s *selection) add(k ids.Kind, id ids.UUID) {
	u := pgtype.UUID{Bytes: id, Valid: true}
	switch k {
	case ids.Node:
		s.nodes = append(s.nodes, u)
	case ids.Connection:
		s.connections = append(s.connections, u)
	case ids.Variable:
		s.variables = append(s.variables, u)
	case ids.Component:
		s.components = append(s.components, u)
	case ids.AttributeDef:
		s.attributes = append(s.attributes, u)
	}
}

// empty reports whether s holds no entity.
func (s *selection) empty() bool {
	return len(s.nodes) == 0 && len(s.connections) == 0 && len(s.variables) == 0 && len(s.components) == 0 &&
		len(s.attributes) == 0
}

// index writes the document of each entity of sel of the project projectID
// of the workspace workspaceID, in the trash or not, of the current Version,
// as the project's rows hold them now, but for those that hold it already,
// and returns how many entities it found.
func index(ctx context.Context, q *queries.Queries, workspaceID, projectID ids.UUID, sel selection) (int, error) {
	ws, project := nullUUID(workspaceID), nullUUID(projectID)
	rows, err := q.SearchSources(ctx, queries.SearchSourcesParams{WorkspaceID: ws, ProjectID: project,
		NodeIds: list(sel.nodes), ConnectionIds: list(sel.connections), VariableIds: list(sel.variables),
		ComponentIds: list(sel.components), AttributeIds: list(sel.attributes)})
	if err != nil {
		return 0, fmt.Errorf("search: read what the documents are made of: %w", err)
	}
	if len(rows) == 0 {
		return 0, nil
	}
	p := queries.UpsertSearchEntriesParams{WorkspaceID: ws, ProjectID: project, Version: Version}
	for _, r := range rows {
		d, err := NewDocument(Source{Kind: Kind(r.Kind), ID: r.ID.Bytes, Title: r.Title, Doc: r.Doc, Key: r.Key,
			Description: r.Description, Values: r.AttributeValues})
		if err != nil {
			return 0, err
		}
		p.Kinds = append(p.Kinds, string(d.Kind))
		p.EntityIds = append(p.EntityIds, r.ID)
		p.Titles = append(p.Titles, d.Title)
		p.Bodies = append(p.Bodies, d.Body)
	}
	if err := q.UpsertSearchEntries(ctx, p); err != nil {
		return 0, fmt.Errorf("search: write the documents: %w", err)
	}
	return len(rows), nil
}

// list returns l, or an empty list for nil, which a query reads as an array
// of no element rather than as null.
func list(l []pgtype.UUID) []pgtype.UUID {
	if l == nil {
		return []pgtype.UUID{}
	}
	return l
}
