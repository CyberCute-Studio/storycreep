package validation

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/authz"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
)

// The types of command of a mute (FR-DEP-03 AC4). Each names its owner, a
// node, a connection, a branch output, a variable, or a board of the batch's
// project outside its trash, and a code of a warning or an info about the
// owner's kind (Mutable).
const (
	// MuteFinding mutes the code on its owner, with a reason, or changes
	// the reason of its mute. Unmuting it, or muting it again with the
	// reason that it had, undoes it.
	MuteFinding commands.Type = "validation.mute"
	// UnmuteFinding unmutes the code on its owner. Muting it again with its
	// reason undoes it.
	UnmuteFinding commands.Type = "validation.unmute"
)

// MaxReasonLength is the most characters of a mute's reason, which the
// database holds too.
const MaxReasonLength = 500

// mutePayload is MuteFinding's payload.
type mutePayload struct {
	EntityID string `json:"entity_id" doc:"The ID of the entity whose finding the mute silences, its owner: a node, a connection, a branch output, a variable, or a board of the project, not in its trash"`
	Code     string `json:"code" enum:"port_unconnected,var_read_never_written,var_written_never_read,node_unreachable,node_empty,choice_duplicate_label,dead_end,speaker_missing,board_entry_missing,connection_self,connection_parallel" doc:"The code that the mute silences on its owner, a warning's or an info's about the owner's type, as PRD FR-DEP-03 lists them: node_unreachable, node_empty, dead_end, or speaker_missing of a node; choice_duplicate_label, connection_self, or connection_parallel of a connection; port_unconnected of a branch output; var_read_never_written or var_written_never_read of a variable; or board_entry_missing of a board. An error is never muted."`
	Reason   string `json:"reason" minLength:"1" maxLength:"500" doc:"Why the finding is muted, of 1 to 500 characters, not all of them blank, without white space around it, with its line breaks written as line feeds, and without control characters among it but line feeds and tabs"`
}

// unmutePayload is UnmuteFinding's payload.
type unmutePayload struct {
	EntityID string `json:"entity_id" doc:"The ID of the mute's owner: a node, a connection, a branch output, a variable, or a board of the project, not in its trash"`
	Code     string `json:"code" enum:"port_unconnected,var_read_never_written,var_written_never_read,node_unreachable,node_empty,choice_duplicate_label,dead_end,speaker_missing,board_entry_missing,connection_self,connection_parallel" doc:"The code that the mute silences on its owner, as validation.mute names it. Unmuting a code that the owner has no mute of changes nothing."`
}

// Register registers in r the commands of the mutes, each of which changes
// the project's draft, so that an API token needs project:write for it
// (FR-AUTH-04), and its role "Edit nodes, connections, text, expressions"
// (§5.6), as editing the content does, whatever the kind of its owner. A
// mute is part of its owner, so the commands touch the kinds of the owners,
// which r has registered already (board.Register, variable.Register,
// node.Register, connection.Register): a mute's change is a change of its
// owner, whose revision it increments, and which a client expects the
// revision of.
func Register(r *commands.Registry) {
	sc := scope.ProjectWrite
	commands.Register(r, commands.Spec[mutePayload]{
		Type: MuteFinding, Capability: authz.EditContent, Scope: sc, Touches: MuteOwners,
		Check: func(p mutePayload) input.FieldErrors {
			return checkMute(p.EntityID, p.Code)
		},
		// The text rules of a reason, which a step that writes back a stored
		// reason is not held to (commands.Spec.CheckNew).
		CheckNew: func(p mutePayload) input.FieldErrors {
			return field("reason", checkReason(p.Reason))
		},
		Inverse: func(ctx context.Context, tx *commands.Tx, p mutePayload) ([]commands.Step, error) {
			return back(ctx, tx, p.EntityID, p.Code)
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p mutePayload) error {
			kind, owner, err := liveOwner(ctx, tx, p.EntityID)
			if err != nil {
				return err
			}
			params := queries.UpsertValidationMuteParams{WorkspaceID: pgUUID(tx.WorkspaceID()),
				ProjectID: pgUUID(tx.ProjectID()), Code: p.Code, Reason: p.Reason, UpdatedBy: by(tx)}
			switch kind {
			case ids.Node:
				params.NodeID = owner
			case ids.Connection:
				params.ConnectionID = owner
			case ids.BranchOutput:
				params.BranchOutputID = owner
			case ids.Variable:
				params.VariableID = owner
			default:
				params.BoardID = owner
			}
			n, err := queries.New(tx).UpsertValidationMute(ctx, params)
			if err != nil {
				return fmt.Errorf("validation: mute %s of %s: %w", p.Code, p.EntityID, err)
			}
			return touch(ctx, tx, kind, owner, n)
		},
	})

	commands.Register(r, commands.Spec[unmutePayload]{
		Type: UnmuteFinding, Capability: authz.EditContent, Scope: sc, Touches: MuteOwners,
		Check: func(p unmutePayload) input.FieldErrors {
			return checkMute(p.EntityID, p.Code)
		},
		Inverse: func(ctx context.Context, tx *commands.Tx, p unmutePayload) ([]commands.Step, error) {
			return back(ctx, tx, p.EntityID, p.Code)
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p unmutePayload) error {
			kind, owner, err := liveOwner(ctx, tx, p.EntityID)
			if err != nil {
				return err
			}
			n, err := queries.New(tx).DeleteValidationMute(ctx, queries.DeleteValidationMuteParams{
				WorkspaceID: pgUUID(tx.WorkspaceID()), ProjectID: pgUUID(tx.ProjectID()), OwnerID: owner,
				Code: p.Code})
			if err != nil {
				return fmt.Errorf("validation: unmute %s of %s: %w", p.Code, p.EntityID, err)
			}
			return touch(ctx, tx, kind, owner, n)
		},
	})
}

