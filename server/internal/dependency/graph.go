package dependency

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// EdgeKind is the kind of an edge of a project's graph of the dependencies
// between its boards (PRD FR-DEP-02), which says what its contributions are.
type EdgeKind string

// The kinds of edge, from a board A to another board B: a variable, or a
// component's attribute, that a site of A writes and a site of B reads, of
// each such pair of sites; a jumper of A whose target is a node of B, of each
// such jumper; and a node of A that an expression of B reads whether it was
// visited, by visited() or visits(), of each such pair of the node and the
// expression, since playing A changes what B allows.
const (
	EdgeVariable  EdgeKind = "variable"
	EdgeAttribute EdgeKind = "attribute"
	EdgeJumper    EdgeKind = "jumper"
	EdgeVisit     EdgeKind = "visit"
)

// edgeKinds are the kinds of edge, in the order of a graph's edges between
// the same two boards.
var edgeKinds = []EdgeKind{EdgeVariable, EdgeAttribute, EdgeJumper, EdgeVisit}

// SiteNode is the site of a contribution's end that is a node itself, rather
// than an expression or a text of it: a jumper, its target, or a node that
// an expression reads whether it was visited.
const SiteNode = "node"

// endSites are the sites of a contribution's end (End.Site), in the order of
// its enum: a usage's sites on a board, but a node's speaker, and the node
// itself.
var endSites = []string{SiteNode, SiteNodeOnEnter, SiteNodeOnExit, SiteNodeText, SiteConnectionCondition,
	SiteConnectionOnTraverse, SiteConnectionLabel, SiteBranchOutputCondition}

// IsEndSite reports whether s is a site of a contribution's end (End.Site),
// as a cursor of a list of contributions holds its ends' sites' types.
func IsEndSite(s string) bool { return slices.Contains(endSites, s) }

// Filter keeps some of a graph's contributions, and so of its edges, as
// FR-DEP-02's filters do: each of its fields that is not zero keeps the
// contributions that it names, and a contribution is kept only if every one
// of them keeps it, so that two fields that name nothing in common keep
// nothing.
type Filter struct {
	// Kind keeps the edges of the kind.
	Kind EdgeKind
	// Board keeps the edges that leave the board or enter it.
	Board ids.UUID
	// Variable keeps the contributions of the variable, which only edges of
	// the kind variable have.
	Variable ids.UUID
	// Component keeps the contributions of the component's attributes, and
	// Attribute those of the attributes of the definition, of any
	// component, which only edges of the kind attribute have.
	Component, Attribute ids.UUID
}

// keeps returns the set of the kinds of edge that f keeps.
func (f Filter) keeps() map[EdgeKind]bool {
	out := map[EdgeKind]bool{}
	for _, k := range edgeKinds {
		switch {
		case f.Kind != "" && f.Kind != k:
		case f.Variable != (ids.UUID{}) && k != EdgeVariable:
		case (f.Component != (ids.UUID{}) || f.Attribute != (ids.UUID{})) && k != EdgeAttribute:
		default:
			out[k] = true
		}
	}
	return out
}

// Graph is a project's graph of the dependencies between its boards, as the
// API shows it: its boards, each with the counts of its badge, and its edges,
// each with how many contributions it has, of the draft as of one moment.
type Graph struct {
	Seq    int64        `json:"seq" doc:"The project's operation sequence as of the draft that the graph was read of, the seq of the last batch that it holds"`
	Boards []GraphBoard `json:"boards" nullable:"false" doc:"The project's boards that are not in its trash, the graph's nodes, every one whether an edge leaves it or enters it or not, in the order of their IDs"`
	Edges  []Edge       `json:"edges" nullable:"false" doc:"The edges that the filters keep, in the order of the IDs of the boards that they leave, then of those that they enter, and then of their kinds, as the enum of kind lists them. No edge joins a board to itself."`
}

// GraphBoard is a board of a graph, with the counts of its badge.
type GraphBoard struct {
	ID       string `json:"id" doc:"The board's ID"`
	Name     string `json:"name" doc:"The board's name"`
	Inbound  int    `json:"inbound" doc:"How many other boards the board depends on: those that an edge that the filters keep leaves for it, each once, whatever the kinds of the edges"`
	Outbound int    `json:"outbound" doc:"How many other boards depend on the board: those that an edge that the filters keep enters from it, each once, whatever the kinds of the edges"`
}

