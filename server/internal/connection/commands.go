package connection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/node"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
)

// The types of command of a connection (PRD §5.7). Each names the connection
// that it is about by its ID, id, and applies to the connection of the
// batch's project.
const (
	// Create creates a connection from a node to a node of its board, whose
	// ID the client makes, so that it can refer to the connection before the
	// server answers. Deleting it to the trash undoes it.
	Create commands.Type = "connection.create"
	// Update changes the fields of a connection that its payload names, its
	// label, and leaves the others as they are. Changing them back undoes it.
	Update commands.Type = "connection.update"
	// Reconnect changes the node that a connection leaves, or the node that it
	// enters, or both (FR-EDIT-03). Reconnecting it back undoes it.
	Reconnect commands.Type = "connection.reconnect"
	// Reorder places a connection among those that leave its node, changing
	// its order alone (FR-EDIT-03 AC2). Placing it back undoes it.
	Reorder commands.Type = "connection.reorder"
	// Delete deletes a connection to the project's trash, unlisted, and
	// Restore restores it. Each undoes the other.
	Delete  commands.Type = "connection.delete"
	Restore commands.Type = "connection.restore"
)

// createPayload is Create's payload.
type createPayload struct {
	ID         string        `json:"id" doc:"The new connection's ID, a cn_ ID of a UUIDv7 that the client makes, which no entity of the instance has (PRD §5.2)"`
	FromNodeID string        `json:"from_node_id" doc:"The ID of the node that the connection leaves, by its port out: an element of the project that is not in its trash. A jumper has no connections out, because the flow continues at its target (PRD §5.8.5), and a branch's connections leave its outputs (PRD FR-EDIT-04), so either is answered with 422 validation_failed."`
	ToNodeID   string        `json:"to_node_id" doc:"The ID of the node that the connection enters: a node of the board of from_node_id, of any type, that is not in the project's trash, which may be from_node_id itself, and which other connections of from_node_id may enter too"`
	Label      *richtext.Doc `json:"label,omitempty" doc:"The connection's label, a rich-text document (PRD FR-TEXT-01): the choice that the player is offered. Without it, an empty paragraph, for none."`
	Order      string        `json:"order,omitempty" maxLength:"128" pattern:"^[0-9A-Za-z]*[1-9A-Za-z]$" doc:"The connection's place among the connections that leave from_node_id, the choices that it offers. A key of base-62 digits, 0-9, A-Z, and a-z, that does not end in 0, of at most 128 digits, which orders the connections that leave a node by its bytes, and those with the same key by their IDs without the prefix, cn_, compared by their bytes too: pick one between the keys of the connections that it goes between, as V where there are none, 8 between 7 and 9, or 7V between 7 and 8. Without it, the connection goes after the node's last."`
}

// updatePayload is Update's payload. A field that it leaves out stays as it
// is.
type updatePayload struct {
	ID    string        `json:"id" doc:"The connection's ID"`
	Label *richtext.Doc `json:"label,omitempty" doc:"The connection's new label, a rich-text document (PRD FR-TEXT-01): the choice that the player is offered, or an empty paragraph for none"`
}

// reconnectPayload is Reconnect's payload. An end that it leaves out stays as
// it is.
type reconnectPayload struct {
	ID         string `json:"id" doc:"The connection's ID"`
	FromNodeID string `json:"from_node_id,omitempty" doc:"The ID of the node that the connection leaves from now on, by its port out: an element of the connection's board that is not in the project's trash, or the node that it leaves. A jumper or a branch is answered with 422 validation_failed, as by connection.create."`
	ToNodeID   string `json:"to_node_id,omitempty" doc:"The ID of the node that the connection enters from now on: a node of the connection's board, of any type, that is not in the project's trash, or the node that it enters"`
	Order      string `json:"order,omitempty" maxLength:"128" pattern:"^[0-9A-Za-z]*[1-9A-Za-z]$" doc:"The connection's place among the connections that leave the node that it leaves once it is reconnected, a key as connection.create's order is. Without it, the connection goes after the node's last if it leaves another node than it did, or keeps its place."`
}

