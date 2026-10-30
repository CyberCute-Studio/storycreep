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

// siteRewrite is an expression of a project whose source a rename rewrites
// (planRename), as its preview lists it (Preview): its UUID, its owner and
// its field, the node of its site, as a usage lists it (package dependency),
// whether the owner is outside the project's trash, as a usage list shows it,
// its source and the source rewritten, the edits of its names that make one
// of the other (expr.Edit), and whether the source rewritten is longer than
// MaxSourceLength, which refuses the rename (planWrites).
type siteRewrite struct {
	Expression        ids.UUID
	Owner             Owner
	Field             Field
	Node              ids.UUID
	Live              bool
	Source, Rewritten string
	Edits             []expr.Edit
	TooLong           bool
}

// renaming is what a rename sees of its project (newRenaming): what it reads
// the expressions that it may rewrite by (read), the renamed entity's ID, and,
// once read has found one, what they can name before the rename and after it,
// of the boards in the trash of those that it has read among them.
type renaming struct {
	q             *queries.Queries
	params        queries.NamingExpressionsParams
	r             Rename
	renamed       string
	loaded        bool
	before, after projects
}

// newRenaming returns what the rename r sees of the project, of the workspace
// ws, read in q's transaction (read), or nil for a rename to the key that the
// entity has, which rewrites nothing. The expressions that it reads are
// those that the batch has yet to compile whose sources hold r's key or its
// new key, those whose names (expression_name) hold r's UUID or one of those
// keys, as only an expression whose source names one of them can be
// rewritten, or be made to name the entity (expr.Captures), and those among
// listed, whose sources the undo of a rename gives back (planWrites).
func newRenaming(q *queries.Queries, ws, project pgtype.UUID, r Rename, listed []pgtype.UUID) *renaming {
	if r.From == r.To {
		return nil
	}
	return &renaming{q: q, r: r, renamed: ids.Encode(r.Kind, r.ID), params: queries.NamingExpressionsParams{
		WorkspaceID: ws, ProjectID: project, BoardID: nullUUID(r.Board), Refs: []pgtype.UUID{nullUUID(r.ID)},
		Keys: []string{r.From, r.To}, Ids: list(listed)}}
}

