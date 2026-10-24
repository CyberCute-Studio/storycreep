package commands

import (
	"errors"
	"fmt"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// commandRefusal is a command's refusal of its batch (Refuse).
type commandRefusal struct {
	field, message string
	reason         error
}

func (r *commandRefusal) Error() string {
	if r.field == "" {
		return "commands: the payload " + r.message
	}
	return "commands: the payload's " + r.field + " " + r.message
}

func (r *commandRefusal) Unwrap() error {
	return r.reason
}

// Refuse returns the error with which a command's Inverse or Apply refuses its
// batch for what the project's state makes of its payload, which the client
// can mend, as an ID of a board that the project does not have, or a slug that
// another of its boards has: the payload's field field, named as in its JSON,
// as slug or entry.board_id, or the payload itself if field is "", which the
// message says what is wrong with, in English, for programs and logs, as "is
// another board's slug". reason is nil for a value that the project's state
// does not take, which the API answers with validation_failed, or the error
// of a refusal that it answers with a code of its own, as a slug that is taken
// is answered with slug_taken. The pipeline returns the batch's refusal as a
// *RefusedError. The batch applies nothing, and has no idempotency record: it
// is checked anew whenever it is sent again (FR-API-03).
func Refuse(field, message string, reason error) error {
	return &commandRefusal{field: field, message: message, reason: reason}
}

// RefusedError is a batch that one of its commands refused (Refuse), which
// applies nothing.
type RefusedError struct {
	// Command is the index of the command in the batch, and CommandID and
	// Type are its.
	Command   int
	CommandID ids.UUID
	Type      Type
	// Field is the field of the command's payload that is wrong, as slug, or
	// "" for the payload itself, and Message says what is wrong with it.
	Field, Message string
	// Reason is the refusal's reason, or nil for a value that the project's
	// state does not take.
	Reason error
}

func (e *RefusedError) Error() string {
	if e.Field == "" {
		return fmt.Sprintf("commands: command %d (%s) refused the batch: the payload %s", e.Command, e.Type,
			e.Message)
	}
	return fmt.Sprintf("commands: command %d (%s) refused the batch: the payload's %s %s", e.Command, e.Type,
		e.Field, e.Message)
}

func (e *RefusedError) Unwrap() error {
	return e.Reason
}

// refusedBy returns err as the *RefusedError of the command c, the batch's
// command i, if a refusal of it refused the batch (Refuse), and false
// otherwise.
func refusedBy(err error, i int, c decoded) (*RefusedError, bool) {
	var r *commandRefusal
	if !errors.As(err, &r) {
		return nil, false
	}
	return &RefusedError{Command: i, CommandID: c.ID, Type: c.spec.typ, Field: r.field, Message: r.message,
		Reason: r.reason}, true
}

// undoRefusal returns the refusal of the command c, the batch's command i,
// whose inverse would take the batch's undo past maxUndo bytes, which the
// pipeline answers at the payload's field that its Spec.UndoField names, as
// a value that the project's state does not take.
func undoRefusal(i int, c decoded, maxUndo int) *RefusedError {
	size := fmt.Sprintf("%d bytes", maxUndo)
	if maxUndo%(1<<20) == 0 {
		size = fmt.Sprintf("%d MiB", maxUndo>>20)
	}
	return &RefusedError{Command: i, CommandID: c.ID, Type: c.spec.typ, Field: c.spec.undoField,
		Message: "would take the batch's undo, the inverses of its commands as the operation log keeps them, past " +
			size + ", which a batch that undoes it could not send: send the commands in smaller batches, or make a " +
			"change whose undo holds less"}
}

// undoStepsRefusal returns the refusal of the command c, the batch's command
// i, whose inverse would give the batch's undo more than maxSteps steps,
// which a batch that undoes it could not send, which the pipeline answers at
// the payload, as a value that the project's state does not take. A command
// whose own inverse has that many steps, as the restore of a board under
// more folders in the trash than that, which its undo deletes again, applies
// once what its inverse undoes is changed first, in batches of fewer steps,
// as those folders restored from the top down.
func undoStepsRefusal(i int, c decoded, maxSteps int) *RefusedError {
	return &RefusedError{Command: i, CommandID: c.ID, Type: c.spec.typ,
		Message: fmt.Sprintf("would give the batch's undo, the inverses of its commands as the operation log keeps "+
			"them, more than %d commands, which a batch that undoes it could not send: send the commands in smaller "+
			"batches, or make a change whose undo has fewer commands, as restoring first the folders in the trash "+
			"that a restore would bring back", maxSteps)}
}
