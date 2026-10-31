package search_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/component"
	"github.com/CyberCute-Studio/storycreep/server/internal/connection"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/dependency"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/expression"
	"github.com/CyberCute-Studio/storycreep/server/internal/fixture"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/node"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/search"
	"github.com/CyberCute-Studio/storycreep/server/internal/search/searchtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
	"github.com/CyberCute-Studio/storycreep/server/internal/validation"
	"github.com/CyberCute-Studio/storycreep/server/internal/variable"
)

func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

// unindexed is a project whose content a pipeline without the derivation of
// the documents made, as content that was there before migration 00029.
type unindexed struct {
	db        *database.DB
	admin     *pgx.Conn
	workspace ids.UUID
	project   ids.UUID
}

// newUnindexed returns a project of a new database with the small fixture's
// content, and no document.
func newUnindexed(t *testing.T) unindexed {
	t.Helper()
	ctx := context.Background()
	d := dbtest.New(t)
	db, admin := d.Open(t), d.Admin(t)
	owner, err := isolationtest.NewUser(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	ws := ids.New()
	if _, err := admin.Exec(ctx, `INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Fixtures',
		'fixtures', 'unlimited')`, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO membership (id, workspace_id, user_id, role)
		VALUES ($1, $2, $3, 'owner')`, ids.New(), ws, owner); err != nil {
		t.Fatal(err)
	}
	reg := commands.NewRegistry()
	project.Register(reg)
	board.Register(reg)
	variable.Register(reg)
	component.Register(reg)
	node.Register(reg)
	connection.Register(reg)
	expression.Register(reg)
	dependency.Register(reg)
	validation.Register(reg)
	ent := entitlements.New(entitlements.Options{Meter: usage.Meter{}})
	pipeline := commands.New(db, commands.Options{Registry: reg, Entitlements: ent})
	projects := project.New(db, project.Options{Entitlements: ent, Pipeline: pipeline, Seed: component.Seed})
	p, err := projects.Create(ctx, owner, ws, project.Settings{Name: "Small"}, audit.Client{})
	if err != nil {
		t.Fatal(err)
	}
	projectID, err := ids.Decode(ids.Project, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	f, err := fixture.Generate(fixture.Small, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Apply(ctx, pipeline, commands.Actor{UserID: owner}, projectID); err != nil {
		t.Fatal(err)
	}
	return unindexed{db: db, admin: admin, workspace: ws, project: projectID}
}

// count returns what the query, of the project, counts.
func (u unindexed) count(t *testing.T, query string) int {
	t.Helper()
	var n int
	if err := u.admin.QueryRow(context.Background(), query, u.project).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// entities returns how many nodes, connections, variables, and components
// the project has, in the trash or not.
func (u unindexed) entities(t *testing.T) int {
	return u.count(t, `SELECT (SELECT count(*) FROM node WHERE project_id = $1)
		+ (SELECT count(*) FROM connection WHERE project_id = $1)
		+ (SELECT count(*) FROM variable WHERE project_id = $1)
		+ (SELECT count(*) FROM component WHERE project_id = $1)`)
}

// wantIndexed fails the test unless the project's documents are what a
// recomputation of them gives, and its state records the current version.
func (u unindexed) wantIndexed(t *testing.T) {
	t.Helper()
	drift, err := searchtest.Drift(context.Background(), u.admin, u.project)
	if err != nil {
		t.Fatal(err)
	}
	if len(drift) > 0 {
		t.Errorf("the documents are not what the content holds: %q", drift[:min(len(drift), 5)])
	}
	if got := u.count(t, "SELECT count(*) FROM search_state WHERE project_id = $1 AND version = "+
		"(SELECT max(version) FROM search_entry WHERE project_id = $1)"); got != 1 {
		t.Errorf("the project has %d states of the documents' version, want 1", got)
	}
}

// TestReindex checks the job of the index: it writes the documents of
// content that has none, in transactions of a bounded number of documents,
// and records the project's state, after which its runs write nothing; it
// passes over a project whose lock a batch holds, until a later run; and it
// writes again the documents that a migration marks. It changes how many
// documents a transaction writes, so it runs alone.
func TestReindex(t *testing.T) {
	ctx := context.Background()
	u := newUnindexed(t)
	entities := u.entities(t)
	search.SetReindexRows(t, 100)
	if entities <= 100 {
		t.Fatalf("the project has %d entities, want more than a transaction of the job writes", entities)
	}
	if got := u.count(t, "SELECT count(*) FROM search_entry WHERE project_id = $1"); got != 0 {
		t.Fatalf("the project has %d documents before the job runs, want none", got)
	}

	// A batch holds the project's lock: the job passes over the project.
	lock, err := u.admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Exec(ctx, "SELECT 1 FROM project WHERE id = $1 FOR NO KEY UPDATE", u.project); err != nil {
		t.Fatal(err)
	}
	if n, err := search.Reindex(ctx, u.db); err != nil || n != 0 {
		t.Errorf("Reindex while a batch holds the lock wrote %d documents, %v, want none", n, err)
	}
	if err := lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	n, err := search.Reindex(ctx, u.db)
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(entities) {
		t.Errorf("Reindex wrote %d documents, want one of each of the %d entities", n, entities)
	}
	u.wantIndexed(t)
	if n, err := search.Reindex(ctx, u.db); err != nil || n != 0 {
		t.Errorf("Reindex of an indexed project wrote %d documents, %v, want none", n, err)
	}

	// A migration marks the documents to be written again, and deletes the
	// project's state.
	if _, err := u.admin.Exec(ctx, `UPDATE search_entry SET version = 0, body = '' WHERE project_id = $1`,
		u.project); err != nil {
		t.Fatal(err)
	}
	if _, err := u.admin.Exec(ctx, `DELETE FROM search_state WHERE project_id = $1`, u.project); err != nil {
		t.Fatal(err)
	}
	if n, err := search.Reindex(ctx, u.db); err != nil || n != int64(entities) {
		t.Errorf("Reindex of marked documents wrote %d, %v, want %d", n, err, entities)
	}
	u.wantIndexed(t)

	// A project in the trash whose time there has passed is the purge's,
	// which the job leaves alone.
	if _, err := u.admin.Exec(ctx, `DELETE FROM search_state WHERE project_id = $1`, u.project); err != nil {
		t.Fatal(err)
	}
	if _, err := u.admin.Exec(ctx, `UPDATE project SET deleted_at = now() - interval '31 days',
		hard_delete_after = now() - interval '1 day' WHERE id = $1`, u.project); err != nil {
		t.Fatal(err)
	}
	if n, err := search.Reindex(ctx, u.db); err != nil || n != 0 {
		t.Errorf("Reindex of a project past its time in the trash wrote %d, %v, want none", n, err)
	}
}
