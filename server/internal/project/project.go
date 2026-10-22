// Package project keeps a workspace's projects through their lifecycle (PRD
// FR-PROJ-01, §5.3): it creates them, lists them, reads them, and changes
// them, each change a command of the pipeline (package commands), whose
// project's revision a client expects as it does any entity's (§5.7,
// ADR-006): renaming a project, describing it, changing its settings,
// archiving and unarchiving it, deleting it to the workspace's trash, and
// restoring it from there. Register registers the project as an entity of the
// pipeline's registry, with those commands.
//
// Creating a project is not a command, because a batch of commands applies to
// a project that exists. Create is the one site that creates a project, and
// checks the workspace's projects limit for it (FR-ENT-01), which every path
// that creates a project goes through, as importing one will (plan item 84).
//
// An archived project is read-only: the pipeline refuses every command of it
// but unarchiving, deleting, and restoring it with
// commands.ErrProjectArchived (FR-PROJ-01 AC2). A deleted project stays in the
// trash for TrashDays days, and is answered with ErrNotFound, as one that does
// not exist, except to restore it and to list the trash; then the purge job
// deletes it, with every row of its content (AC3, Purge). Neither an archived
// project nor a deleted one counts towards the workspace's projects, so
// unarchiving and restoring one, which make it count again, check the limit
// too, as the pipeline checks the limits that their commands declare
// (commands.Limit).
//
// Every operation resolves the user's role as package role does (§5.5), and
// package authz decides what it allows (§5.6): reading a project needs an
// effective role on it other than none, changing its settings, which renaming,
// describing, archiving, and unarchiving it are, needs owner or admin there
// (P), and creating, deleting, and restoring projects, and listing the trash,
// need owner or admin in the workspace (W). A user whose role is none is told
// that the project, or the workspace, does not exist (ErrNotFound).
package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/text/language"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/role"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
	"github.com/CyberCute-Studio/storycreep/server/internal/slug"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
)

// Limits of a project's fields.
const (
	// MaxNameLength is the most characters that a project's name may have.
	MaxNameLength = 100
	// MaxDescriptionLength is the most characters that a project's
	// description may have.
	MaxDescriptionLength = 2000
	// MaxLocaleLength is the most characters that a project's default locale
	// may have.
	MaxLocaleLength = 64
	// TrashDays is how many days a deleted project stays in the trash, from
	// which it can be restored, before the purge job deletes it (FR-PROJ-01
	// AC3).
	TrashDays = 30
)

var (
	// ErrNotFound is returned for a project that does not exist, is in the
	// trash, or on which the user's effective role is none, and for a
	// workspace that does not exist, or in which the user's role is none, as
	// it is for a user who is not a member (§5.5).
	ErrNotFound = errors.New("project: no such project or workspace, or the user has no role there")
	// ErrInsufficientRole is returned when the user's role does not allow the
	// operation (§5.6), as package authz decides.
	ErrInsufficientRole = authz.ErrInsufficientRole
)

// Publishing roles: the least role that may publish a project's snapshots
// (publish_min_role, FR-SNAP-01 AC3), editor unless raised to admin.
const (
	PublishEditor = "editor"
	PublishAdmin  = "admin"
)

// fallbackLocale is the default locale of a project whose settings name none,
// as a row added without them would have.
const fallbackLocale = "en"

// ProjectEntry is a project's entry: the board, and the node on it, where the
// game starts (§5.3, the published settings' entry).
type ProjectEntry struct {
	BoardID string `json:"board_id,omitempty" doc:"The ID of the project's entry board, if it has one"`
	NodeID  string `json:"node_id,omitempty" doc:"The ID of the entry node on the entry board, if it has one"`
}

