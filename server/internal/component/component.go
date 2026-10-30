// Package component keeps a project's component types, their attribute
// definitions, and its components (PRD FR-COMP-01, FR-COMP-02, plan note
// N42): the characters, items, locations, and factions of its narrative.
// Register registers the type, the attribute definition, and the component as
// entities of the pipeline's registry (package commands), whose revisions a
// client expects as it does any entity's (§5.7, ADR-006), with their
// commands, the one way that they change; Service reads them.
//
// A component type, as Character, has a key, an identifier of the expression
// language (package ident) unique among the project's types that are not in
// the trash, a name, an icon, and ordered attribute definitions, each with a
// key unique among its type's definitions that are not in the trash, a name,
// a type (package value), an enum's options, a default, and whether it is
// part of the game's state (runtime). A project is created with the default
// types, Character, Item, Location, Faction, and Custom (Seed).
//
// A component, as Elara, has a type, which is fixed once it is created, a key
// unique among the project's components that are not in the trash, derived
// from its name unless a command gives one, a name, a rich-text description
// (package richtext), an image, an asset of the project, and the value of
// each of its type's attribute definitions, a value of the definition's type.
// Adding a definition gives each component of its type the definition's
// default, as one command (FR-COMP-01 AC1), and a change of the default
// leaves the values as they are; a retype converts them, as the command's
// conversion says (AC2). A value has no revision of its own: a change of one
// is a change of its component, whose revision it increments. A component
// sits in a folder of components, or at the top, ordered among the folder's
// children as a variable is among a folder of variables' (package board).
//
// A deleted component goes to the project's trash, which lists it, and from
// which restoring it brings it back where it was, with the folders above it
// and its type, if they were deleted since. A deleted type or attribute
// definition goes to the trash too, unlisted, as a deleted folder does: a
// type is deleted only once each of its components is in the trash, and its
// definitions are in the trash with it; a definition keeps the values that
// its components hold, which restoring it brings back. The purge deletes
// each once its 30 days have passed, a type once it holds no component
// either. The reads of what is not in the trash read the live views,
// live_component_type, live_attribute_def, and live_component.
//
// Every command needs project:write of an API token (FR-AUTH-04), and the
// actor's role on the project "Manage variables, components, component
// types" (§5.6): owner, admin, or editor. An archived project takes none of
// them (FR-PROJ-01 AC2). Reading them is reading the project's content.
package component

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/expression"
	"github.com/CyberCute-Studio/storycreep/server/internal/ident"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// MaxNameLength is the most characters that the name of a type, of an
// attribute definition, or of a component may have, as a board's may.
const MaxNameLength = 100

// MaxAttributes is the most attribute definitions that a type may have that
// are not in the trash.
const MaxAttributes = 100

// MaxQueryLength is the most characters that a search of the components may
// have.
const MaxQueryLength = 100

// IconNone is the icon of a type that shows none.
const IconNone = "none"

// icons are the icons of a type, which the web app maps to glyphs, in the
// order of the database's check and of the schemas' enums.
var icons = []string{IconNone, "person", "group", "item", "location", "event", "book", "star", "heart", "key",
	"gem", "shield", "flag", "tag"}

// Icons returns the icons of a type, as a copy that the caller may change.
func Icons() []string {
	return slices.Clone(icons)
}

// The conversions of an attribute definition's values that its retype takes
// (FR-COMP-01 AC2).
const (
	// ConversionConvert converts each value to the new type (value.Convert),
	// and gives the new default to a value that does not convert.
	ConversionConvert = "convert"
	// ConversionReset gives every value the new default.
	ConversionReset = "reset"
)

