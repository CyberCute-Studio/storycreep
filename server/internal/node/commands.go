package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/expression"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
)

// The types of command of a node (PRD §5.7). Each names the node that it is
// about by its ID, id, and applies to the node of the batch's project.
const (
	// Create creates a node of a board, whose ID the client makes, so that
	// it can refer to the node before the server answers (FR-EDIT-02 AC1).
	// Deleting it to the trash undoes it.
	Create commands.Type = "node.create"
	// Update changes the fields of a node that its payload names, and leaves
	// the others as they are. Changing them back undoes it.
	Update commands.Type = "node.update"
	// Move moves nodes, as many as a selection holds, as one command
	// (FR-EDIT-02 AC2). Moving them back undoes it.
	Move commands.Type = "node.move"
	// Duplicate creates a node, whose ID the client makes, as a copy of
	// another of the same board at another position, with its scripts, but
	// neither its outputs nor its connections, which are entities of their
	// own. Deleting it undoes it.
	Duplicate commands.Type = "node.duplicate"
	// Delete deletes a node to the project's trash, and Restore restores it.
	// Each undoes the other. The node's connections are in the trash with it,
	// and come back with it where their other nodes are not in the trash
	// (package connection).
	Delete  commands.Type = "node.delete"
	Restore commands.Type = "node.restore"
	// DeleteMany deletes nodes, as many as a selection holds, to the trash
	// as one command, as node.delete deletes one, and RestoreMany restores
	// them, as node.restore restores one. Each undoes the other. A paste's
	// undo is one (package clipboard), so that it expects each node that the
	// paste created once, where a node.delete of each would expect every one
	// of them in each of its steps.
	DeleteMany  commands.Type = "node.delete_many"
	RestoreMany commands.Type = "node.restore_many"
	// SetBoardEntry changes the entry node of a board, the node where the
	// flow starts when it enters the board without one, as by the project's
	// entry. Changing it back undoes it.
	SetBoardEntry commands.Type = "board.set_entry_node"
)

// createPayload is Create's payload.
type createPayload struct {
	ID                 string        `json:"id" doc:"The new node's ID, a nd_ ID of a UUIDv7 that the client makes, which no entity of the instance has (PRD §5.2)"`
	BoardID            string        `json:"board_id" doc:"The ID of the node's board, which is not in the project's trash, and which is fixed once the node is created"`
	Type               string        `json:"type" enum:"element,branch,jumper" doc:"The node's type, which is fixed once it is created: an element, a line of dialogue or of narration; a branch, which picks the first of its outputs whose condition holds; or a jumper, which continues the flow at its target (PRD §5.8.5)"`
	Kind               string        `json:"kind,omitempty" enum:"dialogue,narration" doc:"An element's kind: dialogue, which a speaker says, or narration. Without it, dialogue. Another type has none, and is answered with 422 validation_failed for one."`
	Title              string        `json:"title,omitempty" maxLength:"200" doc:"The node's title, of at most 200 characters, without white space around it, and without control characters, bidirectional controls, and line separators among it. Without it, the node has none."`
	Text               *richtext.Doc `json:"text,omitempty" doc:"An element's text, a rich-text document (PRD FR-TEXT-01). Without it, an empty paragraph. Another type has none."`
	SpeakerComponentID string        `json:"speaker_component_id,omitempty" doc:"The ID of an element's speaker, a component of the project, in its trash or not. Without it, none. Another type has none."`
	CoverAssetID       string        `json:"cover_asset_id,omitempty" doc:"The ID of an element's cover image, an asset of the project. Without it, none. Another type has none."`
	Color              string        `json:"color,omitempty" enum:"none,red,orange,yellow,green,teal,blue,purple,pink,gray" doc:"The node's colour, none or a name of the palette that boards have. Without it, none."`
	Tags               []string      `json:"tags,omitempty" maxItems:"32" doc:"The node's tags, at most 32, each of 1 to 50 characters, not all of them blank, without white space around it, and without control characters, bidirectional controls, and line separators among them, and no two the same tag but for their case, which tags are compared without, as Quest and quest are one tag. They are kept as they are written. Without them, none."`
	Position           NodePosition  `json:"position" doc:"Where the node goes on its board's canvas"`
	Size               *NodeSize     `json:"size,omitempty" doc:"The node's width and height. Without it, or with {}, the node has no size of its own, and the canvas lays it out by itself."`
	IsEnding           bool          `json:"is_ending,omitempty" doc:"Whether the flow ends at the element (PRD §5.8.5). Without it, false. Another type may not."`
	Target             *NodeTarget   `json:"target,omitempty" doc:"A jumper's target: a node of the project, in its trash or not, and that node's board, which may be the jumper's own. Without it, or with {}, the jumper has none, which the validation reports. Another type has none."`
	OnEnter            string        `json:"on_enter,omitempty" maxLength:"16384" doc:"An element's or a branch's on-enter script, which runs when the flow enters the node (PRD §5.8.5): the source of a script, of at most 16384 characters, which may not compile, and is then kept, marked invalid (PRD FR-EDIT-03 AC4). Without it, or with the empty string, none. A jumper has none."`
	OnExit             string        `json:"on_exit,omitempty" maxLength:"16384" doc:"An element's or a branch's on-exit script, which runs when the flow leaves the node, by a connection or by ending there (PRD §5.8.5), as on_enter's source is. Without it, or with the empty string, none. A jumper has none."`
}

// updatePayload is Update's payload. A field that it leaves out stays as it
// is.
type updatePayload struct {
	ID                 string        `json:"id" doc:"The node's ID"`
	Kind               *string       `json:"kind,omitempty" enum:"dialogue,narration" doc:"An element's new kind. Another type has none, and is answered with 422 validation_failed for one."`
	Title              *string       `json:"title,omitempty" maxLength:"200" doc:"The node's new title, of at most 200 characters, which may be empty, without white space around it, and without control characters, bidirectional controls, and line separators among it"`
	Text               *richtext.Doc `json:"text,omitempty" doc:"An element's new text, a rich-text document (PRD FR-TEXT-01), which may be an empty paragraph. Another type has none."`
	SpeakerComponentID *string       `json:"speaker_component_id,omitempty" doc:"The ID of an element's new speaker, a component of the project, in its trash or not, or the empty string for none. Another type has none."`
	CoverAssetID       *string       `json:"cover_asset_id,omitempty" doc:"The ID of an element's new cover image, an asset of the project, or the empty string for none. Another type has none."`
	Color              *string       `json:"color,omitempty" enum:"none,red,orange,yellow,green,teal,blue,purple,pink,gray" doc:"The node's new colour, none or a name of the palette that boards have"`
	Tags               *[]string     `json:"tags,omitempty" maxItems:"32" doc:"The node's new tags, which replace its tags, as [] removes them: at most 32, each of 1 to 50 characters, not all of them blank, without white space around it, and without control characters, bidirectional controls, and line separators among them, and no two the same tag but for their case, which tags are compared without. They are kept as they are written."`
	Size               *NodeSize     `json:"size,omitempty" doc:"The node's new width and height, or {} for no size of its own, which the canvas then lays the node out by itself for"`
	IsEnding           *bool         `json:"is_ending,omitempty" doc:"Whether the flow ends at the element (PRD §5.8.5). Another type may not."`
	Target             *NodeTarget   `json:"target,omitempty" doc:"A jumper's new target: a node of the project, in its trash or not, and that node's board, or the target that it has, which may no longer exist, or {} for none, which the validation reports. Another type has none."`
	OnEnter            *string       `json:"on_enter,omitempty" maxLength:"16384" doc:"An element's or a branch's new on-enter script (PRD §5.8.5): the source of a script, of at most 16384 characters, which may not compile, and is then kept, marked invalid (PRD FR-EDIT-03 AC4), or the empty string for none. A jumper has none."`
	OnExit             *string       `json:"on_exit,omitempty" maxLength:"16384" doc:"An element's or a branch's new on-exit script, as on_enter's is, or the empty string for none. A jumper has none."`
}