// Project is a project as the API shows it, and as an answer of
// revision_conflict shows it (commands.Entity.States).
type Project struct {
	ID             string       `json:"id" doc:"The project's ID"`
	WorkspaceID    string       `json:"workspace_id" doc:"The ID of the project's workspace"`
	Name           string       `json:"name" doc:"The project's name"`
	Slug           string       `json:"slug" doc:"The project's slug, which the published format's manifest shows (PRD §6.5): derived from the project's name when it was created, and changed only when it is changed itself, never when the project is renamed. Projects may share a slug."`
	Description    string       `json:"description" doc:"The project's description, which may be empty"`
	DefaultLocale  string       `json:"default_locale" doc:"The locale of the project's text, as a BCP 47 tag"`
	PublishMinRole string       `json:"publish_min_role" enum:"editor,admin" doc:"The least role that may publish the project's snapshots: editor, or admin once raised (PRD FR-SNAP-01 AC3)"`
	Entry          ProjectEntry `json:"entry" doc:"Where the game starts: the entry board, and the node on it"`
	Rev            int64        `json:"rev" doc:"The project's revision, which every change of its name, description, settings, archiving, or deletion increments, and which a change expects with expected_rev"`
	Seq            int64        `json:"seq" doc:"The sequence number of the project's latest operation, 0 before its first"`
	// ArchivedAt, DeletedAt, and HardDeleteAfter are set while the project is
	// archived, and in the trash.
	ArchivedAt      *time.Time `json:"archived_at,omitempty" doc:"When the project was archived, if it is: an archived project is read-only"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty" doc:"When the project was deleted to the trash, if it is there"`
	HardDeleteAfter *time.Time `json:"hard_delete_after,omitempty" doc:"When a project in the trash is deleted for good, after which it cannot be restored"`
	CreatedAt       time.Time  `json:"created_at" doc:"When the project was created"`
	UpdatedAt       time.Time  `json:"updated_at" doc:"When the project last changed"`
}

// Visible is a project that a user may see, with the role that they act with
// on it.
type Visible struct {
	Project
	// Role is the user's effective role on the project (§5.5).
	Role role.Role
}

// settings are a project's settings, as the database holds them.
type settings struct {
	DefaultLocale  string       `json:"default_locale"`
	PublishMinRole string       `json:"publish_min_role"`
	Entry          ProjectEntry `json:"entry"`
}

// decodeSettings returns the settings that doc holds, with the defaults of
// the settings that it lacks.
func decodeSettings(doc []byte) settings {
	var s settings
	// A document that does not decode, which the database's check that it is
	// an object rules out, has the defaults.
	_ = json.Unmarshal(doc, &s)
	if s.DefaultLocale == "" {
		s.DefaultLocale = fallbackLocale
	}
	if s.PublishMinRole != PublishAdmin {
		s.PublishMinRole = PublishEditor
	}
	return s
}

// stateOf returns the state of the project p.
func stateOf(p queries.Project) Project {
	set := decodeSettings(p.Settings)
	return Project{ID: ids.Encode(ids.Project, p.ID.Bytes), WorkspaceID: ids.Encode(ids.Workspace, p.WorkspaceID.Bytes),
		Name: p.Name, Slug: p.Slug, Description: p.Description, DefaultLocale: set.DefaultLocale,
		PublishMinRole: set.PublishMinRole, Entry: set.Entry, Rev: p.Rev, Seq: p.Seq, ArchivedAt: timeOf(p.ArchivedAt),
		DeletedAt: timeOf(p.DeletedAt), HardDeleteAfter: timeOf(p.HardDeleteAfter), CreatedAt: p.CreatedAt.Time,
		UpdatedAt: p.UpdatedAt.Time}
}

// timeOf returns the time of the column t, or nil if it is null.
func timeOf(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}

