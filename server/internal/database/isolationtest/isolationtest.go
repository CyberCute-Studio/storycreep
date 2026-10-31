// Package isolationtest is the cross-tenant isolation harness (PRD
// NFR-SEC-01, §5.4). It seeds two workspaces, A and B, each with rows of its
// own in every tenant table, and runs repository operations in a transaction
// of workspace A, pointed at workspace B's rows, as a repository with a
// broken filter would be: one that filters by the wrong workspace, or by
// none. Row-level security must deny them all the same, so the harness fails
// the test if such an operation returns, changes, or adds a row of B.
//
// It also runs each operation on A's own rows first, in a transaction that it
// rolls back, and fails the test unless the operation works there, so that
// an operation that fails on every workspace's rows, such as one the
// application login lacks a privilege for, does not pass for one that row-
// level security holds.
//
// Tables lists the tenant tables. A migration that adds one adds it there,
// with how to add a row, and the schema test in package database fails until
// it does (FR-TEN-01 AC1). Fixture.CheckTables tries reads, updates, deletes,
// and inserts of every table from the wrong workspace, reads, updates, and
// deletes without a filter, and moves of a workspace's rows into another, and
// Fixture.Check tries a repository's own operations.
package isolationtest

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// Querier runs statements: the superuser's connection, or a transaction of
// the application login.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Table is a tenant table: one whose rows belong to a workspace, and that
// row-level security protects.
type Table struct {
	Name string
	// ID is the column of the IDs that the harness gives the table's rows,
	// or "" for id. The harness reads the rows that an operation reaches by
	// it, so each row that it adds has an ID of its own there, which a random
	// one is, even where the table's key is wider, as applied_command's
	// command_id is unique only within its project.
	ID string
	// Insert prepares a new row of the workspace w. It first adds, through
	// admin, the superuser's connection, the rows of other tables that the
	// new row needs, and then returns the statement that adds the row. The
	// statement goes through the application login's transaction when the
	// harness tries it, and through admin when the fixture seeds w, and it
	// must add a row that conflicts with none that an earlier call added.
	Insert func(ctx context.Context, admin Querier, w *Workspace) (Statement, error)
}

// idColumn returns the quoted name of the column that identifies a row of
// the table (Table.ID).
func (t Table) idColumn() string {
	if t.ID == "" {
		return "id"
	}
	return pgx.Identifier{t.ID}.Sanitize()
}

// Statement adds a row.
type Statement struct {
	// ID is the new row's ID, the value of its Table.ID column.
	ID   ids.UUID
	SQL  string
	Args []any
}