var (
	// ErrNotFound is returned for a component that the project does not have,
	// or that is in its trash.
	ErrNotFound = errors.New("component: no such component")
	// ErrTypeNotFound is returned for a component type that the project does
	// not have, or that is in its trash.
	ErrTypeNotFound = errors.New("component: no such component type")
	// ErrAttributeNotFound is returned for an attribute definition that the
	// type does not have, or that is in its trash.
	ErrAttributeNotFound = errors.New("component: no such attribute definition")
	// ErrKeyTaken is the reason of a command's refusal of a key that another
	// type of the project, another attribute definition of the type, or
	// another component of the project has, of those that are not in the
	// trash (commands.RefusedError), which the API answers with key_taken: the
	// reason of every entity's key that another has (ident.ErrTaken).
	ErrKeyTaken = ident.ErrTaken
	// ErrTypeInUse is the reason of the refusal of the deletion of a type of
	// which a component that is not in the trash is, which the API answers
	// with component_type_in_use.
	ErrTypeInUse = errors.New("component: a component that is not in the trash is of the type")
)

// ComponentType is a component type as the API shows it, with its attribute
// definitions that are not in the trash, and as an answer of
// revision_conflict shows it (commands.Entity.States).
type ComponentType struct {
	ID         string      `json:"id" doc:"The component type's ID"`
	ProjectID  string      `json:"project_id" doc:"The ID of the type's project"`
	Key        string      `json:"key" doc:"The type's key: an identifier of the expression language (PRD §5.8.1), unique among the project's types that are not in its trash"`
	Name       string      `json:"name" doc:"The type's name, as Character"`
	Icon       string      `json:"icon" enum:"none,person,group,item,location,event,book,star,heart,key,gem,shield,flag,tag" doc:"The type's icon, a name that the web app shows a glyph for, or none"`
	Attributes []Attribute `json:"attributes" nullable:"false" doc:"The type's attribute definitions that are not in the project's trash, in their order: by their order, compared by its bytes, then by their IDs without the prefix, atr_, compared by their bytes too (package orderkey)"`
	Rev        int64       `json:"rev" doc:"The type's revision, which every change of it increments, and which a change expects with expected_rev. A change of one of its attribute definitions changes the definition's revision, not the type's."`
	// DeletedAt is set while the type is in the project's trash, which only
	// an answer of revision_conflict shows.
	DeletedAt *time.Time `json:"deleted_at,omitempty" doc:"When the type went to the project's trash, if it is there"`
	CreatedAt time.Time  `json:"created_at" doc:"When the type was created"`
	UpdatedAt time.Time  `json:"updated_at" doc:"When the type last changed"`
}

// Attribute is an attribute definition as the API shows it, and as an answer
// of revision_conflict shows it.
type Attribute struct {
	ID              string `json:"id" doc:"The attribute definition's ID, by which saved games record its values (PRD FR-COMP-03 AC3)"`
	ComponentTypeID string `json:"component_type_id" doc:"The ID of the definition's component type"`
	Order           string `json:"order" doc:"The definition's place among its type's: they are ordered by their order, compared by its bytes, then by their IDs without the prefix, atr_, compared by their bytes too (package orderkey)"`
	Key             string `json:"key" doc:"The definition's key, by which expressions name its values, as trust in elara.trust: an identifier of the expression language (PRD §5.8.1), unique among its type's definitions that are not in the project's trash"`
	Name            string `json:"name" doc:"The definition's name, as Trust"`
	Type            string `json:"type" enum:"bool,int,float,string,text,enum" doc:"The definition's type: bool, int, float, string, of at most 4096 characters, text, a string of several lines of at most 100000 characters, or enum, one of its options"`
	// Options are an enum's, and none of another type.
	Options []string    `json:"options,omitempty" doc:"An enum's options, which its values are one of, in their order; another type has none"`
	Default value.Value `json:"default" doc:"The definition's default, a value of its type, which a component of its type that is created without a value of it holds, and which each of the type's components is given when the definition is added: true or false for bool, an integer for int, a number for float, which is written without a decimal point when it is integral, as 2, a string for string and for text, and one of its options for enum. A client reads a number by the definition's type, never by how it is written (PRD §5.8.9)."`
	Runtime bool        `json:"runtime" doc:"Whether the definition's values are part of the game's state, which expressions read and write as component_key.attribute_key, and which saved games record (PRD FR-COMP-03). A text never is."`
	Rev     int64       `json:"rev" doc:"The definition's revision, which every change of it increments, and which a change expects with expected_rev"`
	// DeletedAt is set while the definition is in the project's trash: when
	// it was deleted, or, for one in the trash with its type alone, when the
	// type was, which only an answer of revision_conflict shows.
	DeletedAt *time.Time `json:"deleted_at,omitempty" doc:"When the definition went to the project's trash, if it is there: when it was deleted, or, if it was not, but its type is in the trash, with which it is in the trash too, when the type was deleted"`
	CreatedAt time.Time  `json:"created_at" doc:"When the definition was created"`
	UpdatedAt time.Time  `json:"updated_at" doc:"When the definition last changed"`
}

