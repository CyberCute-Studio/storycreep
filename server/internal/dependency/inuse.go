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

// textsKey is the key under which TextsUsing keeps what the texts that the
// batch has changed name for the rest of the batch (commands.Tx.Keep), a map
// of readText by the texts' owners.
type textsKey struct{}

// readText is what a text that the batch has changed names, as TextsUsing
// read it: the revision that its owner had then, and its references.
type readText struct {
	rev  int64
	refs []textRef
}

// textRef is a reference of a text (TextReferences), with what it names.
type textRef struct {
	row queries.TextReferencesRow
	use Use
}

// TextsUsing returns the texts of the batch's project, of its nodes, its
// connections, and its components outside its trash, that use the variable,
// or the component, of the kind, ids.Variable or ids.Component, whose UUID is
// target, as the batch's commands before the one that calls it leave them:
// those whose rows of the index name it, and those whose texts the batch may
// have changed so far (changedTexts, commands.Tx.Changes), which the index
// holds as they were before the batch until its derivation indexes them
// again, each read anew, but once at each of its owner's revisions: what a
// text that the batch has changed names is kept for the batch's later
// commands (commands.Tx.Keep), so that 300 creations of variables after 2,000
// of nodes read each node's text once, not 300 times. A text reads the
// variable or the component's attributes that its value references show,
// each once, and a component that it mentions is a use of its own. A
// component's own description is no use of it: it goes to the trash with the
// component, and leaves nothing broken that names it, so that a component
// whose description mentions it, or shows one of its attributes, is deleted
// as one whose description does not, by the undo of its creation too.
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
	read, _ := tx.Kept(textsKey{}).(map[commands.Ref]readText)
	if read == nil {
		read = map[commands.Ref]readText{}
		tx.Keep(textsKey{}, read)
	}
	// The texts to read: those that the batch has changed since they were
	// last read, and those of the index that it has not changed.
	var refs []textRef
	var nodes, connections, components []pgtype.UUID
	changed, stale := map[pgtype.UUID]bool{}, map[commands.Ref]bool{}
	add := func(kind ids.Kind, id pgtype.UUID) {
		switch kind {
		case ids.Node:
			nodes = append(nodes, id)
		case ids.Connection:
			connections = append(connections, id)
		case ids.Component:
			components = append(components, id)
		}
	}
	for _, c := range changedTexts(tx.Changes()) {
		changed[nullUUID(c.ID)] = true
		if r, ok := read[c.Ref]; ok && r.rev == c.Rev {
			refs = append(refs, r.refs...)
			continue
		}
		stale[c.Ref] = true
		read[c.Ref] = readText{rev: c.Rev}
		add(c.Kind, nullUUID(c.ID))
	}
	for _, r := range referrers {
		for _, o := range []struct {
			kind ids.Kind
			id   pgtype.UUID
		}{{ids.Node, r.NodeID}, {ids.Connection, r.ConnectionID}, {ids.Component, r.ComponentID}} {
			if o.id.Valid && !changed[o.id] {
				changed[o.id] = true
				add(o.kind, o.id)
			}
		}
	}
	if len(nodes)+len(connections)+len(components) > 0 {
		rows, err := q.TextReferences(ctx, queries.TextReferencesParams{WorkspaceID: ws, ProjectID: project,
			NodeIds: list(nodes), ConnectionIds: list(connections), ComponentIds: list(components)})
		if err != nil {
			return nil, fmt.Errorf("dependency: read what the texts name: %w", err)
		}
		for _, r := range rows {
			u, err := inlineUse(r.Inline)
			if err != nil {
				return nil, err
			}
			ref := textRef{row: r, use: u}
			if owner := textOwner(r); stale[owner] {
				t := read[owner]
				t.refs = append(t.refs, ref)
				read[owner] = t
			}
			refs = append(refs, ref)
		}
	}
	// The references to the target, but those of a component's own
	// description, and the owners of their texts, of which only those outside
	// the trash are sites.
	var hits []textRef
	var hitNodes, hitConnections, hitComponents []pgtype.UUID
	owned := map[pgtype.UUID]bool{}
	for _, h := range refs {
		r, u := h.row, h.use
		if u.Target != target || kind == ids.Variable && u.Kind != Variable ||
			kind == ids.Component && (u.Kind == Variable || r.ComponentID.Bytes == target) {
			continue
		}
		hits = append(hits, h)
		owner, l := r.NodeID, &hitNodes
		switch {
		case r.ConnectionID.Valid:
			owner, l = r.ConnectionID, &hitConnections
		case r.ComponentID.Valid:
			owner, l = r.ComponentID, &hitComponents
		}
		if !owned[owner] {
			owned[owner] = true
			*l = append(*l, owner)
		}
	}
	if len(hits) == 0 {
		return nil, nil
	}
	live, err := q.LiveTextOwners(ctx, queries.LiveTextOwnersParams{WorkspaceID: ws, ProjectID: project,
		NodeIds: list(hitNodes), ConnectionIds: list(hitConnections), ComponentIds: list(hitComponents)})
	if err != nil {
		return nil, fmt.Errorf("dependency: read the owners of the texts: %w", err)
	}
	sites := map[ids.UUID]queries.LiveTextOwnersRow{}
	for _, l := range live {
		sites[l.ID.Bytes] = l
	}
	var out []Found
	found := map[Key]map[string]bool{}
	for _, h := range hits {
		r, u := h.row, h.use
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
		if found[f.key] == nil {
			found[f.key] = map[string]bool{}
		}
		if !found[f.key][use] {
			found[f.key][use] = true
			out = append(out, f)
		}
	}
	return out, nil
}

// textOwner returns the owner of the text of the reference r: a node, a
// connection, or a component.
func textOwner(r queries.TextReferencesRow) commands.Ref {
	switch {
	case r.ConnectionID.Valid:
		return commands.Ref{Kind: ids.Connection, ID: r.ConnectionID.Bytes}
	case r.ComponentID.Valid:
		return commands.Ref{Kind: ids.Component, ID: r.ComponentID.Bytes}
	}
	return commands.Ref{Kind: ids.Node, ID: r.NodeID.Bytes}
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
// needs only the role of its command's type. It reads the actor's role
// alone: an API token needs only its command's scope, project:write, as for
// any change of the draft, which reaches every site that the force breaks
// already (PRD §5.6). It holds the undo of a creation or a restoration that
// forces the deletion alike, which a batch does not tell from another.
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