// Tables are the tenant tables, in an order in which each table's rows need
// only rows of the tables before it.
var Tables = []Table{
	{Name: "project", Insert: func(_ context.Context, _ Querier, w *Workspace) (Statement, error) {
		id := ids.New()
		return Statement{id, "INSERT INTO project (id, workspace_id, name) VALUES ($1, $2, 'Harbour')",
			[]any{id, w.ID}}, nil
	}},
	{Name: "membership", Insert: func(ctx context.Context, admin Querier, w *Workspace) (Statement, error) {
		// A user is a member of a workspace once.
		user, err := NewUser(ctx, admin)
		if err != nil {
			return Statement{}, err
		}
		id := ids.New()
		return Statement{id, "INSERT INTO membership (id, workspace_id, user_id, role) VALUES ($1, $2, $3, 'editor')",
			[]any{id, w.ID, user}}, nil
	}},
	{Name: "project_grant", Insert: func(ctx context.Context, admin Querier, w *Workspace) (Statement, error) {
		// A membership has one grant per project.
		project := ids.New()
		if _, err := admin.Exec(ctx, "INSERT INTO project (id, workspace_id, name) VALUES ($1, $2, 'Harbour')",
			project, w.ID); err != nil {
			return Statement{}, fmt.Errorf("add a project: %w", err)
		}
		id := ids.New()
		return Statement{id, "INSERT INTO project_grant (id, workspace_id, membership_id, project_id, role) " +
			"VALUES ($1, $2, $3, $4, 'viewer')", []any{id, w.ID, w.Rows["membership"], project}}, nil
	}},
	{Name: "invite", Insert: func(_ context.Context, _ Querier, w *Workspace) (Statement, error) {
		id := ids.New()
		return Statement{id, "INSERT INTO invite (id, workspace_id, role, token_hash, expires_at) " +
			"VALUES ($1, $2, 'viewer', $3, now() + interval '14 days')", []any{id, w.ID, TokenHash()}}, nil
	}},
	{Name: "api_token", Insert: func(_ context.Context, _ Querier, w *Workspace) (Statement, error) {
		id := ids.New()
		return Statement{id, "INSERT INTO api_token (id, workspace_id, user_id, project_id, name, token_hash, scopes) " +
			"VALUES ($1, $2, $3, $4, 'isolation', $5, ARRAY['project:read'])",
			[]any{id, w.ID, w.UserID, w.Rows["project"], TokenHash()}}, nil
	}},
	{Name: "audit_entry", Insert: func(_ context.Context, _ Querier, w *Workspace) (Statement, error) {
		id := ids.New()
		return Statement{id, "INSERT INTO audit_entry (id, workspace_id, actor_user_id, action, result) " +
			"VALUES ($1, $2, $3, 'isolation.test', 'success')", []any{id, w.ID, w.UserID}}, nil
	}},
	{Name: "operation", Insert: func(_ context.Context, _ Querier, w *Workspace) (Statement, error) {
		// A project has one operation of a seq.
		id := ids.New()
		return Statement{id, "INSERT INTO operation (id, workspace_id, project_id, seq, command_ids, actor_user_id, " +
			"type, payload, entity_refs) VALUES ($1, $2, $3, $4, ARRAY[$5::uuid], $6, 'isolation.test', '[]', '[]')",
			[]any{id, w.ID, w.Rows["project"], seq(), ids.New(), w.UserID}}, nil
	}},
	{Name: "applied_command", ID: "command_id", Insert: func(_ context.Context, _ Querier, w *Workspace) (Statement,
		error) {
		id := ids.New()
		return Statement{id, "INSERT INTO applied_command (workspace_id, project_id, command_id, seq) " +
			"VALUES ($1, $2, $3, $4)", []any{w.ID, w.Rows["project"], id, seq()}}, nil
	}},
	{Name: "idempotency_record", ID: "command_id", Insert: func(_ context.Context, _ Querier, w *Workspace) (Statement,
		error) {
		// The record of a batch of one command, which its own row holds.
		id := ids.New()
		return Statement{id, "INSERT INTO idempotency_record (workspace_id, project_id, command_id, batch_command_id, " +
				"command_ids, response) VALUES ($1, $2, $3, $3, ARRAY[$3::uuid], '{}')", []any{w.ID, w.Rows["project"], id}},
			nil
	}},
	{Name: "asset", Insert: func(_ context.Context, _ Querier, w *Workspace) (Statement, error) {
		// A project has one asset of a content, which a random hash makes new.
		id, sum := ids.New(), make([]byte, 32)
		_, _ = rand.Read(sum)
		key := fmt.Sprintf("%s/%x.png", w.ID, sum)
		return Statement{id, "INSERT INTO asset (id, workspace_id, project_id, sha256, mime, bytes, storage_key) " +
			"VALUES ($1, $2, $3, $4, 'image/png', 1, $5)", []any{id, w.ID, w.Rows["project"], sum, key}}, nil
	}},
	{Name: "folder", Insert: func(_ context.Context, _ Querier, w *Workspace) (Statement, error) {
		id := ids.New()
		return Statement{id, `INSERT INTO folder (id, workspace_id, project_id, kind, name, "order") ` +
			"VALUES ($1, $2, $3, 'board', 'Act 1', 'V')", []any{id, w.ID, w.Rows["project"]}}, nil
	}},
	{Name: "board", Insert: func(_ context.Context, _ Querier, w *Workspace) (Statement, error) {
		// A board's slug is unique within its project, which its ID makes.
		id := ids.New()
		args := []any{id, w.ID, w.Rows["project"], w.Rows["folder"], "harbour-" + strings.ReplaceAll(id.String(), "-", "")}
		return Statement{id, `INSERT INTO board (id, workspace_id, project_id, folder_id, "order", name, slug) ` +
			"VALUES ($1, $2, $3, $4, 'V', 'Harbour', $5)", args}, nil
	}},
	{Name: "node", Insert: func(_ context.Context, _ Querier, w *Workspace) (Statement, error) {
		// A node is of the workspace's board, with which it is in the trash
		// while the board is, so the trash's tests delete it before they
		// delete the board.
		id := ids.New()
		return Statement{id, "INSERT INTO node (id, workspace_id, project_id, board_id, type, kind, text, x, y) " +
			`VALUES ($1, $2, $3, $4, 'element', 'dialogue', '{"type": "doc", "content": [{"type": "paragraph"}]}', ` +
			"240, 120)", []any{id, w.ID, w.Rows["project"], w.Rows["board"]}}, nil
	}},
	{Name: "connection", Insert: func(_ context.Context, _ Querier, w *Workspace) (Statement, error) {
		// A connection from the workspace's node to itself, as many as the
		// harness adds, which is in the trash with the node while the node
		// is, so the trash's tests delete it before they delete the node.
		id := ids.New()
		return Statement{id, "INSERT INTO connection (id, workspace_id, project_id, board_id, from_node_id, " +
			`to_node_id, "order", label) VALUES ($1, $2, $3, $4, $5, $5, 'V', ` +
			`'{"type": "doc", "content": [{"type": "paragraph"}]}')`,
			[]any{id, w.ID, w.Rows["project"], w.Rows["board"], w.Rows["node"]}}, nil
	}},
	{Name: "branch_output", Insert: func(_ context.Context, _ Querier, w *Workspace) (Statement, error) {
		// An output of the workspace's node, as many as the harness adds,
		// which is in the trash with the node while the node is, so the
		// trash's tests delete it before they delete the node. The commands
		// hold an output to a branch, which the database does not.
		id := ids.New()
		return Statement{id, "INSERT INTO branch_output (id, workspace_id, project_id, board_id, node_id, kind, " +
			`"order") VALUES ($1, $2, $3, $4, $5, 'elseif', 'V')`,
			[]any{id, w.ID, w.Rows["project"], w.Rows["board"], w.Rows["node"]}}, nil
	}},
	{Name: "expression", Insert: func(ctx context.Context, admin Querier, w *Workspace) (Statement, error) {
		// An owner has one expression at each of its fields, so each is at a
		// field of the workspace's node, connection, or output that has none
		// yet, rather than of an owner of its own, which the trash's tests of
		// the owners' views would find, as a compiled source that names a
		// variable that the project does not have.
		var owner, field string
		if err := admin.QueryRow(ctx, `SELECT f.owner, f.field FROM (VALUES
				(1, 'node_id', 'on_enter', $1::uuid), (2, 'node_id', 'on_exit', $1),
				(3, 'connection_id', 'condition', $2), (4, 'connection_id', 'on_traverse', $2),
				(5, 'branch_output_id', 'condition', $3)) f (n, owner, field, id)
			WHERE NOT EXISTS (SELECT 1 FROM expression e WHERE e.field = f.field
				AND f.id IN (e.node_id, e.connection_id, e.branch_output_id))
			ORDER BY f.n LIMIT 1`, w.Rows["node"], w.Rows["connection"], w.Rows["branch_output"]).Scan(&owner,
			&field); err != nil {
			return Statement{}, err
		}
		reads := "script"
		if field == "condition" {
			reads = "condition"
		}
		id := ids.New()
		return Statement{id, "INSERT INTO expression (id, workspace_id, project_id, board_id, " + owner + ", " +
			"field, context, source, error) VALUES ($1, $2, $3, $4, $5, $6, $7, 'x', " +
			`'{"code": "unknown_identifier", "start": 0, "end": 1}')`,
			[]any{id, w.ID, w.Rows["project"], w.Rows["board"], w.Rows[map[string]string{"node_id": "node",
				"connection_id": "connection", "branch_output_id": "branch_output"}[owner]], field, reads}}, nil
	}},
	{Name: "expression_name", ID: "ref", Insert: func(_ context.Context, _ Querier, w *Workspace) (Statement,
		error) {
		// A name of the workspace's expression, of a UUID of its own, as many
		// as the harness adds. The expression's refs do not hold it, as
		// compiling the expression would keep them, but the harness reads the
		// names alone.
		ref := ids.New()
		return Statement{ref, "INSERT INTO expression_name (workspace_id, project_id, expression_id, board_id, " +
			"ref) VALUES ($1, $2, $3, $4, $5)", []any{w.ID, w.Rows["project"], w.Rows["expression"], w.Rows["board"],
			ref}}, nil
	}},
	{Name: "expression_ref", ID: "target_id", Insert: func(_ context.Context, _ Querier, w *Workspace) (Statement,
		error) {
		// A row of the index of the workspace's expression, of a variable of
		// its own, as many as the harness adds. The expression names none, as
		// compiling it would index, but the harness reads the rows alone.
		target := ids.New()
		return Statement{target, "INSERT INTO expression_ref (workspace_id, project_id, expression_id, board_id, " +
			"target_kind, target_id, access) VALUES ($1, $2, $3, $4, 'variable', $5, 'read')", []any{w.ID,
			w.Rows["project"], w.Rows["expression"], w.Rows["board"], target}}, nil
	}},
	{Name: "variable", Insert: func(_ context.Context, _ Querier, w *Workspace) (Statement, error) {
		// A global variable's key is unique within its project, which its ID
		// makes. It belongs to no board, so that the trash's tests delete it
		// on its own, whatever becomes of the workspace's board.
		id := ids.New()
		args := []any{id, w.ID, w.Rows["project"], "met_" + strings.ReplaceAll(id.String(), "-", "")}
		return Statement{id, `INSERT INTO variable (id, workspace_id, project_id, scope, "order", key, type, ` +
			"default_value) VALUES ($1, $2, $3, 'global', 'V', $4, 'bool', 'false')", args}, nil
	}},
	{Name: "validation_mute", Insert: func(ctx context.Context, admin Querier, w *Workspace) (Statement, error) {
		// An owner has one mute of a code at most, so each mute is of a code
		// of the workspace's node, connection, output, variable, or board
		// that has none yet, rather than of an owner of its own, which the
		// trash's tests of the owners' views would find.
		var owner, code string
		if err := admin.QueryRow(ctx, `SELECT f.owner, f.code FROM (VALUES
				(1, 'node_id', 'node_unreachable', $1::uuid), (2, 'node_id', 'node_empty', $1),
				(3, 'node_id', 'dead_end', $1), (4, 'node_id', 'speaker_missing', $1),
				(5, 'connection_id', 'choice_duplicate_label', $2), (6, 'connection_id', 'connection_self', $2),
				(7, 'connection_id', 'connection_parallel', $2), (8, 'branch_output_id', 'port_unconnected', $3),
				(9, 'variable_id', 'var_read_never_written', $4), (10, 'variable_id', 'var_written_never_read', $4),
				(11, 'board_id', 'board_entry_missing', $5)) f (n, owner, code, id)
			WHERE NOT EXISTS (SELECT 1 FROM validation_mute m WHERE m.code = f.code
				AND f.id IN (m.node_id, m.connection_id, m.branch_output_id, m.variable_id, m.board_id))
			ORDER BY f.n LIMIT 1`, w.Rows["node"], w.Rows["connection"], w.Rows["branch_output"], w.Rows["variable"],
			w.Rows["board"]).Scan(&owner, &code); err != nil {
			return Statement{}, err
		}
		id := ids.New()
		return Statement{id, "INSERT INTO validation_mute (id, workspace_id, project_id, " + owner + ", code, reason) " +
			"VALUES ($1, $2, $3, $4, $5, 'Meant so')", []any{id, w.ID, w.Rows["project"],
			w.Rows[map[string]string{"node_id": "node", "connection_id": "connection",
				"branch_output_id": "branch_output", "variable_id": "variable", "board_id": "board"}[owner]], code}}, nil
	}},
	{Name: "component_type", Insert: func(_ context.Context, _ Querier, w *Workspace) (Statement, error) {
		// A type's key is unique within its project, which its ID makes.
		id := ids.New()
		return Statement{id, "INSERT INTO component_type (id, workspace_id, project_id, key, name) " +
			"VALUES ($1, $2, $3, $4, 'Character')", []any{id, w.ID, w.Rows["project"], key("character", id)}}, nil
	}},
	{Name: "attribute_def", Insert: func(_ context.Context, _ Querier, w *Workspace) (Statement, error) {
		// An attribute definition's key is unique within its type, which its
		// ID makes.
		id := ids.New()
		return Statement{id, `INSERT INTO attribute_def (id, workspace_id, project_id, component_type_id, "order", ` +
			"key, name, type, default_value) VALUES ($1, $2, $3, $4, 'V', $5, 'Trust', 'int', '0')",
			[]any{id, w.ID, w.Rows["project"], w.Rows["component_type"], key("trust", id)}}, nil
	}},
	{Name: "component", Insert: func(_ context.Context, _ Querier, w *Workspace) (Statement, error) {
		// A component's key is unique within its project, which its ID makes.
		// It is at the top, so that the trash's tests delete it on its own,
		// whatever becomes of the workspace's folder.
		id := ids.New()
		return Statement{id, `INSERT INTO component (id, workspace_id, project_id, component_type_id, "order", key, ` +
			"name) VALUES ($1, $2, $3, $4, 'V', $5, 'Elara')", []any{id, w.ID, w.Rows["project"],
			w.Rows["component_type"], key("elara", id)}}, nil
	}},
	{Name: "attribute_value", Insert: func(ctx context.Context, admin Querier, w *Workspace) (Statement, error) {
		// A component holds one value of a definition. The workspace's first
		// value is its component's, so that the trash's tests find the
		// workspace's one component, and each one after it, as the harness
		// tries to add, is a new component's, which it adds.
		component := w.Rows["component"]
		if _, seeded := w.Rows["attribute_value"]; seeded {
			component = ids.New()
			if _, err := admin.Exec(ctx, `INSERT INTO component (id, workspace_id, project_id, component_type_id, `+
				`"order", key, name) VALUES ($1, $2, $3, $4, 'V', $5, 'Mira')`, component, w.ID, w.Rows["project"],
				w.Rows["component_type"], key("mira", component)); err != nil {
				return Statement{}, fmt.Errorf("add a component: %w", err)
			}
		}
		id := ids.New()
		return Statement{id, "INSERT INTO attribute_value (id, workspace_id, project_id, component_id, " +
			"attribute_def_id, value) VALUES ($1, $2, $3, $4, $5, '3')", []any{id, w.ID, w.Rows["project"], component,
			w.Rows["attribute_def"]}}, nil
	}},
	{Name: "search_entry", Insert: func(ctx context.Context, admin Querier, w *Workspace) (Statement, error) {
		// An entity has one document, so each is of the workspace's node,
		// connection, variable, or component that has none yet, rather than of
		// an entity of its own, which the trash's tests of the entities' views
		// would find.
		var kind string
		var entity ids.UUID
		if err := admin.QueryRow(ctx, `SELECT f.kind, f.id FROM (VALUES
				(1, 'node', $1::uuid), (2, 'connection', $2), (3, 'variable', $3), (4, 'component', $4)) f (n, kind, id)
			WHERE NOT EXISTS (SELECT 1 FROM search_entry e WHERE e.kind = f.kind AND e.entity_id = f.id)
			ORDER BY f.n LIMIT 1`, w.Rows["node"], w.Rows["connection"], w.Rows["variable"],
			w.Rows["component"]).Scan(&kind, &entity); err != nil {
			return Statement{}, err
		}
		row := ids.New()
		return Statement{row, "INSERT INTO search_entry (id, workspace_id, project_id, kind, entity_id, title, body, " +
			"version) VALUES ($1, $2, $3, $4, $5, 'The Lantern', 'Light it before the storm.', 1)",
			[]any{row, w.ID, w.Rows["project"], kind, entity}}, nil
	}},
	{Name: "search_state", Insert: func(ctx context.Context, admin Querier, w *Workspace) (Statement, error) {
		// A project has one state, so each is of a project of its own, which
		// it adds first.
		id := ids.New()
		if _, err := admin.Exec(ctx, "INSERT INTO project (id, workspace_id, name) VALUES ($1, $2, 'Harbour')", id,
			w.ID); err != nil {
			return Statement{}, err
		}
		row := ids.New()
		return Statement{row, "INSERT INTO search_state (id, workspace_id, project_id, version) VALUES ($1, $2, $3, 1)",
			[]any{row, w.ID, id}}, nil
	}},
	{Name: "usage_counter", Insert: func(_ context.Context, _ Querier, w *Workspace) (Statement, error) {
		// A workspace has one counter of a key.
		id := ids.New()
		return Statement{id, "INSERT INTO usage_counter (id, workspace_id, key, value) VALUES ($1, $2, $3, 7)",
			[]any{id, w.ID, usageKey()}}, nil
	}},
	{Name: "usage_delta", Insert: func(_ context.Context, _ Querier, w *Workspace) (Statement, error) {
		// Two members, which the workspace's one membership does not match,
		// so that a recount of its members finds it drifted.
		id := ids.New()
		return Statement{id, "INSERT INTO usage_delta (id, workspace_id, key, delta) VALUES ($1, $2, 'members', 2)",
			[]any{id, w.ID}}, nil
	}},
}

