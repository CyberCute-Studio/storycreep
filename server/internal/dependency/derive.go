package dependency

import (
	"context"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// Name is the name of the derivation that indexes what the project's texts
// name (commands.Derivation).
const Name = "text_references"

// Register registers in r the derivation that replaces, once every command of
// a batch has applied, the rows of the index of the texts that the batch may
// have changed: the body of each node, the label of each connection, and the
// description of each component that a command of the batch changed, but for
// the changes that commands of textNeutral types alone made. It changes no
// entity's revision, as the index is no part of what the API shows of one.
// Register it after expression.Register, whose derivation's changes of the
// owners of expressions it passes over.
func Register(r *commands.Registry) {
	commands.RegisterDerivation(r, commands.Derivation{Name: Name, Derive: derive})
}

// SourceRewrites are the types of command that change nodes, connections,
// and branch outputs only as they rewrite the sources of their expressions,
// and change nothing else of them, as package expression's ApplyRename
// does: a variable's rename and a component's change of key, which change
// their own entity's key besides (PRD FR-VAR-03). They change none of the
// texts, so textNeutral holds them, and package expression's derivation,
// which compiles the sources that they write, passes over their changes of
// nodes, which change nothing that a node reference reads (its rewriting,
// which is this list, as that package imports this one): a type that
// rewrites sources so joins both derivations' lists here. TestTextNeutral
// checks that each is a registered type.
var SourceRewrites = []commands.Type{"variable.rename", "component.set_key"}

// textNeutral are the types of command that change a node, a connection, or
// a component, but none of their texts, those of SourceRewrites among them,
// and the name of the derivation that compiles expressions (expression.Name,
// which this package cannot import, as package expression imports it), which
// changes their owners' revisions alone. derive passes over a change that
// these alone made, so that moving a selection of 2,000 nodes, or adding an
// attribute to a type of 100 components, which changes each of them, reads
// none of their texts. A type that is not here is taken to change texts, so
// that a new type of command indexes too much, never too little, until it is
// added here. TestTextNeutral checks that each is a registered type, or the
// derivation's name.
var textNeutral = append([]commands.Type{
	"expression",
	"node.move", "node.delete", "node.restore", "board.set_entry_node", "branch_output.create",
	"connection.reconnect", "connection.reorder", "connection.delete", "connection.restore",
	"component.rename", "component.set_image", "component.set_attribute", "component.move",
	"component.delete", "component.restore",
	"attribute.create", "attribute.rename", "attribute.set_key", "attribute.set_default", "attribute.set_runtime",
	"attribute.set_options", "attribute.retype", "attribute.move", "attribute.delete", "attribute.restore",
}, SourceRewrites...)

// derive replaces the rows of the index of the texts of the nodes, the
// connections, and the components that changed holds, but of those that the
// commands of textNeutral types alone changed: what their value references
// and mentions name now, which a text that changed holds, and one that did
// not holds as it did.
func derive(ctx context.Context, tx *commands.Tx, changed []commands.Change) error {
	var nodes, connections, components []pgtype.UUID
	for _, c := range changedTexts(changed) {
		switch c.Kind {
		case ids.Node:
			nodes = append(nodes, nullUUID(c.ID))
		case ids.Connection:
			connections = append(connections, nullUUID(c.ID))
		case ids.Component:
			components = append(components, nullUUID(c.ID))
		}
	}
	if len(nodes) == 0 && len(connections) == 0 && len(components) == 0 {
		return nil
	}
	q := queries.New(tx)
	ws, project := nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID())
	refs, err := q.TextReferences(ctx, queries.TextReferencesParams{WorkspaceID: ws, ProjectID: project,
		NodeIds: list(nodes), ConnectionIds: list(connections), ComponentIds: list(components)})
	if err != nil {
		return fmt.Errorf("dependency: read what the texts name: %w", err)
	}
	// The sites of the texts that name something, each once, in the order of
	// their first reference.
	at := map[ids.UUID]int{}
	var sites []Site
	for _, r := range refs {
		u, err := inlineUse(r.Inline)
		if err != nil {
			return err
		}
		s := Site{Node: r.NodeID.Bytes, Connection: r.ConnectionID.Bytes, Component: r.ComponentID.Bytes,
			Board: r.BoardID.Bytes}
		owner := s.Node
		switch {
		case r.ConnectionID.Valid:
			owner = s.Connection
		case r.ComponentID.Valid:
			owner = s.Component
		}
		i, ok := at[owner]
		if !ok {
			i = len(sites)
			at[owner] = i
			sites = append(sites, s)
		}
		sites[i].Uses = append(sites[i].Uses, u)
	}
	for i := range sites {
		sites[i].Uses = sorted(sites[i].Uses)
	}
	return replace(ctx, q, queries.DeleteReferencesParams{WorkspaceID: ws, ProjectID: project,
		ExpressionIds: list(nil), NodeIds: list(nodes), ConnectionIds: list(connections),
		ComponentIds: list(components)}, sites)
}

// changedTexts returns the nodes, the connections, and the components of
// changed whose texts the batch's commands may have changed, with their
// latest revisions, as a derivation reads them (commands.Derivation.Derive,
// commands.Tx.Changes): each that a command of a type that is not
// textNeutral changed, whose rows of the index derive replaces, and which a
// command's Apply that reads what the texts name before the batch is indexed
// reads anew (TextsUsing), so that both read the same texts anew.
func changedTexts(changed []commands.Change) []commands.Revision {
	var out []commands.Revision
	for _, c := range changed {
		switch {
		case c.Kind != ids.Node && c.Kind != ids.Connection && c.Kind != ids.Component,
			!slices.ContainsFunc(c.By, func(t commands.Type) bool { return !slices.Contains(textNeutral, t) }):
			continue
		}
		out = append(out, c.Revision)
	}
	return out
}
