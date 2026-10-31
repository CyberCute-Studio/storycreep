package component

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
)

// The types of command of a component type (PRD §5.7). Each names the type
// that it is about by its ID, id, and applies to the type of the batch's
// project.
const (
	// CreateType creates a component type, whose ID the client makes, without
	// attribute definitions. Deleting it to the trash undoes it.
	CreateType commands.Type = "component_type.create"
	// RenameType changes a type's name, SetTypeKey its key, and SetTypeIcon
	// its icon. Changing each back undoes it.
	RenameType  commands.Type = "component_type.rename"
	SetTypeKey  commands.Type = "component_type.set_key"
	SetTypeIcon commands.Type = "component_type.set_icon"
	// DeleteType deletes a type, each of whose components is in the trash, to
	// the trash, with its attribute definitions, and RestoreType restores it.
	// Each undoes the other.
	DeleteType  commands.Type = "component_type.delete"
	RestoreType commands.Type = "component_type.restore"
)

// createTypePayload is CreateType's payload.
type createTypePayload struct {
	ID   string `json:"id" doc:"The new type's ID, a ct_ ID of a UUIDv7 that the client makes, which no entity of a project's content has, of any kind, in any workspace (PRD §5.2); one that one has is answered with 422 validation_failed"`
	Name string `json:"name" maxLength:"100" doc:"The type's name, of 1 to 100 characters, not all of them blank, without white space around it, and without control characters, bidirectional controls, and line separators among them"`
	Key  string `json:"key,omitempty" doc:"The type's key, by which an export names it: an identifier of the expression language (PRD §5.8.1): an ASCII letter or an underscore, then ASCII letters, digits, and underscores, of at most 64 characters, as character, compared case by case, and not one of its reserved words, true, false, and, or, not, global, board, null, if, else, and elseif, which is answered with 422 key_invalid, and that no other type of the project has, of those that are not in its trash, which is answered with 409 key_taken. Without it, the key is derived from the name: its letters and digits in lowercase ASCII, without their accents, each run of other characters an underscore, with an underscore before it if it starts with a digit, or type for a name without them, and with _2, _3, and so on after it if another type of the project that is not in its trash has it, or it is a reserved word."`
	Icon string `json:"icon,omitempty" enum:"none,person,group,item,location,event,book,star,heart,key,gem,shield,flag,tag" doc:"The type's icon, which the web app shows a glyph for. Without it, none."`
}

// renameTypePayload is RenameType's payload.
type renameTypePayload struct {
	ID   string `json:"id" doc:"The type's ID"`
	Name string `json:"name" maxLength:"100" doc:"The type's new name, of 1 to 100 characters, not all of them blank, without white space around it, and without control characters, bidirectional controls, and line separators among them. Its key stays as it is."`
}

// typeKeyPayload is SetTypeKey's payload.
type typeKeyPayload struct {
	ID  string `json:"id" doc:"The type's ID"`
	Key string `json:"key" doc:"The type's new key: an identifier of the expression language (PRD §5.8.1): an ASCII letter or an underscore, then ASCII letters, digits, and underscores, of at most 64 characters, as character, compared case by case, and not one of its reserved words, true, false, and, or, not, global, board, null, if, else, and elseif, which is answered with 422 key_invalid, and that no other type of the project has, of those that are not in its trash, which is answered with 409 key_taken."`
}

// typeIconPayload is SetTypeIcon's payload.
type typeIconPayload struct {
	ID   string `json:"id" doc:"The type's ID"`
	Icon string `json:"icon" enum:"none,person,group,item,location,event,book,star,heart,key,gem,shield,flag,tag" doc:"The type's new icon, which the web app shows a glyph for, or none"`
}

// typeIDPayload is the payload of a command that needs nothing but its type's
// ID.
type typeIDPayload struct {
	ID string `json:"id" doc:"The type's ID"`
}