// key returns a key of an identifier's form, unique by the ID id: the word
// prefix, an underscore, and id's hexadecimal digits.
func key(prefix string, id ids.UUID) string {
	return prefix + "_" + strings.ReplaceAll(id.String(), "-", "")
}

// usageKey returns a random key of usage, of lowercase letters as the keys
// are, so that the counters that the harness adds to one workspace do not
// share one.
func usageKey() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = 'a' + b[i]%26
	}
	return "isolation_" + string(b)
}

// seq returns a random positive seq, so that the operations that the harness
// adds to one project do not share one.
func seq() int64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return int64(binary.BigEndian.Uint64(b[:])>>2) + 1
}

// TokenHash returns a random 32-byte value, as the SHA-256 hash of a secret
// is.
func TokenHash() []byte {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return b
}

// NewUser adds a user through q, and returns its ID.
func NewUser(ctx context.Context, q Querier) (ids.UUID, error) {
	id := ids.New()
	if _, err := q.Exec(ctx, `INSERT INTO "user" (id, email, display_name) VALUES ($1, $2, 'Isolation')`,
		id, id.String()+"@example.com"); err != nil {
		return ids.UUID{}, fmt.Errorf("add a user: %w", err)
	}
	return id, nil
}

// Workspace is one of the fixture's workspaces.
type Workspace struct {
	ID ids.UUID
	// UserID is a user that the fixture added with the workspace, which the
	// harness's transactions act for, and which owns its API token.
	UserID ids.UUID
	// Rows hold the ID of the row that the fixture added to each table, by the
	// table's name.
	Rows map[string]ids.UUID
}

