package component

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// The types of command of an attribute definition (PRD §5.7). Each names the
// definition that it is about by its ID, id, and applies to the definition
// of the batch's project.
const (
	// CreateAttribute adds an attribute definition to a component type,
	// whose ID the client makes, and gives each component of the type its
	// default, as one command (FR-COMP-01 AC1). Deleting it to the trash
	// undoes it.
	CreateAttribute commands.Type = "attribute.create"
	// RenameAttribute changes a definition's name, SetAttributeKey its key,
	// SetAttributeDefault its default, which leaves the values that its
	// components hold as they are, and SetAttributeRuntime whether it is part
	// of the game's state. Changing each back undoes it.
	RenameAttribute     commands.Type = "attribute.rename"
	SetAttributeKey     commands.Type = "attribute.set_key"
	SetAttributeDefault commands.Type = "attribute.set_default"
	SetAttributeRuntime commands.Type = "attribute.set_runtime"
	// SetAttributeOptions changes an enum's options, and gives a value that
	// is none of them the default, unless the command gives another, and
	// RetypeAttribute changes a definition's type, and converts its values as
	// the command's conversion says (FR-COMP-01 AC2). Each is undone by
	// another that sets the options or the type back, with the values that
	// the change lost.
	SetAttributeOptions commands.Type = "attribute.set_options"
	RetypeAttribute     commands.Type = "attribute.retype"
	// MoveAttribute places a definition among its type's. Moving it back
	// undoes it.
	MoveAttribute commands.Type = "attribute.move"
	// DeleteAttribute deletes a definition to the project's trash, with the
	// values that its components hold, and RestoreAttribute restores it with
	// them. Each undoes the other.
	DeleteAttribute  commands.Type = "attribute.delete"
	RestoreAttribute commands.Type = "attribute.restore"
)

// createAttributePayload is CreateAttribute's payload.
type createAttributePayload struct {
	ID              string      `json:"id" doc:"The new attribute definition's ID, an atr_ ID of a UUIDv7 that the client makes, which no entity of the instance has (PRD §5.2)"`
	ComponentTypeID string      `json:"component_type_id" doc:"The ID of the component type that the definition is added to, which is not in the project's trash, and which has fewer than 100 definitions that are not"`
	Name            string      `json:"name" maxLength:"100" doc:"The definition's name, of 1 to 100 characters, not all of them blank, without white space around it, and without control characters, bidirectional controls, and line separators among them"`
	Key             string      `json:"key,omitempty" doc:"The definition's key, by which expressions name its values, as trust in elara.trust: an identifier of the expression language (PRD §5.8.1): an ASCII letter or an underscore, then ASCII letters, digits, and underscores, of at most 64 characters, as trust, compared case by case, and not one of its reserved words, true, false, and, or, not, global, board, null, if, else, and elseif, which is answered with 422 key_invalid, and that no other attribute definition of its type has, of those that are not in the project's trash, which is answered with 409 key_taken. Without it, the key is derived from the name, as a component type's is, or is attribute for a name without letters or digits."`
	Type            string      `json:"type" enum:"bool,int,float,string,text,enum" doc:"The definition's type: bool, int, float, string, of at most 4096 characters, text, a string of several lines of at most 100000 characters, or enum, one of its options"`
	Options         []string    `json:"options,omitempty" maxItems:"100" doc:"An enum's options, which its values are one of: 1 to 100 options, each of 1 to 100 characters, not all of them blank, without white space around it, and without control characters, bidirectional controls, line feeds, line separators, and U+FFFD, the replacement character, which an escape of a lone surrogate is read as, among them, no two alike, compared by their characters, as they are. Another type has none."`
	Default         value.Value `json:"default,omitzero" doc:"The definition's default, which each component of its type is given: a value of its type: true or false for bool; an integer from -9007199254740991 to 9007199254740991 for int, which every runtime holds exactly (PRD §5.8.9), written as 3, 3.0, or 3e0 alike; a number for float, which is read as the nearest double, and is not beyond a double's range; a string of at most 4096 characters for string, and of at most 100000 for text, without control characters but line feeds and tabs; and one of its options for enum. A value of another kind, as a string for an int, is answered with 422 validation_failed, and so is a number written with more than 327 characters, or with an exponent beyond ±999, as a variable's default is. Without it, the type's zero value: false, 0, 0.0, the empty string, or an enum's first option."`
	Runtime         bool        `json:"runtime,omitempty" doc:"Whether the definition's values are part of the game's state, which expressions read and write, and which saved games record (PRD FR-COMP-03). A text may not be. Without it, false."`
	Order           string      `json:"order,omitempty" maxLength:"128" pattern:"^[0-9A-Za-z]*[1-9A-Za-z]$" doc:"The definition's place among its type's. A key of base-62 digits, 0-9, A-Z, and a-z, that does not end in 0, of at most 128 digits, which orders the definitions by its bytes, and those with the same key by their IDs without the prefix, atr_, compared by their bytes too: pick one between the keys of the definitions that it goes between, as V where there are none, 8 between 7 and 9, or 7V between 7 and 8. Without it, the definition goes after the type's last."`
}

// renameAttributePayload is RenameAttribute's payload.
type renameAttributePayload struct {
	ID   string `json:"id" doc:"The attribute definition's ID"`
	Name string `json:"name" maxLength:"100" doc:"The definition's new name, of 1 to 100 characters, not all of them blank, without white space around it, and without control characters, bidirectional controls, and line separators among them. Its key stays as it is."`
}

// attributeKeyPayload is SetAttributeKey's payload.
type attributeKeyPayload struct {
	ID  string `json:"id" doc:"The attribute definition's ID"`
	Key string `json:"key" doc:"The definition's new key: an identifier of the expression language (PRD §5.8.1): an ASCII letter or an underscore, then ASCII letters, digits, and underscores, of at most 64 characters, as trust, compared case by case, and not one of its reserved words, true, false, and, or, not, global, board, null, if, else, and elseif, which is answered with 422 key_invalid, and that no other attribute definition of its type has, of those that are not in the project's trash, which is answered with 409 key_taken. The definition keeps its ID, by which saved games record its values (PRD FR-COMP-03 AC3)."`
}

