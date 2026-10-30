package expression

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
)

// Rename is a change of the key of a variable or a component of a project,
// by which its expressions name it (PRD FR-VAR-03, FR-COMP-02 AC2): Kind is
// ids.Variable or ids.Component, ID the entity's UUID, Board the board of a
// board's variable, zero for a global variable and a component, and From and
// To its key and its new key.
type Rename struct {
	Kind     ids.Kind
	ID       ids.UUID
	Board    ids.UUID
	From, To string
}

// Rewrite is an expression of a project whose source a rename rewrites
// (PlanRename): its UUID, its owner and its field, the node of its site, as
// a usage lists it (package dependency), whether the owner is outside the
// project's trash, as a usage list shows it, its source and the source
// rewritten, and the edits of its names that make one of the other
// (expr.Edit).
type Rewrite struct {
	Expression        ids.UUID
	Owner             Owner
	Field             Field
	Node              ids.UUID
	Live              bool
	Source, Rewritten string
	Edits             []expr.Edit
}

// PlanRename returns the expressions of the project, of the workspace ws,
// in its trash or not, that the rename r rewrites, read in q's transaction, in
// the order of their boards, the nodes of their sites, and their UUIDs: each
// whose source, compiled where its expression is, at its owner's board, as
// one of a board in the trash is compiled with the board's own variables
// (projects), names the renamed entity, or names another that the new key
// would take its name from, as a bare key from a global variable that a
// variable of the board renamed to the global's key takes, and which the
// rename writes as expr.Rewrite says (FR-VAR-03), so that it names what it
// named once the rename applies, and an expression that does not compile for
// any other reason than its names is rewritten too. A rename to the key that
// the entity has rewrites nothing. The expressions that it reads are those
// that the batch has yet to compile, whose sources commands of the batch
// wrote, and those whose names (expression_name) hold r's UUID, its key, or,
// for a variable's, its new key: only an expression whose source names one of
// them can be rewritten. It returns an error if r's entity is not one of the
// project's, outside its trash, with r's key, or if another of its scope has
// the new key, which the caller checks first.
func PlanRename(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, r Rename) ([]Rewrite, error) {
	if r.From == r.To {
		return nil, nil
	}
	keys := []string{r.From}
	if r.Kind == ids.Variable {
		keys = append(keys, r.To)
	}
	rows, err := q.NamingExpressions(ctx, queries.NamingExpressionsParams{WorkspaceID: ws, ProjectID: project,
		BoardID: nullUUID(r.Board), Refs: []pgtype.UUID{nullUUID(r.ID)}, Keys: keys})
	if err != nil {
		return nil, fmt.Errorf("expression: read the expressions that may name what a rename renames: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	var trashed []pgtype.UUID
	for _, row := range rows {
		if row.BoardDeleted && !slices.Contains(trashed, row.BoardID) {
			trashed = append(trashed, row.BoardID)
		}
	}
	before, err := loadNames(ctx, q, ws, project, nil, trashed)
	if err != nil {
		return nil, err
	}
	after, err := before.renamed(r)
	if err != nil {
		return nil, err
	}
	b, err := before.build()
	if err != nil {
		return nil, err
	}
	a, err := after.build()
	if err != nil {
		return nil, err
	}
	renamed := ids.Encode(r.Kind, r.ID)
	var out []Rewrite
	for _, row := range rows {
		source, edits, err := expr.Rewrite(row.Source, expr.Context(row.Context), b.of(row.BoardID),
			a.of(row.BoardID), ids.Encode(ids.Board, row.BoardID.Bytes), renamed)
		if err != nil {
			return nil, fmt.Errorf("expression: rewrite the expression %s: %w", ids.UUID(row.ID.Bytes), err)
		}
		if len(edits) == 0 {
			continue
		}
		out = append(out, Rewrite{Expression: row.ID.Bytes, Owner: namingOwner(row), Field: Field(row.Field),
			Node: row.SiteNodeID.Bytes, Live: row.Live, Source: row.Source, Rewritten: source, Edits: edits})
	}
	return out, nil
}

// renamed returns the names n with the key of the entity that r renames
// changed to r's new key, or an error if n lacks the entity, as the project
// lacks it outside its trash with r's key.
func (n names) renamed(r Rename) (names, error) {
	out := n
	id := ids.Encode(r.Kind, r.ID)
	found := false
	switch r.Kind {
	case ids.Variable:
		out.variables = slices.Clone(n.variables)
		for i, v := range out.variables {
			if v.ID == id && v.Key == r.From {
				out.variables[i].Key, found = r.To, true
			}
		}
	case ids.Component:
		out.components = slices.Clone(n.components)
		for i, c := range out.components {
			if c.ID == id && c.Key == r.From {
				out.components[i].Key, found = r.To, true
			}
		}
	}
	if !found {
		return names{}, fmt.Errorf("expression: the project has no %s %s of the key %s outside its trash to rename",
			r.Kind, id, r.From)
	}
	return out, nil
}

// namingOwner returns the owner of the expression row.
func namingOwner(row queries.NamingExpressionsRow) Owner {
	switch {
	case row.NodeID.Valid:
		return Owner{Kind: ids.Node, ID: row.NodeID, Board: row.BoardID}
	case row.ConnectionID.Valid:
		return Owner{Kind: ids.Connection, ID: row.ConnectionID, Board: row.BoardID}
	}
	return Owner{Kind: ids.BranchOutput, ID: row.BranchOutputID, Board: row.BoardID}
}

// WriteRewrites writes the rewritten sources of rewrites, which PlanRename
// returned in the batch's transaction, as the command of the rename does it
// in its Apply, so that the pipeline compiles them once every command of the
// batch has applied, and reports the change of each of their owners, which
// the command touches, but of an owner deleted to the trash itself, which
// nothing changes meanwhile (touchOwners), whose expressions are rewritten
// all the same, so that its restoration brings back what names the renamed
// entity. It refuses the batch, at the payload's field at, the field of the
// new key, if a rewritten source has more than MaxSourceLength characters,
// before it writes anything.
func WriteRewrites(ctx context.Context, tx *commands.Tx, at string, rewrites []Rewrite) error {
	list := make([]idSource, len(rewrites))
	for i, rw := range rewrites {
		if CheckSource(rw.Rewritten) != "" {
			return commands.Refuse(at, fmt.Sprintf("would make the %s of %s, which names what it renames, longer "+
				"than %d characters: shorten that expression, or choose a shorter key", rw.Field,
				ids.Encode(rw.Owner.Kind, rw.Owner.ID.Bytes), MaxSourceLength), nil)
		}
		list[i] = idSource{ID: ids.UUID(rw.Expression).String(), Source: rw.Rewritten}
	}
	return writeSources(ctx, tx, list)
}

// idSource is an expression's UUID, in its text form, and a source to write
// it, as SetExpressionSources reads them.
type idSource struct {
	ID     string `json:"id"`
	Source string `json:"source"`
}

// writeSources writes the sources of list, as a command's Apply does, and
// reports the change of each owner of an expression whose source it changes,
// but of an owner deleted to the trash itself (touchOwners).
func writeSources(ctx context.Context, tx *commands.Tx, list []idSource) error {
	if len(list) == 0 {
		return nil
	}
	if err := leave(tx); err != nil {
		return err
	}
	data, err := json.Marshal(list)
	if err != nil {
		return fmt.Errorf("expression: encode the sources: %w", err)
	}
	rows, err := queries.New(tx).SetExpressionSources(ctx, queries.SetExpressionSourcesParams{
		WorkspaceID: nullUUID(tx.WorkspaceID()), ProjectID: nullUUID(tx.ProjectID()), Rows: data})
	if err != nil {
		return fmt.Errorf("expression: write the sources: %w", err)
	}
	touched := map[ids.Kind][]pgtype.UUID{}
	seen := map[pgtype.UUID]bool{}
	for _, row := range rows {
		kind, id := ids.BranchOutput, row.BranchOutputID
		switch {
		case row.NodeID.Valid:
			kind, id = ids.Node, row.NodeID
		case row.ConnectionID.Valid:
			kind, id = ids.Connection, row.ConnectionID
		}
		if !seen[id] {
			seen[id] = true
			touched[kind] = append(touched[kind], id)
		}
	}
	return touch(ctx, tx, touched)
}

// Source is the source of an expression of a project, by its owner and its
// field, as the undo of a rename writes it back, rather than rewrite the
// sources anew, so that the undo gives back the sources exactly as they were,
// however their authors wrote their names (FR-VAR-03 AC1).
type Source struct {
	Owner  string `json:"owner" doc:"The ID of the expression's owner: a node's, a connection's, or a branch output's"`
	Field  string `json:"field" enum:"on_enter,on_exit,condition,on_traverse" doc:"The owner's field that holds the expression: a node's on_enter or on_exit, a connection's condition or on_traverse, or a branch output's condition"`
	Source string `json:"source" maxLength:"16384" doc:"The expression's source, of at most 16384 characters"`
}

// Sources returns the sources of rewrites before the rename, by their owners
// and fields, as the undo of the rename writes them back.
func Sources(rewrites []Rewrite) []Source {
	out := make([]Source, len(rewrites))
	for i, rw := range rewrites {
		out[i] = Source{Owner: ids.Encode(rw.Owner.Kind, rw.Owner.ID.Bytes), Field: string(rw.Field),
			Source: rw.Source}
	}
	return out
}

// FitUndo returns commands.ErrUndoTooLarge if the payload of a step of an
// inverse that holds sources, which grow with the project, takes more bytes,
// as the operation log keeps it (commands.LogBytes), than the batch's undo
// has room for (commands.Tx.UndoRoom), as the inverse of a rename that
// rewrites expressions returns it, which the pipeline then refuses the
// rename for, at its UndoField, rather than compute the rest of its inverse
// only to refuse it once it reports its change. A rename that rewrites
// nothing still changes its entity's key, so the refusal is never of a
// command that changes nothing.
func FitUndo(tx *commands.Tx, payload any) error {
	enc, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("expression: encode the undo: %w", err)
	}
	if commands.LogBytes(enc) > tx.UndoRoom() {
		return commands.ErrUndoTooLarge
	}
	return nil
}

// CheckSources returns the problems of sources, the field at of a payload:
// an owner that is not a node's, a connection's, or a branch output's ID, a
// field that its kind has no expression at, a source longer than
// MaxSourceLength, which the schema's maxLength says too, and an owner's
// field listed twice.
func CheckSources(at string, sources []Source) input.FieldErrors {
	var errs input.FieldErrors
	seen := map[string]bool{}
	for i, s := range sources {
		where := fmt.Sprintf("%s[%d]", at, i)
		kind, _, ok := sourceOwner(s.Owner)
		switch {
		case !ok:
			errs = append(errs, input.FieldError{Field: where + ".owner",
				Message: "is not the ID of a node, a connection, or a branch output"})
			continue
		case fieldOf(kind, Field(s.Field)) != nil:
			errs = append(errs, input.FieldError{Field: where + ".field",
				Message: fmt.Sprintf("is no field of a %s that holds an expression", kind)})
			continue
		}
		if problem := CheckSource(s.Source); problem != "" {
			errs = append(errs, input.FieldError{Field: where + ".source", Message: problem})
		}
		_, u, _ := sourceOwner(s.Owner)
		if seen[u.String()+" "+s.Field] {
			errs = append(errs, input.FieldError{Field: where, Message: "lists an owner's field that the list holds " +
				"already"})
		}
		seen[u.String()+" "+s.Field] = true
	}
	return errs
}

// sourceOwner returns the kind and the UUID of the owner of an expression
// whose ID is id, a node's, a connection's, or a branch output's, and
// whether it is one.
func sourceOwner(id string) (ids.Kind, ids.UUID, bool) {
	for _, kind := range ownerKinds {
		if u, err := ids.Decode(kind, id); err == nil {
			return kind, u, true
		}
	}
	return 0, ids.UUID{}, false
}

// WriteSources writes back sources, which CheckSources checked, the field at
// of the payload of the undo of a rename, to the project's expressions of
// their owners' fields, as WriteRewrites writes a rename's, and refuses the
// batch at the source of an owner's field that the project has no expression
// at, in its trash or not, before it writes anything.
func WriteSources(ctx context.Context, tx *commands.Tx, at string, sources []Source) error {
	rows, err := sourceExpressions(ctx, queries.New(tx), nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID()), at,
		sources)
	if err != nil {
		return err
	}
	list := make([]idSource, len(sources))
	for i, s := range sources {
		list[i] = idSource{ID: ids.UUID(rows[i].ID.Bytes).String(), Source: s.Source}
	}
	return writeSources(ctx, tx, list)
}

