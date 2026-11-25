package httpserver

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/CyberCute-Studio/storycreep/server/internal/account"
	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/authoring"
	"github.com/CyberCute-Studio/storycreep/server/internal/blob"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/personaldata"
	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// askPersonalExport asks for an export of si's personal data.
func askPersonalExport(t *testing.T, h http.Handler, si signedIn) PersonalExportBody {
	t.Helper()
	res := call(h, http.MethodPost, personalExportsPath, nil, si.header(true))
	if res.status != http.StatusAccepted {
		t.Fatalf("ask for a personal-data export: %d %s", res.status, res.body)
	}
	var e PersonalExportBody
	if err := json.Unmarshal(res.body, &e); err != nil {
		t.Fatal(err)
	}
	if want := personalExportsPath + "/" + e.ID; res.header.Get("Location") != want {
		t.Errorf("Location is %q, want %q", res.header.Get("Location"), want)
	}
	return e
}

// readPersonalExport reads si's export id.
func readPersonalExport(t *testing.T, h http.Handler, si signedIn, id string) PersonalExportBody {
	t.Helper()
	res := call(h, http.MethodGet, personalExportsPath+"/"+id, nil, si.header(false))
	if res.status != http.StatusOK {
		t.Fatalf("read the personal-data export: %d %s", res.status, res.body)
	}
	var e PersonalExportBody
	if err := json.Unmarshal(res.body, &e); err != nil {
		t.Fatal(err)
	}
	return e
}

// personalArchive asks for an export of si's personal data, builds it as its
// job does, and returns the files of the archive that si downloads.
func personalArchive(t *testing.T, s *server, si signedIn) map[string][]byte {
	t.Helper()
	e := askPersonalExport(t, s.main, si)
	if err := s.authoring.BuildPersonalExport(t.Context(), decode(t, ids.User, si.body.UserID),
		decode(t, ids.Job, e.ID)); err != nil {
		t.Fatal(err)
	}
	res := call(s.main, http.MethodGet, personalExportsPath+"/"+e.ID+"/download", nil, si.header(false))
	if res.status != http.StatusOK {
		t.Fatalf("download the personal-data export: %d %s", res.status, res.body)
	}
	return unzipDeflated(t, res.body)
}

// validatePersonalArchive validates the archive's JSON files against the
// committed format's schema, with a validator that is not the generator's,
// and checks that the manifest's hashes and counts are of its files, and that
// it holds no file that the manifest does not account for.
func validatePersonalArchive(t *testing.T, files map[string][]byte) authoring.PersonalManifest {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(authoring.PersonalSchema()))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource(authoring.PersonalSchemaID, doc); err != nil {
		t.Fatal(err)
	}
	schema, err := c.Compile(authoring.PersonalSchemaID)
	if err != nil {
		t.Fatal(err)
	}
	var m authoring.PersonalManifest
	if err := json.Unmarshal(files[personaldata.ManifestPath], &m); err != nil {
		t.Fatalf("the manifest: %v", err)
	}
	inst := map[string]any{}
	accounted := map[string]bool{personaldata.ReadmePath: true}
	read := func(name string) {
		v, err := jsonschema.UnmarshalJSON(bytes.NewReader(files[name]))
		if err != nil {
			t.Fatalf("%s is not JSON: %v", name, err)
		}
		inst[name] = v
		accounted[name] = true
	}
	read(personaldata.ManifestPath)
	for _, s := range m.Sections {
		if _, ok := files[s.Path]; !ok {
			t.Errorf("the manifest lists %s, which the archive lacks", s.Path)
			continue
		}
		read(s.Path)
		if got := sha256Hex(files[s.Path]); got != s.SHA256 {
			t.Errorf("the manifest's hash of %s is %s, and the file's is %s", s.Path, s.SHA256, got)
		}
		if s.Path != personaldata.AccountPath {
			if n := len(items[json.RawMessage](t, files, s.Path)); n != s.Items {
				t.Errorf("the manifest says %s holds %d items, and it holds %d", s.Path, s.Items, n)
			}
		}
	}
	if err := schema.Validate(inst); err != nil {
		t.Errorf("the archive does not validate against its schema: %v", err)
	}
	for name := range files {
		if !accounted[name] {
			t.Errorf("the archive holds %s, which its manifest does not account for", name)
		}
	}
	if !strings.Contains(string(files[personaldata.ReadmePath]), "**This archive contains personal data**") {
		t.Errorf("the README does not say that the archive holds personal data:\n%s", files[personaldata.ReadmePath])
	}
	return m
}