// read returns the expressions that the rename may rewrite, in the order of
// their keys (PreviewKey), which Postgres compares by their bytes, after the
// key after, the zero key to read from the first, and at most limit of
// them, or every one if limit is 0, so that a preview's page reads its own
// alone. Each is compiled where it is, at its owner's board, one of a board
// in the trash with the board's own variables (projects), which read reads
// once it finds one. It returns an error if the rename's entity is not one of
// the project's, outside its trash, with its key, which the caller checks
// first.
func (g *renaming) read(ctx context.Context, after PreviewKey, limit int) ([]queries.NamingExpressionsRow, error) {
	p := g.params
	p.AfterBoard, p.AfterNode, p.AfterID = keyUUID(after.Board), keyUUID(after.Node), keyUUID(after.Expression)
	if limit > 0 {
		p.MaxRows = pgtype.Int8{Int64: int64(limit), Valid: true}
	}
	rows, err := g.q.NamingExpressions(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("expression: read the expressions that may name what a rename renames: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	if !g.loaded {
		before, err := loadNames(ctx, g.q, p.WorkspaceID, p.ProjectID, nil, nil)
		if err != nil {
			return nil, err
		}
		after, err := before.renamed(g.r)
		if err != nil {
			return nil, err
		}
		if g.before, err = before.build(); err != nil {
			return nil, err
		}
		if g.after, err = after.build(); err != nil {
			return nil, err
		}
		g.loaded = true
	}
	var trashed []pgtype.UUID
	for _, row := range rows {
		if _, ok := g.before.trashed[row.BoardID]; row.BoardDeleted && !ok && !slices.Contains(trashed, row.BoardID) {
			trashed = append(trashed, row.BoardID)
		}
	}
	if len(trashed) == 0 {
		return rows, nil
	}
	// A board in the trash's own variables are the same before the rename and
	// after it, which renames an entity outside the trash.
	variables, err := trashedVariables(ctx, g.q, p.WorkspaceID, p.ProjectID, trashed)
	if err != nil {
		return nil, err
	}
	for _, b := range trashed {
		board := ids.Encode(ids.Board, b.Bytes)
		if g.before.trashed[b], err = g.before.live.With(board, variables[b], nil); err == nil {
			g.after.trashed[b], err = g.after.live.With(board, variables[b], nil)
		}
		if err != nil {
			return nil, fmt.Errorf("expression: what the expressions of the board %s in the trash can name: %w",
				ids.UUID(b.Bytes), err)
		}
	}
	return rows, nil
}

// rowKey returns where the expression row is in the lists of a rename's
// preview (PreviewKey).
func rowKey(row queries.NamingExpressionsRow) PreviewKey {
	return PreviewKey{Board: row.BoardID.Bytes, Node: row.SiteNodeID.Bytes, Expression: row.ID.Bytes}
}

// keyUUID returns id as a uuid column that is never null, the zero UUID's
// too, as a key of a list of a rename's preview holds it (NamingExpressions).
func keyUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
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

// captures reports whether the rename makes the source of the expression row,
// which it leaves as it is, name something where a name of it named nothing
// (expr.Captures): the renamed entity, or one whose name its old key
// shadowed.
func (g *renaming) captures(row queries.NamingExpressionsRow) (bool, error) {
	captured, err := expr.Captures(row.Source, expr.Context(row.Context), g.before.of(row.BoardID),
		g.after.of(row.BoardID), ids.Encode(ids.Board, row.BoardID.Bytes))
	if err != nil {
		return false, fmt.Errorf("expression: read what the rename makes the expression %s name: %w",
			ids.UUID(row.ID.Bytes), err)
	}
	return captured, nil
}

// planRename calls visit with each expression of the project, of the
// workspace ws, in its trash or not, that the rename r rewrites, read in q's
// transaction, with its source as the rename rewrites it (renaming.rewrite),
// in the order of their boards, the nodes of their sites, and their UUIDs
// (PreviewKey), after the key after, until visit returns false. It reads the
// expressions from after on, chunk of them, and twice as many at each read
// after, or all at once if chunk is 0, and none once visit has returned
// false, so that a preview's page reads those up to its end alone, but for
// the rest of the read that it ends in. Each read costs what every
// expression that may name the entity costs (NamingExpressions), however few
// it returns, so that the reads double: a page whose sites are few among
// many expressions, as those that a rename qualifies are, takes a few reads,
// not one for each chunk of them. A rename to the key that the entity has
// rewrites nothing. It returns an error as renaming.read does.
func planRename(ctx context.Context, q *queries.Queries, ws, project pgtype.UUID, r Rename, after PreviewKey,
	chunk int, visit func(siteRewrite) bool) error {
	g := newRenaming(q, ws, project, r, nil)
	if g == nil {
		return nil
	}
	for {
		rows, err := g.read(ctx, after, chunk)
		if err != nil {
			return err
		}
		for _, row := range rows {
			source, edits, err := g.rewrite(row)
			if err != nil {
				return err
			}
			if len(edits) == 0 {
				continue
			}
			if !visit(siteRewrite{Expression: row.ID.Bytes, Owner: namingOwner(row), Field: Field(row.Field),
				Node: row.SiteNodeID.Bytes, Live: row.Live, Source: row.Source, Rewritten: source, Edits: edits,
				TooLong: CheckSource(source) != ""}) {
				return nil
			}
		}
		if chunk <= 0 || len(rows) < chunk {
			return nil
		}
		after, chunk = rowKey(rows[len(rows)-1]), 2*chunk
	}
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
	kind, id := ownerRef(row.NodeID, row.ConnectionID, row.BranchOutputID)
	return Owner{Kind: kind, ID: id, Board: row.BoardID}
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

// sourceWrite is an expression whose source a rename writes, or whose source
// its undo gives back though the rename leaves it as it is (planWrites): the
// expression's UUID, its owner and its field, its source as it is, and the
// source that the rename gives it, the same for one that it leaves.
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
// payload gives, the field sources, or nil for none, a rename's own, and
// those that its undo gives back though it leaves them as they are. These
// are every case of an expression that the rename may rewrite (newRenaming):
//
//   - of an owner's field that sources lists, which CheckSources checked, and
//     whose expression is one that the rename undone would have rewritten to
//     the source that it has now (renaming.restores), the source that it
//     lists, as the undo of a rename gives back each source that the rename
//     rewrote exactly as it was. A listed source that is the expression's
//     source as it is keeps it as it is, which leaves it naming nothing
//     where it names the renamed entity now, as the undo of a rename leaves
//     an expression that the rename made name the entity without rewriting
//     it (the last case): a source that restores holds this way names no
//     other entity once the rename applies, so a made-up one can make an
//     expression name nothing, as a command that sets its source can, but
//     never name what it did not;
//   - of every other expression that the rename rewrites (renaming.rewrite),
//     the source rewritten, as of one that names the renamed entity by its
//     new key, written since the rename that its undo undoes, and of one that
//     sources lists whose source has changed since that rename, as when its
//     owner was in the trash and has been restored and edited since, which
//     the undo leaves naming what it names, rather than write back over it;
//   - of an owner's field that sources lists but that has no expression now,
//     nothing, as of one whose owner the trash has purged since;
//   - and of every other expression that the rename leaves as it is, but
//     makes name the renamed entity, where a name of it named nothing
//     (renaming.captures), the source as it is, which the rename does not
//     write, but its undo lists, so that the undo leaves it naming nothing
//     again, rather than rewrite it as one written since.
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
	g := newRenaming(q, ws, project, r, listedIDs)
	if g == nil {
		return nil, nil
	}
	rows, err := g.read(ctx, PreviewKey{}, 0)
	if err != nil {
		return nil, err
	}
	var out []sourceWrite
	for _, row := range rows {
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
				o := namingOwner(row)
				// An expression in the trash, which the rename rewrites too, so
				// that its restoration brings back what names the entity, is
				// shortened once it is restored.
				fix := "shorten that expression"
				if !row.Live {
					fix = "that expression is in the project's trash, which the rename rewrites too: restore it to " +
						"shorten it"
				}
				return nil, commands.Refuse(at, fmt.Sprintf("would make the %s of %s, which names what it renames, "+
					"longer than %d characters: %s, or choose a shorter key", row.Field, ids.Encode(o.Kind, o.ID.Bytes),
					MaxSourceLength, fix), nil)
			}
		}
		w := sourceWrite{expression: row.ID.Bytes, owner: namingOwner(row), field: Field(row.Field),
			source: row.Source, to: to}
		switch {
		case ok && to != row.Source:
			out = append(out, w)
		case !ok:
			// A source that the rename leaves as it is, which its undo gives
			// back if the rename makes it name what it did not.
			captured, err := g.captures(row)
			if err != nil {
				return nil, err
			}
			if captured {
				w.to = row.Source
				out = append(out, w)
			}
		}
	}
	return out, nil
}

