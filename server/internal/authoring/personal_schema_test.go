package authoring

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/CyberCute-Studio/storycreep/server/internal/personaldata"
	"github.com/CyberCute-Studio/storycreep/server/internal/serializer"
)

var personalSchemaFile = filepath.Join("..", "..", "..", filepath.FromSlash(PersonalSchemaPath))

// TestPersonalSchemaFile checks that the committed schema of the personal-data
// export is the one that the types generate, that its $id names its version,
// and that it has a property for each file of the inventory (package
// personaldata).
//
// Verifies: FR-GDPR-01/AC2
func TestPersonalSchemaFile(t *testing.T) {
	want := PersonalSchema()
	if *update {
		if err := os.MkdirAll(filepath.Dir(personalSchemaFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(personalSchemaFile, want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(personalSchemaFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is not what PersonalSchema generates; run go test ./internal/authoring "+
			"-run TestPersonalSchemaFile -update", PersonalSchemaPath)
	}
	if !strings.Contains(PersonalSchemaID, "/v1/") || !strings.HasSuffix(PersonalSchemaID, PersonalSchemaPath) ||
		PersonalVersion != 1 {
		t.Errorf("the schema's $id %s does not name the version %d at %s", PersonalSchemaID, PersonalVersion,
			PersonalSchemaPath)
	}
	files := map[string]bool{}
	for _, f := range personalFiles {
		files[f.path] = true
	}
	for _, r := range personaldata.Inventory {
		for _, p := range r.Paths {
			if !files[p] && p != personaldata.ReadmePath {
				t.Errorf("the inventory's row %q is held in %s, which the format has no file of", r.Data, p)
			}
		}
	}
}

// TestPersonalReadme checks that the README of a personal-data archive says
// that it holds personal data and no credential, what each file holds, how it
// names someone else who acted on the user, and what it leaves out.
//
// Verifies: FR-GDPR-01/AC2
func TestPersonalReadme(t *testing.T) {
	t.Parallel()
	m := PersonalManifest{Format: PersonalFormat, FormatVersion: PersonalVersion,
		ExportedAt: time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC), UserID: "usr_1",
		Sections: []ManifestSection{{Name: "sessions", Path: personaldata.SessionsPath, Items: 2}}}
	got := personalReadme(m)
	for _, want := range []string{"**This archive contains personal data**", "usr_1", "2026-11-25T09:00:00Z",
		"`sessions.json`: sessions, 2 item(s)", "schema/personal-data/v1/archive.schema.json",
		"no hash of one", `"a workspace admin"`, `"an instance administrator"`, "operation log",
		"payment provider", "whether or not you are still a member"} {
		if !strings.Contains(got, want) {
			t.Errorf("the README lacks %q:\n%s", want, got)
		}
	}
}

// TestPersonalSchemaValidates checks that the schema, compiled by a validator
// that is not the generator's, takes a small archive's files, and refuses a
// manifest of another format, an entry about the user that names its actor
// otherwise than by a role, and a file that the format does not have.
//
// Verifies: FR-GDPR-01/AC2
func TestPersonalSchemaValidates(t *testing.T) {
	t.Parallel()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(PersonalSchema()))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource(PersonalSchemaID, doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile(PersonalSchemaID)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	files := func() map[string]any {
		return map[string]any{
			personaldata.ManifestPath: PersonalManifest{Format: PersonalFormat, FormatVersion: PersonalVersion,
				Generator: serializer.Generator(), ExportedAt: at, UserID: "usr_1", PersonalData: true,
				Sections: []ManifestSection{}},
			personaldata.AccountPath: PersonalAccount{ID: "usr_1", Email: "ada@example.com", DisplayName: "Ada",
				Status: "active", CreatedAt: at, Credentials: PersonalCredentials{PasswordSet: true, PasswordChangedAt: &at}},
			personaldata.EmailLinksPath: Items[PersonalEmailLink]{Items: []PersonalEmailLink{{Kind: "password_reset",
				CreatedAt: at, ExpiresAt: at}}},
			personaldata.SessionsPath: Items[PersonalSession]{Items: []PersonalSession{{ID: "ses_1", IP: "192.0.2.4",
				UserAgent: "Browser/1.0", CreatedAt: at, LastSeenAt: at, ExpiresAt: at}}},
			personaldata.TokensPath: Items[PersonalToken]{Items: []PersonalToken{{ID: "tok_1", Name: "CI",
				WorkspaceID: "ws_1", Scopes: []string{"snapshot:read"}, CreatedAt: at}}},
			personaldata.MembershipsPath: Items[PersonalMembership]{Items: []PersonalMembership{{MembershipID: "mem_1",
				WorkspaceID: "ws_1", WorkspaceName: "Harbour", Role: "editor", JoinedAt: at, Grants: []MemberGrant{}}}},
			personaldata.InvitesPath: Items[PersonalInvite]{Items: []PersonalInvite{{ID: "inv_1", WorkspaceID: "ws_2",
				WorkspaceName: "Quay", Kind: "email", Role: "viewer", Status: "open", CreatedAt: at, ExpiresAt: at}}},
			personaldata.ActedPath: Items[PersonalActedEntry]{Items: []PersonalActedEntry{{ID: "0192",
				Action: "auth.login", ActorUserID: "usr_1", Result: "success", Metadata: Value(`{}`), CreatedAt: at}}},
			personaldata.ActedOnPath: Items[PersonalActedOnEntry]{Items: []PersonalActedOnEntry{{
				Action: "membership.remove", Result: "success", Actor: personaldata.LabelWorkspaceAdmin, CreatedAt: at}}},
			personaldata.CommentsPath: Items[PersonalComment]{Items: []PersonalComment{{ID: "cmt_1", WorkspaceID: "ws_1",
				ProjectID: "prj_1", ThreadID: "cmt_1", Target: PersonalTarget{Kind: "node", ID: "nd_1"},
				Body:      Text(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Hi"}]}]}`),
				CreatedAt: at}}},
			personaldata.NotificationsPath: Items[PersonalNotification]{Items: []PersonalNotification{{ID: "ntf_1",
				WorkspaceID: "ws_1", ProjectID: "prj_1", CommentID: "cmt_2", Kind: "mention", CreatedAt: at}}},
			personaldata.AttributionPath: Items[PersonalReference]{Items: []PersonalReference{{Entity: "node", ID: "nd_1",
				WorkspaceID: "ws_1", ProjectID: "prj_1", Relation: "created", At: at}}},
			personaldata.IdentitiesPath: Items[PersonalIdentity]{Items: []PersonalIdentity{{WorkspaceID: "ws_3",
				DisplayName: "Ada", CreatedAt: at}}},
		}
	}
	if err := s.Validate(instance(t, files())); err != nil {
		t.Errorf("a small archive does not validate: %v", err)
	}
	other := files()
	m := other[personaldata.ManifestPath].(PersonalManifest)
	m.Format = WorkspaceFormat
	other[personaldata.ManifestPath] = m
	named := files()
	named[personaldata.ActedOnPath] = map[string]any{"items": []any{map[string]any{"action": "membership.remove",
		"result": "success", "actor": "usr_2", "createdAt": at}}}
	extra := files()
	extra[WorkspaceMembersPath] = Items[WorkspaceMember]{Items: []WorkspaceMember{}}
	for name, f := range map[string]map[string]any{"another format": other, "an actor named by their ID": named,
		"a file of another format": extra} {
		if err := s.Validate(instance(t, f)); err == nil {
			t.Errorf("%s validates", name)
		}
	}
}

// TestPersonalReferenceEnums checks that the format's enums of a reference's
// entity and relation are those of the inventory's attributions, so that the
// schema takes every reference that the archive holds.
func TestPersonalReferenceEnums(t *testing.T) {
	t.Parallel()
	field := func(name string) []string {
		f, ok := reflect.TypeFor[PersonalReference]().FieldByName(name)
		if !ok {
			t.Fatalf("PersonalReference has no %s", name)
		}
		return strings.Split(f.Tag.Get("enum"), ",")
	}
	var tables []string
	for _, a := range personaldata.Attributions() {
		if !slices.Contains(tables, a.Table) {
			tables = append(tables, a.Table)
		}
	}
	if got := field("Entity"); !slices.Equal(got, tables) {
		t.Errorf("the enum of a reference's entity is %q, want the attributions' tables %q", got, tables)
	}
	if got := field("Relation"); !slices.Equal(got, personaldata.Relations) {
		t.Errorf("the enum of a reference's relation is %q, want %q", got, personaldata.Relations)
	}
}