// deleteTypePayload is DeleteType's payload.
type deleteTypePayload struct {
	ID string `json:"id" doc:"The ID of the type to delete, each of whose components is in the project's trash, or the command is answered with 409 component_type_in_use. Its attribute definitions go to the trash with it, and restoring it, or one of its components, brings them back."`
}

// registerTypes registers in r the commands of a component type.
func registerTypes(r *commands.Registry) {
	ct := []ids.Kind{ids.ComponentType}
	op, sc := authz.ManageVariablesAndComponents, scope.ProjectWrite

	commands.Register(r, commands.Spec[createTypePayload]{
		Type: CreateType, Capability: op, Scope: sc, Touches: ct,
		Check: func(p createTypePayload) input.FieldErrors { return newID(ids.ComponentType, p.ID) },
		CheckNew: func(p createTypePayload) input.FieldErrors {
			return field("name", checkName(p.Name))
		},
		Inverse: func(_ context.Context, _ *commands.Tx, p createTypePayload) ([]commands.Step, error) {
			return []commands.Step{{Type: DeleteType, Payload: deleteTypePayload{ID: p.ID}}}, nil
		},
		Apply: applyCreateType,
	})

	commands.Register(r, commands.Spec[renameTypePayload]{
		Type: RenameType, Capability: op, Scope: sc, Touches: ct,
		Check:    func(p renameTypePayload) input.FieldErrors { return entityID(ids.ComponentType, "id", p.ID) },
		CheckNew: func(p renameTypePayload) input.FieldErrors { return field("name", checkName(p.Name)) },
		Inverse: func(ctx context.Context, tx *commands.Tx, p renameTypePayload) ([]commands.Step, error) {
			t, err := liveType(ctx, tx, "id", p.ID)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: RenameType, Payload: renameTypePayload{ID: p.ID, Name: t.Name}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p renameTypePayload) error {
			t, err := liveType(ctx, tx, "id", p.ID)
			if err != nil {
				return err
			}
			return changed(tx, ids.ComponentType, t.ID)(queries.New(tx).UpdateComponentTypeName(ctx,
				queries.UpdateComponentTypeNameParams{Name: p.Name, UpdatedBy: by(tx), WorkspaceID: t.WorkspaceID,
					ProjectID: t.ProjectID, ID: t.ID}))
		},
	})

	commands.Register(r, commands.Spec[typeKeyPayload]{
		Type: SetTypeKey, Capability: op, Scope: sc, Touches: ct,
		Check: func(p typeKeyPayload) input.FieldErrors { return entityID(ids.ComponentType, "id", p.ID) },
		Inverse: func(ctx context.Context, tx *commands.Tx, p typeKeyPayload) ([]commands.Step, error) {
			t, err := liveType(ctx, tx, "id", p.ID)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: SetTypeKey, Payload: typeKeyPayload{ID: p.ID, Key: t.Key}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p typeKeyPayload) error {
			t, err := liveType(ctx, tx, "id", p.ID)
			if err != nil {
				return err
			}
			q := queries.New(tx)
			if err := checkKey(ctx, typeKeyOwner(q, t.WorkspaceID, t.ProjectID), "key", p.Key, t.ID,
				"component type"); err != nil {
				return err
			}
			return changed(tx, ids.ComponentType, t.ID)(q.UpdateComponentTypeKey(ctx,
				queries.UpdateComponentTypeKeyParams{Key: p.Key, UpdatedBy: by(tx), WorkspaceID: t.WorkspaceID,
					ProjectID: t.ProjectID, ID: t.ID}))
		},
	})

	commands.Register(r, commands.Spec[typeIconPayload]{
		Type: SetTypeIcon, Capability: op, Scope: sc, Touches: ct,
		Check: func(p typeIconPayload) input.FieldErrors { return entityID(ids.ComponentType, "id", p.ID) },
		Inverse: func(ctx context.Context, tx *commands.Tx, p typeIconPayload) ([]commands.Step, error) {
			t, err := liveType(ctx, tx, "id", p.ID)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: SetTypeIcon, Payload: typeIconPayload{ID: p.ID, Icon: t.Icon}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p typeIconPayload) error {
			t, err := liveType(ctx, tx, "id", p.ID)
			if err != nil {
				return err
			}
			return changed(tx, ids.ComponentType, t.ID)(queries.New(tx).UpdateComponentTypeIcon(ctx,
				queries.UpdateComponentTypeIconParams{Icon: p.Icon, UpdatedBy: by(tx), WorkspaceID: t.WorkspaceID,
					ProjectID: t.ProjectID, ID: t.ID}))
		},
	})

	commands.Register(r, commands.Spec[deleteTypePayload]{
		Type: DeleteType, Capability: op, Scope: sc, Touches: ct,
		Check: func(p deleteTypePayload) input.FieldErrors { return entityID(ids.ComponentType, "id", p.ID) },
		// Deleting a type that is in the trash already does nothing, and its
		// undo nothing either.
		Inverse: func(ctx context.Context, tx *commands.Tx, p deleteTypePayload) ([]commands.Step, error) {
			t, err := anyType(ctx, tx, "id", p.ID)
			if err != nil || t.DeletedAt.Valid {
				return []commands.Step{}, err
			}
			return []commands.Step{{Type: RestoreType, Payload: typeIDPayload{ID: p.ID}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p deleteTypePayload) error {
			t, err := anyType(ctx, tx, "id", p.ID)
			if err != nil || t.DeletedAt.Valid {
				return err
			}
			q := queries.New(tx)
			inUse, err := q.ComponentTypeInUse(ctx, queries.ComponentTypeInUseParams{WorkspaceID: t.WorkspaceID,
				ProjectID: t.ProjectID, ComponentTypeID: t.ID})
			switch {
			case err != nil:
				return fmt.Errorf("component: read whether the type is in use: %w", err)
			case inUse:
				return commands.Refuse("id", "is a type of which a component that is not in the project's trash is: "+
					"delete its components first", ErrTypeInUse)
			}
			return changed(tx, ids.ComponentType, t.ID)(q.TrashComponentType(ctx, queries.TrashComponentTypeParams{
				UpdatedBy: by(tx), WorkspaceID: t.WorkspaceID, ProjectID: t.ProjectID, ID: t.ID}))
		},
	})

	commands.Register(r, commands.Spec[typeIDPayload]{
		Type: RestoreType, Capability: op, Scope: sc, Touches: ct,
		Check: func(p typeIDPayload) input.FieldErrors { return entityID(ids.ComponentType, "id", p.ID) },
		// Restoring a type that is not in the trash does nothing, and its
		// undo nothing either.
		Inverse: func(ctx context.Context, tx *commands.Tx, p typeIDPayload) ([]commands.Step, error) {
			t, err := anyType(ctx, tx, "id", p.ID)
			if err != nil || !t.DeletedAt.Valid {
				return []commands.Step{}, err
			}
			return []commands.Step{{Type: DeleteType, Payload: deleteTypePayload{ID: p.ID}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p typeIDPayload) error {
			t, err := anyType(ctx, tx, "id", p.ID)
			if err != nil || !t.DeletedAt.Valid {
				return err
			}
			return restoreType(ctx, tx, t, "id")
		},
	})
}

// applyCreateType is CreateType's Apply.
func applyCreateType(ctx context.Context, tx *commands.Tx, p createTypePayload) error {
	q := queries.New(tx)
	ws, project := nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID())
	id := decoded(ids.ComponentType, p.ID)
	// A type of the project with the ID is refused for its ID before anything
	// else is checked, as a variable's create is.
	if _, err := q.ComponentTypeAny(ctx, queries.ComponentTypeAnyParams{WorkspaceID: ws, ProjectID: project,
		ID: id}); err == nil {
		return commands.Refuse("id", "is the ID of a component type that exists already", nil)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("component: read the type: %w", err)
	}
	owner := typeKeyOwner(q, ws, project)
	key := p.Key
	if key == "" {
		var err error
		if key, err = deriveKey(ctx, func(ctx context.Context, stem string) ([]string, error) {
			return q.ComponentTypeKeysFrom(ctx, queries.ComponentTypeKeysFromParams{WorkspaceID: ws, ProjectID: project,
				Stem: stem})
		}, p.Name, "type"); err != nil {
			return fmt.Errorf("component: derive the type's key: %w", err)
		}
	} else if err := checkKey(ctx, owner, "key", key, pgtype.UUID{}, "component type"); err != nil {
		return err
	}
	icon := p.Icon
	if icon == "" {
		icon = IconNone
	}
	err := q.InsertComponentType(ctx, queries.InsertComponentTypeParams{ID: id, WorkspaceID: ws, ProjectID: project,
		Key: key, Name: p.Name, Icon: icon, CreatedBy: by(tx)})
	if database.IDTaken(err) {
		// The ID is a type's of a workspace or project whose rows this
		// transaction does not see, or an entity's of another kind of the
		// content, of any workspace or project.
		return commands.RefuseTakenID()
	}
	if err != nil {
		return fmt.Errorf("component: add the type: %w", err)
	}
	return tx.Changed(ids.ComponentType, id.Bytes, 1)
}

// restoreType restores the type t, which is in the trash, once it has
// checked that no other type of the project has taken its key since it was
// deleted, which it refuses the batch for otherwise, at the payload's field
// at, and reports the change.
func restoreType(ctx context.Context, tx *commands.Tx, t queries.ComponentType, at string) error {
	q := queries.New(tx)
	// The type in the trash, which nothing but its restore changes, keeps its
	// key, so the type that has taken it is the one to change.
	if err := keyFree(ctx, typeKeyOwner(q, t.WorkspaceID, t.ProjectID), at, t.Key, t.ID,
		"is of a component type whose key another type, which is not in the trash, has taken since it was "+
			"deleted: change that type's key, or delete it, first"); err != nil {
		return err
	}
	return changed(tx, ids.ComponentType, t.ID)(q.RestoreComponentType(ctx, queries.RestoreComponentTypeParams{
		UpdatedBy: by(tx), WorkspaceID: t.WorkspaceID, ProjectID: t.ProjectID, ID: t.ID}))
}

// typeKeyOwner returns the owner of a key among the project's types.
func typeKeyOwner(q *queries.Queries, ws, project pgtype.UUID) keyOwner {
	return func(ctx context.Context, key string) (pgtype.UUID, error) {
		return q.ComponentTypeWithKey(ctx, queries.ComponentTypeWithKeyParams{WorkspaceID: ws, ProjectID: project,
			Key: key})
	}
}

// anyType returns the type of the batch's project whose ID is id, a type's ID
// that its payload's Check checked, whether or not it is in the trash, or
// refuses the batch, at the payload's field at, if the project has no such
// type.
func anyType(ctx context.Context, tx *commands.Tx, at, id string) (queries.ComponentType, error) {
	row, err := queries.New(tx).ComponentTypeAny(ctx, queries.ComponentTypeAnyParams{
		WorkspaceID: nullUUID(tx.WorkspaceID()), ProjectID: nullUUID(tx.ProjectID()),
		ID: decoded(ids.ComponentType, id)})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return queries.ComponentType{}, commands.Refuse(at, "is not a component type of the project", nil)
	case err != nil:
		return queries.ComponentType{}, fmt.Errorf("component: read the type: %w", err)
	}
	return row.ComponentType, nil
}

// liveType returns the type of the batch's project whose ID is id as anyType
// does, or refuses the batch if it is in the trash, where nothing but
// restoring it changes it.
func liveType(ctx context.Context, tx *commands.Tx, at, id string) (queries.ComponentType, error) {
	t, err := anyType(ctx, tx, at, id)
	if err == nil && t.DeletedAt.Valid {
		return queries.ComponentType{}, commands.Refuse(at, "is a component type in the project's trash, which only "+
			"restoring it changes", nil)
	}
	return t, err
}
