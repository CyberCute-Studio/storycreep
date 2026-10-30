package expression

import (
	"context"
	"slices"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/dependency"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// RenameSite is an expression of a project that a rename rewrites, as the
// preview of the rename lists it (FR-VAR-03): its site, as a usage of
// package dependency names it, by which a client opens it, whether it is in
// the project's trash, which the rename rewrites too, its source, the source
// rewritten, whether that is too long, which refuses the rename (planWrites),
// and the names that the rename rewrites of the list's kind.
type RenameSite struct {
	Site           string        `json:"site" enum:"node_on_enter,node_on_exit,connection_condition,connection_on_traverse,branch_output_condition" doc:"What the site is: a node's on-enter or on-exit script, a connection's condition or on-traverse script, or a branch output's condition"`
	BoardID        string        `json:"board_id" doc:"The ID of the site's board"`
	BoardName      string        `json:"board_name" doc:"The name of the site's board"`
	NodeID         string        `json:"node_id" doc:"The ID of the site's node: the node of its script, the node that its connection leaves, or its output's branch"`
	NodeTitle      string        `json:"node_title" doc:"The title of the site's node, which may be empty"`
	ConnectionID   string        `json:"connection_id,omitempty" doc:"The ID of the connection of a connection's condition or on-traverse script"`
	BranchOutputID string        `json:"branch_output_id,omitempty" doc:"The ID of the output of a branch output's condition"`
	InTrash        bool          `json:"in_trash" doc:"Whether the site is in the project's trash, with its owner, its node, or its board, which the rename rewrites all the same, so that restoring it brings back what names what it named"`
	Source         string        `json:"source" doc:"The expression's source as it is"`
	Rewritten      string        `json:"rewritten" doc:"The expression's source as the rename rewrites it, with every name that it rewrites, of either list"`
	TooLong        bool          `json:"too_long" doc:"Whether the source rewritten has more than 16384 characters, the most that a source has: the rename is then answered with 422 validation_failed at its key, and writes nothing, until the expression is shortened or a shorter key is chosen, so that a rename of whose sites, of either list, one is too long does not apply"`
	Names          []RenamedName `json:"names" doc:"The names that the rename rewrites of the list's kind, in the order of the source"`
	key            PreviewKey
}

// RenamedName is a name of a source that a rename rewrites (expr.Edit).
type RenamedName struct {
	Start int    `json:"start" doc:"The offset of the name's first character in the source as it is, counted in code points from 0"`
	End   int    `json:"end" doc:"The offset of the character after the name's last, counted in code points from 0"`
	From  string `json:"from" doc:"The name as the source writes it"`
	To    string `json:"to" doc:"The name as the rename writes it"`
	Ref   string `json:"ref" doc:"The ID of what the name names, before the rename and after it: the renamed variable's or component's, the component's whose attribute of the renamed definition it names, or, for a qualified name, the other variable's or component's whose name the new key would take"`
}

// PreviewKey is where a site is in the lists of a rename's preview: its
// board, its node, and its expression, as a rename reads them (planRename).
type PreviewKey struct {
	Board, Node, Expression ids.UUID
}

// Key returns where s is in its list.
func (s RenameSite) Key() PreviewKey { return s.key }

// previewChunk is the fewest expressions that a page of a rename's preview
// reads at its first read, and twice as many at each after (planRename), so
// that a page of few sites, of a list that few expressions are in, reads in
// few round trips.
const previewChunk = 100

// Preview returns at most limit of the expressions of the project, of the
// workspace ws, in its trash or not, that the rename r rewrites, read in q's
// transaction (planRename): those that it rewrites a name of the renamed
// entity of, or, if qualified is set, those that it rewrites a name of
// another entity of, which it qualifies, each with those names, in the order
// of their keys (PreviewKey), after after, or from the first if after is zero.
// It reads none of the expressions up to after, nor any once it has found
// limit, but for the rest of the read that it found the last of them in
// (planRename). The list that a change of a component's key, or of an
// attribute definition's, qualifies is empty, since neither key takes
// another entity's name, and is read as such. The caller checks r's new key
// first, as a rename does.
func Preview(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, r Rename, qualified bool,
	after PreviewKey, limit int) ([]RenameSite, error) {
	out := []RenameSite{}
	if limit <= 0 || qualified && r.Kind != ids.Variable {
		return out, nil
	}
	var boards, nodes []pgtype.UUID
	err := planRename(ctx, q, ws, project, r, after, max(limit, previewChunk), func(rw siteRewrite) bool {
		var names []RenamedName
		for _, e := range rw.Edits {
			if e.Qualified == qualified {
				names = append(names, RenamedName{Start: e.Range.Start, End: e.Range.End, From: e.Old, To: e.New,
					Ref: e.Ref})
			}
		}
		if len(names) == 0 {
			return true
		}
		key := PreviewKey{Board: rw.Owner.Board.Bytes, Node: rw.Node, Expression: rw.Expression}
		site := RenameSite{Site: siteOf(rw.Owner.Kind, rw.Field), BoardID: ids.Encode(ids.Board, key.Board),
			NodeID: ids.Encode(ids.Node, key.Node), InTrash: !rw.Live, Source: rw.Source, Rewritten: rw.Rewritten,
			TooLong: rw.TooLong, Names: names, key: key}
		switch rw.Owner.Kind {
		case ids.Connection:
			site.ConnectionID = ids.Encode(ids.Connection, rw.Owner.ID.Bytes)
		case ids.BranchOutput:
			site.BranchOutputID = ids.Encode(ids.BranchOutput, rw.Owner.ID.Bytes)
		}
		out = append(out, site)
		boards, nodes = append(boards, nullUUID(key.Board)), append(nodes, nullUUID(key.Node))
		return len(out) < limit
	})
	if err != nil || len(out) == 0 {
		return out, err
	}
	labels, err := dependency.SiteLabels(ctx, q, ws, project, slices.Compact(boards), nodes)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].BoardName, out[i].NodeTitle = labels[out[i].key.Board], labels[out[i].key.Node]
	}
	return out, nil
}