// RenameInverse returns the inverse of the rename r, of the type typ, which
// writes the sources that its payload gives, or nil for none, a rename's own
// (planWrites), as a command's Inverse returns it: a step of the type whose
// payload payload makes of r's key and of the sources that the rename writes,
// and of those that it leaves as they are but makes name what they did not,
// as they are, by their owners and fields, which the undo gives back
// (planWrites), in the order of their boards, the nodes of their sites, and
// their UUIDs. It keeps what it planned for the command's Apply
// (ApplyRename), and stops with commands.ErrUndoTooLarge once the batch's
// undo has no room (commands.Tx.UndoRoom) for either of two undos, which the
// pipeline refuses the rename for, at its UndoField, rather than compute the
// rest of its inverse only to refuse it once it reports its change:
//
//   - the undo, its payload as the operation log keeps it (commands.LogBytes),
//     whose revisions the pipeline counts as the rename reports its changes;
//   - and the undo's own undo, which renames to r's new key again: its
//     payload, of the sources as the rename writes them, and the revisions
//     that it expects (commands.ExpectedRevBytes), of the entity and of each
//     owner whose source the undo writes back, counted whether or not that
//     owner is in the trash itself, which the undo does not touch, so that it
//     is never less than the undo's undo takes.
//
// So a rename that makes its sources longer, as to a longer key, is refused
// rather than applied with an undo that could never be undone, which, as one
// command, no smaller batch sends apart. A rename that rewrites nothing still
// changes its entity's key, so the refusal is never of a command that changes
// nothing. It refuses the batch, at the payload's field at, as planWrites
// does, before it measures the undo.
func RenameInverse(ctx context.Context, tx *commands.Tx, typ commands.Type, at string, r Rename,
	sources *[]Source, payload func(key string, sources []Source) any) ([]commands.Step, error) {
	writes, err := planWrites(ctx, queries.New(tx), nullUUID(tx.WorkspaceID()), nullUUID(tx.ProjectID()), at, r,
		sources)
	if err != nil {
		return nil, err
	}
	tx.Carry(carriedWrites{r: r, sources: sources, writes: writes})
	back := make([]Source, len(writes))
	redo := []Source{}
	// expects is what the revisions that the undo's own undo expects take.
	expects := commands.ExpectedRevBytes(commands.Ref{Kind: r.Kind, ID: r.ID})
	owners := map[commands.Ref]bool{}
	for i, w := range writes {
		back[i] = Source{Owner: ids.Encode(w.owner.Kind, w.owner.ID.Bytes), Field: string(w.field), Source: w.source}
		if w.to == w.source {
			continue
		}
		redo = append(redo, Source{Owner: back[i].Owner, Field: back[i].Field, Source: w.to})
		if o := (commands.Ref{Kind: w.owner.Kind, ID: w.owner.ID.Bytes}); !owners[o] {
			owners[o] = true
			expects += commands.ExpectedRevBytes(o)
		}
	}
	p := payload(r.From, back)
	for _, u := range []struct {
		payload any
		expects int
	}{{p, 0}, {payload(r.To, redo), expects}} {
		enc, err := json.Marshal(u.payload)
		if err != nil {
			return nil, fmt.Errorf("expression: encode the undo: %w", err)
		}
		if commands.LogBytes(enc)+u.expects > tx.UndoRoom() {
			return nil, commands.ErrUndoTooLarge
		}
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
	var list []idSource
	for _, w := range writes {
		if w.to != w.source {
			list = append(list, idSource{ID: ids.UUID(w.expression).String(), Source: w.to})
		}
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
		kind, id := ownerRef(row.NodeID, row.ConnectionID, row.BranchOutputID)
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
	kind, u, err := ids.DecodeAny(id)
	if err != nil || !slices.Contains(ownerKinds, kind) {
		return 0, ids.UUID{}, false
	}
	return kind, u, true
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
	out := map[int]ids.UUID{}
	if len(sources) == 0 {
		return out, nil
	}
	params := queries.OwnerExpressionsParams{WorkspaceID: ws, ProjectID: project, NodeIds: []pgtype.UUID{},
		ConnectionIds: []pgtype.UUID{}, OutputIds: []pgtype.UUID{}}
	byKind := map[ids.Kind]*[]pgtype.UUID{ids.Node: &params.NodeIds, ids.Connection: &params.ConnectionIds,
		ids.BranchOutput: &params.OutputIds}
	for _, s := range sources {
		kind, u, _ := sourceOwner(s.Owner)
		*byKind[kind] = append(*byKind[kind], nullUUID(u))
	}
	rows, err := q.OwnerExpressions(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("expression: read the expressions of the sources' owners: %w", err)
	}
	stored := map[ownerField]ids.UUID{}
	for _, row := range rows {
		stored[ownerField{ownerOf(row), row.Field}] = row.ID.Bytes
	}
	for i, s := range sources {
		_, u, _ := sourceOwner(s.Owner)
		if id, ok := stored[ownerField{u, s.Field}]; ok {
			out[i] = id
		}
	}
	return out, nil
}