// Summary is a component as the API lists it: without its description and
// its values, which a component's own read shows (Component).
type Summary struct {
	ID              string `json:"id" doc:"The component's ID"`
	ProjectID       string `json:"project_id" doc:"The ID of the component's project"`
	ComponentTypeID string `json:"component_type_id" doc:"The ID of the component's type, which is fixed once it is created"`
	FolderID        string `json:"folder_id,omitempty" doc:"The ID of the folder of components that the component is in, if it is in one, or none for a component at the top"`
	Order           string `json:"order" doc:"The component's place among the folders and components of its folder, or of the top: they are ordered by their order, compared by its bytes, then by their IDs without the prefix, cmp_ or fld_, compared by their bytes too (package orderkey)"`
	Key             string `json:"key" doc:"The component's key, by which expressions name it, as elara in elara.trust: an identifier of the expression language (PRD §5.8.1), unique among the project's components that are not in its trash. A change of the component's name leaves it as it is (PRD FR-COMP-02 AC2)."`
	Name            string `json:"name" doc:"The component's name, as Elara"`
	ImageAssetID    string `json:"image_asset_id,omitempty" doc:"The ID of the component's image, an asset of the project, or none"`
	Rev             int64  `json:"rev" doc:"The component's revision, which every change of it increments, a change of one of its values or of the attribute definitions whose values it holds among them, and which a change expects with expected_rev"`
	// DeletedAt is set while the component is in the project's trash, which
	// only an answer of revision_conflict shows.
	DeletedAt *time.Time `json:"deleted_at,omitempty" doc:"When the component went to the project's trash, if it is there"`
	CreatedAt time.Time  `json:"created_at" doc:"When the component was created"`
	UpdatedAt time.Time  `json:"updated_at" doc:"When the component last changed"`
}

// Component is a component as the API shows it, with its description and its
// values, and as an answer of revision_conflict shows it.
type Component struct {
	Summary
	Description richtext.Doc `json:"description" doc:"The component's description, a rich-text document (PRD FR-TEXT-01)"`
	// Attributes are the values that the component holds of its type's
	// definitions that are not in the trash.
	Attributes map[string]value.Value `json:"attributes" nullable:"false" doc:"The values that the component holds of its type's attribute definitions that are not in the project's trash, by the definitions' IDs: each a value of its definition's type, as the definition's default is written"`
}

// Unconvertible is a component whose value of an attribute definition does
// not convert to another type, as a retype with the conversion convert
// would give the new default to (FR-COMP-01 AC2).
type Unconvertible struct {
	ComponentID string `json:"component_id" doc:"The component's ID"`
	Key         string `json:"key" doc:"The component's key"`
	Name        string `json:"name" doc:"The component's name"`
	// DeletedAt is set for a component in the trash, whose values a retype
	// converts too.
	DeletedAt *time.Time `json:"deleted_at,omitempty" doc:"When the component went to the project's trash, if it is there: a retype converts the values of the components in the trash too"`
}

// typeOf returns the type t as the API shows it, with its attribute
// definitions attrs.
func typeOf(t queries.ComponentType, attrs []Attribute) ComponentType {
	if attrs == nil {
		attrs = []Attribute{}
	}
	return ComponentType{ID: ids.Encode(ids.ComponentType, t.ID.Bytes),
		ProjectID: ids.Encode(ids.Project, t.ProjectID.Bytes), Key: t.Key, Name: t.Name, Icon: t.Icon,
		Attributes: attrs, Rev: t.Rev, DeletedAt: timeOf(t.DeletedAt), CreatedAt: t.CreatedAt.Time,
		UpdatedAt: t.UpdatedAt.Time}
}