// reorderPayload is Reorder's payload.
type reorderPayload struct {
	ID    string `json:"id" doc:"The connection's ID"`
	Order string `json:"order,omitempty" maxLength:"128" pattern:"^[0-9A-Za-z]*[1-9A-Za-z]$" doc:"The connection's place among the connections that leave its node, the choices that the node offers, a key as connection.create's order is. Without it, the connection goes after the node's last, or stays where it is if it is the last already that is not in the trash."`
}

// idPayload is the payload of a command that needs nothing but its
// connection's ID.
type idPayload struct {
	ID string `json:"id" doc:"The connection's ID"`
}

// Register registers in r the connection as an entity of the pipeline,
// whose revisions a command expects as it does any entity's, its commands,
// each of which changes the project's draft, so that an API token needs
// project:write for it (FR-AUTH-04), and its role "Edit nodes, connections,
// text, expressions" (§5.6), and the reconnection of a node's connections
// when the node is restored from the trash (commands.RegisterReconnection,
// FR-PROJ-06 AC1). The connection's commands refer to its nodes, so r has
// the node registered already (node.Register).
func Register(r *commands.Registry) {
	commands.RegisterEntity(r, commands.Entity{
		Kind: ids.Connection,
		Revs: func(ctx context.Context, tx *commands.Tx, entities []ids.UUID) (map[ids.UUID]int64, error) {
			rows, err := queries.New(tx).ConnectionRevs(ctx, queries.ConnectionRevsParams{
				WorkspaceID: nullUUID(tx.WorkspaceID()), ProjectID: nullUUID(tx.ProjectID()), Ids: uuids(entities)})
			if err != nil {
				return nil, err
			}
			out := make(map[ids.UUID]int64, len(rows))
			for _, row := range rows {
				out[row.ID.Bytes] = row.Rev
			}
			return out, nil
		},
		States: func(ctx context.Context, tx *commands.Tx, entities []ids.UUID) (map[ids.UUID]any, error) {
			rows, err := queries.New(tx).ConnectionsByID(ctx, queries.ConnectionsByIDParams{
				WorkspaceID: nullUUID(tx.WorkspaceID()), ProjectID: nullUUID(tx.ProjectID()), Ids: uuids(entities)})
			if err != nil {
				return nil, err
			}
			out := make(map[ids.UUID]any, len(rows))
			for _, row := range rows {
				c, err := connectionOf(row.Connection)
				if err != nil {
					return nil, err
				}
				// A connection of a node or a board in the trash is in the
				// trash with it, which its state says, as the state of an
				// entity in the trash does, though only that deletion put it
				// there.
				if c.DeletedAt == nil && row.EndsDeletedAt.Valid {
					c.DeletedAt = timeOf(row.EndsDeletedAt)
				}
				out[row.Connection.ID.Bytes] = c
			}
			return out, nil
		},
	})

	connection := []ids.Kind{ids.Connection}
	// A command that sets a connection's nodes refers to them, so that a
	// client may expect their revisions too.
	ends := []ids.Kind{ids.Connection, ids.Node}
	op, sc := authz.EditContent, scope.ProjectWrite

	commands.Register(r, commands.Spec[createPayload]{
		Type: Create, Capability: op, Scope: sc, Touches: ends,
		Check: func(p createPayload) input.FieldErrors {
			errs := newID(ids.Connection, p.ID)
			errs = append(errs, entityID(ids.Node, "from_node_id", p.FromNodeID)...)
			return append(errs, entityID(ids.Node, "to_node_id", p.ToNodeID)...)
		},
		Inverse: func(_ context.Context, _ *commands.Tx, p createPayload) ([]commands.Step, error) {
			return []commands.Step{{Type: Delete, Payload: idPayload{ID: p.ID}}}, nil
		},
		Apply: applyCreate,
	})

	commands.Register(r, commands.Spec[updatePayload]{
		Type: Update, Capability: op, Scope: sc, Touches: connection,
		// The inverse holds the label that the update replaces, which may be
		// as large as a document is.
		UndoField: "label",
		Check:     func(p updatePayload) input.FieldErrors { return entityID(ids.Connection, "id", p.ID) },
		Inverse: func(ctx context.Context, tx *commands.Tx, p updatePayload) ([]commands.Step, error) {
			c, err := liveConnectionOf(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			back := updatePayload{ID: p.ID}
			if p.Label != nil {
				if back.Label, err = richtext.Parse(c.Label); err != nil {
					return nil, fmt.Errorf("connection: the label of the connection %s: %w", p.ID, err)
				}
			}
			return []commands.Step{{Type: Update, Payload: back}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p updatePayload) error {
			c, err := liveConnectionOf(ctx, tx, p.ID)
			if err != nil || p.Label == nil {
				return err
			}
			label, err := json.Marshal(*p.Label)
			if err != nil {
				return fmt.Errorf("connection: encode the label: %w", err)
			}
			return changed(tx, c.ID)(queries.New(tx).UpdateConnectionLabel(ctx, queries.UpdateConnectionLabelParams{
				Label: label, UpdatedBy: by(tx), WorkspaceID: c.WorkspaceID, ProjectID: c.ProjectID, ID: c.ID}))
		},
	})

	commands.Register(r, commands.Spec[reconnectPayload]{
		Type: Reconnect, Capability: op, Scope: sc, Touches: ends,
		Check: func(p reconnectPayload) input.FieldErrors {
			errs := entityID(ids.Connection, "id", p.ID)
			errs = append(errs, optionalID(ids.Node, "from_node_id", p.FromNodeID)...)
			return append(errs, optionalID(ids.Node, "to_node_id", p.ToNodeID)...)
		},
		Inverse: func(ctx context.Context, tx *commands.Tx, p reconnectPayload) ([]commands.Step, error) {
			c, _, err := planReconnect(ctx, tx, p)
			if err != nil {
				return nil, err
			}
			back := reconnectPayload{ID: p.ID}
			if p.FromNodeID != "" {
				back.FromNodeID = ids.Encode(ids.Node, c.FromNodeID.Bytes)
			}
			if p.ToNodeID != "" {
				back.ToNodeID = ids.Encode(ids.Node, c.ToNodeID.Bytes)
			}
			// Only a change of the node that it leaves, or of its order, may
			// change its order.
			if p.FromNodeID != "" || p.Order != "" {
				back.Order = c.Order
			}
			return []commands.Step{{Type: Reconnect, Payload: back}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p reconnectPayload) error {
			c, next, err := planReconnect(ctx, tx, p)
			if err != nil {
				return err
			}
			next.UpdatedBy, next.WorkspaceID, next.ProjectID, next.ID = by(tx), c.WorkspaceID, c.ProjectID, c.ID
			return changed(tx, c.ID)(queries.New(tx).ReconnectConnection(ctx, next))
		},
	})

	commands.Register(r, commands.Spec[reorderPayload]{
		Type: Reorder, Capability: op, Scope: sc, Touches: connection,
		Check: func(p reorderPayload) input.FieldErrors { return entityID(ids.Connection, "id", p.ID) },
		Inverse: func(ctx context.Context, tx *commands.Tx, p reorderPayload) ([]commands.Step, error) {
			c, err := liveConnectionOf(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: Reorder, Payload: reorderPayload{ID: p.ID, Order: c.Order}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p reorderPayload) error {
			c, err := liveConnectionOf(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			q := queries.New(tx)
			order, err := sortKey(ctx, q, c.WorkspaceID, c.ProjectID, c.FromNodeID, p.Order,
				&board.Child{ID: c.ID, Key: c.Order, Kind: ids.Connection})
			if err != nil {
				return err
			}
			return changed(tx, c.ID)(q.ReorderConnection(ctx, queries.ReorderConnectionParams{SortKey: order,
				UpdatedBy: by(tx), WorkspaceID: c.WorkspaceID, ProjectID: c.ProjectID, ID: c.ID}))
		},
	})

	commands.Register(r, commands.Spec[idPayload]{
		Type: Delete, Capability: op, Scope: sc, Touches: connection,
		Check: func(p idPayload) input.FieldErrors { return entityID(ids.Connection, "id", p.ID) },
		// Deleting a connection that is in the trash already does nothing,
		// and its undo nothing either. One of a node or a board in the trash,
		// in the trash with it but not deleted itself, is refused, as every
		// command refuses it, because its undo, a restore, would be refused
		// until its nodes are restored, as a node of a board in the trash is
		// (package node).
		Inverse: func(ctx context.Context, tx *commands.Tx, p idPayload) ([]commands.Step, error) {
			row, err := anyConnection(ctx, tx, p.ID)
			if err != nil || row.Connection.DeletedAt.Valid || row.EndsDeleted {
				return []commands.Step{}, err
			}
			return []commands.Step{{Type: Restore, Payload: idPayload{ID: p.ID}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p idPayload) error {
			row, err := anyConnection(ctx, tx, p.ID)
			switch {
			case err != nil:
				return err
			case row.Connection.DeletedAt.Valid:
				return nil
			case row.EndsDeleted:
				return refuseEndsInTrash()
			}
			c := row.Connection
			return changed(tx, c.ID)(queries.New(tx).TrashConnection(ctx, queries.TrashConnectionParams{
				UpdatedBy: by(tx), WorkspaceID: c.WorkspaceID, ProjectID: c.ProjectID, ID: c.ID}))
		},
	})

	commands.Register(r, commands.Spec[idPayload]{
		Type: Restore, Capability: op, Scope: sc, Touches: connection,
		Check: func(p idPayload) input.FieldErrors { return entityID(ids.Connection, "id", p.ID) },
		// Restoring a connection that is not in the trash does nothing, and its
		// undo nothing either. A connection of a node or a board in the trash,
		// which is in the trash with it, deleted on its own or not, is
		// refused, since it comes back only once its nodes do, so the inverse
		// of its restore, which Apply refuses, reads nothing more.
		Inverse: func(ctx context.Context, tx *commands.Tx, p idPayload) ([]commands.Step, error) {
			row, err := anyConnection(ctx, tx, p.ID)
			if err != nil || !row.Connection.DeletedAt.Valid || row.EndsDeleted {
				return []commands.Step{}, err
			}
			return []commands.Step{{Type: Delete, Payload: idPayload{ID: p.ID}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p idPayload) error {
			row, err := anyConnection(ctx, tx, p.ID)
			switch {
			case err != nil:
				return err
			case row.EndsDeleted:
				return commands.Refuse("id", "is a connection of a node in the project's trash, itself or with its "+
					"board, which comes back only once its nodes do: restore them first", nil)
			case !row.Connection.DeletedAt.Valid:
				return nil
			}
			c := row.Connection
			return changed(tx, c.ID)(queries.New(tx).RestoreConnection(ctx, queries.RestoreConnectionParams{
				UpdatedBy: by(tx), WorkspaceID: c.WorkspaceID, ProjectID: c.ProjectID, ID: c.ID}))
		},
	})

	// A node's connections are in the trash with it, and come back with it
	// where their other nodes are not in the trash, which the view of what is
	// live says by itself; what a restoration leaves is what the batch's
	// answer lists.
	commands.RegisterReconnection(r, commands.Reconnection{
		Restored: ids.Node, Kind: ids.Connection,
		Left: func(ctx context.Context, tx *commands.Tx, restored []ids.UUID) ([]commands.Unreconnected, error) {
			rows, err := queries.New(tx).UnreconnectedConnections(ctx, queries.UnreconnectedConnectionsParams{
				WorkspaceID: nullUUID(tx.WorkspaceID()), ProjectID: nullUUID(tx.ProjectID()), Ids: uuids(restored)})
			if err != nil {
				return nil, fmt.Errorf("connection: read the connections that wait for their nodes: %w", err)
			}
			out := make([]commands.Unreconnected, len(rows))
			for i, row := range rows {
				out[i] = commands.Unreconnected{Ref: commands.Ref{Kind: ids.Connection, ID: row.ID.Bytes},
					WaitingFor: []commands.Ref{{Kind: ids.Node, ID: row.WaitingFor.Bytes}}}
			}
			return out, nil
		},
	})
}

// applyCreate is Create's Apply.
func applyCreate(ctx context.Context, tx *commands.Tx, p createPayload) error {
	q := queries.New(tx)
	ws, project := nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID())
	id := decoded(ids.Connection, p.ID)
	if err := checkNewConnection(ctx, q, ws, project, id); err != nil {
		return err
	}
	from, err := fromNode(ctx, q, ws, project, "from_node_id", p.FromNodeID)
	if err != nil {
		return err
	}
	to, err := liveNode(ctx, q, ws, project, "to_node_id", p.ToNodeID)
	if err != nil {
		return err
	}
	if to.BoardID != from.BoardID {
		return commands.Refuse("to_node_id", "is a node of another board than from_node_id", nil)
	}
	order, err := sortKey(ctx, q, ws, project, from.ID, p.Order, nil)
	if err != nil {
		return err
	}
	var label richtext.Doc
	if p.Label != nil {
		label = *p.Label
	}
	doc, err := json.Marshal(label)
	if err != nil {
		return fmt.Errorf("connection: encode the label: %w", err)
	}
	err = q.InsertConnection(ctx, queries.InsertConnectionParams{ID: id, WorkspaceID: ws, ProjectID: project,
		BoardID: from.BoardID, FromNodeID: from.ID, ToNodeID: to.ID, SortKey: order, Label: doc, CreatedBy: by(tx)})
	if database.UniqueViolation(err, "connection_pkey") {
		return refuseTakenID()
	}
	if err != nil {
		return fmt.Errorf("connection: add the connection: %w", err)
	}
	return tx.Changed(ids.Connection, id.Bytes, 1)
}

// planReconnect returns the connection of Reconnect's payload p, as it is,
// and the nodes that it leaves and enters, and its order, once p has
// reconnected it, which leaves an end that p leaves out as it is, once it
// has checked that the connection is not in the trash, and that each node
// that p sets is one of the connection's board that is not in the trash
// either, and one that a connection may leave for the node that it leaves.
// It only reads, so that Inverse, which runs before Apply, refuses the batch
// as Apply would.
func planReconnect(ctx context.Context, tx *commands.Tx, p reconnectPayload) (queries.Connection,
	queries.ReconnectConnectionParams, error) {
	c, err := liveConnectionOf(ctx, tx, p.ID)
	if err != nil {
		return queries.Connection{}, queries.ReconnectConnectionParams{}, err
	}
	next := queries.ReconnectConnectionParams{FromNodeID: c.FromNodeID, ToNodeID: c.ToNodeID, SortKey: c.Order}
	q := queries.New(tx)
	if p.FromNodeID != "" {
		if id := decoded(ids.Node, p.FromNodeID); !sameUUID(id, c.FromNodeID) {
			from, err := fromNode(ctx, q, c.WorkspaceID, c.ProjectID, "from_node_id", p.FromNodeID)
			if err != nil {
				return c, next, err
			}
			if from.BoardID != c.BoardID {
				return c, next, commands.Refuse("from_node_id", "is a node of another board than the connection's", nil)
			}
			next.FromNodeID = from.ID
		}
	}
	if p.ToNodeID != "" {
		if id := decoded(ids.Node, p.ToNodeID); !sameUUID(id, c.ToNodeID) {
			to, err := liveNode(ctx, q, c.WorkspaceID, c.ProjectID, "to_node_id", p.ToNodeID)
			if err != nil {
				return c, next, err
			}
			if to.BoardID != c.BoardID {
				return c, next, commands.Refuse("to_node_id", "is a node of another board than the connection's", nil)
			}
			next.ToNodeID = to.ID
		}
	}
	switch {
	case p.Order != "":
		next.SortKey = p.Order
	case !sameUUID(next.FromNodeID, c.FromNodeID):
		// The connection leaves another node, after whose last it goes.
		if next.SortKey, err = sortKey(ctx, q, c.WorkspaceID, c.ProjectID, next.FromNodeID, "", nil); err != nil {
			return c, next, err
		}
	}
	return c, next, nil
}

// sortKey returns the key that a connection that leaves the node from is
// placed at: the key given, if it is not "", or else one after the last
// connection's that leaves the node, as board.Place places a child after a
// folder's last: those in the trash included, so that one restored later
// keeps its place, unless that key has no key after it. moving is the
// connection that moves among the node's connections, which it leaves
// already, or nil for one that it does not leave yet: it keeps its key if it
// comes after every other that is not in the trash.
func sortKey(ctx context.Context, q *queries.Queries, ws, project, from pgtype.UUID, given string,
	moving *board.Child) (string, error) {
	if given != "" {
		return given, nil
	}
	params := queries.LastConnectionSortKeysParams{WorkspaceID: ws, ProjectID: project, FromNodeID: from}
	if moving != nil {
		params.Moved = moving.ID
	}
	last, err := q.LastConnectionSortKeys(ctx, params)
	if err != nil {
		return "", fmt.Errorf("connection: read the node's last keys: %w", err)
	}
	return board.Place(board.Last{Key: last.Last, Live: last.LastLive, LiveID: last.LastLiveID}, moving,
		"the node's last connection")
}

// fromNode returns the node whose ID is id, a node's ID that its payload's
// Check checked, as liveNode does, once it has checked that a connection may
// leave it: that it is an element, since a jumper has no connections out,
// and a branch's connections leave its outputs (plan item 43). It refuses the
// batch otherwise, at the payload's field at.
func fromNode(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, at, id string) (queries.Node,
	error) {
	n, err := liveNode(ctx, q, ws, project, at, id)
	switch {
	case err != nil:
		return queries.Node{}, err
	case n.Type == node.TypeJumper:
		return queries.Node{}, commands.Refuse(at, "is a jumper, which no connection leaves: the flow continues at "+
			"its target (PRD §5.8.5)", nil)
	case n.Type == node.TypeBranch:
		return queries.Node{}, commands.Refuse(at, "is a branch, whose connections leave its outputs, not the "+
			"branch itself (PRD FR-EDIT-04)", nil)
	}
	return n, nil
}

// liveNode returns the node of the project whose ID is id, a node's ID that
// its payload's Check checked, or refuses the batch, at the payload's field
// at, if the project has no such node, or it is in the trash, itself or with
// its board, where nothing connects to it.
func liveNode(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, at, id string) (queries.Node,
	error) {
	row, err := q.NodeAny(ctx, queries.NodeAnyParams{WorkspaceID: ws, ProjectID: project, ID: decoded(ids.Node, id)})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return queries.Node{}, commands.Refuse(at, "is not a node of the project", nil)
	case err != nil:
		return queries.Node{}, fmt.Errorf("connection: read the node: %w", err)
	case row.Node.DeletedAt.Valid:
		return queries.Node{}, commands.Refuse(at, "is a node in the project's trash, which nothing connects to "+
			"until it is restored", nil)
	case row.BoardDeleted:
		return queries.Node{}, commands.Refuse(at, "is a node of a board in the project's trash, which nothing "+
			"connects to until the board is restored", nil)
	}
	return row.Node, nil
}

// checkNewConnection refuses the batch, at id, if the project has a
// connection whose UUID is id, the ID of a connection that a command creates,
// before anything else is checked, as a node's create does; the insert
// refuses the ID of a connection that the transaction does not see
// (refuseTakenID).
func checkNewConnection(ctx context.Context, q *queries.Queries, ws, project, id pgtype.UUID) error {
	_, err := q.ConnectionAny(ctx, queries.ConnectionAnyParams{WorkspaceID: ws, ProjectID: project, ID: id})
	switch {
	case err == nil:
		return refuseTakenID()
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("connection: read the connection: %w", err)
	}
	return nil
}

// refuseTakenID returns the refusal, at id, of a command that creates a
// connection with the ID of one that exists already, which may be another
// workspace's, or another project's, whose rows the transaction does not see.
func refuseTakenID() error {
	return commands.Refuse("id", "is the ID of a connection that exists already", nil)
}

// anyConnection returns the connection of the batch's project whose ID is id,
// a connection's ID that its payload's Check checked, whether or not it is in
// the trash, with whether it is in the trash with one of its nodes or its
// board, or refuses the batch, at id, if the project has no such connection.
func anyConnection(ctx context.Context, tx *commands.Tx, id string) (queries.ConnectionAnyRow, error) {
	row, err := queries.New(tx).ConnectionAny(ctx, queries.ConnectionAnyParams{
		WorkspaceID: nullUUID(tx.WorkspaceID()), ProjectID: nullUUID(tx.ProjectID()),
		ID: decoded(ids.Connection, id)})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return queries.ConnectionAnyRow{}, commands.Refuse("id", "is not a connection of the project", nil)
	case err != nil:
		return queries.ConnectionAnyRow{}, fmt.Errorf("connection: read the connection: %w", err)
	}
	return row, nil
}

// liveConnectionOf returns the connection of the batch's project whose ID is
// id as anyConnection does, or refuses the batch if it is in the trash, where
// nothing but restoring it changes it, or one of its nodes or its board is,
// with which it comes back.
func liveConnectionOf(ctx context.Context, tx *commands.Tx, id string) (queries.Connection, error) {
	row, err := anyConnection(ctx, tx, id)
	switch {
	case err != nil:
		return queries.Connection{}, err
	case row.Connection.DeletedAt.Valid:
		return queries.Connection{}, commands.Refuse("id", "is a connection in the project's trash, which only "+
			"restoring it changes", nil)
	case row.EndsDeleted:
		return queries.Connection{}, refuseEndsInTrash()
	}
	return row.Connection, nil
}

// refuseEndsInTrash returns the refusal, at id, of a command that would
// change a connection of a node or a board in the trash, which is in the
// trash with it, and which nothing changes until its nodes are restored.
func refuseEndsInTrash() error {
	return commands.Refuse("id", "is a connection of a node in the project's trash, itself or with its board, "+
		"which comes back with its nodes", nil)
}

// sameUUID reports whether the columns a and b are the same: both null, or
// both the same UUID.
func sameUUID(a, b pgtype.UUID) bool {
	return a.Valid == b.Valid && (!a.Valid || a.Bytes == b.Bytes)
}

// changed returns a function that reports the change of the connection id,
// to the revision that an update returned, with err, or reports nothing if
// the update found nothing to change, which it returns as pgx.ErrNoRows.
func changed(tx *commands.Tx, id pgtype.UUID) func(rev int64, err error) error {
	return func(rev int64, err error) error {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		case err != nil:
			return fmt.Errorf("connection: change the connection: %w", err)
		}
		return tx.Changed(ids.Connection, id.Bytes, rev)
	}
}

// by returns the batch's actor, as the column of who changed a row.
func by(tx *commands.Tx) pgtype.UUID {
	return nullUUID(tx.Actor().UserID)
}

// decoded returns the UUID of id, an ID of the kind that a payload's Check
// checked, as a uuid column.
func decoded(kind ids.Kind, id string) pgtype.UUID {
	// Check refused a payload whose ID does not decode.
	u, _ := ids.Decode(kind, id)
	return nullUUID(u)
}

// newID returns the problem of id, the ID of an entity of the kind that a
// command creates, unless it is an ID of the kind of a UUIDv7 (PRD §5.2).
func newID(kind ids.Kind, id string) input.FieldErrors {
	u, err := ids.Decode(kind, id)
	switch {
	case err != nil:
		return field("id", fmt.Sprintf("is not a %s's ID: %v", kind, err))
	case !u.IsV7():
		return field("id", "is not the ID of a UUIDv7, which the ID of every entity is")
	}
	return nil
}

// entityID returns the problem of the payload's field name, unless its value
// id is an ID of the kind.
func entityID(kind ids.Kind, name, id string) input.FieldErrors {
	if _, err := ids.Decode(kind, id); err != nil {
		return field(name, fmt.Sprintf("is not a %s's ID: %v", kind, err))
	}
	return nil
}

// optionalID returns the problem of the payload's field name as entityID
// does, unless its value id is "", for none.
func optionalID(kind ids.Kind, name, id string) input.FieldErrors {
	if id == "" {
		return nil
	}
	return entityID(kind, name, id)
}

// field returns the problem msg of the payload's field name, or none if msg
// is "".
func field(name, msg string) input.FieldErrors {
	if msg == "" {
		return nil
	}
	return input.FieldErrors{{Field: name, Message: msg}}
}

// uuids returns entities as uuid columns.
func uuids(entities []ids.UUID) []pgtype.UUID {
	out := make([]pgtype.UUID, len(entities))
	for i, id := range entities {
		out[i] = nullUUID(id)
	}
	return out
}
