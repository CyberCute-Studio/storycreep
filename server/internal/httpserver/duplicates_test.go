package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/fixture"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/node"
	"github.com/CyberCute-Studio/storycreep/server/internal/snapshot"
)

// duplicate asks for a copy of the project, named name, as si.
func duplicate(t *testing.T, h http.Handler, si signedIn, project, name string) response {
	t.Helper()
	return call(h, http.MethodPost, projectPath(project)+"/duplicate", jsonBody(t, map[string]string{"name": name}),
		si.header(true))
}

// TestDuplicateProject checks a copy of a project of more than 1,000
// entities, with an asset as a node's cover, a muted warning, a comment, a
// play state, and a release: it is made in under 10 s, as a new project of
// the workspace, named as asked, with a slug of its name, which records the
// project that it was duplicated from; its export differs from the
// project's only in the IDs, the names, and the timestamps; and it holds
// neither the comment, nor the play state, nor the release. Its creation is
// recorded in the audit log.
//
// Verifies: FR-PROJ-01/AC1
func TestDuplicateProject(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	startWorkers(t, s, d)
	h := s.main
	f, err := fixture.GenerateSpec(fixture.Spec{Boards: 5, Nodes: 360, Variables: 60, Components: 10}, 85)
	if err != nil {
		t.Fatal(err)
	}
	p := createProject(t, h, owner, ws, "Source of the copy").ID
	sender := fixture.Sender{Client: &http.Client{Transport: handlerTransport{h}},
		Endpoint: "http://localhost:8080" + commandsPath(p), Header: owner.header(true)}
	if err := sender.Send(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	pid := decode(t, ids.Project, p).String()
	entities := countRows(t, d, `SELECT (SELECT count(*) FROM folder WHERE project_id = $1::uuid)
		+ (SELECT count(*) FROM board WHERE project_id = $1::uuid)
		+ (SELECT count(*) FROM variable WHERE project_id = $1::uuid)
		+ (SELECT count(*) FROM component_type WHERE project_id = $1::uuid)
		+ (SELECT count(*) FROM attribute_def WHERE project_id = $1::uuid)
		+ (SELECT count(*) FROM component WHERE project_id = $1::uuid)
		+ (SELECT count(*) FROM node WHERE project_id = $1::uuid)
		+ (SELECT count(*) FROM branch_output WHERE project_id = $1::uuid)
		+ (SELECT count(*) FROM connection WHERE project_id = $1::uuid)`, pid)
	if entities < 1000 {
		t.Fatalf("the project has %d entities, want 1,000 at least", entities)
	}
	// An element's cover, which the copy keeps, as its own asset.
	var element string
	for _, b := range f.Batches {
		for _, c := range b {
			var n struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			}
			if c.Type == node.Create && json.Unmarshal(c.Payload, &n) == nil && n.Type == node.TypeElement {
				element = n.ID
				break
			}
		}
		if element != "" {
			break
		}
	}
	cover := upload(t, h, owner, p, testPNG(t, 7, 5), http.StatusCreated).ID
	sendBatch(t, h, owner, p, wireCommand{Type: "node.update", Payload: map[string]any{"id": element,
		"cover_asset_id": cover}})
	// A muted warning, which is content, and a comment, a play state, and a
	// release, which are not.
	r := validationReport(t, h, owner, p)
	if len(r.Findings) == 0 || r.Findings[len(r.Findings)-1].Severity == "error" {
		t.Fatalf("the project's report is %+v", r.Counts)
	}
	warning := r.Findings[len(r.Findings)-1]
	sendBatch(t, h, owner, p, wireCommand{Type: "validation.mute", Payload: map[string]any{
		"entity_id": warning.EntityID, "code": warning.Code, "reason": "On purpose."}})
	if res := postComment(t, h, owner, p, ids.Encode(ids.Comment, ids.New()), element, "",
		"Is this the right cover?"); res.status != http.StatusOK {
		t.Fatalf("post a comment: %d %s", res.status, res.body)
	}
	state := emptyState(p)
	sendBatch(t, h, owner, p, wireCommand{Type: "play_state.create", Payload: map[string]any{
		"id": newPlayStateID(), "name": "Halfway", "state": state}})
	rel := snapshotFrom(t, call(h, http.MethodPost, snapshotsPath(p), publishBody(t, "1.0.0",
		validationReport(t, h, owner, p)), owner.header(true)), http.StatusAccepted)
	for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		got := snapshotFrom(t, call(h, http.MethodGet, snapshotsPath(p)+"/"+rel.ID, nil, owner.header(false)),
			http.StatusOK)
		if got.Status == snapshot.Ready {
			break
		}
		if got.Status == snapshot.Failed || time.Now().After(deadline) {
			t.Fatalf("the release's build ended as %+v", got)
		}
	}

	start := time.Now()
	res := duplicate(t, h, owner, p, "  Its copy ")
	took := time.Since(start)
	c := projectFrom(t, res, http.StatusCreated)
	t.Logf("duplicated %d entities in %v", entities, took.Round(time.Millisecond))
	if took >= 10*time.Second {
		t.Errorf("duplicating %d entities took %v, want under 10 s", entities, took)
	}
	if want := "/api/v1/projects/" + c.ID; res.header.Get("Location") != want {
		t.Errorf("Location is %q, want %q", res.header.Get("Location"), want)
	}
	if c.ID == p || c.Name != "Its copy" || c.Slug != "its-copy" || c.DuplicatedFrom != p || c.Role != "owner" ||
		c.WorkspaceID != ws || c.ArchivedAt != nil {
		t.Errorf("the copy is %+v", c)
	}
	if got := projectFrom(t, call(h, http.MethodGet, projectPath(c.ID), nil, owner.header(false)),
		http.StatusOK); got.DuplicatedFrom != p {
		t.Errorf("the copy reads as duplicated from %q, want %s", got.DuplicatedFrom, p)
	}

	// The exports, content-only, with the assets, differ in the IDs, the
	// names, and the timestamps alone.
	opts := map[string]any{"profile": "content", "include_assets": true, "include_releases": false}
	names := strings.NewReplacer(`"Source of the copy"`, `"<name>"`, `"source-of-the-copy"`, `"<slug>"`,
		`"Its copy"`, `"<name>"`, `"its-copy"`, `"<slug>"`)
	named := func(files map[string]string) map[string]string {
		for k, v := range files {
			files[k] = names.Replace(v)
		}
		return files
	}
	before, after := exportAs(t, s, owner, p, opts), exportAs(t, s, owner, c.ID, opts)
	if shared := sharedIDs(before, after); len(shared) > 0 {
		t.Errorf("the copy shares IDs with its project: %v", shared[:min(len(shared), 5)])
	}
	sameFiles(t, "the project's and its copy's exports", named(normalised(t, before)), named(normalised(t, after)))

	// What the copy does not hold, and the project keeps.
	for what, path := range map[string]string{"comments": "/comments", "play states": "/play-states",
		"releases": "/snapshots"} {
		var page struct {
			Items []json.RawMessage `json:"items"`
		}
		for _, project := range []string{p, c.ID} {
			res := call(h, http.MethodGet, projectPath(project)+path, nil, owner.header(false))
			if res.status != http.StatusOK || json.Unmarshal(res.body, &page) != nil {
				t.Fatalf("list the %s of %s: %d %s", what, project, res.status, res.body)
			}
			if got, want := len(page.Items), map[bool]int{true: 1, false: 0}[project == p]; got != want {
				t.Errorf("%s has %d %s, want %d", project, got, what, want)
			}
		}
	}
	if n := countRows(t, d, `SELECT count(*) FROM audit_entry WHERE action = 'project.duplicate'
		AND subject_id = $1::uuid AND actor_user_id = $2::uuid AND metadata->>'duplicated_from' = $3`,
		decode(t, ids.Project, c.ID).String(), decode(t, ids.User, owner.body.UserID).String(), p); n != 1 {
		t.Errorf("the copy's creation has %d audit entries, want 1", n)
	}
}

