package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
	"github.com/CyberCute-Studio/storycreep/server/internal/slug"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
)

// The types of command of a project (PRD §5.7). Each applies to the project
// of its batch, which it names by its batch alone, so that its payload names
// no project.
const (
	// Rename renames the project, and keeps its slug; SetSlug changes its
	// slug, and Describe its description. Changing each back undoes it.
	Rename   commands.Type = "project.rename"
	SetSlug  commands.Type = "project.set_slug"
	Describe commands.Type = "project.describe"
	// UpdateSettings changes the settings that its payload names: the
	// project's default locale, its publish_min_role (FR-SNAP-01 AC3), and its
	// entry. Changing them back undoes it.
	UpdateSettings commands.Type = "project.update_settings"
	// Archive archives the project, which makes it read-only (FR-PROJ-01
	// AC2), and Unarchive unarchives it. Each undoes the other.
	Archive   commands.Type = "project.archive"
	Unarchive commands.Type = "project.unarchive"
	// Delete deletes the project to its workspace's trash (FR-PROJ-01 AC3),
	// and Restore restores it. Each undoes the other.
	Delete  commands.Type = "project.delete"
	Restore commands.Type = "project.restore"
)

// renamePayload is Rename's payload.
type renamePayload struct {
	Name string `json:"name" maxLength:"100" doc:"The project's new name, of 1 to 100 characters, not all of them blank, without white space around it, and without control characters, bidirectional controls, and line separators among them"`
}

// slugPayload is SetSlug's payload.
type slugPayload struct {
	Slug string `json:"slug" doc:"The project's new slug: groups of lowercase ASCII letters and digits joined by single hyphens, as harbour, of at most 64 characters, that is not a name that Windows reserves for a device, such as con or com1. A slug that is not one is answered with 422 slug_invalid."`
}

// describePayload is Describe's payload.
type describePayload struct {
	Description string `json:"description" maxLength:"2000" doc:"The project's new description, of at most 2000 characters, which may be empty, without white space around it, and without control characters but line feeds and tabs"`
}

// settingsPayload is UpdateSettings's payload. A setting that it leaves out
// stays as it is.
type settingsPayload struct {
	DefaultLocale  *string       `json:"default_locale,omitempty" maxLength:"64" doc:"The locale of the project's text, as a BCP 47 tag of a language in its canonical form, such as en or cs-CZ"`
	PublishMinRole *string       `json:"publish_min_role,omitempty" enum:"editor,admin" doc:"The least role that may publish the project's snapshots"`
	Entry          *ProjectEntry `json:"entry,omitempty" doc:"The project's entry, which replaces its entry: without a board, the project has none. A board other than the entry's is one of the project's boards, not in its trash, or the command is answered with 422 validation_failed; the node needs not exist yet."`
}

// emptyPayload is the payload of a command that needs nothing but its
// project.
type emptyPayload struct{}

// touches are the kinds of entity that every command of a project touches.
var touches = []ids.Kind{ids.Project}

// The commands that a patch of the project applies (Update), Rename,
// Describe, and UpdateSettings, need the operation patchOperation of the
// actor's role, and the scope patchScope of their API token, which Update
// checks too for a patch that it does not apply, as the pipeline would
// check them.
const (
	patchOperation = authz.ChangeProjectSettings
	patchScope     = scope.ProjectWrite
)

