// Package clipboard copies a selection of a board's nodes as a clipboard, a
// documented, versioned JSON structure (PRD FR-EDIT-07, plan note N58), and
// pastes a clipboard onto a board of the same project, or of another of the
// workspace's, as one command, clipboard.paste. What it pastes, the nodes,
// their outputs, and the connections between them, gets new IDs, which the
// client makes, as it makes those of what it creates. What the nodes refer to
// outside the selection, the variables and components that their texts and
// expressions name, their speakers, their jumpers' targets, and their covers,
// is resolved in the project that they are pasted into: the same entity where
// the board that they are pasted onto reaches it, and else the entity of its
// key, of the same scope, so that a board's variable resolves to a variable of
// that board alone. Resolve reports what resolves to nothing, with what a
// client needs to create it, in the batch of the paste, before it, which the
// paste then resolves to.
//
// A clipboard declares each variable, component, and attribute definition
// that its content names, as it was where it was copied, which a paste
// resolves by its ID and its key, and Resolve reports as it is declared. A
// text keeps a reference that resolves to nothing as it is, which the
// validation reports (PRD FR-TEXT-02 AC2); an expression names what its names
// resolve to by the shortest name that names it at the board, and what
// resolves to nothing explicitly, as global.<key>, board.<key>, or
// cmp.<component>.<attribute>, so that it names nothing of another scope or
// kind meanwhile (expr.Relocate); a speaker that resolves to nothing, or that
// the clipboard does not declare, as one in the trash where it was copied,
// and a jumper's target outside the clipboard are kept where the project has
// them, in its trash or not, as node.create takes them, and are left out
// where it lacks them, and so is a cover that is not its asset.
package clipboard