// DB runs a transaction of the application login, as *database.DB does.
type DB interface {
	InTx(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error
}

// Options configure a Service.
type Options struct {
	// Policy resolves the roles.
	Policy role.Policy
	// Entitlements check the projects limit of the workspaces' plans
	// (FR-ENT-01). It is required, because a Service without it would let
	// every workspace past its plan.
	Entitlements *entitlements.Service
	// Pipeline applies the commands that change the projects. Its registry
	// holds them (Register). It is required.
	Pipeline *commands.Pipeline
}

// Service keeps the projects.
type Service struct {
	db       DB
	policy   role.Policy
	ent      *entitlements.Service
	pipeline *commands.Pipeline
}

// New returns a Service that keeps the projects in db, with opts. It panics
// if opts.Entitlements or opts.Pipeline is nil.
func New(db DB, opts Options) *Service {
	switch {
	case opts.Entitlements == nil:
		panic("project: a Service needs the entitlements that check its workspaces' projects limit")
	case opts.Pipeline == nil:
		panic("project: a Service needs the pipeline that applies its commands")
	}
	return &Service{db: db, policy: opts.Policy, ent: opts.Entitlements, pipeline: opts.Pipeline}
}

// Settings are what a project is created with.
type Settings struct {
	Name        string
	Description string
	// DefaultLocale is the locale of the project's text, as a BCP 47 tag, or
	// "" for the workspace's default locale.
	DefaultLocale string
}

// Create creates a project with in in the workspace workspaceID, which the
// user userID asks for, as client, and records that in the audit log, in one
// transaction. It returns ErrNotFound if the workspace does not exist or the
// user's role in it is none, ErrInsufficientRole unless the role is owner or
// admin (§5.6), then input.FieldErrors if a field of in is not valid, and an
// *entitlements.LimitError if the workspace has as many projects as its plan
// allows (FR-ENT-01), and creates nothing then. The name and the description
// lose the white space around them, and the description's line breaks are
// written as line feeds (input.LineFeeds). The project's slug is derived
// from its name, as a board's is, or is project for a name without one
// (slug.Derive).
func (s *Service) Create(ctx context.Context, userID, workspaceID ids.UUID, in Settings, client audit.Client) (
	Visible, error) {
	in.Description = input.LineFeeds(in.Description)
	var out Visible
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID, UserID: userID}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		ws, err := activeWorkspace(ctx, q, workspaceID)
		if err != nil {
			return err
		}
		a, err := s.access(ctx, q, userID, workspaceID, false)
		if err != nil {
			return err
		}
		r := s.policy.Workspace(a.admin, a.member)
		if r == role.None {
			return ErrNotFound
		}
		if err := authz.Check(r, authz.ManageProjects); err != nil {
			return err
		}
		set, err := check(in, workspaceLocale(ws.Settings))
		if err != nil {
			return err
		}
		if err := s.ent.Check(ctx, tx, workspaceID, entitlements.Projects, 1); err != nil {
			return err
		}
		doc, err := json.Marshal(set)
		if err != nil {
			return fmt.Errorf("project: encode the settings: %w", err)
		}
		id := ids.New()
		name, description := strings.TrimSpace(in.Name), strings.TrimSpace(in.Description)
		projectSlug := slug.Derive(name, "project")
		row, err := q.InsertProject(ctx, queries.InsertProjectParams{ID: nullUUID(id),
			WorkspaceID: nullUUID(workspaceID), Name: name, Slug: projectSlug, Description: description, Settings: doc,
			CreatedBy: nullUUID(userID)})
		if err != nil {
			return fmt.Errorf("project: add the project: %w", err)
		}
		if err := usage.Record(ctx, tx, workspaceID, usage.Projects, 1); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Entry{Action: audit.ActionProjectCreate, WorkspaceID: workspaceID,
			ActorUserID: userID, SubjectID: id, Result: audit.ResultSuccess, IP: client.IP,
			UserAgent: client.UserAgent}); err != nil {
			return err
		}
		out = Visible{Role: s.policy.Effective(a.admin, a.member, role.None), Project: stateOf(queries.Project{
			ID: nullUUID(id), WorkspaceID: nullUUID(workspaceID), Name: name, Slug: projectSlug, Description: description,
			Settings: doc, Rev: 1, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})}
		return nil
	})
	if err != nil {
		return Visible{}, err
	}
	return out, nil
}

// check returns the settings of a project created with in, whose default
// locale is locale unless in names one, or input.FieldErrors if a field of
// in is not valid.
func check(in Settings, locale string) (settings, error) {
	var errs input.FieldErrors
	if msg := checkName(strings.TrimSpace(in.Name)); msg != "" {
		errs = append(errs, input.FieldError{Field: "name", Message: msg})
	}
	if msg := checkDescription(strings.TrimSpace(in.Description)); msg != "" {
		errs = append(errs, input.FieldError{Field: "description", Message: msg})
	}
	if in.DefaultLocale != "" {
		var msg string
		if locale, msg = canonicalLocale(in.DefaultLocale); msg != "" {
			errs = append(errs, input.FieldError{Field: "default_locale", Message: msg})
		}
	}
	if len(errs) > 0 {
		return settings{}, errs
	}
	return settings{DefaultLocale: locale, PublishMinRole: PublishEditor}, nil
}

// activeWorkspace returns the workspace workspaceID, read in q's transaction,
// or ErrNotFound if it does not exist or its deletion is pending (FR-TEN-05
// AC1).
func activeWorkspace(ctx context.Context, q *queries.Queries, workspaceID ids.UUID) (queries.WorkspaceRow, error) {
	ws, err := q.Workspace(ctx, nullUUID(workspaceID))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return queries.WorkspaceRow{}, ErrNotFound
	case err != nil:
		return queries.WorkspaceRow{}, fmt.Errorf("project: read the workspace: %w", err)
	}
	return ws, nil
}

