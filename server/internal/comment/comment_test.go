package comment_test

import (
	"context"
	"encoding/json"
	"errors"
	"go/build"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/comment"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/notification"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/trash"
)

func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

// body is the body of the comments that the tests add, as the commands
// store it.
const body = `{"type": "doc", "content": [{"type": "paragraph", "content": [{"type": "text", "text": "Lit?"}]}]}`

// TestCommentIsolation tries the package's queries from another workspace's
// transaction, where row-level security must keep them from reading, adding,
// or changing its comments (NFR-SEC-01): each of them, the restore once the
// comments are in the trash, and the opening of a thread once the threads
// are resolved, since each changes none that is not.
//
// Verifies: NFR-SEC-01
func TestCommentIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := isolationtest.New(t, dbtest.New(t), isolationtest.Tables)
	uuid := func(id ids.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }
	params := func(target *isolationtest.Workspace) (pgtype.UUID, pgtype.UUID, pgtype.UUID) {
		return uuid(target.ID), uuid(target.Rows["project"]), uuid(target.Rows["comment"])
	}
	one := func(id pgtype.UUID, err error) ([]ids.UUID, error) {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return []ids.UUID{id.Bytes}, err
	}
	for _, op := range []isolationtest.Op{
		{Name: "a comment, deleted or not", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			ws, p, id := params(target)
			row, err := queries.New(tx).CommentAny(ctx, queries.CommentAnyParams{WorkspaceID: ws, ProjectID: p, ID: id})
			return one(row.Comment.ID, err)
		}},
		{Name: "the target of a thread", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			ws, p, _ := params(target)
			row, err := queries.New(tx).CommentTarget(ctx, queries.CommentTargetParams{WorkspaceID: ws, ProjectID: p,
				NodeID: uuid(target.Rows["node"])})
			if err != nil || !row.Found {
				return nil, err
			}
			return []ids.UUID{target.Rows["node"]}, nil
		}},
		{Name: "comments by their IDs", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			ws, p, id := params(target)
			rows, err := queries.New(tx).CommentsByID(ctx, queries.CommentsByIDParams{WorkspaceID: ws, ProjectID: p,
				Ids: []pgtype.UUID{id}})
			var found []ids.UUID
			for _, r := range rows {
				found = append(found, r.ID.Bytes)
			}
			return found, err
		}},
		{Name: "a project's comments", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			ws, p, _ := params(target)
			rows, err := queries.New(tx).Comments(ctx, queries.CommentsParams{WorkspaceID: ws, ProjectID: p,
				AfterThread: uuid(ids.UUID{}), AfterID: uuid(ids.UUID{}), MaxRows: 100})
			var found []ids.UUID
			for _, r := range rows {
				found = append(found, r.ID.Bytes)
			}
			return found, err
		}},
		{Name: "the counts of a project's threads", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			ws, p, _ := params(target)
			rows, err := queries.New(tx).CommentCounts(ctx, queries.CommentCountsParams{WorkspaceID: ws, ProjectID: p,
				AfterID: uuid(ids.UUID{}), MaxRows: 100})
			var found []ids.UUID
			for _, r := range rows {
				found = append(found, r.TargetID.Bytes)
			}
			return found, err
		}},
		// The authors are users, which belong to no workspace, so the comment
		// of the author that the workspace's comment names stands for it.
		{Name: "the authors of a project's comments", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			ws, p, id := params(target)
			rows, err := queries.New(tx).CommentAuthors(ctx, queries.CommentAuthorsParams{WorkspaceID: ws,
				ProjectID: p, MaxRows: 100})
			var found []ids.UUID
			for _, r := range rows {
				if r.ID.Bytes == target.UserID {
					found = append(found, id.Bytes)
				}
			}
			return found, err
		}},
		{Name: "the revisions of comments", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			ws, p, id := params(target)
			rows, err := queries.New(tx).CommentRevs(ctx, queries.CommentRevsParams{WorkspaceID: ws, ProjectID: p,
				Ids: []pgtype.UUID{id}})
			var found []ids.UUID
			for _, r := range rows {
				found = append(found, r.ID.Bytes)
			}
			return found, err
		}},
		{Name: "add a comment", Denied: true, Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			ws, p, _ := params(target)
			id := ids.New()
			err := queries.New(tx).InsertComment(ctx, queries.InsertCommentParams{ID: uuid(id), WorkspaceID: ws,
				ProjectID: p, NodeID: uuid(target.Rows["node"]), Body: []byte(body)})
			return []ids.UUID{id}, err
		}},
		{Name: "edit a comment", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			ws, p, id := params(target)
			_, err := queries.New(tx).EditComment(ctx, queries.EditCommentParams{Body: []byte(`{"type": "doc"}`),
				WorkspaceID: ws, ProjectID: p, ID: id})
			return one(id, err)
		}},
		{Name: "resolve a thread", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			ws, p, id := params(target)
			_, err := queries.New(tx).ResolveComment(ctx, queries.ResolveCommentParams{WorkspaceID: ws, ProjectID: p,
				ID: id})
			return one(id, err)
		}},
		{Name: "delete a comment to the trash", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			ws, p, id := params(target)
			_, err := queries.New(tx).TrashComment(ctx, queries.TrashCommentParams{WorkspaceID: ws, ProjectID: p,
				ID: id})
			return one(id, err)
		}},
	} {
		f.Check(t, op)
	}

	// The opening of the threads once they are resolved, and the restore of
	// the comments once they are in the trash.
	for _, w := range []*isolationtest.Workspace{f.A, f.B} {
		if _, err := f.Admin().Exec(ctx, "UPDATE comment SET resolved_at = now(), deleted_at = now() WHERE id = $1",
			w.Rows["comment"]); err != nil {
			t.Fatal(err)
		}
	}
	for _, op := range []isolationtest.Op{
		{Name: "open a thread again", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			ws, p, id := params(target)
			_, err := queries.New(tx).UnresolveComment(ctx, queries.UnresolveCommentParams{WorkspaceID: ws,
				ProjectID: p, ID: id})
			return one(id, err)
		}},
		{Name: "restore a comment from the trash", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			ws, p, id := params(target)
			_, err := queries.New(tx).RestoreComment(ctx, queries.RestoreCommentParams{WorkspaceID: ws, ProjectID: p,
				ID: id})
			return one(id, err)
		}},
	} {
		f.Check(t, op)
	}
}