// Scope is the scope of a request transaction of the workspace.
func (w *Workspace) Scope() database.Scope {
	return database.Scope{WorkspaceID: w.ID, UserID: w.UserID}
}

// Fixture is two workspaces, A and B, with a row of each of its tables.
type Fixture struct {
	A, B *Workspace
	// DB is the application login's pool.
	DB *database.DB

	admin  *pgx.Conn
	tables []Table
}

// New seeds the database d with the workspaces A and B, each with a row in
// every table of tables, which come in an order in which each table's rows
// need only rows of the tables before it.
func New(t testing.TB, d *dbtest.Database, tables []Table) *Fixture {
	t.Helper()
	ctx := context.Background()
	f := &Fixture{DB: d.Open(t), admin: d.Admin(t), tables: tables}
	for _, w := range []**Workspace{&f.A, &f.B} {
		ws, err := f.seed(ctx)
		if err != nil {
			t.Fatalf("seed a workspace: %v", err)
		}
		*w = ws
	}
	return f
}

// Admin is the superuser's connection, which row-level security does not
// hold.
func (f *Fixture) Admin() *pgx.Conn {
	return f.admin
}

// seed adds a workspace with a user and a row of each of the fixture's
// tables.
func (f *Fixture) seed(ctx context.Context) (*Workspace, error) {
	user, err := NewUser(ctx, f.admin)
	if err != nil {
		return nil, err
	}
	w := &Workspace{ID: ids.New(), UserID: user, Rows: map[string]ids.UUID{}}
	if _, err := f.admin.Exec(ctx, "INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Isolation', $2, 'unlimited')",
		w.ID, "isolation-"+w.ID.String()); err != nil {
		return nil, fmt.Errorf("add a workspace: %w", err)
	}
	for _, table := range f.tables {
		s, err := table.Insert(ctx, f.admin, w)
		if err == nil {
			_, err = f.admin.Exec(ctx, s.SQL, s.Args...)
		}
		if err != nil {
			return nil, fmt.Errorf("add a row to %s: %w", table.Name, err)
		}
		w.Rows[table.Name] = s.ID
	}
	return w, nil
}

