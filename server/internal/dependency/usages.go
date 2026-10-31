package dependency

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
)

// ErrNotFound is returned for the usages of a variable or a component, or the
// contributions to an edge between two boards, that the project does not
// have, or that is in its trash.
var ErrNotFound = errors.New("dependency: no such variable, component, or board")

// The sites of a usage (Usage.Site): a node's on-enter and on-exit scripts,
// its text, and its speaker; a connection's condition, its on-traverse
// script, and its label; a branch output's condition; and a component's
// description.
const (
	SiteNodeOnEnter           = "node_on_enter"
	SiteNodeOnExit            = "node_on_exit"
	SiteNodeText              = "node_text"
	SiteNodeSpeaker           = "node_speaker"
	SiteConnectionCondition   = "connection_condition"
	SiteConnectionOnTraverse  = "connection_on_traverse"
	SiteConnectionLabel       = "connection_label"
	SiteBranchOutputCondition = "branch_output_condition"
	SiteComponentDescription  = "component_description"
)

// ComponentUse is a use of a component that its usages list (FR-COMP-04):
// the nodes whose speaker it is, the texts that mention it, and the sites
// that read or write its attributes.
type ComponentUse string

// The uses of a component.
const (
	UseSpeaker ComponentUse = "speaker"
	UseMention ComponentUse = "mention"
	UseRead    ComponentUse = "read"
	UseWrite   ComponentUse = "write"
)

// Usage is a site that uses a variable or a component, as the API shows it,
// with its board and its node, by which a client opens it (FR-DEP-01 AC2,
// FR-COMP-04).
type Usage struct {
	Site string `json:"site" enum:"node_on_enter,node_on_exit,node_text,node_speaker,connection_condition,connection_on_traverse,connection_label,branch_output_condition,component_description" doc:"What the site is: a node's on-enter or on-exit script, its text, or its speaker; a connection's condition, its on-traverse script, or its label; a branch output's condition; or a component's description"`
	// BoardID and BoardName are those of every site but a component's
	// description, which belongs to no board.
	BoardID   string  `json:"board_id,omitempty" doc:"The ID of the site's board. A component's description has none."`
	BoardName *string `json:"board_name,omitempty" doc:"The name of the site's board, which a site has exactly when it has board_id"`
	// NodeID and NodeTitle are the node of the site: a node's own, the node
	// that a connection leaves, or an output's branch.
	NodeID         string  `json:"node_id,omitempty" doc:"The ID of the site's node: the node of its script, text, or speaker, the node that its connection leaves, or its output's branch. A component's description has none."`
	NodeTitle      *string `json:"node_title,omitempty" doc:"The title of the site's node, which may be empty, and which a site has exactly when it has node_id"`
	ConnectionID   string  `json:"connection_id,omitempty" doc:"The ID of the connection of a connection's condition, on-traverse script, or label"`
	BranchOutputID string  `json:"branch_output_id,omitempty" doc:"The ID of the output of a branch output's condition"`
	ComponentID    string  `json:"component_id,omitempty" doc:"The ID of the component of a component's description"`
	AttributeID    string  `json:"attribute_id,omitempty" doc:"The ID of the attribute definition of the component's attribute that the site reads or writes, in a list of a component's reads or writes, of which a site may read or write several, each listed once"`
	key            Key
}

// Key is where a usage is in its list (Usage.Key), by which a page of the
// list says where the next one starts: its board, its node, the site itself,
// an expression's UUID, or that of a text's or a speaker's node, connection,
// or component, and the attribute definition that it reads or writes, each
// zero where it has none.
type Key struct {
	Board, Node, Site, Attribute ids.UUID
}

// Key returns where u is in its list.
func (u Usage) Key() Key { return u.key }

