package database_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/dependency/dependencytest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// TestTextReferencesByKind upgrades a database whose index of what the texts
// name (expression_ref) holds the rows that the derivation wrote before plan
// item 89 of the texts of a node, a connection, and a component that share a
// UUID, which one batch changed: what all three name, as the node's, which it
// took them for; and of a node and a component whose twins, which shared
// their UUIDs, have since been purged: what the node and its twin named, as
// the node's, and nothing as the component's, whose rows the twin's purge
// deleted. It checks that the migration writes each such owner's rows anew,
// of what its own text names, as package dependency gives them, which a
// recomputation from scratch reads (dependencytest.Drift), and leaves as they
// are the rows of the texts whose rows are what they name.
func TestTextReferencesByKind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.Empty(t)
	if _, err := database.Migrate(ctx, d.AdminURL, d.AppURL,
		database.MigrateOptions{Migrations: migrationsBefore(t, "00042")}); err != nil {
		t.Fatal(err)
	}
	admin := d.Admin(t)
	ws, project, folder, board := ids.New(), ids.New(), ids.New(), ids.New()
	gold, tide, cast, trust, elara := ids.New(), ids.New(), ids.New(), ids.New(), ids.New()
	shared, keeper, orphan, bereft := ids.New(), ids.New(), ids.New(), ids.New()
	enc := func(k ids.Kind, u ids.UUID) string { return ids.Encode(k, u) }
	text := func(inline string) string {
		return `{"type": "doc", "content": [{"type": "paragraph", "content": [` + inline + `]}]}`
	}
	value := func(ref string) string { return fmt.Sprintf(`{"type": "valueRef", "attrs": {"ref": %q}}`, ref) }
	attr := func(c, a ids.UUID) string {
		return fmt.Sprintf(`{"type": "valueRef", "attrs": {"ref": %q, "attribute": %q}}`, enc(ids.Component, c),
			enc(ids.AttributeDef, a))
	}
	mention := func(c ids.UUID) string {
		return fmt.Sprintf(`{"type": "mention", "attrs": {"id": %q}}`, enc(ids.Component, c))
	}
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
			VALUES ($1, $3, $4, 'global', 'V', 'gold', 'int', '0'), ($2, $3, $4, 'global', 'W', 'tide', 'int', '0')`,
			[]any{gold, tide, ws, project}},
		{`INSERT INTO component_type (id, workspace_id, project_id, key, name) VALUES ($1, $2, $3, 'cast', 'Cast')`,
			[]any{cast, ws, project}},
		{`INSERT INTO attribute_def (id, workspace_id, project_id, component_type_id, "order", key, name, type,
				default_value, is_runtime)
			VALUES ($1, $2, $3, $4, 'V', 'trust', 'Trust', 'int', '0', true)`, []any{trust, ws, project, cast}},
		// The component that shares its UUID with the node and the
		// connection mentions Elara, whose own description shows tide, and
		// Nell, whose twin is purged, mentions Elara too.
		{`INSERT INTO component (id, workspace_id, project_id, component_type_id, "order", key, name, description)
			VALUES ($1, $4, $5, $6, 'V', 'mira', 'Mira', $7), ($2, $4, $5, $6, 'W', 'elara', 'Elara', $8),
				($3, $4, $5, $6, 'X', 'nell', 'Nell', $7)`,
			[]any{shared, elara, bereft, ws, project, cast, text(mention(elara)),
				text(value(enc(ids.Variable, tide)))}},
		// The node that shares its UUID shows gold, Keeper shows tide, and
		// the node whose twin is purged shows gold.
		{`INSERT INTO node (id, workspace_id, project_id, board_id, type, kind, text, x, y)
			VALUES ($1, $4, $5, $6, 'element', 'narration', $7, 0, 0),
				($2, $4, $5, $6, 'element', 'narration', $8, 0, 0),
				($3, $4, $5, $6, 'element', 'narration', $7, 0, 0)`,
			[]any{shared, keeper, orphan, ws, project, board, text(value(enc(ids.Variable, gold))),
				text(value(enc(ids.Variable, tide)))}},
		// The connection that shares its UUID, from Keeper to the node,
		// shows Elara's trust.
		{`INSERT INTO connection (id, workspace_id, project_id, board_id, from_node_id, to_node_id, "order", label)
			VALUES ($1, $2, $3, $4, $5, $1, 'V', $6)`, []any{shared, ws, project, board, keeper, text(attr(elara, trust))}},
		// The index's rows, as the derivation wrote them: Keeper's and
		// Elara's; what the three texts that share a UUID name, all of them
		// as the node's; and what the node whose twin is purged and the
		// twin, a connection that showed tide, named, as the node's, while
		// Nell's rows, which were its twin's, a node, are gone with it.
		{`INSERT INTO expression_ref (workspace_id, project_id, node_id, component_id, board_id, target_kind, target_id,
				attribute_def_id, access)
			VALUES ($1, $2, $3, NULL, $4, 'variable', $7, NULL, 'read'),
				($1, $2, NULL, $5, NULL, 'variable', $7, NULL, 'read'),
				($1, $2, $6, NULL, $4, 'variable', $8, NULL, 'read'),
				($1, $2, $6, NULL, $4, 'attribute', $5, $9, 'read'),
				($1, $2, $6, NULL, $4, 'component', $5, NULL, 'read'),
				($1, $2, $10, NULL, $4, 'variable', $8, NULL, 'read'),
				($1, $2, $10, NULL, $4, 'variable', $7, NULL, 'read')`,
			[]any{ws, project, keeper, board, elara, shared, tide, gold, trust, orphan}},
	} {
		if _, err := admin.Exec(ctx, c.sql, c.args...); err != nil {
			t.Fatalf("%s: %v", c.sql, err)
		}
	}
	// What the derivation wrote is not what the texts name, so that a
	// migration that wrote nothing would not pass for one that wrote it anew.
	drift, err := dependencytest.Drift(ctx, admin, project)
	if err != nil {
		t.Fatal(err)
	}
	if len(drift) != 6 {
		t.Fatalf("before the migration, the index differs from what the texts name by %q, want 6 rows", drift)
	}
	// Where the rows of Keeper's and Elara's texts, which are what they
	// name, are stored, which a migration that wrote them anew would change.
	kept := func() string {
		t.Helper()
		var at string
		if err := admin.QueryRow(ctx, `SELECT string_agg(ctid::text, ' ' ORDER BY ctid) FROM expression_ref
			WHERE node_id = $1 OR component_id = $2`, keeper, elara).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}
	before := kept()
	if _, err := database.Migrate(ctx, d.AdminURL, d.AppURL, database.MigrateOptions{}); err != nil {
		t.Fatal(err)
	}
	if drift, err = dependencytest.Drift(ctx, admin, project); err != nil {
		t.Fatal(err)
	}
	if len(drift) > 0 {
		t.Errorf("the migration's rows are not what the texts name: %q", drift)
	}
	if after := kept(); after != before {
		t.Errorf("the rows of the texts whose rows are what they name are at %s, want them left at %s", after, before)
	}
}