// Op is a repository operation that the harness tries.
type Op struct {
	Name string
	// Run runs the operation in tx on the rows of the workspace target, as
	// a repository called with target's ID, or the ID of one of its rows,
	// would, and returns the IDs of the rows that it read, changed, or added.
	// If it succeeds on another workspace's rows, its transaction commits, so
	// that the harness sees what it changed; it must touch no row but
	// target's.
	Run func(ctx context.Context, tx pgx.Tx, target *Workspace) ([]ids.UUID, error)
	// Denied says that row-level security must refuse the operation on
	// another workspace's rows with an error, rather than find none of them,
	// as it refuses an insert.
	Denied bool
	// Blind says that Run cannot tell which rows it changed, as an UPDATE or
	// DELETE without a WHERE or RETURNING clause cannot. The harness then
	// finds them itself, as the target's rows that Run's transaction holds
	// locked, as it holds every row that it updated or deleted, and rolls the
	// transaction back rather than commit it: such an operation changes its
	// own workspace's rows too, which the operations that the harness tries
	// next need. Run's IDs are not used.
	Blind bool
}

// Check tries op on workspace A's rows, where it must work, and then on
// workspace B's, from a transaction of A, where row-level security must keep
// it from reading, changing, or adding any of B's rows.
func (f *Fixture) Check(t testing.TB, op Op) {
	t.Helper()
	f.check(t, op, nil, false)
}

