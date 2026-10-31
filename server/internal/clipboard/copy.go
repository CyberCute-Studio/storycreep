package clipboard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/connection"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/expression"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/node"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// Options configure a Service.
type Options struct {
	// Projects authorises the reads (project.Service.ReadSnapshot).
	Projects *project.Service
}

// Service copies a project's nodes as a clipboard, and reports what of one
// resolves to nothing at a board of a project.
type Service struct {
	db       project.SnapshotDB
	projects *project.Service
}

// New returns a Service that reads db, with opts. It panics if opts.Projects
// is nil.
func New(db project.SnapshotDB, opts Options) *Service {
	if opts.Projects == nil {
		panic("clipboard: a Service needs the projects' service that authorises its reads")
	}
	return &Service{db: db, projects: opts.Projects}
}

// Copy returns the clipboard of the nodes of the project projectID whose IDs
// are nodeIDs, as the API writes them, at most MaxNodes, each once, of one
// board, which are not in the trash, themselves or with their board (PRD
// FR-EDIT-07): the nodes, in the order of their IDs, with their outputs that
// are not in the trash, the connections between them that are not either,
// in the order of their ports and of each port's choices, and a declaration
// of each variable, component, and attribute definition that is not in the
// trash that their texts, expressions, and speakers name, read as of one
// moment. It returns the errors of project.Service.Get first, then the
// problems of nodeIDs, as an input.FieldErrors at node_ids[i].
func (s *Service) Copy(ctx context.Context, actor commands.Actor, projectID ids.UUID, nodeIDs []string) (Clipboard,
	error) {
	var out Clipboard
	err := s.projects.ReadSnapshot(ctx, s.db, actor, projectID, func(tx pgx.Tx, workspaceID ids.UUID) error {
		var err error
		out, err = copyNodes(ctx, queries.New(tx), uuidOf(workspaceID), uuidOf(projectID), nodeIDs)
		return err
	})
	return out, err
}

// copyNodes is Copy's read, by q, in a transaction of the workspace ws.
func copyNodes(ctx context.Context, q *queries.Queries, ws, projectID pgtype.UUID, nodeIDs []string) (Clipboard,
	error) {
	if len(nodeIDs) > MaxNodes {
		return Clipboard{}, input.FieldErrors{{Field: "node_ids", Message: fmt.Sprintf("holds more than %d nodes",
			MaxNodes)}}
	}
	var problems input.FieldErrors
	list := make([]pgtype.UUID, 0, len(nodeIDs))
	at := map[ids.UUID]int{}
	for i, id := range nodeIDs {
		u, err := ids.Decode(ids.Node, id)
		switch {
		case err != nil:
			problems = append(problems, input.FieldError{Field: fmt.Sprintf("node_ids[%d]", i),
				Message: fmt.Sprintf("is not a node's ID: %v", err)})
			continue
		}
		if j, dup := at[u]; dup {
			problems = append(problems, input.FieldError{Field: fmt.Sprintf("node_ids[%d]", i),
				Message: fmt.Sprintf("is the ID of node_ids[%d] too", j)})
			continue
		}
		at[u] = i
		list = append(list, uuidOf(u))
	}
	if len(problems) > 0 {
		return Clipboard{}, problems
	}
	nodes, err := node.Live(ctx, q, ws, projectID, list)
	if err != nil {
		return Clipboard{}, err
	}
	found := map[ids.UUID]node.Node{}
	for _, n := range nodes {
		u, _ := ids.Decode(ids.Node, n.ID)
		found[u] = n
	}
	board := found[list[0].Bytes].BoardID
	for i, id := range list {
		n, ok := found[id.Bytes]
		switch {
		case !ok:
			problems = append(problems, input.FieldError{Field: fmt.Sprintf("node_ids[%d]", i),
				Message: "is not a node of the project, or is one in its trash"})
		case board != "" && n.BoardID != board:
			problems = append(problems, input.FieldError{Field: fmt.Sprintf("node_ids[%d]", i),
				Message: "is a node of another board than node_ids[0], which a clipboard holds the nodes of one " +
					"board of"})
		}
	}
	if len(problems) > 0 {
		return Clipboard{}, problems
	}
	connections, err := connection.Among(ctx, q, ws, projectID, list)
	if err != nil {
		return Clipboard{}, err
	}
	c := Clipboard{Format: Format, Version: Version, ProjectID: ids.Encode(ids.Project, projectID.Bytes),
		BoardID: nodes[0].BoardID, Nodes: make([]ClipboardNode, len(nodes)),
		Connections: make([]ClipboardConnection, len(connections)), Variables: []ClipboardVariable{},
		Components: []ClipboardComponent{}}
	for i, n := range nodes {
		if c.Nodes[i], err = clipboardNode(n); err != nil {
			return Clipboard{}, err
		}
	}
	for i, cn := range connections {
		if c.Connections[i], err = clipboardConnection(cn); err != nil {
			return Clipboard{}, err
		}
	}
	if err := declare(ctx, q, ws, projectID, &c); err != nil {
		return Clipboard{}, err
	}
	return c, nil
}