// Register registers in r the project as an entity of the pipeline, whose
// revision a command expects as it does any entity's, and the project's
// commands, each of which changes its draft, so that an API token needs
// project:write for it (FR-AUTH-04). A change of the entry's board checks
// that the board is one of the project's, not in its trash, and the node is
// not checked to exist, which the nodes do once they are added (plan item
// 35); the validation reports an entry that no longer exists (FR-DEP-04).
func Register(r *commands.Registry) {
	commands.RegisterEntity(r, commands.Entity{
		Kind: ids.Project,
		Revs: func(ctx context.Context, tx *commands.Tx, entities []ids.UUID) (map[ids.UUID]int64, error) {
			rows, err := queries.New(tx).ProjectRevs(ctx, queries.ProjectRevsParams{
				WorkspaceID: nullUUID(tx.WorkspaceID()), ID: nullUUID(tx.ProjectID()), Ids: uuids(entities)})
			if err != nil {
				return nil, err
			}
			revs := make(map[ids.UUID]int64, len(rows))
			for _, row := range rows {
				revs[row.ID.Bytes] = row.Rev
			}
			return revs, nil
		},
		States: func(ctx context.Context, tx *commands.Tx, entities []ids.UUID) (map[ids.UUID]any, error) {
			states := map[ids.UUID]any{}
			for _, id := range entities {
				if id != tx.ProjectID() {
					continue
				}
				p, err := read(ctx, tx)
				if err != nil {
					return nil, err
				}
				states[id] = stateOf(p)
			}
			return states, nil
		},
	})

	commands.Register(r, commands.Spec[renamePayload]{
		Type: Rename, Capability: patchOperation, Scope: patchScope, Touches: touches,
		Check: func(p renamePayload) input.FieldErrors { return field("name", checkName(p.Name)) },
		Inverse: func(ctx context.Context, tx *commands.Tx, _ renamePayload) ([]commands.Step, error) {
			p, err := read(ctx, tx)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: Rename, Payload: renamePayload{Name: p.Name}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p renamePayload) error {
			return changed(tx)(queries.New(tx).UpdateProjectName(ctx, queries.UpdateProjectNameParams{Name: p.Name,
				UpdatedBy: nullUUID(tx.Actor().UserID), WorkspaceID: nullUUID(tx.WorkspaceID()),
				ID: nullUUID(tx.ProjectID())}))
		},
	})

	commands.Register(r, commands.Spec[slugPayload]{
		Type: SetSlug, Capability: patchOperation, Scope: patchScope, Touches: touches,
		Inverse: func(ctx context.Context, tx *commands.Tx, _ slugPayload) ([]commands.Step, error) {
			p, err := read(ctx, tx)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: SetSlug, Payload: slugPayload{Slug: p.Slug}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p slugPayload) error {
			var e *slug.Error
			if errors.As(slug.Check(p.Slug), &e) {
				return commands.Refuse("slug", e.Problem(), e)
			}
			return changed(tx)(queries.New(tx).UpdateProjectSlug(ctx, queries.UpdateProjectSlugParams{Slug: p.Slug,
				UpdatedBy: nullUUID(tx.Actor().UserID), WorkspaceID: nullUUID(tx.WorkspaceID()),
				ID: nullUUID(tx.ProjectID())}))
		},
	})

	commands.Register(r, commands.Spec[describePayload]{
		Type: Describe, Capability: patchOperation, Scope: patchScope, Touches: touches,
		Check: func(p describePayload) input.FieldErrors {
			return field("description", checkDescription(p.Description))
		},
		Inverse: func(ctx context.Context, tx *commands.Tx, _ describePayload) ([]commands.Step, error) {
			p, err := read(ctx, tx)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: Describe, Payload: describePayload{Description: p.Description}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p describePayload) error {
			return changed(tx)(queries.New(tx).UpdateProjectDescription(ctx, queries.UpdateProjectDescriptionParams{
				Description: p.Description, UpdatedBy: nullUUID(tx.Actor().UserID),
				WorkspaceID: nullUUID(tx.WorkspaceID()), ID: nullUUID(tx.ProjectID())}))
		},
	})

	commands.Register(r, commands.Spec[settingsPayload]{
		Type: UpdateSettings, Capability: patchOperation, Scope: patchScope, Touches: touches,
		Check: checkSettings,
		Inverse: func(ctx context.Context, tx *commands.Tx, p settingsPayload) ([]commands.Step, error) {
			cur, err := read(ctx, tx)
			if err != nil {
				return nil, err
			}
			// The inverse sets back exactly the settings that p sets.
			set := decodeSettings(cur.Settings)
			var back settingsPayload
			if p.DefaultLocale != nil {
				back.DefaultLocale = &set.DefaultLocale
			}
			if p.PublishMinRole != nil {
				back.PublishMinRole = &set.PublishMinRole
			}
			if p.Entry != nil {
				back.Entry = &set.Entry
			}
			return []commands.Step{{Type: UpdateSettings, Payload: back}}, nil
		},
		Apply: applySettings,
	})

	commands.Register(r, commands.Spec[emptyPayload]{
		Type: Archive, Capability: authz.ChangeProjectSettings, Scope: scope.ProjectWrite, Touches: touches,
		Inverse: func(context.Context, *commands.Tx, emptyPayload) ([]commands.Step, error) {
			return []commands.Step{{Type: Unarchive, Payload: emptyPayload{}}}, nil
		},
		Apply: lifecycle(func(ctx context.Context, q *queries.Queries, by queries.ArchiveProjectParams) (int64, error) {
			return q.ArchiveProject(ctx, by)
		}, ""),
	})

	commands.Register(r, commands.Spec[emptyPayload]{
		Type: Unarchive, Capability: authz.ChangeProjectSettings, Scope: scope.ProjectWrite, Touches: touches,
		Archived: true,
		// Only an archived project, which counts towards none, adds to the
		// projects. Unarchiving one that is not undoes nothing.
		Limits: []commands.Limit[emptyPayload]{{Key: entitlements.Projects, Adds: func(ctx context.Context,
			tx *commands.Tx, _ emptyPayload) (int64, error) {
			p, err := read(ctx, tx)
			if err != nil || !p.ArchivedAt.Valid || p.DeletedAt.Valid {
				return 0, err
			}
			return 1, nil
		}}},
		Inverse: func(ctx context.Context, tx *commands.Tx, _ emptyPayload) ([]commands.Step, error) {
			p, err := read(ctx, tx)
			if err != nil || !p.ArchivedAt.Valid {
				return []commands.Step{}, err
			}
			return []commands.Step{{Type: Archive, Payload: emptyPayload{}}}, nil
		},
		Apply: lifecycle(func(ctx context.Context, q *queries.Queries, by queries.ArchiveProjectParams) (int64, error) {
			return q.UnarchiveProject(ctx, queries.UnarchiveProjectParams(by))
		}, ""),
	})

	commands.Register(r, commands.Spec[emptyPayload]{
		Type: Delete, Capability: authz.ManageProjects, Scope: scope.ProjectWrite, Touches: touches,
		// Deleting a project is session-only (FR-AUTH-04), through a batch
		// too.
		SessionOnly: true, Archived: true,
		Inverse: func(context.Context, *commands.Tx, emptyPayload) ([]commands.Step, error) {
			return []commands.Step{{Type: Restore, Payload: emptyPayload{}}}, nil
		},
		Apply: lifecycle(func(ctx context.Context, q *queries.Queries, by queries.ArchiveProjectParams) (int64, error) {
			return q.TrashProject(ctx, queries.TrashProjectParams{TrashDays: TrashDays, UpdatedBy: by.UpdatedBy,
				WorkspaceID: by.WorkspaceID, ID: by.ID})
		}, audit.ActionProjectDelete),
	})

	commands.Register(r, commands.Spec[emptyPayload]{
		Type: Restore, Capability: authz.ManageProjects, Scope: scope.ProjectWrite, Touches: touches,
		SessionOnly: true, Archived: true, Trashed: true,
		// Only a project in the trash that is not archived adds to the
		// projects. Restoring one that is not in the trash undoes nothing.
		Limits: []commands.Limit[emptyPayload]{{Key: entitlements.Projects, Adds: func(ctx context.Context,
			tx *commands.Tx, _ emptyPayload) (int64, error) {
			p, err := read(ctx, tx)
			if err != nil || !p.DeletedAt.Valid || p.ArchivedAt.Valid {
				return 0, err
			}
			return 1, nil
		}}},
		Inverse: func(ctx context.Context, tx *commands.Tx, _ emptyPayload) ([]commands.Step, error) {
			p, err := read(ctx, tx)
			if err != nil || !p.DeletedAt.Valid {
				return []commands.Step{}, err
			}
			return []commands.Step{{Type: Delete, Payload: emptyPayload{}}}, nil
		},
		Apply: lifecycle(func(ctx context.Context, q *queries.Queries, by queries.ArchiveProjectParams) (int64, error) {
			return q.RestoreProject(ctx, queries.RestoreProjectParams(by))
		}, audit.ActionProjectRestore),
	})
}