// CurrentSources returns the sources that the project's expressions of the
// owners' fields that sources lists, which CheckSources checked, have now, as
// the inverse of a command that writes sources back sets them back to, or
// refuses the batch as WriteSources does.
func CurrentSources(ctx context.Context, tx *commands.Tx, at string, sources []Source) ([]Source, error) {
	rows, err := sourceExpressions(ctx, queries.New(tx), nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID()), at,
		sources)
	if err != nil {
		return nil, err
	}
	out := make([]Source, len(sources))
	for i, s := range sources {
		out[i] = Source{Owner: s.Owner, Field: s.Field, Source: rows[i].Source}
	}
	return out, nil
}

// sourceExpressions returns the project's expressions of the owners' fields
// that sources lists, which CheckSources checked, at their indexes, or
// refuses the batch, at the payload's field at, at the first that the
// project has no expression at.
func sourceExpressions(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, at string,
	sources []Source) ([]queries.Expression, error) {
	type ownerField struct {
		owner ids.UUID
		field string
	}
	owners := map[ids.Kind][]pgtype.UUID{}
	for _, s := range sources {
		kind, u, _ := sourceOwner(s.Owner)
		owners[kind] = append(owners[kind], nullUUID(u))
	}
	stored := map[ownerField]queries.Expression{}
	for _, kind := range ownerKinds {
		if len(owners[kind]) == 0 {
			continue
		}
		rows, err := ownerExpressions(ctx, q, ws, project, kind, owners[kind])
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			stored[ownerField{ownerOf(row), row.Field}] = row
		}
	}
	out := make([]queries.Expression, len(sources))
	for i, s := range sources {
		_, u, _ := sourceOwner(s.Owner)
		row, ok := stored[ownerField{u, s.Field}]
		if !ok {
			return nil, commands.Refuse(fmt.Sprintf("%s[%d]", at, i), "is an owner's field that the project has no "+
				"expression at", nil)
		}
		out[i] = row
	}
	return out, nil
}
