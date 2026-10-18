// Package scope is the scopes of the API tokens (PRD FR-AUTH-04, NFR-SEC-05):
// what each lets a token do, which imply which, and what a token restricts
// its owner to on each request that it makes.
//
// A route that takes an API token declares the scope that it needs (package
// api), and a token that does not hold it, or a scope that implies it, is
// refused with 403 insufficient_scope; a route that declares none takes no
// token, which it refuses with 403 session_required. A token also acts only
// in its workspace, and only on its project if it is narrowed to one, and
// never with more than its owner's role allows at the time of use: the
// permission check is allowed(role) AND token_has_scope AND
// token_covers_project (§5.6). The services check the last two with Token,
// once they know the project that a request acts on: they act in the
// token's workspace (Token.Workspace), and answer a project that the token
// does not cover, of another workspace or other than the one that it is
// narrowed to, as one that does not exist, with 404, so that the answer does
// not tell that it exists (AC3).
package scope

import (
	"errors"
	"slices"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// Scope is a scope of an API token, as the API and the database name it.
type Scope string

// The scopes (FR-AUTH-04). A scope grants exactly what its row of the PRD's
// table says, and the only implications are those of implies.
const (
	// SnapshotRead is listing snapshots and their metadata, exporting ready
	// snapshots, and diffing two snapshots, without any read of the draft.
	SnapshotRead Scope = "snapshot:read"
	// ProjectRead is everything of SnapshotRead, and reading the draft's
	// content, its export, its diffs, and its text export.
	ProjectRead Scope = "project:read"
	// ProjectWrite is applying commands to the draft.
	ProjectWrite Scope = "project:write"
	// SnapshotWrite is publishing snapshots.
	SnapshotWrite Scope = "snapshot:write"
	// ExportRead is the authoring-data export, which can hold comments and
	// personal data, and which no other scope implies.
	ExportRead Scope = "export:read"
)

// implies holds, for each scope, the scopes that it implies directly.
var implies = map[Scope][]Scope{
	SnapshotRead:  nil,
	ProjectRead:   {SnapshotRead},
	ProjectWrite:  {ProjectRead},
	SnapshotWrite: {SnapshotRead},
	ExportRead:    nil,
}

// All returns the scopes, in the order of the PRD's table.
func All() []Scope {
	return []Scope{SnapshotRead, ProjectRead, ProjectWrite, SnapshotWrite, ExportRead}
}

// Known reports whether s is a scope.
func (s Scope) Known() bool {
	_, ok := implies[s]
	return ok
}

// Names returns the names of the scopes, in their order, as the API and the
// database write them.
func Names(scopes []Scope) []string {
	out := make([]string, len(scopes))
	for i, s := range scopes {
		out[i] = string(s)
	}
	return out
}

// Grants reports whether held, the scopes that a token holds, grant needed:
// whether needed is one of them, or one of them implies it, directly or
// through another. Nothing grants a scope that is not one, as "".
func Grants(held []Scope, needed Scope) bool {
	if !needed.Known() {
		return false
	}
	seen := map[Scope]bool{}
	todo := slices.Clone(held)
	for len(todo) > 0 {
		s := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		if s == needed {
			return true
		}
		if !seen[s] {
			seen[s] = true
			todo = append(todo, implies[s]...)
		}
	}
	return false
}

// ErrInsufficientScope is returned for an operation that an API token does
// not hold the scope of, which the API answers with 403 insufficient_scope
// (FR-AUTH-04 AC2), as an *InsufficientError that names the scope.
var ErrInsufficientScope = errors.New("scope: the API token does not hold the scope that the operation needs")

// InsufficientError is ErrInsufficientScope for an operation that needs the
// scope Scope, which the API's answer names (details.scope).
type InsufficientError struct {
	Scope Scope
}

func (e *InsufficientError) Error() string {
	return "scope: the API token does not hold " + string(e.Scope) + ", which the operation needs"
}

// Unwrap returns ErrInsufficientScope, so that errors.Is finds it.
func (e *InsufficientError) Unwrap() error {
	return ErrInsufficientScope
}

// Token is what an API token restricts its owner to, on a request that it
// makes. A nil *Token is a request made with a session, which nothing
// restricts but the user's role.
type Token struct {
	// ID is the token's.
	ID ids.UUID
	// WorkspaceID is the workspace that the token belongs to, and acts in
	// alone.
	WorkspaceID ids.UUID
	// ProjectID is the one project that the token is narrowed to, or zero
	// for a token of the whole workspace.
	ProjectID ids.UUID
	// Scopes are the scopes that it holds.
	Scopes []Scope
}

// Workspace returns the workspace that t's owner acts in on the project
// projectID, and true, or false if t is narrowed to another project. It is
// t's own workspace, in which alone t acts: a service reads the project in
// a transaction of it, which finds no project of another workspace, so that
// the service answers one as a project that does not exist. t is not nil: a
// request made with a session acts in the project's workspace, which the
// service finds through the context bootstrap (PRD §5.4).
func (t *Token) Workspace(projectID ids.UUID) (ids.UUID, bool) {
	if t.ProjectID != (ids.UUID{}) && t.ProjectID != projectID {
		return ids.UUID{}, false
	}
	return t.WorkspaceID, true
}

// Allows reports whether t holds needed, or a scope that implies it
// (Grants). A nil Token allows every scope.
func (t *Token) Allows(needed Scope) bool {
	return t == nil || Grants(t.Scopes, needed)
}

// TokenID returns t's ID, or zero for a nil Token.
func (t *Token) TokenID() ids.UUID {
	if t == nil {
		return ids.UUID{}
	}
	return t.ID
}