// TestCommentPurge checks the trash's purge of a project's comments: a
// comment that has been in the trash for 30 days is deleted, but for the
// first comment of a thread of which a reply is outside the trash, or has
// been in it for less, which its thread shows as deleted meanwhile, and whose
// purge, once due, deletes its replies with it; a comment deleted for less
// than 30 days, and one that is not deleted, stay.
func TestCommentPurge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := isolationtest.New(t, dbtest.New(t), isolationtest.Tables)
	admin := f.Admin()
	a := f.A
	// add adds a comment of A's project, the first of a thread on its node
	// if parent is zero, deleted days ago, or not if days is negative.
	add := func(parent ids.UUID, days int) ids.UUID {
		t.Helper()
		id := ids.New()
		node := any(a.Rows["node"])
		var p any
		if parent != (ids.UUID{}) {
			node, p = nil, parent
		}
		if _, err := admin.Exec(ctx, `INSERT INTO comment (id, workspace_id, project_id, parent_id, node_id, body,
			created_by, deleted_at) VALUES ($1, $2, $3, $4, $5, $6, $7,
			CASE WHEN $8::integer >= 0 THEN now() - make_interval(days => $8::integer) END)`, id, a.ID,
			a.Rows["project"], p, node, body, a.UserID, days); err != nil {
			t.Fatal(err)
		}
		return id
	}
	kept := add(ids.UUID{}, 31)
	keptReply := add(kept, -1)
	late := add(ids.UUID{}, 31)
	lateReply := add(late, 10)
	gone := add(ids.UUID{}, 31)
	goneReply := add(gone, 31)
	live := add(ids.UUID{}, -1)
	goneAlone := add(live, 31)
	recent := add(ids.UUID{}, 10)
	if _, err := trash.Purge(ctx, f.DB); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[ids.UUID]bool{kept: true, keptReply: true, late: true, lateReply: true, gone: false,
		goneReply: false, live: true, goneAlone: false, recent: true, a.Rows["comment"]: true} {
		var n int
		if err := admin.QueryRow(ctx, "SELECT count(*) FROM comment WHERE id = $1", id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if (n == 1) != want {
			t.Errorf("after the purge, the comment %s is there %d times, want it kept %v", id, n, want)
		}
	}
}

