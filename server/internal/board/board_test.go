package board_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/slug"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
)

func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

// fixture is a database of the test's own with a workspace whose owner is
// owner, a project of it, and the pipeline of the projects' and the boards'
// commands.
type fixture struct {
	d        *dbtest.Database
	db       *database.DB
	ws       ids.UUID
	owner    ids.UUID
	project  ids.UUID
	pipeline *commands.Pipeline
	projects *project.Service
	boards   *board.Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	d := dbtest.New(t)
	f := &fixture{d: d, db: d.Open(t), ws: ids.New()}
	admin := d.Admin(t)
	var err error
	if f.owner, err = isolationtest.NewUser(ctx, admin); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Harbour', $2, 'unlimited')`,
			[]any{f.ws, "harbour-" + f.ws.String()}},
		{`INSERT INTO membership (id, workspace_id, user_id, role) VALUES ($1, $2, $3, 'owner')`,
			[]any{ids.New(), f.ws, f.owner}},
	} {
		if _, err := admin.Exec(ctx, c.sql, c.args...); err != nil {
			t.Fatal(err)
		}
	}
	reg := commands.NewRegistry()
	project.Register(reg)
	board.Register(reg)
	ent := entitlements.New(entitlements.Options{Meter: usage.Meter{}})
	f.pipeline = commands.New(f.db, commands.Options{Registry: reg, Entitlements: ent})
	f.projects = project.New(f.db, project.Options{Entitlements: ent, Pipeline: f.pipeline})
	f.boards = board.New(f.db, board.Options{Projects: f.projects})
	p, err := f.projects.Create(ctx, f.owner, f.ws, project.Settings{Name: "Harbour"}, audit.Client{})
	if err != nil {
		t.Fatal(err)
	}
	if f.project, err = ids.Decode(ids.Project, p.ID); err != nil {
		t.Fatal(err)
	}
	return f
}

// actor is the owner, as the actor of a batch.
func (f *fixture) actor() commands.Actor {
	return commands.Actor{UserID: f.owner}
}

// cmd is a command of a batch: its type and its payload.
type cmd struct {
	typ     commands.Type
	payload any
}

// apply applies the commands to the fixture's project as one batch, as the
// owner.
func (f *fixture) apply(t *testing.T, cmds ...cmd) (commands.Result, error) {
	t.Helper()
	batch := make([]commands.Command, len(cmds))
	for i, c := range cmds {
		raw, err := json.Marshal(c.payload)
		if err != nil {
			t.Fatal(err)
		}
		batch[i] = commands.Command{ID: ids.New(), Type: c.typ, Payload: raw}
	}
	return f.pipeline.Apply(context.Background(), f.actor(), f.project, batch)
}

// must applies the commands as apply does, and fails the test unless they
// apply.
func (f *fixture) must(t *testing.T, cmds ...cmd) commands.Result {
	t.Helper()
	res, err := f.apply(t, cmds...)
	if err != nil {
		t.Fatalf("%v: %v", cmds, err)
	}
	return res
}