// read reads the batch's project in its transaction, whether or not it is in
// the trash.
func read(ctx context.Context, tx *commands.Tx) (queries.Project, error) {
	row, err := queries.New(tx).ProjectAny(ctx, queries.ProjectAnyParams{WorkspaceID: nullUUID(tx.WorkspaceID()),
		ID: nullUUID(tx.ProjectID())})
	if err != nil {
		return queries.Project{}, fmt.Errorf("project: read the project: %w", err)
	}
	return row.Project, nil
}

// changed returns a function that reports the change of the batch's project
// to the revision that an update returned, with err, or reports nothing if
// the update found nothing to change, which it returns as pgx.ErrNoRows.
func changed(tx *commands.Tx) func(rev int64, err error) error {
	return func(rev int64, err error) error {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		case err != nil:
			return fmt.Errorf("project: change the project: %w", err)
		}
		return tx.Changed(ids.Project, tx.ProjectID(), rev)
	}
}

// counted returns what the project p adds to its workspace's projects: 1 if
// it is neither archived nor in the trash, and 0 otherwise.
func counted(p queries.Project) int64 {
	if p.ArchivedAt.Valid || p.DeletedAt.Valid {
		return 0
	}
	return 1
}

// lifecycle returns the Apply of a command that archives, unarchives,
// deletes, or restores the batch's project with update, which returns the
// project's new revision, or pgx.ErrNoRows if the project is as the command
// would leave it already, and which records what the change adds to the
// workspace's projects, or takes from them, and, unless action is "", the
// event action in the audit log.
func lifecycle(update func(ctx context.Context, q *queries.Queries, by queries.ArchiveProjectParams) (int64, error),
	action audit.Action) func(ctx context.Context, tx *commands.Tx, _ emptyPayload) error {
	return func(ctx context.Context, tx *commands.Tx, _ emptyPayload) error {
		before, err := read(ctx, tx)
		if err != nil {
			return err
		}
		q := queries.New(tx)
		rev, err := update(ctx, q, queries.ArchiveProjectParams{UpdatedBy: nullUUID(tx.Actor().UserID),
			WorkspaceID: nullUUID(tx.WorkspaceID()), ID: nullUUID(tx.ProjectID())})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("project: change the project: %w", err)
		}
		if err := tx.Changed(ids.Project, tx.ProjectID(), rev); err != nil {
			return err
		}
		after, err := read(ctx, tx)
		if err != nil {
			return err
		}
		if err := tx.AddUsage(usage.Projects, counted(after)-counted(before)); err != nil {
			return err
		}
		if action == "" {
			return nil
		}
		return tx.Audit(ctx, audit.Entry{Action: action, SubjectID: tx.ProjectID(), Result: audit.ResultSuccess})
	}
}

