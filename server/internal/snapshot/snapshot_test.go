package snapshot_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/component"
	"github.com/CyberCute-Studio/storycreep/server/internal/connection"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/dependency"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/expression"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/jobs"
	"github.com/CyberCute-Studio/storycreep/server/internal/node"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/serializer"
	"github.com/CyberCute-Studio/storycreep/server/internal/snapshot"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
	"github.com/CyberCute-Studio/storycreep/server/internal/validation"
	"github.com/CyberCute-Studio/storycreep/server/internal/variable"
)

func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

// env is a project of a new database, with its owner, the pipeline of the
// commands that change its content, and the service of its snapshots, whose
// builds the tests run themselves.
type env struct {
	d         *dbtest.Database
	db        *database.DB
	workspace ids.UUID
	project   ids.UUID
	owner     ids.UUID
	pipeline  *commands.Pipeline
	projects  *project.Service
	snapshots *snapshot.Service
	// enqueued counts the builds' jobs that publications enqueued.
	enqueued *atomic.Int32
}

// countingQueue counts the jobs that it is given, and runs none.
type countingQueue struct{ n *atomic.Int32 }

func (q countingQueue) Enqueue(context.Context, pgx.Tx, jobs.Args, *jobs.EnqueueOptions) (jobs.Enqueued, error) {
	q.n.Add(1)
	return jobs.Enqueued{}, nil
}

// newEnv returns a new project of a new database, whose owner publishes it.
func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	d := dbtest.New(t)
	db, admin := d.Open(t), d.Admin(t)
	owner, ws := ids.New(), ids.New()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO "user" (id, email, display_name) VALUES ($1, 'elara@example.com', 'Elara')`, []any{owner}},
		{`INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Harbour', 'harbour', 'unlimited')`, []any{ws}},
		{`INSERT INTO membership (id, workspace_id, user_id, role) VALUES ($1, $2, $3, 'owner')`,
			[]any{ids.New(), ws, owner}},
	} {
		if _, err := admin.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatal(err)
		}
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
	p, err := projects.Create(ctx, owner, ws, project.Settings{Name: "Harbour"}, audit.Client{})
	if err != nil {
		t.Fatal(err)
	}
	projectID, err := ids.Decode(ids.Project, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	n := &atomic.Int32{}
	return &env{d: d, db: db, workspace: ws, project: projectID, owner: owner, pipeline: pipeline,
		projects: projects, snapshots: snapshot.New(db, snapshot.Options{Projects: projects,
			Queue: countingQueue{n}}), enqueued: n}
}

// actor is the project's owner.
func (e *env) actor() commands.Actor { return commands.Actor{UserID: e.owner} }

// scope is a transaction of the project's workspace, of its owner.
func (e *env) scope() database.Scope {
	return database.Scope{WorkspaceID: e.workspace, UserID: e.owner}
}

// apply applies one batch of the commands to the project as its owner, each
// a type and its payload, in turn, or fails the test.
func (e *env) apply(t *testing.T, cmds ...any) {
	t.Helper()
	var batch []commands.Command
	for i := 0; i+1 < len(cmds); i += 2 {
		payload, err := json.Marshal(cmds[i+1])
		if err != nil {
			t.Fatal(err)
		}
		batch = append(batch, commands.Command{ID: ids.New(), Type: commands.Type(cmds[i].(string)),
			Payload: payload})
	}
	if _, err := e.pipeline.Apply(context.Background(), e.actor(), e.project, batch); err != nil {
		t.Fatal(err)
	}
}

// id returns a new public ID of the kind k.
func id(k ids.Kind) string { return ids.Encode(k, ids.New()) }

// text returns a rich text of one paragraph of s.
func text(s string) *richtext.Doc {
	return &richtext.Doc{Blocks: []richtext.Block{&richtext.Paragraph{Inlines: []richtext.Inline{
		&richtext.Text{Text: s}}}}}
}

// story is a board of the project, and its two nodes, the first leading to
// the second, an ending.
type story struct {
	board, start, end, choice string
}