// undo applies the inverse of the batch whose seq is seq, which the
// operation log keeps.
func (f *fixture) undo(t *testing.T, seq int64) {
	t.Helper()
	var steps []struct {
		Type    commands.Type   `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	var inverse []byte
	if err := f.d.Admin(t).QueryRow(context.Background(),
		"SELECT inverse FROM operation WHERE project_id = $1 AND seq = $2", f.project, seq).Scan(&inverse); err != nil ||
		json.Unmarshal(inverse, &steps) != nil {
		t.Fatalf("the inverse of %d, %s: %v", seq, inverse, err)
	}
	batch := make([]commands.Command, len(steps))
	for i, st := range steps {
		batch[i] = commands.Command{ID: ids.New(), Type: st.Type, Payload: st.Payload}
	}
	if len(batch) == 0 {
		return
	}
	if _, err := f.pipeline.Apply(context.Background(), f.actor(), f.project, batch); err != nil {
		t.Fatalf("undo %d (%s): %v", seq, inverse, err)
	}
}

// state reads every board and folder of the fixture's project as the
// superuser, the deleted ones too, but for their revisions and timestamps:
// what a change and its undo must leave alike.
func (f *fixture) state(t *testing.T) string {
	t.Helper()
	return f.read(t, true)
}

// live reads the boards and folders of the fixture's project as state does,
// but for the deleted ones: what a creation and its undo, which deletes what
// was created, must leave alike.
func (f *fixture) live(t *testing.T) string {
	t.Helper()
	return f.read(t, false)
}

// read reads the boards and folders of the fixture's project as state does,
// the deleted ones too if deleted is set.
func (f *fixture) read(t *testing.T, deleted bool) string {
	t.Helper()
	var s string
	if err := f.d.Admin(t).QueryRow(context.Background(), `SELECT concat_ws(' / ',
		(SELECT string_agg(concat_ws('|', id, folder_id, "order", name, slug, description, color,
			deleted_at IS NOT NULL), ', ' ORDER BY id) FROM board
			WHERE project_id = $1 AND ($2 OR deleted_at IS NULL)),
		(SELECT string_agg(concat_ws('|', id, kind, parent_id, name, "order", deleted_at IS NOT NULL), ', '
			ORDER BY id) FROM folder WHERE project_id = $1 AND ($2 OR deleted_at IS NULL)))`, f.project,
		deleted).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// boardID and folderID return new IDs of a board and of a folder.
func boardID() string  { return ids.Encode(ids.Board, ids.New()) }
func folderID() string { return ids.Encode(ids.Folder, ids.New()) }

// get returns the board id of the fixture's project, or fails the test.
func (f *fixture) get(t *testing.T, id string) board.Board {
	t.Helper()
	u, err := ids.Decode(ids.Board, id)
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.boards.Get(context.Background(), f.actor(), f.project, u)
	if err != nil {
		t.Fatalf("the board %s: %v", id, err)
	}
	return b
}

// create creates a board named name, with payload's other fields, and
// returns it.
func (f *fixture) create(t *testing.T, name string, payload map[string]any) board.Board {
	t.Helper()
	id := boardID()
	p := map[string]any{"id": id, "name": name}
	for k, v := range payload {
		p[k] = v
	}
	f.must(t, cmd{board.Create, p})
	return f.get(t, id)
}

// refused returns the refusal of the batch that err refused, or fails the
// test if err is not one.
func refused(t *testing.T, err error) *commands.RefusedError {
	t.Helper()
	var r *commands.RefusedError
	if !errors.As(err, &r) {
		t.Fatalf("the batch was not refused by a command: %v", err)
	}
	return r
}

// TestDerivedSlugs checks that boards created without a slug get their
// names' slugs, the first that no board of the project has, in its trash or
// not, as name, name-2, and so on, the same in every project, and that a
// slug changes neither when its board is renamed nor when it is deleted and
// restored.
//
// Verifies: FR-PROJ-02/AC1
func TestDerivedSlugs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var first []string
	for range 2 {
		f := newFixture(t)
		var got []string
		var made []board.Board
		for range 3 {
			b := f.create(t, "Elara — First Meeting", nil)
			made = append(made, b)
			got = append(got, b.Slug)
		}
		if want := []string{"elara-first-meeting", "elara-first-meeting-2", "elara-first-meeting-3"}; !slices.Equal(got,
			want) {
			t.Errorf("the slugs are %v, want %v", got, want)
		}
		if first == nil {
			first = got
		} else if !slices.Equal(got, first) {
			t.Errorf("another project's slugs are %v, the first's %v", got, first)
		}
		// A rename keeps the slug.
		f.must(t, cmd{board.Rename, map[string]any{"id": made[0].ID, "name": "Harbour Intro"}})
		if b := f.get(t, made[0].ID); b.Name != "Harbour Intro" || b.Slug != "elara-first-meeting" {
			t.Errorf("the renamed board is %q, %q", b.Name, b.Slug)
		}
		// A board in the trash keeps its slug, which a new board does not take,
		// and has it once it is restored.
		f.must(t, cmd{board.Delete, map[string]any{"id": made[1].ID}})
		if b := f.create(t, "Elara: first meeting", nil); b.Slug != "elara-first-meeting-4" {
			t.Errorf("a new board's slug is %q while the trash holds elara-first-meeting-2", b.Slug)
		}
		f.must(t, cmd{board.Restore, map[string]any{"id": made[1].ID}})
		if b := f.get(t, made[1].ID); b.Slug != "elara-first-meeting-2" {
			t.Errorf("the restored board's slug is %q", b.Slug)
		}
		// A slug freed by a change of slug is taken by the next board that
		// derives it.
		f.must(t, cmd{board.SetSlug, map[string]any{"id": made[2].ID, "slug": "elara-later"}})
		if b := f.create(t, "Elara First Meeting", nil); b.Slug != "elara-first-meeting-3" {
			t.Errorf("a new board's slug is %q, want the freed elara-first-meeting-3", b.Slug)
		}
		list, err := f.boards.List(ctx, f.actor(), f.project, ids.UUID{}, 10)
		if err != nil || len(list) != 5 {
			t.Errorf("the project lists %d boards, %v; want 5", len(list), err)
		}
	}
}

// TestSlugRules checks the slugs that a board is given and may be given: a
// derived slug is file-name safe on every platform, a name that Windows
// reserves for a device becomes con-board and so on, a name without letters
// in Latin script becomes board, and a typed slug that is not one, or that
// another board has, is refused as such.
//
// Verifies: FR-PROJ-02/AC4
func TestSlugRules(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for name, want := range map[string]string{
		"CON":                            "con-board",
		"com1":                           "com1-board",
		"Příliš žluťoučký kůň":           "prilis-zlutoucky-kun",
		"最初の出会い":                         "board",
		"  -- Harbour -- Intro ":         "harbour-intro",
		strings.Repeat("Lighthouse ", 9): strings.TrimRight(strings.Repeat("lighthouse-", 6)[:64], "-"),
	} {
		trimmed := strings.TrimSpace(name)
		b := f.create(t, trimmed, nil)
		if b.Slug != want {
			t.Errorf("a board named %q has the slug %q, want %q", trimmed, b.Slug, want)
		}
		if err := slug.Check(b.Slug); err != nil {
			t.Errorf("the derived slug %q is not one: %v", b.Slug, err)
		}
	}
	// A long name's slug, which a board has already, is cut to leave room for
	// its number.
	long := strings.TrimRight(strings.Repeat("lighthouse-", 6)[:64], "-")
	if b := f.create(t, strings.TrimSpace(strings.Repeat("Lighthouse ", 9)), nil); b.Slug != long[:62]+"-2" {
		t.Errorf("a second board of a long name has the slug %q, want %q", b.Slug, long[:62]+"-2")
	}
	taken := f.create(t, "Harbourmaster", map[string]any{"slug": "harbourmaster"})
	for typed, reason := range map[string]slug.Reason{
		"nul": slug.Reserved, "lpt9": slug.Reserved, "Harbour": slug.Format, "harbour_intro": slug.Format,
		"harbour--intro": slug.Format, "-harbour": slug.Format, "harbour.json": slug.Format,
		strings.Repeat("a", 65): slug.Format,
	} {
		for _, c := range []cmd{
			{board.Create, map[string]any{"id": boardID(), "name": "Pier", "slug": typed}},
			{board.SetSlug, map[string]any{"id": taken.ID, "slug": typed}},
		} {
			_, err := f.apply(t, c)
			r := refused(t, err)
			var e *slug.Error
			if !errors.As(r.Reason, &e) || e.Reason != reason || r.Field != "slug" {
				t.Errorf("%s with the slug %q was refused for %v at %q, want %s", c.typ, typed, r.Reason, r.Field,
					reason)
			}
		}
	}
	for _, c := range []cmd{
		{board.Create, map[string]any{"id": boardID(), "name": "Pier", "slug": "harbourmaster"}},
		{board.SetSlug, map[string]any{"id": f.create(t, "Pier", nil).ID, "slug": "harbourmaster"}},
	} {
		_, err := f.apply(t, c)
		if r := refused(t, err); !errors.Is(r.Reason, board.ErrSlugTaken) || r.Field != "slug" {
			t.Errorf("%s with another board's slug was refused for %v at %q", c.typ, r.Reason, r.Field)
		}
	}
	// A board may keep its own slug, which changes nothing.
	if res := f.must(t, cmd{board.SetSlug, map[string]any{"id": taken.ID, "slug": "harbourmaster"}}); len(
		res.Revisions) != 0 {
		t.Errorf("setting a board's own slug changed %v", res.Revisions)
	}
}

// TestManyBoardsInDeepFolders checks the documented supported scale: a
// project with 100 boards, in a tree of folders nested 6 deep, all of which
// the API lists, page by page, each in its folder.
//
// Verifies: FR-PROJ-02/AC3
func TestManyBoardsInDeepFolders(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	var chain []string
	var batch []cmd
	parent := ""
	for i := range 6 {
		id := folderID()
		p := map[string]any{"id": id, "kind": "board", "name": fmt.Sprintf("Act %d", i+1)}
		if parent != "" {
			p["parent_id"] = parent
		}
		batch = append(batch, cmd{board.CreateFolder, p})
		chain, parent = append(chain, id), id
	}
	f.must(t, batch...)
	made := map[string]string{}
	batch = nil
	for i := range 100 {
		id, folder := boardID(), chain[i%len(chain)]
		made[id] = folder
		batch = append(batch, cmd{board.Create, map[string]any{"id": id, "name": "Scene", "folder_id": folder}})
	}
	f.must(t, batch...)
	seen, slugs := map[string]bool{}, map[string]bool{}
	var after ids.UUID
	for {
		page, err := f.boards.List(ctx, f.actor(), f.project, after, 30)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range page {
			if made[b.ID] != b.FolderID {
				t.Errorf("the board %s is in %q, want %q", b.ID, b.FolderID, made[b.ID])
			}
			seen[b.ID], slugs[b.Slug] = true, true
		}
		if len(page) < 30 {
			break
		}
		after, _ = ids.Decode(ids.Board, page[len(page)-1].ID)
	}
	if len(seen) != 100 || len(slugs) != 100 {
		t.Errorf("the project lists %d boards with %d slugs, want 100 of each", len(seen), len(slugs))
	}
	folders, err := f.boards.Folders(ctx, f.actor(), f.project, board.KindBoard, ids.UUID{}, 200)
	if err != nil || len(folders) != 6 {
		t.Fatalf("the project lists %d folders, %v; want 6", len(folders), err)
	}
	parents := map[string]string{}
	for _, fl := range folders {
		parents[fl.ID] = fl.ParentID
	}
	depth := 0
	for id := chain[5]; id != "" && depth <= len(chain); id = parents[id] {
		depth++
	}
	if depth != 6 {
		t.Errorf("the deepest folder is %d deep, want 6", depth)
	}
}

// TestFolderTree checks what keeps a tree of folders a tree: a folder goes
// only into a folder of its kind, of the project, that is not deleted, and
// never into itself or a folder inside it; a board only into a folder of
// boards; and a folder is deleted only once it holds nothing but what is in
// the trash, which restoring a board in it then restores it for.
func TestFolderTree(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	outer, inner, other := folderID(), folderID(), folderID()
	f.must(t, cmd{board.CreateFolder, map[string]any{"id": outer, "kind": "board", "name": "Act 1"}},
		cmd{board.CreateFolder, map[string]any{"id": inner, "kind": "board", "name": "Harbour", "parent_id": outer}},
		cmd{board.CreateFolder, map[string]any{"id": other, "kind": "variable", "name": "Quests"}})
	for _, c := range []struct {
		cmd   cmd
		field string
	}{
		{cmd{board.MoveFolder, map[string]any{"id": outer, "parent_id": inner}}, "parent_id"},
		{cmd{board.MoveFolder, map[string]any{"id": outer, "parent_id": outer}}, "parent_id"},
		{cmd{board.MoveFolder, map[string]any{"id": other, "parent_id": outer}}, "parent_id"},
		{cmd{board.MoveFolder, map[string]any{"id": folderID()}}, "id"},
		{cmd{board.CreateFolder, map[string]any{"id": folderID(), "kind": "board", "name": "Act 2",
			"parent_id": other}}, "parent_id"},
		{cmd{board.Create, map[string]any{"id": boardID(), "name": "Pier", "folder_id": other}}, "folder_id"},
		{cmd{board.Create, map[string]any{"id": boardID(), "name": "Pier", "folder_id": folderID()}}, "folder_id"},
		{cmd{board.CreateFolder, map[string]any{"id": outer, "kind": "board", "name": "Again"}}, "id"},
		{cmd{board.DeleteFolder, map[string]any{"id": outer}}, "id"},
	} {
		_, err := f.apply(t, c.cmd)
		if r := refused(t, err); r.Field != c.field {
			t.Errorf("%s %v was refused at %q, want %q: %v", c.cmd.typ, c.cmd.payload, r.Field, c.field, r)
		}
	}
	b := f.create(t, "Pier", map[string]any{"folder_id": inner})
	_, err := f.apply(t, cmd{board.DeleteFolder, map[string]any{"id": inner}})
	if r := refused(t, err); !errors.Is(r.Reason, board.ErrFolderNotEmpty) {
		t.Errorf("deleting a folder with a board was refused for %v", r.Reason)
	}
	// A folder that holds only what is in the trash is deleted, and so is
	// the folder above it then.
	f.must(t, cmd{board.Delete, map[string]any{"id": b.ID}}, cmd{board.DeleteFolder, map[string]any{"id": inner}},
		cmd{board.DeleteFolder, map[string]any{"id": outer}})
	for _, c := range []cmd{
		{board.Create, map[string]any{"id": boardID(), "name": "Pier", "folder_id": inner}},
		{board.RenameFolder, map[string]any{"id": inner, "name": "Docks"}},
		{board.Rename, map[string]any{"id": b.ID, "name": "Docks"}},
	} {
		if _, err := f.apply(t, c); refused(t, err).Field == "" {
			t.Errorf("%s of something deleted was refused without a field", c.typ)
		}
	}
	res := f.must(t, cmd{board.Restore, map[string]any{"id": b.ID}})
	if len(res.Revisions) != 3 {
		t.Errorf("restoring the board changed %v, want it and both folders", res.Revisions)
	}
	if got := f.get(t, b.ID); got.FolderID != inner {
		t.Errorf("the restored board is in %q, want %q", got.FolderID, inner)
	}
	folders, err := f.boards.Folders(context.Background(), f.actor(), f.project, "", ids.UUID{}, 10)
	if err != nil || len(folders) != 3 {
		t.Errorf("after the restore, the project lists %d folders, %v; want 3", len(folders), err)
	}
	// The undo of the restore deletes the board and both folders again.
	before := f.state(t)
	f.undo(t, res.Seq)
	f.must(t, cmd{board.Restore, map[string]any{"id": b.ID}})
	if got := f.state(t); got != before {
		t.Errorf("restoring after undoing a restore left %s, want %s", got, before)
	}
}

// TestOrder checks the keys that order a folder's children: a child added
// without one goes after the folder's last, boards and folders alike, and
// after its last that is not deleted once a deleted child's key has no key
// after it, a move to a key places a child between two others, and a move
// without one of the folder's last child into its folder changes nothing.
func TestOrder(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	folder := folderID()
	f.must(t, cmd{board.CreateFolder, map[string]any{"id": folder, "kind": "board", "name": "Act 1"}})
	a, b := f.create(t, "A", nil), f.create(t, "B", nil)
	if a.Order != "W" || b.Order != "X" {
		t.Errorf("the keys after a folder's V are %q and %q, want W and X", a.Order, b.Order)
	}
	f.must(t, cmd{board.Move, map[string]any{"id": b.ID, "order": "VV"}})
	if got := f.get(t, b.ID); got.Order != "VV" || got.FolderID != "" {
		t.Errorf("the moved board is %q in %q", got.Order, got.FolderID)
	}
	f.must(t, cmd{board.Move, map[string]any{"id": a.ID, "folder_id": folder}})
	if got := f.get(t, a.ID); got.Order != "V" || got.FolderID != folder {
		t.Errorf("a board moved into an empty folder is %q in %q, want V there", got.Order, got.FolderID)
	}
	for _, order := range []string{"V0", "", "a-b", strings.Repeat("V", 129)} {
		if _, err := f.apply(t, cmd{board.Move, map[string]any{"id": a.ID, "order": order}}); err == nil {
			t.Errorf("the key %q was taken", order)
		}
	}
	// The folder's last child, moved into the folder without a key, stays
	// where it is, while another goes after it.
	c := f.create(t, "C", map[string]any{"folder_id": folder})
	if res := f.must(t, cmd{board.Move, map[string]any{"id": c.ID, "folder_id": folder}}); len(res.Revisions) != 0 {
		t.Errorf("moving the folder's last child into it changed %v", res.Revisions)
	}
	f.must(t, cmd{board.Move, map[string]any{"id": a.ID, "folder_id": folder}})
	if got := f.get(t, a.ID); c.Order != "W" || got.Order != "X" {
		t.Errorf("the keys of the last child and of the one moved after it are %q and %q, want W and X", c.Order,
			got.Order)
	}
	sub := folderID()
	f.must(t, cmd{board.CreateFolder, map[string]any{"id": sub, "kind": "board", "name": "Scenes",
		"parent_id": folder}})
	if res := f.must(t, cmd{board.MoveFolder, map[string]any{"id": sub, "parent_id": folder}}); len(
		res.Revisions) != 0 {
		t.Errorf("moving the folder's last folder into it changed %v", res.Revisions)
	}
	// A key that has no key after it, which a client may pick, keeps children
	// from being added after it while its child is not deleted, and no longer
	// once it is.
	last := f.create(t, "Last", map[string]any{"folder_id": folder, "order": strings.Repeat("z", 128)})
	_, err := f.apply(t, cmd{board.Create, map[string]any{"id": boardID(), "name": "After", "folder_id": folder}})
	if r := refused(t, err); r.Field != "order" {
		t.Errorf("a board added after a key that has no key after it was refused at %q", r.Field)
	}
	f.must(t, cmd{board.Delete, map[string]any{"id": last.ID}})
	if b := f.create(t, "After", map[string]any{"folder_id": folder}); b.Order != "Z" {
		t.Errorf("a board added after a deleted child whose key has no key after it is at %q, want Z", b.Order)
	}
}

// TestEnumerations checks that the database takes every colour of a board
// and every kind of folder that the package lists, which their schemas take
// too (TestSchemaEnums), and refuses as a board's or a project's slug every
// name that Windows reserves for a device that package slug lists, as the
// commands do (FR-PROJ-02 AC4), so that a name added to one list and not to
// the database's checks fails.
//
// Verifies: FR-PROJ-02/AC4
func TestEnumerations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	for _, c := range board.Colors {
		if b := f.create(t, "Pier", map[string]any{"color": c}); b.Color != c {
			t.Errorf("a board created %s is %s", c, b.Color)
		}
	}
	for _, k := range board.Kinds {
		f.must(t, cmd{board.CreateFolder, map[string]any{"id": folderID(), "kind": k, "name": "Act 1"}})
	}
	b, err := ids.Decode(ids.Board, f.create(t, "Harbour", nil).ID)
	if err != nil {
		t.Fatal(err)
	}
	admin := f.d.Admin(t)
	for _, s := range append(slug.Devices(), "com10") {
		for table, id := range map[string]ids.UUID{"board": b, "project": f.project} {
			_, err := admin.Exec(ctx, "UPDATE "+table+" SET slug = $1 WHERE id = $2", s, id)
			var pg *pgconn.PgError
			switch refused := errors.As(err, &pg) && pg.Code == "23514"; {
			case s == "com10" && err != nil:
				t.Errorf("the slug %s of a %s was refused: %v", s, table, err)
			case s != "com10" && !refused:
				t.Errorf("the slug %s of a %s, a name that Windows reserves for a device, was not refused: %v", s,
					table, err)
			}
		}
	}
}

// TestUndo checks the inverse of every command of a board and a folder:
// applying the inverse that the operation log keeps of a change leaves the
// boards and folders as they were before it, and that of a creation deletes
// what it created, which leaves the others as they were.
func TestUndo(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	act, scenes := folderID(), folderID()
	f.must(t, cmd{board.CreateFolder, map[string]any{"id": act, "kind": "board", "name": "Act 1"}},
		cmd{board.CreateFolder, map[string]any{"id": scenes, "kind": "board", "name": "Scenes"}})
	b := f.create(t, "Pier", map[string]any{"description": "Where Elara waits", "color": "teal"})
	deleted := f.create(t, "Lighthouse", nil)
	f.must(t, cmd{board.Delete, map[string]any{"id": deleted.ID}})
	for _, c := range []cmd{
		{board.Create, map[string]any{"id": boardID(), "name": "Harbour", "folder_id": act, "slug": "harbour"}},
		{board.Rename, map[string]any{"id": b.ID, "name": "Docks"}},
		{board.Describe, map[string]any{"id": b.ID, "description": ""}},
		{board.SetColor, map[string]any{"id": b.ID, "color": "red"}},
		{board.SetSlug, map[string]any{"id": b.ID, "slug": "docks"}},
		{board.Move, map[string]any{"id": b.ID, "folder_id": act, "order": "a"}},
		{board.Delete, map[string]any{"id": b.ID}},
		{board.Restore, map[string]any{"id": deleted.ID}},
		{board.CreateFolder, map[string]any{"id": folderID(), "kind": "component", "name": "Cast"}},
		{board.RenameFolder, map[string]any{"id": act, "name": "Act One"}},
		{board.MoveFolder, map[string]any{"id": scenes, "parent_id": act}},
		{board.DeleteFolder, map[string]any{"id": scenes}},
	} {
		state := f.state
		if c.typ == board.Create || c.typ == board.CreateFolder {
			state = f.live
		}
		before := state(t)
		res := f.must(t, c)
		if state(t) == before {
			t.Errorf("%s %v changed nothing", c.typ, c.payload)
		}
		f.undo(t, res.Seq)
		if got := state(t); got != before {
			t.Errorf("undoing %s left\n%s\nwant\n%s", c.typ, got, before)
		}
	}
	// A deletion of what is in the trash already, and a restoration of what
	// is not, change nothing, and are undone by nothing.
	for _, c := range []cmd{
		{board.Delete, map[string]any{"id": deleted.ID}},
		{board.Restore, map[string]any{"id": b.ID}},
	} {
		if res := f.must(t, c); len(res.Revisions) != 0 {
			t.Errorf("%s changed %v", c.typ, res.Revisions)
		}
	}
}

// TestBoardUsage checks that creating, deleting, and restoring a board add to
// the workspace's boards and take from them, so that its usage is a recount
// of its boards that are not in the trash, to which reconciliation corrects
// it once it is corrupted.
func TestBoardUsage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	a, b := f.create(t, "A", nil), f.create(t, "B", nil)
	f.create(t, "C", nil)
	f.must(t, cmd{board.Delete, map[string]any{"id": a.ID}}, cmd{board.Delete, map[string]any{"id": b.ID}})
	f.must(t, cmd{board.Restore, map[string]any{"id": b.ID}})
	read := func() int64 {
		var used int64
		if err := f.db.InTx(ctx, database.Scope{WorkspaceID: f.ws}, func(tx pgx.Tx) error {
			m, err := usage.Read(ctx, tx, f.ws)
			used = m[usage.Boards]
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return used
	}
	if used := read(); used != 2 {
		t.Errorf("the workspace uses %d boards, want 2", used)
	}
	if _, err := f.d.Admin(t).Exec(ctx, "INSERT INTO usage_delta (id, workspace_id, key, delta) "+
		"VALUES ($1, $2, 'boards', 5)", ids.New(), f.ws); err != nil {
		t.Fatal(err)
	}
	if _, err := usage.Reconcile(ctx, f.db); err != nil {
		t.Fatal(err)
	}
	if used := read(); used != 2 {
		t.Errorf("after reconciliation, the workspace uses %d boards, want 2", used)
	}
}

// TestEntryBoard checks that a project's entry board is one of its boards, in
// its trash or not (N32), so that the undo of a change of the entry from a
// board that was deleted since sets that board back, while an entry that
// keeps a board that no longer exists, whatever the case of its ID's
// letters, is not refused for it.
func TestEntryBoard(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	entry := func(b string) (project.Visible, error) {
		return f.projects.Update(ctx, f.actor(), f.project, project.Patch{Entry: &project.ProjectEntry{BoardID: b}})
	}
	_, err := entry(boardID())
	if r := refused(t, err); r.Field != "entry.board_id" {
		t.Errorf("an entry of a board that the project does not have was refused at %q", r.Field)
	}
	live, trashed := f.create(t, "Harbour", nil), f.create(t, "Pier", nil)
	f.must(t, cmd{board.Delete, map[string]any{"id": trashed.ID}})
	for _, b := range []string{trashed.ID, live.ID} {
		if p, err := entry(b); err != nil || p.Entry.BoardID != b {
			t.Fatalf("an entry of %s is %+v, %v", b, p.Entry, err)
		}
	}
	// The entry's board is deleted, and the entry changed to another board,
	// whose undo sets the deleted board back.
	f.must(t, cmd{board.Delete, map[string]any{"id": live.ID}})
	other := f.create(t, "Lighthouse", nil)
	res := f.must(t, cmd{project.UpdateSettings, map[string]any{"entry": map[string]string{"board_id": other.ID}}})
	f.undo(t, res.Seq)
	if p, err := f.projects.Get(ctx, f.actor(), f.project); err != nil || p.Entry.BoardID != live.ID {
		t.Errorf("after the undo, the entry is %+v, %v; want %s", p.Entry, err, live.ID)
	}
	// An entry that keeps a board that does not exist, as one set before the
	// boards were checked, is not refused for it, whatever the case of the
	// letters of its ID.
	gone := strings.ToLower(boardID())
	if _, err := f.d.Admin(t).Exec(ctx, `UPDATE project SET settings = jsonb_set(settings, '{entry}',
		jsonb_build_object('board_id', $1::text)) WHERE id = $2`, gone, f.project); err != nil {
		t.Fatal(err)
	}
	node := ids.Encode(ids.Node, ids.New())
	if _, err := f.projects.Update(ctx, f.actor(), f.project, project.Patch{
		Entry: &project.ProjectEntry{BoardID: strings.ToUpper(gone), NodeID: node},
	}); err != nil {
		t.Errorf("changing the node of an entry whose board does not exist: %v", err)
	}
}

// TestNotFound checks that the boards of a project are read only by those
// who may read the project, and that a board in the trash is not found.
func TestNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	b := f.create(t, "Harbour", nil)
	f.must(t, cmd{board.Delete, map[string]any{"id": b.ID}})
	id, _ := ids.Decode(ids.Board, b.ID)
	if _, err := f.boards.Get(ctx, f.actor(), f.project, id); !errors.Is(err, board.ErrNotFound) {
		t.Errorf("a board in the trash: %v", err)
	}
	stranger := commands.Actor{UserID: ids.New()}
	if _, err := f.boards.List(ctx, stranger, f.project, ids.UUID{}, 10); !errors.Is(err, project.ErrNotFound) {
		t.Errorf("a stranger's list: %v", err)
	}
	if _, err := f.boards.Folders(ctx, stranger, f.project, "", ids.UUID{}, 10); !errors.Is(err,
		project.ErrNotFound) {
		t.Errorf("a stranger's folders: %v", err)
	}
}