// Edge is an edge of a graph.
type Edge struct {
	FromBoardID   string   `json:"from_board_id" doc:"The ID of the board that the edge leaves, which the board that it enters depends on"`
	ToBoardID     string   `json:"to_board_id" doc:"The ID of the board that the edge enters, another"`
	Kind          EdgeKind `json:"kind" enum:"variable,attribute,jumper,visit" doc:"What the edge's contributions are: of variable, a variable that a site of the board that it leaves writes and a site of the board that it enters reads; of attribute, a component's attribute, so; of jumper, a jumper of the one whose target is a node of the other; of visit, a node of the one that an expression of the other reads whether it was visited, by visited() or visits()"`
	Contributions int64    `json:"contributions" doc:"How many contributions the edge has that the filters keep, which its list of contributions lists: of variable and attribute, the triples of the variable or the attribute, a site that writes it, and a site that reads it; of jumper, the jumpers; and of visit, the pairs of the node visited and the expression that reads it"`
}

// Contribution is a contribution to an edge of a graph, as the API shows it
// (FR-DEP-02 AC2): of a variable or an attribute, the triple of it, a site of
// the board that the edge leaves that writes it, and a site of the board
// that it enters that reads it; of a jumper, the jumper and its target; and
// of a visit, the pair of the node visited and the expression that reads it.
type Contribution struct {
	VariableID  string `json:"variable_id,omitempty" doc:"The ID of the variable of a contribution to an edge of the kind variable"`
	ComponentID string `json:"component_id,omitempty" doc:"The ID of the component of the attribute of a contribution to an edge of the kind attribute"`
	AttributeID string `json:"attribute_id,omitempty" doc:"The ID of the definition of the attribute of a contribution to an edge of the kind attribute, which is the component's attribute of that definition"`
	From        End    `json:"from" doc:"The end of the contribution on the board that the edge leaves: the site that writes the variable or the attribute, the jumper, or the node visited"`
	To          End    `json:"to" doc:"The end of the contribution on the board that the edge enters: the site that reads the variable or the attribute, the jumper's target, or the expression that reads whether the node was visited"`
	key         ContributionKey
}

// End is an end of a contribution: a site, an expression or a text, with its
// node, by which a client opens it, as a usage's site is, or a node itself.
type End struct {
	Site           string `json:"site" enum:"node,node_on_enter,node_on_exit,node_text,connection_condition,connection_on_traverse,connection_label,branch_output_condition" doc:"What the end is: a node itself, a jumper, its target, or the node visited; a node's on-enter or on-exit script, or its text; a connection's condition, its on-traverse script, or its label; or a branch output's condition"`
	NodeID         string `json:"node_id" doc:"The ID of the end's node: the node itself, the node of its script or its text, the node that its connection leaves, or its output's branch"`
	NodeTitle      string `json:"node_title" doc:"The title of the end's node, which may be empty"`
	ConnectionID   string `json:"connection_id,omitempty" doc:"The ID of the connection of a connection's condition, on-traverse script, or label"`
	BranchOutputID string `json:"branch_output_id,omitempty" doc:"The ID of the output of a branch output's condition"`
}

// ContributionKey is where a contribution is in its list
// (Contribution.Key), by which a page of the list says where the next one
// starts: its target, the variable, the component, or the node visited, and
// its attribute's definition, and the node and the site of each of its ends,
// a site being an expression's UUID, or that of a text's owner, or the node
// itself, each zero where it has none, as a jumper has no target, and then
// the site's type (End.Site).
type ContributionKey struct {
	Target, Attribute, FromNode, FromSite, ToNode, ToSite ids.UUID
	// FromSiteType and ToSiteType are the types of the ends' sites
	// (End.Site), which tell apart two sites of one node that share a UUID,
	// as a node's text and the label of a connection that leaves it do if a
	// client chose the connection's UUID to be the node's before migration
	// 00029 refused it (PRD §5.2).
	FromSiteType, ToSiteType string
}

// Key returns where c is in its list.
func (c Contribution) Key() ContributionKey { return c.key }