// checkMute returns what is wrong with a mute's owner, entity, and its code,
// that a payload's schema cannot say: an owner that is not an ID of a kind
// that a mute may be of, and a code that is not a warning's or an info's
// about the owner's kind.
func checkMute(entity, code string) input.FieldErrors {
	kind, _, err := ids.DecodeAny(entity)
	if err != nil || !slices.Contains(MuteOwners, kind) {
		return field("entity_id", "is not the ID of a node, a connection, a branch output, a variable, or a board")
	}
	if !slices.Contains(Mutable(kind), Code(code)) {
		return field("code", fmt.Sprintf("is not a warning or an info about a %s", kind))
	}
	return nil
}

// checkReason says what is wrong with a mute's reason, or returns "": one
// that is blank, or that the text rules of package input refuse.
func checkReason(reason string) string {
	if strings.IndexFunc(reason, input.Printed) < 0 {
		return "is empty"
	}
	return input.CheckTrimmedText(reason, MaxReasonLength)
}

// back returns the step that sets back the mute of the code on the owner
// entity as it is: muting it with its reason, or unmuting it, if it has no
// mute.
func back(ctx context.Context, tx *commands.Tx, entity, code string) ([]commands.Step, error) {
	_, owner, _ := ids.DecodeAny(entity)
	reason, err := queries.New(tx).ValidationMute(ctx, queries.ValidationMuteParams{
		WorkspaceID: pgUUID(tx.WorkspaceID()), ProjectID: pgUUID(tx.ProjectID()), OwnerID: pgUUID(owner), Code: code})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return []commands.Step{{Type: UnmuteFinding, Payload: unmutePayload{EntityID: entity, Code: code}}}, nil
	case err != nil:
		return nil, fmt.Errorf("validation: read the mute of %s of %s: %w", code, entity, err)
	}
	return []commands.Step{{Type: MuteFinding, Payload: mutePayload{EntityID: entity, Code: code, Reason: reason}}}, nil
}

// liveOwner returns the kind and the UUID of the owner entity, an ID that
// Check checked, or refuses the command at entity_id unless it is an entity
// of the project outside its trash.
func liveOwner(ctx context.Context, tx *commands.Tx, entity string) (ids.Kind, pgtype.UUID, error) {
	kind, id, _ := ids.DecodeAny(entity)
	live, err := queries.New(tx).MuteOwnerLive(ctx, queries.MuteOwnerLiveParams{Kind: kind.String(),
		WorkspaceID: pgUUID(tx.WorkspaceID()), ProjectID: pgUUID(tx.ProjectID()), ID: pgUUID(id)})
	if err != nil {
		return 0, pgtype.UUID{}, fmt.Errorf("validation: read the mute's owner %s: %w", entity, err)
	}
	if !live {
		return 0, pgtype.UUID{}, commands.Refuse("entity_id",
			fmt.Sprintf("is not a %s of the project outside its trash", kind), nil)
	}
	return kind, pgUUID(id), nil
}

// touch reports the change of the owner of the kind, whose mute changed if
// n, the mutes that the command added, changed, or deleted, is not 0, with
// its new revision; a command that changed no mute changed nothing.
func touch(ctx context.Context, tx *commands.Tx, kind ids.Kind, owner pgtype.UUID, n int64) error {
	if n == 0 {
		return nil
	}
	rev, err := queries.New(tx).TouchMuteOwner(ctx, queries.TouchMuteOwnerParams{UpdatedBy: by(tx),
		Kind: kind.String(), WorkspaceID: pgUUID(tx.WorkspaceID()), ProjectID: pgUUID(tx.ProjectID()), ID: owner})
	if err != nil {
		return fmt.Errorf("validation: change the mute's owner: %w", err)
	}
	return tx.Changed(kind, owner.Bytes, rev)
}

// by returns the user who applies the batch, as a uuid column.
func by(tx *commands.Tx) pgtype.UUID {
	return pgUUID(tx.Actor().UserID)
}

// field returns the problem msg of the payload's field name, or none if msg
// is "".
func field(name, msg string) input.FieldErrors {
	if msg == "" {
		return nil
	}
	return input.FieldErrors{{Field: name, Message: msg}}
}
