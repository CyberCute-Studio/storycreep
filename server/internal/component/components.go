package component

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/expression"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// The types of command of a component (PRD §5.7). Each names the component
// that it is about by its ID, id, and applies to the component of the
// batch's project.
const (
	// Create creates a component of a type, whose ID the client makes, with
	// a value of each of the type's attribute definitions. Deleting it to the
	// trash undoes it.
	Create commands.Type = "component.create"
	// Rename changes a component's name, which leaves its key as it is
	// (FR-COMP-02 AC2), SetKey its key, and rewrites the sources of the
	// expressions that name the component's attributes, so that each names
	// what it named (FR-VAR-03), Describe its description, SetImage its image,
	// and SetAttribute the value that it holds of one of its type's attribute
	// definitions. Changing each back undoes it, a change of the key with the
	// sources that it rewrote as they were.
	Rename       commands.Type = "component.rename"
	SetKey       commands.Type = "component.set_key"
	Describe     commands.Type = "component.describe"
	SetImage     commands.Type = "component.set_image"
	SetAttribute commands.Type = "component.set_attribute"
	// Move moves a component into a folder of components, or to the top, at
	// a place among the folder's children. Moving it back undoes it.
	Move commands.Type = "component.move"
	// Delete deletes a component to the project's trash, unless sites of the
	// project use it, a node as its speaker, a text, or an expression, or the
	// deletion forces it (FR-COMP-04, FR-EDIT-06 AC2), and Restore restores
	// it, with the folders above it and its type, if they were deleted since.
	// Each undoes the other.
	Delete  commands.Type = "component.delete"
	Restore commands.Type = "component.restore"
)

// createPayload is Create's payload.
type createPayload struct {
	ID              string `json:"id" doc:"The new component's ID, a cmp_ ID of a UUIDv7 that the client makes, which no entity of the instance has (PRD §5.2)"`
	ComponentTypeID string `json:"component_type_id" doc:"The ID of the component's type, which is not in the project's trash, and which is fixed once the component is created"`
	Name            string `json:"name" maxLength:"100" doc:"The component's name, of 1 to 100 characters, not all of them blank, without white space around it, and without control characters, bidirectional controls, and line separators among them"`
	Key             string `json:"key,omitempty" doc:"The component's key, by which expressions name it, as elara in elara.trust: an identifier of the expression language (PRD §5.8.1): an ASCII letter or an underscore, then ASCII letters, digits, and underscores, of at most 64 characters, as elara, compared case by case, and not one of its reserved words, true, false, and, or, not, global, board, null, if, else, and elseif, which is answered with 422 key_invalid, and that no other component of the project has, of those that are not in its trash, which is answered with 409 key_taken. Without it, the key is derived from the name (PRD FR-COMP-02): its letters and digits in lowercase ASCII, without their accents, each run of other characters an underscore, with an underscore before it if it starts with a digit, or component for a name without them, and with _2, _3, and so on after it if another component of the project that is not in its trash has it, or it is a reserved word."`
	// Description is a pointer, so that a null leaves it out, as its absence
	// does.
	Description  *richtext.Doc `json:"description,omitempty" doc:"The component's description, a rich-text document (PRD FR-TEXT-01). Without it, an empty paragraph."`
	ImageAssetID string        `json:"image_asset_id,omitempty" doc:"The ID of the component's image, an asset of the project. Without it, none."`
	FolderID     string        `json:"folder_id,omitempty" doc:"The ID of the folder of components that the component goes into, which is not deleted. Without it, the component goes at the top."`
	Order        string        `json:"order,omitempty" maxLength:"128" pattern:"^[0-9A-Za-z]*[1-9A-Za-z]$" doc:"The component's place among the children of its folder, or of the top. A key of base-62 digits, 0-9, A-Z, and a-z, that does not end in 0, of at most 128 digits, which orders the children by its bytes, and those with the same key by their IDs without the prefix, cmp_ or fld_, compared by their bytes too: pick one between the keys of the children that it goes between, as V where there are none, 8 between 7 and 9, or 7V between 7 and 8. Without it, the component goes after the last child."`
	// Attributes are the values that the component is given, rather than the
	// definitions' defaults, by the definitions' IDs.
	Attributes map[string]value.Value `json:"attributes,omitempty" doc:"The values that the component holds of its type's attribute definitions that are not in the project's trash, by the definitions' IDs, each a value of its type: true or false for bool; an integer from -9007199254740991 to 9007199254740991 for int, which every runtime holds exactly (PRD §5.8.9), written as 3, 3.0, or 3e0 alike; a number for float, which is read as the nearest double, and is not beyond a double's range; a string of at most 4096 characters for string, and of at most 100000 for text, without control characters but line feeds and tabs; and one of its options for enum. A value of another kind, as a string for an int, is answered with 422 validation_failed, and so is a number written with more than 327 characters, or with an exponent beyond ±999, as a variable's default is. A definition that this leaves out gives the component its default."`
}