// clipboardNode returns the node n as a clipboard holds it.
func clipboardNode(n node.Node) (ClipboardNode, error) {
	out := ClipboardNode{ID: n.ID, Type: n.Type, Kind: n.Kind, Title: n.Title, SpeakerComponentID: n.SpeakerComponentID,
		CoverAssetID: n.CoverAssetID, Color: n.Color, Tags: n.Tags, Position: n.Position, Size: n.Size,
		IsEnding: n.IsEnding, Target: n.Target, OnEnter: sourceOf(n.OnEnter), OnExit: sourceOf(n.OnExit)}
	if len(out.Tags) == 0 {
		out.Tags = nil
	}
	if n.Text != nil {
		var err error
		if out.Text, err = textOf(*n.Text); err != nil {
			return ClipboardNode{}, err
		}
	}
	for _, o := range n.Outputs {
		out.Outputs = append(out.Outputs, ClipboardOutput{ID: o.ID, Else: o.Kind == "else", Condition: sourceOf(o.Condition)})
	}
	return out, nil
}

// clipboardConnection returns the connection c as a clipboard holds it.
func clipboardConnection(c connection.Connection) (ClipboardConnection, error) {
	label, err := textOf(c.Label)
	if err != nil {
		return ClipboardConnection{}, err
	}
	out := ClipboardConnection{ID: c.ID, FromNodeID: c.FromNodeID, FromPort: c.FromPort, ToNodeID: c.ToNodeID,
		Label: label, Condition: sourceOf(c.Condition), OnTraverse: sourceOf(c.OnTraverse)}
	if out.FromPort == portOut {
		out.FromPort = ""
	}
	return out, nil
}

// sourceOf returns the source of the expression e, or "" for none.
func sourceOf(e *expression.Expression) string {
	if e == nil {
		return ""
	}
	return e.Source
}

// declare gives the clipboard c the declarations of the variables, the
// components, and the attribute definitions of the project, not in its trash,
// that its texts, its expressions' names at its board, and its speakers name.
func declare(ctx context.Context, q *queries.Queries, ws, projectID pgtype.UUID, c *Clipboard) error {
	names, err := expression.Live(ctx, q, ws, projectID, nil)
	if err != nil {
		return err
	}
	variables, components := map[string]bool{}, map[string]bool{}
	attributes := map[string]bool{} // an attribute definition's ID, after its component's and a slash
	use := func(variable, component, attribute string) {
		switch {
		case variable != "":
			variables[variable] = true
		case component != "":
			components[component] = true
			if attribute != "" {
				attributes[component+"/"+attribute] = true
			}
		}
	}
	if err := c.eachSource(func(_ string, source string, ctx expr.Context) error {
		named, err := expr.Names(source, ctx, names, c.BoardID)
		for _, n := range named {
			use(n.Variable, n.Component, n.Attribute)
		}
		return err
	}); err != nil {
		return err
	}
	texts := make([]Text, 0, len(c.Nodes)+len(c.Connections))
	for _, n := range c.Nodes {
		use("", n.SpeakerComponentID, "")
		texts = append(texts, n.Text)
	}
	for _, cn := range c.Connections {
		texts = append(texts, cn.Label)
	}
	for _, t := range texts {
		if len(t) == 0 {
			continue
		}
		doc, err := richtext.Parse(t)
		if err != nil {
			return fmt.Errorf("clipboard: a text of the project: %w", err)
		}
		inlines(doc, func(in richtext.Inline) {
			switch in := in.(type) {
			case *richtext.Mention:
				use("", in.Component, "")
			case *richtext.Value:
				if in.Attribute == "" {
					use(in.Ref, "", "")
				} else {
					use("", in.Ref, in.Attribute)
				}
			}
		})
	}
	if c.Variables, err = declareVariables(ctx, q, ws, projectID, variables); err != nil {
		return err
	}
	c.Components, err = declareComponents(ctx, q, ws, projectID, components, attributes)
	return err
}

// uuidsOf returns the UUIDs of the IDs of the kind, keys of set, in their
// order, leaving out those that are no such IDs.
func uuidsOf(kind ids.Kind, set map[string]bool) []pgtype.UUID {
	var out []pgtype.UUID
	for id := range set {
		if u, err := ids.Decode(kind, id); err == nil {
			out = append(out, uuidOf(u))
		}
	}
	slices.SortFunc(out, func(a, b pgtype.UUID) int { return bytes.Compare(a.Bytes[:], b.Bytes[:]) })
	return out
}

