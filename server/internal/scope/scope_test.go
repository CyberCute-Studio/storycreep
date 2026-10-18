package scope

import (
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// TestGrants checks every pair of a held scope and a needed one against the
// PRD's table of scopes (FR-AUTH-04): a scope grants itself and what it
// implies, transitively, and nothing else.
//
// Verifies: NFR-SEC-05
func TestGrants(t *testing.T) {
	// want holds, for each held scope, the scopes that it grants.
	want := map[Scope][]Scope{
		SnapshotRead:  {SnapshotRead},
		ProjectRead:   {ProjectRead, SnapshotRead},
		ProjectWrite:  {ProjectWrite, ProjectRead, SnapshotRead},
		SnapshotWrite: {SnapshotWrite, SnapshotRead},
		ExportRead:    {ExportRead},
	}
	for _, held := range All() {
		for _, needed := range All() {
			granted := false
			for _, s := range want[held] {
				granted = granted || s == needed
			}
			if got := Grants([]Scope{held}, needed); got != granted {
				t.Errorf("Grants(%s, %s) = %v, want %v", held, needed, got, granted)
			}
		}
	}
	if Grants(All(), "") || Grants(All(), "project:admin") {
		t.Error("every scope together grants a scope that is not one")
	}
	if Grants(nil, SnapshotRead) {
		t.Error("no scope grants snapshot:read")
	}
	if !Grants([]Scope{SnapshotRead, ExportRead}, ExportRead) {
		t.Error("snapshot:read and export:read do not grant export:read")
	}
}

// TestToken checks what a token covers and allows, and that a nil Token, a
// request's session, is restricted by neither.
func TestToken(t *testing.T) {
	ws, other, p, q := ids.New(), ids.New(), ids.New(), ids.New()
	wide := &Token{ID: ids.New(), WorkspaceID: ws, Scopes: []Scope{ProjectRead}}
	narrowed := &Token{ID: ids.New(), WorkspaceID: ws, ProjectID: p, Scopes: []Scope{ProjectWrite}}
	var session *Token
	for _, tt := range []struct {
		name      string
		token     *Token
		workspace ids.UUID
		project   ids.UUID
		want      bool
	}{
		{"a workspace's token on its project", wide, ws, p, true},
		{"a workspace's token on another of its projects", wide, ws, q, true},
		{"a workspace's token in another workspace", wide, other, p, false},
		{"a narrowed token on its project", narrowed, ws, p, true},
		{"a narrowed token on another project", narrowed, ws, q, false},
		{"a narrowed token in another workspace", narrowed, other, p, false},
		{"a session", session, other, q, true},
	} {
		if got := tt.token.Covers(tt.workspace, tt.project); got != tt.want {
			t.Errorf("%s: Covers = %v, want %v", tt.name, got, tt.want)
		}
	}
	switch {
	case !wide.Allows(SnapshotRead) || wide.Allows(ProjectWrite):
		t.Error("a project:read token allows the wrong scopes")
	case !narrowed.Allows(ProjectRead):
		t.Error("a project:write token does not allow project:read")
	case !session.Allows(ExportRead):
		t.Error("a session does not allow export:read")
	case session.TokenID() != ids.UUID{} || wide.TokenID() != wide.ID:
		t.Error("TokenID is wrong")
	}
}