// TestPersonalDataExport checks an export of a user's personal data, which a
// job builds without an operator: it holds, for each row of the inventory of
// PRD §9.6, what the row's "In export" column says, from the workspace that
// the user is a member of and from another that they are not, and validates
// against the format's schema.
//
// Verifies: FR-GDPR-01/AC1, FR-GDPR-01/AC2, FR-TEN-07/AC2
func TestPersonalDataExport(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	openMaintenance(t, f.s, f.d)
	h := f.s.main
	ctx := context.Background()
	owner, user := f.as["owner"], f.as["editor"]
	u := decode(t, ids.User, user.body.UserID)
	ws := decode(t, ids.Workspace, f.ws)
	lighthouse := f.project(t, "")
	project := decode(t, ids.Project, lighthouse)
	membership := f.membership(t, "editor")
	f.grant(t, membership, lighthouse, "admin")
	// The account: a locale of the user's, a verified address.
	if _, err := f.admin.Exec(ctx, `UPDATE "user" SET locale = 'en-GB', email_verified_at = now() WHERE id = $1`,
		u); err != nil {
		t.Fatal(err)
	}
	// An email link, an API token, and a comment, a reply, and a deleted one.
	if _, err := f.admin.Exec(ctx, `INSERT INTO email_token (id, kind, user_id, email, locale, expires_at, sent_at)
		VALUES ($1, 'email_verification', $2, 'as-editor@example.com', 'en', now() + interval '1 day', now())`,
		ids.New(), u); err != nil {
		t.Fatal(err)
	}
	tok := makeToken(t, h, user, workspaceTokensPath(f.ws), "snapshot:read")
	root := f.comment(t, lighthouse, owner)
	reply := ids.New()
	if _, err := f.admin.Exec(ctx, `INSERT INTO comment (id, workspace_id, project_id, parent_id, thread_at, body,
		created_by) SELECT $1, workspace_id, project_id, id, thread_at, $2, $3 FROM comment WHERE id = $4`, reply,
		[]byte(paragraph("It does.")), u, decode(t, ids.Comment, root)); err != nil {
		t.Fatal(err)
	}
	own := f.comment(t, lighthouse, user)
	if _, err := f.admin.Exec(ctx, `UPDATE comment SET deleted_at = now() WHERE id = $1`,
		decode(t, ids.Comment, own)); err != nil {
		t.Fatal(err)
	}
	// A notification of the owner's mention of the user, which names who
	// mentioned them nowhere in the archive.
	notification := ids.New()
	if _, err := f.admin.Exec(ctx, `INSERT INTO notification (id, workspace_id, project_id, user_id, comment_id,
		actor_id, kind) VALUES ($1, $2, $3, $4, $5, $6, 'mention')`, notification, ws, project, u,
		decode(t, ids.Comment, root), decode(t, ids.User, owner.body.UserID)); err != nil {
		t.Fatal(err)
	}
	// What the user created and last changed.
	node := f.node(t, lighthouse)
	if _, err := f.admin.Exec(ctx, `UPDATE node SET created_by = $1, updated_by = $1 WHERE id = $2`, u,
		decode(t, ids.Node, node)); err != nil {
		t.Fatal(err)
	}
	// Another workspace that the user is no member of, which invited their
	// address and imported an archive that named them.
	other := ids.New()
	if _, err := f.admin.Exec(ctx, `INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Quay', $2, 'free')`,
		other, "quay-"+other.String()[:8]); err != nil {
		t.Fatal(err)
	}
	invite := ids.New()
	if _, err := f.admin.Exec(ctx, `INSERT INTO invite (id, workspace_id, email, role, token_hash, expires_at,
		created_by) VALUES ($1, $2, 'AS-EDITOR@example.com', 'viewer', $3, now() + interval '14 days', $4)`, invite,
		other, bytes.Repeat([]byte{7}, 32), decode(t, ids.User, owner.body.UserID)); err != nil {
		t.Fatal(err)
	}
	imported := ids.New()
	if _, err := f.admin.Exec(ctx, `INSERT INTO "user" (id, email, display_name, status)
		VALUES ($1, $2, 'Editor', 'imported')`, imported, imported.String()+"@imported.invalid"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.admin.Exec(ctx, `INSERT INTO imported_user (workspace_id, source_user_id, display_name, user_id)
		VALUES ($1, $2, 'Editor of old', $3)`, other, u, imported); err != nil {
		t.Fatal(err)
	}
	// What the owner did to the user, which the archive holds as a role's.
	if res := changeRole(t, h, owner, f.ws, membership, "commenter"); res.status != http.StatusOK {
		t.Fatalf("change the user's role: %d %s", res.status, res.body)
	}

	asked := askPersonalExport(t, h, user)
	if asked.Status != authoring.StatusQueued || !asked.ContainsPersonal || asked.SizeBytes != nil {
		t.Errorf("the export as asked for is %+v", asked)
	}
	// AC1: the job builds it, without an operator.
	startWorkers(t, f.s, f.d)
	var e PersonalExportBody
	for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		e = readPersonalExport(t, h, user, asked.ID)
		if e.Status == authoring.StatusReady {
			break
		}
		if e.Status == authoring.StatusFailed || time.Now().After(deadline) {
			t.Fatalf("the export ended as %+v", e)
		}
	}
	if e.SizeBytes == nil || e.FinishedAt == nil || !strings.HasPrefix(e.FileName, "personal-data-") ||
		!strings.HasSuffix(e.FileName, ".zip") {
		t.Errorf("the ready export is %+v", e)
	}
	res := call(h, http.MethodGet, personalExportsPath+"/"+e.ID+"/download", nil, user.header(false))
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
	zr, err := zip.NewReader(bytes.NewReader(res.body), int64(len(res.body)))
	if err != nil {
		t.Fatal(err)
	}
	if last := zr.File[len(zr.File)-1].Name; last != personaldata.ManifestPath {
		t.Errorf("the archive's last file is %s, want the manifest", last)
	}
	m := validatePersonalArchive(t, files)
	if m.Format != authoring.PersonalFormat || m.FormatVersion != 1 || !m.PersonalData ||
		m.UserID != user.body.UserID {
		t.Errorf("the manifest is %+v", m)
	}

	// AC2: each row of the inventory, as its "In export" column says.
	ownerID := owner.body.UserID
	checks := map[string]func(){
		"Account": func() {
			var a authoring.PersonalAccount
			if err := json.Unmarshal(files[personaldata.AccountPath], &a); err != nil {
				t.Fatal(err)
			}
			if a.ID != user.body.UserID || a.Email != "as-editor@example.com" || a.DisplayName != "Editor" ||
				a.Locale != "en-GB" || a.Status != "active" || a.EmailVerifiedAt == nil || a.InstanceAdmin ||
				a.CreatedAt.IsZero() {
				t.Errorf("the account is %+v", a)
			}
		},
		"Credentials": func() {
			var a authoring.PersonalAccount
			if err := json.Unmarshal(files[personaldata.AccountPath], &a); err != nil {
				t.Fatal(err)
			}
			if !a.Credentials.PasswordSet || a.Credentials.PasswordChangedAt == nil {
				t.Errorf("the credentials are %+v, want a password set when the account was made", a.Credentials)
			}
			if bytes.Contains(files[personaldata.AccountPath], []byte("$argon2")) {
				t.Error("the account holds the password's hash")
			}
		},
		"Email links": func() {
			links := items[authoring.PersonalEmailLink](t, files, personaldata.EmailLinksPath)
			if !slices.ContainsFunc(links, func(l authoring.PersonalEmailLink) bool {
				return l.Kind == "email_verification" && l.SentAt != nil && l.UsedAt == nil && !l.ExpiresAt.IsZero()
			}) {
				t.Errorf("the email links are %+v", links)
			}
			if bytes.Contains(files[personaldata.EmailLinksPath], []byte("as-editor@example.com")) {
				t.Error("an email link holds the address that it was sent to")
			}
		},
		"Sessions": func() {
			sessions := items[authoring.PersonalSession](t, files, personaldata.SessionsPath)
			if len(sessions) == 0 || sessions[0].IP == "" || sessions[0].CreatedAt.IsZero() ||
				sessions[0].LastSeenAt.IsZero() || sessions[0].ExpiresAt.IsZero() {
				t.Errorf("the sessions are %+v", sessions)
			}
		},
		"API tokens": func() {
			tokens := items[authoring.PersonalToken](t, files, personaldata.TokensPath)
			if len(tokens) != 1 || tokens[0].ID != tok.ID || tokens[0].Name == "" || tokens[0].WorkspaceID != f.ws ||
				!slices.Equal(tokens[0].Scopes, []string{"snapshot:read"}) {
				t.Errorf("the API tokens are %+v", tokens)
			}
		},
		"Memberships and grants": func() {
			// The fixture's workspace, and the user's personal one, which their
			// first sign-in gave them.
			ms := items[authoring.PersonalMembership](t, files, personaldata.MembershipsPath)
			i := slices.IndexFunc(ms, func(m authoring.PersonalMembership) bool { return m.WorkspaceID == f.ws })
			if len(ms) != 2 || i < 0 || ms[i].MembershipID != membership || ms[i].Role != "commenter" ||
				ms[i].WorkspaceName == "" || !slices.Equal(ms[i].Grants, []authoring.MemberGrant{{ProjectID: lighthouse,
				Role: "admin"}}) || ms[1-i].Role != "owner" {
				t.Errorf("the memberships are %+v", ms)
			}
		},
		"Audit entries about the user": func() {
			acted := items[authoring.PersonalActedEntry](t, files, personaldata.ActedPath)
			i := slices.IndexFunc(acted, func(a authoring.PersonalActedEntry) bool {
				return a.Action == "account.data_export"
			})
			if i < 0 || acted[i].ActorUserID != user.body.UserID || acted[i].IP == "" ||
				!bytes.Contains(acted[i].Metadata, []byte(asked.ID)) {
				t.Errorf("the audit entries of what the user did lack the export's request: %+v", acted)
			}
			if !slices.ContainsFunc(acted, func(a authoring.PersonalActedEntry) bool {
				return a.Action == "auth.login" && a.Result == "success" && a.IP != ""
			}) {
				t.Errorf("the audit entries of what the user did lack their sign-in: %+v", acted)
			}
			on := items[authoring.PersonalActedOnEntry](t, files, personaldata.ActedOnPath)
			if !slices.ContainsFunc(on, func(a authoring.PersonalActedOnEntry) bool {
				return a.Action == "membership.role_change" && a.Result == "success" &&
					a.Actor == personaldata.LabelWorkspaceAdmin && !a.CreatedAt.IsZero()
			}) {
				t.Errorf("the audit entries of what others did lack the role's change: %+v", on)
			}
		},
		"Comments": func() {
			comments := items[authoring.PersonalComment](t, files, personaldata.CommentsPath)
			var got []string
			for _, c := range comments {
				got = append(got, c.ID)
				if c.Target.Kind != "node" || c.ProjectID != lighthouse || c.WorkspaceID != f.ws || len(c.Body) == 0 {
					t.Errorf("the comment is %+v", c)
				}
			}
			// In the order of their IDs: the reply was made first.
			if want := []string{ids.Encode(ids.Comment, reply), own}; !slices.Equal(got, want) {
				t.Errorf("the comments are %q, want the user's reply and their deleted comment, %q", got, want)
			}
			for _, c := range comments {
				switch c.ID {
				case own:
					if c.DeletedAt == nil || c.ParentID != "" {
						t.Errorf("the deleted comment is %+v", c)
					}
				default:
					if c.ParentID != root || c.ThreadID != root || !strings.Contains(string(c.Body), "It does.") {
						t.Errorf("the reply is %+v", c)
					}
				}
			}
		},
		"Notifications": func() {
			ns := items[authoring.PersonalNotification](t, files, personaldata.NotificationsPath)
			if len(ns) != 1 || ns[0].ID != ids.Encode(ids.Notification, notification) || ns[0].CommentID != root ||
				ns[0].Kind != "mention" || ns[0].ReadAt != nil {
				t.Errorf("the notifications are %+v", ns)
			}
		},
		"Authored content attribution": func() {
			refs := items[authoring.PersonalReference](t, files, personaldata.AttributionPath)
			for _, rel := range []string{"created", "updated"} {
				if !slices.ContainsFunc(refs, func(r authoring.PersonalReference) bool {
					return r.Entity == "node" && r.ID == node && r.Relation == rel && r.ProjectID == lighthouse &&
						r.WorkspaceID == f.ws && !r.At.IsZero()
				}) {
					t.Errorf("the attribution lacks that the user %s the node: %+v", rel, refs)
				}
			}
			if bytes.Contains(files[personaldata.AttributionPath], []byte(`"text"`)) {
				t.Error("the attribution holds a node's content")
			}
		},
		"Invites addressed to the user's email": func() {
			invites := items[authoring.PersonalInvite](t, files, personaldata.InvitesPath)
			if len(invites) != 1 || invites[0].ID != ids.Encode(ids.Invite, invite) ||
				invites[0].WorkspaceID != ids.Encode(ids.Workspace, other) || invites[0].WorkspaceName != "Quay" ||
				invites[0].Kind != "email" || invites[0].Role != "viewer" || invites[0].Status != "open" {
				t.Errorf("the invitations are %+v", invites)
			}
		},
		"Imported identities": func() {
			idents := items[authoring.PersonalIdentity](t, files, personaldata.IdentitiesPath)
			if len(idents) != 1 || idents[0].WorkspaceID != ids.Encode(ids.Workspace, other) ||
				idents[0].DisplayName != "Editor of old" {
				t.Errorf("the imported identities are %+v", idents)
			}
		},
		"Billing (hosted)": func() {
			if !strings.Contains(string(files[personaldata.ReadmePath]), "payment provider") {
				t.Error("the README does not point to the payment provider's export")
			}
		},
	}
	for _, row := range personaldata.Inventory {
		check, ok := checks[row.Data]
		if !ok {
			t.Errorf("the inventory's row %q has no check", row.Data)
			continue
		}
		check()
	}
	// The owner, who mentioned the user, made the invitation, and changed the
	// user's role, is named nowhere.
	for name, data := range files {
		for _, v := range []string{ownerID, decode(t, ids.User, ownerID).String(), "admin@example.com"} {
			if bytes.Contains(data, []byte(v)) {
				t.Errorf("%s holds %s of the owner's", name, v)
			}
		}
	}

	// The export is recorded in the audit log, as FR-TEN-07 lists.
	if n := countRows(t, f.d, `SELECT count(*) FROM audit_entry WHERE action = 'account.data_export'
		AND actor_user_id = $1::uuid AND subject_id = $1::uuid AND workspace_id IS NULL`, u.String()); n != 1 {
		t.Errorf("the audit log has %d entries of the export, want 1", n)
	}
}

