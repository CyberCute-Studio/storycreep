package published

import (
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemagen"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// StateVersion is the version of the save-state format that SaveState is of,
// which is versioned apart from the published format (§6.4, FR-GODOT-07 AC2).
const StateVersion = 1

// SaveState is the narrative state that a runtime saves and loads (§8.5,
// FR-GODOT-07): where the flow is, and the values of the variables and the
// runtime attributes, and the nodes' visit counts, by ID, never by key, so
// that a save survives renames (FR-COMP-03 AC3). It holds no text. A game
// stores it as it likes, and may read it back with every number a float, as
// Godot's JSON parser reads it, which a runtime types by its declaration
// (§5.8.9).
type SaveState struct {
	Attributes      map[string]AttributeValues `json:"attributes" keyId:"component" doc:"The values of the components' runtime attributes, by the component's ID and the attribute definition's."`
	Cursor          *Cursor                    `json:"cursor,omitempty" doc:"Where the flow is. Without it, the flow has not started."`
	ProjectID       string                     `json:"projectId" id:"project"`
	SchemaVersion   Int                        `json:"schemaVersion" minimum:"1" maximum:"9007199254740991" doc:"The version of the published format of the snapshot that the state was saved against."`
	SnapshotID      string                     `json:"snapshotId,omitempty" id:"snapshot" doc:"The snapshot that the state was saved against. Without it, the state was saved against a draft (PRD §7.8)."`
	SnapshotVersion string                     `json:"snapshotVersion,omitempty" minLength:"1" doc:"The snapshot's version. Without it, the state was saved against a draft."`
	// StateVersion is StateVersion.
	StateVersion Int                    `json:"stateVersion" const:"1" doc:"The version of the save-state format, 1, which a runtime of an older one refuses (PRD §8.5)."`
	Variables    map[string]value.Value `json:"variables" keyId:"variable" doc:"The values of the variables, global and of each board, by their IDs, each of its variable's type."`
	Visits       Visits                 `json:"visits" doc:"How many times the flow has entered each node that it has entered, by the node's ID; a node that it has not entered has none."`
}

// MarshalJSON writes the save state, its variables, attributes, and visits {}
// when it has none, or fails on what Decode would refuse of it, as a value
// that is not set (marshalFile).
func (s SaveState) MarshalJSON() ([]byte, error) {
	type plain SaveState
	return marshalFile[SaveState]("a save state", plain(s))
}

// JSONSchemaDoc describes a save state (schemagen.Documented).
func (SaveState) JSONSchemaDoc() string {
	return "The narrative state that a runtime saves and loads (PRD §8.5, FR-GODOT-07): where the flow is, and " +
		"the values of the variables and the runtime attributes, and the nodes' visit counts, by ID, never by key."
}

// The phases of a cursor.
const (
	// PhaseAwaitingChoice is a flow at a node whose choices are built, of
	// which the game chooses one, or advances by the one connection that
	// has no condition (§8.4.2).
	PhaseAwaitingChoice = "awaiting_choice"
	// PhaseEnded is a flow that ended at its node, whose on-exit has run
	// (§5.8.5).
	PhaseEnded = "ended"
)

// Cursor is where a flow is: its current node, and its phase.
type Cursor struct {
	BoardID string `json:"boardId" id:"board"`
	NodeID  string `json:"nodeId" id:"node" doc:"The current node, a node of the board that is no jumper."`
	Phase   string `json:"phase" enum:"awaiting_choice,ended" doc:"awaiting_choice when the node's choices are built and the game chooses one, or advances; ended when the flow ended at the node, after its on-exit ran (PRD §5.8.5)."`
}

// JSONSchemaDoc describes a cursor (schemagen.Documented).
func (Cursor) JSONSchemaDoc() string {
	return "Where the flow is: its current node, and its phase."
}

// Visits are how many times a flow has entered each node that it has entered,
// by the node's ID, each at least once.
type Visits map[string]Int

// JSONSchema gives the schemas the definition of visit counts
// (schemagen.Provider).
func (Visits) JSONSchema(g *schemagen.Generator) *schemagen.Schema {
	return schemagen.Describe(schemagen.Map(&schemagen.Schema{Type: "integer", Minimum: schemagen.Float(1),
		Maximum: schemagen.Float(MaxInt)},
		g.ID(ids.Node)), "How many times the flow has entered each node that it has entered, by the node's ID.")
}