// errRollback rolls back the transaction that tries an operation on the
// workspace's own rows.
var errRollback = errors.New("rolled back")

// check is Check. prepare, if set, is called with the target before its
// rows are read for comparison, to add the rows that the operation needs.
// refused says that the application login lacks the privilege for the
// operation, so that it must fail on every workspace's rows.
func (f *Fixture) check(t testing.TB, op Op, prepare func(target *Workspace) error, refused bool) {
	t.Helper()
	ctx := context.Background()

	if prepare != nil {
		if err := prepare(f.A); err != nil {
			t.Fatalf("%s: %v", op.Name, err)
		}
	}
	var own []ids.UUID
	err := f.DB.InTx(ctx, f.A.Scope(), func(tx pgx.Tx) error {
		var err error
		if own, err = f.run(ctx, t, tx, op, f.A); err == nil {
			err = errRollback
		}
		return err
	})
	switch {
	case refused:
		if !isDenied(err) {
			t.Errorf("%s: on its own workspace's rows: %v, want the application login's privileges to refuse it",
				op.Name, err)
		}
	case !errors.Is(err, errRollback):
		t.Errorf("%s: fails on its own workspace's rows: %v", op.Name, err)
	case len(own) == 0:
		t.Errorf("%s: finds none of its own workspace's rows, so it shows nothing about another's", op.Name)
	}

	if prepare != nil {
		if err := prepare(f.B); err != nil {
			t.Fatalf("%s: %v", op.Name, err)
		}
	}
	before, rows := f.snapshot(t, f.B)
	var got []ids.UUID
	err = f.DB.InTx(ctx, f.A.Scope(), func(tx pgx.Tx) error {
		var err error
		if got, err = f.run(ctx, t, tx, op, f.B); err == nil && op.Blind {
			err = errRollback
		}
		return err
	})
	if errors.Is(err, errRollback) {
		err = nil
	}
	if leaked := slices.DeleteFunc(slices.Clone(got), func(id ids.UUID) bool { return !slices.Contains(rows, id) }); len(leaked) > 0 {
		t.Errorf("%s: from another workspace's transaction, reaches %d of the workspace's rows", op.Name, len(leaked))
	}
	if (op.Denied || refused) && !isDenied(err) {
		t.Errorf("%s: from another workspace's transaction: %v, want row-level security to refuse it", op.Name, err)
	}
	if after, _ := f.snapshot(t, f.B); after != before {
		t.Errorf("%s: from another workspace's transaction, changes the workspace's rows", op.Name)
	}
}