// checkSettings returns what is wrong with UpdateSettings's payload p that its
// schema cannot say.
func checkSettings(p settingsPayload) input.FieldErrors {
	var errs input.FieldErrors
	if p.DefaultLocale != nil {
		if locale, msg := canonicalLocale(*p.DefaultLocale); msg != "" {
			errs = append(errs, field("default_locale", msg)...)
		} else if locale != *p.DefaultLocale {
			errs = append(errs, field("default_locale", "is not in its canonical form, "+locale)...)
		}
	}
	if p.Entry != nil {
		if f, msg := checkEntry(*p.Entry); msg != "" {
			errs = append(errs, field("entry."+f, msg)...)
		}
	}
	return errs
}

// applySettings is UpdateSettings's Apply: it changes the settings that p
// names, and records a change of publish_min_role in the audit log
// (FR-SNAP-01 AC6).
func applySettings(ctx context.Context, tx *commands.Tx, p settingsPayload) error {
	cur, err := read(ctx, tx)
	if err != nil {
		return err
	}
	previous := decodeSettings(cur.Settings)
	set := previous
	if p.DefaultLocale != nil {
		set.DefaultLocale = *p.DefaultLocale
	}
	if p.PublishMinRole != nil {
		set.PublishMinRole = *p.PublishMinRole
	}
	if p.Entry != nil {
		if err := checkEntryBoard(ctx, tx, previous.Entry, *p.Entry); err != nil {
			return err
		}
		set.Entry = *p.Entry
	}
	doc, err := json.Marshal(set)
	if err != nil {
		return fmt.Errorf("project: encode the settings: %w", err)
	}
	if err := changed(tx)(queries.New(tx).UpdateProjectSettings(ctx, queries.UpdateProjectSettingsParams{
		Settings: doc, UpdatedBy: nullUUID(tx.Actor().UserID), WorkspaceID: nullUUID(tx.WorkspaceID()),
		ID: nullUUID(tx.ProjectID())})); err != nil {
		return err
	}
	if set.PublishMinRole == previous.PublishMinRole {
		return nil
	}
	return tx.Audit(ctx, audit.Entry{Action: audit.ActionProjectPublishPolicyChange, SubjectID: tx.ProjectID(),
		Result: audit.ResultSuccess, Metadata: map[string]any{"publish_min_role": set.PublishMinRole,
			"previous_publish_min_role": previous.PublishMinRole}})
}

// checkEntryBoard refuses the batch unless the board of the project's new
// entry is none, the board of its entry before, which may no longer exist,
// so that a change that keeps it is not refused for it, or one of the
// project's boards that is not in its trash (N32).
func checkEntryBoard(ctx context.Context, tx *commands.Tx, before, entry ProjectEntry) error {
	if entry.BoardID == "" || entry.BoardID == before.BoardID {
		return nil
	}
	// Check refused an entry whose board's ID does not decode.
	board, _ := ids.Decode(ids.Board, entry.BoardID)
	live, err := queries.New(tx).LiveBoard(ctx, queries.LiveBoardParams{WorkspaceID: nullUUID(tx.WorkspaceID()),
		ProjectID: nullUUID(tx.ProjectID()), ID: nullUUID(board)})
	switch {
	case err != nil:
		return fmt.Errorf("project: read the entry's board: %w", err)
	case !live:
		return commands.Refuse("entry.board_id", "is not a board of the project, or is in its trash", nil)
	}
	return nil
}

// field returns the problem msg of the payload's field name, or none if msg
// is "".
func field(name, msg string) input.FieldErrors {
	if msg == "" {
		return nil
	}
	return input.FieldErrors{{Field: name, Message: msg}}
}

// uuids returns entities as uuid columns.
func uuids(entities []ids.UUID) []pgtype.UUID {
	out := make([]pgtype.UUID, len(entities))
	for i, id := range entities {
		out[i] = nullUUID(id)
	}
	return out
}
