package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/snapshot"
	"github.com/CyberCute-Studio/storycreep/server/internal/validation"
)

func snapshotsPath(project string) string { return projectPath(project) + "/snapshots" }

// fixtureVersions numbers the versions of the snapshots that the fixtures
// add, which are unique within a project.
var fixtureVersions atomic.Int32

// snapshot adds a snapshot of the project, ready, whose build stored a
// report without a finding, and returns its ID. It holds no files.
func (f *rolesFixture) snapshot(t *testing.T, project string) string {
	t.Helper()
	ctx := context.Background()
	id := ids.New()
	if _, err := f.admin.Exec(ctx, `INSERT INTO snapshot (id, workspace_id, project_id, version, version_major,
		version_minor, version_patch, note, acknowledged_seq, acknowledged_warnings)
		VALUES ($1, $2, $3, '0.0.' || $4::integer, 0, 0, $4, 'Harbour intro', 0, '[]')`, id,
		decode(t, ids.Workspace, f.ws), decode(t, ids.Project, project), fixtureVersions.Add(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.admin.Exec(ctx, `UPDATE snapshot SET status = 'ready', source_seq = 0,
		validation_report = '{"seq": 0, "findings": []}', schema_version = 1,
		content_hash = 'sha256:' || repeat('0', 64), counts = '{"boards": 0, "components": 0, "connections": 0,
		"nodes": 0, "variables": 0}', built_at = now() WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	return ids.Encode(ids.Snapshot, id)
}

// publishBody returns the body of a publication of the version, which
// acknowledges the report r, its seq and every warning of it.
func publishBody(t testing.TB, version string, r validation.Report) []byte {
	t.Helper()
	acks := []validation.Acknowledgement{}
	for _, f := range r.Findings {
		if f.Severity == validation.Warning {
			acks = append(acks, validation.Acknowledgement{Code: f.Code, EntityID: f.EntityID, Field: f.Field,
				Target: f.Target})
		}
	}
	return jsonBody(t, map[string]any{"version": version, "note": "Harbour intro", "acknowledged_seq": r.Seq,
		"acknowledged_warnings": acks})
}

// snapshotFrom returns the snapshot of the answer res, of the status, or
// fails the test.
func snapshotFrom(t *testing.T, res response, status int) snapshot.Snapshot {
	t.Helper()
	var s snapshot.Snapshot
	if res.status != status || json.Unmarshal(res.body, &s) != nil {
		t.Fatalf("%d %s, want %d with a snapshot", res.status, res.body, status)
	}
	return s
}

// gateFindings returns the seq and the findings of an answer of the publish
// gate, or fails the test.
func gateFindings(t *testing.T, res response) (int64, []validation.Finding) {
	t.Helper()
	var body struct {
		Error struct {
			Details struct {
				Seq      int64                `json:"seq"`
				Findings []validation.Finding `json:"findings"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(res.body, &body); err != nil {
		t.Fatal(err)
	}
	return body.Error.Details.Seq, body.Error.Details.Findings
}

// TestPublishThroughTheAPI checks publishing a project's draft through the
// API, as a client publishes it: the proposed version; a refusal of a draft
// with a warning that the request does not acknowledge, which lists it, and
// of a version that is not valid; a publication that acknowledges the
// report, answered with 202 and the snapshot, building, whose URL Location
// names, which its job builds once the workers run, and whose stored report
// is then readable, of the draft that the build read (FR-DEP-04 AC2); a
// version that is not newer; the publication's audit entry; and the refusal
// of a draft with an error, which lists it (AC1).
//
// Verifies: FR-DEP-04/AC1, FR-DEP-04/AC2, FR-TEN-07/AC2
func TestPublishThroughTheAPI(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	h := s.main
	p := createProject(t, h, owner, ws, "Harbour").ID
	var proposal snapshot.Proposal
	if res := call(h, http.MethodGet, snapshotsPath(p)+"/next-version", nil, owner.header(false)); res.status !=
		http.StatusOK || json.Unmarshal(res.body, &proposal) != nil || proposal != (snapshot.Proposal{Version: "0.1.0"}) {
		t.Fatalf("the first proposal: %d %s, want 0.1.0", res.status, res.body)
	}
	board, start, end, stray := ids.Encode(ids.Board, ids.New()), ids.Encode(ids.Node, ids.New()),
		ids.Encode(ids.Node, ids.New()), ids.Encode(ids.Node, ids.New())
	pos := map[string]int{"x": 0, "y": 0}
	element := func(n, title string, ending bool) wireCommand {
		return wireCommand{Type: "node.create", Payload: map[string]any{"id": n, "board_id": board, "type": "element",
			"kind": "narration", "title": title, "text": paragraph(title + " at night."), "position": pos,
			"is_ending": ending}}
	}
	sendBatch(t, h, owner, p,
		wireCommand{Type: "board.create", Payload: map[string]any{"id": board, "name": "Harbour"}},
		element(start, "Quay", false), element(end, "Home", true), element(stray, "Lighthouse", false),
		wireCommand{Type: "connection.create", Payload: map[string]any{"id": ids.Encode(ids.Connection, ids.New()),
			"from_node_id": start, "to_node_id": end}})
	r := validationReport(t, h, owner, p)
	if r.Counts.Warnings == 0 || r.Counts.Errors != 0 {
		t.Fatalf("the draft's report is %+v, want warnings alone", r.Counts)
	}

	res := call(h, http.MethodPost, snapshotsPath(p), jsonBody(t, map[string]any{"version": "1.0.0",
		"note": "Harbour intro", "acknowledged_seq": r.Seq}), owner.header(true))
	wantCode(t, "a publication that acknowledges nothing", res, http.StatusConflict, api.CodeWarningsUnacknowledged)
	if seq, findings := gateFindings(t, res); seq != r.Seq || len(findings) != r.Counts.Warnings ||
		slices.ContainsFunc(findings, func(f validation.Finding) bool { return f.Severity != validation.Warning }) {
		t.Errorf("the refusal lists %+v of seq %d, want the %d warnings of seq %d", findings, seq, r.Counts.Warnings,
			r.Seq)
	}
	wantCode(t, "a version with a leading zero", call(h, http.MethodPost, snapshotsPath(p),
		publishBody(t, "1.0.01", r), owner.header(true)), http.StatusUnprocessableEntity, api.CodeValidationFailed)

	res = call(h, http.MethodPost, snapshotsPath(p), publishBody(t, "1.0.0", r), owner.header(true))
	published := snapshotFrom(t, res, http.StatusAccepted)
	if published.Status != snapshot.Building || published.Version != "1.0.0" || published.ProjectID != p ||
		res.header.Get("Location") != snapshotsPath(p)+"/"+published.ID {
		t.Fatalf("the publication answers %+v at %q, want building at its URL", published,
			res.header.Get("Location"))
	}
	reportPath := snapshotsPath(p) + "/" + published.ID + "/validation"
	wantCode(t, "the report of a snapshot that is building", call(h, http.MethodGet, reportPath, nil,
		owner.header(false)), http.StatusNotFound, api.CodeNotFound)
	if n := countRows(t, d, `SELECT count(*) FROM audit_entry WHERE action = 'snapshot.publish'
		AND subject_id = $1::uuid AND actor_user_id = $2::uuid AND result = 'success'`,
		decode(t, ids.Snapshot, published.ID).String(), decode(t, ids.User, owner.body.UserID).String()); n != 1 {
		t.Errorf("the publication has %d audit entries, want 1", n)
	}

	startWorkers(t, s, d)
	var built snapshot.Snapshot
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		built = snapshotFrom(t, call(h, http.MethodGet, res.header.Get("Location"), nil, owner.header(false)),
			http.StatusOK)
		if built.Status != snapshot.Building {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the snapshot's job did not build it")
		}
	}
	if built.Status != snapshot.Ready || built.SourceSeq == nil || *built.SourceSeq != r.Seq || built.ContentHash == "" {
		t.Fatalf("the built snapshot is %+v, want ready, of seq %d", built, r.Seq)
	}
	res = call(h, http.MethodGet, reportPath, nil, owner.header(false))
	var stored validation.Report
	if res.status != http.StatusOK || json.Unmarshal(res.body, &stored) != nil {
		t.Fatalf("the stored report: %d %s", res.status, res.body)
	}
	if stored.Seq != r.Seq || stored.Counts != r.Counts || len(stored.Findings) != len(r.Findings) ||
		res.header.Get("Content-Language") != "en" {
		t.Errorf("the stored report is %+v, want the report that the author acknowledged, %+v", stored, r)
	}
	for i := range min(len(stored.Findings), len(r.Findings)) {
		if a, b := stored.Findings[i], r.Findings[i]; a.Code != b.Code || a.EntityID != b.EntityID ||
			a.Message != b.Message {
			t.Errorf("the stored report's finding %d is %+v, want %+v", i, a, b)
		}
	}

	if res := call(h, http.MethodGet, snapshotsPath(p)+"/next-version", nil, owner.header(false)); res.status !=
		http.StatusOK || json.Unmarshal(res.body, &proposal) != nil ||
		proposal != (snapshot.Proposal{Version: "1.0.1", LatestVersion: "1.0.0"}) {
		t.Errorf("the proposal after 1.0.0: %d %s, want 1.0.1", res.status, res.body)
	}
	res = call(h, http.MethodPost, snapshotsPath(p), publishBody(t, "1.0.0", r), owner.header(true))
	wantCode(t, "a version that is not newer", res, http.StatusConflict, api.CodeVersionNotNewer)
	var notNewer struct {
		Error struct {
			Details struct {
				Latest   string `json:"latest_version"`
				Proposed string `json:"proposed_version"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(res.body, &notNewer) != nil || notNewer.Error.Details.Latest != "1.0.0" ||
		notNewer.Error.Details.Proposed != "1.0.1" {
		t.Errorf("the refusal of a version that is not newer is %s", res.body)
	}

	// A jumper without a target is an error, which blocks publishing.
	jumper := ids.Encode(ids.Node, ids.New())
	sendBatch(t, h, owner, p, wireCommand{Type: "node.create", Payload: map[string]any{"id": jumper,
		"board_id": board, "type": "jumper", "position": pos}})
	r = validationReport(t, h, owner, p)
	res = call(h, http.MethodPost, snapshotsPath(p), publishBody(t, "1.1.0", r), owner.header(true))
	wantCode(t, "a publication of a draft with an error", res, http.StatusConflict, api.CodePublishBlocked)
	if seq, findings := gateFindings(t, res); seq != r.Seq || len(findings) != 1 ||
		findings[0].Code != validation.JumperBroken || findings[0].EntityID != jumper || findings[0].Message == "" {
		t.Errorf("the refusal lists %+v of seq %d, want the jumper's jumper_broken", findings, seq)
	}
	if n := countRows(t, d, "SELECT count(*) FROM snapshot"); n != 1 {
		t.Errorf("the project has %d snapshots, want the one publication's", n)
	}
}

// TestPublishRole checks that an owner, an admin, and an editor of a
// project may publish it, and a commenter and a viewer may not, and that a
// project whose publish_min_role is admin refuses its editors too.
//
// Verifies: FR-SNAP-01/AC3
func TestPublishRole(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	p := f.project(t, "")
	version := 0
	publish := func(r string) response {
		version++
		report := validationReport(t, f.s.main, f.as["owner"], p)
		return call(f.s.main, http.MethodPost, snapshotsPath(p), publishBody(t, "1.0."+strconv.Itoa(version), report),
			f.as[r].header(true))
	}
	for _, r := range []string{"owner", "admin", "editor"} {
		if res := publish(r); res.status != http.StatusAccepted {
			t.Errorf("the %s's publication: %d %s, want 202", r, res.status, res.body)
		}
	}
	for _, r := range []string{"commenter", "viewer"} {
		wantCode(t, "the "+r+"'s publication", publish(r), http.StatusForbidden, api.CodeInsufficientRole)
	}
	if _, err := f.admin.Exec(context.Background(), `UPDATE project SET settings = jsonb_set(settings,
		'{publish_min_role}', '"admin"') WHERE id = $1`, uuidOf(t, p)); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "the editor's publication where admins alone publish", publish("editor"), http.StatusForbidden,
		api.CodeInsufficientRole)
	for _, r := range []string{"owner", "admin"} {
		if res := publish(r); res.status != http.StatusAccepted {
			t.Errorf("the %s's publication where admins alone publish: %d %s, want 202", r, res.status, res.body)
		}
	}
	if n := countRows(t, f.d, "SELECT count(*) FROM snapshot WHERE project_id = $1::uuid",
		uuidOf(t, p).String()); n != 5 {
		t.Errorf("the project has %d snapshots, want the 5 publications'", n)
	}
}