// run runs op in tx on the rows of target, and returns the IDs of the rows
// that it read, changed, or added: for a blind operation, those of target's
// rows that tx holds locked.
func (f *Fixture) run(ctx context.Context, t testing.TB, tx pgx.Tx, op Op, target *Workspace) ([]ids.UUID, error) {
	t.Helper()
	got, err := op.Run(ctx, tx, target)
	if err != nil || !op.Blind {
		return got, err
	}
	return f.locked(t, target), nil
}

// locked returns, as the superuser, the IDs of the rows of w in the
// fixture's tables that another transaction holds locked, as one holds each
// row that it updated or deleted until it ends. The superuser tries to lock
// every row of w, and skips, rather than waits for, those it cannot lock at
// once. Its lock, FOR NO KEY UPDATE, does not conflict with the one that a
// foreign key check takes on the row that a new row refers to, so that a row
// that the transaction only refers to does not count.
func (f *Fixture) locked(t testing.TB, w *Workspace) []ids.UUID {
	t.Helper()
	var all []ids.UUID
	for _, table := range f.tables {
		ident := pgx.Identifier{table.Name}.Sanitize()
		id := table.idColumn()
		rows, err := f.admin.Query(context.Background(), "WITH free AS (SELECT "+id+" FROM "+ident+
			" WHERE workspace_id = $1 FOR NO KEY UPDATE SKIP LOCKED) "+
			"SELECT "+id+" FROM "+ident+" WHERE workspace_id = $1 AND "+id+" NOT IN (SELECT "+id+" FROM free)", w.ID)
		var got []ids.UUID
		if err == nil {
			got, err = pgx.CollectRows(rows, pgx.RowTo[ids.UUID])
		}
		if err != nil {
			t.Fatalf("read the locked rows of %s: %v", table.Name, err)
		}
		all = append(all, got...)
	}
	return all
}

// isDenied reports whether err is PostgreSQL's refusal for want of a
// privilege, which is also how row-level security refuses a row.
func isDenied(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42501"
}

// snapshot returns, as the superuser, a digest of every row of w in the
// fixture's tables, and their IDs.
func (f *Fixture) snapshot(t testing.TB, w *Workspace) (string, []ids.UUID) {
	t.Helper()
	ctx := context.Background()
	var digest string
	var all []ids.UUID
	for _, table := range f.tables {
		ident := pgx.Identifier{table.Name}.Sanitize()
		var d string
		if err := f.admin.QueryRow(ctx, "SELECT coalesce(md5(string_agg(t::text, ',' ORDER BY t::text)), '') FROM "+
			ident+" t WHERE workspace_id = $1", w.ID).Scan(&d); err != nil {
			t.Fatalf("read %s: %v", table.Name, err)
		}
		rows, err := f.admin.Query(ctx, "SELECT "+table.idColumn()+" FROM "+ident+" WHERE workspace_id = $1", w.ID)
		var got []ids.UUID
		if err == nil {
			got, err = pgx.CollectRows(rows, pgx.RowTo[ids.UUID])
		}
		if err != nil {
			t.Fatalf("read %s: %v", table.Name, err)
		}
		digest += table.Name + ":" + d + ";"
		all = append(all, got...)
	}
	return digest, all
}