// encodings returns the forms in which a secret's bytes could be written into
// a file: as they are, in hexadecimal, and in base64.
func encodings(b []byte) []string {
	return []string{string(b), hex.EncodeToString(b), strings.ToUpper(hex.EncodeToString(b)),
		base64.StdEncoding.EncodeToString(b), base64.RawStdEncoding.EncodeToString(b),
		base64.URLEncoding.EncodeToString(b), base64.RawURLEncoding.EncodeToString(b)}
}

// TestPersonalDataExportHoldsNoCredential checks that the archive holds no
// credential material: neither the hash of the password, nor that of a
// session's, an API token's, an email link's, or an invitation's secret, each
// seeded with a known value, as it is or in any encoding (FR-AUTH-01 AC4).
// The 2FA secret and the recovery codes' hashes join it with 2FA (plan item
// 123).
//
// Verifies: FR-GDPR-01/AC3
func TestPersonalDataExportHoldsNoCredential(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	openMaintenance(t, f.s, f.d)
	ctx := context.Background()
	user := f.as["editor"]
	u := decode(t, ids.User, user.body.UserID)
	marker := func(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }
	session, token, link, invite := marker(0xa1), marker(0xb2), marker(0xc3), marker(0xd4)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO session (id, user_id, token_hash, expires_at) VALUES ($1, $2, $3, now() + interval '1 day')`,
			[]any{ids.New(), u, session}},
		{`INSERT INTO api_token (id, workspace_id, user_id, name, token_hash, scopes) VALUES ($1, $2, $3, 'Seeded', $4,
			ARRAY['project:read'])`, []any{ids.New(), decode(t, ids.Workspace, f.ws), u, token}},
		{`INSERT INTO email_token (id, kind, user_id, token_hash, expires_at) VALUES ($1, 'password_reset', $2, $3,
			now() + interval '1 hour')`, []any{ids.New(), u, link}},
		{`INSERT INTO invite (id, workspace_id, email, role, token_hash, expires_at) VALUES ($1, $2,
			'as-editor@example.com', 'viewer', $3, now() + interval '14 days')`,
			[]any{ids.New(), decode(t, ids.Workspace, f.ws), invite}},
	} {
		if _, err := f.admin.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	// The password's hash, which the user signed in with, and the hash of the
	// session that the sign-in started.
	var password string
	var signedInHash []byte
	if err := f.admin.QueryRow(ctx, `SELECT password_hash FROM "user" WHERE id = $1`, u).Scan(&password); err != nil {
		t.Fatal(err)
	}
	if err := f.admin.QueryRow(ctx, `SELECT token_hash FROM session WHERE user_id = $1 AND token_hash <> $2
		ORDER BY created_at LIMIT 1`, u, session).Scan(&signedInHash); err != nil {
		t.Fatal(err)
	}
	files := personalArchive(t, f.s, user)
	validatePersonalArchive(t, files)
	// The encoded hash whole, and each of its parts that is long enough to be
	// the salt or the hash itself.
	secrets := map[string][]string{"the password's hash": {password}}
	for _, part := range strings.Split(password, "$") {
		if len(part) >= 16 {
			secrets["the password's hash"] = append(secrets["the password's hash"], part)
		}
	}
	for name, b := range map[string][]byte{"the seeded session's hash": session, "the sign-in's session's hash": signedInHash,
		"the API token's hash": token, "the email link's hash": link, "the invitation's hash": invite} {
		secrets[name] = encodings(b)
	}
	for name, data := range files {
		for what, forms := range secrets {
			for _, v := range forms {
				if bytes.Contains(data, []byte(v)) {
					t.Errorf("%s holds %s, as %q", name, what, v)
				}
			}
		}
	}
	// The seeded rows are in it, without their secrets.
	if tokens := items[authoring.PersonalToken](t, files, personaldata.TokensPath); len(tokens) != 1 ||
		tokens[0].Name != "Seeded" {
		t.Errorf("the API tokens are %+v", tokens)
	}
	if sessions := items[authoring.PersonalSession](t, files, personaldata.SessionsPath); len(sessions) != 2 {
		t.Errorf("the sessions are %+v, want the sign-in's and the seeded one", sessions)
	}
}

// TestPersonalDataExportOfARemovedMember checks the fixture of FR-GDPR-01
// AC4: an admin removes the user from a workspace, and the user then signs in
// themselves. The user's archive holds their sign-in with their IP address and
// user agent, and the removal with its action, result, and time, and the
// label of a workspace admin, and holds neither the admin's ID, nor their IP
// address, nor their user agent, anywhere.
//
// Verifies: FR-GDPR-01/AC4
func TestPersonalDataExportOfARemovedMember(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	openMaintenance(t, f.s, f.d)
	h := f.s.main
	admin, user := f.as["admin"], f.as["editor"]
	const adminIP, adminAgent = "198.51.100.71", "AdminConsole/9.9 (removal)"
	const userIP, userAgent = "203.0.113.45", "ReaderOwn/1.2 (sign-in)"
	removal := send(h, http.MethodDelete, memberPath(f.ws, f.membership(t, "editor")), nil, "",
		with(admin.header(true), http.Header{"X-Forwarded-For": {adminIP}, "User-Agent": {adminAgent}}))
	if removal.status != http.StatusNoContent {
		t.Fatalf("remove the user: %d %s", removal.status, removal.body)
	}
	again := signedInFrom(t, send(h, http.MethodPost, loginPath, credentials(t, "as-editor@example.com",
		testPassword), "", http.Header{"X-Forwarded-For": {userIP}, "User-Agent": {userAgent}}))
	files := personalArchive(t, f.s, again)
	validatePersonalArchive(t, files)

	acted := items[authoring.PersonalActedEntry](t, files, personaldata.ActedPath)
	if !slices.ContainsFunc(acted, func(a authoring.PersonalActedEntry) bool {
		return a.Action == "auth.login" && a.Result == "success" && a.ActorUserID == user.body.UserID &&
			a.IP == userIP && a.UserAgent == userAgent
	}) {
		t.Errorf("the archive lacks the user's sign-in with their IP address and user agent: %+v", acted)
	}
	on := items[authoring.PersonalActedOnEntry](t, files, personaldata.ActedOnPath)
	i := slices.IndexFunc(on, func(a authoring.PersonalActedOnEntry) bool { return a.Action == "membership.remove" })
	if i < 0 {
		t.Fatalf("the archive lacks the removal: %+v", on)
	}
	if got := on[i]; got.Result != "success" || got.Actor != "a workspace admin" || got.CreatedAt.IsZero() {
		t.Errorf("the removal is %+v", got)
	}
	if !bytes.Contains(files[personaldata.ActedOnPath], []byte(`"actor": "a workspace admin"`)) &&
		!bytes.Contains(files[personaldata.ActedOnPath], []byte(`"actor":"a workspace admin"`)) {
		t.Errorf("the removal is not labelled as a workspace admin's:\n%s", files[personaldata.ActedOnPath])
	}
	adminID := admin.body.UserID
	for name, data := range files {
		for _, v := range []string{adminID, decode(t, ids.User, adminID).String(), adminIP, adminAgent} {
			if bytes.Contains(data, []byte(v)) {
				t.Errorf("%s holds %q, of the admin's", name, v)
			}
		}
	}
	// The user is a member of no workspace that the fixture made.
	if ms := items[authoring.PersonalMembership](t, files, personaldata.MembershipsPath); slices.ContainsFunc(ms,
		func(m authoring.PersonalMembership) bool { return m.WorkspaceID == f.ws }) {
		t.Errorf("the archive holds the membership that was removed: %+v", ms)
	}
}

// TestPersonalDataExportAccess checks who reads a personal-data export: only
// its user, signed in, as an API token is refused and another user finds none;
// the export expires seven days after it was asked for; a user has one on its
// way at a time; and an export that is not ready has no file.
//
// Verifies: FR-GDPR-01/AC1
func TestPersonalDataExportAccess(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	openMaintenance(t, f.s, f.d)
	h := f.s.main
	user, other := f.as["editor"], f.as["viewer"]
	queued := askPersonalExport(t, h, user)
	path := personalExportsPath + "/" + queued.ID
	if got := queued.ExpiresAt.Sub(queued.CreatedAt); got < 7*24*time.Hour-time.Minute || got > 7*24*time.Hour+time.Minute {
		t.Errorf("the export expires %v after it was asked for, want seven days", got)
	}
	again := call(h, http.MethodPost, personalExportsPath, nil, user.header(true))
	wantCode(t, "a second export on its way", again, http.StatusConflict, api.CodeExportInProgress)
	if got := again.details(t)["export_id"]; got != queued.ID {
		t.Errorf("details.export_id is %v, want %s", got, queued.ID)
	}
	notReady := call(h, http.MethodGet, path+"/download", nil, user.header(false))
	wantCode(t, "the download of a queued export", notReady, http.StatusConflict, api.CodeExportNotReady)
	u, id := decode(t, ids.User, user.body.UserID), decode(t, ids.Job, queued.ID)
	if err := f.s.authoring.BuildPersonalExport(t.Context(), u, id); err != nil {
		t.Fatal(err)
	}

	// Its routes need a session: none, and a token with every scope, are
	// refused.
	wantCode(t, "a download without a session", call(h, http.MethodGet, path+"/download", nil, nil),
		http.StatusUnauthorized, api.CodeUnauthenticated)
	tok := makeToken(t, h, user, workspaceTokensPath(f.ws), "project:read", "project:write", "snapshot:read",
		"snapshot:write", "export:read")
	for _, c := range []struct{ method, path string }{{http.MethodPost, personalExportsPath},
		{http.MethodGet, personalExportsPath}, {http.MethodGet, path}, {http.MethodGet, path + "/download"}} {
		wantCode(t, "a token's "+c.method+" "+c.path, call(h, c.method, c.path, nil, bearer(tok.Secret)),
			http.StatusForbidden, api.CodeSessionRequired)
	}
	if res := call(h, http.MethodGet, path+"/download", nil, user.header(false)); res.status != http.StatusOK {
		t.Errorf("the user's download: %d %s", res.status, res.body)
	}
	// Another user reads none of it.
	for _, c := range []string{path, path + "/download"} {
		wantCode(t, "another user's read of "+c, call(h, http.MethodGet, c, nil, other.header(false)),
			http.StatusNotFound, api.CodeNotFound)
	}
	if got := itemIDs(t, h, other.header(false), personalExportsPath); len(got) != 0 {
		t.Errorf("another user lists the exports %q", got)
	}
	if got := itemIDs(t, h, user.header(false), personalExportsPath); !slices.Equal(got, []string{queued.ID}) {
		t.Errorf("the user lists the exports %q, want %s", got, queued.ID)
	}

	var key string
	if err := f.admin.QueryRow(t.Context(), `SELECT storage_key FROM personal_data_export WHERE id = $1`,
		id).Scan(&key); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, "personal-data/") {
		t.Errorf("the archive's key %s is not of the personal-data exports' part of the store", key)
	}
	// The expiry job has at least a year of hourly attempts.
	if n := countRows(t, f.d, `SELECT count(*) FROM river_job WHERE kind = 'personal_export_expire'
		AND args->>'export_id' = $1 AND max_attempts >= 8760`, id.String()); n != 1 {
		t.Errorf("%d expiry jobs of the export have a year of attempts, want 1", n)
	}
	// Past its time an export is gone to its user, though its job has not run.
	if _, err := f.admin.Exec(t.Context(), `UPDATE personal_data_export SET expires_at = now() - interval '1 second'
		WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{path, path + "/download"} {
		wantCode(t, "a read past its time of "+c, call(h, http.MethodGet, c, nil, user.header(false)),
			http.StatusNotFound, api.CodeNotFound)
	}
	if got := itemIDs(t, h, user.header(false), personalExportsPath); len(got) != 0 {
		t.Errorf("the list holds %q past their time", got)
	}
	if err := f.s.authoring.ExpirePersonalExport(t.Context(), u, id); err != nil {
		t.Fatal(err)
	}
	store, err := f.s.assets.Storage().Store(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Stat(t.Context(), key); !errors.Is(err, blob.ErrNotFound) {
		t.Errorf("the archive of the expired export is still stored: %v", err)
	}
	if n := countRows(t, f.d, `SELECT count(*) FROM personal_data_export WHERE id = $1::uuid`, id.String()); n != 0 {
		t.Errorf("%d rows of the expired export are left", n)
	}
	if err := f.s.authoring.ExpirePersonalExport(t.Context(), u, id); err != nil {
		t.Errorf("expiring an export that is gone: %v", err)
	}
	// An export on its way past its time holds the way no longer.
	stale := askPersonalExport(t, h, user)
	if _, err := f.admin.Exec(t.Context(), `UPDATE personal_data_export SET expires_at = now() - interval '1 second'
		WHERE id = $1`, decode(t, ids.Job, stale.ID)); err != nil {
		t.Fatal(err)
	}
	if fresh := askPersonalExport(t, h, user); fresh.ID == stale.ID {
		t.Errorf("the new export is the one past its time, %s", fresh.ID)
	}
}

// TestPersonalDataExportOfInactiveAccount checks that an export of an account
// that is no longer active by the time that its job reads it, as one
// suspended, whose user can no longer sign in to read it, fails at once, and
// stores nothing.
func TestPersonalDataExportOfInactiveAccount(t *testing.T) {
	t.Parallel()
	f := newRolesFixture(t)
	openMaintenance(t, f.s, f.d)
	user := f.as["editor"]
	e := askPersonalExport(t, f.s.main, user)
	id := decode(t, ids.Job, e.ID)
	var key string
	if err := f.admin.QueryRow(t.Context(), `SELECT storage_key FROM personal_data_export WHERE id = $1`, id).
		Scan(&key); err != nil {
		t.Fatal(err)
	}
	if _, err := f.admin.Exec(t.Context(), `UPDATE "user" SET status = 'suspended' WHERE id = $1`,
		decode(t, ids.User, user.body.UserID)); err != nil {
		t.Fatal(err)
	}
	startWorkers(t, f.s, f.d)
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		n := countRows(t, f.d, `SELECT count(*) FROM river_job WHERE kind = 'personal_export_build'
			AND args->>'export_id' = $1 AND attempt = 1 AND state = 'cancelled'`, id.String())
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the build's job did not end after its one attempt")
		}
	}
	var status string
	if err := f.admin.QueryRow(t.Context(), `SELECT status FROM personal_data_export WHERE id = $1`, id).
		Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != authoring.StatusFailed {
		t.Errorf("the export of the suspended account is %s, want failed", status)
	}
	store, err := f.s.assets.Storage().Store(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Stat(t.Context(), key); !errors.Is(err, blob.ErrNotFound) {
		t.Errorf("the archive of the suspended account is stored: %v", err)
	}
}

// TestPersonalDataExportBeforeSetup checks that a personal-data export is
// refused while first-run setup may yet choose the store, as a workspace
// export is.
func TestPersonalDataExportBeforeSetup(t *testing.T) {
	t.Parallel()
	s, d := pendingServer(t, inviteEnv("open", map[string]string{"STORYCREEP_ASSET_STORAGE": ""}))
	if _, err := account.Create(t.Context(), d.Open(t), account.NewHasher(), account.Registration{
		Email: "jo@example.com", DisplayName: "Jo", Password: secret.New(testPassword)}, false); err != nil {
		t.Fatal(err)
	}
	jo := signIn(t, s.main, "jo@example.com")
	wantCode(t, "a personal-data export before setup", call(s.main, http.MethodPost, personalExportsPath, nil,
		jo.header(true)), http.StatusForbidden, api.CodeSetupRequired)
}