// TestTargetEnums checks the enums of a target's kind that the API declares,
// of the comments' and of the notifications', against the kinds of target, in
// their order, and the kinds of IDs that the notifications encode a target's
// with against the comments'.
func TestTargetEnums(t *testing.T) {
	t.Parallel()
	var kinds []string
	for _, k := range comment.TargetTypes() {
		kinds = append(kinds, string(k))
		if comment.TargetOf(k.Kind()) != k {
			t.Errorf("the kind of target %s is of IDs of the kind %s, whose kind of target is %q", k, k.Kind(),
				comment.TargetOf(k.Kind()))
		}
	}
	for _, v := range []any{comment.CommentBody{}, comment.CommentCount{}, notification.NotificationBody{}} {
		f, ok := reflect.TypeOf(v).FieldByName("TargetType")
		if got := strings.Split(f.Tag.Get("enum"), ","); !ok || !slices.Equal(got, kinds) {
			t.Errorf("the enum of %T's target_type is %q, want %q", v, got, kinds)
		}
	}
	// The notifications name a thread's target by its kind's IDs as the
	// comments do.
	for _, k := range comment.TargetTypes() {
		if got := notification.TargetKind(string(k)); got != k.Kind() {
			t.Errorf("a notification's target of the kind %s has IDs of the kind %s, want %s", k, got, k.Kind())
		}
	}
}

// TestPublishedFormatHoldsNoComment checks that the published format and the
// save state hold no comment (PRD FR-COMMENT-01 AC3): no type of package
// published, of which a snapshot's files and a save state are written, is a
// comment's, or has a field that is, by its name or its JSON's; no schema of
// their files, which the unions' variants are in too, has a property or a
// definition that is; and package published depends on no package that
// holds the comments, so that what it writes of a project holds none,
// whatever the project's threads say. The play mode's projection, which
// writes a snapshot of the draft in the web app, is checked by its own test.
//
// Verifies: FR-COMMENT-01/AC3
func TestPublishedFormatHoldsNoComment(t *testing.T) {
	t.Parallel()
	commentish := func(name string) bool { return strings.Contains(strings.ToLower(name), "comment") }

	// The types that the documents' roots reach, by reflection.
	seen := map[reflect.Type]bool{}
	var walk func(typ reflect.Type, at string)
	walk = func(typ reflect.Type, at string) {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array ||
			typ.Kind() == reflect.Map {
			if typ.Kind() == reflect.Map {
				walk(typ.Key(), at)
			}
			typ = typ.Elem()
		}
		if seen[typ] {
			return
		}
		seen[typ] = true
		if commentish(typ.Name()) {
			t.Errorf("%s is of the type %s, a comment's", at, typ)
		}
		if typ.Kind() != reflect.Struct {
			return
		}
		for i := range typ.NumField() {
			f := typ.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if commentish(f.Name) || commentish(name) {
				t.Errorf("%s has the field %s (%q), a comment's", at, f.Name, name)
			}
			walk(f.Type, at+"."+f.Name)
		}
	}
	docs := published.Documents()
	for _, d := range docs {
		walk(d.Root, d.Path)
	}
	if len(seen) < 20 {
		t.Errorf("the walk of the published types found %d types", len(seen))
	}

	// The properties and the definitions of the documents' schemas.
	files, err := published.Files(false)
	if err != nil {
		t.Fatal(err)
	}
	var names func(v any, at string)
	names = func(v any, at string) {
		switch v := v.(type) {
		case map[string]any:
			for k, child := range v {
				if commentish(k) {
					t.Errorf("%s has the key %q, a comment's", at, k)
				}
				if k == "properties" || k == "$defs" {
					if m, ok := child.(map[string]any); ok {
						for name := range m {
							if commentish(name) {
								t.Errorf("%s/%s names %q, a comment's", at, k, name)
							}
						}
					}
				}
				names(child, at+"/"+k)
			}
		case []any:
			for _, child := range v {
				names(child, at)
			}
		}
	}
	if len(files) != len(docs) {
		t.Errorf("the formats have %d files, want one of each of the %d documents", len(files), len(docs))
	}
	for path, data := range files {
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		names(v, path)
	}

	// The module's packages that package published depends on, read from
	// their directories, without their tests.
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	const module = "github.com/CyberCute-Studio/storycreep/server"
	deps := map[string]bool{}
	var depend func(path string)
	depend = func(path string) {
		if deps[path] || path != module && !strings.HasPrefix(path, module+"/") {
			return
		}
		deps[path] = true
		pkg, err := build.ImportDir(filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(path, module))), 0)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for _, imp := range pkg.Imports {
			depend(imp)
		}
	}
	depend(module + "/internal/published")
	for _, forbidden := range []string{"/internal/comment", "/internal/database/queries"} {
		if deps[module+forbidden] {
			t.Errorf("package published depends on %s", module+forbidden)
		}
	}
	if len(deps) < 3 {
		t.Errorf("package published depends on %d of the module's packages, %v, which the walk did not read",
			len(deps), deps)
	}
}