// attributeOf returns the attribute definition a as the API shows it, or an
// error if its default is not a value of its type, which the database's check
// keeps from it.
func attributeOf(a queries.AttributeDef) (Attribute, error) {
	def, err := storedDefault(a)
	if err != nil {
		return Attribute{}, err
	}
	var options []string
	if a.Type == value.Enum {
		options = a.EnumOptions
	}
	return Attribute{ID: ids.Encode(ids.AttributeDef, a.ID.Bytes),
		ComponentTypeID: ids.Encode(ids.ComponentType, a.ComponentTypeID.Bytes), Order: a.Order, Key: a.Key,
		Name: a.Name, Type: a.Type, Options: options, Default: def, Runtime: a.IsRuntime, Rev: a.Rev,
		DeletedAt: timeOf(a.DeletedAt), CreatedAt: a.CreatedAt.Time, UpdatedAt: a.UpdatedAt.Time}, nil
}

// storedDefault returns the default of the attribute definition a, as the
// database holds it, in its canonical form, without checking its characters
// or its number's length again, as a variable's stored default is read, or an
// error if it is not a value of its type.
func storedDefault(a queries.AttributeDef) (value.Value, error) {
	def, problem := value.FromJSON(a.DefaultValue).Read(a.Type, value.Checks{}, a.EnumOptions...)
	if problem != "" {
		return value.Value{}, fmt.Errorf("component: the default of the attribute definition %s %s",
			ids.Encode(ids.AttributeDef, a.ID.Bytes), problem)
	}
	return def, nil
}

// summaryOf returns the component c as the API lists it.
func summaryOf(c queries.Component) Summary {
	return Summary{ID: ids.Encode(ids.Component, c.ID.Bytes), ProjectID: ids.Encode(ids.Project, c.ProjectID.Bytes),
		ComponentTypeID: ids.Encode(ids.ComponentType, c.ComponentTypeID.Bytes), FolderID: encode(ids.Folder,
			c.FolderID), Order: c.Order, Key: c.Key, Name: c.Name, ImageAssetID: encode(ids.Asset, c.ImageAssetID),
		Rev: c.Rev, DeletedAt: timeOf(c.DeletedAt), CreatedAt: c.CreatedAt.Time, UpdatedAt: c.UpdatedAt.Time}
}

// componentsOf returns the components rows as the API shows them, with their
// values, which it reads in q's transaction, in the order of rows.
func componentsOf(ctx context.Context, q *queries.Queries, rows []queries.Component) ([]Component, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	ws, project := rows[0].WorkspaceID, rows[0].ProjectID
	byID := make(map[ids.UUID]*Component, len(rows))
	out := make([]Component, len(rows))
	idList := make([]pgtype.UUID, len(rows))
	for i, c := range rows {
		doc, err := richtext.Parse(c.Description)
		if err != nil {
			return nil, fmt.Errorf("component: the description of the component %s: %w", ids.Encode(ids.Component,
				c.ID.Bytes), err)
		}
		out[i] = Component{Summary: summaryOf(c), Description: *doc, Attributes: map[string]value.Value{}}
		byID[c.ID.Bytes] = &out[i]
		idList[i] = c.ID
	}
	values, err := q.ComponentValues(ctx, queries.ComponentValuesParams{WorkspaceID: ws, ProjectID: project,
		Ids: idList})
	if err != nil {
		return nil, fmt.Errorf("component: read the components' values: %w", err)
	}
	for _, v := range values {
		val, problem := value.FromJSON(v.Value).Read(v.Type, value.Checks{}, v.EnumOptions...)
		if problem != "" {
			return nil, fmt.Errorf("component: the value of the attribute definition %s that the component %s "+
				"holds %s", ids.Encode(ids.AttributeDef, v.AttributeDefID.Bytes), ids.Encode(ids.Component,
				v.ComponentID.Bytes), problem)
		}
		if c := byID[v.ComponentID.Bytes]; c != nil {
			c.Attributes[ids.Encode(ids.AttributeDef, v.AttributeDefID.Bytes)] = val
		}
	}
	return out, nil
}