// attributeDefaultPayload is SetAttributeDefault's payload.
type attributeDefaultPayload struct {
	ID      string      `json:"id" doc:"The attribute definition's ID"`
	Default value.Value `json:"default" doc:"The definition's new default, which a component of its type that is created later is given: a value of its type: true or false for bool; an integer from -9007199254740991 to 9007199254740991 for int, which every runtime holds exactly (PRD §5.8.9), written as 3, 3.0, or 3e0 alike; a number for float, which is read as the nearest double, and is not beyond a double's range; a string of at most 4096 characters for string, and of at most 100000 for text, without control characters but line feeds and tabs; and one of its options for enum. A value of another kind, as a string for an int, is answered with 422 validation_failed, and so is a number written with more than 327 characters, or with an exponent beyond ±999, as a variable's default is. The values that the type's components hold stay as they are."`
}

// attributeRuntimePayload is SetAttributeRuntime's payload.
type attributeRuntimePayload struct {
	ID      string `json:"id" doc:"The attribute definition's ID"`
	Runtime bool   `json:"runtime" doc:"Whether the definition's values are part of the game's state, which expressions read and write, and which saved games record (PRD FR-COMP-03). A text may not be."`
}

// attributeOptionsPayload is SetAttributeOptions's payload.
type attributeOptionsPayload struct {
	ID      string      `json:"id" doc:"The ID of the attribute definition, an enum"`
	Options []string    `json:"options" maxItems:"100" doc:"The enum's new options, in their order: 1 to 100 options, each of 1 to 100 characters, not all of them blank, without white space around it, and without control characters, bidirectional controls, line feeds, line separators, and U+FFFD, the replacement character, which an escape of a lone surrogate is read as, among them, no two alike, compared by their characters, as they are. A value that a component holds that is none of them is given the default, unless values gives it another."`
	Default value.Value `json:"default,omitzero" doc:"The enum's new default, one of its new options. Without it, the default stays as it is, which must then be one of them."`
	// Values are the values that the components are given, rather than the
	// default, by their IDs.
	Values map[string]value.Value `json:"values,omitempty" doc:"The values, each one of the new options, that components of the definition's type are given, by their IDs, in place of what they hold: a component that holds a value that is none of the new options, and is not given one here, is given the default. A component of another type is refused, and one that the project no longer has, as one that the purge has deleted, is passed over."`
}

// retypeAttributePayload is RetypeAttribute's payload.
type retypeAttributePayload struct {
	ID         string      `json:"id" doc:"The attribute definition's ID"`
	Type       string      `json:"type" enum:"bool,int,float,string,text,enum" doc:"The definition's new type, which is not its type already: an enum's options change with attribute.set_options. A runtime definition may not become a text: unset runtime first."`
	Conversion string      `json:"conversion" enum:"convert,reset" doc:"What becomes of the values that the type's components hold, those in the project's trash included (PRD FR-COMP-01 AC2): convert converts each to the new type, as the expression language's to_int, to_float, and to_string do (PRD §5.8.4, §5.8.9), a bool as 0 or 1 and an int or a float that is 0 or 1 as a bool, and a string that is true or false as a bool, and gives the new default to each that does not convert, which GET /api/v1/projects/{project_id}/component-types/{component_type_id}/attributes/{attribute_id}/conversion lists beforehand; reset gives every value the new default."`
	Options    []string    `json:"options,omitempty" maxItems:"100" doc:"The options of an enum, which the new type then is: 1 to 100 options, each of 1 to 100 characters, not all of them blank, without white space around it, and without control characters, bidirectional controls, line feeds, line separators, and U+FFFD, the replacement character, which an escape of a lone surrogate is read as, among them, no two alike, compared by their characters, as they are. Another type has none."`
	Default    value.Value `json:"default,omitzero" doc:"The definition's default of its new type: a value of its type: true or false for bool; an integer from -9007199254740991 to 9007199254740991 for int, which every runtime holds exactly (PRD §5.8.9), written as 3, 3.0, or 3e0 alike; a number for float, which is read as the nearest double, and is not beyond a double's range; a string of at most 4096 characters for string, and of at most 100000 for text, without control characters but line feeds and tabs; and one of its options for enum. A value of another kind, as a string for an int, is answered with 422 validation_failed, and so is a number written with more than 327 characters, or with an exponent beyond ±999, as a variable's default is. Without it, the type's zero value: false, 0, 0.0, the empty string, or an enum's first option."`
	// Values are the values that the components are given, rather than what
	// the conversion makes of theirs, by their IDs.
	Values map[string]value.Value `json:"values,omitempty" doc:"The values of the new type that components of the definition's type are given, by their IDs, in place of what the conversion makes of what they hold. A component of another type is refused, and one that the project no longer has, as one that the purge has deleted, is passed over."`
}

// moveAttributePayload is MoveAttribute's payload.
type moveAttributePayload struct {
	ID    string `json:"id" doc:"The attribute definition's ID"`
	Order string `json:"order,omitempty" maxLength:"128" pattern:"^[0-9A-Za-z]*[1-9A-Za-z]$" doc:"The definition's place among its type's. A key of base-62 digits, 0-9, A-Z, and a-z, that does not end in 0, of at most 128 digits, which orders the definitions by its bytes, and those with the same key by their IDs without the prefix, atr_, compared by their bytes too: pick one between the keys of the definitions that it goes between, as V where there are none, 8 between 7 and 9, or 7V between 7 and 8. Without it, the definition goes after the type's last, or stays where it is if it is the last already that is not deleted."`
}

// attributeIDPayload is the payload of a command that needs nothing but its
// definition's ID.
type attributeIDPayload struct {
	ID string `json:"id" doc:"The attribute definition's ID"`
}