// DB runs a transaction of the application login, as *database.DB does, and
// one that reads as of one moment (InSnapshot), which the reads run in.
type DB interface {
	InTx(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error
	InSnapshot(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error
}

// Options configure a Service.
type Options struct {
	// Projects reads the projects, which authorises each read of their
	// usages. It is required.
	Projects *project.Service
}

// Service answers the usage queries of the projects' variables and
// components.
type Service struct {
	db       DB
	projects *project.Service
}

// New returns a Service that reads the index in db, with opts. It panics if
// opts.Projects is nil.
func New(db DB, opts Options) *Service {
	if opts.Projects == nil {
		panic("dependency: a Service needs the projects' service that authorises its reads")
	}
	return &Service{db: db, projects: opts.Projects}
}

// VariableUsages returns at most limit of the sites of the project projectID
// that are not in its trash that read the variable variableID, or that write
// it, as access says (FR-DEP-01 AC2), in the order of their keys (Key): the
// first, if after is zero, or else those after it. It returns ErrNotFound if
// the project has no such variable, or it is in the trash, and the errors of
// project.Service.Get first, as project.ErrNotFound.
func (s *Service) VariableUsages(ctx context.Context, actor commands.Actor, projectID, variableID ids.UUID,
	access Access, after Key, limit int) ([]Usage, error) {
	if access != Read && access != Write {
		return nil, fmt.Errorf("dependency: no access %q", access)
	}
	return s.usages(ctx, actor, projectID, after, limit, queries.UsagesParams{TargetID: nullUUID(variableID),
		TargetKind: string(Variable), Access: string(access)},
		func(q *queries.Queries, ws pgtype.UUID) error {
			_, err := q.Variable(ctx, queries.VariableParams{WorkspaceID: ws, ProjectID: nullUUID(projectID),
				ID: nullUUID(variableID)})
			return err
		})
}

// ComponentUsages returns at most limit of the sites of the project
// projectID that are not in its trash that use the component componentID as
// use says (FR-COMP-04): the nodes whose speaker it is, the texts that
// mention it, or the sites that read its attributes, or write them, each of
// them once for each attribute that it reads or writes; in the order of
// their keys (Key), the first, if after is zero, or else those after it. It
// returns ErrNotFound if the project has no such component, or it is in the
// trash, and the errors of project.Service.Get first.
func (s *Service) ComponentUsages(ctx context.Context, actor commands.Actor, projectID, componentID ids.UUID,
	use ComponentUse, after Key, limit int) ([]Usage, error) {
	params := queries.UsagesParams{TargetID: nullUUID(componentID), TargetKind: string(Attribute)}
	switch use {
	case UseSpeaker:
		params.Speaker = true
	case UseMention:
		params.TargetKind, params.Access = string(Component), string(Read)
	case UseRead:
		params.Access = string(Read)
	case UseWrite:
		params.Access = string(Write)
	default:
		return nil, fmt.Errorf("dependency: no use %q", use)
	}
	return s.usages(ctx, actor, projectID, after, limit, params, func(q *queries.Queries, ws pgtype.UUID) error {
		_, err := q.Component(ctx, queries.ComponentParams{WorkspaceID: ws, ProjectID: nullUUID(projectID),
			ID: nullUUID(componentID)})
		return err
	})
}

// usages returns the usages that the query of params finds in the project
// projectID, after after, once the actor may read the project's content
// (project.Service.ReadContent), and exists finds the target, which returns
// pgx.ErrNoRows for one that the project does not have outside its trash.
// The transaction reads as of one moment, that of its start
// (project.Service.ReadSnapshot), and locks nothing, so that a batch that
// commits once exists has found the target, as one that deletes it, or the
// sites that use it, to the trash, is read whole or not at all, never the
// target as it was with usages as the batch left them.
func (s *Service) usages(ctx context.Context, actor commands.Actor, projectID ids.UUID, after Key, limit int,
	params queries.UsagesParams, exists func(q *queries.Queries, ws pgtype.UUID) error) ([]Usage, error) {
	var out []Usage
	err := s.projects.ReadSnapshot(ctx, s.db, actor, projectID, func(tx pgx.Tx, workspaceID ids.UUID) error {
		q, ws := queries.New(tx), nullUUID(workspaceID)
		switch err := exists(q, ws); {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrNotFound
		case err != nil:
			return fmt.Errorf("dependency: read what the usages are of: %w", err)
		}
		params.WorkspaceID, params.ProjectID = ws, nullUUID(projectID)
		params.AfterBoard, params.AfterNode = zeroUUID(after.Board), zeroUUID(after.Node)
		params.AfterSite, params.AfterAttribute = zeroUUID(after.Site), zeroUUID(after.Attribute)
		params.MaxRows = int32(min(limit, 1<<20))
		rows, err := q.Usages(ctx, params)
		if err != nil {
			return fmt.Errorf("dependency: read the usages: %w", err)
		}
		out = make([]Usage, len(rows))
		for i, r := range rows {
			out[i] = usageOf(r)
		}
		return nil
	})
	return out, err
}

// usageOf returns the usage r as the API shows it.
func usageOf(r queries.UsagesRow) Usage {
	u := Usage{Site: r.Site, BoardID: encode(ids.Board, r.BoardID), NodeID: encode(ids.Node, r.NodeID),
		ConnectionID: encode(ids.Connection, r.ConnectionID), BranchOutputID: encode(ids.BranchOutput, r.BranchOutputID),
		ComponentID: encode(ids.Component, r.ComponentID), AttributeID: encode(ids.AttributeDef, r.AttributeDefID),
		key: Key{Board: r.BoardID.Bytes, Node: r.NodeID.Bytes, Site: r.SiteID.Bytes, Attribute: r.AttributeDefID.Bytes}}
	if r.BoardID.Valid {
		u.BoardName = &r.BoardName.String
	}
	if r.NodeID.Valid {
		u.NodeTitle = &r.NodeTitle.String
	}
	return u
}

// encode returns the public ID of the kind of id, or "" for null.
func encode(k ids.Kind, id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return ids.Encode(k, id.Bytes)
}

// zeroUUID returns id as a uuid column, the zero UUID for zero, as a key of
// the usages' order holds what a site lacks.
func zeroUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}