// renamePayload is Rename's payload.
type renamePayload struct {
	ID   string `json:"id" doc:"The component's ID"`
	Name string `json:"name" maxLength:"100" doc:"The component's new name, of 1 to 100 characters, not all of them blank, without white space around it, and without control characters, bidirectional controls, and line separators among them. Its key stays as it is (PRD FR-COMP-02 AC2)."`
}

// keyPayload is SetKey's payload.
type keyPayload struct {
	ID  string `json:"id" doc:"The component's ID"`
	Key string `json:"key" doc:"The component's new key: an identifier of the expression language (PRD §5.8.1): an ASCII letter or an underscore, then ASCII letters, digits, and underscores, of at most 64 characters, as elara, compared case by case, and not one of its reserved words, true, false, and, or, not, global, board, null, if, else, and elseif, which is answered with 422 key_invalid, and that no other component of the project has, of those that are not in its trash, which is answered with 409 key_taken. The component keeps its ID, by which saved games record its values (PRD FR-COMP-03 AC3). The change rewrites the source of each expression of the project, in its trash or not, that names the component's attributes, in the shortest form that names them from the expression's board, <component>.<attribute> unless a variable of the board or a global one has the new key, which cmp.<component>.<attribute> names past, so that each names what it named (PRD FR-COMP-02 AC2, FR-VAR-03), as GET /api/v1/projects/{project_id}/components/{component_id}/rename-preview lists them. A change that would make a source longer than 16384 characters is answered with 422 validation_failed, before anything is written."`
	// Sources is a pointer, so that an empty list, as the undo of a change
	// that rewrote nothing writes, is told from none, a change's own.
	Sources *[]expression.Source `json:"sources,omitempty" doc:"The sources that the undo of a change of the key gives back to the expressions of the owners' fields that it lists, those that the change rewrote, exactly as they were: each to its field's expression where the change of the component's key from key back to the key that it has would rewrite that source to the expression's as it is, which it then names and does exactly as. Every other expression that names the component's attributes, as one that names them by the key that it has written since the change, and one that it lists whose source has changed since, is rewritten as key says, as without it, and an owner's field that it lists that has no expression is passed over. Without it, the change rewrites the sources as key says."`
}

// deletePayload is Delete's payload.
type deletePayload struct {
	ID    string `json:"id" doc:"The component's ID"`
	Force bool   `json:"force,omitempty" doc:"Whether to delete the component even though sites of the project that are not in its trash use it, as its usage lists list them, but for its own description, which goes to the trash with it: the nodes whose speaker it is, which keep it as their speaker, in the trash, the texts that mention it or whose value references show its attributes, which then show nothing, and the expressions that read or write its attributes, which then name what no longer resolves, and are invalid, each a validation error (PRD FR-COMP-04, FR-VAR-04 AC2). Only a role that may publish the project's snapshots may force a deletion, which a role that may not is answered with 403 insufficient_role for; an API token needs project:write for it, as for any deletion, and not snapshot:write. Without it, a component that sites use is not deleted: the command is answered with 409 component_in_use, which counts and lists them, as the project stands once the batch's commands before it have applied (PRD FR-COMP-04 AC1, FR-EDIT-06 AC2)."`
}

// describePayload is Describe's payload.
type describePayload struct {
	ID          string       `json:"id" doc:"The component's ID"`
	Description richtext.Doc `json:"description" doc:"The component's new description, a rich-text document (PRD FR-TEXT-01), which may be an empty paragraph"`
}

// imagePayload is SetImage's payload.
type imagePayload struct {
	ID           string `json:"id" doc:"The component's ID"`
	ImageAssetID string `json:"image_asset_id,omitempty" doc:"The ID of the component's new image, an asset of the project. Without it, the component has none."`
}

// attributePayload is SetAttribute's payload.
type attributePayload struct {
	ID          string      `json:"id" doc:"The component's ID"`
	AttributeID string      `json:"attribute_id" doc:"The ID of the attribute definition, one of the component's type's that is not in the project's trash"`
	Value       value.Value `json:"value" doc:"The component's new value of the definition, a value of its type: true or false for bool; an integer from -9007199254740991 to 9007199254740991 for int, which every runtime holds exactly (PRD §5.8.9), written as 3, 3.0, or 3e0 alike; a number for float, which is read as the nearest double, and is not beyond a double's range; a string of at most 4096 characters for string, and of at most 100000 for text, without control characters but line feeds and tabs; and one of its options for enum. A value of another kind, as a string for an int, is answered with 422 validation_failed, and so is a number written with more than 327 characters, or with an exponent beyond ±999, as a variable's default is."`
}

