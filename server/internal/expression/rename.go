package expression

import (
	"bytes"
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

// siteRewrite is an expression of a project whose source a rename rewrites
// (planRename), as its preview lists it (Preview): its UUID, its owner and
// its field, the node of its site, as a usage lists it (package dependency),
// whether the owner is outside the project's trash, as a usage list shows it,
// its source and the source rewritten, and the edits of its names that make
// one of the other (expr.Edit).
type siteRewrite struct {
	Expression        ids.UUID
	Owner             Owner
	Field             Field
	Node              ids.UUID
	Live              bool
	Source, Rewritten string
	Edits             []expr.Edit
}

// renaming is what a rename sees of its project (newRenaming): the
// expressions that it may rewrite, in the order of their boards, the nodes of
// their sites, and their UUIDs, and what they can name before the rename and
// after it.
type renaming struct {
	rows          []queries.NamingExpressionsRow
	before, after projects
	renamed       string
}

// newRenaming returns what the rename r sees of the project, of the workspace
// ws, read in q's transaction, or nil for a rename that rewrites nothing, as
// one to the key that the entity has, or one that no expression can name. The
// expressions that it reads are those that the batch has yet to compile whose
// sources hold r's key, or, for a variable's, its new key, those whose names
// (expression_name) hold r's UUID or one of those keys, as only an expression
// whose source names one of them can be rewritten, and those among listed,
// whose sources the undo of a rename gives back (planWrites). Each is
// compiled where it is, at its owner's board, one of a board in the trash
// with the board's own variables (projects). It returns an error if r's
// entity is not one of the project's, outside its trash, with r's key, or if
// another of its scope has the new key, which the caller checks first.
func newRenaming(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, r Rename, listed []pgtype.UUID) (
	*renaming, error) {
	if r.From == r.To {
		return nil, nil
	}
	keys := []string{r.From}
	if r.Kind == ids.Variable {
		keys = append(keys, r.To)
	}
	rows, err := q.NamingExpressions(ctx, queries.NamingExpressionsParams{WorkspaceID: ws, ProjectID: project,
		BoardID: nullUUID(r.Board), Refs: []pgtype.UUID{nullUUID(r.ID)}, Keys: keys, Ids: list(listed)})
	if err != nil {
		return nil, fmt.Errorf("expression: read the expressions that may name what a rename renames: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	// The rows come in the order of their keys, which Postgres compares by
	// their bytes, as compareKeys does; they are sorted all the same, so that
	// a preview may stop at the end of its page.
	slices.SortFunc(rows, func(a, b queries.NamingExpressionsRow) int {
		return compareKeys(rowKey(a), rowKey(b))
	})
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
	g := &renaming{rows: rows, renamed: ids.Encode(r.Kind, r.ID)}
	if g.before, err = before.build(); err != nil {
		return nil, err
	}
	if g.after, err = after.build(); err != nil {
		return nil, err
	}
	return g, nil
}

// rowKey returns where the expression row is in the lists of a rename's
// preview (PreviewKey).
func rowKey(row queries.NamingExpressionsRow) PreviewKey {
	return PreviewKey{Board: row.BoardID.Bytes, Node: row.SiteNodeID.Bytes, Expression: row.ID.Bytes}
}

// rewrite returns the source of the expression row as the rename rewrites it,
// with its edits, none if the rename leaves it as it is: each name of the
// renamed entity, and each name of another entity that the new key would take
// its name from, as a bare key from a global variable that a variable of the
// board renamed to the global's key takes, written as expr.Rewrite says
// (FR-VAR-03), so that the source names what it named once the rename
// applies. A source that does not parse is left as it is.
func (g *renaming) rewrite(row queries.NamingExpressionsRow) (string, []expr.Edit, error) {
	source, edits, err := expr.Rewrite(row.Source, expr.Context(row.Context), g.before.of(row.BoardID),
		g.after.of(row.BoardID), ids.Encode(ids.Board, row.BoardID.Bytes), g.renamed)
	if err != nil {
		return "", nil, fmt.Errorf("expression: rewrite the expression %s: %w", ids.UUID(row.ID.Bytes), err)
	}
	return source, edits, nil
}

// restores reports whether the source, which the undo of a rename gives back
// to the expression row, is one that the rename that the undo undoes would
// have rewritten to the source that row has now: the rename back, of the
// project after this one to the project before it, so that the source names
// and does exactly what row does, with the names that it gives back as their
// author wrote them.
func (g *renaming) restores(row queries.NamingExpressionsRow, source string) (bool, error) {
	forward, _, err := expr.Rewrite(source, expr.Context(row.Context), g.after.of(row.BoardID),
		g.before.of(row.BoardID), ids.Encode(ids.Board, row.BoardID.Bytes), g.renamed)
	if err != nil {
		return false, fmt.Errorf("expression: rewrite the source given back to the expression %s: %w",
			ids.UUID(row.ID.Bytes), err)
	}
	return forward == row.Source, nil
}

// planRename calls visit with each expression of the project, of the
// workspace ws, in its trash or not, that the rename r rewrites, read in q's
// transaction, with its source as the rename rewrites it (renaming.rewrite),
// in the order of their boards, the nodes of their sites, and their UUIDs
// (PreviewKey), after the key after, until visit returns false: it rewrites
// none of those up to after, nor any once visit has returned false. A rename
// to the key that the entity has rewrites nothing. It returns an error as
// newRenaming does.
func planRename(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, r Rename, after PreviewKey,
	visit func(siteRewrite) bool) error {
	g, err := newRenaming(ctx, q, ws, project, r, nil)
	if err != nil || g == nil {
		return err
	}
	for _, row := range g.rows {
		if compareKeys(rowKey(row), after) <= 0 {
			continue
		}
		source, edits, err := g.rewrite(row)
		if err != nil {
			return err
		}
		if len(edits) == 0 {
			continue
		}
		if !visit(siteRewrite{Expression: row.ID.Bytes, Owner: namingOwner(row), Field: Field(row.Field),
			Node: row.SiteNodeID.Bytes, Live: row.Live, Source: row.Source, Rewritten: source, Edits: edits}) {
			return nil
		}
	}
	return nil
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

// Source is the source of an expression of a project, by its owner and its
// field, as the undo of a rename gives it back, rather than rewrite the
// source anew, so that the undo gives the source back exactly as it was,
// however its author wrote its names (FR-VAR-03 AC1).
type Source struct {
	Owner  string `json:"owner" doc:"The ID of the expression's owner: a node's, a connection's, or a branch output's"`
	Field  string `json:"field" enum:"on_enter,on_exit,condition,on_traverse" doc:"The owner's field that holds the expression: a node's on_enter or on_exit, a connection's condition or on_traverse, or a branch output's condition"`
	Source string `json:"source" maxLength:"16384" doc:"The expression's source, of at most 16384 characters"`
}

// sourceWrite is a source that a rename writes (planWrites): the
// expression's UUID, its owner and its field, its source as it is, and the
// source that the rename gives it.
type sourceWrite struct {
	expression ids.UUID
	owner      Owner
	field      Field
	source, to string
}

// carriedWrites is what the inverse of a rename keeps for its Apply
// (commands.Tx.Carry): the rename, the sources that its payload gives, and
// what it writes.
type carriedWrites struct {
	r       Rename
	sources *[]Source
	writes  []sourceWrite
}

// planWrites returns the sources that the rename r writes, read in q's
// transaction of the project of the workspace ws, with the sources that its
// payload gives, the field sources, or nil for none, a rename's own:
//
//   - of each owner's field that sources lists, which CheckSources checked,
//     the source that it lists, if the field's expression is one that the
//     rename undone would have rewritten to the source that it has now
//     (renaming.restores), as the undo of a rename gives back each source
//     that the rename rewrote exactly as it was;
//   - of each other expression that the rename rewrites (renaming.rewrite),
//     the source rewritten, as of one that names the renamed entity by its
//     new key, written since the rename that its undo undoes, and of one that
//     sources lists whose source has changed since that rename, as when its
//     owner was in the trash and has been restored and edited since, which
//     the undo leaves naming what it names, rather than write back over it;
//   - and of an owner's field that sources lists but that has no expression
//     now, nothing, as of one whose owner the trash has purged since.
//
// It refuses the batch, at the payload's field at, the field of the new key,
// if a source that the rename rewrites would have more than MaxSourceLength
// characters, before anything is written.
func planWrites(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, at string, r Rename,
	sources *[]Source) ([]sourceWrite, error) {
	listed := map[ids.UUID]string{}
	var listedIDs []pgtype.UUID
	if sources != nil {
		found, err := sourceExpressions(ctx, q, ws, project, *sources)
		if err != nil {
			return nil, err
		}
		for i, s := range *sources {
			if id, ok := found[i]; ok {
				listed[id] = s.Source
				listedIDs = append(listedIDs, nullUUID(id))
			}
		}
	}
	g, err := newRenaming(ctx, q, ws, project, r, listedIDs)
	if err != nil || g == nil {
		return nil, err
	}
	var out []sourceWrite
	for _, row := range g.rows {
		to, ok := "", false
		if source, isListed := listed[row.ID.Bytes]; isListed {
			if ok, err = g.restores(row, source); err != nil {
				return nil, err
			}
			to = source
		}
		if !ok {
			var edits []expr.Edit
			if to, edits, err = g.rewrite(row); err != nil {
				return nil, err
			}
			ok = len(edits) > 0
			if ok && CheckSource(to) != "" {
				return nil, commands.Refuse(at, fmt.Sprintf("would make the %s of %s, which names what it renames, "+
					"longer than %d characters: shorten that expression, or choose a shorter key", row.Field,
					ids.Encode(namingOwner(row).Kind, namingOwner(row).ID.Bytes), MaxSourceLength), nil)
			}
		}
		if !ok || to == row.Source {
			continue
		}
		out = append(out, sourceWrite{expression: row.ID.Bytes, owner: namingOwner(row), field: Field(row.Field),
			source: row.Source, to: to})
	}
	return out, nil
}

// RenameInverse returns the inverse of the rename r, of the type typ, which
// writes the sources that its payload gives, or nil for none, a rename's own
// (planWrites), as a command's Inverse returns it: a step of the type whose
// payload payload makes of the sources that the rename writes, as they are,
// by their owners and fields, which the undo gives back (planWrites), in the
// order of their boards, the nodes of their sites, and their UUIDs. It keeps
// what it planned for the command's Apply (ApplyRename), and stops with
// commands.ErrUndoTooLarge once the payload takes more bytes, as the
// operation log keeps it (commands.LogBytes), than the batch's undo has room
// for (commands.Tx.UndoRoom), which the pipeline refuses the rename for, at
// its UndoField, rather than compute the rest of its inverse only to refuse
// it once it reports its change. A rename that rewrites nothing still
// changes its entity's key, so the refusal is never of a command that
// changes nothing. It refuses the batch, at the payload's field at, as
// planWrites does, before it measures the undo.
func RenameInverse(ctx context.Context, tx *commands.Tx, typ commands.Type, at string, r Rename,
	sources *[]Source, payload func(back []Source) any) ([]commands.Step, error) {
	writes, err := planWrites(ctx, queries.New(tx), nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID()), at, r,
		sources)
	if err != nil {
		return nil, err
	}
	tx.Carry(carriedWrites{r: r, sources: sources, writes: writes})
	back := make([]Source, len(writes))
	for i, w := range writes {
		back[i] = Source{Owner: ids.Encode(w.owner.Kind, w.owner.ID.Bytes), Field: string(w.field), Source: w.source}
	}
	p := payload(back)
	enc, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("expression: encode the undo: %w", err)
	}
	if commands.LogBytes(enc) > tx.UndoRoom() {
		return nil, commands.ErrUndoTooLarge
	}
	return []commands.Step{{Type: typ, Payload: p}}, nil
}

// ApplyRename writes the sources that the rename r writes, with the sources
// that its payload gives, or nil for none (planWrites), as the command of the
// rename does in its Apply, before it changes the entity's key, which it
// checks first, so that the pipeline compiles them once every command of the
// batch has applied, and reports the change of each of their owners, which
// the command touches, but of an owner deleted to the trash itself, which
// nothing changes meanwhile (touchOwners), whose expressions are rewritten
// all the same, so that its restoration brings back what names the renamed
// entity. It writes what the command's inverse planned (RenameInverse), or
// plans it itself, for a batch that cannot be undone, and refuses the batch
// as planWrites does, before it writes anything.
func ApplyRename(ctx context.Context, tx *commands.Tx, at string, r Rename, sources *[]Source) error {
	c, ok := tx.Carried().(carriedWrites)
	writes := c.writes
	if !ok || c.r != r || c.sources != sources {
		var err error
		if writes, err = planWrites(ctx, queries.New(tx), nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID()), at,
			r, sources); err != nil {
			return err
		}
	}
	list := make([]idSource, len(writes))
	for i, w := range writes {
		list[i] = idSource{ID: ids.UUID(w.expression).String(), Source: w.to}
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
		kind, u, ok := sourceOwner(s.Owner)
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

// sourceExpressions returns the UUIDs of the project's expressions, in its
// trash or not, of the owners' fields that sources lists, which CheckSources
// checked, by their indexes in sources, without the indexes of those that
// the project has no expression at.
func sourceExpressions(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, sources []Source) (
	map[int]ids.UUID, error) {
	type ownerField struct {
		owner ids.UUID
		field string
	}
	owners := map[ids.Kind][]pgtype.UUID{}
	for _, s := range sources {
		kind, u, _ := sourceOwner(s.Owner)
		owners[kind] = append(owners[kind], nullUUID(u))
	}
	stored := map[ownerField]ids.UUID{}
	for _, kind := range ownerKinds {
		if len(owners[kind]) == 0 {
			continue
		}
		rows, err := ownerExpressions(ctx, q, ws, project, kind, owners[kind])
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			stored[ownerField{ownerOf(row), row.Field}] = row.ID.Bytes
		}
	}
	out := map[int]ids.UUID{}
	for i, s := range sources {
		_, u, _ := sourceOwner(s.Owner)
		if id, ok := stored[ownerField{u, s.Field}]; ok {
			out[i] = id
		}
	}
	return out, nil
}

// compareKeys orders the keys of the expressions that a rename may rewrite,
// as its preview lists them, by their boards, their nodes, and their
// expressions, by their bytes.
func compareKeys(a, b PreviewKey) int {
	if c := bytes.Compare(a.Board[:], b.Board[:]); c != 0 {
		return c
	}
	if c := bytes.Compare(a.Node[:], b.Node[:]); c != 0 {
		return c
	}
	return bytes.Compare(a.Expression[:], b.Expression[:])
}