// NodePlace is where node.move moves a node to.
type NodePlace struct {
	ID       string       `json:"id" doc:"The node's ID"`
	Position NodePosition `json:"position" doc:"Where the node goes on its board's canvas"`
}

// movePayload is Move's payload.
type movePayload struct {
	Nodes []NodePlace `json:"nodes" minItems:"1" maxItems:"10000" nullable:"false" doc:"The nodes that move, each once, at most 10000, of any of the project's boards that are not in its trash, each with where it goes. One that is there already stays as it is."`
}

// duplicatePayload is Duplicate's payload.
type duplicatePayload struct {
	ID       string       `json:"id" doc:"The new node's ID, a nd_ ID of a UUIDv7 that the client makes, which no entity of the instance has (PRD §5.2)"`
	SourceID string       `json:"source_id" doc:"The ID of the node that the new node is a copy of, which is not in the project's trash: the new node is of the same board, and has its type and every field of it but its position, its text, speaker, target, and scripts among them, but neither a branch's outputs nor the node's connections, which are entities of their own, and which a client copies with commands of their own"`
	Position NodePosition `json:"position" doc:"Where the new node goes on the board's canvas"`
}

// idPayload is the payload of a command that needs nothing but its node's
// ID.
type idPayload struct {
	ID string `json:"id" doc:"The node's ID"`
}

// manyPayload is the payload of DeleteMany and RestoreMany.
type manyPayload struct {
	IDs []string `json:"ids" minItems:"1" maxItems:"10000" nullable:"false" doc:"The IDs of the nodes, each once, at most 10000, of any of the project's boards"`
}

// entryPayload is SetBoardEntry's payload.
type entryPayload struct {
	ID     string `json:"id" doc:"The board's ID"`
	NodeID string `json:"node_id,omitempty" doc:"The ID of the board's new entry node, a node of the board, in the project's trash or not, or the entry node that the board has, which may no longer exist. Without it, the board has none."`
}