// workspaceLocale returns the default locale of a workspace whose settings
// are doc (package workspace), or fallbackLocale if they name none.
func workspaceLocale(doc []byte) string {
	var set struct {
		DefaultLocale string `json:"default_locale"`
	}
	if json.Unmarshal(doc, &set) != nil {
		return fallbackLocale
	}
	if locale, msg := canonicalLocale(set.DefaultLocale); msg == "" {
		return locale
	}
	return fallbackLocale
}

// checkName says what is wrong with a project's name, or returns "".
func checkName(name string) string {
	return input.CheckTrimmedName(name, MaxNameLength)
}

// checkDescription says what is wrong with a project's description, or
// returns "".
func checkDescription(description string) string {
	return input.CheckTrimmedText(description, MaxDescriptionLength)
}

// canonicalLocale returns the BCP 47 tag v in its canonical form, as en-US
// for en-us, or says what is wrong with it: a tag that names no language, as
// und, or und-CZ, whose language is only guessed from its region, is not a
// locale of text, and neither v nor its canonical form, which may be the
// longer, as sr-Latn is for sh, may have more than MaxLocaleLength
// characters, which UpdateSettings's payload allows, so that the inverse of a
// change of the locale, which sets the one that the project has back, is
// valid too.
func canonicalLocale(v string) (string, string) {
	if len(v) > MaxLocaleLength {
		return "", fmt.Sprintf("is longer than %d characters", MaxLocaleLength)
	}
	if tag, err := language.Parse(v); err == nil {
		if _, named := tag.Base(); named == language.Exact {
			if s := tag.String(); len(s) > MaxLocaleLength {
				return "", fmt.Sprintf("is longer than %d characters in its canonical form, %s", MaxLocaleLength, s)
			}
			return tag.String(), ""
		}
	}
	return "", "is not a BCP 47 tag of a language, such as en or cs-CZ"
}

// checkEntry says what is wrong with an entry, by the field that is wrong,
// or returns "", "": each ID is of its kind, and a node needs its board.
func checkEntry(e ProjectEntry) (field, message string) {
	if e.BoardID != "" {
		if _, err := ids.Decode(ids.Board, e.BoardID); err != nil {
			return "board_id", "is not a board's ID: " + err.Error()
		}
	}
	if e.NodeID != "" {
		if _, err := ids.Decode(ids.Node, e.NodeID); err != nil {
			return "node_id", "is not a node's ID: " + err.Error()
		}
		if e.BoardID == "" {
			return "node_id", "is set without board_id, the board that the node is on"
		}
	}
	return "", ""
}

// access is what the database holds about a user that their roles in a
// workspace are resolved from (§5.5).
type access struct {
	// admin is whether the user is an instance admin who acts as owner, as
	// they do only where the policy lets them.
	admin bool
	// membership is the user's membership of the workspace, zero if there is
	// none, and member its role, role.None then.
	membership ids.UUID
	member     role.Role
}

// access reads, in q's transaction, a transaction of the workspace
// workspaceID, what the user userID's roles are resolved from. It reads
// whether the user is an instance admin only if the policy lets an instance
// admin act as owner, because otherwise the answer changes nothing, and
// returns ErrNotFound if it does and the account is no longer active. If
// lock is set, it locks the user's membership FOR SHARE until the
// transaction ends, so that a change of its role, or its removal, waits for
// the operation that the role authorises.
func (s *Service) access(ctx context.Context, q *queries.Queries, userID, workspaceID ids.UUID, lock bool) (access,
	error) {
	a := access{member: role.None}
	var m queries.MembershipOfRow
	var err error
	if lock {
		var locked queries.LockMembershipOfRow
		locked, err = q.LockMembershipOf(ctx, queries.LockMembershipOfParams{WorkspaceID: nullUUID(workspaceID),
			UserID: nullUUID(userID)})
		m = queries.MembershipOfRow(locked)
	} else {
		m, err = q.MembershipOf(ctx, queries.MembershipOfParams{WorkspaceID: nullUUID(workspaceID),
			UserID: nullUUID(userID)})
	}
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return access{}, fmt.Errorf("project: read the membership: %w", err)
	default:
		a.membership, a.member = m.ID.Bytes, role.Role(m.Role)
	}
	if s.policy.InstanceAdminAccess {
		a.admin, err = q.InstanceAdmin(ctx, nullUUID(userID))
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return access{}, ErrNotFound
		case err != nil:
			return access{}, fmt.Errorf("project: read the account: %w", err)
		}
	}
	return a, nil
}

