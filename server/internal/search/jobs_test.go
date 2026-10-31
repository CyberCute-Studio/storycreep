package search_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/component"
	"github.com/CyberCute-Studio/storycreep/server/internal/connection"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
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
// the documents made, as content that was there before migration 00029,
// with its owner, the projects' service, which creates others of the
// workspace through that pipeline, and a pipeline of the commands with the
// derivation.
type unindexed struct {
	db        *database.DB
	admin     *pgx.Conn
	workspace ids.UUID
	project   ids.UUID
	owner     ids.UUID
	projects  *project.Service
	indexed   *commands.Pipeline
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
	registry := func(indexed bool) *commands.Registry {
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
		if indexed {
			search.Register(reg)
		}
		return reg
	}
	ent := entitlements.New(entitlements.Options{Meter: usage.Meter{}})
	pipeline := commands.New(db, commands.Options{Registry: registry(false), Entitlements: ent})
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
	return unindexed{db: db, admin: admin, workspace: ws, project: projectID, owner: owner, projects: projects,
		indexed: commands.New(db, commands.Options{Registry: registry(true), Entitlements: ent})}
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
// passes over a project whose lock a transaction holds for longer than it
// waits, until a later run, and waits out a batch that holds it for less; it
// writes again the documents that a migration marks; a run that stops once
// its time runs out while it writes documents, or records projects, has the
// job run again soon, even if another project's index failed, which it warns
// of, and whose error a run that reaches every project returns; it leaves
// alone the documents and the state of a newer version, even as it records
// its own; it comes back to a project whose state a newer version recorded
// once an older version's batch writes a document of it; and it lists no
// project whose time in the trash has passed. It changes how many documents
// a transaction writes, and how long the job waits for a lock, so it runs
// alone.
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

	// A transaction holds the project's lock for longer than the job waits
	// for it: the job passes over the project.
	lock, err := u.admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Exec(ctx, "SELECT 1 FROM project WHERE id = $1 FOR NO KEY UPDATE", u.project); err != nil {
		t.Fatal(err)
	}
	search.SetReindexLockWait(t, 100*time.Millisecond)
	if n, err := search.Reindex(ctx, u.db); err != nil || n != 0 {
		t.Errorf("Reindex while a transaction holds the lock for long wrote %d documents, %v, want none", n, err)
	}

	// A batch holds it for less, as one that applies does: the job waits it
	// out, and writes the project's documents, rather than passing over the
	// project until its next period.
	search.SetReindexLockWait(t, time.Minute)
	released := make(chan error, 1)
	go func() {
		time.Sleep(200 * time.Millisecond)
		released <- lock.Rollback(ctx)
	}()
	n, err := search.Reindex(ctx, u.db)
	if err := <-released; err != nil {
		t.Fatal(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(entities) {
		t.Errorf("Reindex wrote %d documents, want one of each of the %d entities, past the batch that held "+
			"the lock", n, entities)
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

	// A run of the job that stops once its time runs out while it writes
	// documents has the job run again soon, without counting an attempt
	// (jobs.Snooze); one that writes none, or that has the time to write
	// them all, does not.
	if _, err := u.admin.Exec(ctx, `UPDATE search_entry SET version = 0 WHERE project_id = $1`,
		u.project); err != nil {
		t.Fatal(err)
	}
	if _, err := u.admin.Exec(ctx, `DELETE FROM search_state WHERE project_id = $1`, u.project); err != nil {
		t.Fatal(err)
	}
	var logged bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logged, nil))
	if err := search.Work(ctx, u.db, func() bool { return false }, nil); err != nil {
		t.Errorf("a run without the time to write a document returned %v, want nil", err)
	}
	untilWritten := func() bool {
		return u.count(t, "SELECT count(*) FROM search_entry WHERE project_id = $1 AND version > 0") == 0
	}
	if err := search.Work(ctx, u.db, untilWritten, log); !errors.As(err, new(*river.JobSnoozeError)) {
		t.Errorf("a run whose time ran out once it wrote a transaction's documents returned %v, want a snooze", err)
	}
	if err := search.Work(ctx, u.db, func() bool { return true }, nil); err != nil {
		t.Errorf("a run with the time to write the rest returned %v, want nil", err)
	}
	u.wantIndexed(t)

	// So does a run whose time runs out once it has recorded that a project
	// lacks no document, having written none, as of a project whose
	// documents the derivation wrote, which has no state until the job
	// records it.
	states := func(projectID ids.UUID) int {
		var n int
		if err := u.admin.QueryRow(ctx, "SELECT count(*) FROM search_state WHERE project_id = $1",
			projectID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if _, err := u.admin.Exec(ctx, `DELETE FROM search_state WHERE project_id = $1`, u.project); err != nil {
		t.Fatal(err)
	}
	if err := search.Work(ctx, u.db, func() bool { return states(u.project) == 0 }, log); !errors.As(err,
		new(*river.JobSnoozeError)) {
		t.Errorf("a run whose time ran out once it recorded a project returned %v, want a snooze", err)
	}
	if logged.Len() > 0 {
		t.Errorf("the runs that snoozed without an error warned: %s", logged.String())
	}
	u.wantIndexed(t)

	// And so does a run whose time runs out once it has indexed a project,
	// though the index of another failed, as of a stored text that
	// richtext.Parse refuses, which no command stores; the first run that has
	// the time to reach every project returns the failure.
	var nodeID ids.UUID
	var text []byte
	if err := u.admin.QueryRow(ctx, `SELECT id, text FROM node WHERE project_id = $1 AND text IS NOT NULL
		ORDER BY id LIMIT 1`, u.project).Scan(&nodeID, &text); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{`UPDATE node SET text = '{"type": "not-a-doc"}' WHERE id = $1`,
		`UPDATE search_entry SET version = 0 WHERE node_id = $1`,
		`DELETE FROM search_state WHERE project_id = (SELECT project_id FROM node WHERE id = $1)`} {
		if _, err := u.admin.Exec(ctx, s, nodeID); err != nil {
			t.Fatal(err)
		}
	}
	created, err := u.projects.Create(ctx, u.owner, u.workspace, project.Settings{Name: "Empty"}, audit.Client{})
	if err != nil {
		t.Fatal(err)
	}
	other, err := ids.Decode(ids.Project, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Compare(other[:], u.project[:]) <= 0 {
		t.Fatal("the new project's UUID sorts before the first's, which the job must reach first")
	}
	if err := search.Work(ctx, u.db, func() bool { return states(other) == 0 }, log); !errors.As(err,
		new(*river.JobSnoozeError)) {
		t.Errorf("a run whose time ran out once it indexed a project, after another's index failed, returned %v, "+
			"want a snooze", err)
	}
	// The snoozed attempt keeps no error, so the run warns of the failure.
	if !strings.Contains(logged.String(), u.project.String()) {
		t.Errorf("the run that snoozed past the failure warned %q, want the failing project named", logged.String())
	}
	if err := search.Work(ctx, u.db, func() bool { return true }, nil); err == nil ||
		errors.As(err, new(*river.JobSnoozeError)) {
		t.Errorf("a run with the time to reach a project whose index fails returned %v, want its error", err)
	}
	if _, err := u.admin.Exec(ctx, `UPDATE node SET text = $2 WHERE id = $1`, nodeID, text); err != nil {
		t.Fatal(err)
	}
	if n, err := search.Reindex(ctx, u.db); err != nil || n != 1 {
		t.Errorf("Reindex once the text is mended wrote %d documents, %v, want its node's", n, err)
	}
	u.wantIndexed(t)

	// The documents and the state of a newer version, which a newer server
	// writes while a rolling upgrade replaces this one, are this version's
	// job's to leave alone, rather than to write again of its own.
	for _, s := range []string{`UPDATE search_entry SET version = version + 1 WHERE project_id = $1`,
		`UPDATE search_state SET version = version + 1 WHERE project_id = $1`} {
		if _, err := u.admin.Exec(ctx, s, u.project); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := search.Reindex(ctx, u.db); err != nil || n != 0 {
		t.Errorf("Reindex of a newer version's documents wrote %d, %v, want none", n, err)
	}
	// This version's job, which found the project before the newer one
	// recorded it, keeps the newer state as it records its own.
	ws := pgtype.UUID{Bytes: u.workspace, Valid: true}
	if err := queries.New(u.admin).SetSearchState(ctx, queries.SetSearchStateParams{WorkspaceID: ws,
		ProjectID: pgtype.UUID{Bytes: u.project, Valid: true}, Version: search.Version}); err != nil {
		t.Fatal(err)
	}
	if got := u.count(t, fmt.Sprintf(`SELECT (SELECT count(*) FROM search_entry WHERE project_id = $1
		AND version = %[1]d) + (SELECT count(*) FROM search_state WHERE project_id = $1 AND version = %[1]d)`,
		search.Version+1)); got != entities+1 {
		t.Errorf("after Reindex and this version's record, %d documents and states are of the newer version, "+
			"want all %d", got, entities+1)
	}
	for _, s := range []string{`UPDATE search_entry SET version = version - 1 WHERE project_id = $1`,
		`UPDATE search_state SET version = version - 1 WHERE project_id = $1`} {
		if _, err := u.admin.Exec(ctx, s, u.project); err != nil {
			t.Fatal(err)
		}
	}

	// A batch of a version of the server older than the one that recorded
	// the project's state, as a replica that a rolling upgrade has yet to
	// replace applies, deletes the state, so that the newer version's job
	// comes back to the project and writes again the document that it wrote.
	if _, err := u.admin.Exec(ctx, `UPDATE search_state SET version = version + 1 WHERE project_id = $1`,
		u.project); err != nil {
		t.Fatal(err)
	}
	var variableID ids.UUID
	if err := u.admin.QueryRow(ctx, `SELECT id FROM variable WHERE project_id = $1 ORDER BY id LIMIT 1`,
		u.project).Scan(&variableID); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]string{"id": ids.Encode(ids.Variable, variableID),
		"description": "Described by an older server."})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := u.indexed.Apply(ctx, commands.Actor{UserID: u.owner}, u.project,
		[]commands.Command{{ID: ids.New(), Type: variable.Describe, Payload: payload}}); err != nil {
		t.Fatal(err)
	}
	if got := u.count(t, "SELECT count(*) FROM search_state WHERE project_id = $1"); got != 0 {
		t.Errorf("after an older version's batch, the project has %d states, want none", got)
	}
	if n, err := search.Reindex(ctx, u.db); err != nil || n != 0 {
		t.Errorf("Reindex after the batch wrote %d documents, %v, want none, all of its version", n, err)
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
	if listed, err := queries.New(u.admin).ProjectsToIndex(ctx, queries.ProjectsToIndexParams{WorkspaceID: ws,
		Version: search.Version}); err != nil || len(listed) != 0 {
		t.Errorf("the projects to index are %v, %v, want none, not the one past its time in the trash", listed, err)
	}
}