// Register registers in r the node as an entity of the pipeline, whose
// revisions a command expects as it does any entity's, and its commands, each
// of which changes the project's draft, so that an API token needs
// project:write for it (FR-AUTH-04), and its role "Edit nodes, connections,
// text, expressions" (§5.6), but board.set_entry_node, a change of a board,
// which needs "Create/rename/archive boards", as the board's other commands
// do, and which Register registers here, because it refers to a node, which
// the board's registration (board.Register) knows nothing of. It registers a
// branch's outputs and their commands too. The node's commands refer to its
// board and its speaker, so r has the board and the component registered
// already (board.Register, component.Register). A node's scripts and an
// output's condition are expressions, which the derivation that
// expression.Register registers compiles, once connection.Register has
// registered the connection, whose expressions it compiles too.
func Register(r *commands.Registry) {
	commands.RegisterEntity(r, commands.Entity{
		Kind: ids.Node,
		Revs: func(ctx context.Context, tx *commands.Tx, entities []ids.UUID) (map[ids.UUID]int64, error) {
			rows, err := queries.New(tx).NodeRevs(ctx, queries.NodeRevsParams{WorkspaceID: nullUUID(tx.WorkspaceID()),
				ProjectID: nullUUID(tx.ProjectID()), Ids: uuids(entities)})
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
			q := queries.New(tx)
			ws, project := nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID())
			rows, err := q.NodesByID(ctx, queries.NodesByIDParams{WorkspaceID: ws, ProjectID: project,
				Ids: uuids(entities)})
			if err != nil {
				return nil, err
			}
			list := make([]queries.Node, len(rows))
			for i, row := range rows {
				list[i] = row.Node
			}
			nodes, err := nodesOf(ctx, q, ws, project, list)
			if err != nil {
				return nil, err
			}
			out := make(map[ids.UUID]any, len(rows))
			for i, row := range rows {
				n := nodes[i]
				// A node of a board in the trash is in the trash with it, which
				// its state says, as the state of an entity in the trash does,
				// though only its board's deletion put it there.
				if n.DeletedAt == nil && row.BoardDeletedAt.Valid {
					n.DeletedAt = timeOf(row.BoardDeletedAt)
				}
				// A branch in the trash shows the outputs that are there with
				// it alone, which come back with it, each with the branch's
				// deleted_at.
				for j := range n.Outputs {
					if n.Outputs[j].DeletedAt == nil {
						n.Outputs[j].DeletedAt = n.DeletedAt
					}
				}
				out[row.Node.ID.Bytes] = n
			}
			return out, nil
		},
	})
	registerOutputs(r)

	node := []ids.Kind{ids.Node}
	op, sc := authz.EditContent, scope.ProjectWrite

	commands.Register(r, commands.Spec[createPayload]{
		Type: Create, Capability: op, Scope: sc, Touches: []ids.Kind{ids.Node, ids.Board, ids.Component},
		Check: checkCreate,
		CheckNew: func(p createPayload) input.FieldErrors {
			return append(field("title", checkTitle(p.Title)), checkTags("tags", p.Tags)...)
		},
		Inverse: func(_ context.Context, _ *commands.Tx, p createPayload) ([]commands.Step, error) {
			return []commands.Step{{Type: Delete, Payload: idPayload{ID: p.ID}}}, nil
		},
		Apply: applyCreate,
	})

	commands.Register(r, commands.Spec[updatePayload]{
		Type: Update, Capability: op, Scope: sc, Touches: []ids.Kind{ids.Node, ids.Board, ids.Component},
		Check: checkUpdate,
		// The step of the inverse writes back the stored title and tags,
		// which the text rules may refuse (commands.Spec.CheckNew).
		CheckNew: func(p updatePayload) input.FieldErrors {
			var errs input.FieldErrors
			if p.Title != nil {
				errs = field("title", checkTitle(*p.Title))
			}
			if p.Tags != nil {
				errs = append(errs, checkTags("tags", *p.Tags)...)
			}
			return errs
		},
		Inverse: func(ctx context.Context, tx *commands.Tx, p updatePayload) ([]commands.Step, error) {
			n, _, err := planUpdate(ctx, tx, p)
			if err != nil {
				return nil, err
			}
			back, err := updateBack(n, p)
			if err != nil {
				return nil, err
			}
			scripts, err := expression.Back(ctx, queries.New(tx), n.WorkspaceID, n.ProjectID, ids.Node, n.ID,
				map[expression.Field]*string{expression.OnEnter: p.OnEnter, expression.OnExit: p.OnExit})
			if err != nil {
				return nil, err
			}
			back.OnEnter, back.OnExit = scripts[expression.OnEnter], scripts[expression.OnExit]
			return []commands.Step{{Type: Update, Payload: back}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p updatePayload) error {
			n, next, err := planUpdate(ctx, tx, p)
			if err != nil {
				return err
			}
			owner := expression.Owner{Kind: ids.Node, ID: n.ID, Board: n.BoardID}
			scripts, err := setScripts(ctx, tx, owner, p.OnEnter, p.OnExit)
			if err != nil {
				return err
			}
			next.UpdatedBy, next.WorkspaceID, next.ProjectID, next.ID = by(tx), n.WorkspaceID, n.ProjectID, n.ID
			rev, err := queries.New(tx).UpdateNode(ctx, next)
			// A change of the node's scripts alone is a change of the node,
			// whose revision it increments.
			return changed(tx, n.ID)(expression.OwnerRevision(ctx, tx, owner, scripts, rev, err))
		},
	})

	commands.Register(r, commands.Spec[movePayload]{
		Type: Move, Capability: op, Scope: sc, Touches: node,
		Check: checkMove,
		Inverse: func(ctx context.Context, tx *commands.Tx, p movePayload) ([]commands.Step, error) {
			before, err := movedNodes(ctx, tx, p)
			if err != nil {
				return nil, err
			}
			var back movePayload
			for i, place := range p.Nodes {
				x, y := position(place.Position)
				if old := before[i]; !sameNumeric(old.X, x) || !sameNumeric(old.Y, y) {
					pos, err := positionOf(old.X, old.Y)
					if err != nil {
						return nil, err
					}
					back.Nodes = append(back.Nodes, NodePlace{ID: place.ID, Position: pos})
				}
			}
			if len(back.Nodes) == 0 {
				return []commands.Step{}, nil
			}
			return []commands.Step{{Type: Move, Payload: back}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p movePayload) error {
			if _, err := movedNodes(ctx, tx, p); err != nil {
				return err
			}
			params := queries.MoveNodesParams{UpdatedBy: by(tx), WorkspaceID: nullUUID(tx.WorkspaceID()),
				ProjectID: nullUUID(tx.ProjectID())}
			for _, place := range p.Nodes {
				x, y := position(place.Position)
				params.Ids = append(params.Ids, decoded(ids.Node, place.ID))
				params.Xs, params.Ys = append(params.Xs, x), append(params.Ys, y)
			}
			rows, err := queries.New(tx).MoveNodes(ctx, params)
			if err != nil {
				return fmt.Errorf("node: move the nodes: %w", err)
			}
			for _, row := range rows {
				if err := tx.Changed(ids.Node, row.ID.Bytes, row.Rev); err != nil {
					return err
				}
			}
			return nil
		},
	})

	commands.Register(r, commands.Spec[duplicatePayload]{
		Type: Duplicate, Capability: op, Scope: sc, Touches: node,
		Check: func(p duplicatePayload) input.FieldErrors {
			errs := newID(ids.Node, p.ID)
			errs = append(errs, entityID(ids.Node, "source_id", p.SourceID)...)
			return append(errs, checkPosition("position", p.Position)...)
		},
		Inverse: func(_ context.Context, _ *commands.Tx, p duplicatePayload) ([]commands.Step, error) {
			return []commands.Step{{Type: Delete, Payload: idPayload{ID: p.ID}}}, nil
		},
		Apply: applyDuplicate,
	})

	commands.Register(r, commands.Spec[idPayload]{
		Type: Delete, Capability: op, Scope: sc, Touches: node,
		Check: func(p idPayload) input.FieldErrors { return entityID(ids.Node, "id", p.ID) },
		// Deleting a node that is in the trash already does nothing, and its
		// undo nothing either (deletes).
		Inverse: func(ctx context.Context, tx *commands.Tx, p idPayload) ([]commands.Step, error) {
			if ok, err := deletes(ctx, tx, "id", p.ID); err != nil || !ok {
				return []commands.Step{}, err
			}
			return []commands.Step{{Type: Restore, Payload: idPayload{ID: p.ID}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p idPayload) error { return deleteNode(ctx, tx, "id", p.ID) },
	})

	commands.Register(r, commands.Spec[idPayload]{
		Type: Restore, Capability: op, Scope: sc, Touches: node,
		Check: func(p idPayload) input.FieldErrors { return entityID(ids.Node, "id", p.ID) },
		// Restoring a node that is not in the trash does nothing, and its undo
		// nothing either (restores).
		Inverse: func(ctx context.Context, tx *commands.Tx, p idPayload) ([]commands.Step, error) {
			if ok, err := restores(ctx, tx, "id", p.ID); err != nil || !ok {
				return []commands.Step{}, err
			}
			return []commands.Step{{Type: Delete, Payload: idPayload{ID: p.ID}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p idPayload) error {
			return restoreNode(ctx, tx, "id", p.ID)
		},
	})

	// node.delete_many and node.restore_many do what node.delete and
	// node.restore do to each node of their payload, each reading the nodes,
	// and changing them, in one statement, and the inverse of each does the
	// other to those that it changes.
	commands.Register(r, commands.Spec[manyPayload]{
		Type: DeleteMany, Capability: op, Scope: sc, Touches: node,
		Check: checkMany,
		Inverse: func(ctx context.Context, tx *commands.Tx, p manyPayload) ([]commands.Step, error) {
			return manyInverse(ctx, tx, p, deletable, RestoreMany)
		},
		Apply: applyDeleteMany,
	})

	commands.Register(r, commands.Spec[manyPayload]{
		Type: RestoreMany, Capability: op, Scope: sc, Touches: node,
		Check: checkMany,
		Inverse: func(ctx context.Context, tx *commands.Tx, p manyPayload) ([]commands.Step, error) {
			return manyInverse(ctx, tx, p, restorable, DeleteMany)
		},
		Apply: applyRestoreMany,
	})

	commands.Register(r, commands.Spec[entryPayload]{
		Type: SetBoardEntry, Capability: authz.ManageBoards, Scope: sc, Touches: []ids.Kind{ids.Board, ids.Node},
		Check: func(p entryPayload) input.FieldErrors {
			return append(entityID(ids.Board, "id", p.ID), optionalID(ids.Node, "node_id", p.NodeID)...)
		},
		Inverse: func(ctx context.Context, tx *commands.Tx, p entryPayload) ([]commands.Step, error) {
			b, err := liveBoardOf(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: SetBoardEntry, Payload: entryPayload{ID: p.ID,
				NodeID: encode(ids.Node, b.EntryNodeID)}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p entryPayload) error {
			b, err := liveBoardOf(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			entry := pgtype.UUID{}
			if p.NodeID != "" {
				entry = decoded(ids.Node, p.NodeID)
				if !sameUUID(entry, b.EntryNodeID) {
					row, err := anyNode(ctx, tx, "node_id", p.NodeID)
					if err != nil {
						return err
					}
					if row.Node.BoardID != b.ID {
						return commands.Refuse("node_id", "is a node of another board", nil)
					}
				}
			}
			return changedBoard(tx, b.ID)(queries.New(tx).UpdateBoardEntryNode(ctx, queries.UpdateBoardEntryNodeParams{
				EntryNodeID: entry, UpdatedBy: by(tx), WorkspaceID: b.WorkspaceID, ProjectID: b.ProjectID, ID: b.ID}))
		},
	})
}

// checkCreate returns what is wrong with Create's payload p that its schema
// cannot say, but for the text rules of its title and tags (CheckNew): its
// IDs, and what checkCreated checks.
func checkCreate(p createPayload) input.FieldErrors {
	errs := newID(ids.Node, p.ID)
	errs = append(errs, entityID(ids.Board, "board_id", p.BoardID)...)
	return append(errs, checkCreated(Created{Type: p.Type, Kind: p.Kind, Text: p.Text != nil,
		SpeakerComponentID: p.SpeakerComponentID, CoverAssetID: p.CoverAssetID, Position: p.Position, Size: p.Size,
		IsEnding: p.IsEnding, Target: p.Target, OnEnter: p.OnEnter, OnExit: p.OnExit})...)
}

// Created is a node as node.create creates it, but for its ID, its board, and
// its text, of which it says only whether the node has one: what package
// clipboard checks of a node of a clipboard, which a paste creates with
// node.create, as node.create checks it (CheckCreated).
type Created struct {
	Type, Kind, Title                string
	Text                             bool
	SpeakerComponentID, CoverAssetID string
	Tags                             []string
	Position                         NodePosition
	Size                             *NodeSize
	IsEnding                         bool
	Target                           *NodeTarget
	OnEnter, OnExit                  string
}

// CheckCreated returns what node.create would find wrong with the node n
// before it reads the project, as its Check and its CheckNew find it, each at
// its field of a node.create's payload, as on_enter: a field that only an
// element or a jumper has, or a script, set on a node of another type, the
// lengths of its scripts' sources, the IDs of its speaker and its cover, its
// position, its size, and its target, and the text rules of its title and its
// tags, those of a new node, so that package clipboard holds a clipboard's
// nodes to the rules that a paste of them is held to, without a copy of them.
func CheckCreated(n Created) input.FieldErrors {
	errs := checkCreated(n)
	errs = append(errs, field("title", checkTitle(n.Title))...)
	return append(errs, checkTags("tags", n.Tags)...)
}

// checkCreated returns what is wrong with the node n that node.create's Check
// finds, but for its ID and its board: its position and size, its scripts'
// lengths, its speaker's, cover's, and target's IDs, and the fields that only
// an element or a jumper has, set on a node of another type.
func checkCreated(n Created) input.FieldErrors {
	errs := typedFields{kind: n.Kind != "", text: n.Text, speaker: n.SpeakerComponentID != "",
		cover: n.CoverAssetID != "", isEnding: n.IsEnding, target: n.Target != nil, onEnter: n.OnEnter != "",
		onExit: n.OnExit != ""}.misplaced(n.Type)
	errs = append(errs, field("on_enter", expression.CheckSource(n.OnEnter))...)
	errs = append(errs, field("on_exit", expression.CheckSource(n.OnExit))...)
	errs = append(errs, optionalID(ids.Component, "speaker_component_id", n.SpeakerComponentID)...)
	errs = append(errs, optionalID(ids.Asset, "cover_asset_id", n.CoverAssetID)...)
	errs = append(errs, checkPosition("position", n.Position)...)
	if n.Size != nil {
		errs = append(errs, checkSize("size", *n.Size)...)
	}
	if n.Target != nil {
		errs = append(errs, checkTarget("target", *n.Target)...)
	}
	return errs
}

// checkUpdate returns what is wrong with Update's payload p that its schema
// cannot say, but for the text rules of its title and tags (CheckNew): its
// IDs, and its size. Which fields its node's type takes, Apply checks.
func checkUpdate(p updatePayload) input.FieldErrors {
	errs := entityID(ids.Node, "id", p.ID)
	if p.SpeakerComponentID != nil {
		errs = append(errs, optionalID(ids.Component, "speaker_component_id", *p.SpeakerComponentID)...)
	}
	if p.CoverAssetID != nil {
		errs = append(errs, optionalID(ids.Asset, "cover_asset_id", *p.CoverAssetID)...)
	}
	if p.Size != nil {
		errs = append(errs, checkSize("size", *p.Size)...)
	}
	if p.Target != nil {
		errs = append(errs, checkTarget("target", *p.Target)...)
	}
	if p.OnEnter != nil {
		errs = append(errs, field("on_enter", expression.CheckSource(*p.OnEnter))...)
	}
	if p.OnExit != nil {
		errs = append(errs, field("on_exit", expression.CheckSource(*p.OnExit))...)
	}
	return errs
}

// checkMove returns what is wrong with Move's payload p that its schema
// cannot say: each node's ID, which is no other node's of the payload, and
// its position, once it has checked that p moves at most MaxMoved nodes, as
// its schema says too.
func checkMove(p movePayload) input.FieldErrors {
	if len(p.Nodes) > MaxMoved {
		return field("nodes", fmt.Sprintf("holds more than %d nodes", MaxMoved))
	}
	var errs input.FieldErrors
	first := make(map[ids.UUID]int, len(p.Nodes))
	for i, place := range p.Nodes {
		at := fmt.Sprintf("nodes[%d]", i)
		u, err := ids.Decode(ids.Node, place.ID)
		if err != nil {
			errs = append(errs, input.FieldError{Field: at + ".id", Message: fmt.Sprintf("is not a node's ID: %v", err)})
		} else if j, dup := first[u]; dup {
			errs = append(errs, input.FieldError{Field: at + ".id", Message: fmt.Sprintf("is the ID of nodes[%d] too, "+
				"which moves once", j)})
		} else {
			first[u] = i
		}
		errs = append(errs, checkPosition(at+".position", place.Position)...)
	}
	return errs
}

// checkMany returns what is wrong with the payload p of DeleteMany or
// RestoreMany that its schema cannot say: each node's ID, which is no other
// node's of the payload, once it has checked that p names at most MaxMany
// nodes, as its schema says too.
func checkMany(p manyPayload) input.FieldErrors {
	if len(p.IDs) > MaxMany {
		return field("ids", fmt.Sprintf("holds more than %d nodes", MaxMany))
	}
	var errs input.FieldErrors
	first := make(map[ids.UUID]int, len(p.IDs))
	for i, id := range p.IDs {
		at := fmt.Sprintf("ids[%d]", i)
		u, err := ids.Decode(ids.Node, id)
		if err != nil {
			errs = append(errs, input.FieldError{Field: at, Message: fmt.Sprintf("is not a node's ID: %v", err)})
		} else if j, dup := first[u]; dup {
			errs = append(errs, input.FieldError{Field: at, Message: fmt.Sprintf("is the ID of ids[%d] too, "+
				"which the command names once", j)})
		} else {
			first[u] = i
		}
	}
	return errs
}

// deletes reports whether deleting the node id, at the payload's field at,
// deletes it (deleteNode): whether it is a node of the project that is not
// in the trash, deleted itself or with its board, so that the inverse of a
// deletion that does nothing does nothing either. One of a board in the trash
// is refused by the deletion, so the inverse reads nothing more of it. It
// refuses the batch, at at, if the project has no such node.
func deletes(ctx context.Context, tx *commands.Tx, at, id string) (bool, error) {
	row, err := anyNode(ctx, tx, at, id)
	return err == nil && !row.Node.DeletedAt.Valid && !row.BoardDeleted, err
}

// restores reports whether restoring the node id, at the payload's field at,
// restores it (restoreNode): whether it is a node of the project that is in
// the trash, deleted itself, of a board that is not, since a node of a board
// in the trash comes back only with its board, which the restoration refuses,
// so the inverse reads nothing more of it. It refuses the batch, at at, if
// the project has no such node.
func restores(ctx context.Context, tx *commands.Tx, at, id string) (bool, error) {
	row, err := anyNode(ctx, tx, at, id)
	return err == nil && row.Node.DeletedAt.Valid && !row.BoardDeleted, err
}

// deleteNode deletes the node id of the batch's project, at the payload's
// field at, to the trash, as node.delete does: a node in the trash already
// stays as it is, and one of a board in the trash, in the trash with it but
// not deleted itself, is refused, as every command but its board's
// restoration refuses it, because its undo, a restore, would be refused until
// the board is restored, as a board's variable's is (package variable).
func deleteNode(ctx context.Context, tx *commands.Tx, at, id string) error {
	row, err := anyNode(ctx, tx, at, id)
	switch {
	case err != nil:
		return err
	case row.Node.DeletedAt.Valid:
		return nil
	case row.BoardDeleted:
		return refuseBoardInTrash(at)
	}
	n := row.Node
	rev, err := queries.New(tx).TrashNode(ctx, queries.TrashNodeParams{UpdatedBy: by(tx),
		WorkspaceID: n.WorkspaceID, ProjectID: n.ProjectID, ID: n.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err := changed(tx, n.ID)(rev, err); err != nil {
		return err
	}
	return tx.AddUsage(usage.Nodes, -1)
}

// restoreNode restores the node id of the batch's project, at the payload's
// field at, from the trash, as node.restore does: a node that is not in the
// trash stays as it is, and one of a board in the trash, which is in the
// trash with it, deleted on its own or not, is refused, since it comes back
// only with its board. Its connections whose other node is not in the trash
// come back with it, and the batch's answer lists the others (package
// connection, FR-PROJ-06 AC1).
func restoreNode(ctx context.Context, tx *commands.Tx, at, id string) error {
	row, err := anyNode(ctx, tx, at, id)
	switch {
	case err != nil:
		return err
	case row.BoardDeleted:
		return commands.Refuse(at, "is a node of a board in the project's trash, which comes back only with its "+
			"board: restore the board first", nil)
	case !row.Node.DeletedAt.Valid:
		return nil
	}
	n := row.Node
	rev, err := queries.New(tx).RestoreNode(ctx, queries.RestoreNodeParams{UpdatedBy: by(tx),
		WorkspaceID: n.WorkspaceID, ProjectID: n.ProjectID, ID: n.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err := changed(tx, n.ID)(rev, err); err != nil {
		return err
	}
	if err := tx.Restored(ids.Node, n.ID.Bytes); err != nil {
		return err
	}
	return tx.AddUsage(usage.Nodes, 1)
}

// manyNodes returns the nodes of the payload p of DeleteMany or RestoreMany,
// each at its place in p, read in one query, in the trash or not, with
// whether their boards are, or refuses the batch, at the place in p of the
// first that the project does not have, as anyNode does.
func manyNodes(ctx context.Context, tx *commands.Tx, p manyPayload) ([]queries.NodesByIDRow, error) {
	list := make([]pgtype.UUID, len(p.IDs))
	for i, id := range p.IDs {
		list[i] = decoded(ids.Node, id)
	}
	rows, err := queries.New(tx).NodesByID(ctx, queries.NodesByIDParams{WorkspaceID: nullUUID(tx.WorkspaceID()),
		ProjectID: nullUUID(tx.ProjectID()), Ids: list})
	if err != nil {
		return nil, fmt.Errorf("node: read the nodes: %w", err)
	}
	found := make(map[[16]byte]queries.NodesByIDRow, len(rows))
	for _, row := range rows {
		found[row.Node.ID.Bytes] = row
	}
	out := make([]queries.NodesByIDRow, len(list))
	for i, u := range list {
		row, ok := found[u.Bytes]
		if !ok {
			return nil, commands.Refuse(fmt.Sprintf("ids[%d]", i), "is not a node of the project", nil)
		}
		out[i] = row
	}
	return out, nil
}

// deletable reports whether DeleteMany deletes the node of row, as deletes
// reports of node.delete: whether it is not in the trash, deleted itself or
// with its board.
func deletable(row queries.NodesByIDRow) bool {
	return !row.Node.DeletedAt.Valid && !row.BoardDeletedAt.Valid
}

// restorable reports whether RestoreMany restores the node of row, as
// restores reports of node.restore: whether it is in the trash, deleted
// itself, of a board that is not.
func restorable(row queries.NodesByIDRow) bool {
	return row.Node.DeletedAt.Valid && !row.BoardDeletedAt.Valid
}

// manyInverse returns the inverse of the command of DeleteMany or RestoreMany
// whose payload is p: a step of the type back of the nodes of p that the
// command changes, as changes reports of each, in p's order, or no step if it
// changes none.
func manyInverse(ctx context.Context, tx *commands.Tx, p manyPayload, changes func(queries.NodesByIDRow) bool,
	back commands.Type) ([]commands.Step, error) {
	rows, err := manyNodes(ctx, tx, p)
	if err != nil {
		return nil, err
	}
	var nodes []string
	for i, row := range rows {
		if changes(row) {
			nodes = append(nodes, p.IDs[i])
		}
	}
	if len(nodes) == 0 {
		return []commands.Step{}, nil
	}
	return []commands.Step{{Type: back, Payload: manyPayload{IDs: nodes}}}, nil
}

// applyDeleteMany is DeleteMany's Apply: what deleteNode does to each node of
// p, in one statement, which refuses the batch at the place in p of the first
// node of a board in the trash that is not in the trash itself.
func applyDeleteMany(ctx context.Context, tx *commands.Tx, p manyPayload) error {
	rows, err := manyNodes(ctx, tx, p)
	if err != nil {
		return err
	}
	var list []pgtype.UUID
	for i, row := range rows {
		switch {
		case row.Node.DeletedAt.Valid:
			continue
		case row.BoardDeletedAt.Valid:
			return refuseBoardInTrash(fmt.Sprintf("ids[%d]", i))
		}
		list = append(list, row.Node.ID)
	}
	if len(list) == 0 {
		return nil
	}
	revs, err := queries.New(tx).TrashNodes(ctx, queries.TrashNodesParams{UpdatedBy: by(tx),
		WorkspaceID: nullUUID(tx.WorkspaceID()), ProjectID: nullUUID(tx.ProjectID()), Ids: list})
	if err != nil {
		return fmt.Errorf("node: delete the nodes: %w", err)
	}
	changed := make(map[[16]byte]int64, len(revs))
	for _, r := range revs {
		changed[r.ID.Bytes] = r.Rev
	}
	return reportMany(tx, list, changed, false)
}

// applyRestoreMany is RestoreMany's Apply: what restoreNode does to each node
// of p, in one statement, which refuses the batch at the place in p of the
// first node of a board in the trash.
func applyRestoreMany(ctx context.Context, tx *commands.Tx, p manyPayload) error {
	rows, err := manyNodes(ctx, tx, p)
	if err != nil {
		return err
	}
	var list []pgtype.UUID
	for i, row := range rows {
		switch {
		case row.BoardDeletedAt.Valid:
			return commands.Refuse(fmt.Sprintf("ids[%d]", i), "is a node of a board in the project's trash, which "+
				"comes back only with its board: restore the board first", nil)
		case !row.Node.DeletedAt.Valid:
			continue
		}
		list = append(list, row.Node.ID)
	}
	if len(list) == 0 {
		return nil
	}
	revs, err := queries.New(tx).RestoreNodes(ctx, queries.RestoreNodesParams{UpdatedBy: by(tx),
		WorkspaceID: nullUUID(tx.WorkspaceID()), ProjectID: nullUUID(tx.ProjectID()), Ids: list})
	if err != nil {
		return fmt.Errorf("node: restore the nodes: %w", err)
	}
	changed := make(map[[16]byte]int64, len(revs))
	for _, r := range revs {
		changed[r.ID.Bytes] = r.Rev
	}
	return reportMany(tx, list, changed, true)
}

// reportMany reports the change of each node of list whose new revision
// changed holds, in list's order, and its restoration too if restored, with
// its connections, as restoreNode reports one's, and takes each from the
// workspace's nodes, or adds it back if restored.
func reportMany(tx *commands.Tx, list []pgtype.UUID, changed map[[16]byte]int64, restored bool) error {
	var n int64
	for _, id := range list {
		rev, ok := changed[id.Bytes]
		if !ok {
			continue
		}
		if err := tx.Changed(ids.Node, id.Bytes, rev); err != nil {
			return err
		}
		if restored {
			if err := tx.Restored(ids.Node, id.Bytes); err != nil {
				return err
			}
		}
		n++
	}
	switch {
	case n == 0:
		return nil
	case !restored:
		n = -n
	}
	return tx.AddUsage(usage.Nodes, n)
}

// checkTarget returns the problems of a jumper's target at the field at:
// both a board's ID and a node's, or neither.
func checkTarget(at string, t NodeTarget) input.FieldErrors {
	switch {
	case t.BoardID == "" && t.NodeID == "":
		return nil
	case t.BoardID == "":
		return field(at+".board_id", "is missing, which a target with a node needs")
	case t.NodeID == "":
		return field(at+".node_id", "is missing, which a target with a board needs")
	}
	return append(entityID(ids.Board, at+".board_id", t.BoardID), entityID(ids.Node, at+".node_id", t.NodeID)...)
}

// typedFields says which of the fields that only an element has, only a
// jumper, or only an element or a branch, a payload sets: a create's and an
// update's, which check them alike.
type typedFields struct {
	// kind, text, speaker, cover, and isEnding are an element's alone.
	kind, text, speaker, cover, isEnding bool
	// target is a jumper's alone.
	target bool
	// onEnter and onExit are an element's or a branch's, which a jumper,
	// where the flow never stops, has none of (§5.8.5).
	onEnter, onExit bool
}

// misplaced returns the problems of the fields that the payload sets, as f
// says, that a node of the type nodeType does not have, in the order of the
// payload's fields.
func (f typedFields) misplaced(nodeType string) input.FieldErrors {
	var errs input.FieldErrors
	if nodeType != TypeElement {
		for _, c := range []struct {
			name string
			set  bool
		}{{"kind", f.kind}, {"text", f.text}, {"speaker_component_id", f.speaker}, {"cover_asset_id", f.cover},
			{"is_ending", f.isEnding}} {
			if c.set {
				errs = append(errs, notOf(c.name, nodeType, TypeElement))
			}
		}
	}
	if nodeType != TypeJumper && f.target {
		errs = append(errs, notOf("target", nodeType, TypeJumper))
	}
	if nodeType == TypeJumper {
		for _, c := range []struct {
			name string
			set  bool
		}{{"on_enter", f.onEnter}, {"on_exit", f.onExit}} {
			if c.set {
				errs = append(errs, input.FieldError{Field: c.name, Message: "is set on a jumper, which has no " +
					"scripts, since the flow continues at its target before anything happens at it (PRD §5.8.5): " +
					"only an element or a branch has them"})
			}
		}
	}
	return errs
}

// notOf returns the problem of a payload's field that only a node of the
// type typ has, which a node of the type nodeType does not.
func notOf(name, nodeType, typ string) input.FieldError {
	return input.FieldError{Field: name, Message: fmt.Sprintf("is set on a node of the type %s, which has none: "+
		"only a node of the type %s has it", nodeType, typ)}
}

// checkTitle says what is wrong with a node's title, or returns "": empty, or
// a name of at most MaxTitleLength characters, without white space around it,
// and without control characters (input.CheckTrimmedName).
func checkTitle(title string) string {
	if title == "" {
		return ""
	}
	return input.CheckTrimmedName(title, MaxTitleLength)
}

// applyCreate is Create's Apply.
func applyCreate(ctx context.Context, tx *commands.Tx, p createPayload) error {
	q := queries.New(tx)
	ws, project := nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID())
	id := decoded(ids.Node, p.ID)
	if err := checkNewNode(ctx, q, ws, project, id); err != nil {
		return err
	}
	boardID := decoded(ids.Board, p.BoardID)
	if _, err := q.Board(ctx, queries.BoardParams{WorkspaceID: ws, ProjectID: project, ID: boardID}); errors.Is(err,
		pgx.ErrNoRows) {
		return commands.Refuse("board_id", "is not a board of the project, or is one in its trash", nil)
	} else if err != nil {
		return fmt.Errorf("node: read the board: %w", err)
	}
	params := queries.InsertNodeParams{ID: id, WorkspaceID: ws, ProjectID: project, BoardID: boardID, Type: p.Type,
		Title: p.Title, Color: p.Color, Tags: p.Tags, IsEnding: p.IsEnding, CreatedBy: by(tx)}
	if params.Color == "" {
		params.Color = board.ColorNone
	}
	if params.Tags == nil {
		params.Tags = []string{}
	}
	params.X, params.Y = position(p.Position)
	if p.Size != nil {
		params.Width, params.Height = size(*p.Size)
	}
	if p.Type == TypeElement {
		kind := p.Kind
		if kind == "" {
			kind = KindDialogue
		}
		params.Kind = pgtype.Text{String: kind, Valid: true}
		var text richtext.Doc
		if p.Text != nil {
			text = *p.Text
		}
		doc, err := json.Marshal(text)
		if err != nil {
			return fmt.Errorf("node: encode the text: %w", err)
		}
		params.Text = doc
	}
	var err error
	if params.SpeakerComponentID, err = speaker(ctx, q, ws, project, p.SpeakerComponentID, pgtype.UUID{}); err != nil {
		return err
	}
	if params.CoverAssetID, err = cover(ctx, q, ws, project, p.CoverAssetID, pgtype.UUID{}); err != nil {
		return err
	}
	if p.Target != nil {
		if params.TargetBoardID, params.TargetNodeID, err = target(ctx, q, ws, project, *p.Target,
			queries.Node{}); err != nil {
			return err
		}
	}
	err = q.InsertNode(ctx, params)
	if database.UniqueViolation(err, "node_pkey") {
		return refuseTakenID()
	}
	if err != nil {
		return fmt.Errorf("node: add the node: %w", err)
	}
	if p.OnEnter != "" || p.OnExit != "" {
		if _, err := setScripts(ctx, tx, expression.Owner{Kind: ids.Node, ID: id, Board: boardID}, &p.OnEnter,
			&p.OnExit); err != nil {
			return err
		}
	}
	if err := tx.Changed(ids.Node, id.Bytes, 1); err != nil {
		return err
	}
	return tx.AddUsage(usage.Nodes, 1)
}

// setScripts sets the on-enter and on-exit scripts of the node owner, each to
// its source, or removes it for the empty string, but for one that is nil,
// which stays as it is, and reports whether that changed either
// (expression.SetFields).
func setScripts(ctx context.Context, tx *commands.Tx, owner expression.Owner, onEnter, onExit *string) (bool,
	error) {
	return expression.SetFields(ctx, tx, owner, map[expression.Field]*string{expression.OnEnter: onEnter,
		expression.OnExit: onExit})
}

// applyDuplicate is Duplicate's Apply.
func applyDuplicate(ctx context.Context, tx *commands.Tx, p duplicatePayload) error {
	q := queries.New(tx)
	ws, project := nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID())
	id := decoded(ids.Node, p.ID)
	if err := checkNewNode(ctx, q, ws, project, id); err != nil {
		return err
	}
	source, err := liveNodeOf(ctx, tx, "source_id", p.SourceID)
	if err != nil {
		return err
	}
	x, y := position(p.Position)
	n, err := q.DuplicateNode(ctx, queries.DuplicateNodeParams{ID: id, X: x, Y: y, CreatedBy: by(tx),
		WorkspaceID: ws, ProjectID: project, SourceID: source.ID})
	switch {
	case database.UniqueViolation(err, "node_pkey"):
		return refuseTakenID()
	case err != nil:
		return fmt.Errorf("node: duplicate the node: %w", err)
	case n != 1:
		return fmt.Errorf("node: duplicating the node added %d nodes", n)
	}
	// Its scripts are copies of the node's, which the batch compiles at the
	// copy's board, the node's own.
	if err := expression.CopyNode(ctx, tx, source.ID, id); err != nil {
		return err
	}
	if err := tx.Changed(ids.Node, id.Bytes, 1); err != nil {
		return err
	}
	return tx.AddUsage(usage.Nodes, 1)
}

// checkNewNode refuses the batch, at id, if the project has a node whose
// UUID is id, the ID of a node that a command creates, before anything else
// is checked, as a board's create does; the insert refuses the ID of a node
// that the transaction does not see (refuseTakenID).
func checkNewNode(ctx context.Context, q *queries.Queries, ws, project, id pgtype.UUID) error {
	_, err := q.NodeAny(ctx, queries.NodeAnyParams{WorkspaceID: ws, ProjectID: project, ID: id})
	switch {
	case err == nil:
		return refuseTakenID()
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("node: read the node: %w", err)
	}
	return nil
}

// refuseTakenID returns the refusal, at id, of a command that creates a node
// with the ID of one that exists already, which may be another workspace's,
// or another project's, whose rows the transaction does not see.
func refuseTakenID() error {
	return commands.Refuse("id", "is the ID of a node that exists already", nil)
}

// planUpdate returns the node of Update's payload p, as it is, and every
// field of it that the update sets, which leaves those that p leaves out as
// they are, once it has checked that the node is not in the trash, that it is
// of a type that takes each field that p sets, and that each entity that p
// refers to is one of the project's, as speaker, cover, and target check.
// It only reads, so that Inverse, which runs before Apply, refuses the batch
// as Apply would, rather than return a step for a field that the node does
// not take.
func planUpdate(ctx context.Context, tx *commands.Tx, p updatePayload) (queries.Node, queries.UpdateNodeParams,
	error) {
	n, err := liveNodeOf(ctx, tx, "id", p.ID)
	if err != nil {
		return queries.Node{}, queries.UpdateNodeParams{}, err
	}
	next := queries.UpdateNodeParams{Kind: n.Kind, Title: n.Title, Text: n.Text,
		SpeakerComponentID: n.SpeakerComponentID, CoverAssetID: n.CoverAssetID, Color: n.Color, Tags: n.Tags,
		Width: n.Width, Height: n.Height, IsEnding: n.IsEnding, TargetBoardID: n.TargetBoardID,
		TargetNodeID: n.TargetNodeID}
	if next.Tags == nil {
		next.Tags = []string{}
	}
	typed := typedFields{kind: p.Kind != nil, text: p.Text != nil, speaker: p.SpeakerComponentID != nil,
		cover: p.CoverAssetID != nil, isEnding: p.IsEnding != nil, target: p.Target != nil,
		onEnter: p.OnEnter != nil, onExit: p.OnExit != nil}
	if errs := typed.misplaced(n.Type); len(errs) > 0 {
		return n, next, commands.Refuse(errs[0].Field, errs[0].Message, nil)
	}
	if p.Kind != nil {
		next.Kind = pgtype.Text{String: *p.Kind, Valid: true}
	}
	if p.Title != nil {
		next.Title = *p.Title
	}
	if p.Text != nil {
		if next.Text, err = json.Marshal(*p.Text); err != nil {
			return n, next, fmt.Errorf("node: encode the text: %w", err)
		}
	}
	if p.Color != nil {
		next.Color = *p.Color
	}
	if p.Tags != nil {
		next.Tags = *p.Tags
		if next.Tags == nil {
			next.Tags = []string{}
		}
	}
	if p.Size != nil {
		next.Width, next.Height = size(*p.Size)
	}
	if p.IsEnding != nil {
		next.IsEnding = *p.IsEnding
	}
	q := queries.New(tx)
	if p.SpeakerComponentID != nil {
		if next.SpeakerComponentID, err = speaker(ctx, q, n.WorkspaceID, n.ProjectID, *p.SpeakerComponentID,
			n.SpeakerComponentID); err != nil {
			return n, next, err
		}
	}
	if p.CoverAssetID != nil {
		if next.CoverAssetID, err = cover(ctx, q, n.WorkspaceID, n.ProjectID, *p.CoverAssetID,
			n.CoverAssetID); err != nil {
			return n, next, err
		}
	}
	if p.Target != nil {
		if next.TargetBoardID, next.TargetNodeID, err = target(ctx, q, n.WorkspaceID, n.ProjectID, *p.Target,
			n); err != nil {
			return n, next, err
		}
	}
	return n, next, nil
}

// updateBack returns the payload of the update that undoes Update's payload
// p of the node n, as it is before the update: each field that p sets, set
// back to n's.
func updateBack(n queries.Node, p updatePayload) (updatePayload, error) {
	back := updatePayload{ID: p.ID}
	if p.Kind != nil {
		back.Kind = &n.Kind.String
	}
	if p.Title != nil {
		back.Title = &n.Title
	}
	if p.Text != nil {
		doc, err := richtext.Parse(n.Text)
		if err != nil {
			return updatePayload{}, fmt.Errorf("node: the text of the node %s: %w", p.ID, err)
		}
		back.Text = doc
	}
	if p.SpeakerComponentID != nil {
		s := encode(ids.Component, n.SpeakerComponentID)
		back.SpeakerComponentID = &s
	}
	if p.CoverAssetID != nil {
		c := encode(ids.Asset, n.CoverAssetID)
		back.CoverAssetID = &c
	}
	if p.Color != nil {
		back.Color = &n.Color
	}
	if p.Tags != nil {
		tags := slices.Clone(n.Tags)
		if tags == nil {
			tags = []string{}
		}
		back.Tags = &tags
	}
	if p.Size != nil {
		s, err := sizeOf(n.Width, n.Height)
		if err != nil {
			return updatePayload{}, err
		}
		if s == nil {
			s = &NodeSize{}
		}
		back.Size = s
	}
	if p.IsEnding != nil {
		back.IsEnding = &n.IsEnding
	}
	if p.Target != nil {
		back.Target = &NodeTarget{BoardID: encode(ids.Board, n.TargetBoardID), NodeID: encode(ids.Node, n.TargetNodeID)}
	}
	return back, nil
}

// movedNodes returns the positions of the nodes that Move's payload p moves,
// as they are before it moves them, in the order of p's nodes, once it has
// checked that each is a node of the project that is not in its trash, and
// refuses the batch otherwise, at the node's id.
func movedNodes(ctx context.Context, tx *commands.Tx, p movePayload) ([]queries.NodePositionsRow, error) {
	list := make([]pgtype.UUID, len(p.Nodes))
	for i, place := range p.Nodes {
		list[i] = decoded(ids.Node, place.ID)
	}
	rows, err := queries.New(tx).NodePositions(ctx, queries.NodePositionsParams{
		WorkspaceID: nullUUID(tx.WorkspaceID()), ProjectID: nullUUID(tx.ProjectID()), Ids: list})
	if err != nil {
		return nil, fmt.Errorf("node: read the nodes' positions: %w", err)
	}
	byID := make(map[ids.UUID]queries.NodePositionsRow, len(rows))
	for _, row := range rows {
		byID[row.ID.Bytes] = row
	}
	out := make([]queries.NodePositionsRow, len(p.Nodes))
	for i, id := range list {
		row, ok := byID[id.Bytes]
		at := fmt.Sprintf("nodes[%d].id", i)
		switch {
		case !ok:
			return nil, commands.Refuse(at, "is not a node of the project", nil)
		case row.Deleted:
			return nil, commands.Refuse(at, "is a node in the project's trash, itself or with its board, which only "+
				"restoring it changes", nil)
		}
		out[i] = row
	}
	return out, nil
}

// speaker returns the UUID of the component whose ID is id, a component's ID
// that a payload's Check checked, as the column of an element's speaker, or a
// null column if id is "", for none, once it has checked that it is one of
// the project's components, in its trash or not, or that it is kept, the
// speaker that the element had, which its foreign key holds to exist. It
// refuses the batch otherwise, at speaker_component_id, as for a component
// that the purge has deleted, which it leaves no element with.
func speaker(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, id string, kept pgtype.UUID) (
	pgtype.UUID, error) {
	if id == "" {
		return pgtype.UUID{}, nil
	}
	u := decoded(ids.Component, id)
	if sameUUID(u, kept) {
		return kept, nil
	}
	_, err := q.ComponentAny(ctx, queries.ComponentAnyParams{WorkspaceID: ws, ProjectID: project, ID: u})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return pgtype.UUID{}, commands.Refuse("speaker_component_id", "is not a component of the project", nil)
	case err != nil:
		return pgtype.UUID{}, fmt.Errorf("node: read the speaker: %w", err)
	}
	return u, nil
}

// cover returns the UUID of the asset whose ID is id, an asset's ID that a
// payload's Check checked, as the column of an element's cover, or a null
// column if id is "", for none, once it has checked that it is one of the
// project's assets, or that it is kept, the cover that the element has. It
// refuses the batch otherwise, at cover_asset_id.
func cover(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, id string, kept pgtype.UUID) (
	pgtype.UUID, error) {
	if id == "" {
		return pgtype.UUID{}, nil
	}
	u := decoded(ids.Asset, id)
	if sameUUID(u, kept) {
		return kept, nil
	}
	_, err := q.Asset(ctx, queries.AssetParams{WorkspaceID: ws, ProjectID: project, ID: u})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return pgtype.UUID{}, commands.Refuse("cover_asset_id", "is not an asset of the project", nil)
	case err != nil:
		return pgtype.UUID{}, fmt.Errorf("node: read the cover: %w", err)
	}
	return u, nil
}

// target returns the UUIDs of the board and the node of a jumper's target t,
// which a payload's Check checked, as its columns, or null columns for none,
// once it has checked that the node is a node of the project, in its trash or
// not, on the board, or that the target is kept, the one that the jumper
// jumper has, which may no longer exist. It refuses the batch otherwise, at
// target.node_id.
func target(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, t NodeTarget, jumper queries.Node) (
	boardID, nodeID pgtype.UUID, err error) {
	if t.NodeID == "" {
		return pgtype.UUID{}, pgtype.UUID{}, nil
	}
	boardID, nodeID = decoded(ids.Board, t.BoardID), decoded(ids.Node, t.NodeID)
	if sameUUID(boardID, jumper.TargetBoardID) && sameUUID(nodeID, jumper.TargetNodeID) {
		return boardID, nodeID, nil
	}
	row, err := q.NodeAny(ctx, queries.NodeAnyParams{WorkspaceID: ws, ProjectID: project, ID: nodeID})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return pgtype.UUID{}, pgtype.UUID{}, commands.Refuse("target.node_id", "is not a node of the project", nil)
	case err != nil:
		return pgtype.UUID{}, pgtype.UUID{}, fmt.Errorf("node: read the target: %w", err)
	case row.Node.BoardID != boardID:
		return pgtype.UUID{}, pgtype.UUID{}, commands.Refuse("target.node_id", "is a node of another board than "+
			"target.board_id", nil)
	}
	return boardID, nodeID, nil
}

// anyNode returns the node of the batch's project whose ID is id, a node's ID
// that its payload's Check checked, whether or not it is in the trash, with
// whether its board is, or refuses the batch, at the payload's field at, if
// the project has no such node.
func anyNode(ctx context.Context, tx *commands.Tx, at, id string) (queries.NodeAnyRow, error) {
	row, err := queries.New(tx).NodeAny(ctx, queries.NodeAnyParams{WorkspaceID: nullUUID(tx.WorkspaceID()),
		ProjectID: nullUUID(tx.ProjectID()), ID: decoded(ids.Node, id)})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return queries.NodeAnyRow{}, commands.Refuse(at, "is not a node of the project", nil)
	case err != nil:
		return queries.NodeAnyRow{}, fmt.Errorf("node: read the node: %w", err)
	}
	return row, nil
}

// liveNodeOf returns the node of the batch's project whose ID is id as
// anyNode does, or refuses the batch if it is in the trash, where nothing but
// restoring it changes it, or its board is, with which it comes back.
func liveNodeOf(ctx context.Context, tx *commands.Tx, at, id string) (queries.Node, error) {
	row, err := anyNode(ctx, tx, at, id)
	switch {
	case err != nil:
		return queries.Node{}, err
	case row.Node.DeletedAt.Valid:
		return queries.Node{}, commands.Refuse(at, "is a node in the project's trash, which only restoring it "+
			"changes", nil)
	case row.BoardDeleted:
		return queries.Node{}, refuseBoardInTrash(at)
	}
	return row.Node, nil
}

// refuseBoardInTrash returns the refusal, at the payload's field at, of a
// command that would change a node of a board in the trash, which is in the
// trash with it, and which nothing but the board's restoration changes.
func refuseBoardInTrash(at string) error {
	return commands.Refuse(at, "is a node of a board in the project's trash, which comes back with its board", nil)
}

// liveBoardOf returns the board of the batch's project whose ID is id, a
// board's ID that its payload's Check checked, or refuses the batch if the
// project has no such board, or it is in the trash, where nothing but
// restoring it changes it.
func liveBoardOf(ctx context.Context, tx *commands.Tx, id string) (queries.Board, error) {
	row, err := queries.New(tx).BoardAny(ctx, queries.BoardAnyParams{WorkspaceID: nullUUID(tx.WorkspaceID()),
		ProjectID: nullUUID(tx.ProjectID()), ID: decoded(ids.Board, id)})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return queries.Board{}, commands.Refuse("id", "is not a board of the project", nil)
	case err != nil:
		return queries.Board{}, fmt.Errorf("node: read the board: %w", err)
	case row.Board.DeletedAt.Valid:
		return queries.Board{}, commands.Refuse("id", "is a board in the project's trash, which only restoring it "+
			"changes", nil)
	}
	return row.Board, nil
}

// sameUUID reports whether the columns a and b are the same: both null, or
// both the same UUID.
func sameUUID(a, b pgtype.UUID) bool {
	return a.Valid == b.Valid && (!a.Valid || a.Bytes == b.Bytes)
}

// changed returns a function that reports the change of the node id, to the
// revision that an update returned, with err, or reports nothing if the
// update found nothing to change, which it returns as pgx.ErrNoRows.
func changed(tx *commands.Tx, id pgtype.UUID) func(rev int64, err error) error {
	return changedKind(tx, ids.Node, id)
}

// changedBoard returns a function that reports the change of the board id as
// changed does of a node.
func changedBoard(tx *commands.Tx, id pgtype.UUID) func(rev int64, err error) error {
	return changedKind(tx, ids.Board, id)
}

// changedKind returns a function that reports the change of the entity of
// the kind with the UUID id as changed does of a node.
func changedKind(tx *commands.Tx, kind ids.Kind, id pgtype.UUID) func(rev int64, err error) error {
	return func(rev int64, err error) error {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		case err != nil:
			return fmt.Errorf("node: change the %s: %w", kind, err)
		}
		return tx.Changed(kind, id.Bytes, rev)
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