// effective returns the user's effective role on the project projectID, of
// whom a is what the database holds, read in q's transaction.
func (s *Service) effective(ctx context.Context, q *queries.Queries, a access, workspaceID, projectID ids.UUID) (
	role.Role, error) {
	grant := role.None
	if a.membership != (ids.UUID{}) {
		g, err := q.ProjectGrantRole(ctx, queries.ProjectGrantRoleParams{WorkspaceID: nullUUID(workspaceID),
			MembershipID: nullUUID(a.membership), ProjectID: nullUUID(projectID)})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return "", fmt.Errorf("project: read the project grant: %w", err)
		default:
			grant = role.Role(g)
		}
	}
	return s.policy.Effective(a.admin, a.member, grant), nil
}

// workspaceOf returns the workspace of the project projectID, through the
// context bootstrap (§5.4), or ErrNotFound if there is no such project, or
// its workspace's deletion is pending.
func (s *Service) workspaceOf(ctx context.Context, userID, projectID ids.UUID) (ids.UUID, error) {
	var ws pgtype.UUID
	err := s.db.InTx(ctx, database.Scope{UserID: userID}, func(tx pgx.Tx) error {
		var err error
		ws, err = queries.New(tx).ProjectWorkspace(ctx, nullUUID(projectID))
		return err
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ids.UUID{}, ErrNotFound
	case err != nil:
		return ids.UUID{}, fmt.Errorf("project: find the project's workspace: %w", err)
	}
	return ws.Bytes, nil
}

// actorWorkspace returns the workspace that the actor acts in on the project
// projectID: that of their API token (scope.Token.Workspace), which acts in
// no other, so that a transaction of it does not find a project of another
// workspace, or else the project's, through the context bootstrap
// (workspaceOf). It returns ErrNotFound as workspaceOf does, and for a
// project other than the one that the token is narrowed to.
func (s *Service) actorWorkspace(ctx context.Context, actor commands.Actor, projectID ids.UUID) (ids.UUID, error) {
	if actor.Token == nil {
		return s.workspaceOf(ctx, actor.UserID, projectID)
	}
	if ws, ok := actor.Token.Workspace(projectID); ok {
		return ws, nil
	}
	return ids.UUID{}, ErrNotFound
}

// Get returns the project projectID, with the role that the actor acts with
// on it, or ErrNotFound if it does not exist, is in the trash, the actor's
// effective role on it is none, or the actor's API token does not cover it,
// as one of another workspace, or narrowed to another project, does not
// (FR-AUTH-04 AC3). The token's scopes are the route's to check.
func (s *Service) Get(ctx context.Context, actor commands.Actor, projectID ids.UUID) (Visible, error) {
	workspaceID, err := s.actorWorkspace(ctx, actor, projectID)
	if err != nil {
		return Visible{}, err
	}
	var out Visible
	err = s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID, UserID: actor.UserID}, func(tx pgx.Tx) error {
		out, err = s.GetIn(ctx, tx, actor, workspaceID, projectID)
		return err
	})
	if err != nil {
		return Visible{}, err
	}
	return out, nil
}

// ActorWorkspace returns the workspace that the actor acts in on the project
// projectID, in whose transactions a service reads the project
// (GetIn): that of their API token, or else the project's, through the
// context bootstrap (§5.4). It returns ErrNotFound if there is no such
// project, its workspace's deletion is pending, or the actor's token is
// narrowed to another project.
func (s *Service) ActorWorkspace(ctx context.Context, actor commands.Actor, projectID ids.UUID) (ids.UUID, error) {
	return s.actorWorkspace(ctx, actor, projectID)
}

// ReadContent runs fn in a transaction of db, of the workspace that the
// actor acts in on the project projectID (ActorWorkspace), with the actor's
// scope, once it has checked there that the actor may read the project's
// content (GetIn), for a service that reads what belongs to the project, as
// package board reads its boards and package trash its trash. It returns the
// errors of Get, and fn's. The token's scopes are the route's to check.
func (s *Service) ReadContent(ctx context.Context, db DB, actor commands.Actor, projectID ids.UUID,
	fn func(tx pgx.Tx, workspaceID ids.UUID) error) error {
	workspaceID, err := s.actorWorkspace(ctx, actor, projectID)
	if err != nil {
		return err
	}
	return db.InTx(ctx, database.Scope{WorkspaceID: workspaceID, UserID: actor.UserID}, func(tx pgx.Tx) error {
		if _, err := s.GetIn(ctx, tx, actor, workspaceID, projectID); err != nil {
			return err
		}
		return fn(tx, workspaceID)
	})
}

