package httpserver

import (
	"encoding/json"
	"errors"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// conflictBody is an entity of the details of an answer of
// revision_conflict, whose revision is not the one that a command of the
// batch expected.
type conflictBody struct {
	// Command is the command's index in the batch, and CommandID and Type
	// are its.
	Command     int           `json:"command"`
	CommandID   string        `json:"command_id"`
	Type        commands.Type `json:"type"`
	ID          string        `json:"id"`
	ExpectedRev int64         `json:"expected_rev"`
	Exists      bool          `json:"exists"`
	// CurrentRev and State are the entity's revision and the entity as it is
	// now, if it still exists.
	CurrentRev *int64          `json:"current_rev,omitempty"`
	State      json.RawMessage `json:"state,omitempty"`
}

// revisionConflictError returns the error that the API answers err with if
// err is a *commands.RevisionConflictError, or err itself otherwise: 409
// revision_conflict, whose details list as conflicts each entity of a
// command of the batch whose revision is not the one that the command
// expected, with the command, by its index in the batch, its command_id, and
// its type, the revision that it expected, and the entity's current revision
// and state if it still exists, or exists false if it does not (PRD FR-API-03
// AC3, FR-OFF-04 AC5). The error mapper of a route that applies commands
// calls it.
func revisionConflictError(err error) error {
	var conflict *commands.RevisionConflictError
	if !errors.As(err, &conflict) {
		return err
	}
	conflicts := make([]conflictBody, len(conflict.Conflicts))
	for i, c := range conflict.Conflicts {
		conflicts[i] = conflictBody{Command: c.Command, CommandID: c.CommandID.String(), Type: c.Type,
			ID: ids.Encode(c.Kind, c.ID), ExpectedRev: c.Expected, Exists: c.Exists}
		if c.Exists {
			current := c.Current
			conflicts[i].CurrentRev, conflicts[i].State = &current, c.State
		}
	}
	return &api.Error{Code: api.CodeRevisionConflict, Details: map[string]any{"conflicts": conflicts}}
}