// declareVariables returns the declarations of the project's variables, not
// in its trash, whose IDs are keys of used, in the order of their IDs.
func declareVariables(ctx context.Context, q *queries.Queries, ws, projectID pgtype.UUID, used map[string]bool) (
	[]ClipboardVariable, error) {
	out := []ClipboardVariable{}
	list := uuidsOf(ids.Variable, used)
	if len(list) == 0 {
		return out, nil
	}
	rows, err := q.ClipboardVariables(ctx, queries.ClipboardVariablesParams{WorkspaceID: ws, ProjectID: projectID,
		Ids: list})
	if err != nil {
		return nil, fmt.Errorf("clipboard: read the variables: %w", err)
	}
	for _, r := range rows {
		v := ClipboardVariable{ID: ids.Encode(ids.Variable, r.ID.Bytes), Key: r.Key, Scope: r.Scope, Type: r.Type,
			Description: r.Description, ResetOnEnter: r.ResetOnEnter}
		if err := json.Unmarshal(r.DefaultValue, &v.Default); err != nil {
			return nil, fmt.Errorf("clipboard: the default of a variable: %w", err)
		}
		// The default as variable.create writes it, as jsonb need not have
		// kept it.
		if def, problem := v.Default.Read(r.Type, value.Checks{}); problem == "" {
			v.Default = def
		}
		if r.BoardID.Valid {
			v.BoardID = ids.Encode(ids.Board, r.BoardID.Bytes)
		}
		out = append(out, v)
	}
	return out, nil
}

// declareComponents returns the declarations of the project's components,
// not in its trash, whose IDs are keys of used, in the order of their IDs,
// each with the attribute definitions of its type, not in the trash, whose
// IDs are keys of attributes after its ID and a slash, in their order.
func declareComponents(ctx context.Context, q *queries.Queries, ws, projectID pgtype.UUID, used,
	attributes map[string]bool) ([]ClipboardComponent, error) {
	out := []ClipboardComponent{}
	list := uuidsOf(ids.Component, used)
	if len(list) == 0 {
		return out, nil
	}
	rows, err := q.ClipboardComponents(ctx, queries.ClipboardComponentsParams{WorkspaceID: ws,
		ProjectID: projectID, Ids: list})
	if err != nil {
		return nil, fmt.Errorf("clipboard: read the components: %w", err)
	}
	defs, err := q.ExpressionAttributes(ctx, queries.ExpressionAttributesParams{WorkspaceID: ws,
		ProjectID: projectID})
	if err != nil {
		return nil, fmt.Errorf("clipboard: read the attribute definitions: %w", err)
	}
	for _, r := range rows {
		k := ClipboardComponent{ID: ids.Encode(ids.Component, r.ID.Bytes), Key: r.Key, Name: r.Name,
			Type: ClipboardComponentType{ID: ids.Encode(ids.ComponentType, r.TypeID.Bytes), Key: r.TypeKey,
				Name: r.TypeName},
			Attributes: []ClipboardAttribute{}}
		for _, a := range defs {
			id := ids.Encode(ids.AttributeDef, a.ID.Bytes)
			if a.ComponentTypeID == r.TypeID && attributes[k.ID+"/"+id] {
				k.Attributes = append(k.Attributes, ClipboardAttribute{ID: id, Key: a.Key, Type: a.Type, Runtime: a.IsRuntime,
					Options: a.EnumOptions})
			}
		}
		out = append(out, k)
	}
	return out, nil
}

// Resolve returns what of the clipboard c resolves to nothing at the board
// boardID of the project projectID (ClipboardReport), as a paste of it there
// would resolve it, read as of one moment. It returns the errors of
// project.Service.Get first, then the problems of c (Check), each at clipboard
// and its field, and of boardID, at board_id, as an input.FieldErrors: one that
// is no board of the project, or one in its trash.
func (s *Service) Resolve(ctx context.Context, actor commands.Actor, projectID ids.UUID, boardID string,
	c Clipboard) (ClipboardReport, error) {
	var out ClipboardReport
	err := s.projects.ReadSnapshot(ctx, s.db, actor, projectID, func(tx pgx.Tx, workspaceID ids.UUID) error {
		problems := checkID(ids.Board, "board_id", boardID)
		for _, e := range Check(c) {
			e.Field = trimField("clipboard." + e.Field)
			problems = append(problems, e)
		}
		if len(problems) > 0 {
			return problems
		}
		q := queries.New(tx)
		ws, project := uuidOf(workspaceID), uuidOf(projectID)
		b, _ := ids.Decode(ids.Board, boardID)
		if _, err := q.Board(ctx, queries.BoardParams{WorkspaceID: ws, ProjectID: project,
			ID: uuidOf(b)}); isNoRows(err) {
			return input.FieldErrors{{Field: "board_id", Message: "is not a board of the project, or is one in " +
				"its trash"}}
		} else if err != nil {
			return fmt.Errorf("clipboard: read the board: %w", err)
		}
		d, err := loadDestination(ctx, q, ws, project, uuidOf(b))
		if err != nil {
			return err
		}
		out, err = d.report(ctx, q, c, d.resolve(c))
		return err
	})
	return out, err
}

// trimField returns the field at without a dot at its end, which a problem of
// the whole of what it is under leaves.
func trimField(at string) string {
	if len(at) > 0 && at[len(at)-1] == '.' {
		return at[:len(at)-1]
	}
	return at
}
