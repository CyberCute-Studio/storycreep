package httpserver

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/CyberCute-Studio/storycreep/server/internal/account"
	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/authoring"
	"github.com/CyberCute-Studio/storycreep/server/internal/blob"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// workspaceExportsPath returns the path of the workspace's exports.
func workspaceExportsPath(ws string) string { return workspacesPath + "/" + ws + "/exports" }

// askWorkspaceExport asks for an export of the workspace as si.
func askWorkspaceExport(t *testing.T, h http.Handler, si signedIn, ws string) WorkspaceExportBody {
	t.Helper()
	res := call(h, http.MethodPost, workspaceExportsPath(ws), nil, si.header(true))
	if res.status != http.StatusAccepted {
		t.Fatalf("ask for a workspace export: %d %s", res.status, res.body)
	}
	var e WorkspaceExportBody
	if err := json.Unmarshal(res.body, &e); err != nil {
		t.Fatal(err)
	}
	if want := workspaceExportsPath(ws) + "/" + e.ID; res.header.Get("Location") != want {
		t.Errorf("Location is %q, want %q", res.header.Get("Location"), want)
	}
	return e
}

// readWorkspaceExport reads the export id of the workspace as si.
func readWorkspaceExport(t *testing.T, h http.Handler, si signedIn, ws, id string) WorkspaceExportBody {
	t.Helper()
	res := call(h, http.MethodGet, workspaceExportsPath(ws)+"/"+id, nil, si.header(false))
	if res.status != http.StatusOK {
		t.Fatalf("read the workspace export: %d %s", res.status, res.body)
	}
	var e WorkspaceExportBody
	if err := json.Unmarshal(res.body, &e); err != nil {
		t.Fatal(err)
	}
	return e
}

// unzipDeflated returns the members of the zip data by their paths, and fails the test
// on a member that is not deflated, which a reader that goes through a zip
// from its start refuses of a stored member with a data descriptor.
func unzipDeflated(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("the archive is not a zip: %v", err)
	}
	files := make(map[string][]byte, len(zr.File))
	for _, f := range zr.File {
		if f.Method != zip.Deflate {
			t.Errorf("%s has the zip method %d, want deflate", f.Name, f.Method)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		member, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[f.Name] = member
	}
	return files
}

// validateWorkspaceArchive validates the workspace archive's JSON files against
// the committed format's schema, with a validator that is not the generator's,
// and checks that the manifest's hashes, sizes, and counts are of its files,
// and that it holds no file that the manifest does not account for.
func validateWorkspaceArchive(t *testing.T, files map[string][]byte) authoring.WorkspaceManifest {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(authoring.WorkspaceSchema()))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource(authoring.WorkspaceSchemaID, doc); err != nil {
		t.Fatal(err)
	}
	schema, err := c.Compile(authoring.WorkspaceSchemaID)
	if err != nil {
		t.Fatal(err)
	}
	var m authoring.WorkspaceManifest
	if err := json.Unmarshal(files[authoring.WorkspaceManifestPath], &m); err != nil {
		t.Fatalf("the manifest: %v", err)
	}
	inst := map[string]any{}
	accounted := map[string]bool{authoring.ReadmePath: true}
	read := func(name string) {
		v, err := jsonschema.UnmarshalJSON(bytes.NewReader(files[name]))
		if err != nil {
			t.Fatalf("%s is not JSON: %v", name, err)
		}
		inst[name] = v
		accounted[name] = true
	}
	read(authoring.WorkspaceManifestPath)
	for _, s := range m.Sections {
		if _, ok := files[s.Path]; !ok {
			t.Errorf("the manifest lists %s, which the archive lacks", s.Path)
			continue
		}
		read(s.Path)
		if got := sha256Hex(files[s.Path]); got != s.SHA256 {
			t.Errorf("the manifest's hash of %s is %s, and the file's is %s", s.Path, s.SHA256, got)
		}
	}
	if err := schema.Validate(inst); err != nil {
		t.Errorf("the archive does not validate against its schema: %v", err)
	}
	for _, p := range m.Projects {
		data, ok := files[p.Path]
		if !ok {
			t.Errorf("the manifest lists %s, which the archive lacks", p.Path)
			continue
		}
		accounted[p.Path] = true
		if got := sha256Hex(data); got != p.SHA256 || int64(len(data)) != p.SizeBytes {
			t.Errorf("the manifest says %s has %d bytes of the hash %s, and it has %d of %s", p.Path, p.SizeBytes,
				p.SHA256, len(data), got)
		}
	}
	for _, a := range items[authoring.Asset](t, files, authoring.WorkspaceAssetsPath) {
		accounted[a.Path] = true
	}
	for name := range files {
		if !accounted[name] {
			t.Errorf("the archive holds %s, which its manifest does not account for", name)
		}
	}
	return m
}