// CheckTables tries, on every table of the fixture, what a repository with a
// broken filter would do: read, update, delete, and add rows of another
// workspace's ID, read, update, and delete rows without a filter, and move
// rows of its own workspace into another by changing their workspace_id.
// Operations that the application login has no privilege for must fail on
// its own workspace's rows too.
func (f *Fixture) CheckTables(t testing.TB) {
	t.Helper()
	for _, table := range f.tables {
		ident, id := pgx.Identifier{table.Name}.Sanitize(), table.idColumn()
		can := f.privileges(t, table.Name)
		query := func(sql string, filtered bool) func(context.Context, pgx.Tx, *Workspace) ([]ids.UUID, error) {
			return func(ctx context.Context, tx pgx.Tx, target *Workspace) ([]ids.UUID, error) {
				var args []any
				if filtered {
					args = append(args, target.ID)
				}
				rows, err := tx.Query(ctx, sql, args...)
				if err != nil {
					return nil, err
				}
				return pgx.CollectRows(rows, pgx.RowTo[ids.UUID])
			}
		}
		blind := func(sql string, args ...any) func(context.Context, pgx.Tx, *Workspace) ([]ids.UUID, error) {
			return func(ctx context.Context, tx pgx.Tx, _ *Workspace) ([]ids.UUID, error) {
				_, err := tx.Exec(ctx, sql, args...)
				return nil, err
			}
		}
		for _, c := range []struct {
			op        Op
			privilege string
		}{
			{Op{Name: table.Name + ": read by workspace",
				Run: query("SELECT "+id+" FROM "+ident+" WHERE workspace_id = $1", true)}, "SELECT"},
			{Op{Name: table.Name + ": read without a filter",
				Run: query("SELECT "+id+" FROM "+ident, false)}, "SELECT"},
			// A WHERE or RETURNING clause that reads the table's columns, as
			// those of the update and the delete by workspace below do, holds
			// the rows that the statement reaches to the table's SELECT
			// policies too, which would hide behind them a looser policy for
			// updates or deletes, such as one that lets a transaction delete
			// every workspace's rows. These statements read no column, so that
			// the policies for updates and deletes are all that holds them,
			// and the harness finds the rows that they reach by their locks
			// (Op.Blind). The update sets workspace_id to A's, the workspace of
			// the transaction, which a policy's check of new rows lets
			// through, so that it would take another workspace's rows into A.
			// They come before the operations that commit what they change if
			// row-level security fails to hold them, so that they find every
			// workspace's rows as the fixture seeded them.
			{Op{Name: table.Name + ": update without a filter", Blind: true,
				Run: blind("UPDATE "+ident+" SET workspace_id = $1", f.A.ID)}, "UPDATE"},
			{Op{Name: table.Name + ": delete without a filter", Blind: true,
				Run: blind("DELETE FROM " + ident)}, "DELETE"},
			{Op{Name: table.Name + ": update by workspace",
				Run: query("UPDATE "+ident+" SET workspace_id = workspace_id WHERE workspace_id = $1 RETURNING "+id, true)},
				"UPDATE"},
			{Op{Name: table.Name + ": delete by workspace",
				Run: query("DELETE FROM "+ident+" WHERE workspace_id = $1 RETURNING "+id, true)}, "DELETE"},
			// Unlike the others, this operation changes the rows that the
			// transaction sees, A's, rather than the target's: it moves them
			// into the target, which adds rows to the target as surely as an
			// insert does, so the policy's check of new rows must refuse it in
			// the same way. The update above, which keeps workspace_id, never
			// reaches that check. The statement reads no column, as a WHERE
			// or RETURNING clause would, which would hold the new rows to the
			// table's SELECT policies too, so that the check of new rows is
			// all that refuses it. On A's own rows, it moves them nowhere.
			{Op{Name: table.Name + ": move into a workspace", Denied: true,
				Run: func(ctx context.Context, tx pgx.Tx, target *Workspace) ([]ids.UUID, error) {
					moved, err := query("SELECT "+id+" FROM "+ident, false)(ctx, tx, target)
					if err == nil {
						_, err = tx.Exec(ctx, "UPDATE "+ident+" SET workspace_id = $1", target.ID)
					}
					return moved, err
				}}, "UPDATE"},
		} {
			f.check(t, c.op, nil, !can[c.privilege])
		}

		// The statement is prepared before the harness reads the target's
		// rows, so that the rows it adds through the superuser's connection
		// do not count as rows that the operation added.
		var s Statement
		prepare := func(w *Workspace) error {
			var err error
			s, err = table.Insert(context.Background(), f.admin, w)
			return err
		}
		insert := Op{Name: table.Name + ": insert", Denied: true,
			Run: func(ctx context.Context, tx pgx.Tx, _ *Workspace) ([]ids.UUID, error) {
				_, err := tx.Exec(ctx, s.SQL, s.Args...)
				return []ids.UUID{s.ID}, err
			}}
		f.check(t, insert, prepare, !can["INSERT"])
	}
}

// privileges returns which of SELECT, INSERT, UPDATE, and DELETE the
// application's role has on the table.
func (f *Fixture) privileges(t testing.TB, table string) map[string]bool {
	t.Helper()
	can := map[string]bool{}
	for _, p := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
		var ok bool
		if err := f.admin.QueryRow(context.Background(), "SELECT has_table_privilege('storycreep_app', $1::regclass, $2)",
			pgx.Identifier{table}.Sanitize(), p).Scan(&ok); err != nil {
			t.Fatalf("read the privileges on %s: %v", table, err)
		}
		can[p] = ok
	}
	return can
}