// typesOf returns the types rows as the API shows them, with their attribute
// definitions that are not in the trash, which it reads in q's transaction,
// in the order of rows.
func typesOf(ctx context.Context, q *queries.Queries, rows []queries.ComponentType) ([]ComponentType, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	idList := make([]pgtype.UUID, len(rows))
	for i, t := range rows {
		idList[i] = t.ID
	}
	defs, err := q.TypeAttributeDefs(ctx, queries.TypeAttributeDefsParams{WorkspaceID: rows[0].WorkspaceID,
		ProjectID: rows[0].ProjectID, TypeIds: idList})
	if err != nil {
		return nil, fmt.Errorf("component: read the types' attribute definitions: %w", err)
	}
	attrs := map[ids.UUID][]Attribute{}
	for _, d := range defs {
		a, err := attributeOf(queries.AttributeDef(d.LiveAttributeDef))
		if err != nil {
			return nil, err
		}
		attrs[d.LiveAttributeDef.ComponentTypeID.Bytes] = append(attrs[d.LiveAttributeDef.ComponentTypeID.Bytes], a)
	}
	out := make([]ComponentType, len(rows))
	for i, t := range rows {
		out[i] = typeOf(t, attrs[t.ID.Bytes])
	}
	return out, nil
}

// encode returns the ID of the entity of the kind k whose UUID is the column
// id, or "" if it is null.
func encode(k ids.Kind, id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return ids.Encode(k, id.Bytes)
}

// timeOf returns the time of the column t, or nil if it is null.
func timeOf(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}

