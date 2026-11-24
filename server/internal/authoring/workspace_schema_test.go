package authoring

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/CyberCute-Studio/storycreep/server/internal/serializer"
)

var workspaceSchemaFile = filepath.Join("..", "..", "..", filepath.FromSlash(WorkspaceSchemaPath))

// TestWorkspaceSchemaFile checks that the committed schema of the workspace
// export is the one that the types generate, and that its $id names its
// version.
//
// Verifies: FR-DATA-02/AC3
func TestWorkspaceSchemaFile(t *testing.T) {
	want := WorkspaceSchema()
	if *update {
		if err := os.MkdirAll(filepath.Dir(workspaceSchemaFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(workspaceSchemaFile, want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(workspaceSchemaFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is not what WorkspaceSchema generates; run go test ./internal/authoring "+
			"-run TestWorkspaceSchemaFile -update", WorkspaceSchemaPath)
	}
	if !strings.Contains(WorkspaceSchemaID, "/v1/") || !strings.HasSuffix(WorkspaceSchemaID, WorkspaceSchemaPath) ||
		WorkspaceVersion != 1 {
		t.Errorf("the schema's $id %s does not name the version %d at %s", WorkspaceSchemaID, WorkspaceVersion,
			WorkspaceSchemaPath)
	}
}

// TestWorkspaceReadme checks that the README of a workspace's archive says
// that it holds personal data, what it holds, and what it leaves out.
//
// Verifies: FR-DATA-02/AC3
func TestWorkspaceReadme(t *testing.T) {
	t.Parallel()
	m := WorkspaceManifest{Format: WorkspaceFormat, FormatVersion: WorkspaceVersion,
		ExportedAt: time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC), Workspace: ManifestWorkspace{Name: "Harbour"},
		Projects:        []WorkspaceProject{{ID: "prj_1"}, {ID: "prj_2"}},
		TrashedProjects: []TrashedProject{{ID: "prj_3"}},
		Sections:        []ManifestSection{{Name: "members", Path: WorkspaceMembersPath, Items: 3}}}
	got := workspaceReadme(m)
	for _, want := range []string{"**This archive contains personal data.**", `"Harbour"`, "2026-11-24T09:00:00Z",
		"each of the workspace's 2 project(s)", "`members.json`: members, 3 item(s)",
		"1 project(s) in the workspace's trash", "schema/workspace/v1/archive.schema.json"} {
		if !strings.Contains(got, want) {
			t.Errorf("the README lacks %q:\n%s", want, got)
		}
	}
	if m.TrashedProjects = nil; !strings.Contains(workspaceReadme(m), "leaves none out") {
		t.Error("the README of an archive that leaves nothing out does not say so")
	}
}

// TestWorkspaceSchemaValidates checks that the schema, compiled by a validator
// that is not the generator's, takes a small archive's files, and refuses a
// manifest of another format, a member without an email address, and a file
// that the format does not have.
//
// Verifies: FR-DATA-02/AC3
func TestWorkspaceSchemaValidates(t *testing.T) {
	t.Parallel()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(WorkspaceSchema()))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource(WorkspaceSchemaID, doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile(WorkspaceSchemaID)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	files := func() map[string]any {
		return map[string]any{
			WorkspaceManifestPath: WorkspaceManifest{Format: WorkspaceFormat, FormatVersion: WorkspaceVersion,
				AuthoringFormatVersion: Version, Generator: serializer.Generator(), ExportedAt: at,
				Workspace:    ManifestWorkspace{ID: "ws_1", Name: "Harbour", Slug: "harbour", CreatedAt: at},
				PersonalData: true, Projects: []WorkspaceProject{{ID: "prj_1", Name: "Lighthouse", Slug: "lighthouse",
					Path: "projects/prj_1.zip", SizeBytes: 10, SHA256: strings.Repeat("a", 64)}},
				TrashedProjects: []TrashedProject{{ID: "prj_2", Name: "Old", Slug: "old", DeletedAt: &at}},
				Sections:        []ManifestSection{}},
			WorkspaceMembersPath: Items[WorkspaceMember]{Items: []WorkspaceMember{{MembershipID: "mem_1",
				UserID: "usr_1", DisplayName: "Ada", Email: "ada@example.com", Role: "owner", JoinedAt: at,
				Grants: []MemberGrant{{ProjectID: "prj_1", Role: "editor"}}}}},
			WorkspaceAuditPath: Items[WorkspaceAuditEntry]{Items: []WorkspaceAuditEntry{{ID: "0192", Action: "project.create",
				Result: "success", Metadata: Value(`{}`), CreatedAt: at}}},
			WorkspaceAssetsPath: Items[Asset]{Items: []Asset{}},
		}
	}
	if err := s.Validate(instance(t, files())); err != nil {
		t.Errorf("a small archive does not validate: %v", err)
	}
	other := files()
	m := other[WorkspaceManifestPath].(WorkspaceManifest)
	m.Format = Format
	other[WorkspaceManifestPath] = m
	noEmail := files()
	noEmail[WorkspaceMembersPath] = map[string]any{"items": []any{map[string]any{"membershipId": "mem_1",
		"userId": "usr_1", "displayName": "Ada", "role": "owner", "joinedAt": at, "grants": []any{}}}}
	extra := files()
	extra["people.json"] = Items[Person]{Items: []Person{}}
	for name, f := range map[string]map[string]any{"another format": other, "a member without an email": noEmail,
		"a file of another format": extra} {
		if err := s.Validate(instance(t, f)); err == nil {
			t.Errorf("%s validates", name)
		}
	}
}
