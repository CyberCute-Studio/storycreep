// Package dependencytest checks the index of what a project's content names
// (package dependency, PRD FR-DEP-01 AC1) against a recomputation of it from
// scratch: from what the project's expressions compiled to, and from what
// its texts hold, as they are stored, rather than from what the batches that
// wrote them changed, which is how the pipeline keeps the index.
package dependencytest

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/dependency"
	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
)

// Querier runs a query: the superuser's connection, which sees every
// workspace's rows, or a transaction of the project's workspace.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Drift returns how the index of the project differs from what a
// recomputation of it from scratch gives, each row of the index that it
// lacks, as "lacks …", and each that it has beyond them, as "has …", or
// nothing if they are the same: a row of each use (dependency.Uses) of the
// AST of each expression that compiled, in the trash or not, and of each use
// (dependency.TextUses) of each node's text, each connection's label, and
// each component's description, in the trash or not, each with its site's
// board.
func Drift(ctx context.Context, conn Querier, project ids.UUID) ([]string, error) {
	var want []string
	add := func(site string, board pgtype.UUID, uses []dependency.Use) {
		for _, u := range uses {
			want = append(want, line(site, board, u))
		}
	}
	rows, err := conn.Query(ctx, `SELECT id, board_id, context, ast FROM expression
		WHERE project_id = $1 AND ast IS NOT NULL`, project)
	if err != nil {
		return nil, err
	}
	type expression struct {
		ID, Board pgtype.UUID
		Context   string
		AST       []byte
	}
	exprs, err := pgx.CollectRows(rows, pgx.RowToStructByPos[expression])
	if err != nil {
		return nil, err
	}
	for _, e := range exprs {
		n, err := expr.Decode(e.AST, expr.Context(e.Context))
		if err != nil {
			return nil, fmt.Errorf("the AST of the expression %s: %w", ids.UUID(e.ID.Bytes), err)
		}
		uses, err := dependency.Uses(n)
		if err != nil {
			return nil, fmt.Errorf("the expression %s: %w", ids.UUID(e.ID.Bytes), err)
		}
		add("expression "+ids.UUID(e.ID.Bytes).String(), e.Board, uses)
	}
	rows, err = conn.Query(ctx, `SELECT 'node ' || id::text, board_id, text FROM node
			WHERE project_id = $1 AND text IS NOT NULL
		UNION ALL
		SELECT 'connection ' || id::text, board_id, label FROM connection WHERE project_id = $1
		UNION ALL
		SELECT 'component ' || id::text, NULL, description FROM component WHERE project_id = $1`, project)
	if err != nil {
		return nil, err
	}
	type text struct {
		Site  string
		Board pgtype.UUID
		Doc   []byte
	}
	texts, err := pgx.CollectRows(rows, pgx.RowToStructByPos[text])
	if err != nil {
		return nil, err
	}
	for _, t := range texts {
		doc, err := richtext.Parse(t.Doc)
		if err != nil {
			return nil, fmt.Errorf("the text of the %s: %w", t.Site, err)
		}
		uses, err := dependency.TextUses(*doc)
		if err != nil {
			return nil, fmt.Errorf("the text of the %s: %w", t.Site, err)
		}
		add(t.Site, t.Board, uses)
	}
	rows, err = conn.Query(ctx, `SELECT CASE WHEN expression_id IS NOT NULL THEN 'expression ' || expression_id::text
			WHEN node_id IS NOT NULL THEN 'node ' || node_id::text
			WHEN connection_id IS NOT NULL THEN 'connection ' || connection_id::text
			ELSE 'component ' || component_id::text END,
			board_id, target_kind, target_id, attribute_def_id, access
		FROM expression_ref WHERE project_id = $1`, project)
	if err != nil {
		return nil, err
	}
	type indexed struct {
		Site              string
		Board             pgtype.UUID
		Kind              string
		Target, Attribute pgtype.UUID
		Access            string
	}
	index, err := pgx.CollectRows(rows, pgx.RowToStructByPos[indexed])
	if err != nil {
		return nil, err
	}
	have := make([]string, len(index))
	for i, r := range index {
		have[i] = line(r.Site, r.Board, dependency.Use{Kind: dependency.Kind(r.Kind), Target: r.Target.Bytes,
			Attribute: r.Attribute.Bytes, Access: dependency.Access(r.Access)})
	}
	return diff(want, have), nil
}

// line returns the row of the index of the use u of the site, of the board,
// as Drift lists it.
func line(site string, board pgtype.UUID, u dependency.Use) string {
	b := "no board"
	if board.Valid {
		b = "board " + ids.UUID(board.Bytes).String()
	}
	s := fmt.Sprintf("%s (%s) %s %s %s", site, b, u.Access, u.Kind, u.Target)
	if u.Attribute != (ids.UUID{}) {
		s += " " + u.Attribute.String()
	}
	return s
}

// diff returns what want holds that have does not, as "lacks …", and what
// have holds that want does not, as "has …", each as often as the one holds
// it more often than the other.
func diff(want, have []string) []string {
	count := map[string]int{}
	for _, w := range want {
		count[w]++
	}
	for _, h := range have {
		count[h]--
	}
	var out []string
	for _, k := range slices.Sorted(maps.Keys(count)) {
		for n := count[k]; n > 0; n-- {
			out = append(out, "lacks "+k)
		}
		for n := count[k]; n < 0; n++ {
			out = append(out, "has "+k)
		}
	}
	return out
}