// TestDuplicateProjectRefusals checks what a duplication refuses, each time
// creating nothing: a name that a project may not take, a project in the
// trash, a workspace at its projects limit, and a project whose asset's file
// the store no longer has, which the copy's import refuses. An archived
// project is copied, and its copy is not archived. Once a project is purged,
// its copy no longer names it.
func TestDuplicateProjectRefusals(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", map[string]string{"STORYCREEP_WORKSPACE_LIMITS": "projects=2"})
	h := s.main
	p := createProject(t, h, owner, ws, "Quay").ID
	wantCode(t, "a blank name", duplicate(t, h, owner, p, "   "), http.StatusUnprocessableEntity, "validation_failed")
	wantCode(t, "a name past 100 characters", duplicate(t, h, owner, p, strings.Repeat("a", 101)),
		http.StatusUnprocessableEntity, "validation_failed")
	wantCode(t, "a project that does not exist", duplicate(t, h, owner, ids.Encode(ids.Project, ids.New()), "Copy"),
		http.StatusNotFound, "not_found")
	if n := projectCount(t, s, owner, ws); n != 1 {
		t.Fatalf("the workspace has %d projects after the refusals, want 1", n)
	}

	// An asset whose file the store no longer has.
	upload(t, h, owner, p, testPNG(t, 5, 5), http.StatusCreated)
	var key string
	if err := d.Admin(t).QueryRow(t.Context(), `SELECT storage_key FROM asset WHERE project_id = $1`,
		decode(t, ids.Project, p)).Scan(&key); err != nil {
		t.Fatal(err)
	}
	store, err := s.assets.Storage().Store(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	res := duplicate(t, h, owner, p, "Copy")
	wantCode(t, "a project whose asset's file is gone", res, http.StatusConflict, "duplicate_refused")
	if f, _ := res.details(t)["failure"].(map[string]any); f["code"] != "asset_invalid" || f["check"] != "missing_file" {
		t.Errorf("the refusal's failure is %v", res.details(t)["failure"])
	}
	if n := projectCount(t, s, owner, ws); n != 1 {
		t.Fatalf("the workspace has %d projects after a refused copy, want 1", n)
	}

	// An archived project, whose copy is not archived, and then the limit.
	q := createProject(t, h, owner, ws, "Lighthouse").ID
	if res := call(h, http.MethodPost, projectPath(q)+"/archive", nil, owner.header(true)); res.status !=
		http.StatusOK {
		t.Fatalf("archive the project: %d %s", res.status, res.body)
	}
	c := projectFrom(t, duplicate(t, h, owner, q, "Lighthouse again"), http.StatusCreated)
	if c.ArchivedAt != nil || c.DuplicatedFrom != q {
		t.Errorf("the archived project's copy is %+v", c)
	}
	wantCode(t, "a copy past the projects limit", duplicate(t, h, owner, q, "One too many"), http.StatusConflict,
		"limit_exceeded")
	if n := projectCount(t, s, owner, ws); n != 3 {
		t.Errorf("the workspace has %d projects after a copy past its limit, want 3", n)
	}

	// A project in the trash, and one that is purged, whose copy keeps
	// nothing of it.
	if res := call(h, http.MethodDelete, projectPath(q), nil, owner.header(true)); res.status != http.StatusNoContent {
		t.Fatalf("delete the project: %d %s", res.status, res.body)
	}
	wantCode(t, "a project in the trash", duplicate(t, h, owner, q, "From the trash"), http.StatusNotFound,
		"not_found")
	if _, err := d.Admin(t).Exec(t.Context(), `DELETE FROM project WHERE id = $1`, decode(t, ids.Project, q)); err != nil {
		t.Fatal(err)
	}
	if got := projectFrom(t, call(h, http.MethodGet, projectPath(c.ID), nil, owner.header(false)),
		http.StatusOK); got.DuplicatedFrom != "" {
		t.Errorf("the copy of a purged project names it: %+v", got)
	}
}