// TestWorkspaceExport checks an export of a workspace, which its job builds:
// it holds every project of the workspace outside its trash, an archived one
// too, each as a full authoring export of its draft, with its comments, its
// release, and its assets, which validates as one; it names the project in the
// trash without holding it; it holds every member with their email address,
// role, and grants, the audit log, the export's own request among it, and the
// workspace's own assets; it says that it holds personal data; and its JSON
// validates against the format's schema.
//
// Verifies: FR-DATA-02/AC1, FR-DATA-02/AC3
func TestWorkspaceExport(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	h := f.s.main
	owner := f.as["owner"]
	lighthouse := f.project(t, "")
	f.node(t, lighthouse)
	cover := testPNG(t, 6, 6)
	coverID := upload(t, h, owner, lighthouse, cover, http.StatusCreated).ID
	comment := f.comment(t, lighthouse, owner)
	release := f.snapshot(t, lighthouse)
	archived := f.project(t, "archived")
	trashed := f.project(t, "deleted")
	f.grant(t, f.membership(t, "guest"), lighthouse, "editor")
	// An asset of the workspace itself, which no route makes yet.
	own := testPNG(t, 3, 3)
	sum := sha256.Sum256(own)
	ownID := ids.New()
	ownKey := decode(t, ids.Workspace, f.ws).String() + "/" + sha256Hex(own) + ".png"
	store, err := f.s.assets.Storage().Store(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(t.Context(), ownKey, own, "image/png"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.admin.Exec(t.Context(), `INSERT INTO asset (id, workspace_id, sha256, mime, bytes, width, height,
		storage_key, thumbnail_key) VALUES ($1, $2, $3, 'image/png', $4, 3, 3, $5, $5)`, ownID,
		decode(t, ids.Workspace, f.ws), sum[:], len(own), ownKey); err != nil {
		t.Fatal(err)
	}

	asked := askWorkspaceExport(t, h, owner, f.ws)
	if asked.Status != authoring.StatusQueued || !asked.ContainsPersonal || asked.WorkspaceID != f.ws ||
		asked.SizeBytes != nil || asked.ProjectCount != nil {
		t.Errorf("the export as asked for is %+v", asked)
	}
	startWorkers(t, f.s, f.d)
	var e WorkspaceExportBody
	for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		e = readWorkspaceExport(t, h, owner, f.ws, asked.ID)
		if e.Status == authoring.StatusReady {
			break
		}
		if e.Status == authoring.StatusFailed || time.Now().After(deadline) {
			t.Fatalf("the export ended as %+v", e)
		}
	}
	if e.SizeBytes == nil || e.ProjectCount == nil || *e.ProjectCount != 2 || e.FinishedAt == nil ||
		!strings.HasSuffix(e.FileName, "-workspace.zip") {
		t.Errorf("the ready export is %+v", e)
	}
	res := call(h, http.MethodGet, workspaceExportsPath(f.ws)+"/"+e.ID+"/download", nil, owner.header(false))
	if res.status != http.StatusOK {
		t.Fatalf("download the export: %d %s", res.status, res.body)
	}
	if e.SizeBytes == nil || int64(len(res.body)) != *e.SizeBytes {
		t.Errorf("the archive has %d bytes, and the export says %v", len(res.body), e.SizeBytes)
	}
	if got := res.header.Get("Content-Disposition"); !strings.HasPrefix(got, "attachment") ||
		!strings.Contains(got, e.FileName) {
		t.Errorf("Content-Disposition is %q, want an attachment named %s", got, e.FileName)
	}
	files := unzipDeflated(t, res.body)
	// The manifest last in the zip, as the format says, after what it names.
	zr, err := zip.NewReader(bytes.NewReader(res.body), int64(len(res.body)))
	if err != nil {
		t.Fatal(err)
	}
	if last := zr.File[len(zr.File)-1].Name; last != authoring.WorkspaceManifestPath {
		t.Errorf("the archive's last file is %s, want the manifest, %s", last, authoring.WorkspaceManifestPath)
	}
	m := validateWorkspaceArchive(t, files)
	if m.Format != authoring.WorkspaceFormat || m.FormatVersion != 1 || m.AuthoringFormatVersion != authoring.Version ||
		!m.PersonalData || m.Workspace.ID != f.ws || !bytes.Contains(m.Workspace.Settings, []byte(`"default_locale"`)) {
		t.Errorf("the manifest is %+v", m)
	}
	if !strings.Contains(string(files[authoring.ReadmePath]), "**This archive contains personal data.**") {
		t.Errorf("the README does not say that the archive holds personal data:\n%s", files[authoring.ReadmePath])
	}

	// AC1: every project of the workspace outside its trash, each a full
	// authoring export of its draft.
	var live []string
	rows, err := f.admin.Query(t.Context(), `SELECT id FROM project WHERE workspace_id = $1 AND deleted_at IS NULL
		ORDER BY id`, decode(t, ids.Workspace, f.ws))
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id ids.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		live = append(live, ids.Encode(ids.Project, id))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	var held []string
	for _, p := range m.Projects {
		held = append(held, p.ID)
		if p.Path != "projects/"+p.ID+".zip" || p.Archived != (p.ID == archived) {
			t.Errorf("the manifest's project is %+v", p)
		}
		project := unzipDeflated(t, files[p.Path])
		pm := validateArchive(t, project)
		if pm.Project.ID != p.ID || pm.Profile != authoring.ProfileFull || pm.Source.Kind != "draft" ||
			!pm.IncludesAssets || !pm.IncludesReleases {
			t.Errorf("the archive of %s has the manifest %+v", p.ID, pm)
		}
		if p.ID != lighthouse {
			continue
		}
		comments := items[authoring.Comment](t, project, "comments.json")
		if len(comments) != 1 || comments[0].ID != comment {
			t.Errorf("the comments of %s are %+v", p.ID, comments)
		}
		releases := items[authoring.Release](t, project, "releases/index.json")
		if len(releases) != 1 || releases[0].ID != release {
			t.Errorf("the releases of %s are %+v", p.ID, releases)
		}
		assets := items[authoring.Asset](t, project, "assets/index.json")
		if i := slices.IndexFunc(assets, func(a authoring.Asset) bool { return a.ID == coverID }); i < 0 ||
			!bytes.Equal(project[assets[i].Path], cover) {
			t.Errorf("the archive of %s does not hold its asset as it was uploaded: %+v", p.ID, assets)
		}
	}
	if !slices.Equal(held, live) || len(live) != 2 {
		t.Errorf("the archive holds the projects %q, want every project outside the trash, %q", held, live)
	}
	if len(m.TrashedProjects) != 1 || m.TrashedProjects[0].ID != trashed || m.TrashedProjects[0].DeletedAt == nil {
		t.Errorf("the trashed projects are %+v, want %s with when it was deleted", m.TrashedProjects, trashed)
	}
	if _, ok := files["projects/"+trashed+".zip"]; ok {
		t.Error("the archive holds the project in the trash")
	}

	// The members, with their email addresses, roles, and grants.
	members := items[authoring.WorkspaceMember](t, files, authoring.WorkspaceMembersPath)
	for _, r := range matrixRoles[:6] {
		si := f.as[r]
		i := slices.IndexFunc(members, func(m authoring.WorkspaceMember) bool { return m.UserID == si.body.UserID })
		switch {
		case i < 0:
			t.Errorf("the members lack the %s", r)
		case members[i].Role != r || members[i].Email == "" || members[i].DisplayName == "" ||
			members[i].MembershipID != f.membership(t, r):
			t.Errorf("the %s is %+v", r, members[i])
		case r == "guest" && !slices.Equal(members[i].Grants, []authoring.MemberGrant{{ProjectID: lighthouse,
			Role: "editor"}}):
			t.Errorf("the guest's grants are %+v", members[i].Grants)
		}
	}
	if slices.ContainsFunc(members, func(m authoring.WorkspaceMember) bool { return m.UserID == f.as["none"].body.UserID }) {
		t.Error("the members hold a user who is not a member")
	}
	if !bytes.Contains(files[authoring.WorkspaceMembersPath], []byte(`"email": "admin@example.com"`)) {
		t.Error("the members do not hold the owner's email address")
	}

	// The audit log, which holds the export's request with who asked, from
	// where.
	log := items[authoring.WorkspaceAuditEntry](t, files, authoring.WorkspaceAuditPath)
	i := slices.IndexFunc(log, func(a authoring.WorkspaceAuditEntry) bool { return a.Action == "workspace.export" })
	if i < 0 {
		t.Fatalf("the audit log lacks the export's request: %+v", log)
	}
	if got := log[i]; got.ActorUserID != owner.body.UserID || got.SubjectKind != "workspace" || got.SubjectID != f.ws ||
		got.Result != "success" || got.IP == "" ||
		!bytes.Contains(got.Metadata, []byte(asked.ID)) {
		t.Errorf("the export's entry is %+v", got)
	}
	if n := countRows(t, f.d, `SELECT count(*) FROM audit_entry WHERE workspace_id = $1::uuid
		AND created_at <= $2`, decode(t, ids.Workspace, f.ws).String(), m.ExportedAt); len(log) != n || n < 2 {
		t.Errorf("the audit log holds %d entries, and the workspace had %d as its reading began", len(log), n)
	}
	if !slices.IsSortedFunc(log, func(a, b authoring.WorkspaceAuditEntry) int { return a.CreatedAt.Compare(b.CreatedAt) }) {
		t.Error("the audit log is not in the order in which it was recorded")
	}

	// The workspace's own asset.
	assets := items[authoring.Asset](t, files, authoring.WorkspaceAssetsPath)
	if len(assets) != 1 || assets[0].ID != ids.Encode(ids.Asset, ownID) || !bytes.Equal(files[assets[0].Path], own) {
		t.Errorf("the workspace's assets are %+v", assets)
	}

	// The export is recorded in the audit log, as FR-TEN-07 lists.
	if n := countRows(t, f.d, `SELECT count(*) FROM audit_entry WHERE action = 'workspace.export'
		AND actor_user_id = $1::uuid`, decode(t, ids.User, owner.body.UserID).String()); n != 1 {
		t.Errorf("the audit log has %d entries of the export, want 1", n)
	}
}

// TestWorkspaceExportAccess checks who reads an export of a workspace: its
// download needs a session, an API token is refused, and another owner reads
// none of an owner's exports; the export expires seven days after it was asked
// for; an owner who is demoted reads their export no longer; a user has one on
// its way at a time; and an export that is not ready has no file.
//
// Verifies: FR-DATA-02/AC2
func TestWorkspaceExportAccess(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	h := f.s.main
	owner := f.as["owner"]
	f.project(t, "")
	queued := askWorkspaceExport(t, h, owner, f.ws)
	path := workspaceExportsPath(f.ws) + "/" + queued.ID
	if got := queued.ExpiresAt.Sub(queued.CreatedAt); got < 7*24*time.Hour-time.Minute || got > 7*24*time.Hour+time.Minute {
		t.Errorf("the export expires %v after it was asked for, want seven days", got)
	}

	again := call(h, http.MethodPost, workspaceExportsPath(f.ws), nil, owner.header(true))
	wantCode(t, "a second export on its way", again, http.StatusConflict, api.CodeExportInProgress)
	if got := again.details(t)["export_id"]; got != queued.ID {
		t.Errorf("details.export_id is %v, want %s", got, queued.ID)
	}
	notReady := call(h, http.MethodGet, path+"/download", nil, owner.header(false))
	wantCode(t, "the download of a queued export", notReady, http.StatusConflict, api.CodeExportNotReady)
	if got := notReady.details(t)["status"]; got != authoring.StatusQueued {
		t.Errorf("details.status is %v", got)
	}
	ws, id := decode(t, ids.Workspace, f.ws), decode(t, ids.Job, queued.ID)
	if err := f.s.authoring.BuildWorkspaceExport(t.Context(), ws, id); err != nil {
		t.Fatal(err)
	}

	// The download needs a session: none, and a token with every scope, are
	// refused.
	wantCode(t, "a download without a session", call(h, http.MethodGet, path+"/download", nil, nil),
		http.StatusUnauthorized, api.CodeUnauthenticated)
	tok := makeToken(t, h, owner, workspaceTokensPath(f.ws), "project:read", "project:write", "snapshot:read",
		"snapshot:write", "export:read")
	for _, c := range []struct{ method, path string }{{http.MethodPost, workspaceExportsPath(f.ws)},
		{http.MethodGet, workspaceExportsPath(f.ws)}, {http.MethodGet, path}, {http.MethodGet, path + "/download"}} {
		wantCode(t, "a token's "+c.method+" "+c.path, call(h, c.method, c.path, nil, bearer(tok.Secret)),
			http.StatusForbidden, api.CodeSessionRequired)
	}
	if res := call(h, http.MethodGet, path+"/download", nil, owner.header(false)); res.status != http.StatusOK {
		t.Errorf("the owner's download: %d %s", res.status, res.body)
	}

	// Another owner reads none of it, and an owner who is demoted reads their
	// own no longer.
	other := f.as["admin"]
	if res := changeRole(t, h, owner, f.ws, f.membership(t, "admin"), "owner"); res.status != http.StatusOK {
		t.Fatalf("make the admin an owner: %d %s", res.status, res.body)
	}
	for _, c := range []string{path, path + "/download"} {
		wantCode(t, "another owner's read of "+c, call(h, http.MethodGet, c, nil, other.header(false)),
			http.StatusNotFound, api.CodeNotFound)
	}
	if got := itemIDs(t, h, other.header(false), workspaceExportsPath(f.ws)); len(got) != 0 {
		t.Errorf("another owner lists the exports %q", got)
	}
	theirs := askWorkspaceExport(t, h, other, f.ws)
	if got := itemIDs(t, h, owner.header(false), workspaceExportsPath(f.ws)); !slices.Equal(got, []string{queued.ID}) {
		t.Errorf("the owner lists the exports %q, want their own, %s", got, queued.ID)
	}
	if res := changeRole(t, h, owner, f.ws, f.membership(t, "admin"), "admin"); res.status != http.StatusOK {
		t.Fatalf("demote the other owner: %d %s", res.status, res.body)
	}
	wantCode(t, "a demoted owner's read of their export", call(h, http.MethodGet,
		workspaceExportsPath(f.ws)+"/"+theirs.ID, nil, other.header(false)), http.StatusForbidden, api.CodeOwnerRequired)

	// The orphan reconciliation reads the archive's key as a row's while the
	// export exists, and as nobody's once it is gone.
	var key string
	if err := f.admin.QueryRow(t.Context(), `SELECT storage_key FROM workspace_export WHERE id = $1`,
		id).Scan(&key); err != nil {
		t.Fatal(err)
	}
	referenced := func() bool {
		t.Helper()
		got, err := queries.New(f.d.Admin(t)).ReferencedBlobs(t.Context(), queries.ReferencedBlobsParams{
			WorkspaceID: pgtype.UUID{Bytes: ws, Valid: true}, Keys: []string{key}})
		if err != nil {
			t.Fatal(err)
		}
		return len(got) == 1
	}
	if !referenced() {
		t.Errorf("the orphan reconciliation does not read %s as an export's", key)
	}
	// The expiry job has at least a year of hourly attempts, as an authoring
	// export's has.
	if n := countRows(t, f.d, `SELECT count(*) FROM river_job WHERE kind = 'workspace_export_expire'
		AND args->>'export_id' = $1 AND max_attempts >= 8760`, id.String()); n != 1 {
		t.Errorf("%d expiry jobs of the export have a year of attempts, want 1", n)
	}
	// Past its time an export is gone to its user, though its job has not
	// run, and its file stays referenced until the job deletes it.
	if _, err := f.admin.Exec(t.Context(), `UPDATE workspace_export SET expires_at = now() - interval '1 second'
		WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{path, path + "/download"} {
		wantCode(t, "a read past its time of "+c, call(h, http.MethodGet, c, nil, owner.header(false)),
			http.StatusNotFound, api.CodeNotFound)
	}
	if got := itemIDs(t, h, owner.header(false), workspaceExportsPath(f.ws)); len(got) != 0 {
		t.Errorf("the list holds %q past their time", got)
	}
	if !referenced() {
		t.Errorf("%s is no longer read as an export's before its job has run", key)
	}
	if err := f.s.authoring.ExpireWorkspaceExport(t.Context(), ws, id); err != nil {
		t.Fatal(err)
	}
	if referenced() {
		t.Errorf("%s is still read as an export's once the export is gone", key)
	}
	store, err := f.s.assets.Storage().Store(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Stat(t.Context(), key); !errors.Is(err, blob.ErrNotFound) {
		t.Errorf("the archive of the expired export is still stored: %v", err)
	}
	if n := countRows(t, f.d, `SELECT count(*) FROM workspace_export WHERE id = $1::uuid`, id.String()); n != 0 {
		t.Errorf("%d rows of the expired export are left", n)
	}
	if err := f.s.authoring.ExpireWorkspaceExport(t.Context(), ws, id); err != nil {
		t.Errorf("expiring an export that is gone: %v", err)
	}
	// Past its time, an export on its way holds the way no longer.
	if _, err := f.admin.Exec(t.Context(), `UPDATE workspace_export SET expires_at = now() - interval '1 second'
		WHERE id = $1`, decode(t, ids.Job, theirs.ID)); err != nil {
		t.Fatal(err)
	}
	if res := changeRole(t, h, owner, f.ws, f.membership(t, "admin"), "owner"); res.status != http.StatusOK {
		t.Fatalf("make the admin an owner again: %d %s", res.status, res.body)
	}
	if fresh := askWorkspaceExport(t, h, other, f.ws); fresh.ID == theirs.ID {
		t.Errorf("the new export is the one past its time, %s", fresh.ID)
	}
}

// TestWorkspaceExportFails checks that an export of a workspace whose
// project's asset has lost its file fails at once, as another attempt would
// find it missing too, and keeps nothing of what it stored.
func TestWorkspaceExportFails(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	h := f.s.main
	owner := f.as["owner"]
	p := f.project(t, "")
	upload(t, h, owner, p, testPNG(t, 4, 4), http.StatusCreated)
	var assetKey string
	if err := f.admin.QueryRow(t.Context(), `SELECT storage_key FROM asset WHERE project_id = $1`,
		decode(t, ids.Project, p)).Scan(&assetKey); err != nil {
		t.Fatal(err)
	}
	store, err := f.s.assets.Storage().Store(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(t.Context(), assetKey); err != nil {
		t.Fatal(err)
	}
	e := askWorkspaceExport(t, h, owner, f.ws)
	var key string
	if err := f.admin.QueryRow(t.Context(), `SELECT storage_key FROM workspace_export WHERE id = $1`,
		decode(t, ids.Job, e.ID)).Scan(&key); err != nil {
		t.Fatal(err)
	}
	startWorkers(t, f.s, f.d)
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		got := readWorkspaceExport(t, h, owner, f.ws, e.ID)
		if got.Status == authoring.StatusFailed {
			if got.FinishedAt == nil || got.SizeBytes != nil {
				t.Errorf("the failed export is %+v", got)
			}
			break
		}
		if got.Status == authoring.StatusReady || time.Now().After(deadline) {
			t.Fatalf("the export is %+v, want failed", got)
		}
	}
	// River records the job's end once its attempt returns, after the attempt
	// failed the export.
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		n := countRows(t, f.d, `SELECT count(*) FROM river_job WHERE kind = 'workspace_export_build'
			AND args->>'export_id' = $1 AND attempt = 1 AND state = 'cancelled'`, decode(t, ids.Job, e.ID).String())
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the build's job did not end after its one attempt")
		}
	}
	if _, err := store.Stat(t.Context(), key); !errors.Is(err, blob.ErrNotFound) {
		t.Errorf("the archive of the failed export is still stored: %v", err)
	}
}

// TestWorkspaceExportBeforeSetup checks that an export of a workspace is
// refused while first-run setup may yet choose the store, as an authoring
// export is.
func TestWorkspaceExportBeforeSetup(t *testing.T) {
	t.Parallel()
	s, d := pendingServer(t, inviteEnv("open", map[string]string{"STORYCREEP_ASSET_STORAGE": ""}))
	if _, err := account.Create(t.Context(), d.Open(t), account.NewHasher(), account.Registration{
		Email: "jo@example.com", DisplayName: "Jo", Password: secret.New(testPassword)}, false); err != nil {
		t.Fatal(err)
	}
	jo := signIn(t, s.main, "jo@example.com")
	res := call(s.main, http.MethodPost, workspacesPath, jsonBody(t, map[string]string{"name": "Harbour"}),
		jo.header(true))
	var ws struct {
		ID string `json:"id"`
	}
	if res.status != http.StatusCreated || json.Unmarshal(res.body, &ws) != nil {
		t.Fatalf("create a workspace before setup: %d %s, want 201", res.status, res.body)
	}
	wantCode(t, "a workspace export before setup", call(s.main, http.MethodPost, workspaceExportsPath(ws.ID), nil,
		jo.header(true)), http.StatusForbidden, api.CodeSetupRequired)
}