// DB runs a transaction of the application login, as *database.DB does, and
// one that reads as of one moment (InSnapshot).
type DB interface {
	InTx(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error
	InSnapshot(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error
}

// Options configure a Service.
type Options struct {
	// Projects reads the projects, which authorises each read of their
	// components. It is required.
	Projects *project.Service
}

// Service reads the component types and the components of the projects.
type Service struct {
	db       DB
	projects *project.Service
}

// New returns a Service that reads the components in db, with opts. It
// panics if opts.Projects is nil.
func New(db DB, opts Options) *Service {
	if opts.Projects == nil {
		panic("component: a Service needs the projects' service that authorises its reads")
	}
	return &Service{db: db, projects: opts.Projects}
}

// read runs fn in a transaction of the workspace of the project projectID,
// once it has checked that the actor may read the project's content
// (project.Service.ReadContent): it returns the errors of
// project.Service.Get, as project.ErrNotFound for a project that does not
// exist, is in the trash, on which the actor's role is none, or that the
// actor's API token does not cover. The token's scopes are the route's to
// check.
func (s *Service) read(ctx context.Context, actor commands.Actor, projectID ids.UUID,
	fn func(q *queries.Queries, workspaceID pgtype.UUID) error) error {
	return s.projects.ReadContent(ctx, s.db, actor, projectID, func(tx pgx.Tx, workspaceID ids.UUID) error {
		return fn(queries.New(tx), nullUUID(workspaceID))
	})
}

// Types returns at most limit of the component types of the project
// projectID that are not in its trash, with their attribute definitions, in
// the order of their IDs: the first, if after is zero, or else those after
// the type after. It returns the errors of project.Service.Get.
func (s *Service) Types(ctx context.Context, actor commands.Actor, projectID, after ids.UUID, limit int) (
	[]ComponentType, error) {
	var out []ComponentType
	err := s.read(ctx, actor, projectID, func(q *queries.Queries, workspaceID pgtype.UUID) error {
		rows, err := q.ComponentTypes(ctx, queries.ComponentTypesParams{WorkspaceID: workspaceID,
			ProjectID: nullUUID(projectID), After: nullUUID(after), MaxRows: int32(min(limit, 1<<20))})
		if err != nil {
			return fmt.Errorf("component: list the types: %w", err)
		}
		types := make([]queries.ComponentType, len(rows))
		for i, row := range rows {
			types[i] = queries.ComponentType(row.LiveComponentType)
		}
		if out, err = typesOf(ctx, q, types); out == nil && err == nil {
			out = []ComponentType{}
		}
		return err
	})
	return out, err
}

// Type returns the component type typeID of the project projectID, with its
// attribute definitions, or ErrTypeNotFound if the project has no such type,
// or it is in the trash. It returns the errors of project.Service.Get first.
func (s *Service) Type(ctx context.Context, actor commands.Actor, projectID, typeID ids.UUID) (ComponentType,
	error) {
	var out ComponentType
	err := s.read(ctx, actor, projectID, func(q *queries.Queries, workspaceID pgtype.UUID) error {
		row, err := q.ComponentType(ctx, queries.ComponentTypeParams{WorkspaceID: workspaceID,
			ProjectID: nullUUID(projectID), ID: nullUUID(typeID)})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrTypeNotFound
		case err != nil:
			return fmt.Errorf("component: read the type: %w", err)
		}
		types, err := typesOf(ctx, q, []queries.ComponentType{queries.ComponentType(row.LiveComponentType)})
		if err != nil {
			return err
		}
		out = types[0]
		return nil
	})
	return out, err
}

// Filter narrows a list of components.
type Filter struct {
	// TypeID is the type of the components listed, or zero for every type.
	TypeID ids.UUID
	// Query is what the components' names or keys hold, compared without
	// regard to case, or "" for any. It has at most MaxQueryLength
	// characters.
	Query string
}

// CheckQuery says what is wrong with q as a search of the components, or
// returns "": at most MaxQueryLength characters, without control characters.
func CheckQuery(q string) string {
	switch {
	case utf8.RuneCountInString(q) > MaxQueryLength:
		return fmt.Sprintf("is longer than %d characters", MaxQueryLength)
	case !utf8.ValidString(q) || strings.IndexFunc(q, input.IsControl) >= 0:
		return "contains a control character"
	}
	return ""
}

// pattern returns the pattern of ILIKE that matches a text that holds q, with
// its wildcards and its escape character escaped.
func pattern(q string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%"
}

// Components returns at most limit of the components of the project
// projectID that are not in its trash and that f takes, without their
// descriptions and values, in the order of their IDs: the first, if after is
// zero, or else those after the component after (FR-COMP-02 AC3). It
// returns the errors of project.Service.Get first, so that what a query holds
// is checked only for an actor who may read the project, as Unconvertible's
// target is, and then input.FieldErrors at query for a query that CheckQuery
// refuses. The route's schema refuses a query longer than MaxQueryLength
// before, as it refuses any parameter that breaks it, whoever sends it, which
// tells nothing of the project.
func (s *Service) Components(ctx context.Context, actor commands.Actor, projectID ids.UUID, f Filter,
	after ids.UUID, limit int) ([]Summary, error) {
	var out []Summary
	err := s.read(ctx, actor, projectID, func(q *queries.Queries, workspaceID pgtype.UUID) error {
		if problem := CheckQuery(f.Query); problem != "" {
			return input.FieldErrors{{Field: "query", Message: problem}}
		}
		params := queries.ComponentsParams{WorkspaceID: workspaceID, ProjectID: nullUUID(projectID),
			ComponentTypeID: nullUUID(f.TypeID), After: nullUUID(after), MaxRows: int32(min(limit, 1<<20))}
		if f.Query != "" {
			params.Pattern = pgtype.Text{String: pattern(f.Query), Valid: true}
		}
		rows, err := q.Components(ctx, params)
		if err != nil {
			return fmt.Errorf("component: list the components: %w", err)
		}
		out = make([]Summary, len(rows))
		for i, row := range rows {
			out[i] = summaryOf(queries.Component(row.LiveComponent))
		}
		return nil
	})
	return out, err
}

// Component returns the component componentID of the project projectID, with
// its description and its values, or ErrNotFound if the project has no such
// component, or it is in the trash. It returns the errors of
// project.Service.Get first.
func (s *Service) Component(ctx context.Context, actor commands.Actor, projectID, componentID ids.UUID) (
	Component, error) {
	var out Component
	err := s.read(ctx, actor, projectID, func(q *queries.Queries, workspaceID pgtype.UUID) error {
		row, err := q.Component(ctx, queries.ComponentParams{WorkspaceID: workspaceID, ProjectID: nullUUID(projectID),
			ID: nullUUID(componentID)})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrNotFound
		case err != nil:
			return fmt.Errorf("component: read the component: %w", err)
		}
		cs, err := componentsOf(ctx, q, []queries.Component{queries.Component(row.LiveComponent)})
		if err != nil {
			return err
		}
		out = cs[0]
		return nil
	})
	return out, err
}

