package playstate

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
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// The types of command of a play state (PRD §5.7). Each names the play state
// that it is about by its ID, id, and applies to the play state of the
// batch's project.
const (
	// Create saves a play state, whose ID the client makes, as it makes the
	// command's, so that it can refer to the play state before the server
	// answers. Deleting it to the trash undoes it.
	Create commands.Type = "play_state.create"
	// Rename changes a play state's name, and SetState its state, as Replace
	// in play mode replaces it with the flow's state as it is. Changing each
	// back undoes it.
	Rename   commands.Type = "play_state.rename"
	SetState commands.Type = "play_state.set_state"
	// Delete deletes a play state to the project's trash, unlisted, and
	// Restore restores it. Each undoes the other.
	Delete  commands.Type = "play_state.delete"
	Restore commands.Type = "play_state.restore"
)

// createPayload is Create's payload.
type createPayload struct {
	ID    string              `json:"id" doc:"The new play state's ID, a pst_ ID of a UUIDv7 that the client makes, which no entity of the instance has (PRD §5.2)"`
	Name  string              `json:"name" maxLength:"100" doc:"The play state's name, of 1 to 100 characters, without white space around it, and without control characters, which need not be unique among the project's play states"`
	State published.SaveState `json:"state" doc:"The play state's state: a save state of the project's flow, as a runtime's save_state() returns it (PRD §8.5), whose JSON Schema is /schema/save-state/v1/save-state.schema.json: where the flow is, and the values of the variables and of the components' runtime attributes, and the nodes' visit counts, by ID, never by key. Its stateVersion is 1, its projectId the project's ID, and each ID, as the keys of its variables, attributes, and visits, a canonical ID of its kind, as the schema's patterns say; one that is not is answered with 422 validation_failed, as a state of more than 1048576 bytes is, as JSON. The server keeps it as the client sent it, and reads nothing of the project's content in it: what its entities have become since it was saved is play mode's to read as it loads it (PRD FR-PLAY-05 AC2)."`
	Names PlayStateNames      `json:"names,omitzero" doc:"The names of the state's values, as play mode named them when it saved the state, which it names a value that a load of the state drops by once its entity is deleted (PRD FR-PLAY-05 AC2): a variable's by its ID, and a runtime attribute's by its component's ID and its attribute definition's, each of a value that the state holds, of 1 to 200 characters, without white space around it, and without control characters. A name of a value that the state does not hold is answered with 422 validation_failed, as names of more than 1048576 bytes are, as JSON. Without it, none."`
}

// renamePayload is Rename's payload.
type renamePayload struct {
	ID   string `json:"id" doc:"The play state's ID"`
	Name string `json:"name" maxLength:"100" doc:"The play state's new name, of 1 to 100 characters, without white space around it, and without control characters, which need not be unique among the project's play states"`
}

// statePayload is SetState's payload.
type statePayload struct {
	ID    string              `json:"id" doc:"The play state's ID"`
	State published.SaveState `json:"state" doc:"The play state's new state: a save state of the project's flow, as a runtime's save_state() returns it (PRD §8.5), whose JSON Schema is /schema/save-state/v1/save-state.schema.json: where the flow is, and the values of the variables and of the components' runtime attributes, and the nodes' visit counts, by ID, never by key. Its stateVersion is 1, its projectId the project's ID, and each ID, as the keys of its variables, attributes, and visits, a canonical ID of its kind, as the schema's patterns say; one that is not is answered with 422 validation_failed, as a state of more than 1048576 bytes is, as JSON. The server keeps it as the client sent it, and reads nothing of the project's content in it: what its entities have become since it was saved is play mode's to read as it loads it (PRD FR-PLAY-05 AC2)."`
	Names PlayStateNames      `json:"names,omitzero" doc:"The names of the state's values, as play mode named them when it saved the state, which it names a value that a load of the state drops by once its entity is deleted (PRD FR-PLAY-05 AC2): a variable's by its ID, and a runtime attribute's by its component's ID and its attribute definition's, each of a value that the state holds, of 1 to 200 characters, without white space around it, and without control characters. A name of a value that the state does not hold is answered with 422 validation_failed, as names of more than 1048576 bytes are, as JSON. Without it, none."`
}

