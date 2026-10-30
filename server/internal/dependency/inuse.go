package dependency

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
)

// MaxInUse is the most usages that the refusal of the deletion of a used
// variable or component lists (InUseError).
const MaxInUse = 100

// Found is a site that uses a variable or a component as a batch finds the
// project before one of its commands deletes it (FR-VAR-04, FR-COMP-04),
// with how it uses it: as the variable's usage lists, read or write, or as
// the component's, speaker, mention, read, or write (Usage).
type Found struct {
	Use string `json:"use" doc:"How the site uses what the deletion deletes: read or write, for a variable, or speaker, mention, read, or write, for a component, as its usage lists say"`
	Usage
}

// InUseError is the reason of the refusal of a deletion of a variable or a
// component that sites of the project use, which the deletion does not force
// (FR-VAR-04 AC1, FR-COMP-04 AC1, FR-EDIT-06 AC2), which the API answers with
// variable_in_use or component_in_use: Kind is ids.Variable or ids.Component,
// Count how many usages there are, as the usage lists count them, Counts how
// many of each use, and Usages the first MaxInUse of them, in the order of
// their uses, as Counts lists them, and then as the usage lists order them.
type InUseError struct {
	Kind   ids.Kind
	Count  int
	Counts map[string]int
	Usages []Found
}

func (e *InUseError) Error() string {
	return fmt.Sprintf("dependency: %d sites of the project use the %s", e.Count, e.Kind)
}

// uses are the uses of what a site uses, in the order that a refusal lists
// them.
var uses = []string{string(UseSpeaker), string(UseMention), string(UseRead), string(UseWrite)}

// ExpressionUsage returns the usage of the site of an expression, of the
// site's type, as a usage list shows it (Usage.Site), of the board, the
// node, and the connection or the branch output, if it is one's, of the UUID
// expression, which reads or writes the attribute definition attribute, zero
// for a variable, as package expression finds it.
func ExpressionUsage(site string, board, node, connection, output, expression, attribute ids.UUID) Usage {
	return Usage{Site: site, BoardID: encodeID(ids.Board, board), NodeID: encodeID(ids.Node, node),
		ConnectionID: encodeID(ids.Connection, connection), BranchOutputID: encodeID(ids.BranchOutput, output),
		AttributeID: encodeID(ids.AttributeDef, attribute),
		key:         Key{Board: board, Node: node, Site: expression, Attribute: attribute}}
}

// encodeID returns the public ID of the kind of id, or "" for zero.
func encodeID(k ids.Kind, id ids.UUID) string {
	if id == (ids.UUID{}) {
		return ""
	}
	return ids.Encode(k, id)
}

// TextsUsing returns the texts of the batch's project, of its nodes, its
// connections, and its components outside its trash, that use the variable,
// or the component, of the kind, ids.Variable or ids.Component, whose UUID is
// target, as the batch's commands before the one that calls it leave them:
// those whose rows of the index name it, and those that the batch has
// changed so far (commands.Tx.Changes), but for the changes that commands of
// textNeutral types alone made, which the index holds as they were before
// the batch until its derivation indexes them again, each read anew. A text
// reads the variable or the component's attributes that its value references
// show, each once, and a component that it mentions is a use of its own.
func TextsUsing(ctx context.Context, tx *commands.Tx, kind ids.Kind, target ids.UUID) ([]Found, error) {
	q := queries.New(tx)
	ws, project := nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID())
	kinds := []string{string(Variable)}
	if kind == ids.Component {
		kinds = []string{string(Component), string(Attribute)}
	}
	referrers, err := q.TextReferrers(ctx, queries.TextReferrersParams{WorkspaceID: ws, ProjectID: project,
		TargetID: nullUUID(target), TargetKinds: kinds})
	if err != nil {
		return nil, fmt.Errorf("dependency: read the texts that name the %s: %w", kind, err)
	}
	owners := map[ids.Kind][]pgtype.UUID{}
	add := func(k ids.Kind, id pgtype.UUID) {
		if id.Valid && !slices.Contains(owners[k], id) {
			owners[k] = append(owners[k], id)
		}
	}
	for _, r := range referrers {
		add(ids.Node, r.NodeID)
		add(ids.Connection, r.ConnectionID)
		add(ids.Component, r.ComponentID)
	}
	for _, c := range tx.Changes() {
		if slices.ContainsFunc(c.By, func(t commands.Type) bool { return !slices.Contains(textNeutral, t) }) {
			add(c.Kind, nullUUID(c.ID))
		}
	}
	nodes, connections, components := list(owners[ids.Node]), list(owners[ids.Connection]),
		list(owners[ids.Component])
	if len(nodes)+len(connections)+len(components) == 0 {
		return nil, nil
	}
	live, err := q.LiveTextOwners(ctx, queries.LiveTextOwnersParams{WorkspaceID: ws, ProjectID: project,
		NodeIds: nodes, ConnectionIds: connections, ComponentIds: components})
	if err != nil {
		return nil, fmt.Errorf("dependency: read the owners of the texts: %w", err)
	}
	if len(live) == 0 {
		return nil, nil
	}
	sites := map[ids.UUID]queries.LiveTextOwnersRow{}
	for _, l := range live {
		sites[l.ID.Bytes] = l
	}
	refs, err := q.TextReferences(ctx, queries.TextReferencesParams{WorkspaceID: ws, ProjectID: project,
		NodeIds: nodes, ConnectionIds: connections, ComponentIds: components})
	if err != nil {
		return nil, fmt.Errorf("dependency: read what the texts name: %w", err)
	}
	var out []Found
	seen := map[Key]map[string]bool{}
	for _, r := range refs {
		u, err := inlineUse(r.Inline)
		if err != nil {
			return nil, err
		}
		if u.Target != target || kind == ids.Variable && u.Kind != Variable ||
			kind == ids.Component && u.Kind == Variable {
			continue
		}
		site, owner := SiteNodeText, r.NodeID
		switch {
		case r.ConnectionID.Valid:
			site, owner = SiteConnectionLabel, r.ConnectionID
		case r.ComponentID.Valid:
			site, owner = SiteComponentDescription, r.ComponentID
		}
		l, ok := sites[owner.Bytes]
		if !ok {
			continue
		}
		use := string(Read)
		if u.Kind == Component {
			use = string(UseMention)
		}
		f := Found{Use: use, Usage: Usage{Site: site, BoardID: encode(ids.Board, l.BoardID),
			NodeID: encode(ids.Node, l.SiteNodeID), AttributeID: encodeID(ids.AttributeDef, u.Attribute),
			key: Key{Board: l.BoardID.Bytes, Node: l.SiteNodeID.Bytes, Site: owner.Bytes, Attribute: u.Attribute}}}
		switch site {
		case SiteConnectionLabel:
			f.ConnectionID = ids.Encode(ids.Connection, owner.Bytes)
		case SiteComponentDescription:
			f.ComponentID = ids.Encode(ids.Component, owner.Bytes)
		}
		if seen[f.key] == nil {
			seen[f.key] = map[string]bool{}
		}
		if !seen[f.key][use] {
			seen[f.key][use] = true
			out = append(out, f)
		}
	}
	return out, nil
}

