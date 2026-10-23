package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
)

// projectsPath returns the path of the projects of the workspace ws, and
// trashPath the path of its trash.
func projectsPath(ws string) string { return workspacesPath + "/" + ws + "/projects" }
func trashPath(ws string) string    { return workspacesPath + "/" + ws + "/trash/projects" }

// projectPath returns the path of the project id.
func projectPath(id string) string { return "/api/v1/projects/" + id }

// projectFrom returns the project of a response with status.
func projectFrom(t *testing.T, res response, status int) projectBody {
	t.Helper()
	var p projectBody
	if res.status != status || json.Unmarshal(res.body, &p) != nil {
		t.Fatalf("%d %s, want %d and a project", res.status, res.body, status)
	}
	return p
}

// createProject creates a project named name in the workspace ws, as si.
func createProject(t *testing.T, h http.Handler, si signedIn, ws, name string) projectBody {
	t.Helper()
	return projectFrom(t, call(h, http.MethodPost, projectsPath(ws), jsonBody(t, map[string]string{"name": name}),
		si.header(true)), http.StatusCreated)
}

// projectIDs returns the IDs of the projects on the first page of the list at
// path, which si reads, and which must answer 200.
func projectIDs(t *testing.T, h http.Handler, si signedIn, path string) []string {
	t.Helper()
	res := call(h, http.MethodGet, path, nil, si.header(false))
	var page struct {
		Items []projectBody `json:"items"`
	}
	if res.status != http.StatusOK || json.Unmarshal(res.body, &page) != nil {
		t.Fatalf("list %s: %d %s", path, res.status, res.body)
	}
	out := make([]string, len(page.Items))
	for i, p := range page.Items {
		out[i] = p.ID
	}
	return out
}