// Target is the type that a retype would give an attribute definition: its
// name, and an enum's options.
type Target struct {
	Type    string
	Options []string
}

// Unconvertible returns at most limit of the components of the project
// projectID whose values of the attribute definition attributeID, of the
// type typeID, do not convert to the target type to (value.Convert), those
// in the trash included, which a retype converts too, in the order of their
// IDs: the first, if after is zero, or else those after the component after,
// so that a client can show what a retype with the conversion convert would
// give the new default to before it applies one (FR-COMP-01 AC2), as the
// retype reads and converts them. It reads the definition and its values, a
// page at a time, as of one moment, that of the read's start
// (database.DB.InSnapshot), which locks nothing, so that what a change that
// commits meanwhile does, to the definition or to a value, is not read. It
// returns the errors of project.Service.Get first, then input.FieldErrors at
// type for a target of no type, and at options for an enum's options that
// are not valid, or options of another type, ErrTypeNotFound or
// ErrAttributeNotFound if the project has no such type, or the type no such
// definition, that is not in the trash, and input.FieldErrors at type for a
// target that the retype refuses: the definition's own type, or a text for a
// runtime definition. The route's schema refuses a target of no type, or of
// one that is none, and options longer than an enum's can be, before, as it
// refuses any parameter that breaks it, whoever sends it.
func (s *Service) Unconvertible(ctx context.Context, actor commands.Actor, projectID, typeID, attributeID ids.UUID,
	to Target, after ids.UUID, limit int) ([]Unconvertible, error) {
	var out []Unconvertible
	err := s.projects.ReadSnapshot(ctx, s.db, actor, projectID, func(tx pgx.Tx, workspaceID ids.UUID) error {
		if errs := checkTarget(to); len(errs) > 0 {
			return errs
		}
		var err error
		out, err = s.unconvertible(ctx, queries.New(tx), nullUUID(workspaceID), nullUUID(projectID), typeID,
			nullUUID(attributeID), to, nullUUID(after), limit)
		return err
	})
	return out, err
}