// GetIn returns the project projectID as Get does, read in tx, a transaction
// of its workspace workspaceID, which ActorWorkspace returns, for a service
// that reads or changes what belongs to the project in the same
// transaction, as package asset does its assets. It returns the errors of
// Get. It locks nothing.
func (s *Service) GetIn(ctx context.Context, tx pgx.Tx, actor commands.Actor, workspaceID, projectID ids.UUID) (
	Visible, error) {
	q := queries.New(tx)
	if _, err := activeWorkspace(ctx, q, workspaceID); err != nil {
		return Visible{}, err
	}
	row, err := q.Project(ctx, queries.ProjectParams{WorkspaceID: nullUUID(workspaceID), ID: nullUUID(projectID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return Visible{}, ErrNotFound
	}
	if err != nil {
		return Visible{}, fmt.Errorf("project: read the project: %w", err)
	}
	a, err := s.access(ctx, q, actor.UserID, workspaceID, false)
	if err != nil {
		return Visible{}, err
	}
	r, err := s.effective(ctx, q, a, workspaceID, projectID)
	switch {
	case err != nil:
		return Visible{}, err
	case r == role.None:
		return Visible{}, ErrNotFound
	}
	if err := authz.Check(r, authz.ReadProject); err != nil {
		return Visible{}, err
	}
	return Visible{Project: stateOf(row.Project), Role: r}, nil
}

// List returns at most limit of the projects of the workspace workspaceID
// that are not in the trash and on which the actor's effective role is not
// none, each with that role, in the order of their IDs: the first, if after
// is zero, or else those after the project after. A guest is shown only the
// projects granted to them (FR-TEN-04 AC2), and a request made with an API
// token narrowed to a project only that project, on which alone the token
// acts. It returns ErrNotFound if the workspace does not exist, the actor is
// neither a member of it nor an instance admin who acts as owner, or the
// actor's token is another workspace's (FR-AUTH-04 AC3). The token's scopes
// are the route's to check.
func (s *Service) List(ctx context.Context, actor commands.Actor, workspaceID, after ids.UUID, limit int) ([]Visible,
	error) {
	userID := actor.UserID
	var only ids.UUID
	if actor.Token != nil {
		if !actor.Token.InWorkspace(workspaceID) {
			return nil, ErrNotFound
		}
		only = actor.Token.ProjectID
	}
	var out []Visible
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID, UserID: userID}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		if _, err := activeWorkspace(ctx, q, workspaceID); err != nil {
			return err
		}
		a, err := s.access(ctx, q, userID, workspaceID, false)
		if err != nil {
			return err
		}
		if !a.admin && a.member == role.None {
			return ErrNotFound
		}
		rows, err := q.Projects(ctx, queries.ProjectsParams{MembershipID: nullUUID(a.membership),
			WorkspaceID: nullUUID(workspaceID), After: nullUUID(after),
			GrantedOnly: s.policy.Workspace(a.admin, a.member) == role.None, OnlyProject: nullUUID(only),
			MaxRows: int32(min(limit, 1<<20))})
		if err != nil {
			return fmt.Errorf("project: list the projects: %w", err)
		}
		for _, row := range rows {
			grant := role.None
			if row.GrantRole.Valid {
				grant = role.Role(row.GrantRole.String)
			}
			if r := s.policy.Effective(a.admin, a.member, grant); r != role.None {
				out = append(out, Visible{Project: stateOf(row.Project), Role: r})
			}
		}
		return nil
	})
	return out, err
}

// Trash returns at most limit of the projects of the workspace workspaceID
// that are in its trash and can still be restored, in the order of their
// IDs: the first, if after is zero, or else those after the project after.
// It returns ErrNotFound if the workspace does not exist or the user
// userID's role in it is none, and ErrInsufficientRole unless the role is
// owner or admin, who delete and restore projects (§5.6).
func (s *Service) Trash(ctx context.Context, userID, workspaceID, after ids.UUID, limit int) ([]Project, error) {
	var out []Project
	err := s.db.InTx(ctx, database.Scope{WorkspaceID: workspaceID, UserID: userID}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		if _, err := activeWorkspace(ctx, q, workspaceID); err != nil {
			return err
		}
		a, err := s.access(ctx, q, userID, workspaceID, false)
		if err != nil {
			return err
		}
		r := s.policy.Workspace(a.admin, a.member)
		if r == role.None {
			return ErrNotFound
		}
		if err := authz.Check(r, authz.ManageProjects); err != nil {
			return err
		}
		rows, err := q.TrashedProjects(ctx, queries.TrashedProjectsParams{WorkspaceID: nullUUID(workspaceID),
			After: nullUUID(after), MaxRows: int32(min(limit, 1<<20))})
		if err != nil {
			return fmt.Errorf("project: list the trash: %w", err)
		}
		for _, row := range rows {
			out = append(out, stateOf(row.Project))
		}
		return nil
	})
	return out, err
}