// auditActions returns the actions of the audit entries whose subject is the
// project id, in the order of their IDs, with their actors' IDs.
func auditActions(t *testing.T, d *dbtest.Database, id string) []string {
	t.Helper()
	rows, err := d.Admin(t).Query(context.Background(), `SELECT action, coalesce(actor_user_id::text, '')
		FROM audit_entry WHERE subject_id = $1 ORDER BY id`, decode(t, ids.Project, id))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var action, actor string
		if err := rows.Scan(&action, &actor); err != nil {
			t.Fatal(err)
		}
		out = append(out, action+" by "+actor)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestProjectLifecycle checks a project's lifecycle through the API: an
// admin creates it, with the workspace's default locale, as its only
// project, and renames, describes, and configures it, each change a command
// that increments its revision and seq; archiving it makes it read-only, so
// that every change but unarchiving and deleting it is answered with 409
// project_archived, archiving it again too, while it can still be read and
// listed; and unarchiving it lets it change again. Creating and deleting it
// are recorded in the audit log, with the admin as their actor.
//
// Verifies: FR-PROJ-01/AC2, FR-TEN-07/AC2
func TestProjectLifecycle(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	admin := newNamedAccount(t, s.main, "ad@example.com", "Ad")
	join(t, d, admin, ws, "admin")

	res := call(s.main, http.MethodPost, projectsPath(ws), jsonBody(t, map[string]string{"name": "  Harbour  ",
		"description": "Elara's first conversation\nat the harbour"}), admin.header(true))
	p := projectFrom(t, res, http.StatusCreated)
	if loc := res.header.Get("Location"); loc != projectPath(p.ID) {
		t.Errorf("Location = %q, want %q", loc, projectPath(p.ID))
	}
	if p.Name != "Harbour" || p.WorkspaceID != ws || p.DefaultLocale != "en" || p.PublishMinRole != "editor" ||
		p.Rev != 1 || p.Seq != 0 || p.Role != "admin" || p.ArchivedAt != nil || p.DeletedAt != nil {
		t.Errorf("the new project is %+v", p)
	}
	if got := projectIDs(t, s.main, owner, projectsPath(ws)); !slices.Equal(got, []string{p.ID}) {
		t.Errorf("the workspace's projects are %v, want %v", got, []string{p.ID})
	}
	if got := projectFrom(t, call(s.main, http.MethodGet, projectPath(p.ID), nil, owner.header(false)),
		http.StatusOK); got.Role != "owner" || got.ID != p.ID {
		t.Errorf("the project, as the owner reads it: %+v", got)
	}

	changed := projectFrom(t, call(s.main, http.MethodPatch, projectPath(p.ID), jsonBody(t, map[string]any{
		"name": "Lighthouse", "description": "", "default_locale": "cs-cz",
		"entry": map[string]string{"board_id": createBoard(t, s.main, admin, p.ID, "Harbour Intro")}}),
		admin.header(true)), http.StatusOK)
	if changed.Name != "Lighthouse" || changed.Description != "" || changed.DefaultLocale != "cs-CZ" ||
		changed.Entry.BoardID == "" || changed.Rev != 4 || changed.Seq != 2 {
		t.Errorf("the changed project is %+v, want three changes in one operation", changed)
	}

	archived := projectFrom(t, call(s.main, http.MethodPost, projectPath(p.ID)+"/archive", nil, admin.header(true)),
		http.StatusOK)
	if archived.ArchivedAt == nil || archived.Rev != 5 {
		t.Errorf("the archived project is %+v", archived)
	}
	for _, c := range []struct {
		what, method, path string
		body               any
	}{
		{"rename", http.MethodPatch, projectPath(p.ID), map[string]string{"name": "Harbour"}},
		{"raise publish_min_role", http.MethodPatch, projectPath(p.ID), map[string]string{"publish_min_role": "admin"}},
		{"archive again", http.MethodPost, projectPath(p.ID) + "/archive", nil},
	} {
		var body []byte
		if c.body != nil {
			body = jsonBody(t, c.body)
		}
		wantCode(t, c.what+" an archived project", call(s.main, c.method, c.path, body, admin.header(true)),
			http.StatusConflict, api.CodeProjectArchived)
	}
	if got := projectFrom(t, call(s.main, http.MethodGet, projectPath(p.ID), nil, owner.header(false)),
		http.StatusOK); got.Name != "Lighthouse" || got.Rev != 5 {
		t.Errorf("the archived project after the refused changes is %+v", got)
	}
	if got := projectIDs(t, s.main, owner, projectsPath(ws)); !slices.Equal(got, []string{p.ID}) {
		t.Errorf("the workspace's projects with one archived are %v", got)
	}

	unarchived := projectFrom(t, call(s.main, http.MethodPost, projectPath(p.ID)+"/unarchive", nil,
		admin.header(true)), http.StatusOK)
	if unarchived.ArchivedAt != nil || unarchived.Rev != 6 {
		t.Errorf("the unarchived project is %+v", unarchived)
	}
	// Unarchiving a project that is not archived changes nothing.
	if again := projectFrom(t, call(s.main, http.MethodPost, projectPath(p.ID)+"/unarchive", nil,
		admin.header(true)), http.StatusOK); again.Rev != 6 {
		t.Errorf("unarchiving the project again left it %+v", again)
	}
	if got := projectFrom(t, call(s.main, http.MethodPatch, projectPath(p.ID),
		jsonBody(t, map[string]string{"name": "Harbour"}), admin.header(true)), http.StatusOK); got.Name != "Harbour" {
		t.Errorf("the renamed project is %+v", got)
	}

	if res := call(s.main, http.MethodDelete, projectPath(p.ID), nil, admin.header(true)); res.status !=
		http.StatusNoContent {
		t.Fatalf("delete the project: %d %s", res.status, res.body)
	}
	actor := decode(t, ids.User, admin.body.UserID).String()
	if got, want := auditActions(t, d, p.ID), []string{"project.create by " + actor,
		"project.delete by " + actor}; !slices.Equal(got, want) {
		t.Errorf("the project's audit entries are %q, want %q", got, want)
	}
}

// TestProjectTrash checks that a deleted project stays in the trash, from
// which an admin restores it as it was, archived or not: meanwhile, every
// route of it but restoring it answers 404, and neither the workspace's list
// nor its usage counts it. Once its 30 days have passed, it cannot be
// restored, and the purge job deletes it with every row that refers to it,
// the operations and grants and tokens of its own included, and records
// that in the audit log, without an actor, and the projects of other
// workspaces stay.
//
// Verifies: FR-PROJ-01/AC3, FR-TEN-07/AC2
func TestProjectTrash(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, d, owner, ws := inviteServer(t, "open", nil)
	kept := createProject(t, s.main, owner, ws, "Kept")
	p := createProject(t, s.main, owner, ws, "Harbour")
	projectFrom(t, call(s.main, http.MethodPost, projectPath(p.ID)+"/archive", nil, owner.header(true)), http.StatusOK)
	// Rows that refer to the project: its operation, applied command, a
	// grant, and a token narrowed to it.
	member := newNamedAccount(t, s.main, "jo@example.com", "Jo")
	join(t, d, member, ws, "guest")
	admin := d.Admin(t)
	projectID, memberID := decode(t, ids.Project, p.ID), decode(t, ids.User, member.body.UserID)
	for _, c := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO project_grant (id, workspace_id, membership_id, project_id, role)
			SELECT $1, workspace_id, id, $2, 'viewer' FROM membership
			WHERE user_id = $3 AND workspace_id = (SELECT workspace_id FROM project WHERE id = $2)`,
			[]any{ids.New(), projectID, memberID}},
		{`INSERT INTO api_token (id, workspace_id, user_id, project_id, name, token_hash, scopes)
			SELECT $1, workspace_id, $3, $2, 'ci', $4, ARRAY['project:read'] FROM project WHERE id = $2`,
			[]any{ids.New(), projectID, memberID, isolationtest.TokenHash()}},
	} {
		if _, err := admin.Exec(ctx, c.sql, c.args...); err != nil {
			t.Fatalf("%s: %v", c.sql, err)
		}
	}

	if res := call(s.main, http.MethodDelete, projectPath(p.ID), nil, owner.header(true)); res.status !=
		http.StatusNoContent {
		t.Fatalf("delete the project: %d %s", res.status, res.body)
	}
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, projectPath(p.ID)},
		{http.MethodDelete, projectPath(p.ID)},
		{http.MethodPost, projectPath(p.ID) + "/unarchive"},
		{http.MethodPatch, projectPath(p.ID)},
	} {
		var body []byte
		if c.method == http.MethodPatch {
			body = jsonBody(t, map[string]string{"name": "Gone"})
		}
		wantCode(t, c.method+" "+c.path+" in the trash", call(s.main, c.method, c.path, body, owner.header(true)),
			http.StatusNotFound, api.CodeNotFound)
	}
	if got := projectIDs(t, s.main, owner, projectsPath(ws)); !slices.Equal(got, []string{kept.ID}) {
		t.Errorf("the workspace's projects are %v, want only the one kept", got)
	}
	if got := projectIDs(t, s.main, owner, trashPath(ws)); !slices.Equal(got, []string{p.ID}) {
		t.Errorf("the trash holds %v, want the deleted project", got)
	}
	if got := readUsage(t, s.main, owner, ws)["projects"]["used"]; got != 1.0 {
		t.Errorf("the workspace uses %v projects, want 1", got)
	}

	restored := projectFrom(t, call(s.main, http.MethodPost, projectPath(p.ID)+"/restore", nil, owner.header(true)),
		http.StatusOK)
	if restored.DeletedAt != nil || restored.HardDeleteAfter != nil || restored.ArchivedAt == nil {
		t.Errorf("the restored project is %+v, want it out of the trash and still archived", restored)
	}
	if got := projectIDs(t, s.main, owner, trashPath(ws)); len(got) != 0 {
		t.Errorf("after the restore, the trash holds %v", got)
	}
	actor := decode(t, ids.User, owner.body.UserID).String()
	if got, want := auditActions(t, d, p.ID), []string{"project.create by " + actor, "project.delete by " + actor,
		"project.restore by " + actor}; !slices.Equal(got, want) {
		t.Errorf("the project's audit entries are %q, want %q", got, want)
	}

	// Another workspace has a project whose time in the trash has passed
	// too, which the purge deletes in its own workspace's transaction, and
	// one that is not in the trash, which it keeps.
	other, gone, live := ids.New(), ids.New(), ids.New()
	for _, c := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Other', $2, 'unlimited')`,
			[]any{other, "other-" + other.String()}},
		{`INSERT INTO project (id, workspace_id, name, deleted_at, hard_delete_after)
			VALUES ($1, $2, 'Gone', now() - interval '31 days', now() - interval '1 day')`, []any{gone, other}},
		{`INSERT INTO project (id, workspace_id, name) VALUES ($1, $2, 'Live')`, []any{live, other}},
	} {
		if _, err := admin.Exec(ctx, c.sql, c.args...); err != nil {
			t.Fatalf("%s: %v", c.sql, err)
		}
	}
	if res := call(s.main, http.MethodDelete, projectPath(p.ID), nil, owner.header(true)); res.status !=
		http.StatusNoContent {
		t.Fatalf("delete the project again: %d %s", res.status, res.body)
	}
	if _, err := admin.Exec(ctx, `UPDATE project SET deleted_at = now() - interval '31 days',
		hard_delete_after = now() - interval '1 day' WHERE id = $1`, decode(t, ids.Project, p.ID)); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "restore once the 30 days have passed", call(s.main, http.MethodPost, projectPath(p.ID)+"/restore",
		nil, owner.header(true)), http.StatusNotFound, api.CodeNotFound)
	if got := projectIDs(t, s.main, owner, trashPath(ws)); len(got) != 0 {
		t.Errorf("once its time has passed, the trash holds %v", got)
	}

	purged, err := project.Purge(ctx, d.Open(t))
	if err != nil || purged != 2 {
		t.Fatalf("the purge deleted %d projects, %v; want 2", purged, err)
	}
	for _, id := range []ids.UUID{decode(t, ids.Project, p.ID), gone} {
		if left := projectRows(t, d, id); len(left) > 0 {
			t.Errorf("after the purge, rows refer to the project %v: %v", id, left)
		}
	}
	for _, id := range []ids.UUID{decode(t, ids.Project, kept.ID), live} {
		if left := projectRows(t, d, id); len(left) == 0 {
			t.Errorf("the purge deleted the project %v, which was not in the trash", id)
		}
	}
	if got, want := auditActions(t, d, p.ID), []string{"project.create by " + actor, "project.delete by " + actor,
		"project.restore by " + actor, "project.delete by " + actor, "project.purge by "}; !slices.Equal(got, want) {
		t.Errorf("the project's audit entries are %q, want %q", got, want)
	}
	if purged, err := project.Purge(ctx, d.Open(t)); err != nil || purged != 0 {
		t.Errorf("a second purge deleted %d projects, %v; want none", purged, err)
	}
}