// idPayload is the payload of a command that needs nothing but its play
// state's ID.
type idPayload struct {
	ID string `json:"id" doc:"The play state's ID"`
}

// Register registers in r the play state as an entity of the pipeline, whose
// revisions a command expects as it does any entity's, and its commands, each
// of which changes the project's draft, as far as an API token and the trash
// are concerned, so that an API token needs project:write for it
// (FR-AUTH-04), and its role "Edit nodes, connections, text, expressions"
// (§5.6).
func Register(r *commands.Registry) {
	commands.RegisterEntity(r, commands.Entity{
		Kind: ids.PlayState,
		Revs: func(ctx context.Context, tx *commands.Tx, entities []ids.UUID) (map[ids.UUID]int64, error) {
			rows, err := queries.New(tx).PlayStateRevs(ctx, queries.PlayStateRevsParams{
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
			rows, err := queries.New(tx).PlayStatesByID(ctx, queries.PlayStatesByIDParams{
				WorkspaceID: nullUUID(tx.WorkspaceID()), ProjectID: nullUUID(tx.ProjectID()), Ids: uuids(entities)})
			if err != nil {
				return nil, err
			}
			out := make(map[ids.UUID]any, len(rows))
			for _, row := range rows {
				b, err := bodyOf(row)
				if err != nil {
					return nil, err
				}
				out[row.ID.Bytes] = b
			}
			return out, nil
		},
	})

	playState := []ids.Kind{ids.PlayState}
	op, sc := authz.EditContent, scope.ProjectWrite

	commands.Register(r, commands.Spec[createPayload]{
		Type: Create, Capability: op, Scope: sc, Touches: playState,
		Check: func(p createPayload) input.FieldErrors {
			return append(append(newID(p.ID), checkState("state", p.State)...), checkNames(p.State, p.Names)...)
		},
		CheckNew: func(p createPayload) input.FieldErrors {
			return append(field("name", checkName(p.Name)), checkNameTexts(p.Names)...)
		},
		Inverse: func(_ context.Context, _ *commands.Tx, p createPayload) ([]commands.Step, error) {
			return []commands.Step{{Type: Delete, Payload: idPayload{ID: p.ID}}}, nil
		},
		Apply: applyCreate,
	})

	commands.Register(r, commands.Spec[renamePayload]{
		Type: Rename, Capability: op, Scope: sc, Touches: playState,
		Check: func(p renamePayload) input.FieldErrors { return entityID(p.ID) },
		// The step of the inverse writes back the stored name, which the
		// text rules may refuse (commands.Spec.CheckNew). The database holds
		// a name to the length that the payload's schema allows.
		CheckNew: func(p renamePayload) input.FieldErrors { return field("name", checkName(p.Name)) },
		Inverse: func(ctx context.Context, tx *commands.Tx, p renamePayload) ([]commands.Step, error) {
			s, err := livePlayState(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			return []commands.Step{{Type: Rename, Payload: renamePayload{ID: p.ID, Name: s.Name}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p renamePayload) error {
			s, err := livePlayState(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			return changed(tx, s.ID)(queries.New(tx).RenamePlayState(ctx, queries.RenamePlayStateParams{
				Name: p.Name, UpdatedBy: by(tx), WorkspaceID: s.WorkspaceID, ProjectID: s.ProjectID, ID: s.ID}))
		},
	})

	commands.Register(r, commands.Spec[statePayload]{
		Type: SetState, Capability: op, Scope: sc, Touches: playState, UndoField: "state",
		Check: func(p statePayload) input.FieldErrors {
			return append(append(entityID(p.ID), checkState("state", p.State)...), checkNames(p.State, p.Names)...)
		},
		CheckNew: func(p statePayload) input.FieldErrors { return checkNameTexts(p.Names) },
		Inverse: func(ctx context.Context, tx *commands.Tx, p statePayload) ([]commands.Step, error) {
			s, err := livePlayState(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			st, names, err := stateOf(s.State, s.Names)
			if err != nil {
				return nil, fmt.Errorf("playstate: the state of the play state %s: %w", p.ID, err)
			}
			return []commands.Step{{Type: SetState, Payload: statePayload{ID: p.ID, State: st, Names: names}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p statePayload) error {
			s, err := livePlayState(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			data, names, err := stored(tx, p.State, p.Names)
			if err != nil {
				return err
			}
			return changed(tx, s.ID)(queries.New(tx).UpdatePlayState(ctx, queries.UpdatePlayStateParams{
				State: data, Names: names, UpdatedBy: by(tx), WorkspaceID: s.WorkspaceID, ProjectID: s.ProjectID,
				ID: s.ID}))
		},
	})

	commands.Register(r, commands.Spec[idPayload]{
		Type: Delete, Capability: op, Scope: sc, Touches: playState,
		Check: func(p idPayload) input.FieldErrors { return entityID(p.ID) },
		// Deleting a play state that is in the trash already does nothing,
		// and its undo nothing either.
		Inverse: func(ctx context.Context, tx *commands.Tx, p idPayload) ([]commands.Step, error) {
			s, err := anyPlayState(ctx, tx, p.ID)
			if err != nil || s.DeletedAt.Valid {
				return []commands.Step{}, err
			}
			return []commands.Step{{Type: Restore, Payload: idPayload{ID: p.ID}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p idPayload) error {
			s, err := anyPlayState(ctx, tx, p.ID)
			if err != nil || s.DeletedAt.Valid {
				return err
			}
			return changed(tx, s.ID)(queries.New(tx).TrashPlayState(ctx, queries.TrashPlayStateParams{
				WorkspaceID: s.WorkspaceID, ProjectID: s.ProjectID, ID: s.ID}))
		},
	})

	commands.Register(r, commands.Spec[idPayload]{
		Type: Restore, Capability: op, Scope: sc, Touches: playState,
		Check: func(p idPayload) input.FieldErrors { return entityID(p.ID) },
		// Restoring a play state that is not in the trash does nothing, and
		// its undo nothing either.
		Inverse: func(ctx context.Context, tx *commands.Tx, p idPayload) ([]commands.Step, error) {
			s, err := anyPlayState(ctx, tx, p.ID)
			if err != nil || !s.DeletedAt.Valid {
				return []commands.Step{}, err
			}
			return []commands.Step{{Type: Delete, Payload: idPayload{ID: p.ID}}}, nil
		},
		Apply: func(ctx context.Context, tx *commands.Tx, p idPayload) error {
			s, err := anyPlayState(ctx, tx, p.ID)
			if err != nil || !s.DeletedAt.Valid {
				return err
			}
			return changed(tx, s.ID)(queries.New(tx).RestorePlayState(ctx, queries.RestorePlayStateParams{
				WorkspaceID: s.WorkspaceID, ProjectID: s.ProjectID, ID: s.ID}))
		},
	})
}

// applyCreate is Create's Apply.
func applyCreate(ctx context.Context, tx *commands.Tx, p createPayload) error {
	q := queries.New(tx)
	ws, project := nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID())
	id := decoded(ids.PlayState, p.ID)
	// A play state of the project with the ID is refused for its ID, so that
	// a create sent again with a new command ID, as an offline client's
	// keep-mine sends it (FR-OFF-04), is refused for its ID; the insert below
	// refuses the ID of a play state that this transaction does not see.
	if _, err := q.PlayStateAny(ctx, queries.PlayStateAnyParams{WorkspaceID: ws, ProjectID: project, ID: id}); err == nil {
		return commands.Refuse("id", "is the ID of a play state that exists already", nil)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("playstate: read the play state: %w", err)
	}
	data, names, err := stored(tx, p.State, p.Names)
	if err != nil {
		return err
	}
	err = q.InsertPlayState(ctx, queries.InsertPlayStateParams{ID: id, WorkspaceID: ws, ProjectID: project,
		Name: p.Name, State: data, Names: names, CreatedBy: by(tx)})
	if database.UniqueViolation(err, "play_state_pkey") {
		// The ID is another project's, or another workspace's, whose rows
		// this transaction does not see.
		return commands.Refuse("id", "is the ID of a play state that exists already", nil)
	}
	if err != nil {
		return fmt.Errorf("playstate: add the play state: %w", err)
	}
	return tx.Changed(ids.PlayState, id.Bytes, 1)
}

// stored returns the state st and the names of its values as the columns of
// a play state hold them, once it has checked that st is a state of the
// batch's project, or refuses the batch at state.
func stored(tx *commands.Tx, st published.SaveState, names PlayStateNames) (state, namesData []byte, err error) {
	if st.ProjectID != ids.Encode(ids.Project, tx.ProjectID()) {
		return nil, nil, commands.Refuse("state.projectId", "is not the ID of the batch's project, whose play "+
			"states hold its own states only", nil)
	}
	if state, err = json.Marshal(st); err != nil {
		// Check took the state, so this is only a value that the schema
		// cannot describe.
		return nil, nil, commands.Refuse("state", "is no save state: "+err.Error(), nil)
	}
	if namesData, err = json.Marshal(names); err != nil {
		return nil, nil, fmt.Errorf("playstate: write the names of the state's values: %w", err)
	}
	return state, namesData, nil
}

// checkNames returns the problems of names, the names of the values of the
// state st, of which each must be a value's that st holds, and which take at
// most MaxNamesBytes, as JSON. It reads nothing but its arguments.
func checkNames(st published.SaveState, names PlayStateNames) input.FieldErrors {
	var errs input.FieldErrors
	if data, err := json.Marshal(names); err == nil && len(data) > MaxNamesBytes {
		errs = append(errs, input.FieldError{Field: "names", Message: fmt.Sprintf("take %d bytes as JSON, more "+
			"than the %d that the names of a play state's values may take", len(data), MaxNamesBytes)})
	}
	for _, v := range slices.Sorted(maps.Keys(names.Variables)) {
		if _, ok := st.Variables[v]; !ok {
			errs = append(errs, input.FieldError{Field: "names.variables." + v,
				Message: "is the name of a variable whose value the state does not hold"})
		}
	}
	for _, c := range slices.Sorted(maps.Keys(names.Attributes)) {
		for _, a := range slices.Sorted(maps.Keys(names.Attributes[c])) {
			if _, ok := st.Attributes[c][a]; !ok {
				errs = append(errs, input.FieldError{Field: "names.attributes." + c + "." + a,
					Message: "is the name of an attribute whose value the state does not hold"})
			}
		}
	}
	return errs
}

// checkNameTexts returns the problems of names as texts that people are shown
// (input.CheckTrimmedName), which a stored name that the step of an inverse
// writes back may break (commands.Spec.CheckNew).
func checkNameTexts(names PlayStateNames) input.FieldErrors {
	var errs input.FieldErrors
	for _, v := range slices.Sorted(maps.Keys(names.Variables)) {
		if msg := input.CheckTrimmedName(names.Variables[v], MaxValueNameLength); msg != "" {
			errs = append(errs, input.FieldError{Field: "names.variables." + v, Message: msg})
		}
	}
	for _, c := range slices.Sorted(maps.Keys(names.Attributes)) {
		for _, a := range slices.Sorted(maps.Keys(names.Attributes[c])) {
			if msg := input.CheckTrimmedName(names.Attributes[c][a], MaxValueNameLength); msg != "" {
				errs = append(errs, input.FieldError{Field: "names.attributes." + c + "." + a, Message: msg})
			}
		}
	}
	return errs
}

// checkState returns the problems of st, the state of a payload's field name,
// beyond what its schema, which huma builds from its Go type, says: a
// stateVersion that is not 1; a projectId, a snapshotId, a cursor's boardId or
// nodeId, or a key of its variables, its attributes, or its visits, that is
// not a canonical ID of its kind; a snapshotId without a snapshotVersion, or
// one without the other; a value that is no boolean, number, or string; a
// visit count below 1 or beyond MaxInt; and a state of more than
// MaxStateBytes, as JSON, with each number written as a runtime writes it,
// however the payload writes it (shortestNumbers), so that the undo of a
// replace, which writes each number of the state back with every digit, as
// the operation log does, is held to the bound that the state was. That is
// all that the server checks of a state, with Apply's check that it is of
// the batch's project (stored). It reads nothing but st.
func checkState(name string, st published.SaveState) input.FieldErrors {
	var errs input.FieldErrors
	add := func(at, msg string) {
		errs = append(errs, input.FieldError{Field: name + "." + at, Message: msg})
	}
	canonical := func(at string, k ids.Kind, id string) {
		if !ids.IsCanonical(k, id) {
			add(at, fmt.Sprintf("is not a canonical %s's ID, as %s", k, k.CanonicalPattern()))
		}
	}
	if st.StateVersion != published.StateVersion {
		add("stateVersion", fmt.Sprintf("is not %d, the version of the save-state format that the server takes",
			published.StateVersion))
	}
	canonical("projectId", ids.Project, st.ProjectID)
	switch {
	case st.SnapshotID != "" && st.SnapshotVersion == "":
		add("snapshotVersion", "is missing, which a state with a snapshotId has")
	case st.SnapshotID == "" && st.SnapshotVersion != "":
		add("snapshotId", "is missing, which a state with a snapshotVersion has")
	case st.SnapshotID != "":
		canonical("snapshotId", ids.Snapshot, st.SnapshotID)
	}
	if c := st.Cursor; c != nil {
		canonical("cursor.boardId", ids.Board, c.BoardID)
		canonical("cursor.nodeId", ids.Node, c.NodeID)
	}
	checkValue := func(at string, v value.Value) {
		if v.Kind() == "" {
			add(at, "is no value: neither a boolean, a number, nor a string")
		}
	}
	// A key that is not canonical is refused at its own path, as its value
	// is, so that each is told apart.
	for _, k := range slices.Sorted(maps.Keys(st.Variables)) {
		canonical("variables."+k, ids.Variable, k)
		checkValue("variables."+k, st.Variables[k])
	}
	for _, c := range slices.Sorted(maps.Keys(st.Attributes)) {
		canonical("attributes."+c, ids.Component, c)
		for _, a := range slices.Sorted(maps.Keys(st.Attributes[c])) {
			canonical("attributes."+c+"."+a, ids.AttributeDef, a)
			checkValue("attributes."+c+"."+a, st.Attributes[c][a])
		}
	}
	for _, n := range slices.Sorted(maps.Keys(st.Visits)) {
		canonical("visits."+n, ids.Node, n)
		if count := st.Visits[n]; count < 1 || count > published.MaxInt {
			add("visits."+n, fmt.Sprintf("is not a visit count, from 1 to %d", published.MaxInt))
		}
	}
	if len(errs) > 0 {
		return errs
	}
	data, err := json.Marshal(shortestNumbers(st))
	switch {
	case err != nil:
		return input.FieldErrors{{Field: name, Message: "is no save state: " + err.Error()}}
	case len(data) > MaxStateBytes:
		return input.FieldErrors{{Field: name, Message: fmt.Sprintf("takes %d bytes as JSON, more than the %d "+
			"that a play state's state may take", len(data), MaxStateBytes)}}
	}
	return nil
}

// checkName says what is wrong with a play state's name, or returns "".
func checkName(name string) string {
	return input.CheckTrimmedName(name, MaxNameLength)
}

// anyPlayState returns the play state of the batch's project whose ID is id,
// whether or not it is in the trash, or refuses the batch if the project has
// none.
func anyPlayState(ctx context.Context, tx *commands.Tx, id string) (queries.PlayState, error) {
	s, err := queries.New(tx).PlayStateAny(ctx, queries.PlayStateAnyParams{WorkspaceID: nullUUID(tx.WorkspaceID()),
		ProjectID: nullUUID(tx.ProjectID()), ID: decoded(ids.PlayState, id)})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return queries.PlayState{}, commands.Refuse("id", "is not a play state of the project", nil)
	case err != nil:
		return queries.PlayState{}, fmt.Errorf("playstate: read the play state: %w", err)
	}
	return s, nil
}

// livePlayState returns the play state of the batch's project whose ID is id
// as anyPlayState does, or refuses the batch if it is in the trash, where
// nothing but restoring it changes it.
func livePlayState(ctx context.Context, tx *commands.Tx, id string) (queries.PlayState, error) {
	s, err := anyPlayState(ctx, tx, id)
	switch {
	case err != nil:
		return queries.PlayState{}, err
	case s.DeletedAt.Valid:
		return queries.PlayState{}, commands.Refuse("id", "is a play state in the project's trash, which only "+
			"restoring it changes", nil)
	}
	return s, nil
}

// changed returns a function that reports the change of the play state id,
// to the revision that an update returned, with err, or reports nothing if
// the update found nothing to change, which it returns as pgx.ErrNoRows.
func changed(tx *commands.Tx, id pgtype.UUID) func(rev int64, err error) error {
	return func(rev int64, err error) error {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		case err != nil:
			return fmt.Errorf("playstate: change the play state: %w", err)
		}
		return tx.Changed(ids.PlayState, id.Bytes, rev)
	}
}

// by returns the batch's actor, as the column of who changed a row.
func by(tx *commands.Tx) pgtype.UUID {
	return nullUUID(tx.Actor().UserID)
}

// decoded returns the UUID of id, an ID of the kind that a payload's Check
// checked, as a uuid column.
func decoded(kind ids.Kind, id string) pgtype.UUID {
	// Check refused a payload whose ID does not decode.
	u, _ := ids.Decode(kind, id)
	return nullUUID(u)
}

// newID returns the problem of id, the ID of the play state that a create
// makes, unless it is a play state's ID of a UUIDv7 (PRD §5.2).
func newID(id string) input.FieldErrors {
	u, err := ids.Decode(ids.PlayState, id)
	switch {
	case err != nil:
		return field("id", fmt.Sprintf("is not a %s's ID: %v", ids.PlayState, err))
	case !u.IsV7():
		return field("id", "is not the ID of a UUIDv7, which the ID of every entity is")
	}
	return nil
}

// entityID returns the problem of the payload's id, unless it is a play
// state's ID.
func entityID(id string) input.FieldErrors {
	if _, err := ids.Decode(ids.PlayState, id); err != nil {
		return field("id", fmt.Sprintf("is not a %s's ID: %v", ids.PlayState, err))
	}
	return nil
}

// field returns the problem msg of the payload's field name, or none if msg
// is "".
func field(name, msg string) input.FieldErrors {
	if msg == "" {
		return nil
	}
	return input.FieldErrors{{Field: name, Message: msg}}
}

// uuids returns entities as uuid columns.
func uuids(entities []ids.UUID) []pgtype.UUID {
	out := make([]pgtype.UUID, len(entities))
	for i, id := range entities {
		out[i] = nullUUID(id)
	}
	return out
}