// Patch is a change of a project's name, slug, description, and settings. A
// nil field stays as it is.
type Patch struct {
	Name, Slug, Description, DefaultLocale *string
	// PublishMinRole is PublishEditor or PublishAdmin.
	PublishMinRole *string
	// Entry replaces the project's entry: an entry without a board removes
	// it.
	Entry *ProjectEntry
	// ExpectedRev is the project's revision that the change was made against,
	// or 0 to change it whatever its revision.
	ExpectedRev int64
}

// Update changes the project projectID as p says, which the actor asks for,
// in one batch of commands (Register), and returns the project. The name and
// the description lose the white space around them, the description's line
// breaks are written as line feeds (input.LineFeeds), and the default locale
// takes its canonical form. It returns ErrNotFound if the project does not
// exist, is in the trash, the actor's effective role on it is none, or the
// actor's API token does not cover it, commands.ErrInsufficientScope if the
// token does not hold project:write, ErrInsufficientRole unless the role is
// owner or admin (§5.6), then input.FieldErrors if a field of p is not
// valid, commands.ErrProjectArchived if the project is archived, a
// *commands.RevisionConflictError if p expects a revision that the project
// does not have, and a *commands.RefusedError if the slug is not one, or the
// entry names a board that the project does not have, in its trash or not,
// and changes nothing then. A patch that names no change is
// not applied, and returns the project, whatever revision it expects.
func (s *Service) Update(ctx context.Context, actor commands.Actor, projectID ids.UUID, p Patch) (Visible, error) {
	var steps []commands.Step
	var errs input.FieldErrors
	if p.Name != nil {
		name := strings.TrimSpace(*p.Name)
		if msg := checkName(name); msg != "" {
			errs = append(errs, input.FieldError{Field: "name", Message: msg})
		}
		steps = append(steps, commands.Step{Type: Rename, Payload: renamePayload{Name: name}})
	}
	if p.Slug != nil {
		// The command refuses a slug that is not one, as slug_invalid.
		steps = append(steps, commands.Step{Type: SetSlug, Payload: slugPayload{Slug: *p.Slug}})
	}
	if p.Description != nil {
		description := strings.TrimSpace(input.LineFeeds(*p.Description))
		if msg := checkDescription(description); msg != "" {
			errs = append(errs, input.FieldError{Field: "description", Message: msg})
		}
		steps = append(steps, commands.Step{Type: Describe, Payload: describePayload{Description: description}})
	}
	set := settingsPayload{PublishMinRole: p.PublishMinRole, Entry: p.Entry}
	if p.DefaultLocale != nil {
		locale, msg := canonicalLocale(*p.DefaultLocale)
		if msg != "" {
			errs = append(errs, input.FieldError{Field: "default_locale", Message: msg})
		}
		set.DefaultLocale = &locale
	}
	if p.PublishMinRole != nil && *p.PublishMinRole != PublishEditor && *p.PublishMinRole != PublishAdmin {
		errs = append(errs, input.FieldError{Field: "publish_min_role", Message: "is neither editor nor admin"})
	}
	if p.Entry != nil {
		if field, msg := checkEntry(*p.Entry); msg != "" {
			errs = append(errs, input.FieldError{Field: "entry." + field, Message: msg})
		}
	}
	if set != (settingsPayload{}) {
		steps = append(steps, commands.Step{Type: UpdateSettings, Payload: set})
	}
	if len(errs) > 0 || len(steps) == 0 {
		// The actor's role is checked before the fields, as the pipeline
		// checks it before a command's payload would reach the project, and a
		// patch that names no change is answered with the project that the
		// check reads.
		v, err := s.authorise(ctx, actor, projectID, patchOperation, patchScope)
		switch {
		case err != nil:
			return Visible{}, err
		case len(errs) > 0:
			return Visible{}, errs
		}
		return v, nil
	}
	return s.apply(ctx, actor, projectID, p.ExpectedRev, steps...)
}