// projectRows returns, for each table with a project_id column, how many of
// its rows refer to the project id, if any do, so that a table that a later
// release adds is checked too.
func projectRows(t *testing.T, d *dbtest.Database, id ids.UUID) []string {
	t.Helper()
	ctx := context.Background()
	admin := d.Admin(t)
	rows, err := admin.Query(ctx, `SELECT table_name FROM information_schema.columns
		WHERE table_schema = 'public' AND column_name = 'project_id' ORDER BY table_name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, table := range append(tables, "project") {
		column := "project_id"
		if table == "project" {
			column = "id"
		}
		var n int
		if err := admin.QueryRow(ctx, fmt.Sprintf("SELECT count(*) FROM %s WHERE %s = $1",
			pgx.Identifier{table}.Sanitize(), column), id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			out = append(out, fmt.Sprintf("%s: %d", table, n))
		}
	}
	return out
}

// TestPublishMinRole checks the project setting that governs publishing: a
// new project lets editors publish (publish_min_role editor), only an owner
// or an admin of the project may raise it to admin or lower it again, which
// an editor is refused with 403 insufficient_role, and each change is
// recorded in the audit log with the role before and after it, while a
// change to the role that it has already is not.
//
// Verifies: FR-SNAP-01/AC3, FR-SNAP-01/AC6
func TestPublishMinRole(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	editor := newNamedAccount(t, s.main, "ed@example.com", "Ed")
	join(t, d, editor, ws, "editor")
	p := createProject(t, s.main, owner, ws, "Harbour")
	if p.PublishMinRole != "editor" {
		t.Errorf("a new project's publish_min_role is %q, want editor", p.PublishMinRole)
	}
	raise := jsonBody(t, map[string]string{"publish_min_role": "admin"})
	wantCode(t, "an editor raises publish_min_role", call(s.main, http.MethodPatch, projectPath(p.ID), raise,
		editor.header(true)), http.StatusForbidden, api.CodeInsufficientRole)
	for _, role := range []string{"admin", "admin", "editor"} {
		got := projectFrom(t, call(s.main, http.MethodPatch, projectPath(p.ID),
			jsonBody(t, map[string]string{"publish_min_role": role}), owner.header(true)), http.StatusOK)
		if got.PublishMinRole != role {
			t.Errorf("publish_min_role is %q, want %q", got.PublishMinRole, role)
		}
	}
	rows, err := d.Admin(t).Query(context.Background(), `SELECT actor_user_id, metadata->>'previous_publish_min_role',
		metadata->>'publish_min_role' FROM audit_entry WHERE action = 'project.publish_policy_change'
		AND subject_id = $1 ORDER BY id`, decode(t, ids.Project, p.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var changes []string
	for rows.Next() {
		var actor ids.UUID
		var from, to string
		if err := rows.Scan(&actor, &from, &to); err != nil {
			t.Fatal(err)
		}
		if actor != decode(t, ids.User, owner.body.UserID) {
			t.Errorf("a change of publish_min_role is recorded with the actor %v, want the owner", actor)
		}
		changes = append(changes, from+" to "+to)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"editor to admin", "admin to editor"}; !slices.Equal(changes, want) {
		t.Errorf("the audit log records the changes %q, want %q", changes, want)
	}
}

// TestProjectsLimit checks the projects limit at each change that makes a
// project count: creating one, at the one site that creates a project, and
// unarchiving or restoring one. A workspace that has as many projects as
// STORYCREEP_WORKSPACE_LIMITS caps it at refuses another with 409
// limit_exceeded, naming the limit and the usage; an archived project and one
// in the trash do not count, so archiving or deleting one makes room; and
// unarchiving or restoring one into a full workspace is refused the same way,
// while restoring an archived one, which counts for nothing, is not.
//
// Verifies: FR-ENT-01/AC1
func TestProjectsLimit(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", map[string]string{"STORYCREEP_WORKSPACE_LIMITS": "projects=2"})
	wantFull := func(what string, res response) {
		t.Helper()
		wantCode(t, what, res, http.StatusConflict, api.CodeLimitExceeded)
		if d := res.details(t); d["key"] != "projects" || d["usage"] != 2.0 || d["limit"] != 2.0 {
			t.Errorf("%s: details %v, want the projects limit of 2 reached", what, d)
		}
	}
	create := func(name string) response {
		return call(s.main, http.MethodPost, projectsPath(ws), jsonBody(t, map[string]string{"name": name}),
			owner.header(true))
	}
	action := func(id, verb string) response {
		if verb == "delete" {
			return call(s.main, http.MethodDelete, projectPath(id), nil, owner.header(true))
		}
		return call(s.main, http.MethodPost, projectPath(id)+"/"+verb, nil, owner.header(true))
	}
	a := createProject(t, s.main, owner, ws, "A")
	b := createProject(t, s.main, owner, ws, "B")
	wantFull("create a third project", create("C"))

	projectFrom(t, action(a.ID, "archive"), http.StatusOK)
	c := projectFrom(t, create("C"), http.StatusCreated)
	wantFull("unarchive into a full workspace", action(a.ID, "unarchive"))

	if res := action(b.ID, "delete"); res.status != http.StatusNoContent {
		t.Fatalf("delete B: %d %s", res.status, res.body)
	}
	projectFrom(t, create("D"), http.StatusCreated)
	wantFull("restore into a full workspace", action(b.ID, "restore"))
	// An archived project counts for nothing, in the trash or out of it.
	if res := action(a.ID, "delete"); res.status != http.StatusNoContent {
		t.Fatalf("delete the archived A: %d %s", res.status, res.body)
	}
	projectFrom(t, action(a.ID, "restore"), http.StatusOK)
	if got := readUsage(t, s.main, owner, ws)["projects"]; got["used"] != 2.0 || got["limit"] != 2.0 {
		t.Errorf("the usage of projects is %v, want 2 of 2", got)
	}
	if res := action(c.ID, "delete"); res.status != http.StatusNoContent {
		t.Fatalf("delete C: %d %s", res.status, res.body)
	}
	projectFrom(t, action(b.ID, "restore"), http.StatusOK)
}

// TestProjectRevisionConflict checks that a change that expects a revision
// of the project that it no longer has is refused whole with 409
// revision_conflict, with the project as it is now, that one that expects
// the current revision is made, and that a body that names no field to
// change is answered with the project as it is, whatever revision it
// expects, but to a role that may not change it.
func TestProjectRevisionConflict(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	p := createProject(t, s.main, owner, ws, "Harbour")
	projectFrom(t, call(s.main, http.MethodPatch, projectPath(p.ID), jsonBody(t, map[string]any{"name": "Lighthouse",
		"expected_rev": p.Rev}), owner.header(true)), http.StatusOK)

	res := call(s.main, http.MethodPatch, projectPath(p.ID), jsonBody(t, map[string]any{"name": "Harbour",
		"description": "Stale", "expected_rev": p.Rev}), owner.header(true))
	wantCode(t, "a change against a stale revision", res, http.StatusConflict, api.CodeRevisionConflict)
	var details struct {
		Conflicts []conflictBody             `json:"conflicts"`
		States    map[string]json.RawMessage `json:"states"`
	}
	raw, _ := json.Marshal(res.details(t))
	if err := json.Unmarshal(raw, &details); err != nil || len(details.Conflicts) != 2 || len(details.States) != 1 {
		t.Fatalf("the conflicts are %s, want one of each command, and the project's state once", raw)
	}
	var state project.Project
	c := details.Conflicts[0]
	if err := json.Unmarshal(details.States[p.ID], &state); err != nil || c.ID != p.ID || c.ExpectedRev != p.Rev ||
		c.CurrentRev == nil || *c.CurrentRev != p.Rev+1 || state.Name != "Lighthouse" || state.Rev != p.Rev+1 {
		t.Errorf("the conflict is %+v, with the states %s", c, raw)
	}
	if got := projectFrom(t, call(s.main, http.MethodGet, projectPath(p.ID), nil, owner.header(false)),
		http.StatusOK); got.Name != "Lighthouse" || got.Description != "" {
		t.Errorf("after the refused change, the project is %+v", got)
	}

	for _, body := range []map[string]any{{}, {"expected_rev": p.Rev}} {
		if got := projectFrom(t, call(s.main, http.MethodPatch, projectPath(p.ID), jsonBody(t, body),
			owner.header(true)), http.StatusOK); got.Name != "Lighthouse" || got.Rev != p.Rev+1 || got.Role != "owner" {
			t.Errorf("a change of nothing, %v, answers %+v", body, got)
		}
	}
	editor := newNamedAccount(t, s.main, "ed@example.com", "Ed")
	join(t, d, editor, ws, "editor")
	wantCode(t, "an editor's change of nothing", call(s.main, http.MethodPatch, projectPath(p.ID),
		jsonBody(t, map[string]any{}), editor.header(true)), http.StatusForbidden, api.CodeInsufficientRole)
}

// TestGuestSeesGrantedProjects checks that a guest, who has no role in the
// workspace, lists only the projects granted to them, each with the grant's
// role, and that a direct URL to another project is answered with 404, as is
// the list to a user who is not a member at all, while a change of the
// granted project that the grant's role does not allow is answered with 403.
//
// Verifies: FR-TEN-04/AC2
func TestGuestSeesGrantedProjects(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	granted := createProject(t, s.main, owner, ws, "Granted")
	hidden := createProject(t, s.main, owner, ws, "Hidden")
	guest := newNamedAccount(t, s.main, "gu@example.com", "Gu")
	join(t, d, guest, ws, "guest")
	if _, err := d.Admin(t).Exec(context.Background(), `INSERT INTO project_grant
		(id, workspace_id, membership_id, project_id, role)
		SELECT $1, workspace_id, id, $2, 'editor' FROM membership WHERE user_id = $3 AND workspace_id = $4`,
		ids.New(), decode(t, ids.Project, granted.ID), decode(t, ids.User, guest.body.UserID),
		decode(t, ids.Workspace, ws)); err != nil {
		t.Fatal(err)
	}
	if got := projectIDs(t, s.main, guest, projectsPath(ws)); !slices.Equal(got, []string{granted.ID}) {
		t.Errorf("the guest lists %v, want only the granted project", got)
	}
	if got := projectFrom(t, call(s.main, http.MethodGet, projectPath(granted.ID), nil, guest.header(false)),
		http.StatusOK); got.Role != "editor" {
		t.Errorf("the guest's role on the granted project is %q, want editor", got.Role)
	}
	wantCode(t, "a guest reads a project not granted to them", call(s.main, http.MethodGet, projectPath(hidden.ID),
		nil, guest.header(false)), http.StatusNotFound, api.CodeNotFound)
	// The guest's role on the granted project, which tells them that it
	// exists, does not allow changing its settings, which is refused with 403
	// rather than 404.
	wantCode(t, "a guest with an editor grant renames the project", call(s.main, http.MethodPatch,
		projectPath(granted.ID), jsonBody(t, map[string]string{"name": "Renamed"}), guest.header(true)),
		http.StatusForbidden, api.CodeInsufficientRole)
	if got := projectIDs(t, s.main, owner, projectsPath(ws)); len(got) != 2 {
		t.Errorf("the owner lists %v, want both projects", got)
	}
	outsider := newNamedAccount(t, s.main, "out@example.com", "Out")
	wantCode(t, "an outsider lists the projects", call(s.main, http.MethodGet, projectsPath(ws), nil,
		outsider.header(false)), http.StatusNotFound, api.CodeNotFound)
}

// TestProjectFields checks that a project's fields are refused with 422
// validation_failed, naming the field, when their values are not valid, as a
// default locale whose canonical form is longer than 64 characters, although
// the tag sent is not, and that nothing changes then, and that a
// description's line breaks are kept as line feeds however the client writes
// them.
func TestProjectFields(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	board, node := ids.Encode(ids.Board, ids.New()), ids.Encode(ids.Node, ids.New())
	// 62 characters, whose canonical form begins sr-Latn for sh, and has 67.
	const longLocale = "sh-u-co-phonebk-ca-buddhist-nu-latn-cu-usd-tz-uslax-va-posix00"
	for _, c := range []struct {
		body  map[string]any
		field string
	}{
		{map[string]any{"name": "   "}, "body.name"},
		{map[string]any{"name": "A‮B"}, "body.name"},
		{map[string]any{"name": "Harbour", "description": "A\u0007"}, "body.description"},
		{map[string]any{"name": "Harbour", "default_locale": "not a locale"}, "body.default_locale"},
		{map[string]any{"name": "Harbour", "default_locale": "und"}, "body.default_locale"},
		{map[string]any{"name": "Harbour", "default_locale": "und-CZ"}, "body.default_locale"},
		{map[string]any{"name": "Harbour", "default_locale": longLocale}, "body.default_locale"},
	} {
		res := call(s.main, http.MethodPost, projectsPath(ws), jsonBody(t, c.body), owner.header(true))
		wantProblem(t, fmt.Sprintf("create with %v", c.body), res, c.field)
	}
	p := createProject(t, s.main, owner, ws, "Harbour")
	for _, c := range []struct {
		body  map[string]any
		field string
	}{
		{map[string]any{"name": ""}, "body.name"},
		// The entry's board is one of the project's (N32).
		{map[string]any{"entry": map[string]string{"board_id": board}}, "body.entry.board_id"},
		{map[string]any{"default_locale": "x-"}, "body.default_locale"},
		{map[string]any{"default_locale": "und-Latn"}, "body.default_locale"},
		{map[string]any{"default_locale": longLocale}, "body.default_locale"},
		{map[string]any{"entry": map[string]string{"board_id": node}}, "body.entry.board_id"},
		{map[string]any{"entry": map[string]string{"node_id": node}}, "body.entry.node_id"},
		{map[string]any{"entry": map[string]string{"board_id": board, "node_id": board}}, "body.entry.node_id"},
	} {
		res := call(s.main, http.MethodPatch, projectPath(p.ID), jsonBody(t, c.body), owner.header(true))
		wantProblem(t, fmt.Sprintf("change with %v", c.body), res, c.field)
	}
	if got := projectFrom(t, call(s.main, http.MethodGet, projectPath(p.ID), nil, owner.header(false)),
		http.StatusOK); got.Rev != 1 {
		t.Errorf("after the refused changes, the project is %+v", got)
	}
	// An entry that names a node has its board, of which the node is, and
	// an empty one removes it.
	board = createBoard(t, s.main, owner, p.ID, "Harbour")
	other := createBoard(t, s.main, owner, p.ID, "Pier")
	for _, n := range []string{node, createNode(t, s.main, owner, p.ID, other, "element", nil)} {
		wantProblem(t, "an entry of a node of no board or another board", call(s.main, http.MethodPatch,
			projectPath(p.ID), jsonBody(t, map[string]any{"entry": map[string]string{"board_id": board, "node_id": n}}),
			owner.header(true)), "body.entry.node_id")
	}
	node = createNode(t, s.main, owner, p.ID, board, "element", nil)
	for _, entry := range []map[string]string{{"board_id": board, "node_id": node}, {}} {
		got := projectFrom(t, call(s.main, http.MethodPatch, projectPath(p.ID),
			jsonBody(t, map[string]any{"entry": entry}), owner.header(true)), http.StatusOK)
		if got.Entry.BoardID != entry["board_id"] || got.Entry.NodeID != entry["node_id"] {
			t.Errorf("the entry is %+v, want %v", got.Entry, entry)
		}
	}
	// A description's line breaks written as CR LF, or as a CR alone, are kept
	// as line feeds, when the project is created and when it is changed.
	crlf := projectFrom(t, call(s.main, http.MethodPost, projectsPath(ws), jsonBody(t, map[string]string{
		"name": "Lighthouse", "description": "Elara waits\r\nat the harbour\r\n"}), owner.header(true)),
		http.StatusCreated)
	if crlf.Description != "Elara waits\nat the harbour" {
		t.Errorf("a description created with CR LF is %q", crlf.Description)
	}
	if got := projectFrom(t, call(s.main, http.MethodPatch, projectPath(crlf.ID),
		jsonBody(t, map[string]string{"description": "Elara waits\rat the lighthouse"}), owner.header(true)),
		http.StatusOK); got.Description != "Elara waits\nat the lighthouse" {
		t.Errorf("a description changed with a CR is %q", got.Description)
	}
}

// TestProjectListsPage checks that the list of a workspace's projects and the
// list of its trash each page through every project once, in the order of
// their IDs, with the next_cursor of each page, until a page without one.
func TestProjectListsPage(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	var all []string
	for _, name := range []string{"A", "B", "C"} {
		all = append(all, createProject(t, s.main, owner, ws, name).ID)
	}
	// Projects created in one millisecond have IDs in a random order.
	slices.SortFunc(all, func(a, b string) int {
		x, y := decode(t, ids.Project, a), decode(t, ids.Project, b)
		return bytes.Compare(x[:], y[:])
	})
	pages := func(path string) []string {
		t.Helper()
		var got []string
		cursor := ""
		for range len(all) + 1 {
			res := call(s.main, http.MethodGet, path+"?limit=2&cursor="+cursor, nil, owner.header(false))
			var page struct {
				Items      []projectBody `json:"items"`
				NextCursor *string       `json:"next_cursor"`
			}
			if res.status != http.StatusOK || json.Unmarshal(res.body, &page) != nil {
				t.Fatalf("list %s after %q: %d %s", path, cursor, res.status, res.body)
			}
			for _, p := range page.Items {
				got = append(got, p.ID)
			}
			if page.NextCursor == nil {
				return got
			}
			cursor = *page.NextCursor
		}
		t.Fatalf("list %s has more pages than projects", path)
		return nil
	}
	if got := pages(projectsPath(ws)); !slices.Equal(got, all) {
		t.Errorf("the pages of the projects hold %v, want %v", got, all)
	}
	for _, id := range all {
		if res := call(s.main, http.MethodDelete, projectPath(id), nil, owner.header(true)); res.status !=
			http.StatusNoContent {
			t.Fatalf("delete %s: %d %s", id, res.status, res.body)
		}
	}
	if got := pages(trashPath(ws)); !slices.Equal(got, all) {
		t.Errorf("the pages of the trash hold %v, want %v", got, all)
	}
}

// wantProblem fails the test unless res is 422 validation_failed with a
// problem at location.
func wantProblem(t *testing.T, what string, res response, location string) {
	t.Helper()
	wantCode(t, what, res, http.StatusUnprocessableEntity, api.CodeValidationFailed)
	problems, _ := res.details(t)["errors"].([]any)
	for _, p := range problems {
		if m, _ := p.(map[string]any); m["location"] == location {
			return
		}
	}
	t.Errorf("%s: the problems are %v, want one at %s", what, problems, location)
}