// unconvertible is Unconvertible's read, in q's transaction, of the
// definition attributeID of the type typeID and of its values.
func (s *Service) unconvertible(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, typeID ids.UUID,
	attributeID pgtype.UUID, to Target, after pgtype.UUID, limit int) ([]Unconvertible, error) {
	row, err := q.AttributeDefAny(ctx, queries.AttributeDefAnyParams{WorkspaceID: ws, ProjectID: project,
		ID: attributeID})
	switch {
	case errors.Is(err, pgx.ErrNoRows) || err == nil && row.AttributeDef.ComponentTypeID.Bytes != typeID:
		return nil, ErrAttributeNotFound
	case err != nil:
		return nil, fmt.Errorf("component: read the attribute definition: %w", err)
	case row.TypeDeleted:
		return nil, ErrTypeNotFound
	case row.AttributeDef.DeletedAt.Valid:
		return nil, ErrAttributeNotFound
	case row.AttributeDef.Type == to.Type:
		return nil, input.FieldErrors{{Field: "type", Message: "is the attribute definition's type already"}}
	}
	a := row.AttributeDef
	if errs := checkRuntime("type", to.Type, a.IsRuntime); len(errs) > 0 {
		return nil, errs
	}
	out := []Unconvertible{}
	// The values are read as a retype reads them (storedValue), a page at a
	// time, of which only those that do not convert are kept, until limit of
	// them are found.
	for cursor := after; len(out) < limit; {
		rows, err := q.AttributeValueComponents(ctx, queries.AttributeValueComponentsParams{WorkspaceID: ws,
			ProjectID: project, AttributeDefID: a.ID, After: cursor, MaxRows: valuesPage})
		if err != nil {
			return nil, fmt.Errorf("component: read the attribute definition's values: %w", err)
		}
		for _, c := range rows {
			v, err := storedValue(a, c.ComponentID, c.Value)
			if err != nil {
				return nil, err
			}
			if _, ok := value.Convert(v, a.Type, a.EnumOptions, to.Type, to.Options); !ok {
				out = append(out, Unconvertible{ComponentID: ids.Encode(ids.Component, c.ComponentID.Bytes),
					Key: c.Key, Name: c.Name, DeletedAt: timeOf(c.DeletedAt)})
				if len(out) == limit {
					break
				}
			}
		}
		if len(rows) < valuesPage {
			break
		}
		cursor = rows[len(rows)-1].ComponentID
	}
	return out, nil
}

// valuesPage is how many values of an attribute definition a query reads at
// a time.
const valuesPage = 1000

// checkTarget returns the problems of to as the target of a retype: a type,
// with an enum's options, which are valid as a retype's are (checkOptions,
// value.CheckOptionNames), and none for another type.
func checkTarget(to Target) input.FieldErrors {
	if !slices.Contains(value.Types(), to.Type) {
		return field("type", "is not a type of an attribute definition")
	}
	if errs := checkOptions(to.Type, to.Options); len(errs) > 0 {
		return errs
	}
	return field("options", value.CheckOptionNames(to.Options))
}

// RenamePreview returns at most limit of the expressions of the project
// projectID, in its trash or not, that a change of the key of its component
// componentID to the key would rewrite (FR-COMP-02 AC2, FR-VAR-03,
// expression.Preview): those that it would rewrite a name of the component's
// attributes of, or, if qualified is set, those that it would qualify a name
// of another entity of, which a component's key takes from none, in the
// order of their keys (expression.PreviewKey), after after, or from the first
// if after is zero. It reads the component and the expressions as of one
// moment, that of the read's start (project.Service.ReadSnapshot). It returns
// the errors of project.Service.Get first, ErrNotFound if the project has no
// such component outside its trash, an *ident.Error for a key that is no
// identifier, and an error that wraps ErrKeyTaken for a key that another
// component of the project has, as the change refuses them.
func (s *Service) RenamePreview(ctx context.Context, actor commands.Actor, projectID, componentID ids.UUID,
	key string, qualified bool, after expression.PreviewKey, limit int) ([]expression.RenameSite, error) {
	var out []expression.RenameSite
	err := s.projects.ReadSnapshot(ctx, s.db, actor, projectID, func(tx pgx.Tx, workspaceID ids.UUID) error {
		q, ws, project := queries.New(tx), nullUUID(workspaceID), nullUUID(projectID)
		row, err := q.Component(ctx, queries.ComponentParams{WorkspaceID: ws, ProjectID: project,
			ID: nullUUID(componentID)})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrNotFound
		case err != nil:
			return fmt.Errorf("component: read the component: %w", err)
		}
		c := queries.Component(row.LiveComponent)
		if err := ident.Check(key); err != nil {
			return err
		}
		owner, err := componentKeyOwner(q, ws, project)(ctx, key)
		switch {
		case err == nil && owner != c.ID:
			return fmt.Errorf("component: %w", ErrKeyTaken)
		case err != nil && !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("component: read the component with the key: %w", err)
		}
		out, err = expression.Preview(ctx, q, ws, project, rekey(c, key), qualified, after, limit)
		return err
	})
	return out, err
}

// nullUUID returns id as a uuid column, null if id is zero.
func nullUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != (ids.UUID{})}
}