// movePayload is Move's payload.
type movePayload struct {
	ID       string `json:"id" doc:"The component's ID"`
	FolderID string `json:"folder_id,omitempty" doc:"The ID of the folder of components that the component goes into, which is not deleted. Without it, the component goes to the top."`
	Order    string `json:"order,omitempty" maxLength:"128" pattern:"^[0-9A-Za-z]*[1-9A-Za-z]$" doc:"The component's place among the children of its folder, or of the top. A key of base-62 digits, 0-9, A-Z, and a-z, that does not end in 0, of at most 128 digits, which orders the children by its bytes, and those with the same key by their IDs without the prefix, cmp_ or fld_, compared by their bytes too: pick one between the keys of the children that it goes between, as V where there are none, 8 between 7 and 9, or 7V between 7 and 8. Without it, the component goes after the last child, or stays where it is if it is the last child of the folder already that is not deleted."`
}

// idPayload is the payload of a command that needs nothing but its
// component's ID.
type idPayload struct {
	ID string `json:"id" doc:"The component's ID"`
}

// registerComponents registers in r the commands of a component.
func registerComponents(r *commands.Registry) {
	cmp := []ids.Kind{ids.Component}
	op, sc := authz.ManageVariablesAndComponents, scope.ProjectWrite

	commands.Register(r, commands.Spec[createPayload]{
		Type: Create, Capability: op, Scope: sc,
		Touches: []ids.Kind{ids.Component, ids.ComponentType, ids.AttributeDef, ids.Folder},
		Check: func(p createPayload) input.FieldErrors {
			errs := newID(ids.Component, p.ID)
			errs = append(errs, entityID(ids.ComponentType, "component_type_id", p.ComponentTypeID)...)
			errs = append(errs, optionalID(ids.Asset, "image_asset_id", p.ImageAssetID)...)
			errs = append(errs, optionalID(ids.Folder, "folder_id", p.FolderID)...)
			for _, k := range slices.Sorted(maps.Keys(p.Attributes)) {
				errs = append(errs, entityID(ids.AttributeDef, "attributes."+k, k)...)
			}
			return errs
		},
		CheckNew: func(p createPayload) input.FieldErrors { return field("name", checkName(p.Name)) },
		// The undo of a create deletes the component, forcing the deletion if
		// what the create makes of the project uses it, as an expression that
		// named its key before it existed, so that the undo gives the project
		// back as it was, but not if only what came later uses it, which the
		// undo is then refused for, as a deletion is. A forced undo deletes
		// it whatever uses it then, as any forced deletion does, and needs a
		// role that may publish, as any does (FR-COMP-04).
		Inverse: func(ctx context.Context, tx *commands.Tx, p createPayload) ([]commands.Step, error) {
			q := queries.New(tx)
			key, err := createKey(ctx, q, nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID()), p)
			if err != nil {
				return nil, err
			}
			force, err := expression.Used(ctx, tx, expression.Subject{Kind: ids.Component,
				ID: decoded(ids.Component, p.ID).Bytes, Key: key, Absent: true,
				ComponentType: decoded(ids.ComponentType, p.ComponentTypeID).Bytes})
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: Delete, Payload: deletePayload{ID: p.ID, Force: force}}}, nil
		},
		Apply: applyCreate,
	})

	commands.Register(r, commands.Spec[renamePayload]{
		Type: Rename, Capability: op, Scope: sc, Touches: cmp,
		Check:    func(p renamePayload) input.FieldErrors { return entityID(ids.Component, "id", p.ID) },
		CheckNew: func(p renamePayload) input.FieldErrors { return field("name", checkName(p.Name)) },
		Inverse: func(ctx context.Context, tx *commands.Tx, p renamePayload) ([]commands.Step, error) {
			c, err := liveComponent(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: Rename, Payload: renamePayload{ID: p.ID, Name: c.Name}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p renamePayload) error {
			c, err := liveComponent(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			return changed(tx, ids.Component, c.ID)(queries.New(tx).UpdateComponentName(ctx,
				queries.UpdateComponentNameParams{Name: p.Name, UpdatedBy: by(tx), WorkspaceID: c.WorkspaceID,
					ProjectID: c.ProjectID, ID: c.ID}))
		},
	})

	// A change of the key rewrites the sources of the expressions of nodes,
	// connections, and branch outputs, whose changes it reports.
	rekeyed := []ids.Kind{ids.Component, ids.Node, ids.Connection, ids.BranchOutput}
	commands.Register(r, commands.Spec[keyPayload]{
		Type: SetKey, Capability: op, Scope: sc, Touches: rekeyed, UndoField: "key",
		Check: func(p keyPayload) input.FieldErrors {
			errs := entityID(ids.Component, "id", p.ID)
			if p.Sources != nil {
				errs = append(errs, expression.CheckSources("sources", *p.Sources)...)
			}
			return errs
		},
		Inverse: inverseSetKey,
		Apply:   applySetKey,
	})

	commands.Register(r, commands.Spec[describePayload]{
		Type: Describe, Capability: op, Scope: sc, Touches: cmp,
		Check: func(p describePayload) input.FieldErrors { return entityID(ids.Component, "id", p.ID) },
		Inverse: func(ctx context.Context, tx *commands.Tx, p describePayload) ([]commands.Step, error) {
			c, err := liveComponent(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			doc, err := richtext.Parse(c.Description)
			if err != nil {
				return nil, fmt.Errorf("component: the description of the component %s: %w", p.ID, err)
			}
			return []commands.Step{{Type: Describe, Payload: describePayload{ID: p.ID, Description: *doc}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p describePayload) error {
			c, err := liveComponent(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			doc, err := json.Marshal(p.Description)
			if err != nil {
				return fmt.Errorf("component: encode the description: %w", err)
			}
			return changed(tx, ids.Component, c.ID)(queries.New(tx).UpdateComponentDescription(ctx,
				queries.UpdateComponentDescriptionParams{Description: doc, UpdatedBy: by(tx),
					WorkspaceID: c.WorkspaceID, ProjectID: c.ProjectID, ID: c.ID}))
		},
	})

	commands.Register(r, commands.Spec[imagePayload]{
		Type: SetImage, Capability: op, Scope: sc, Touches: cmp,
		Check: func(p imagePayload) input.FieldErrors {
			return append(entityID(ids.Component, "id", p.ID), optionalID(ids.Asset, "image_asset_id",
				p.ImageAssetID)...)
		},
		Inverse: func(ctx context.Context, tx *commands.Tx, p imagePayload) ([]commands.Step, error) {
			c, err := liveComponent(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: SetImage, Payload: imagePayload{ID: p.ID,
				ImageAssetID: encode(ids.Asset, c.ImageAssetID)}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p imagePayload) error {
			c, err := liveComponent(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			q := queries.New(tx)
			image, err := projectImage(ctx, q, c.WorkspaceID, c.ProjectID, p.ImageAssetID)
			if err != nil {
				return err
			}
			return changed(tx, ids.Component, c.ID)(q.UpdateComponentImage(ctx, queries.UpdateComponentImageParams{
				ImageAssetID: image, UpdatedBy: by(tx), WorkspaceID: c.WorkspaceID, ProjectID: c.ProjectID, ID: c.ID}))
		},
	})

	commands.Register(r, commands.Spec[attributePayload]{
		Type: SetAttribute, Capability: op, Scope: sc, Touches: []ids.Kind{ids.Component, ids.AttributeDef},
		Check: func(p attributePayload) input.FieldErrors {
			return append(entityID(ids.Component, "id", p.ID), entityID(ids.AttributeDef, "attribute_id",
				p.AttributeID)...)
		},
		Inverse: func(ctx context.Context, tx *commands.Tx, p attributePayload) ([]commands.Step, error) {
			c, a, err := componentAttribute(ctx, tx, p)
			if err != nil {
				return nil, err
			}
			old, err := currentValue(ctx, queries.New(tx), c, a)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: SetAttribute, Payload: attributePayload{ID: p.ID, AttributeID: p.AttributeID,
				Value: old}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p attributePayload) error {
			c, a, err := componentAttribute(ctx, tx, p)
			if err != nil {
				return err
			}
			// The value's type is the definition's, which the payload does not
			// say, so Apply checks it whole, as a variable's set_default does.
			v, problem := p.Value.Of(a.Type, a.EnumOptions...)
			if problem != "" {
				return commands.Refuse("value", problem, nil)
			}
			q := queries.New(tx)
			n, err := q.SetAttributeValue(ctx, queries.SetAttributeValueParams{WorkspaceID: c.WorkspaceID,
				ProjectID: c.ProjectID, ComponentID: c.ID, AttributeDefID: a.ID, Value: v.JSON()})
			if err != nil {
				return fmt.Errorf("component: set the value: %w", err)
			}
			if n == 0 {
				return nil
			}
			return touch(ctx, tx, c.WorkspaceID, c.ProjectID, []pgtype.UUID{c.ID})
		},
	})

	commands.Register(r, commands.Spec[movePayload]{
		Type: Move, Capability: op, Scope: sc, Touches: []ids.Kind{ids.Component, ids.Folder},
		Check: func(p movePayload) input.FieldErrors {
			return append(entityID(ids.Component, "id", p.ID), optionalID(ids.Folder, "folder_id", p.FolderID)...)
		},
		Inverse: func(ctx context.Context, tx *commands.Tx, p movePayload) ([]commands.Step, error) {
			c, err := liveComponent(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: Move, Payload: movePayload{ID: p.ID, FolderID: encode(ids.Folder,
				c.FolderID), Order: c.Order}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p movePayload) error {
			c, err := liveComponent(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			q := queries.New(tx)
			folder, err := board.LiveFolder(ctx, q, c.WorkspaceID, c.ProjectID, board.KindComponent, "folder_id",
				p.FolderID)
			if err != nil {
				return err
			}
			order, err := board.SortKey(ctx, q, c.WorkspaceID, c.ProjectID, board.KindComponent, folder, p.Order,
				&board.Child{ID: c.ID, Parent: c.FolderID, Key: c.Order, Kind: ids.Component})
			if err != nil {
				return err
			}
			return changed(tx, ids.Component, c.ID)(q.MoveComponent(ctx, queries.MoveComponentParams{FolderID: folder,
				SortKey: order, UpdatedBy: by(tx), WorkspaceID: c.WorkspaceID, ProjectID: c.ProjectID, ID: c.ID}))
		},
	})

	commands.Register(r, commands.Spec[deletePayload]{
		Type: Delete, Capability: op, Scope: sc, Touches: cmp,
		Check: func(p deletePayload) input.FieldErrors { return entityID(ids.Component, "id", p.ID) },
		// Deleting a component that is in the trash already does nothing, and
		// its undo nothing either.
		Inverse: func(ctx context.Context, tx *commands.Tx, p deletePayload) ([]commands.Step, error) {
			c, err := anyComponent(ctx, tx, p.ID)
			if err != nil || c.DeletedAt.Valid {
				return []commands.Step{}, err
			}
			return []commands.Step{{Type: Restore, Payload: idPayload{ID: p.ID}}}, nil
		},
		Apply: applyDelete,
	})

	commands.Register(r, commands.Spec[idPayload]{
		Type: Restore, Capability: op, Scope: sc, Touches: []ids.Kind{ids.Component, ids.Folder, ids.ComponentType},
		Check: func(p idPayload) input.FieldErrors { return entityID(ids.Component, "id", p.ID) },
		// Restoring a component that is not in the trash does nothing, and its
		// undo nothing either. The undo of a restore deletes the component, then
		// the folders that the restore restored above it, from the nearest,
		// each of which then holds nothing that is not deleted, and then its
		// type, if the restore restored it, which then has no component that
		// is not in the trash.
		Inverse: func(ctx context.Context, tx *commands.Tx, p idPayload) ([]commands.Step, error) {
			c, err := anyComponent(ctx, tx, p.ID)
			if err != nil || !c.DeletedAt.Valid {
				return []commands.Step{}, err
			}
			q := queries.New(tx)
			deleted, err := board.DeletedFolders(ctx, q, c.WorkspaceID, c.ProjectID, c.FolderID)
			if err != nil {
				return nil, err
			}
			// The undo forces the deletion if what the restore makes of the
			// project uses the component, as a node whose speaker it is, or
			// an expression that names its attributes, as the undo of a create
			// does.
			force, err := expression.Used(ctx, tx, expression.Subject{Kind: ids.Component, ID: c.ID.Bytes, Key: c.Key,
				Absent: true, ComponentType: c.ComponentTypeID.Bytes})
			if err != nil {
				return nil, err
			}
			steps := append([]commands.Step{{Type: Delete, Payload: deletePayload{ID: p.ID, Force: force}}},
				board.DeleteFolderSteps(deleted)...)
			t, err := anyType(ctx, tx, "id", ids.Encode(ids.ComponentType, c.ComponentTypeID.Bytes))
			if err != nil {
				return nil, err
			}
			if t.DeletedAt.Valid {
				steps = append(steps, commands.Step{Type: DeleteType, Payload: deleteTypePayload{
					ID: ids.Encode(ids.ComponentType, t.ID.Bytes)}})
			}
			return steps, nil
		},
		Apply: applyRestore,
	})
}

// applyCreate is Create's Apply.
func applyCreate(ctx context.Context, tx *commands.Tx, p createPayload) error {
	q := queries.New(tx)
	ws, project := nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID())
	id := decoded(ids.Component, p.ID)
	// A component of the project with the ID is refused for its ID before
	// anything else is checked, as a variable's create is.
	if _, err := q.ComponentAny(ctx, queries.ComponentAnyParams{WorkspaceID: ws, ProjectID: project,
		ID: id}); err == nil {
		return commands.Refuse("id", "is the ID of a component that exists already", nil)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("component: read the component: %w", err)
	}
	t, err := liveType(ctx, tx, "component_type_id", p.ComponentTypeID)
	if err != nil {
		return err
	}
	key, err := createKey(ctx, q, ws, project, p)
	if err != nil {
		return err
	}
	if p.Key != "" {
		if err := checkKey(ctx, componentKeyOwner(q, ws, project), "key", key, pgtype.UUID{},
			"component"); err != nil {
			return err
		}
	}
	folder, err := board.LiveFolder(ctx, q, ws, project, board.KindComponent, "folder_id", p.FolderID)
	if err != nil {
		return err
	}
	order, err := board.SortKey(ctx, q, ws, project, board.KindComponent, folder, p.Order, nil)
	if err != nil {
		return err
	}
	image, err := projectImage(ctx, q, ws, project, p.ImageAssetID)
	if err != nil {
		return err
	}
	vals, err := createValues(ctx, q, t, p.Attributes)
	if err != nil {
		return err
	}
	var description richtext.Doc
	if p.Description != nil {
		description = *p.Description
	}
	doc, err := json.Marshal(description)
	if err != nil {
		return fmt.Errorf("component: encode the description: %w", err)
	}
	err = q.InsertComponent(ctx, queries.InsertComponentParams{ID: id, WorkspaceID: ws, ProjectID: project,
		ComponentTypeID: t.ID, FolderID: folder, SortKey: order, Key: key, Name: p.Name, Description: doc,
		ImageAssetID: image, CreatedBy: by(tx)})
	if database.UniqueViolation(err, "component_pkey") {
		// The ID is another workspace's, or another project's, whose rows this
		// transaction does not see.
		return commands.Refuse("id", "is the ID of a component that exists already", nil)
	}
	if err != nil {
		return fmt.Errorf("component: add the component: %w", err)
	}
	if err := q.InsertComponentValues(ctx, queries.InsertComponentValuesParams{WorkspaceID: ws, ProjectID: project,
		ComponentID: id, Vals: vals}); err != nil {
		return fmt.Errorf("component: add the component's values: %w", err)
	}
	return tx.Changed(ids.Component, id.Bytes, 1)
}

// createValues returns the values that a new component of the type t holds,
// as the JSON object of each by its definition's UUID: the value that given
// gives the definition, by its ID, or else its default. It refuses the batch
// for a definition in given that is not one of the type's that are not in the
// trash, or that given names twice, in two spellings of its ID, and for a
// value there that is not one of its definition's type.
func createValues(ctx context.Context, q *queries.Queries, t queries.ComponentType,
	given map[string]value.Value) ([]byte, error) {
	byID, err := keyedByUUID(ids.AttributeDef, "attributes", given)
	if err != nil {
		return nil, err
	}
	defs, err := q.TypeAttributeDefs(ctx, queries.TypeAttributeDefsParams{WorkspaceID: t.WorkspaceID,
		ProjectID: t.ProjectID, TypeIds: []pgtype.UUID{t.ID}})
	if err != nil {
		return nil, fmt.Errorf("component: read the type's attribute definitions: %w", err)
	}
	vals := make(map[string]json.RawMessage, len(defs))
	for _, row := range defs {
		a := queries.AttributeDef(row.LiveAttributeDef)
		var v value.Value
		if g, ok := byID[a.ID.Bytes]; ok {
			delete(byID, a.ID.Bytes)
			var problem string
			if v, problem = g.value.Of(a.Type, a.EnumOptions...); problem != "" {
				return nil, commands.Refuse(g.at, problem, nil)
			}
		} else if v, err = storedDefault(a); err != nil {
			return nil, err
		}
		vals[ids.UUID(a.ID.Bytes).String()] = v.JSON()
	}
	// The one that sorts first is refused, so that it is the same each time.
	if left := sortedKeyed(byID); len(left) > 0 {
		return nil, commands.Refuse(byID[left[0]].at, "is not an attribute definition of the component's type, or "+
			"is one in the project's trash", nil)
	}
	// A component holds a value of every definition of its type, those in
	// the trash too (PRD §5.3), which their defaults are, as a definition's
	// restoration would give it, so that the undo of the component's creation
	// and its redo, a restoration, leave its values as its creation did.
	deleted, err := q.TypeDeletedAttributeDefs(ctx, queries.TypeDeletedAttributeDefsParams{
		WorkspaceID: t.WorkspaceID, ProjectID: t.ProjectID, ComponentTypeID: t.ID})
	if err != nil {
		return nil, fmt.Errorf("component: read the type's deleted attribute definitions: %w", err)
	}
	for _, row := range deleted {
		v, err := storedDefault(row.AttributeDef)
		if err != nil {
			return nil, err
		}
		vals[ids.UUID(row.AttributeDef.ID.Bytes).String()] = v.JSON()
	}
	return json.Marshal(vals)
}

// keyed is a value of a payload's map, with the field that it is at.
type keyed struct {
	at    string
	value value.Value
}

// keyedByUUID returns the values of m, a payload's map at the field name of
// values by the IDs of entities of the kind, which its Check checked, by
// their UUIDs, each with its field. It refuses the batch for an entity that
// m names twice, in two spellings of its ID, which a client may write in
// either case.
func keyedByUUID(kind ids.Kind, name string, m map[string]value.Value) (map[ids.UUID]keyed, error) {
	out := make(map[ids.UUID]keyed, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		u, _ := ids.Decode(kind, k)
		at := name + "." + k
		if _, dup := out[u]; dup {
			return nil, commands.Refuse(at, fmt.Sprintf("names the %s %s, which another spelling of its ID names "+
				"too", kind, ids.Encode(kind, u)), nil)
		}
		out[u] = keyed{at: at, value: m[k]}
	}
	return out, nil
}

// createKey returns the key of the component that the create p creates: its
// payload's, or the one derived from its name, which no other component of
// the project has outside its trash (FR-COMP-02), as Create's Inverse and
// Apply read it alike.
func createKey(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, p createPayload) (string, error) {
	if p.Key != "" {
		return p.Key, nil
	}
	key, err := deriveKey(ctx, func(ctx context.Context, stem string) ([]string, error) {
		return q.ComponentKeysFrom(ctx, queries.ComponentKeysFromParams{WorkspaceID: ws, ProjectID: project,
			Stem: stem})
	}, p.Name, "component")
	if err != nil {
		return "", fmt.Errorf("component: derive the component's key: %w", err)
	}
	return key, nil
}

// applyDelete is Delete's Apply. A component that sites of the project use,
// as the batch's commands before it leave the project, a node as its speaker
// among them, is deleted only if the deletion forces it, which the actor's
// role must allow, and is refused with a *dependency.InUseError otherwise
// (expression.GuardDeletion, FR-COMP-04 AC1, FR-EDIT-06 AC2).
func applyDelete(ctx context.Context, tx *commands.Tx, p deletePayload) error {
	c, err := anyComponent(ctx, tx, p.ID)
	if err != nil || c.DeletedAt.Valid {
		return err
	}
	if err := expression.GuardDeletion(ctx, tx, expression.Subject{Kind: ids.Component, ID: c.ID.Bytes, Key: c.Key},
		p.Force); err != nil {
		return err
	}
	return changed(tx, ids.Component, c.ID)(queries.New(tx).TrashComponent(ctx, queries.TrashComponentParams{
		UpdatedBy: by(tx), WorkspaceID: c.WorkspaceID, ProjectID: c.ProjectID, ID: c.ID}))
}

// inverseSetKey is SetKey's Inverse: a change back to the component's key,
// with the sources that the change writes as they are, which the undo gives
// back rather than rewrite them anew, as a variable's rename's does
// (expression.RenameInverse).
func inverseSetKey(ctx context.Context, tx *commands.Tx, p keyPayload) ([]commands.Step, error) {
	c, err := liveComponent(ctx, tx, p.ID)
	if err != nil {
		return nil, err
	}
	// The key is checked before the rewrites are planned, which need a key
	// that the component may have, as Apply checks it.
	if err := checkKey(ctx, componentKeyOwner(queries.New(tx), c.WorkspaceID, c.ProjectID), "key", p.Key, c.ID,
		"component"); err != nil {
		return nil, err
	}
	return expression.RenameInverse(ctx, tx, SetKey, "key", rekey(c, p.Key), p.Sources,
		func(back []expression.Source) any { return keyPayload{ID: p.ID, Key: c.Key, Sources: &back} })
}

// applySetKey is SetKey's Apply: it checks the new key, before it writes
// anything, writes the sources that the change writes
// (expression.ApplyRename), and changes the key.
func applySetKey(ctx context.Context, tx *commands.Tx, p keyPayload) error {
	c, err := liveComponent(ctx, tx, p.ID)
	if err != nil {
		return err
	}
	q := queries.New(tx)
	if err := checkKey(ctx, componentKeyOwner(q, c.WorkspaceID, c.ProjectID), "key", p.Key, c.ID,
		"component"); err != nil {
		return err
	}
	if err := expression.ApplyRename(ctx, tx, "key", rekey(c, p.Key), p.Sources); err != nil {
		return err
	}
	return changed(tx, ids.Component, c.ID)(q.UpdateComponentKey(ctx, queries.UpdateComponentKeyParams{
		Key: p.Key, UpdatedBy: by(tx), WorkspaceID: c.WorkspaceID, ProjectID: c.ProjectID, ID: c.ID}))
}

// rekey returns the rename of the component c to the key.
func rekey(c queries.Component, key string) expression.Rename {
	return expression.Rename{Kind: ids.Component, ID: c.ID.Bytes, From: c.Key, To: key}
}

// applyRestore is Restore's Apply.
func applyRestore(ctx context.Context, tx *commands.Tx, p idPayload) error {
	c, err := anyComponent(ctx, tx, p.ID)
	if err != nil || !c.DeletedAt.Valid {
		return err
	}
	q := queries.New(tx)
	// The component in the trash, which nothing but its restore changes,
	// keeps its key, so the component that has taken it is the one to
	// change.
	if err := keyFree(ctx, componentKeyOwner(q, c.WorkspaceID, c.ProjectID), "id", c.Key, c.ID,
		"is a component whose key another component, which is not in the trash, has taken since it was deleted: "+
			"change that component's key, or delete it, first"); err != nil {
		return err
	}
	t, err := anyType(ctx, tx, "id", ids.Encode(ids.ComponentType, c.ComponentTypeID.Bytes))
	if err != nil {
		return err
	}
	if t.DeletedAt.Valid {
		if err := restoreType(ctx, tx, t, "id"); err != nil {
			return err
		}
	}
	if err := board.RestoreFolders(ctx, tx, q, c.WorkspaceID, c.ProjectID, c.FolderID); err != nil {
		return err
	}
	return changed(tx, ids.Component, c.ID)(q.RestoreComponent(ctx, queries.RestoreComponentParams{
		UpdatedBy: by(tx), WorkspaceID: c.WorkspaceID, ProjectID: c.ProjectID, ID: c.ID}))
}

// projectImage returns the UUID of the asset whose ID is id, an asset's ID
// that a payload's Check checked, as the column of a component's image, or a
// null column if id is "", for none, once it has checked that it is one of
// the project's assets. It refuses the batch otherwise, at image_asset_id.
func projectImage(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, id string) (pgtype.UUID,
	error) {
	if id == "" {
		return pgtype.UUID{}, nil
	}
	a, err := q.Asset(ctx, queries.AssetParams{WorkspaceID: ws, ProjectID: project, ID: decoded(ids.Asset, id)})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return pgtype.UUID{}, commands.Refuse("image_asset_id", "is not an asset of the project", nil)
	case err != nil:
		return pgtype.UUID{}, fmt.Errorf("component: read the asset: %w", err)
	}
	return a.ID, nil
}

// componentAttribute returns the component and the attribute definition of
// the payload p, once it has checked that the component is not in the trash
// and that the definition is one of its type's that is not in the trash. It
// refuses the batch otherwise.
func componentAttribute(ctx context.Context, tx *commands.Tx, p attributePayload) (queries.Component,
	queries.AttributeDef, error) {
	c, err := liveComponent(ctx, tx, p.ID)
	if err != nil {
		return queries.Component{}, queries.AttributeDef{}, err
	}
	row, err := queries.New(tx).AttributeDefAny(ctx, queries.AttributeDefAnyParams{WorkspaceID: c.WorkspaceID,
		ProjectID: c.ProjectID, ID: decoded(ids.AttributeDef, p.AttributeID)})
	switch {
	case errors.Is(err, pgx.ErrNoRows) || err == nil && (row.AttributeDef.ComponentTypeID != c.ComponentTypeID ||
		row.AttributeDef.DeletedAt.Valid || row.TypeDeleted):
		return queries.Component{}, queries.AttributeDef{}, commands.Refuse("attribute_id", "is not an attribute "+
			"definition of the component's type, or is one in the project's trash", nil)
	case err != nil:
		return queries.Component{}, queries.AttributeDef{}, fmt.Errorf("component: read the attribute definition: %w",
			err)
	}
	return c, row.AttributeDef, nil
}

// currentValue returns the value that the component c holds of the attribute
// definition a, one of its type's that is not in the trash, in its canonical
// form, or the definition's default if it holds none, which no component
// does.
func currentValue(ctx context.Context, q *queries.Queries, c queries.Component, a queries.AttributeDef) (
	value.Value, error) {
	raw, err := q.ComponentValue(ctx, queries.ComponentValueParams{WorkspaceID: c.WorkspaceID,
		ProjectID: c.ProjectID, ComponentID: c.ID, AttributeDefID: a.ID})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return storedDefault(a)
	case err != nil:
		return value.Value{}, fmt.Errorf("component: read the component's value: %w", err)
	}
	return storedValue(a, c.ID, raw)
}

// componentKeyOwner returns the owner of a key among the project's
// components.
func componentKeyOwner(q *queries.Queries, ws, project pgtype.UUID) keyOwner {
	return func(ctx context.Context, key string) (pgtype.UUID, error) {
		return q.ComponentWithKey(ctx, queries.ComponentWithKeyParams{WorkspaceID: ws, ProjectID: project, Key: key})
	}
}

// anyComponent returns the component of the batch's project whose ID is id, a
// component's ID that its payload's Check checked, whether or not it is in
// the trash, or refuses the batch if the project has no such component.
func anyComponent(ctx context.Context, tx *commands.Tx, id string) (queries.Component, error) {
	row, err := queries.New(tx).ComponentAny(ctx, queries.ComponentAnyParams{WorkspaceID: nullUUID(tx.WorkspaceID()),
		ProjectID: nullUUID(tx.ProjectID()), ID: decoded(ids.Component, id)})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return queries.Component{}, commands.Refuse("id", "is not a component of the project", nil)
	case err != nil:
		return queries.Component{}, fmt.Errorf("component: read the component: %w", err)
	}
	return row.Component, nil
}

// liveComponent returns the component of the batch's project whose ID is id
// as anyComponent does, or refuses the batch if it is in the trash, where
// nothing but restoring it changes it.
func liveComponent(ctx context.Context, tx *commands.Tx, id string) (queries.Component, error) {
	c, err := anyComponent(ctx, tx, id)
	if err == nil && c.DeletedAt.Valid {
		return queries.Component{}, commands.Refuse("id", "is a component in the project's trash, which only "+
			"restoring it changes", nil)
	}
	return c, err
}