// newStory adds a board of two nodes to the project, of which the validation
// finds nothing, and returns them.
func (e *env) newStory(t *testing.T) story {
	t.Helper()
	s := story{board: id(ids.Board), start: id(ids.Node), end: id(ids.Node), choice: id(ids.Connection)}
	e.apply(t,
		"board.create", map[string]any{"id": s.board, "name": "Harbour"},
		"node.create", map[string]any{"id": s.start, "board_id": s.board, "type": "element", "kind": "narration",
			"title": "Quay", "text": text("The harbour is quiet tonight."), "position": map[string]any{"x": 0, "y": 0}},
		"node.create", map[string]any{"id": s.end, "board_id": s.board, "type": "element", "kind": "narration",
			"title": "Home", "text": text("Elara walks home."), "is_ending": true,
			"position": map[string]any{"x": 200, "y": 0}},
		"connection.create", map[string]any{"id": s.choice, "from_node_id": s.start, "to_node_id": s.end,
			"label": text("Go home")},
	)
	if r := e.stored(t); len(r.Findings) != 0 {
		t.Fatalf("the story's report holds %+v, want nothing", r.Findings)
	}
	return s
}

// stored returns the validation of the project's draft as it is.
func (e *env) stored(t *testing.T) validation.Stored {
	t.Helper()
	var out validation.Stored
	err := e.db.InSnapshot(context.Background(), e.scope(), func(tx pgx.Tx) error {
		r, err := validation.Run(context.Background(), tx, e.workspace, e.project)
		if err != nil {
			return err
		}
		out = r.Stored()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// acknowledged returns the acknowledgements of every warning of r that is
// not muted.
func acknowledged(r validation.Stored) []validation.Acknowledgement {
	var acks []validation.Acknowledgement
	for _, f := range r.Unacknowledged(nil) {
		acks = append(acks, f.Acknowledgement())
	}
	return acks
}

// publish publishes the project's draft as a snapshot of the version, as its
// owner, who acknowledges the draft's report as it is, every warning of it.
func (e *env) publish(t *testing.T, version string) (snapshot.Snapshot, error) {
	t.Helper()
	r := e.stored(t)
	return e.snapshots.Publish(context.Background(), e.actor(), e.project, snapshot.Request{Version: version,
		Note: "Harbour intro", AcknowledgedSeq: r.Seq, AcknowledgedWarnings: acknowledged(r)})
}

// mustPublish publishes as publish does, or fails the test.
func (e *env) mustPublish(t *testing.T, version string) snapshot.Snapshot {
	t.Helper()
	s, err := e.publish(t, version)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// snapshotID returns the ID of s.
func snapshotID(t *testing.T, s snapshot.Snapshot) ids.UUID {
	t.Helper()
	id, err := ids.Decode(ids.Snapshot, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// build runs the build of s, as its job does, and returns s as it is then.
func (e *env) build(t *testing.T, s snapshot.Snapshot) snapshot.Snapshot {
	t.Helper()
	if err := e.snapshots.Build(context.Background(), e.workspace, snapshotID(t, s)); err != nil {
		t.Fatal(err)
	}
	return e.get(t, s)
}

// get returns s as it is.
func (e *env) get(t *testing.T, s snapshot.Snapshot) snapshot.Snapshot {
	t.Helper()
	got, err := e.snapshots.Get(context.Background(), e.actor(), e.project, snapshotID(t, s))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// files returns the files that s holds, by their paths, each checked against
// the hash that is stored with it.
func (e *env) files(t *testing.T, s snapshot.Snapshot) map[string][]byte {
	t.Helper()
	rows, err := e.d.Admin(t).Query(context.Background(), "SELECT path, sha256, bytes FROM snapshot_file "+
		"WHERE snapshot_id = $1", snapshotID(t, s))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	files := map[string][]byte{}
	for rows.Next() {
		var path, hash string
		var data []byte
		if err := rows.Scan(&path, &hash, &data); err != nil {
			t.Fatal(err)
		}
		if got := string(published.HashOf(data)); got != hash {
			t.Errorf("%s is stored with the hash %s, but its bytes' is %s", path, hash, got)
		}
		files[path] = data
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return files
}

// report returns the report that the build of s stored.
func (e *env) report(t *testing.T, s snapshot.Snapshot) validation.Stored {
	t.Helper()
	var data []byte
	if err := e.d.Admin(t).QueryRow(context.Background(), "SELECT validation_report FROM snapshot WHERE id = $1",
		snapshotID(t, s)).Scan(&data); err != nil {
		t.Fatal(err)
	}
	var out validation.Stored
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("the stored report %s: %v", data, err)
	}
	return out
}

// snapshotsUsage returns the workspace's usage of snapshots.
func (e *env) snapshotsUsage(t *testing.T) int64 {
	t.Helper()
	var used map[usage.Key]int64
	err := e.db.InTx(context.Background(), e.scope(), func(tx pgx.Tx) error {
		var err error
		used, err = usage.Read(context.Background(), tx, e.workspace)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return used[usage.Snapshots]
}

// TestPublishBuildsTheDraft checks that publishing adds a snapshot that is
// building, of the version and the note that its author chose, and enqueues
// its build, which makes it ready with the files that the serializer writes
// of the draft, stamped as the release, and with the report of the draft
// that it read, of the same sequence as the files (FR-DEP-04 AC4); that a
// build of a snapshot that is ready changes nothing; and that the snapshot
// counts towards the workspace's snapshots.
//
// Verifies: FR-DEP-04/AC4
func TestPublishBuildsTheDraft(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.newStory(t)
	seq := e.stored(t).Seq
	s := e.mustPublish(t, "0.1.0")
	if s.Status != snapshot.Building || s.Version != "0.1.0" || s.Note != "Harbour intro" ||
		s.PublishedBy != ids.Encode(ids.User, e.owner) || s.AcknowledgedSeq != seq || s.BuiltAt != nil ||
		s.SourceSeq != nil || s.ContentHash != "" || s.Counts != nil || s.FailureCode != "" {
		t.Fatalf("the snapshot is %+v, want building, of 0.1.0, published by its owner", s)
	}
	if n := e.enqueued.Load(); n != 1 {
		t.Errorf("the publication enqueued %d builds, want 1", n)
	}
	if got := e.snapshotsUsage(t); got != 1 {
		t.Errorf("the workspace's usage of snapshots is %d, want 1", got)
	}
	b := e.build(t, s)
	if b.Status != snapshot.Ready || b.BuiltAt == nil || b.SourceSeq == nil || *b.SourceSeq != seq ||
		b.SchemaVersion == nil || *b.SchemaVersion != published.SchemaVersion || b.FailureCode != "" ||
		b.Counts == nil || *b.Counts != (snapshot.EntityCounts{Boards: 1, Nodes: 2, Connections: 1}) {
		t.Fatalf("the built snapshot is %+v, want ready, of seq %d, with its counts", b, seq)
	}
	var want *serializer.Snapshot
	if err := e.db.InSnapshot(context.Background(), e.scope(), func(tx pgx.Tx) error {
		var err error
		want, err = serializer.Build(context.Background(), tx, e.workspace, e.project, serializer.Stamp{
			Generator: serializer.Generator(), At: s.PublishedAt,
			Release: &serializer.Release{ID: s.ID, Version: "0.1.0", Note: "Harbour intro"}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	files := e.files(t, b)
	if !maps.EqualFunc(files, want.Files, bytes.Equal) {
		t.Errorf("the snapshot holds %v, want the files that the serializer writes, %v",
			slices.Sorted(maps.Keys(files)), slices.Sorted(maps.Keys(want.Files)))
	}
	if b.ContentHash != string(want.ContentHash) {
		t.Errorf("the content hash is %s, want %s", b.ContentHash, want.ContentHash)
	}
	var m published.Manifest
	if err := published.Decode(files[published.ManifestPath], &m); err != nil {
		t.Fatal(err)
	}
	release, ok := m.Snapshot.(*published.ReleaseSnapshot)
	if !ok || release.ID != s.ID || release.Version != "0.1.0" || release.Note != "Harbour intro" ||
		string(release.ContentHash) != b.ContentHash ||
		release.PublishedAt != published.TimestampOf(s.PublishedAt) {
		t.Errorf("the manifest's snapshot is %+v, want the release", m.Snapshot)
	}
	if r := e.report(t, b); r.Seq != *b.SourceSeq || len(r.Findings) != 0 {
		t.Errorf("the stored report is of seq %d, with %+v, want of the source_seq, %d, and empty", r.Seq,
			r.Findings, *b.SourceSeq)
	}
	// Building it again, as a job that runs twice does, changes nothing.
	if again := e.build(t, b); !again.BuiltAt.Equal(*b.BuiltAt) || again.Status != snapshot.Ready {
		t.Errorf("a second build made it %+v, want it as it was", again)
	}
	if got := e.snapshotsUsage(t); got != 1 {
		t.Errorf("the workspace's usage of snapshots is %d once the snapshot is ready, want 1", got)
	}
}

// TestPublishTwiceSameContentHash checks that two snapshots of the same
// content, with no change between them, have the same content hash, though
// their manifests differ.
//
// Verifies: FR-SNAP-01/AC2
func TestPublishTwiceSameContentHash(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.newStory(t)
	first := e.build(t, e.mustPublish(t, "1.0.0"))
	second := e.build(t, e.mustPublish(t, "1.0.1"))
	if first.Status != snapshot.Ready || second.Status != snapshot.Ready || first.ContentHash == "" ||
		first.ContentHash != second.ContentHash || first.ID == second.ID {
		t.Fatalf("the snapshots are %+v and %+v, want two, ready, of one content hash", first, second)
	}
	a, b := e.files(t, first), e.files(t, second)
	if bytes.Equal(a[published.ManifestPath], b[published.ManifestPath]) {
		t.Error("the two manifests are the same, want each of its own release")
	}
	delete(a, published.ManifestPath)
	delete(b, published.ManifestPath)
	if !maps.EqualFunc(a, b, bytes.Equal) {
		t.Error("the two snapshots' content files differ")
	}
}

// TestVersionsUniqueAndIncreasing checks that a project's versions are
// unique and increase among its snapshots that are building or ready, and
// that a failed snapshot does not hold its version, which a new snapshot
// takes, while the failed one is kept with its report and no files, which
// the database's index holds to as well.
//
// Verifies: FR-SNAP-01/AC4
func TestVersionsUniqueAndIncreasing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	s := e.newStory(t)
	if p, err := e.snapshots.NextVersion(ctx, e.actor(), e.project); err != nil ||
		p != (snapshot.Proposal{Version: "0.1.0"}) {
		t.Errorf("the proposal for the first snapshot is %+v, %v; want 0.1.0", p, err)
	}
	a := e.build(t, e.mustPublish(t, "1.2.3"))
	for _, v := range []string{"1.2.3", "1.2.0", "0.9.9"} {
		_, err := e.publish(t, v)
		var notNewer *snapshot.NotNewerError
		if !errors.As(err, &notNewer) || notNewer.Latest != "1.2.3" || notNewer.Proposed != "1.2.4" {
			t.Errorf("publishing %s answers %v, want not newer than 1.2.3", v, err)
		}
	}
	if p, err := e.snapshots.NextVersion(ctx, e.actor(), e.project); err != nil ||
		p != (snapshot.Proposal{Version: "1.2.4", LatestVersion: "1.2.3"}) {
		t.Errorf("the proposal is %+v, %v; want 1.2.4 after 1.2.3", p, err)
	}
	// 1.10.0 is greater than 1.2.3, as numbers are compared. A jumper
	// without a target, which its build finds, fails it.
	b := e.mustPublish(t, "1.10.0")
	jumper := id(ids.Node)
	e.apply(t, "node.create", map[string]any{"id": jumper, "board_id": s.board, "type": "jumper",
		"position": map[string]any{"x": 0, "y": 200}})
	b = e.build(t, b)
	if b.Status != snapshot.Failed || b.FailureCode != snapshot.FailureValidation || b.SourceSeq == nil {
		t.Fatalf("the snapshot is %+v, want failed with validation_failed", b)
	}
	if files := e.files(t, b); len(files) != 0 {
		t.Errorf("the failed snapshot holds %d files, want none", len(files))
	}
	if r := e.report(t, b); len(r.Errors()) != 1 || r.Errors()[0].Code != validation.JumperBroken ||
		r.Errors()[0].EntityID != jumper {
		t.Errorf("the failed snapshot's report holds %+v, want the jumper's jumper_broken", r.Findings)
	}
	if p, err := e.snapshots.NextVersion(ctx, e.actor(), e.project); err != nil || p.LatestVersion != "1.2.3" {
		t.Errorf("the proposal is %+v, %v; want of 1.2.3, as the failed snapshot holds no version", p, err)
	}
	e.apply(t, "node.delete", map[string]any{"id": jumper})
	c := e.mustPublish(t, "1.10.0")
	if c.ID == b.ID || c.Status != snapshot.Building {
		t.Errorf("publishing the failed snapshot's version again gives %+v, want a new snapshot", c)
	}
	// The index holds the versions of the snapshots that are building or
	// ready unique, whatever writes them, and the failed one's not.
	admin := e.d.Admin(t)
	_, err := admin.Exec(ctx, "INSERT INTO snapshot (id, workspace_id, project_id, version, version_major, "+
		"version_minor, version_patch, note, acknowledged_seq, acknowledged_warnings) "+
		"VALUES ($1, $2, $3, '1.2.3', 1, 2, 3, 'Again', 0, '[]')", ids.New(), e.workspace, e.project)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" || pgErr.ConstraintName != "snapshot_version" {
		t.Errorf("a second snapshot of 1.2.3 is added with %v, want refused by snapshot_version", err)
	}
	var ofVersion int
	if err := admin.QueryRow(ctx, "SELECT count(*) FROM snapshot WHERE project_id = $1 AND version = '1.10.0'",
		e.project).Scan(&ofVersion); err != nil || ofVersion != 2 {
		t.Errorf("the project has %d snapshots of 1.10.0, %v; want the failed one and the new one", ofVersion, err)
	}
	if a.Status != snapshot.Ready {
		t.Errorf("the first snapshot is %+v, want ready", a)
	}
}

// TestPublishGate checks that the pre-check of publishing refuses a draft
// with a validation error, as a jumper whose target was deleted, or a
// connection whose condition does not parse, which it lists, and one with a
// warning that is neither acknowledged nor muted, and makes no snapshot of
// either; and that it refuses a request that is not valid.
//
// Verifies: FR-DEP-04/AC1, FR-EDIT-05/AC1, FR-EDIT-03/AC4
func TestPublishGate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	s := e.newStory(t)
	blocked := func(code validation.Code, entity string) {
		t.Helper()
		_, err := e.publish(t, "1.0.0")
		var b *snapshot.BlockedError
		if !errors.As(err, &b) || len(b.Report.Findings) != 1 || b.Report.Findings[0].Code != code ||
			b.Report.Findings[0].EntityID != entity || b.Report.Seq != e.stored(t).Seq {
			t.Fatalf("publishing answers %v, want blocked by the %s of %s", err, code, entity)
		}
	}
	// A jumper whose target was deleted.
	jumper := id(ids.Node)
	e.apply(t, "node.create", map[string]any{"id": jumper, "board_id": s.board, "type": "jumper",
		"position": map[string]any{"x": 0, "y": 200}, "target": map[string]any{"board_id": s.board,
			"node_id": s.end}})
	e.apply(t, "node.delete", map[string]any{"id": s.end})
	blocked(validation.JumperBroken, jumper)
	e.apply(t, "node.restore", map[string]any{"id": s.end}, "node.delete", map[string]any{"id": jumper})
	// A connection whose condition does not parse.
	e.apply(t, "connection.update", map[string]any{"id": s.choice, "condition": "visits( >"})
	blocked(validation.ExprInvalid, s.choice)
	e.apply(t, "connection.update", map[string]any{"id": s.choice, "condition": ""})

	// A warning: an element that no connection leaves, which is not an
	// ending, and a second root of the board.
	stray := id(ids.Node)
	e.apply(t, "node.create", map[string]any{"id": stray, "board_id": s.board, "type": "element",
		"kind": "narration", "title": "Lighthouse", "text": text("The lamp is dark."),
		"position": map[string]any{"x": 0, "y": 400}})
	r := e.stored(t)
	warnings := r.Unacknowledged(nil)
	if len(warnings) < 2 {
		t.Fatalf("the draft warns of %+v, want of the stray node and the board", warnings)
	}
	_, err := e.snapshots.Publish(ctx, e.actor(), e.project, snapshot.Request{Version: "1.0.0", Note: "Lamp",
		AcknowledgedSeq: r.Seq, AcknowledgedWarnings: acknowledged(r)[1:]})
	var unacknowledged *snapshot.UnacknowledgedError
	if !errors.As(err, &unacknowledged) || len(unacknowledged.Report.Findings) != 1 ||
		unacknowledged.Report.Findings[0].Acknowledgement().EntityID != warnings[0].EntityID {
		t.Fatalf("publishing with a warning unacknowledged answers %v, want that warning listed", err)
	}
	// A muted warning needs no acknowledgement.
	first := warnings[0]
	e.apply(t, "validation.mute", map[string]any{"entity_id": first.EntityID, "code": first.Code,
		"reason": "The lighthouse is a set piece."})
	r = e.stored(t)
	if _, err := e.snapshots.Publish(ctx, e.actor(), e.project, snapshot.Request{Version: "1.0.0", Note: "Lamp",
		AcknowledgedSeq: r.Seq, AcknowledgedWarnings: acknowledged(r)}); err != nil {
		t.Fatalf("publishing with the muted warning unacknowledged answers %v, want a snapshot", err)
	}
	if got := r.Unacknowledged(acknowledged(r)[:0]); slices.ContainsFunc(got, func(f validation.StoredFinding) bool {
		return f.Acknowledgement().EntityID == first.EntityID && f.Code == first.Code
	}) {
		t.Errorf("the muted warning is listed as needing an acknowledgement: %+v", got)
	}

	for _, tt := range []struct {
		name  string
		r     snapshot.Request
		field string
	}{
		{"a leading zero", snapshot.Request{Version: "1.02.0", Note: "Lamp", AcknowledgedSeq: r.Seq}, "version"},
		{"two parts", snapshot.Request{Version: "1.2", Note: "Lamp", AcknowledgedSeq: r.Seq}, "version"},
		{"a pre-release", snapshot.Request{Version: "2.0.0-beta", Note: "Lamp", AcknowledgedSeq: r.Seq}, "version"},
		{"too great", snapshot.Request{Version: "1000000000.0.0", Note: "Lamp", AcknowledgedSeq: r.Seq},
			"version"},
		{"no note", snapshot.Request{Version: "2.0.0", AcknowledgedSeq: r.Seq}, "note"},
		{"a blank note", snapshot.Request{Version: "2.0.0", Note: " ", AcknowledgedSeq: r.Seq}, "note"},
		{"a long note", snapshot.Request{Version: "2.0.0", Note: strings.Repeat("a", snapshot.MaxNoteLength+1),
			AcknowledgedSeq: r.Seq}, "note"},
		{"a future seq", snapshot.Request{Version: "2.0.0", Note: "Lamp", AcknowledgedSeq: r.Seq + 1,
			AcknowledgedWarnings: acknowledged(r)}, "acknowledged_seq"},
	} {
		_, err := e.snapshots.Publish(ctx, e.actor(), e.project, tt.r)
		var fields input.FieldErrors
		if !errors.As(err, &fields) || len(fields) != 1 || fields[0].Field != tt.field {
			t.Errorf("%s: publishing answers %v, want the field %s refused", tt.name, err, tt.field)
		}
	}
	var made int
	if err := e.d.Admin(t).QueryRow(ctx, "SELECT count(*) FROM snapshot").Scan(&made); err != nil || made != 1 {
		t.Errorf("the refusals left %d snapshots, %v; want the one publication's", made, err)
	}
}

// TestRaceDanglingReference checks that a command that leaves a reference
// dangling, committed after the pre-check of a publication passed and before
// its build, fails the snapshot with validation_failed, whose report names
// the reference, so that no ready snapshot holds it.
//
// Verifies: FR-DEP-04/AC3
func TestRaceDanglingReference(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	s := e.newStory(t)
	jumper := id(ids.Node)
	e.apply(t, "node.create", map[string]any{"id": jumper, "board_id": s.board, "type": "jumper",
		"position": map[string]any{"x": 0, "y": 200}, "target": map[string]any{"board_id": s.board,
			"node_id": s.end}})
	p := e.mustPublish(t, "1.0.0")
	e.apply(t, "node.delete", map[string]any{"id": s.end})
	b := e.build(t, p)
	if b.Status != snapshot.Failed || b.FailureCode != snapshot.FailureValidation {
		t.Fatalf("the snapshot is %+v, want failed with validation_failed", b)
	}
	errs := e.report(t, b).Errors()
	if !slices.ContainsFunc(errs, func(f validation.StoredFinding) bool {
		return f.Code == validation.JumperBroken && f.EntityID == jumper
	}) {
		t.Errorf("the stored report's errors are %+v, want the jumper's jumper_broken", errs)
	}
	var ready int
	if err := e.d.Admin(t).QueryRow(context.Background(), "SELECT count(*) FROM snapshot WHERE status = 'ready'").
		Scan(&ready); err != nil || ready != 0 {
		t.Errorf("%d snapshots are ready, %v; want none", ready, err)
	}
	if files := e.files(t, b); len(files) != 0 {
		t.Errorf("the failed snapshot holds %d files, want none", len(files))
	}
}

// TestEditsDuringBuild checks that a batch that commits while a build reads
// the draft is not in the snapshot, whose source_seq, and whose report's
// seq, are the sequence before it, and that the next snapshot holds it.
//
// Verifies: FR-SNAP-01/AC7, FR-DEP-04/AC4
func TestEditsDuringBuild(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	s := e.newStory(t)
	before := e.stored(t).Seq
	p := e.mustPublish(t, "1.0.0")
	const renamed = "The Old Quay"
	e.snapshots.SetHooks(func() {
		e.apply(t, "node.update", map[string]any{"id": s.start, "title": renamed})
	}, nil)
	b := e.build(t, p)
	e.snapshots.SetHooks(nil, nil)
	if b.Status != snapshot.Ready || b.SourceSeq == nil || *b.SourceSeq != before {
		t.Fatalf("the snapshot is %+v, want ready, of seq %d", b, before)
	}
	if after := e.stored(t).Seq; after <= before {
		t.Fatalf("the edit left the project's seq at %d, want past %d", after, before)
	}
	for path, data := range e.files(t, b) {
		if bytes.Contains(data, []byte(renamed)) {
			t.Errorf("%s holds the title that a batch set while the build read the draft", path)
		}
	}
	if r := e.report(t, b); r.Seq != before {
		t.Errorf("the stored report is of seq %d, want the source_seq, %d", r.Seq, before)
	}
	next := e.build(t, e.mustPublish(t, "1.0.1"))
	if !bytes.Contains(e.files(t, next)[published.BoardPath("harbour")], []byte(renamed)) {
		t.Error("the next snapshot does not hold the new title")
	}
}

// TestValidationChanged checks that a build fails the snapshot with
// validation_changed for a warning that its author did not acknowledge, of a
// draft that changed since the report that they acknowledged, and makes it
// ready for the warnings that they acknowledged of the draft that they
// validated.
func TestValidationChanged(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	s := e.newStory(t)
	p := e.mustPublish(t, "1.0.0")
	stray := id(ids.Node)
	e.apply(t, "node.create", map[string]any{"id": stray, "board_id": s.board, "type": "element",
		"kind": "narration", "title": "Lighthouse", "text": text("The lamp is dark."),
		"position": map[string]any{"x": 0, "y": 400}})
	b := e.build(t, p)
	if b.Status != snapshot.Failed || b.FailureCode != snapshot.FailureChanged || b.SourceSeq == nil ||
		*b.SourceSeq == b.AcknowledgedSeq {
		t.Fatalf("the snapshot is %+v, want failed with validation_changed", b)
	}
	if got := e.snapshotsUsage(t); got != 0 {
		t.Errorf("the workspace's usage of snapshots is %d once the snapshot failed, want 0", got)
	}
	// Published again, with the new warnings acknowledged, and the same
	// version, which the failed snapshot does not hold.
	if again := e.build(t, e.mustPublish(t, "1.0.0")); again.Status != snapshot.Ready {
		t.Errorf("the snapshot published again is %+v, want ready", again)
	}
}

// TestFailedBuild checks that a build validates the files that it writes
// against the published format's schemas before the snapshot becomes ready,
// and that one that does not validate fails the snapshot with build_failed,
// which leaves no file of it, with the report of the draft that it read.
//
// Verifies: FR-SNAP-01/AC5, FR-DATA-04/AC1
func TestFailedBuild(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.newStory(t)
	p := e.mustPublish(t, "1.0.0")
	e.snapshots.SetHooks(nil, func(files map[string][]byte) {
		path := published.BoardPath("harbour")
		// A key that no schema defines may be anything but null.
		files[path] = append([]byte(`{"extra":null,`), files[path][1:]...)
	})
	b := e.build(t, p)
	e.snapshots.SetHooks(nil, nil)
	if b.Status != snapshot.Failed || b.FailureCode != snapshot.FailureBuild || b.ContentHash != "" ||
		b.Counts != nil || b.SchemaVersion != nil || b.SourceSeq == nil {
		t.Fatalf("the snapshot is %+v, want failed with build_failed, without content", b)
	}
	if files := e.files(t, b); len(files) != 0 {
		t.Errorf("the failed snapshot holds %d files, want none", len(files))
	}
	if r := e.report(t, b); r.Seq != *b.SourceSeq {
		t.Errorf("the stored report is of seq %d, want %d", r.Seq, *b.SourceSeq)
	}
	if got := e.snapshotsUsage(t); got != 0 {
		t.Errorf("the workspace's usage of snapshots is %d once the snapshot failed, want 0", got)
	}
}

// refused reports whether err is a refusal for want of a privilege, which a
// trigger of the snapshots raises too.
func refused(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42501"
}

// TestImmutable checks that nothing changes a ready snapshot, nor its files,
// in the database: neither the application, whose grants and the triggers
// refuse it, nor the superuser, whom the triggers refuse; but the deletion
// of its publisher's account, which leaves it as it was but for who
// published it, and the purge of its project, which deletes it whole, through
// the application's login, which deletes no snapshot itself.
//
// Verifies: FR-SNAP-01/AC1
func TestImmutable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	e.newStory(t)
	b := e.build(t, e.mustPublish(t, "1.0.0"))
	id := snapshotID(t, b)
	files := e.files(t, b)
	for _, stmt := range []string{
		"UPDATE snapshot SET note = 'Changed' WHERE id = $1",
		"UPDATE snapshot SET status = 'failed', failure_code = 'build_failed' WHERE id = $1",
		"UPDATE snapshot SET content_hash = 'sha256:' || repeat('1', 64) WHERE id = $1",
		"UPDATE snapshot SET built_at = now() WHERE id = $1",
		"DELETE FROM snapshot WHERE id = $1",
		"INSERT INTO snapshot_file (id, workspace_id, snapshot_id, path, sha256, bytes) VALUES (gen_random_uuid(), " +
			"(SELECT workspace_id FROM snapshot WHERE id = $1), $1, 'boards/extra.json', 'sha256:' || repeat('0', 64), " +
			"'\\x7b7d')",
		"UPDATE snapshot_file SET bytes = '\\x7b7d' WHERE snapshot_id = $1",
		"DELETE FROM snapshot_file WHERE snapshot_id = $1",
	} {
		err := e.db.InTx(ctx, e.scope(), func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, stmt, id)
			return err
		})
		if !refused(err) {
			t.Errorf("the application's %q: %v, want refused", stmt, err)
		}
	}
	admin := e.d.Admin(t)
	for _, stmt := range []string{
		"UPDATE snapshot SET note = 'Changed' WHERE id = $1",
		"UPDATE snapshot SET status = 'building', built_at = NULL WHERE id = $1",
		"UPDATE snapshot_file SET bytes = '\\x7b7d' WHERE snapshot_id = $1",
	} {
		if _, err := admin.Exec(ctx, stmt, id); !refused(err) {
			t.Errorf("the superuser's %q: %v, want refused", stmt, err)
		}
	}
	if got := e.files(t, b); !maps.EqualFunc(got, files, bytes.Equal) {
		t.Error("the snapshot's files changed")
	}
	// The service changes nothing of it either: a build again is nothing.
	if again := e.build(t, b); again.Status != snapshot.Ready || !again.BuiltAt.Equal(*b.BuiltAt) {
		t.Errorf("a build again made it %+v", again)
	}

	row := func() map[string]any {
		t.Helper()
		var doc map[string]any
		if err := admin.QueryRow(ctx, "SELECT to_jsonb(s) FROM snapshot s WHERE id = $1", id).Scan(&doc); err != nil {
			t.Fatal(err)
		}
		return doc
	}
	was := row()
	if _, err := admin.Exec(ctx, `DELETE FROM "user" WHERE id = $1`, e.owner); err != nil {
		t.Fatalf("the deletion of the publisher's account: %v", err)
	}
	now := row()
	if now["published_by"] != nil {
		t.Errorf("published_by is %v once the publisher's account is deleted, want null", now["published_by"])
	}
	delete(was, "published_by")
	delete(now, "published_by")
	if a, b := mustJSON(t, was), mustJSON(t, now); a != b {
		t.Errorf("the account's deletion changed the snapshot from %s to %s", a, b)
	}
	if got := e.files(t, b); !maps.EqualFunc(got, files, bytes.Equal) {
		t.Error("the account's deletion changed the snapshot's files")
	}

	// The purge of the project, as its job deletes it, through the
	// application's login, deletes the snapshot with its files.
	if err := e.db.InTx(ctx, database.Scope{WorkspaceID: e.workspace}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "DELETE FROM project WHERE workspace_id = $1 AND id = $2", e.workspace, e.project)
		return err
	}); err != nil {
		t.Fatalf("the project's purge: %v", err)
	}
	var left int
	if err := admin.QueryRow(ctx, "SELECT (SELECT count(*) FROM snapshot) + (SELECT count(*) FROM snapshot_file)").
		Scan(&left); err != nil || left != 0 {
		t.Errorf("%d rows of snapshots are left once the project is purged, %v; want none", left, err)
	}
}

// mustJSON returns v as JSON, or fails the test.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestSnapshotsRecount checks that the reconciliation of the workspace's
// usage recounts its snapshots that are building or ready, as after a
// project's purge, whose cascade deletes them without a delta.
//
// Verifies: FR-ENT-02/AC1
func TestSnapshotsRecount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	e.newStory(t)
	e.build(t, e.mustPublish(t, "1.0.0"))
	e.mustPublish(t, "1.0.1")
	if got := e.snapshotsUsage(t); got != 2 {
		t.Fatalf("the workspace's usage of snapshots is %d, want 2", got)
	}
	if _, err := e.d.Admin(t).Exec(ctx, "DELETE FROM project WHERE id = $1", e.project); err != nil {
		t.Fatal(err)
	}
	if _, err := usage.Reconcile(ctx, e.db); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for e.snapshotsUsage(t) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the reconciliation left the usage of snapshots at %d, want 0", e.snapshotsUsage(t))
		}
		if _, err := usage.Reconcile(ctx, e.db); err != nil {
			t.Fatal(err)
		}
	}
}