// Speakers returns the nodes of the project, of the workspace ws, outside
// its trash, whose speaker is the component, read in q's transaction, each a
// use of it as its speaker.
func Speakers(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, component ids.UUID) ([]Found,
	error) {
	rows, err := q.SpeakerNodes(ctx, queries.SpeakerNodesParams{WorkspaceID: ws, ProjectID: project,
		SpeakerComponentID: nullUUID(component)})
	if err != nil {
		return nil, fmt.Errorf("dependency: read the nodes whose speaker the component is: %w", err)
	}
	out := make([]Found, len(rows))
	for i, r := range rows {
		out[i] = Found{Use: string(UseSpeaker), Usage: Usage{Site: SiteNodeSpeaker,
			BoardID: encode(ids.Board, r.BoardID), NodeID: encode(ids.Node, r.ID),
			key: Key{Board: r.BoardID.Bytes, Node: r.ID.Bytes, Site: r.ID.Bytes}}}
	}
	return out, nil
}

// Refusal returns the reason of the refusal of the deletion of the variable
// or the component, of the kind, that the usages found use (InUseError), with
// the names of the boards and the titles of the nodes of those that it
// lists, read in q's transaction of the project of the workspace ws.
func Refusal(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, kind ids.Kind, found []Found) (
	*InUseError, error) {
	sorted := slices.Clone(found)
	slices.SortFunc(sorted, func(a, b Found) int {
		return cmp.Or(cmp.Compare(slices.Index(uses, a.Use), slices.Index(uses, b.Use)),
			bytes.Compare(a.key.Board[:], b.key.Board[:]), bytes.Compare(a.key.Node[:], b.key.Node[:]),
			bytes.Compare(a.key.Site[:], b.key.Site[:]), bytes.Compare(a.key.Attribute[:], b.key.Attribute[:]))
	})
	e := &InUseError{Kind: kind, Count: len(sorted), Counts: map[string]int{}}
	for _, u := range uses {
		if kind == ids.Component || u == string(UseRead) || u == string(UseWrite) {
			e.Counts[u] = 0
		}
	}
	for _, f := range sorted {
		e.Counts[f.Use]++
	}
	e.Usages = sorted[:min(len(sorted), MaxInUse)]
	var boards, nodes []pgtype.UUID
	for _, f := range e.Usages {
		if f.key.Board != (ids.UUID{}) {
			boards = append(boards, nullUUID(f.key.Board))
		}
		if f.key.Node != (ids.UUID{}) {
			nodes = append(nodes, nullUUID(f.key.Node))
		}
	}
	rows, err := q.SiteNames(ctx, queries.SiteNamesParams{WorkspaceID: ws, ProjectID: project, BoardIds: list(boards),
		NodeIds: list(nodes)})
	if err != nil {
		return nil, fmt.Errorf("dependency: read the names of the usages' boards and nodes: %w", err)
	}
	labels := map[ids.UUID]string{}
	for _, r := range rows {
		labels[r.ID.Bytes] = r.Label
	}
	for i := range e.Usages {
		f := &e.Usages[i]
		if f.key.Board != (ids.UUID{}) {
			name := labels[f.key.Board]
			f.BoardName = &name
		}
		if f.key.Node != (ids.UUID{}) {
			title := labels[f.key.Node]
			f.NodeTitle = &title
		}
	}
	return e, nil
}

// CheckForce refuses the batch, at the payload's field at, the field that
// forces the deletion of a variable or a component that sites of the
// project use, unless the batch's actor may publish the project's snapshots
// (authz.CheckPublish, FR-VAR-04), as the project's publish_min_role says,
// with authz.ErrInsufficientRole, which the API answers with
// insufficient_role. A deletion that forces nothing, of what nothing uses,
// needs only the role of its command's type.
func CheckForce(ctx context.Context, tx *commands.Tx, at string) error {
	least, err := project.PublishMinRole(ctx, queries.New(tx), tx.WorkspaceID(), tx.ProjectID())
	if err != nil {
		return err
	}
	if err := authz.CheckPublish(tx.Role(authz.Project), least); err != nil {
		return commands.Refuse(at, "is set, but only a role that may publish the project's snapshots deletes what "+
			"the project's content uses", err)
	}
	return nil
}