import (
	"bytes"
	"encoding/json"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/node"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// Format is the clipboard's format, which a clipboard names, so that a client
// tells it from other JSON on the system's clipboard.
const Format = "storycreep.clipboard"

// Version is the version of the clipboard's format that the server writes,
// and the newest that it reads. A change of what a clipboard holds, as a new
// field of a node, increments it, as the published format's schemaVersion is
// incremented (PRD §6.4), and schema/clipboard gains the version's schema.
const Version = 1

// The bounds of a clipboard: the most nodes, outputs of a branch, connections,
// variables, components, and attribute definitions of a component that it
// holds, which its schema says too (TestSchemaBounds), and the most bytes of
// its JSON, as encoding/json writes it, with those of the new IDs that a
// paste of it gives (pasteBytes), that Copy answers with, 4 MiB, so that the
// report's request, and a paste's command, with its new IDs, fit in a
// request's body of at most 5 MiB (api.DefaultMaxBodyBytes), with a mebibyte
// for the rest of its batch, as a batch's undo does
// (commands.DefaultMaxUndoBytes): the commands that create what the report
// lists, which a client that they do not fit with it, in bytes or in the
// commands that a batch holds, sends in batches before the paste's, which it
// resolves to all the same. Copy refuses a selection whose clipboard would
// pass one of them, which a paste could not take.
const (
	MaxNodes       = 1000
	MaxOutputs     = 1000
	MaxConnections = 10_000
	MaxVariables   = 10_000
	MaxComponents  = 10_000
	MaxAttributes  = 1000
	MaxBytes       = 4 << 20
)

// Clipboard is a selection of the nodes of a board, as copied (PRD
// FR-EDIT-07 AC3): the nodes, with their outputs, the connections between
// them, and what their content names outside them.
type Clipboard struct {
	Format      string                `json:"format" enum:"storycreep.clipboard" doc:"What the JSON is, a Storycreep clipboard: always storycreep.clipboard"`
	Version     int                   `json:"version" minimum:"1" doc:"The version of the clipboard's format, which a change of what a clipboard holds increments. A server reads the versions up to the one that it writes, 1, and answers a newer one with 422 validation_failed."`
	ProjectID   string                `json:"project_id" doc:"The ID of the project that the nodes were copied from"`
	BoardID     string                `json:"board_id" doc:"The ID of the board that the nodes were copied from, whose variables are those of the board scope that the clipboard declares, and at which its expressions' names were read"`
	Nodes       []ClipboardNode       `json:"nodes" minItems:"1" maxItems:"1000" nullable:"false" doc:"The nodes, at most 1000, each once, in the order of their IDs"`
	Connections []ClipboardConnection `json:"connections" maxItems:"10000" nullable:"false" doc:"The connections between the nodes, those that leave one of them and enter one of them, at most 10000, in the order of the ports that they leave, by their nodes and then their outputs, out first, and of each port's choices (PRD FR-EDIT-03 AC1), which a paste keeps"`
	Variables   []ClipboardVariable   `json:"variables" maxItems:"10000" nullable:"false" doc:"The variables that the nodes' and the connections' texts and expressions name, each once, as they were where they were copied, by which a paste resolves them"`
	Components  []ClipboardComponent  `json:"components" maxItems:"10000" nullable:"false" doc:"The components that the nodes' and the connections' texts and expressions name, and the elements' speakers, each once, as they were where they were copied, by which a paste resolves them"`
}

// ClipboardNode is a node of a clipboard, with every field of it but its board,
// and a branch's outputs, as node.create creates one, but for its outputs.
type ClipboardNode struct {
	ID                 string            `json:"id" doc:"The node's ID where it was copied, by which the clipboard's connections, jumpers' targets, and node references name it, and the paste's ids give it a new one"`
	Type               string            `json:"type" enum:"element,branch,jumper" doc:"The node's type: an element, a branch, or a jumper (PRD §5.8.5)"`
	Kind               string            `json:"kind,omitempty" enum:"dialogue,narration" doc:"An element's kind. Another type has none."`
	Title              string            `json:"title,omitempty" maxLength:"200" doc:"The node's title, if it has one, as node.create takes it"`
	Text               Text              `json:"text,omitempty" doc:"An element's text, a rich-text document (PRD FR-TEXT-01), whose value references and mentions name variables and components that the clipboard declares, or else name what a paste keeps as it is. Another type has none."`
	SpeakerComponentID string            `json:"speaker_component_id,omitempty" doc:"The ID of an element's speaker, if it has one: a component that the clipboard declares, or one that it does not, as one in the trash of the project that it was copied from, which a paste keeps where the project that it pastes into has it, in its trash or not. Another type has none."`
	CoverAssetID       string            `json:"cover_asset_id,omitempty" doc:"The ID of an element's cover image, an asset of the project that it was copied from, which a paste into another project leaves out, if it has one. Another type has none."`
	Color              string            `json:"color,omitempty" enum:"none,red,orange,yellow,green,teal,blue,purple,pink,gray" doc:"The node's colour. Without it, none."`
	Tags               []string          `json:"tags,omitempty" maxItems:"32" doc:"The node's tags, as node.create takes them"`
	Position           node.NodePosition `json:"position" doc:"Where the node was on its board's canvas, which a paste moves by its offset, so that the pasted nodes keep their places relative to each other (PRD FR-EDIT-07 AC2)"`
	Size               *node.NodeSize    `json:"size,omitempty" doc:"The node's width and height, if it has a size of its own"`
	IsEnding           bool              `json:"is_ending,omitempty" doc:"Whether the flow ends at the element. Another type may not."`
	Target             *node.NodeTarget  `json:"target,omitempty" doc:"A jumper's target, if it has one: a node of the clipboard, or another of the project that it was copied from. Another type has none."`
	OnEnter            string            `json:"on_enter,omitempty" maxLength:"16384" doc:"An element's or a branch's on-enter script's source, if it has one. A jumper has none."`
	OnExit             string            `json:"on_exit,omitempty" maxLength:"16384" doc:"An element's or a branch's on-exit script's source, if it has one. A jumper has none."`
	Outputs            []ClipboardOutput `json:"outputs,omitempty" maxItems:"1000" doc:"A branch's outputs, at most 1000, in their order: its conditional outputs, the first its if, and its else after them, if it has one (PRD FR-EDIT-04). Another type has none."`
}

// ClipboardOutput is an output of a branch of a clipboard.
type ClipboardOutput struct {
	ID        string `json:"id" doc:"The output's ID where it was copied, by which the connections that leave it name it as their from_port, and the paste's ids give it a new one"`
	Else      bool   `json:"else,omitempty" doc:"Whether the output is the branch's else, which comes after its conditional outputs, and has no condition"`
	Condition string `json:"condition,omitempty" maxLength:"16384" doc:"A conditional output's condition's source. An else has none."`
}

// ClipboardConnection is a connection of a clipboard, between two of its nodes.
type ClipboardConnection struct {
	ID         string `json:"id" doc:"The connection's ID where it was copied, which the paste's ids give a new one"`
	FromNodeID string `json:"from_node_id" doc:"The ID of the node of the clipboard that the connection leaves, an element or a branch"`
	FromPort   string `json:"from_port,omitempty" doc:"The port that the connection leaves: out, an element's, or the ID of an output of the branch that it leaves, which a branch's connection has. Without it, out."`
	ToNodeID   string `json:"to_node_id" doc:"The ID of the node of the clipboard that the connection enters"`
	Label      Text   `json:"label,omitempty" doc:"The connection's label, a rich-text document (PRD FR-TEXT-01), as a node's text is. Without it, an empty paragraph."`
	Condition  string `json:"condition,omitempty" maxLength:"16384" doc:"The connection's condition's source, if it has one"`
	OnTraverse string `json:"on_traverse,omitempty" maxLength:"16384" doc:"The connection's on-traverse script's source, if it has one"`
}

// ClipboardVariable is a variable that a clipboard's content names, as it was
// where it was copied: what a paste resolves it by, and what creating one in
// its place takes (variable.create).
type ClipboardVariable struct {
	ID           string      `json:"id" doc:"The variable's ID where it was copied"`
	Key          string      `json:"key" doc:"The variable's key, an identifier of the expression language (PRD §5.8.1)"`
	Scope        string      `json:"scope" enum:"global,board" doc:"Whether the variable is global or of a board (PRD FR-VAR-02)"`
	BoardID      string      `json:"board_id,omitempty" doc:"The ID of the board whose variable it is, a board's: the clipboard's own, whose expressions read it, or another, whose variable a text names. A global variable has none."`
	Type         string      `json:"type" enum:"bool,int,float,string" doc:"The variable's type"`
	Default      value.Value `json:"default,omitzero" doc:"The variable's default, a value of its type, as variable.create takes it. Without it, the type's zero value."`
	Description  string      `json:"description,omitempty" maxLength:"2000" doc:"The variable's description, if it has one"`
	ResetOnEnter bool        `json:"reset_on_enter,omitempty" doc:"Whether a board's variable is reset to its default each time the flow enters its board"`
}

// ClipboardComponent is a component that a clipboard's content names, as it was
// where it was copied, with the attribute definitions of its type that the
// content names.
type ClipboardComponent struct {
	ID         string                 `json:"id" doc:"The component's ID where it was copied"`
	Key        string                 `json:"key" doc:"The component's key, an identifier of the expression language (PRD §5.8.1)"`
	Name       string                 `json:"name" maxLength:"100" doc:"The component's name"`
	Type       ClipboardComponentType `json:"type" doc:"The component's type"`
	Attributes []ClipboardAttribute   `json:"attributes" maxItems:"1000" nullable:"false" doc:"The attribute definitions of the component's type that the content names of the component, each once"`
}

// ClipboardComponentType is the type of a component of a clipboard.
type ClipboardComponentType struct {
	ID   string `json:"id" doc:"The component type's ID where it was copied"`
	Key  string `json:"key" doc:"The component type's key, by which a paste finds the type of the key, in which a component in the component's place may be created"`
	Name string `json:"name" maxLength:"100" doc:"The component type's name"`
}

// ClipboardAttribute is an attribute definition of a component of a
// clipboard: what a paste resolves it by, and what creating one in its place
// takes (attribute.create).
type ClipboardAttribute struct {
	ID      string      `json:"id" doc:"The attribute definition's ID where it was copied"`
	Key     string      `json:"key" doc:"The attribute definition's key, an identifier of the expression language (PRD §5.8.1)"`
	Name    string      `json:"name" maxLength:"100" doc:"The attribute definition's name"`
	Type    string      `json:"type" enum:"bool,int,float,string,text,enum" doc:"The attribute's type (PRD FR-COMP-01)"`
	Runtime bool        `json:"runtime,omitempty" doc:"Whether the attribute is runtime, which expressions read and write (PRD FR-COMP-03)"`
	Options []string    `json:"options,omitempty" doc:"An enum's options"`
	Default value.Value `json:"default,omitzero" doc:"The attribute definition's default, a value of its type, as attribute.create takes it. Without it, the type's zero value."`
}

// Text is a rich-text document of a clipboard (PRD FR-TEXT-01), as its JSON
// writes it, which Check reads (richtext.Parse): a type of its own, rather
// than a richtext.Doc, which a command's payload holds only as a field of its
// own (commands.Register), so that a document that Parse refuses is answered
// at its field of the clipboard all the same (Check). Its schema is a
// document's.
type Text []byte

// Schema gives huma the schema of a rich-text document (huma.SchemaProvider).
func (Text) Schema(r huma.Registry) *huma.Schema { return richtext.Doc{}.Schema(r) }

// MarshalJSON writes the document as it is held, or null for none.
func (t Text) MarshalJSON() ([]byte, error) {
	if len(t) == 0 {
		return []byte("null"), nil
	}
	return t, nil
}

// UnmarshalJSON keeps the document's JSON as it is, and none for null.
func (t *Text) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*t = nil
		return nil
	}
	*t = append(Text(nil), data...)
	return nil
}

// textOf returns the document doc as a Text.
func textOf(doc richtext.Doc) (Text, error) {
	data, err := json.Marshal(doc)
	return Text(data), err
}
