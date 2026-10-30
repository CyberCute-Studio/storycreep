package database_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/dependency/dependencytest"
	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// TestExpressionRefsBackfill upgrades a database whose content has no index
// of what it names yet (expression_ref), as the migrations before the
// index's left it, and checks that the index's migration gives it the rows
// that package dependency gives the same content, which a recomputation from
// scratch reads (dependencytest.Drift): of the expressions that compile, a
// condition's reads, and a script's writes, its updates' reads and writes,
// and the reads of its values and of its calls' arguments, of variables, of
// attributes by their components and definitions, and of nodes, but none of
// an expression that does not compile; and of the texts of the nodes, the
// connections, and the components, the reads of what their value references
// show and of the components that they mention, however deeply their lists
// nest, in the trash or not.
func TestExpressionRefsBackfill(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.Empty(t)
	if _, err := database.Migrate(ctx, d.AdminURL, d.AppURL,
		database.MigrateOptions{Migrations: migrationsBefore(t, "00027")}); err != nil {
		t.Fatal(err)
	}
	admin := d.Admin(t)
	ws, project, folder, board := ids.New(), ids.New(), ids.New(), ids.New()
	gold, tide, cast, trust, mood, elara, mira := ids.New(), ids.New(), ids.New(), ids.New(), ids.New(), ids.New(),
		ids.New()
	keeper, pier, gate := ids.New(), ids.New(), ids.New()
	enc := func(k ids.Kind, u ids.UUID) string { return ids.Encode(k, u) }
	attributes := []expr.Attribute{{ID: enc(ids.AttributeDef, trust), Key: "trust", Type: "int", Runtime: true},
		{ID: enc(ids.AttributeDef, mood), Key: "mood", Type: "string", Runtime: true}}
	p, err := expr.NewProject([]expr.Variable{{ID: enc(ids.Variable, gold), Key: "gold", Type: "int"},
		{ID: enc(ids.Variable, tide), Key: "tide", Type: "int", Board: enc(ids.Board, board)}},
		[]expr.Component{{ID: enc(ids.Component, elara), Key: "elara", Attributes: attributes},
			{ID: enc(ids.Component, mira), Key: "mira", Attributes: attributes}},
		[]expr.BoardNode{{ID: enc(ids.Node, keeper)}, {ID: enc(ids.Node, pier)}})
	if err != nil {
		t.Fatal(err)
	}
	ast := func(source string, c expr.Context) string {
		t.Helper()
		n, err := expr.Compile(source, c, p, enc(ids.Board, board))
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		data, err := expr.Encode(n)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	text := func(inlines string) string {
		return `{"type": "doc", "content": [{"type": "paragraph", "content": [` + inlines + `]}]}`
	}
	value := func(ref string) string { return fmt.Sprintf(`{"type": "valueRef", "attrs": {"ref": %q}}`, ref) }
	attr := func(c, a ids.UUID) string {
		return fmt.Sprintf(`{"type": "valueRef", "attrs": {"ref": %q, "attribute": %q, "decimals": 2}}`,
			enc(ids.Component, c), enc(ids.AttributeDef, a))
	}
	mention := func(c ids.UUID) string {
		return fmt.Sprintf(`{"type": "mention", "attrs": {"id": %q}, "marks": [{"type": "italic"}]}`,
			enc(ids.Component, c))
	}
	nested := `{"type": "doc", "content": [{"type": "paragraph"}, {"type": "orderedList", "attrs": {"start": 1}, ` +
		`"content": [{"type": "listItem", "content": [{"type": "paragraph", "content": [` + value(enc(ids.Variable,
		tide)) + `]}, {"type": "bulletList", "content": [{"type": "listItem", "content": [{"type": "paragraph", ` +
		`"content": [` + mention(elara) + `, {"type": "text", "text": "mention"}]}]}]}]}]}]}`
	for _, c := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Harbour', 'harbour', 'unlimited')`,
			[]any{ws}},
		{`INSERT INTO project (id, workspace_id, name) VALUES ($1, $2, 'Harbour')`, []any{project, ws}},
		{`INSERT INTO folder (id, workspace_id, project_id, kind, name, "order")
			VALUES ($1, $2, $3, 'board', 'Act 1', 'V')`, []any{folder, ws, project}},
		{`INSERT INTO board (id, workspace_id, project_id, folder_id, "order", name, slug)
			VALUES ($1, $2, $3, $4, 'V', 'Harbour', 'harbour')`, []any{board, ws, project, folder}},
		{`INSERT INTO variable (id, workspace_id, project_id, scope, "order", key, type, default_value)
			VALUES ($1, $2, $3, 'global', 'V', 'gold', 'int', '0')`, []any{gold, ws, project}},
		{`INSERT INTO variable (id, workspace_id, project_id, scope, board_id, "order", key, type, default_value)
			VALUES ($1, $2, $3, 'board', $4, 'V', 'tide', 'int', '0')`, []any{tide, ws, project, board}},
		{`INSERT INTO component_type (id, workspace_id, project_id, key, name) VALUES ($1, $2, $3, 'cast', 'Cast')`,
			[]any{cast, ws, project}},
		{`INSERT INTO attribute_def (id, workspace_id, project_id, component_type_id, "order", key, name, type,
				default_value, is_runtime)
			VALUES ($1, $3, $4, $5, 'V', 'trust', 'Trust', 'int', '0', true),
				($2, $3, $4, $5, 'W', 'mood', 'Mood', 'string', '""', true)`, []any{trust, mood, ws, project, cast}},
		// Elara's description mentions Mira, and Mira's, in the trash, shows
		// gold.
		{`INSERT INTO component (id, workspace_id, project_id, component_type_id, "order", key, name, description,
				deleted_at)
			VALUES ($1, $3, $4, $5, 'V', 'elara', 'Elara', $6, NULL), ($2, $3, $4, $5, 'W', 'mira', 'Mira', $7, now())`,
			[]any{elara, mira, ws, project, cast, text(mention(mira)), text(value(enc(ids.Variable, gold)))}},
		// Keeper's text shows gold and elara.trust, and mentions Mira twice;
		// Pier's nests what it names in lists.
		{`INSERT INTO node (id, workspace_id, project_id, board_id, type, kind, text, x, y)
			VALUES ($1, $3, $4, $5, 'element', 'dialogue', $6, 0, 0), ($2, $3, $4, $5, 'element', 'dialogue', $7, 0, 0)`,
			[]any{keeper, pier, ws, project, board, text(value(enc(ids.Variable, gold)) + ", " + attr(elara, trust) +
				", " + mention(mira) + ", " + mention(mira)), nested}},
		{`INSERT INTO connection (id, workspace_id, project_id, board_id, from_node_id, to_node_id, "order", label)
			VALUES ($1, $2, $3, $4, $5, $6, 'V', $7)`, []any{gate, ws, project, board, keeper, pier,
			text(attr(mira, mood))}},
		{`INSERT INTO expression (workspace_id, project_id, board_id, node_id, connection_id, field, context, source,
				ast, ast_version, is_valid, error)
			VALUES ($1, $2, $3, $4, NULL, 'on_enter', 'script', '', $7, 1, true, NULL),
				($1, $2, $3, $4, NULL, 'on_exit', 'script', '', $8, 1, true, NULL),
				($1, $2, $3, NULL, $5, 'condition', 'condition', '', $9, 1, true, NULL),
				($1, $2, $3, NULL, $5, 'on_traverse', 'script', 'gold = nope', NULL, NULL, false,
					'{"code": "unknown_identifier", "start": 7, "end": 11}'),
				($1, $2, $3, $6, NULL, 'on_enter', 'script', '', $10, 1, true, NULL)`,
			[]any{ws, project, board, keeper, gate, pier,
				ast("elara.trust += 1\ngold = tide * 2\nelara.mood = \"calm\"", expr.ContextScript),
				ast(`signal("seen", mira.trust + gold)`+"\ntide -= 1\ngold %= 3", expr.ContextScript),
				ast(`visited(node("`+enc(ids.Node, pier)+`")) && gold >= elara.trust`, expr.ContextCondition),
				ast("", expr.ContextScript)}},
	} {
		if _, err := admin.Exec(ctx, c.sql, c.args...); err != nil {
			t.Fatalf("%s: %v", c.sql, err)
		}
	}
	if _, err := database.Migrate(ctx, d.AdminURL, d.AppURL, database.MigrateOptions{}); err != nil {
		t.Fatal(err)
	}
	drift, err := dependencytest.Drift(ctx, admin, project)
	if err != nil {
		t.Fatal(err)
	}
	if len(drift) > 0 {
		t.Errorf("the migration's rows are not what the content names: %q", drift)
	}
	// The rows are those that the fixture names, so that a migration that
	// wrote none, and content that named none, would not pass for it.
	rows, err := admin.Query(ctx, `SELECT access || ' ' || target_kind FROM expression_ref
		WHERE project_id = $1 ORDER BY 1`, project)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for kind, n := range map[string]int{
		// Keeper's on-enter: elara.trust read and written, tide read, gold
		// and elara.mood written; its on-exit: mira.trust, gold, and tide
		// read, and tide and gold written; the condition: pier, gold, and
		// elara.trust read; the on-traverse script, which does not compile,
		// and Pier's empty script nothing.
		"read attribute": 1 + 1 + 1, "write attribute": 2, "read variable": 1 + 2 + 1, "write variable": 1 + 2,
		"read node": 1,
		// The texts: Keeper's gold, elara.trust, and Mira; Pier's tide and
		// Elara; the label's mira.mood; and the descriptions' Mira and gold.
		"read component": 1 + 1 + 1,
	} {
		for range n {
			want = append(want, kind)
		}
	}
	for _, kind := range []string{"read variable", "read attribute", "read variable", "read attribute",
		"read variable"} {
		want = append(want, kind)
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("the migration's rows are %q, want %q", got, want)
	}
}