// authorise returns the project projectID, with the actor's effective role on
// it, for a change of it by commands that need the operation op, one of the
// project-scoped operations (P), and the scope needed of an API token, as
// they are registered (Register), as the pipeline would apply them: it
// returns the errors of Get, then commands.ErrInsufficientScope, as a
// *scope.InsufficientError, if the actor's API token does not hold needed,
// and the error of authz.Check if the role does not allow op.
func (s *Service) authorise(ctx context.Context, actor commands.Actor, projectID ids.UUID, op authz.Operation,
	needed scope.Scope) (Visible, error) {
	p, err := s.Get(ctx, actor, projectID)
	if err != nil {
		return Visible{}, err
	}
	if !actor.Token.Allows(needed) {
		return Visible{}, &scope.InsufficientError{Scope: needed}
	}
	if err := authz.Check(p.Role, op); err != nil {
		return Visible{}, err
	}
	return p, nil
}

// Archive archives the project projectID, which the actor asks for, and
// returns it (FR-PROJ-01). It returns the errors of Update, and
// commands.ErrProjectArchived if the project is archived already.
func (s *Service) Archive(ctx context.Context, actor commands.Actor, projectID ids.UUID) (Visible, error) {
	return s.apply(ctx, actor, projectID, 0, commands.Step{Type: Archive, Payload: emptyPayload{}})
}

// Unarchive unarchives the project projectID, which the actor asks for, and
// returns it. It returns the errors of Update, but for
// commands.ErrProjectArchived, and an *entitlements.LimitError if the
// project is archived and its workspace has as many projects as its plan
// allows. A project that is not archived stays as it is.
func (s *Service) Unarchive(ctx context.Context, actor commands.Actor, projectID ids.UUID) (Visible, error) {
	return s.apply(ctx, actor, projectID, 0, commands.Step{Type: Unarchive, Payload: emptyPayload{}})
}

// Delete deletes the project projectID to its workspace's trash, which the
// actor asks for, from which it can be restored for TrashDays days
// (FR-PROJ-01 AC3). It returns ErrNotFound if the project does not exist, is
// in the trash already, or the actor's effective role on it is none, and
// ErrInsufficientRole unless the actor's role in the workspace is owner or
// admin (§5.6).
func (s *Service) Delete(ctx context.Context, actor commands.Actor, projectID ids.UUID) error {
	_, err := s.pipeline.Apply(ctx, actor, projectID, batch(projectID, 0,
		commands.Step{Type: Delete, Payload: emptyPayload{}}))
	return mapErr(err)
}

// Restore restores the project projectID from its workspace's trash, which
// the actor asks for, and returns it. It returns ErrNotFound if the project
// does not exist, its time in the trash has passed, or the actor's effective
// role on it is none, ErrInsufficientRole unless the actor's role in the
// workspace is owner or admin (§5.6), and an *entitlements.LimitError if the
// project is not archived and its workspace has as many projects as its plan
// allows. A project that is not in the trash stays as it is.
func (s *Service) Restore(ctx context.Context, actor commands.Actor, projectID ids.UUID) (Visible, error) {
	return s.apply(ctx, actor, projectID, 0, commands.Step{Type: Restore, Payload: emptyPayload{}})
}

// apply applies the steps to the project projectID as one batch, each
// command expecting the project's revision rev, unless it is 0, and returns
// the project.
func (s *Service) apply(ctx context.Context, actor commands.Actor, projectID ids.UUID, rev int64,
	steps ...commands.Step) (Visible, error) {
	if _, err := s.pipeline.Apply(ctx, actor, projectID, batch(projectID, rev, steps...)); err != nil {
		return Visible{}, mapErr(err)
	}
	return s.Get(ctx, actor, projectID)
}

// batch returns the steps as a batch of commands of the project projectID,
// each with an ID of its own, and expecting the project's revision rev,
// unless it is 0.
func batch(projectID ids.UUID, rev int64, steps ...commands.Step) []commands.Command {
	out := make([]commands.Command, len(steps))
	for i, st := range steps {
		// The payloads are this package's, which always encode.
		raw, _ := json.Marshal(st.Payload)
		out[i] = commands.Command{ID: ids.New(), Type: st.Type, Payload: raw}
		if rev != 0 {
			out[i].ExpectedRev = map[commands.Ref]int64{{Kind: ids.Project, ID: projectID}: rev}
		}
	}
	return out
}

// mapErr returns err, with the pipeline's ErrNotFound as this package's.
func mapErr(err error) error {
	if errors.Is(err, commands.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// nullUUID returns id as a uuid column, null if id is zero.
func nullUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != (ids.UUID{})}
}