// registerAttributes registers in r the commands of an attribute definition.
func registerAttributes(r *commands.Registry) {
	atr := []ids.Kind{ids.AttributeDef}
	// A command that changes the values of the definition's components
	// changes the components too.
	values := []ids.Kind{ids.AttributeDef, ids.Component}
	op, sc := authz.ManageVariablesAndComponents, scope.ProjectWrite

	commands.Register(r, commands.Spec[createAttributePayload]{
		Type: CreateAttribute, Capability: op, Scope: sc,
		Touches: []ids.Kind{ids.AttributeDef, ids.ComponentType, ids.Component},
		// A create is never a step of an inverse, so its Check checks the
		// default whole, as a variable's create does, once the options that it
		// is checked against are valid.
		Check: func(p createAttributePayload) input.FieldErrors {
			errs := newID(ids.AttributeDef, p.ID)
			errs = append(errs, entityID(ids.ComponentType, "component_type_id", p.ComponentTypeID)...)
			if options := checkOptions(p.Type, p.Options); len(options) > 0 {
				return append(errs, options...)
			}
			errs = append(errs, checkRuntime("runtime", p.Type, p.Runtime)...)
			_, problem := p.Default.Of(p.Type, p.Options...)
			return append(errs, field("default", problem)...)
		},
		CheckNew: func(p createAttributePayload) input.FieldErrors {
			errs := field("name", checkName(p.Name))
			return append(errs, field("options", value.CheckOptionNames(p.Options))...)
		},
		Inverse: func(_ context.Context, _ *commands.Tx, p createAttributePayload) ([]commands.Step, error) {
			return []commands.Step{{Type: DeleteAttribute, Payload: attributeIDPayload{ID: p.ID}}}, nil
		},
		Apply: applyCreateAttribute,
	})

	commands.Register(r, commands.Spec[renameAttributePayload]{
		Type: RenameAttribute, Capability: op, Scope: sc, Touches: atr,
		Check: func(p renameAttributePayload) input.FieldErrors { return entityID(ids.AttributeDef, "id", p.ID) },
		CheckNew: func(p renameAttributePayload) input.FieldErrors {
			return field("name", checkName(p.Name))
		},
		Inverse: func(ctx context.Context, tx *commands.Tx, p renameAttributePayload) ([]commands.Step, error) {
			a, err := liveAttribute(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: RenameAttribute, Payload: renameAttributePayload{ID: p.ID, Name: a.Name}}},
				nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p renameAttributePayload) error {
			a, err := liveAttribute(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			return changed(tx, ids.AttributeDef, a.ID)(queries.New(tx).UpdateAttributeDefName(ctx,
				queries.UpdateAttributeDefNameParams{Name: p.Name, UpdatedBy: by(tx), WorkspaceID: a.WorkspaceID,
					ProjectID: a.ProjectID, ID: a.ID}))
		},
	})

	commands.Register(r, commands.Spec[attributeKeyPayload]{
		Type: SetAttributeKey, Capability: op, Scope: sc, Touches: atr,
		Check: func(p attributeKeyPayload) input.FieldErrors { return entityID(ids.AttributeDef, "id", p.ID) },
		Inverse: func(ctx context.Context, tx *commands.Tx, p attributeKeyPayload) ([]commands.Step, error) {
			a, err := liveAttribute(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: SetAttributeKey, Payload: attributeKeyPayload{ID: p.ID, Key: a.Key}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p attributeKeyPayload) error {
			a, err := liveAttribute(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			q := queries.New(tx)
			if err := checkKey(ctx, attributeKeyOwner(q, a.WorkspaceID, a.ProjectID, a.ComponentTypeID), "key",
				p.Key, a.ID, "attribute definition of the type"); err != nil {
				return err
			}
			return changed(tx, ids.AttributeDef, a.ID)(q.UpdateAttributeDefKey(ctx, queries.UpdateAttributeDefKeyParams{
				Key: p.Key, UpdatedBy: by(tx), WorkspaceID: a.WorkspaceID, ProjectID: a.ProjectID, ID: a.ID}))
		},
	})

	commands.Register(r, commands.Spec[attributeDefaultPayload]{
		Type: SetAttributeDefault, Capability: op, Scope: sc, Touches: atr,
		Check: func(p attributeDefaultPayload) input.FieldErrors { return entityID(ids.AttributeDef, "id", p.ID) },
		Inverse: func(ctx context.Context, tx *commands.Tx, p attributeDefaultPayload) ([]commands.Step, error) {
			a, err := liveAttribute(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			old, err := storedDefault(a)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: SetAttributeDefault, Payload: attributeDefaultPayload{ID: p.ID,
				Default: old}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p attributeDefaultPayload) error {
			a, err := liveAttribute(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			// The default's type is the definition's, which the payload does not
			// say, so Apply checks it whole, as a variable's set_default does.
			def, problem := p.Default.Of(a.Type, a.EnumOptions...)
			if problem != "" {
				return commands.Refuse("default", problem, nil)
			}
			return setDefinition(ctx, tx, a, a.Type, a.EnumOptions, def)
		},
	})

	commands.Register(r, commands.Spec[attributeRuntimePayload]{
		Type: SetAttributeRuntime, Capability: op, Scope: sc, Touches: atr,
		Check: func(p attributeRuntimePayload) input.FieldErrors { return entityID(ids.AttributeDef, "id", p.ID) },
		Inverse: func(ctx context.Context, tx *commands.Tx, p attributeRuntimePayload) ([]commands.Step, error) {
			a, err := liveAttribute(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: SetAttributeRuntime, Payload: attributeRuntimePayload{ID: p.ID,
				Runtime: a.IsRuntime}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p attributeRuntimePayload) error {
			a, err := liveAttribute(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			if err := refuse(checkRuntime("runtime", a.Type, p.Runtime)); err != nil {
				return err
			}
			return changed(tx, ids.AttributeDef, a.ID)(queries.New(tx).UpdateAttributeDefRuntime(ctx,
				queries.UpdateAttributeDefRuntimeParams{IsRuntime: p.Runtime, UpdatedBy: by(tx),
					WorkspaceID: a.WorkspaceID, ProjectID: a.ProjectID, ID: a.ID}))
		},
	})

	commands.Register(r, commands.Spec[attributeOptionsPayload]{
		Type: SetAttributeOptions, Capability: op, Scope: sc, Touches: values,
		// The undo holds the values that the change loses, which grow with the
		// type's components.
		UndoField: "options",
		Check: func(p attributeOptionsPayload) input.FieldErrors {
			errs := entityID(ids.AttributeDef, "id", p.ID)
			if problem := value.CheckOptions(p.Options); problem != "" {
				return append(errs, field("options", problem)...)
			}
			if !p.Default.IsZero() {
				_, problem := p.Default.Read(value.Enum, value.Checks{}, p.Options...)
				errs = append(errs, field("default", problem)...)
			}
			return append(errs, checkValues(p.Values, value.Enum, p.Options)...)
		},
		CheckNew: func(p attributeOptionsPayload) input.FieldErrors {
			return field("options", value.CheckOptionNames(p.Options))
		},
		Inverse: func(ctx context.Context, tx *commands.Tx, p attributeOptionsPayload) ([]commands.Step, error) {
			plan, err := planOptions(ctx, tx, p)
			if err != nil {
				return nil, err
			}
			// The undo gives a value that is none of the old options the old
			// default, so it holds every value that the change changes.
			undo := attributeOptionsPayload{ID: p.ID, Options: plan.a.EnumOptions, Default: plan.oldDefault}
			lost, err := plan.lost(ctx, tx, undo, func(value.Value) (value.Value, bool) {
				return value.Value{}, false
			})
			if err != nil {
				return nil, err
			}
			undo.Values = lost
			return []commands.Step{{Type: SetAttributeOptions, Payload: undo}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p attributeOptionsPayload) error {
			plan, err := planOptions(ctx, tx, p)
			if err != nil {
				return err
			}
			return plan.apply(ctx, tx)
		},
	})

	commands.Register(r, commands.Spec[retypeAttributePayload]{
		Type: RetypeAttribute, Capability: op, Scope: sc, Touches: values,
		// As set_options's, the undo holds the values that the change loses.
		UndoField: "type",
		Check: func(p retypeAttributePayload) input.FieldErrors {
			errs := append(entityID(ids.AttributeDef, "id", p.ID), checkOptions(p.Type, p.Options)...)
			if len(errs) > 0 {
				return errs
			}
			_, problem := p.Default.Read(p.Type, value.Checks{Length: true}, p.Options...)
			return append(append(errs, field("default", problem)...), checkValues(p.Values, p.Type, p.Options)...)
		},
		CheckNew: func(p retypeAttributePayload) input.FieldErrors {
			return field("options", value.CheckOptionNames(p.Options))
		},
		Inverse: func(ctx context.Context, tx *commands.Tx, p retypeAttributePayload) ([]commands.Step, error) {
			plan, err := planRetype(ctx, tx, p)
			if err != nil {
				return nil, err
			}
			a := plan.a
			var options []string
			if a.Type == value.Enum {
				options = a.EnumOptions
			}
			undo := retypeAttributePayload{ID: p.ID, Type: a.Type, Conversion: ConversionConvert, Options: options,
				Default: plan.oldDefault}
			// A value that the conversion back makes of the new one is the value
			// that the definition's components held, but for those that the
			// undo then gives back.
			lost, err := plan.lost(ctx, tx, undo, func(v value.Value) (value.Value, bool) {
				return value.Convert(v, p.Type, p.Options, a.Type, a.EnumOptions)
			})
			if err != nil {
				return nil, err
			}
			undo.Values = lost
			return []commands.Step{{Type: RetypeAttribute, Payload: undo}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p retypeAttributePayload) error {
			plan, err := planRetype(ctx, tx, p)
			if err != nil {
				return err
			}
			return plan.apply(ctx, tx)
		},
	})

	commands.Register(r, commands.Spec[moveAttributePayload]{
		Type: MoveAttribute, Capability: op, Scope: sc, Touches: atr,
		Check: func(p moveAttributePayload) input.FieldErrors { return entityID(ids.AttributeDef, "id", p.ID) },
		Inverse: func(ctx context.Context, tx *commands.Tx, p moveAttributePayload) ([]commands.Step, error) {
			a, err := liveAttribute(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: MoveAttribute, Payload: moveAttributePayload{ID: p.ID, Order: a.Order}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p moveAttributePayload) error {
			a, err := liveAttribute(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			q := queries.New(tx)
			order, err := attributeSortKey(ctx, q, a.WorkspaceID, a.ProjectID, a.ComponentTypeID, p.Order,
				&board.Child{ID: a.ID, Key: a.Order, Kind: ids.AttributeDef})
			if err != nil {
				return err
			}
			return changed(tx, ids.AttributeDef, a.ID)(q.MoveAttributeDef(ctx, queries.MoveAttributeDefParams{
				SortKey: order, UpdatedBy: by(tx), WorkspaceID: a.WorkspaceID, ProjectID: a.ProjectID, ID: a.ID}))
		},
	})

	commands.Register(r, commands.Spec[attributeIDPayload]{
		Type: DeleteAttribute, Capability: op, Scope: sc, Touches: values,
		Check: func(p attributeIDPayload) input.FieldErrors { return entityID(ids.AttributeDef, "id", p.ID) },
		// Deleting a definition that is in the trash already does nothing, and
		// its undo nothing either. One of a type in the trash, in the trash
		// with it but not deleted itself, is refused, as a variable of a board
		// in the trash is, because its undo, a restore, would be refused until
		// the type is restored.
		Inverse: func(ctx context.Context, tx *commands.Tx, p attributeIDPayload) ([]commands.Step, error) {
			row, err := anyAttribute(ctx, tx, p.ID)
			if err != nil || row.AttributeDef.DeletedAt.Valid || row.TypeDeleted {
				return []commands.Step{}, err
			}
			return []commands.Step{{Type: RestoreAttribute, Payload: attributeIDPayload{ID: p.ID}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p attributeIDPayload) error {
			row, err := anyAttribute(ctx, tx, p.ID)
			switch {
			case err != nil:
				return err
			case row.AttributeDef.DeletedAt.Valid:
				return nil
			case row.TypeDeleted:
				return refuseTypeInTrash()
			}
			a := row.AttributeDef
			if err := changed(tx, ids.AttributeDef, a.ID)(queries.New(tx).TrashAttributeDef(ctx,
				queries.TrashAttributeDefParams{UpdatedBy: by(tx), WorkspaceID: a.WorkspaceID, ProjectID: a.ProjectID,
					ID: a.ID})); err != nil {
				return err
			}
			return touchType(ctx, tx, a.ComponentTypeID)
		},
	})

	commands.Register(r, commands.Spec[attributeIDPayload]{
		Type: RestoreAttribute, Capability: op, Scope: sc, Touches: values,
		Check: func(p attributeIDPayload) input.FieldErrors { return entityID(ids.AttributeDef, "id", p.ID) },
		// Restoring a definition that is not in the trash does nothing, and its
		// undo nothing either. One of a type in the trash is refused, since it
		// comes back with its type.
		Inverse: func(ctx context.Context, tx *commands.Tx, p attributeIDPayload) ([]commands.Step, error) {
			row, err := anyAttribute(ctx, tx, p.ID)
			if err != nil || !row.AttributeDef.DeletedAt.Valid || row.TypeDeleted {
				return []commands.Step{}, err
			}
			return []commands.Step{{Type: DeleteAttribute, Payload: attributeIDPayload{ID: p.ID}}}, nil
		},
		Apply: applyRestoreAttribute,
	})
}

// applyCreateAttribute is CreateAttribute's Apply.
func applyCreateAttribute(ctx context.Context, tx *commands.Tx, p createAttributePayload) error {
	q := queries.New(tx)
	ws, project := nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID())
	id := decoded(ids.AttributeDef, p.ID)
	// A definition of the project with the ID is refused for its ID before
	// anything else is checked, as a variable's create is.
	if _, err := q.AttributeDefAny(ctx, queries.AttributeDefAnyParams{WorkspaceID: ws, ProjectID: project,
		ID: id}); err == nil {
		return commands.Refuse("id", "is the ID of an attribute definition that exists already", nil)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("component: read the attribute definition: %w", err)
	}
	t, err := liveType(ctx, tx, "component_type_id", p.ComponentTypeID)
	if err != nil {
		return err
	}
	if err := checkRoom(ctx, q, t, "component_type_id"); err != nil {
		return err
	}
	key := p.Key
	owner := attributeKeyOwner(q, ws, project, t.ID)
	if key == "" {
		if key, err = deriveKey(ctx, func(ctx context.Context, stem string) ([]string, error) {
			return q.AttributeKeysFrom(ctx, queries.AttributeKeysFromParams{WorkspaceID: ws, ProjectID: project,
				ComponentTypeID: t.ID, Stem: stem})
		}, p.Name, "attribute"); err != nil {
			return fmt.Errorf("component: derive the attribute definition's key: %w", err)
		}
	} else if err := checkKey(ctx, owner, "key", key, pgtype.UUID{}, "attribute definition of the type"); err != nil {
		return err
	}
	order, err := attributeSortKey(ctx, q, ws, project, t.ID, p.Order, nil)
	if err != nil {
		return err
	}
	// Check checked the default against the type.
	def, _ := p.Default.Of(p.Type, p.Options...)
	options := p.Options
	if options == nil {
		options = []string{}
	}
	err = q.InsertAttributeDef(ctx, queries.InsertAttributeDefParams{ID: id, WorkspaceID: ws, ProjectID: project,
		ComponentTypeID: t.ID, SortKey: order, Key: key, Name: p.Name, Type: p.Type, EnumOptions: options,
		DefaultValue: def.JSON(), IsRuntime: p.Runtime, CreatedBy: by(tx)})
	if database.UniqueViolation(err, "attribute_def_pkey") {
		return commands.Refuse("id", "is the ID of an attribute definition that exists already", nil)
	}
	if err != nil {
		return fmt.Errorf("component: add the attribute definition: %w", err)
	}
	if err := tx.Changed(ids.AttributeDef, id.Bytes, 1); err != nil {
		return err
	}
	// Each component of the type, those in the trash included, is given the
	// default (FR-COMP-01 AC1).
	if err := q.FillAttributeValues(ctx, queries.FillAttributeValuesParams{AttributeDefID: id, Value: def.JSON(),
		WorkspaceID: ws, ProjectID: project, ComponentTypeID: t.ID}); err != nil {
		return fmt.Errorf("component: give the components the attribute definition's default: %w", err)
	}
	return touchType(ctx, tx, t.ID)
}

// applyRestoreAttribute is RestoreAttribute's Apply.
func applyRestoreAttribute(ctx context.Context, tx *commands.Tx, p attributeIDPayload) error {
	row, err := anyAttribute(ctx, tx, p.ID)
	switch {
	case err != nil:
		return err
	case row.TypeDeleted:
		return commands.Refuse("id", "is an attribute definition of a component type in the project's trash, which "+
			"comes back only with its type: restore the type first", nil)
	case !row.AttributeDef.DeletedAt.Valid:
		return nil
	}
	a := row.AttributeDef
	q := queries.New(tx)
	if err := keyFree(ctx, attributeKeyOwner(q, a.WorkspaceID, a.ProjectID, a.ComponentTypeID), "id", a.Key, a.ID,
		"is an attribute definition whose key another definition of its type, which is not in the trash, has taken "+
			"since it was deleted: change that definition's key, or delete it, first"); err != nil {
		return err
	}
	t := queries.ComponentType{WorkspaceID: a.WorkspaceID, ProjectID: a.ProjectID, ID: a.ComponentTypeID}
	if err := checkRoom(ctx, q, t, "id"); err != nil {
		return err
	}
	def, err := storedDefault(a)
	if err != nil {
		return err
	}
	if err := changed(tx, ids.AttributeDef, a.ID)(q.RestoreAttributeDef(ctx, queries.RestoreAttributeDefParams{
		UpdatedBy: by(tx), WorkspaceID: a.WorkspaceID, ProjectID: a.ProjectID, ID: a.ID})); err != nil {
		return err
	}
	// A component created while the definition was in the trash holds no
	// value of it, and is given its default.
	if err := q.FillAttributeValues(ctx, queries.FillAttributeValuesParams{AttributeDefID: a.ID,
		Value: def.JSON(), WorkspaceID: a.WorkspaceID, ProjectID: a.ProjectID,
		ComponentTypeID: a.ComponentTypeID}); err != nil {
		return fmt.Errorf("component: give the components the attribute definition's default: %w", err)
	}
	return touchType(ctx, tx, a.ComponentTypeID)
}

// checkRoom refuses the batch, at the payload's field at, if the type t has
// MaxAttributes definitions that are not in the trash already.
func checkRoom(ctx context.Context, q *queries.Queries, t queries.ComponentType, at string) error {
	n, err := q.AttributeDefCount(ctx, queries.AttributeDefCountParams{WorkspaceID: t.WorkspaceID,
		ProjectID: t.ProjectID, ComponentTypeID: t.ID})
	switch {
	case err != nil:
		return fmt.Errorf("component: count the type's attribute definitions: %w", err)
	case n >= MaxAttributes:
		return commands.Refuse(at, fmt.Sprintf("is a component type that has %d attribute definitions that are not "+
			"in the project's trash already, the most that a type may have", MaxAttributes), nil)
	}
	return nil
}

// valuesPlan is what a change of an attribute definition's options or type
// makes of its definition and of the values that its components hold, which
// it reads a page at a time as it works them out (each), so that neither the
// change nor its inverse holds more of them than it keeps.
type valuesPlan struct {
	a queries.AttributeDef
	// oldDefault is the definition's default, and newDefault its default once
	// the change applies.
	oldDefault, newDefault value.Value
	// typ and options are the definition's type and an enum's options once
	// the change applies.
	typ     string
	options []string
	// given are the values that the command gives components, by their UUIDs,
	// each with its field, in their canonical forms.
	given map[ids.UUID]keyed
	// convert returns what the change makes of a value that a component
	// holds, in its canonical form, and whether it converts: one that does not
	// is given the new default.
	convert func(value.Value) (value.Value, bool)
}

// planOptions returns what the change of an enum's options p makes of it and
// of its values: a value that is none of the new options is given the
// default, or the one that p.Values gives the component, as is each that it
// names. It refuses the batch for a definition that is not an enum, for a
// default that is none of the options, and for a value in p.Values that is
// none of them.
func planOptions(ctx context.Context, tx *commands.Tx, p attributeOptionsPayload) (valuesPlan, error) {
	a, err := liveAttribute(ctx, tx, p.ID)
	if err != nil {
		return valuesPlan{}, err
	}
	if a.Type != value.Enum {
		return valuesPlan{}, commands.Refuse("id", fmt.Sprintf("is an attribute definition of the type %s, which "+
			"has no options: change its type with attribute.retype", a.Type), nil)
	}
	old, err := storedDefault(a)
	if err != nil {
		return valuesPlan{}, err
	}
	def := p.Default
	if def.IsZero() {
		def = old
	}
	def, problem := def.Of(value.Enum, p.Options...)
	switch {
	case problem != "" && p.Default.IsZero():
		return valuesPlan{}, commands.Refuse("default", "is missing, and the definition's default is none of the "+
			"new options", nil)
	case problem != "":
		return valuesPlan{}, commands.Refuse("default", problem, nil)
	}
	plan := valuesPlan{a: a, oldDefault: old, newDefault: def, typ: value.Enum, options: p.Options,
		convert: func(v value.Value) (value.Value, bool) {
			_, problem := v.Read(value.Enum, value.Checks{}, p.Options...)
			return v, problem == ""
		}}
	return plan, plan.give(p.Values)
}

// planRetype returns what the retype p makes of its definition and of its
// values, as its conversion says, or the values that p.Values gives. It
// refuses the batch for a retype to the definition's own type, of a runtime
// definition to a text, and for a value in p.Values that is not one of the
// new type.
func planRetype(ctx context.Context, tx *commands.Tx, p retypeAttributePayload) (valuesPlan, error) {
	a, err := liveAttribute(ctx, tx, p.ID)
	if err != nil {
		return valuesPlan{}, err
	}
	switch {
	case a.Type == p.Type && a.Type == value.Enum:
		return valuesPlan{}, commands.Refuse("type", "is the attribute definition's type already: change an enum's "+
			"options with attribute.set_options", nil)
	case a.Type == p.Type:
		return valuesPlan{}, commands.Refuse("type", "is the attribute definition's type already", nil)
	}
	if err := refuse(checkRuntime("type", p.Type, a.IsRuntime)); err != nil {
		return valuesPlan{}, err
	}
	old, err := storedDefault(a)
	if err != nil {
		return valuesPlan{}, err
	}
	def, problem := p.Default.Of(p.Type, p.Options...)
	if problem != "" {
		return valuesPlan{}, commands.Refuse("default", problem, nil)
	}
	plan := valuesPlan{a: a, oldDefault: old, newDefault: def, typ: p.Type, options: p.Options,
		convert: func(v value.Value) (value.Value, bool) {
			if p.Conversion == ConversionReset {
				return value.Value{}, false
			}
			return value.Convert(v, a.Type, a.EnumOptions, p.Type, p.Options)
		}}
	return plan, plan.give(p.Values)
}

// give checks given, the values that the command gives components, by their
// IDs, as values of the plan's new type, in the order of their fields, so
// that the first that is refused is the same each time, and keeps each in its
// canonical form. It refuses the batch for a component that given names
// twice, in two spellings of its ID, and for a value that is not one of the
// type.
func (plan *valuesPlan) give(given map[string]value.Value) error {
	byID, err := keyedByUUID(ids.Component, "values", given)
	if err != nil {
		return err
	}
	for _, k := range sortedKeyed(byID) {
		g := byID[k]
		v, problem := g.value.Of(plan.typ, plan.options...)
		if problem != "" {
			return commands.Refuse(g.at, problem, nil)
		}
		byID[k] = keyed{at: g.at, value: v}
	}
	plan.given = byID
	return nil
}

// each calls fn with each component, those in the trash included, whose value
// of the plan's definition the change changes, by its UUID, with the value
// that it holds and the one that it is given: the value that the command
// gives it, or what the plan's conversion makes of what it holds if that
// converts, or else the new default. It refuses the batch for a component
// that the command gives a value but that holds none of the definition, one
// of another type, and passes over one that the project does not have, as
// one that the purge has deleted since the change whose undo gives it back
// its value, so that the undo still applies.
func (plan *valuesPlan) each(ctx context.Context, tx *commands.Tx, fn func(u ids.UUID, old, to value.Value) error) error {
	found := make(map[ids.UUID]bool, len(plan.given))
	q := queries.New(tx)
	if err := eachValue(ctx, q, plan.a, func(component pgtype.UUID, old value.Value) error {
		u := ids.UUID(component.Bytes)
		var to value.Value
		ok := false
		if g, given := plan.given[u]; given {
			found[u] = true
			to, ok = g.value, true
		} else {
			to, ok = plan.convert(old)
		}
		if !ok {
			to = plan.newDefault
		}
		if to.Equal(old) {
			return nil
		}
		return fn(u, old, to)
	}); err != nil {
		return err
	}
	var missing []ids.UUID
	for _, k := range sortedKeyed(plan.given) {
		if !found[k] {
			missing = append(missing, k)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	rows, err := q.ComponentRevs(ctx, queries.ComponentRevsParams{WorkspaceID: plan.a.WorkspaceID,
		ProjectID: plan.a.ProjectID, Ids: uuids(missing)})
	if err != nil {
		return fmt.Errorf("component: read the components that hold no value of the attribute definition: %w", err)
	}
	exists := make(map[ids.UUID]bool, len(rows))
	for _, row := range rows {
		exists[row.ID.Bytes] = true
	}
	for _, k := range missing {
		if exists[k] {
			return commands.Refuse(plan.given[k].at, "is a component of another type than the attribute "+
				"definition's", nil)
		}
	}
	return nil
}

// lost returns the values that the plan's change loses, by their components'
// IDs, which its undo, undo, a payload whose values field, its last, holds
// none of them, gives back: those that what back makes of the new value is
// not, as none that back does not convert. It stops with
// commands.ErrUndoTooLarge, which the pipeline refuses the change for, at
// the field that it changes (commands.Spec.UndoField), as soon as undo with
// them would take more bytes, as JSON encodes it for the operation log, than
// the batch's undo has room for (commands.Tx.UndoRoom): undo as it encodes,
// its old default and options among it, and then the field that holds them,
// and each value, as JSON encodes it, with its component's ID, quoted, and a
// colon, and a comma between two, so that it never holds more of them than
// the pipeline would keep, whatever the definition's components hold.
func (plan *valuesPlan) lost(ctx context.Context, tx *commands.Tx, undo any,
	back func(value.Value) (value.Value, bool)) (map[string]value.Value, error) {
	base, err := json.Marshal(undo)
	if err != nil {
		return nil, fmt.Errorf("component: encode the undo: %w", err)
	}
	room, size := tx.UndoRoom(), len(base)
	if size > room {
		return nil, commands.ErrUndoTooLarge
	}
	out := map[string]value.Value{}
	err = plan.each(ctx, tx, func(u ids.UUID, old, to value.Value) error {
		if again, ok := back(to); ok && again.Equal(old) {
			return nil
		}
		// json.Marshal escapes what a value's JSON may hold unescaped, as <,
		// which then takes six bytes.
		enc, err := json.Marshal(old)
		if err != nil {
			return fmt.Errorf("component: encode the value: %w", err)
		}
		id := ids.Encode(ids.Component, u)
		if len(out) == 0 {
			size += len(`,"values":{}`)
		} else {
			size += len(`,`)
		}
		if size += len(`"":`) + len(id) + len(enc); size > room {
			return commands.ErrUndoTooLarge
		}
		out[id] = old
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// apply changes the plan's definition to its new type and options, with its
// new default, and its components' values as the plan says, and reports the
// changes. The components that it gives the new default, as a reset gives
// every one, are sent by their IDs, and the default once, so that what it
// sends grows with the values that it converts or that the command gives,
// not with the default's length times the components.
func (plan *valuesPlan) apply(ctx context.Context, tx *commands.Tx) error {
	vals := map[string]json.RawMessage{}
	var defaulted []pgtype.UUID
	if err := plan.each(ctx, tx, func(u ids.UUID, _, to value.Value) error {
		if to.Equal(plan.newDefault) {
			defaulted = append(defaulted, nullUUID(u))
		} else {
			vals[u.String()] = to.JSON()
		}
		return nil
	}); err != nil {
		return err
	}
	if err := setDefinition(ctx, tx, plan.a, plan.typ, plan.options, plan.newDefault); err != nil {
		return err
	}
	a := plan.a
	q := queries.New(tx)
	var set []pgtype.UUID
	if len(vals) > 0 {
		doc, err := json.Marshal(vals)
		if err != nil {
			return fmt.Errorf("component: encode the values: %w", err)
		}
		if set, err = q.SetAttributeValues(ctx, queries.SetAttributeValuesParams{Vals: doc,
			WorkspaceID: a.WorkspaceID, ProjectID: a.ProjectID, AttributeDefID: a.ID}); err != nil {
			return fmt.Errorf("component: change the values: %w", err)
		}
	}
	if len(defaulted) > 0 {
		reset, err := q.SetAttributeValuesTo(ctx, queries.SetAttributeValuesToParams{Value: plan.newDefault.JSON(),
			WorkspaceID: a.WorkspaceID, ProjectID: a.ProjectID, AttributeDefID: a.ID, Ids: defaulted})
		if err != nil {
			return fmt.Errorf("component: give the values the default: %w", err)
		}
		set = append(set, reset...)
	}
	return touch(ctx, tx, a.WorkspaceID, a.ProjectID, set)
}

// eachValue calls fn with each value that the components hold of the
// attribute definition a, those in the trash included, with its component's
// ID, in the order of the components' IDs, read a page at a time, in its
// canonical form (storedValue), until fn returns an error, which it returns.
// A command reads them under its project's lock, which every change of them
// takes, so that its pages agree with one another.
func eachValue(ctx context.Context, q *queries.Queries, a queries.AttributeDef,
	fn func(component pgtype.UUID, v value.Value) error) error {
	var cursor pgtype.UUID
	for {
		rows, err := q.AttributeValues(ctx, queries.AttributeValuesParams{WorkspaceID: a.WorkspaceID,
			ProjectID: a.ProjectID, AttributeDefID: a.ID, After: cursor, MaxRows: valuesPage})
		if err != nil {
			return fmt.Errorf("component: read the attribute definition's values: %w", err)
		}
		for _, row := range rows {
			v, err := storedValue(a, row.ComponentID, row.Value)
			if err != nil {
				return err
			}
			if err := fn(row.ComponentID, v); err != nil {
				return err
			}
		}
		if len(rows) < valuesPage {
			return nil
		}
		cursor = rows[len(rows)-1].ComponentID
	}
}

// storedValue returns raw, the value that the component componentID holds of
// the attribute definition a, in its canonical form, which jsonb may not
// write a number in, or an error for a value that is not one of the
// definition's type, which the commands keep from it.
func storedValue(a queries.AttributeDef, componentID pgtype.UUID, raw []byte) (value.Value, error) {
	v, problem := value.FromJSON(raw).Read(a.Type, value.Checks{}, a.EnumOptions...)
	if problem != "" {
		return value.Value{}, fmt.Errorf("component: the value of the attribute definition %s that the component %s "+
			"holds %s", ids.Encode(ids.AttributeDef, a.ID.Bytes), ids.Encode(ids.Component, componentID.Bytes), problem)
	}
	return v, nil
}

// setDefinition changes the definition a's type to typ, its options to
// options, and its default to def, a value of typ that value.Value.Of
// returned, and reports the change.
func setDefinition(ctx context.Context, tx *commands.Tx, a queries.AttributeDef, typ string, options []string,
	def value.Value) error {
	if typ != value.Enum || options == nil {
		options = []string{}
	}
	return changed(tx, ids.AttributeDef, a.ID)(queries.New(tx).UpdateAttributeDefType(ctx,
		queries.UpdateAttributeDefTypeParams{Type: typ, EnumOptions: options, DefaultValue: def.JSON(),
			UpdatedBy: by(tx), WorkspaceID: a.WorkspaceID, ProjectID: a.ProjectID, ID: a.ID}))
}

// touchType increments the revisions of the components of the type typeID,
// those in the trash included, whose values a change of one of its
// definitions changes, and reports each change.
func touchType(ctx context.Context, tx *commands.Tx, typeID pgtype.UUID) error {
	rows, err := queries.New(tx).TouchTypeComponents(ctx, queries.TouchTypeComponentsParams{UpdatedBy: by(tx),
		WorkspaceID: nullUUID(tx.WorkspaceID()), ProjectID: nullUUID(tx.ProjectID()), ComponentTypeID: typeID})
	if err != nil {
		return fmt.Errorf("component: change the type's components: %w", err)
	}
	for _, row := range rows {
		if err := tx.Changed(ids.Component, row.ID.Bytes, row.Rev); err != nil {
			return err
		}
	}
	return nil
}

// touch increments the revisions of the components components of the
// project, whose values changed, and reports each change.
func touch(ctx context.Context, tx *commands.Tx, ws, project pgtype.UUID, components []pgtype.UUID) error {
	if len(components) == 0 {
		return nil
	}
	rows, err := queries.New(tx).TouchComponents(ctx, queries.TouchComponentsParams{UpdatedBy: by(tx),
		WorkspaceID: ws, ProjectID: project, Ids: components})
	if err != nil {
		return fmt.Errorf("component: change the components: %w", err)
	}
	for _, row := range rows {
		if err := tx.Changed(ids.Component, row.ID.Bytes, row.Rev); err != nil {
			return err
		}
	}
	return nil
}

// attributeSortKey returns the key that a definition of the type typeID is
// placed at among the type's: the key given, if it is not "", or else as
// board.SortKey places a child among a folder's, after the type's last
// definition. moving is the definition that moves, or nil for a new one.
func attributeSortKey(ctx context.Context, q *queries.Queries, ws, project, typeID pgtype.UUID, given string,
	moving *board.Child) (string, error) {
	if given != "" {
		return given, nil
	}
	params := queries.LastAttributeSortKeysParams{WorkspaceID: ws, ProjectID: project, ComponentTypeID: typeID}
	if moving != nil {
		params.Moved = moving.ID
	}
	last, err := q.LastAttributeSortKeys(ctx, params)
	if err != nil {
		return "", fmt.Errorf("component: read the type's last keys: %w", err)
	}
	return board.Place(board.Last{Key: last.Last, Live: last.LastLive, LiveID: last.LastLiveID}, moving,
		"the type's last attribute definition")
}

// attributeKeyOwner returns the owner of a key among the definitions of the
// type typeID.
func attributeKeyOwner(q *queries.Queries, ws, project, typeID pgtype.UUID) keyOwner {
	return func(ctx context.Context, key string) (pgtype.UUID, error) {
		return q.AttributeDefWithKey(ctx, queries.AttributeDefWithKeyParams{WorkspaceID: ws, ProjectID: project,
			ComponentTypeID: typeID, Key: key})
	}
}

// anyAttribute returns the attribute definition of the batch's project whose
// ID is id, a definition's ID that its payload's Check checked, whether or
// not it is in the trash, with whether its type is, or refuses the batch if
// the project has no such definition.
func anyAttribute(ctx context.Context, tx *commands.Tx, id string) (queries.AttributeDefAnyRow, error) {
	row, err := queries.New(tx).AttributeDefAny(ctx, queries.AttributeDefAnyParams{
		WorkspaceID: nullUUID(tx.WorkspaceID()), ProjectID: nullUUID(tx.ProjectID()),
		ID: decoded(ids.AttributeDef, id)})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return queries.AttributeDefAnyRow{}, commands.Refuse("id", "is not an attribute definition of the project",
			nil)
	case err != nil:
		return queries.AttributeDefAnyRow{}, fmt.Errorf("component: read the attribute definition: %w", err)
	}
	return row, nil
}

// liveAttribute returns the attribute definition of the batch's project whose
// ID is id as anyAttribute does, or refuses the batch if it is in the trash,
// where nothing but restoring it changes it, or its type is, with which it
// comes back.
func liveAttribute(ctx context.Context, tx *commands.Tx, id string) (queries.AttributeDef, error) {
	row, err := anyAttribute(ctx, tx, id)
	switch {
	case err != nil:
		return queries.AttributeDef{}, err
	case row.AttributeDef.DeletedAt.Valid:
		return queries.AttributeDef{}, commands.Refuse("id", "is an attribute definition in the project's trash, "+
			"which only restoring it changes", nil)
	case row.TypeDeleted:
		return queries.AttributeDef{}, refuseTypeInTrash()
	}
	return row.AttributeDef, nil
}

// refuseTypeInTrash returns the refusal of a command that would change an
// attribute definition of a type in the trash, which is in the trash with it,
// and which nothing but the type's restoration changes.
func refuseTypeInTrash() error {
	return commands.Refuse("id", "is an attribute definition of a component type in the project's trash, which "+
		"comes back with its type", nil)
}

// checkOptions returns the problems of options as those of a definition of
// the type typ, but for the text rules of their names
// (value.CheckOptionNames): an enum's options, and none of another type.
func checkOptions(typ string, options []string) input.FieldErrors {
	if typ != value.Enum {
		if len(options) > 0 {
			return field("options", "is set for a type other than enum, which has none")
		}
		return nil
	}
	return field("options", value.CheckOptions(options))
}

// checkRuntime returns the problem of a definition of the type typ whose
// runtime is runtime, at the payload's field at, the field that the command
// sets: at runtime for a create's and SetAttributeRuntime's, and at type for
// a retype's of a runtime definition. A text is no part of the game's state.
func checkRuntime(at, typ string, runtime bool) input.FieldErrors {
	switch {
	case typ != value.Text || !runtime:
		return nil
	case at == "type":
		return field(at, "is text, which a runtime attribute definition may not be: unset its runtime first")
	}
	return field(at, "is set on a text, which is no part of the game's state")
}

// checkValues returns the problems of values, the values that a command
// gives components, by their IDs, as values of the type typ, whose options
// are options, but for their characters, which Apply checks.
func checkValues(values map[string]value.Value, typ string, options []string) input.FieldErrors {
	var errs input.FieldErrors
	for _, k := range slices.Sorted(maps.Keys(values)) {
		at := "values." + k
		if e := entityID(ids.Component, at, k); e != nil {
			errs = append(errs, e...)
			continue
		}
		_, problem := values[k].Read(typ, value.Checks{Length: true}, options...)
		errs = append(errs, field(at, problem)...)
	}
	return errs
}

// sortedKeyed returns the UUIDs of m in the order of their values' fields.
func sortedKeyed(m map[ids.UUID]keyed) []ids.UUID {
	keys := make([]ids.UUID, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b ids.UUID) int { return strings.Compare(m[a].at, m[b].at) })
	return keys
}