// Graph returns the graph of the dependencies between the boards of the
// project projectID (FR-DEP-02), once the actor may read the project's
// content (project.Service.ReadSnapshot): its boards that are not in its
// trash, each with the counts of its badge, and the edges that f keeps, of
// the sites, the nodes, and the boards that are not in its trash, each with
// how many contributions it has, as of one moment, so that a batch that
// commits meanwhile is in the graph whole or not at all. It returns the
// errors of project.Service.Get, as project.ErrNotFound.
func (s *Service) Graph(ctx context.Context, actor commands.Actor, projectID ids.UUID, f Filter) (*Graph, error) {
	var g *Graph
	keeps := f.keeps()
	err := s.projects.ReadSnapshot(ctx, s.db, actor, projectID, func(tx pgx.Tx, workspaceID ids.UUID) error {
		q, ws, p := queries.New(tx), nullUUID(workspaceID), nullUUID(projectID)
		project, err := q.Project(ctx, queries.ProjectParams{WorkspaceID: ws, ID: p})
		if err != nil {
			return fmt.Errorf("dependency: read the project's seq: %w", err)
		}
		boards, err := q.GraphBoards(ctx, queries.GraphBoardsParams{WorkspaceID: ws, ProjectID: p})
		if err != nil {
			return fmt.Errorf("dependency: read the boards: %w", err)
		}
		// Each read is made only if f keeps a kind of edge that it gives: the
		// index's rows, of the variables, the attributes, and the visits, and
		// the jumpers. The index's are those of all three kinds, whichever of
		// them f keeps, as the read takes no parameter but the project, for the
		// reason that the header of the graph's queries gives.
		var refs []queries.GraphReferencesRow
		if keeps[EdgeVariable] || keeps[EdgeAttribute] || keeps[EdgeVisit] {
			if refs, err = q.GraphReferences(ctx, queries.GraphReferencesParams{WorkspaceID: ws,
				ProjectID: p}); err != nil {
				return fmt.Errorf("dependency: read the index's rows of the graph: %w", err)
			}
		}
		var jumpers []queries.GraphJumpersRow
		if keeps[EdgeJumper] {
			if jumpers, err = q.GraphJumpers(ctx, queries.GraphJumpersParams{WorkspaceID: ws,
				ProjectID: p}); err != nil {
				return fmt.Errorf("dependency: read the jumpers of the graph: %w", err)
			}
		}
		g = graphOf(project.Project.Seq, boards, edgesOf(f, keeps, refs, jumpers))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return g, nil
}

// edge is an edge of a graph, by its boards and its kind.
type edge struct {
	from, to ids.UUID
	kind     EdgeKind
}

// edgesOf returns the edges that f keeps, of the kinds keeps, which
// f.keeps returns, each with how many of its contributions f keeps, of the
// project's sites' rows of the index refs, which GraphReferences returns,
// and of its jumpers, which GraphJumpers returns: of a variable or an
// attribute, from each board that writes it to each other board that reads
// it, counting each pair of their sites; of a visit, from the visited node's
// board to another board that reads it, counting each site; and of the
// jumpers, from their boards to another board that their targets are on,
// counting each jumper. The rows hold no filter, so that the reads' plans are
// of the project alone.
func edgesOf(f Filter, keeps map[EdgeKind]bool, refs []queries.GraphReferencesRow,
	jumpers []queries.GraphJumpersRow) map[edge]int64 {
	out := map[edge]int64{}
	add := func(from, to ids.UUID, kind EdgeKind, n int64) {
		if from == to || f.Board != (ids.UUID{}) && f.Board != from && f.Board != to {
			return
		}
		out[edge{from, to, kind}] += n
	}
	// The sites of each board that write, and that read, each variable and
	// each component's attribute that f keeps.
	type target struct {
		kind          Kind
		id, attribute ids.UUID
	}
	type sites struct {
		board ids.UUID
		n     int64
	}
	writes, reads := map[target][]sites{}, map[target][]sites{}
	for _, r := range refs {
		board := ids.UUID(r.BoardID.Bytes)
		t := target{kind: Kind(r.TargetKind), id: r.TargetID.Bytes, attribute: r.AttributeDefID.Bytes}
		switch t.kind {
		case Node:
			if keeps[EdgeVisit] && r.VisitedBoardID.Valid {
				add(r.VisitedBoardID.Bytes, board, EdgeVisit, r.Sites)
			}
			continue
		case Variable:
			if !keeps[EdgeVariable] || f.Variable != (ids.UUID{}) && f.Variable != t.id {
				continue
			}
		case Attribute:
			if !keeps[EdgeAttribute] || f.Component != (ids.UUID{}) && f.Component != t.id ||
				f.Attribute != (ids.UUID{}) && f.Attribute != t.attribute {
				continue
			}
		default:
			continue
		}
		if Access(r.Access) == Write {
			writes[t] = append(writes[t], sites{board, r.Sites})
		} else {
			reads[t] = append(reads[t], sites{board, r.Sites})
		}
	}
	for t, ws := range writes {
		kind := EdgeVariable
		if t.kind == Attribute {
			kind = EdgeAttribute
		}
		for _, w := range ws {
			for _, r := range reads[t] {
				add(w.board, r.board, kind, w.n*r.n)
			}
		}
	}
	if keeps[EdgeJumper] {
		for _, j := range jumpers {
			add(j.FromBoardID.Bytes, j.ToBoardID.Bytes, EdgeJumper, j.Jumpers)
		}
	}
	return out
}

// graphOf returns the graph of the boards and the edges, as of the seq: the
// edges in order, and each board's counts, of the other boards that its
// edges leave and enter, each once.
func graphOf(seq int64, boards []queries.GraphBoardsRow, counts map[edge]int64) *Graph {
	edges := slices.Collect(maps.Keys(counts))
	slices.SortFunc(edges, func(a, b edge) int {
		return cmp.Or(bytes.Compare(a.from[:], b.from[:]), bytes.Compare(a.to[:], b.to[:]),
			cmp.Compare(slices.Index(edgeKinds, a.kind), slices.Index(edgeKinds, b.kind)))
	})
	type pair struct{ from, to ids.UUID }
	linked := map[pair]bool{}
	inbound, outbound := map[ids.UUID]int{}, map[ids.UUID]int{}
	g := &Graph{Seq: seq, Boards: make([]GraphBoard, 0, len(boards)), Edges: make([]Edge, 0, len(edges))}
	for _, e := range edges {
		g.Edges = append(g.Edges, Edge{FromBoardID: ids.Encode(ids.Board, e.from), ToBoardID: ids.Encode(ids.Board, e.to),
			Kind: e.kind, Contributions: counts[e]})
		if !linked[pair{e.from, e.to}] {
			linked[pair{e.from, e.to}] = true
			outbound[e.from]++
			inbound[e.to]++
		}
	}
	for _, b := range boards {
		id := ids.UUID(b.ID.Bytes)
		g.Boards = append(g.Boards, GraphBoard{ID: ids.Encode(ids.Board, id), Name: b.Name, Inbound: inbound[id],
			Outbound: outbound[id]})
	}
	return g
}

// Contributions returns at most limit of the contributions to the edge of
// the kind f.Kind from the board from to the board to of the project
// projectID that f keeps (FR-DEP-02 AC2), of the sites, the nodes, and the
// boards that are not in its trash, in the order of their keys
// (ContributionKey): the first, if after is zero, or else those after it. No
// edge joins a board to itself, so a list of from to itself is empty. It
// returns ErrNotFound if the project has no such boards, or one of them is in
// the trash, and the errors of project.Service.Get first. f.Board plays no
// part in it.
func (s *Service) Contributions(ctx context.Context, actor commands.Actor, projectID, from, to ids.UUID, f Filter,
	after ContributionKey, limit int) ([]Contribution, error) {
	if !slices.Contains(edgeKinds, f.Kind) {
		return nil, fmt.Errorf("dependency: no kind of edge %q", f.Kind)
	}
	var out []Contribution
	err := s.projects.ReadSnapshot(ctx, s.db, actor, projectID, func(tx pgx.Tx, workspaceID ids.UUID) error {
		q, ws, p := queries.New(tx), nullUUID(workspaceID), nullUUID(projectID)
		for _, b := range []ids.UUID{from, to} {
			switch _, err := q.Board(ctx, queries.BoardParams{WorkspaceID: ws, ProjectID: p, ID: nullUUID(b)}); {
			case errors.Is(err, pgx.ErrNoRows):
				return ErrNotFound
			case err != nil:
				return fmt.Errorf("dependency: read the edge's boards: %w", err)
			}
		}
		if from == to || !f.keeps()[f.Kind] {
			return nil
		}
		var err error
		out, err = contributions(ctx, q, ws, p, from, to, f, after, int32(min(limit, 1<<20)))
		return err
	})
	return out, err
}

// contributions reads the contributions of Contributions in q, of the
// workspace ws and the project p.
func contributions(ctx context.Context, q *queries.Queries, ws, p pgtype.UUID, from, to ids.UUID, f Filter,
	after ContributionKey, limit int32) ([]Contribution, error) {
	a := [6]pgtype.UUID{zeroUUID(after.Target), zeroUUID(after.Attribute), zeroUUID(after.FromNode),
		zeroUUID(after.FromSite), zeroUUID(after.ToNode), zeroUUID(after.ToSite)}
	var out []Contribution
	switch f.Kind {
	case EdgeVariable, EdgeAttribute:
		params := queries.ValueContributionsParams{WorkspaceID: ws, ProjectID: p, TargetKind: string(Variable),
			FromBoardID: nullUUID(from), ToBoardID: nullUUID(to), TargetID: nullUUID(f.Variable),
			AfterTarget: a[0], AfterAttribute: a[1], AfterFromNode: a[2], AfterFromSite: a[3],
			AfterFromSiteType: after.FromSiteType, AfterToNode: a[4], AfterToSite: a[5],
			AfterToSiteType: after.ToSiteType, MaxRows: limit}
		if f.Kind == EdgeAttribute {
			params.TargetKind, params.TargetID = string(Attribute), nullUUID(f.Component)
			params.AttributeDefID = nullUUID(f.Attribute)
		}
		rows, err := q.ValueContributions(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("dependency: read the contributions: %w", err)
		}
		for _, r := range rows {
			c := Contribution{
				From: End{Site: r.FromSite, NodeID: encode(ids.Node, r.FromNodeID), NodeTitle: r.FromNodeTitle,
					ConnectionID:   encode(ids.Connection, r.FromConnectionID),
					BranchOutputID: encode(ids.BranchOutput, r.FromBranchOutputID)},
				To: End{Site: r.ToSite, NodeID: encode(ids.Node, r.ToNodeID), NodeTitle: r.ToNodeTitle,
					ConnectionID:   encode(ids.Connection, r.ToConnectionID),
					BranchOutputID: encode(ids.BranchOutput, r.ToBranchOutputID)},
				key: ContributionKey{Target: r.TargetID.Bytes, Attribute: r.AttributeDefID.Bytes,
					FromNode: r.FromNodeID.Bytes, FromSite: r.FromSiteID.Bytes, ToNode: r.ToNodeID.Bytes,
					ToSite: r.ToSiteID.Bytes, FromSiteType: r.FromSite, ToSiteType: r.ToSite}}
			if f.Kind == EdgeVariable {
				c.VariableID = encode(ids.Variable, r.TargetID)
			} else {
				c.ComponentID, c.AttributeID = encode(ids.Component, r.TargetID), encode(ids.AttributeDef, r.AttributeDefID)
			}
			out = append(out, c)
		}
	case EdgeVisit:
		rows, err := q.VisitContributions(ctx, queries.VisitContributionsParams{WorkspaceID: ws, ProjectID: p,
			FromBoardID: nullUUID(from), ToBoardID: nullUUID(to), AfterTarget: a[0], AfterAttribute: a[1],
			AfterFromNode: a[2], AfterFromSite: a[3], AfterToNode: a[4], AfterToSite: a[5], MaxRows: limit})
		if err != nil {
			return nil, fmt.Errorf("dependency: read the contributions: %w", err)
		}
		for _, r := range rows {
			out = append(out, Contribution{
				From: End{Site: SiteNode, NodeID: encode(ids.Node, r.VisitedID), NodeTitle: r.VisitedTitle},
				To: End{Site: r.Site, NodeID: encode(ids.Node, r.NodeID), NodeTitle: r.NodeTitle,
					ConnectionID:   encode(ids.Connection, r.ConnectionID),
					BranchOutputID: encode(ids.BranchOutput, r.BranchOutputID)},
				key: ContributionKey{Target: r.VisitedID.Bytes, FromNode: r.VisitedID.Bytes, FromSite: r.VisitedID.Bytes,
					ToNode: r.NodeID.Bytes, ToSite: r.SiteID.Bytes, FromSiteType: SiteNode, ToSiteType: r.Site}})
		}
	case EdgeJumper:
		rows, err := q.JumperContributions(ctx, queries.JumperContributionsParams{WorkspaceID: ws, ProjectID: p,
			FromBoardID: nullUUID(from), ToBoardID: nullUUID(to), AfterTarget: a[0], AfterAttribute: a[1],
			AfterFromNode: a[2], AfterFromSite: a[3], AfterToNode: a[4], AfterToSite: a[5], MaxRows: limit})
		if err != nil {
			return nil, fmt.Errorf("dependency: read the contributions: %w", err)
		}
		for _, r := range rows {
			out = append(out, Contribution{
				From: End{Site: SiteNode, NodeID: encode(ids.Node, r.JumperID), NodeTitle: r.JumperTitle},
				To:   End{Site: SiteNode, NodeID: encode(ids.Node, r.TargetID), NodeTitle: r.TargetTitle},
				key: ContributionKey{FromNode: r.JumperID.Bytes, FromSite: r.JumperID.Bytes, ToNode: r.TargetID.Bytes,
					ToSite: r.TargetID.Bytes, FromSiteType: SiteNode, ToSiteType: SiteNode}})
		}
	}
	return out, nil
}
