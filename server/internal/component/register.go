package component

import (
	"context"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"golang.org/x/text/language"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/i18n"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// Register registers in r the component type, the attribute definition, and
// the component as entities of the pipeline, whose revisions a command
// expects as it does any entity's, and their commands, each of which changes
// the project's draft, so that an API token needs project:write for it
// (FR-AUTH-04), and its role "Manage variables, components, component types"
// (§5.6). The components' commands refer to their folders, so r has the
// folder registered already (board.Register).
func Register(r *commands.Registry) {
	commands.RegisterEntity(r, commands.Entity{
		Kind: ids.ComponentType,
		Revs: func(ctx context.Context, tx *commands.Tx, entities []ids.UUID) (map[ids.UUID]int64, error) {
			rows, err := queries.New(tx).ComponentTypeRevs(ctx, queries.ComponentTypeRevsParams{
				WorkspaceID: nullUUID(tx.WorkspaceID()), ProjectID: nullUUID(tx.ProjectID()), Ids: uuids(entities)})
			if err != nil {
				return nil, err
			}
			out := make(map[ids.UUID]int64, len(rows))
			for _, row := range rows {
				out[row.ID.Bytes] = row.Rev
			}
			return out, nil
		},
		States: func(ctx context.Context, tx *commands.Tx, entities []ids.UUID) (map[ids.UUID]any, error) {
			q := queries.New(tx)
			rows, err := q.ComponentTypesByID(ctx, queries.ComponentTypesByIDParams{
				WorkspaceID: nullUUID(tx.WorkspaceID()), ProjectID: nullUUID(tx.ProjectID()), Ids: uuids(entities)})
			if err != nil {
				return nil, err
			}
			types := make([]queries.ComponentType, len(rows))
			for i, row := range rows {
				types[i] = row.ComponentType
			}
			states, err := typesOf(ctx, q, types)
			if err != nil {
				return nil, err
			}
			out := make(map[ids.UUID]any, len(states))
			for i, t := range states {
				out[types[i].ID.Bytes] = t
			}
			return out, nil
		},
	})
	commands.RegisterEntity(r, commands.Entity{
		Kind: ids.AttributeDef,
		Revs: func(ctx context.Context, tx *commands.Tx, entities []ids.UUID) (map[ids.UUID]int64, error) {
			rows, err := queries.New(tx).AttributeDefRevs(ctx, queries.AttributeDefRevsParams{
				WorkspaceID: nullUUID(tx.WorkspaceID()), ProjectID: nullUUID(tx.ProjectID()), Ids: uuids(entities)})
			if err != nil {
				return nil, err
			}
			out := make(map[ids.UUID]int64, len(rows))
			for _, row := range rows {
				out[row.ID.Bytes] = row.Rev
			}
			return out, nil
		},
		States: func(ctx context.Context, tx *commands.Tx, entities []ids.UUID) (map[ids.UUID]any, error) {
			rows, err := queries.New(tx).AttributeDefsByID(ctx, queries.AttributeDefsByIDParams{
				WorkspaceID: nullUUID(tx.WorkspaceID()), ProjectID: nullUUID(tx.ProjectID()), Ids: uuids(entities)})
			if err != nil {
				return nil, err
			}
			out := make(map[ids.UUID]any, len(rows))
			for _, row := range rows {
				a, err := attributeOf(row.AttributeDef)
				if err != nil {
					return nil, err
				}
				// A definition of a type in the trash is in the trash with it,
				// which its state says, as the state of an entity in the trash
				// does, though only its type's deletion put it there.
				if a.DeletedAt == nil && row.TypeDeletedAt.Valid {
					a.DeletedAt = timeOf(row.TypeDeletedAt)
				}
				out[row.AttributeDef.ID.Bytes] = a
			}
			return out, nil
		},
	})
	commands.RegisterEntity(r, commands.Entity{
		Kind: ids.Component,
		Revs: func(ctx context.Context, tx *commands.Tx, entities []ids.UUID) (map[ids.UUID]int64, error) {
			rows, err := queries.New(tx).ComponentRevs(ctx, queries.ComponentRevsParams{
				WorkspaceID: nullUUID(tx.WorkspaceID()), ProjectID: nullUUID(tx.ProjectID()), Ids: uuids(entities)})
			if err != nil {
				return nil, err
			}
			out := make(map[ids.UUID]int64, len(rows))
			for _, row := range rows {
				out[row.ID.Bytes] = row.Rev
			}
			return out, nil
		},
		States: func(ctx context.Context, tx *commands.Tx, entities []ids.UUID) (map[ids.UUID]any, error) {
			q := queries.New(tx)
			rows, err := q.ComponentsByID(ctx, queries.ComponentsByIDParams{WorkspaceID: nullUUID(tx.WorkspaceID()),
				ProjectID: nullUUID(tx.ProjectID()), Ids: uuids(entities)})
			if err != nil {
				return nil, err
			}
			cs := make([]queries.Component, len(rows))
			for i, row := range rows {
				cs[i] = row.Component
			}
			states, err := componentsOf(ctx, q, cs)
			if err != nil {
				return nil, err
			}
			out := make(map[ids.UUID]any, len(states))
			for i, c := range states {
				out[cs[i].ID.Bytes] = c
			}
			return out, nil
		},
	})
	registerTypes(r)
	registerAttributes(r)
	registerComponents(r)
}

// defaultType is a type that a project is created with.
type defaultType struct {
	key  string
	name i18n.Key
	icon string
}

// defaultTypes are the types that a project is created with (FR-COMP-01), in
// the order that they are listed in.
var defaultTypes = []defaultType{
	{"character", i18n.K("component_type.character"), "person"},
	{"item", i18n.K("component_type.item"), "item"},
	{"location", i18n.K("component_type.location"), "location"},
	{"faction", i18n.K("component_type.faction"), "group"},
	{"custom", i18n.K("component_type.custom"), "tag"},
}

// DefaultTypeKeys returns the keys of the types that a project is created
// with, in the order that they are listed in.
func DefaultTypeKeys() []string {
	keys := make([]string, len(defaultTypes))
	for i, t := range defaultTypes {
		keys[i] = t.key
	}
	return keys
}

// Seed adds the default types, Character, Item, Location, Faction, and Custom
// (FR-COMP-01), without attribute definitions, to the project projectID of
// the workspace workspaceID, which the user userID creates, in tx, the
// transaction that creates it, named in locale, the project's default
// locale, a BCP 47 tag, or in the source locale if the server has no catalog
// for it, as project.Options.Seed. Their IDs are sorted, so that the types
// are listed in that order.
func Seed(ctx context.Context, tx pgx.Tx, workspaceID, projectID, userID ids.UUID, locale string) error {
	tag, err := language.Parse(locale)
	if err != nil {
		tag = i18n.Source
	}
	l := i18n.Default().Localizer(tag)
	typeIDs := make([]ids.UUID, len(defaultTypes))
	for i := range typeIDs {
		typeIDs[i] = ids.New()
	}
	slices.SortFunc(typeIDs, func(a, b ids.UUID) int { return slices.Compare(a[:], b[:]) })
	q := queries.New(tx)
	for i, t := range defaultTypes {
		if err := q.InsertComponentType(ctx, queries.InsertComponentTypeParams{ID: nullUUID(typeIDs[i]),
			WorkspaceID: nullUUID(workspaceID), ProjectID: nullUUID(projectID), Key: t.key,
			Name: l.Format(i18n.Message{Key: t.name}), Icon: t.icon, CreatedBy: nullUUID(userID)}); err != nil {
			return fmt.Errorf("component: add the default type %s: %w", t.key, err)
		}
	}
	return nil
}
