package published

import (
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemagen"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// Settings is a snapshot's project/settings.json: the project's entry and the
// signals that its scripts emit (§6.3).
type Settings struct {
	Entry *Entry `json:"entry,omitempty" doc:"Where the flow starts, as the manifest's entry. Without it, the project has none."`
	// SchemaVersion is SchemaVersion.
	SchemaVersion Int      `json:"schemaVersion" const:"1" doc:"The version of the published format, 1."`
	Signals       []string `json:"signals" minLength:"1" doc:"The names of the signals that the project's scripts emit with signal(), each once, sorted by their code points, [] for none (FR-EXPR-04)."`
}

// MarshalJSON writes the settings, its signals [] when there are none, or
// fails on what Decode would refuse of them (marshalFile).
func (s Settings) MarshalJSON() ([]byte, error) {
	type plain Settings
	return marshalFile[Settings](SettingsPath, plain(s))
}

// JSONSchemaDoc describes the settings (schemagen.Documented).
func (Settings) JSONSchemaDoc() string {
	return "A snapshot's project/settings.json: the project's entry and the signals that its scripts emit " +
		"(PRD §6.3)."
}

// Variables is a snapshot's project/variables.json: every variable, global
// and of each board (§6.3, §6.5).
type Variables struct {
	// SchemaVersion is SchemaVersion.
	SchemaVersion Int        `json:"schemaVersion" const:"1" doc:"The version of the published format, 1."`
	Variables     []Variable `json:"variables" doc:"Every variable, sorted by ID."`
}

// MarshalJSON writes the variables' file, its variables [] when there are
// none, or fails on what Decode would refuse of it, as a variable whose
// default is not set, or is not of its type (marshalFile).
func (v Variables) MarshalJSON() ([]byte, error) {
	type plain Variables
	return marshalFile[Variables](VariablesPath, plain(v))
}

// JSONSchemaDoc describes the variables' file (schemagen.Documented).
func (Variables) JSONSchemaDoc() string {
	return "A snapshot's project/variables.json: every variable, global and of each board (PRD §6.3, §6.5)."
}

// The scopes of a variable (FR-VAR-02).
const (
	ScopeGlobal = "global"
	ScopeBoard  = "board"
)

// Variable is a variable's declaration (FR-VAR-01, FR-VAR-02).
type Variable struct {
	BoardID      string      `json:"boardId,omitempty" id:"board" when:"scope=board" required:"true" doc:"The board that the variable is of."`
	Default      value.Value `json:"default" typeFrom:"type" doc:"The variable's default, of its type."`
	Description  string      `json:"description,omitempty" minLength:"1" doc:"What the variable is for. Without it, the variable has no description."`
	ID           string      `json:"id" id:"variable"`
	Key          string      `json:"key" pattern:"^[A-Za-z_][A-Za-z0-9_]*$" maxLength:"64" doc:"The key by which expressions and game code name the variable, unique among its scope's."`
	ResetOnEnter bool        `json:"resetOnEnter,omitempty" when:"scope=board" doc:"Whether the variable is reset to its default whenever the flow enters its board (PRD §5.8.5). Without it, false."`
	Scope        string      `json:"scope" enum:"global,board" doc:"Whether the variable is global or of a board, which it is visible in alone (FR-VAR-02)."`
	Type         string      `json:"type" enum:"bool,int,float,string" doc:"The variable's type, by which a consumer reads its default and its values, never by their JSON form (PRD §5.8.9)."`
}

// JSONSchemaDoc describes a variable (schemagen.Documented).
func (Variable) JSONSchemaDoc() string {
	return "A variable's declaration (FR-VAR-01, FR-VAR-02)."
}

// ComponentTypes is a snapshot's project/component_types.json: every
// component type, with its attribute definitions (§6.3).
type ComponentTypes struct {
	ComponentTypes []ComponentType `json:"componentTypes" doc:"Every component type that a component of the snapshot is of, sorted by ID."`
	// SchemaVersion is SchemaVersion.
	SchemaVersion Int `json:"schemaVersion" const:"1" doc:"The version of the published format, 1."`
}

// MarshalJSON writes the component types' file, its component types [] when
// there are none, or fails on what Decode would refuse of it, as an attribute
// definition whose default is not set (marshalFile).
func (c ComponentTypes) MarshalJSON() ([]byte, error) {
	type plain ComponentTypes
	return marshalFile[ComponentTypes](ComponentTypesPath, plain(c))
}

// JSONSchemaDoc describes the component types' file (schemagen.Documented).
func (ComponentTypes) JSONSchemaDoc() string {
	return "A snapshot's project/component_types.json: the component types, with their attribute definitions " +
		"(PRD §6.3)."
}

// ComponentType is a component type, with its attribute definitions
// (FR-COMP-01).
type ComponentType struct {
	Attributes []AttributeDefinition `json:"attributes" doc:"The type's attribute definitions, in their author's order, [] for none."`
	ID         string                `json:"id" id:"component_type"`
	Key        string                `json:"key" pattern:"^[A-Za-z_][A-Za-z0-9_]*$" maxLength:"64"`
	Name       string                `json:"name"`
}

// MarshalJSON writes the component type, its attribute definitions [] when
// it has none.
func (c ComponentType) MarshalJSON() ([]byte, error) {
	type plain ComponentType
	return marshalObject(plain(c))
}

// JSONSchemaDoc describes a component type (schemagen.Documented).
func (ComponentType) JSONSchemaDoc() string {
	return "A component type, with its attribute definitions (FR-COMP-01)."
}

// AttributeDefinition is an attribute definition of a component type
// (FR-COMP-01).
type AttributeDefinition struct {
	Default value.Value `json:"default" typeFrom:"type" doc:"The attribute's default, of its type: what a component that a game creates has, as play mode's does. Each component of the snapshot has a value of its own."`
	ID      string      `json:"id" id:"attribute_def"`
	Key     string      `json:"key" pattern:"^[A-Za-z_][A-Za-z0-9_]*$" maxLength:"64" doc:"The key by which expressions name the attribute, after its component's, as elara.trust, unique among its type's."`
	Name    string      `json:"name"`
	Options []string    `json:"options,omitempty" when:"type=enum" required:"true" minItems:"1" maxItems:"100" minLength:"1" maxLength:"100" doc:"An enum's options, in their author's order, one of which each of its values is."`
	Runtime bool        `json:"runtime" doc:"Whether the attribute is part of the game's state, which expressions read and write and a save state holds (FR-COMP-03). A text attribute never is."`
	Type    string      `json:"type" enum:"bool,int,float,string,text,enum" doc:"The attribute's type, by which a consumer reads its values, never by their JSON form (PRD §5.8.9)."`
}

// JSONSchemaDoc describes an attribute definition (schemagen.Documented).
func (AttributeDefinition) JSONSchemaDoc() string {
	return "An attribute definition of a component type (FR-COMP-01)."
}

// Components is a snapshot's project/components.json: every component, with
// its values (§6.3).
type Components struct {
	Components []Component `json:"components" doc:"Every component, sorted by ID."`
	// SchemaVersion is SchemaVersion.
	SchemaVersion Int `json:"schemaVersion" const:"1" doc:"The version of the published format, 1."`
}

// MarshalJSON writes the components' file, its components [] when there are
// none, or fails on what Decode would refuse of it, as a value of an
// attribute that is not set (marshalFile).
func (c Components) MarshalJSON() ([]byte, error) {
	type plain Components
	return marshalFile[Components](ComponentsPath, plain(c))
}

// JSONSchemaDoc describes the components' file (schemagen.Documented).
func (Components) JSONSchemaDoc() string {
	return "A snapshot's project/components.json: the components, with their values (PRD §6.3)."
}

// Component is a component, with a value of each attribute of its type,
// runtime or not (FR-COMP-02). Its description, which is for its authors,
// is in the authoring export alone.
type Component struct {
	Attributes      AttributeValues `json:"attributes" doc:"A value of each of the attribute definitions of the component's type, by the definition's ID, of its type."`
	ComponentTypeID string          `json:"componentTypeId" id:"component_type"`
	ID              string          `json:"id" id:"component"`
	Image           string          `json:"image,omitempty" id:"asset" doc:"The component's image. Its file is the snapshot's assets/<the ID's body>.<its extension> when the snapshot includes assets. Without it, the component has none."`
	Key             string          `json:"key" pattern:"^[A-Za-z_][A-Za-z0-9_]*$" maxLength:"64" doc:"The key by which expressions name the component, unique among the project's."`
	Name            string          `json:"name"`
}

// MarshalJSON writes the component, its attributes {} when it has none.
func (c Component) MarshalJSON() ([]byte, error) {
	type plain Component
	return marshalObject(plain(c))
}

// JSONSchemaDoc describes a component (schemagen.Documented).
func (Component) JSONSchemaDoc() string {
	return "A component, with a value of each attribute of its type, runtime or not (FR-COMP-02)."
}

// AttributeValues are values of attributes, by their attribute definitions'
// IDs, which type them.
type AttributeValues map[string]value.Value

// MarshalJSON writes the values, {} when there are none.
func (a AttributeValues) MarshalJSON() ([]byte, error) {
	if a == nil {
		return []byte("{}"), nil
	}
	return schemagen.Marshal(map[string]value.Value(a))
}

// JSONSchema gives the schemas the definition of attributes' values
// (schemagen.Provider).
func (AttributeValues) JSONSchema(g *schemagen.Generator) *schemagen.Schema {
	return schemagen.Describe(schemagen.Map(g.Ref(valueType), g.ID(ids.AttributeDef)),
		"Values of attributes, by their attribute definitions' IDs, each of its definition's type.")
}
