package component_test

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/component"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/ident"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
	"github.com/CyberCute-Studio/storycreep/server/internal/trash"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
	"github.com/CyberCute-Studio/storycreep/server/internal/variable"
)

func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

// fixture is a database of the test's own with a workspace whose owner is
// owner, a project of it, created with the default types, and the pipeline
// of the projects', the boards', the variables', and the components'
// commands, of the registry reg, with the entitlements ent.
type fixture struct {
	d          *dbtest.Database
	db         *database.DB
	ws         ids.UUID
	owner      ids.UUID
	project    ids.UUID
	reg        *commands.Registry
	ent        *entitlements.Service
	pipeline   *commands.Pipeline
	projects   *project.Service
	components *component.Service
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
	if _, err := admin.Exec(ctx, `INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Harbour', $2,
		'unlimited')`, f.ws, "harbour-"+f.ws.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO membership (id, workspace_id, user_id, role) VALUES ($1, $2, $3,
		'owner')`, ids.New(), f.ws, f.owner); err != nil {
		t.Fatal(err)
	}
	f.reg = commands.NewRegistry()
	project.Register(f.reg)
	board.Register(f.reg)
	variable.Register(f.reg)
	component.Register(f.reg)
	f.ent = entitlements.New(entitlements.Options{Meter: usage.Meter{}})
	f.pipeline = commands.New(f.db, commands.Options{Registry: f.reg, Entitlements: f.ent})
	f.projects = project.New(f.db, project.Options{Entitlements: f.ent, Pipeline: f.pipeline, Seed: component.Seed})
	f.components = component.New(f.db, component.Options{Projects: f.projects})
	p, err := f.projects.Create(ctx, f.owner, f.ws, project.Settings{Name: "Harbour"}, audit.Client{})
	if err != nil {
		t.Fatal(err)
	}
	if f.project, err = ids.Decode(ids.Project, p.ID); err != nil {
		t.Fatal(err)
	}
	return f
}

// maxUndo has the fixture apply its batches with a pipeline whose bound of a
// batch's undo is n bytes (commands.Options.MaxUndoBytes).
func (f *fixture) maxUndo(n int) {
	f.pipeline = commands.New(f.db, commands.Options{Registry: f.reg, Entitlements: f.ent, MaxUndoBytes: n})
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
// operation log keeps, and returns the error of the undo's batch.
func (f *fixture) undo(t *testing.T, seq int64) error {
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
	if len(steps) == 0 {
		return nil
	}
	batch := make([]commands.Command, len(steps))
	for i, st := range steps {
		batch[i] = commands.Command{ID: ids.New(), Type: st.Type, Payload: st.Payload}
	}
	_, err := f.pipeline.Apply(context.Background(), f.actor(), f.project, batch)
	return err
}

// undoValues returns the IDs, sorted, of the components whose values the
// inverse of the batch whose seq is seq gives them, which the operation log
// keeps: those of its one step, a retype or a change of an enum's options.
func (f *fixture) undoValues(t *testing.T, seq int64) []string {
	t.Helper()
	var steps []struct {
		Payload struct {
			Values map[string]json.RawMessage `json:"values"`
		} `json:"payload"`
	}
	var inverse []byte
	if err := f.d.Admin(t).QueryRow(context.Background(),
		"SELECT inverse FROM operation WHERE project_id = $1 AND seq = $2", f.project, seq).Scan(&inverse); err != nil ||
		json.Unmarshal(inverse, &steps) != nil || len(steps) != 1 {
		t.Fatalf("the inverse of %d, %s: %v", seq, inverse, err)
	}
	if len(steps[0].Payload.Values) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(steps[0].Payload.Values))
}

// state reads every type, attribute definition, component, value, and folder
// of the fixture's project as the superuser, the deleted ones too if deleted
// is set, but for their revisions and timestamps: what a change and its undo
// must leave alike.
func (f *fixture) state(t *testing.T, deleted bool) string {
	t.Helper()
	var s string
	if err := f.d.Admin(t).QueryRow(context.Background(), `SELECT concat_ws(' / ',
		(SELECT string_agg(concat_ws('|', id, key, name, icon, deleted_at IS NOT NULL), ', ' ORDER BY id)
			FROM component_type WHERE project_id = $1 AND ($2 OR deleted_at IS NULL)),
		(SELECT string_agg(concat_ws('|', id, component_type_id, "order", key, name, type, enum_options::text,
			default_value::text, is_runtime, deleted_at IS NOT NULL), ', ' ORDER BY id) FROM attribute_def
			WHERE project_id = $1 AND ($2 OR deleted_at IS NULL)),
		(SELECT string_agg(concat_ws('|', id, component_type_id, folder_id, "order", key, name, description::text,
			image_asset_id, deleted_at IS NOT NULL), ', ' ORDER BY id) FROM component
			WHERE project_id = $1 AND ($2 OR deleted_at IS NULL)),
		(SELECT string_agg(concat_ws('|', v.component_id, v.attribute_def_id, v.value::text), ', '
			ORDER BY v.component_id, v.attribute_def_id) FROM attribute_value v
			JOIN component c ON c.id = v.component_id JOIN attribute_def a ON a.id = v.attribute_def_id
			WHERE v.project_id = $1 AND ($2 OR c.deleted_at IS NULL AND a.deleted_at IS NULL)),
		(SELECT string_agg(concat_ws('|', id, kind, parent_id, name, "order", deleted_at IS NOT NULL), ', '
			ORDER BY id) FROM folder WHERE project_id = $1 AND ($2 OR deleted_at IS NULL)))`, f.project,
		deleted).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// The IDs of new entities.
func typeID() string      { return ids.Encode(ids.ComponentType, ids.New()) }
func attributeID() string { return ids.Encode(ids.AttributeDef, ids.New()) }
func componentID() string { return ids.Encode(ids.Component, ids.New()) }
func folderID() string    { return ids.Encode(ids.Folder, ids.New()) }

// decodeID returns the UUID of id, an ID of the kind, or fails the test.
func decodeID(t *testing.T, kind ids.Kind, id string) ids.UUID {
	t.Helper()
	u, err := ids.Decode(kind, id)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// newType creates a component type named name, with payload's other fields,
// and returns its ID.
func (f *fixture) newType(t *testing.T, name string, payload map[string]any) string {
	t.Helper()
	p := map[string]any{"id": typeID(), "name": name}
	for k, v := range payload {
		p[k] = v
	}
	f.must(t, cmd{component.CreateType, p})
	return p["id"].(string)
}

// newAttribute returns the command that adds an attribute definition named
// name of the type typ to the component type ct, with payload's other fields,
// and its ID.
func newAttribute(ct, name, typ string, payload map[string]any) (cmd, string) {
	p := map[string]any{"id": attributeID(), "component_type_id": ct, "name": name, "type": typ}
	for k, v := range payload {
		p[k] = v
	}
	return cmd{component.CreateAttribute, p}, p["id"].(string)
}

// newComponent returns the command that creates a component named name of
// the type ct, with payload's other fields, and its ID.
func newComponent(ct, name string, payload map[string]any) (cmd, string) {
	p := map[string]any{"id": componentID(), "component_type_id": ct, "name": name}
	for k, v := range payload {
		p[k] = v
	}
	return cmd{component.Create, p}, p["id"].(string)
}

// get returns the component id of the fixture's project, or fails the test.
func (f *fixture) get(t *testing.T, id string) component.Component {
	t.Helper()
	c, err := f.components.Component(context.Background(), f.actor(), f.project, decodeID(t, ids.Component, id))
	if err != nil {
		t.Fatalf("the component %s: %v", id, err)
	}
	return c
}

// getType returns the component type id of the fixture's project, or fails
// the test.
func (f *fixture) getType(t *testing.T, id string) component.ComponentType {
	t.Helper()
	ct, err := f.components.Type(context.Background(), f.actor(), f.project, decodeID(t, ids.ComponentType, id))
	if err != nil {
		t.Fatalf("the type %s: %v", id, err)
	}
	return ct
}

// valueOf returns the value that the component id holds of the attribute
// definition atr, as JSON, or "" if it holds none.
func (f *fixture) valueOf(t *testing.T, id, atr string) string {
	t.Helper()
	v, ok := f.get(t, id).Attributes[atr]
	if !ok {
		return ""
	}
	return string(v.JSON())
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

// invalidAt fails the test unless err is a batch that is not valid, with a
// problem of its first command at the field.
func invalidAt(t *testing.T, err error, field string) {
	t.Helper()
	var invalid *commands.InvalidError
	if !errors.As(err, &invalid) || !slices.ContainsFunc(invalid.Problems, func(p commands.Problem) bool {
		return p.Command == 0 && p.Field == field
	}) {
		t.Errorf("the batch was not refused as invalid at %s: %v", field, err)
	}
}

// TestSeed checks that a project is created with the default types,
// Character, Item, Location, Faction, and Custom, in that order, without
// attribute definitions, named in the project's locale.
func TestSeed(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	types, err := f.components.Types(context.Background(), f.actor(), f.project, ids.UUID{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, ct := range types {
		got = append(got, ct.Key+" "+ct.Name+" "+ct.Icon)
		if len(ct.Attributes) != 0 {
			t.Errorf("the default type %s has attribute definitions: %v", ct.Key, ct.Attributes)
		}
	}
	want := []string{"character Character person", "item Item item", "location Location location",
		"faction Faction group", "custom Custom tag"}
	if !slices.Equal(got, want) {
		t.Errorf("a new project's types are %v, want %v", got, want)
	}
}

// TestDeriveKey checks how a key is derived from a name: its letters and
// digits in lowercase ASCII, without accents, each run of other characters
// an underscore, an underscore before a digit, cut to 64 characters, and the
// fallback for a name without letters or digits in Latin script.
func TestDeriveKey(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]string{
		"Elara Vance":                 "elara_vance",
		"Tomáš Novák":                 "tomas_novak",
		"1st Mate":                    "_1st_mate",
		"  Old -- sword! ":            "old_sword",
		"Ἀθηνᾶ":                       "component",
		strings.Repeat("ab ", 40):     strings.TrimRight(strings.Repeat("ab_", 22)[:64], "_"),
		"9" + strings.Repeat("a", 70): "_9" + strings.Repeat("a", 62),
	} {
		got := component.DeriveKey(name, "component")
		if got != want || ident.Check(got) != nil && got != "component" {
			t.Errorf("the key of %q is %q, want %q", name, got, want)
		}
	}
}

// TestKeys checks that the keys of components, of types, and of attribute
// definitions are identifiers, derived from their names unless a command
// gives one, unique among the entities of their scope that are not in the
// trash, which the project's components, the project's types, and a type's
// definitions are, and that a key that another has is refused, as one that
// is not an identifier is, at the payload's key.
//
// Verifies: FR-COMP-01/AC3, FR-COMP-02/AC1
func TestKeys(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ship := f.newType(t, "Ship", nil)
	if got := f.getType(t, ship).Key; got != "ship" {
		t.Errorf("the type Ship's key is %s", got)
	}
	if got := f.getType(t, f.newType(t, "Ship", nil)).Key; got != "ship_2" {
		t.Errorf("a second type Ship's key is %s", got)
	}
	// Derived keys skip the reserved words and the keys that are taken.
	var names []cmd
	var made []string
	for _, name := range []string{"Elara Vance", "Elara Vance", "Board", "Ἀθηνᾶ", "Ἀθηνᾶ"} {
		c, id := newComponent(ship, name, nil)
		names, made = append(names, c), append(made, id)
	}
	f.must(t, names...)
	var keys []string
	for _, id := range made {
		keys = append(keys, f.get(t, id).Key)
	}
	if want := []string{"elara_vance", "elara_vance_2", "board_2", "component", "component_2"}; !slices.Equal(keys,
		want) {
		t.Errorf("the derived keys are %v, want %v", keys, want)
	}
	// A rename leaves the key as it is (FR-COMP-02 AC2).
	f.must(t, cmd{component.Rename, map[string]any{"id": made[0], "name": "Elara Moon"}})
	if c := f.get(t, made[0]); c.Key != "elara_vance" || c.Name != "Elara Moon" {
		t.Errorf("after the rename, the component is %s %s", c.Key, c.Name)
	}
	for _, c := range []struct {
		cmd    cmd
		reason ident.Reason
		taken  bool
	}{
		{cmd{component.SetKey, map[string]any{"id": made[0], "key": "9lives"}}, ident.Format, false},
		{cmd{component.SetKey, map[string]any{"id": made[0], "key": "met-elara"}}, ident.Format, false},
		{cmd{component.SetKey, map[string]any{"id": made[0], "key": "elseif"}}, ident.Reserved, false},
		{cmd{component.SetKey, map[string]any{"id": made[0], "key": "board_2"}}, "", true},
		{cmd{component.SetTypeKey, map[string]any{"id": ship, "key": "ship_2"}}, "", true},
		{cmd{component.SetTypeKey, map[string]any{"id": ship, "key": "not"}}, ident.Reserved, false},
	} {
		r := refused(t, func() error { _, err := f.apply(t, c.cmd); return err }())
		var invalid *ident.Error
		switch {
		case r.Field != "key":
			t.Errorf("%v was refused at %s", c.cmd, r.Field)
		case c.taken && !errors.Is(r.Reason, component.ErrKeyTaken):
			t.Errorf("%v: %v, want the key taken", c.cmd, r)
		case !c.taken && (!errors.As(r.Reason, &invalid) || invalid.Reason != c.reason):
			t.Errorf("%v: %v, want %s", c.cmd, r, c.reason)
		}
	}
	// A definition's key is unique within its type, not the project's.
	trust, _ := newAttribute(ship, "Trust", value.Int, nil)
	f.must(t, trust)
	other := f.newType(t, "Crew", nil)
	again, _ := newAttribute(other, "Trust", value.Bool, nil)
	f.must(t, again)
	same, _ := newAttribute(ship, "Trust", value.Bool, map[string]any{"key": "trust"})
	if r := refused(t, func() error { _, err := f.apply(t, same); return err }()); r.Field != "key" ||
		!errors.Is(r.Reason, component.ErrKeyTaken) {
		t.Errorf("a definition with a key of its type: %v", r)
	}
	// A deleted component's key is free, and its restore is refused while
	// another has it.
	f.must(t, cmd{component.Delete, map[string]any{"id": made[0]}})
	taker, takerID := newComponent(ship, "Elara Vance", nil)
	f.must(t, taker)
	if got := f.get(t, takerID).Key; got != "elara_vance" {
		t.Errorf("the key freed by a deletion is taken as %s", got)
	}
	if r := refused(t, func() error {
		_, err := f.apply(t, cmd{component.Restore, map[string]any{"id": made[0]}})
		return err
	}()); r.Field != "id" || !errors.Is(r.Reason, component.ErrKeyTaken) {
		t.Errorf("the restore of a component whose key is taken: %v", r)
	}
}

// TestAddAttribute checks that adding an attribute definition to a type gives
// each of its components, those in the trash included, its default, as one
// command whose operation names every component that it changed, and that a
// later change of the default leaves the values as they are.
//
// Verifies: FR-COMP-01/AC1
func TestAddAttribute(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ship := f.newType(t, "Ship", nil)
	var made []string
	var cmds []cmd
	for _, name := range []string{"Elara", "Mira", "Sol"} {
		c, id := newComponent(ship, name, nil)
		cmds, made = append(cmds, c), append(made, id)
	}
	cmds = append(cmds, cmd{component.Delete, map[string]any{"id": made[2]}})
	f.must(t, cmds...)
	add, trust := newAttribute(ship, "Trust", value.Int, map[string]any{"default": 3})
	res := f.must(t, add)
	var touched []string
	for _, r := range res.Revisions {
		if r.Ref.Kind == ids.Component {
			touched = append(touched, ids.Encode(ids.Component, r.Ref.ID))
		}
	}
	slices.Sort(touched)
	want := slices.Clone(made)
	slices.Sort(want)
	if !slices.Equal(touched, want) {
		t.Errorf("adding the definition changed the components %v, want %v", touched, want)
	}
	for _, id := range made[:2] {
		if got := f.valueOf(t, id, trust); got != "3" {
			t.Errorf("the component %s holds %s, want 3", id, got)
		}
	}
	f.must(t, cmd{component.Restore, map[string]any{"id": made[2]}})
	if got := f.valueOf(t, made[2], trust); got != "3" {
		t.Errorf("the component restored from the trash holds %s, want 3", got)
	}
	f.must(t, cmd{component.SetAttributeDefault, map[string]any{"id": trust, "default": 7}},
		cmd{component.SetAttribute, map[string]any{"id": made[1], "attribute_id": trust, "value": 5}})
	if a, b := f.valueOf(t, made[0], trust), f.valueOf(t, made[1], trust); a != "3" || b != "5" {
		t.Errorf("after the change of the default, the components hold %s and %s, want 3 and 5", a, b)
	}
	late, lateID := newComponent(ship, "Juno", nil)
	f.must(t, late)
	if got := f.valueOf(t, lateID, trust); got != "7" {
		t.Errorf("a component created after the change of the default holds %s, want 7", got)
	}
	// A definition added without a default gives its type's zero value, and
	// an enum's first option.
	mood, moodID := newAttribute(ship, "Mood", value.Enum, map[string]any{"options": []string{"calm", "angry"}})
	note, noteID := newAttribute(ship, "Notes", value.Text, nil)
	f.must(t, mood, note)
	if m, n := f.valueOf(t, made[0], moodID), f.valueOf(t, made[0], noteID); m != `"calm"` || n != `""` {
		t.Errorf("the definitions added without defaults gave %s and %s", m, n)
	}
}

// TestValues checks the values of a component's attributes: a create gives
// the values that it names, and the defaults of the others, a value is one of
// its definition's type, and a definition that is not one of the
// component's type's, or that is in the trash, is refused.
func TestValues(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ship, crew := f.newType(t, "Ship", nil), f.newType(t, "Crew", nil)
	trust, trustID := newAttribute(ship, "Trust", value.Int, nil)
	mood, moodID := newAttribute(ship, "Mood", value.Enum, map[string]any{"options": []string{"calm", "angry"}})
	other, otherID := newAttribute(crew, "Rank", value.String, nil)
	f.must(t, trust, mood, other)
	c, id := newComponent(ship, "Elara", map[string]any{"attributes": map[string]any{
		strings.ToLower(trustID): 3.0, moodID: "angry"}})
	f.must(t, c)
	if a, b := f.valueOf(t, id, trustID), f.valueOf(t, id, moodID); a != "3" || b != `"angry"` {
		t.Errorf("the component holds %s and %s, want 3 and \"angry\"", a, b)
	}
	for _, c := range []struct {
		cmd   cmd
		field string
	}{
		{cmd{component.SetAttribute, map[string]any{"id": id, "attribute_id": trustID, "value": "three"}}, "value"},
		{cmd{component.SetAttribute, map[string]any{"id": id, "attribute_id": trustID, "value": 3.5}}, "value"},
		{cmd{component.SetAttribute, map[string]any{"id": id, "attribute_id": moodID, "value": "sad"}}, "value"},
		{cmd{component.SetAttribute, map[string]any{"id": id, "attribute_id": otherID, "value": "x"}}, "attribute_id"},
		{func() cmd {
			c, _ := newComponent(ship, "Mira", map[string]any{"attributes": map[string]any{otherID: "x"}})
			return c
		}(), "attributes." + otherID},
		{func() cmd {
			c, _ := newComponent(ship, "Mira", map[string]any{"attributes": map[string]any{
				trustID: 1, strings.ToLower(trustID): 2}})
			return c
		}(), "attributes." + strings.ToLower(trustID)},
	} {
		if r := refused(t, func() error { _, err := f.apply(t, c.cmd); return err }()); r.Field != c.field {
			t.Errorf("%v was refused at %s, want %s", c.cmd, r.Field, c.field)
		}
	}
	f.must(t, cmd{component.DeleteAttribute, map[string]any{"id": trustID}})
	if _, ok := f.get(t, id).Attributes[trustID]; ok {
		t.Errorf("a component holds a value of a definition in the trash")
	}
	if r := refused(t, func() error {
		_, err := f.apply(t, cmd{component.SetAttribute, map[string]any{"id": id, "attribute_id": trustID,
			"value": 1}})
		return err
	}()); r.Field != "attribute_id" {
		t.Errorf("a value of a definition in the trash was refused at %s", r.Field)
	}
	// Restoring the definition brings the value back.
	f.must(t, cmd{component.RestoreAttribute, map[string]any{"id": trustID}})
	if got := f.valueOf(t, id, trustID); got != "3" {
		t.Errorf("after the definition's restore, the component holds %s, want 3", got)
	}
	// A text is no part of the game's state.
	text, _ := newAttribute(ship, "Notes", value.Text, map[string]any{"runtime": true})
	invalidAt(t, func() error { _, err := f.apply(t, text); return err }(), "payload.runtime")
}

// TestRetype checks that changing an attribute definition's type takes an
// explicit conversion, which converts each value, or gives the new default to
// each that does not convert, or to every value, and that its undo gives back
// what the conversion lost.
//
// Verifies: FR-COMP-01/AC2
func TestRetype(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ship := f.newType(t, "Ship", nil)
	add, mood := newAttribute(ship, "Mood", value.String, nil)
	f.must(t, add)
	values := map[string]string{"Elara": "12", "Mira": "calm", "Sol": "3.5"}
	byName := map[string]string{}
	var cmds []cmd
	for name, v := range values {
		c, id := newComponent(ship, name, map[string]any{"attributes": map[string]any{mood: v}})
		cmds, byName[name] = append(cmds, c), id
	}
	f.must(t, cmds...)
	dock := f.newType(t, "Dock", nil)
	pier, pierID := newComponent(dock, "Pier", nil)
	f.must(t, pier)
	// A retype without a conversion is refused by its schema.
	invalidAt(t, func() error {
		_, err := f.apply(t, cmd{component.RetypeAttribute, map[string]any{"id": mood, "type": "int"}})
		return err
	}(), "payload")
	for _, c := range []struct {
		payload map[string]any
		field   string
	}{
		{map[string]any{"id": mood, "type": "string", "conversion": "convert"}, "type"},
		{map[string]any{"id": mood, "type": "int", "conversion": "convert", "values": map[string]any{
			pierID: 1}}, ""},
	} {
		r := refused(t, func() error { _, err := f.apply(t, cmd{component.RetypeAttribute, c.payload}); return err }())
		if c.field != "" && r.Field != c.field || c.field == "" && !strings.HasPrefix(r.Field, "values.") {
			t.Errorf("%v was refused at %s", c.payload, r.Field)
		}
	}
	before := f.state(t, true)
	res := f.must(t, cmd{component.RetypeAttribute, map[string]any{"id": mood, "type": "int",
		"conversion": "convert", "default": -1, "values": map[string]any{byName["Sol"]: 4}}})
	for name, want := range map[string]string{"Elara": "12", "Mira": "-1", "Sol": "4"} {
		if got := f.valueOf(t, byName[name], mood); got != want {
			t.Errorf("after the retype, %s holds %s, want %s", name, got, want)
		}
	}
	if err := f.undo(t, res.Seq); err != nil {
		t.Fatalf("undo the retype: %v", err)
	}
	if got := f.state(t, true); got != before {
		t.Errorf("undoing the retype left\n%s\nwant\n%s", got, before)
	}
	res = f.must(t, cmd{component.RetypeAttribute, map[string]any{"id": mood, "type": "enum", "conversion": "reset",
		"options": []string{"calm", "angry"}}})
	for name := range values {
		if got := f.valueOf(t, byName[name], mood); got != `"calm"` {
			t.Errorf("after the reset, %s holds %s, want the default", name, got)
		}
	}
	if err := f.undo(t, res.Seq); err != nil {
		t.Fatalf("undo the reset: %v", err)
	}
	if got := f.state(t, true); got != before {
		t.Errorf("undoing the reset left\n%s\nwant\n%s", got, before)
	}
	// A runtime definition is not made a text, which the list of what would
	// not convert answers as the retype does.
	f.must(t, cmd{component.RetypeAttribute, map[string]any{"id": mood, "type": "float", "conversion": "convert"}},
		cmd{component.SetAttributeRuntime, map[string]any{"id": mood, "runtime": true}})
	if r := refused(t, func() error {
		_, err := f.apply(t, cmd{component.RetypeAttribute, map[string]any{"id": mood, "type": "text",
			"conversion": "convert"}})
		return err
	}()); r.Field != "type" {
		t.Errorf("the retype of a runtime definition to a text was refused at %s", r.Field)
	}
	var errs input.FieldErrors
	if _, err := f.components.Unconvertible(context.Background(), f.actor(), f.project,
		decodeID(t, ids.ComponentType, ship), decodeID(t, ids.AttributeDef, mood), component.Target{Type: value.Text},
		ids.UUID{}, 10); !errors.As(err, &errs) || len(errs) != 1 || errs[0].Field != "type" {
		t.Errorf("the values that a runtime definition's retype to a text would not convert are answered with %v", err)
	}
}

// TestUndoAfterPurge checks that the undo of a change of a definition's type
// applies once the purge has deleted a component whose value it gives back,
// and gives back the others' values (FR-PROJ-06).
func TestUndoAfterPurge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	ship := f.newType(t, "Ship", nil)
	add, mood := newAttribute(ship, "Mood", value.String, nil)
	elara, elaraID := newComponent(ship, "Elara", map[string]any{"attributes": map[string]any{mood: "calm"}})
	sol, solID := newComponent(ship, "Sol", map[string]any{"attributes": map[string]any{mood: "angry"}})
	f.must(t, add, elara, sol, cmd{component.Delete, map[string]any{"id": solID}})
	res := f.must(t, cmd{component.RetypeAttribute, map[string]any{"id": mood, "type": "int", "conversion": "reset"}})
	if _, err := f.d.Admin(t).Exec(ctx, "UPDATE component SET deleted_at = now() - interval '31 days' WHERE id = $1",
		decodeID(t, ids.Component, solID)); err != nil {
		t.Fatal(err)
	}
	if _, err := trash.Purge(ctx, f.db); err != nil {
		t.Fatal(err)
	}
	if err := f.undo(t, res.Seq); err != nil {
		t.Fatalf("undo the retype once the purge has deleted a component whose value it gives back: %v", err)
	}
	if got := f.valueOf(t, elaraID, mood); got != `"calm"` {
		t.Errorf("after the undo, Elara holds %s, want \"calm\"", got)
	}
}

// TestOptions checks that a change of an enum's options gives the default to
// a value that is none of them, or the value that the command gives its
// component, refuses a default that is none of them, and is undone with the
// values that it lost.
func TestOptions(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ship := f.newType(t, "Ship", nil)
	add, mood := newAttribute(ship, "Mood", value.Enum, map[string]any{"options": []string{"calm", "angry", "sad"},
		"default": "calm"})
	elara, elaraID := newComponent(ship, "Elara", map[string]any{"attributes": map[string]any{mood: "angry"}})
	mira, miraID := newComponent(ship, "Mira", map[string]any{"attributes": map[string]any{mood: "sad"}})
	f.must(t, add, elara, mira)
	if r := refused(t, func() error {
		_, err := f.apply(t, cmd{component.SetAttributeOptions, map[string]any{"id": mood,
			"options": []string{"furious", "sad"}}})
		return err
	}()); r.Field != "default" {
		t.Errorf("options without the default were refused at %s", r.Field)
	}
	before := f.state(t, true)
	res := f.must(t, cmd{component.SetAttributeOptions, map[string]any{"id": mood,
		"options": []string{"calm", "furious"}, "values": map[string]any{elaraID: "furious"}}})
	if e, m := f.valueOf(t, elaraID, mood), f.valueOf(t, miraID, mood); e != `"furious"` || m != `"calm"` {
		t.Errorf("after the options changed, the components hold %s and %s", e, m)
	}
	if err := f.undo(t, res.Seq); err != nil {
		t.Fatalf("undo the options: %v", err)
	}
	if got := f.state(t, true); got != before {
		t.Errorf("undoing the options left\n%s\nwant\n%s", got, before)
	}
	// Renaming the default's option gives Kai, who holds it, the new default,
	// which the undo, whose default is the old, gives back as it is, so it
	// holds no value of Kai's; and so does the undo of a reset, which converts
	// none of the values back, and gives Kai's back as its default too.
	kai, _ := newComponent(ship, "Kai", nil)
	f.must(t, kai)
	before = f.state(t, true)
	for _, c := range []struct {
		change cmd
		held   []string
	}{
		{cmd{component.SetAttributeOptions, map[string]any{"id": mood, "options": []string{"serene", "angry", "sad"},
			"default": "serene"}}, nil},
		{cmd{component.RetypeAttribute, map[string]any{"id": mood, "type": "int", "conversion": "reset"}},
			[]string{elaraID, miraID}},
	} {
		res := f.must(t, c.change)
		if held := f.undoValues(t, res.Seq); !slices.Equal(held, slices.Sorted(slices.Values(c.held))) {
			t.Errorf("the undo of %v holds the values of %v, want %v", c.change, held, c.held)
		}
		if err := f.undo(t, res.Seq); err != nil {
			t.Fatalf("undo %v: %v", c.change, err)
		}
		if got := f.state(t, true); got != before {
			t.Errorf("undoing %v left\n%s\nwant\n%s", c.change, got, before)
		}
	}
	add, num := newAttribute(ship, "Count", value.Int, nil)
	f.must(t, add)
	if r := refused(t, func() error {
		_, err := f.apply(t, cmd{component.SetAttributeOptions, map[string]any{"id": num, "options": []string{"a"}}})
		return err
	}()); r.Field != "id" {
		t.Errorf("the options of an int were refused at %s", r.Field)
	}
}

// TestAttributeChecks checks that a definition's create is answered with one
// problem for each field that is wrong, none derived from another, and that
// an enum's option that encoding/json reads otherwise than the client wrote
// it, an escape of a lone surrogate, is refused, so that the server never
// holds another option than the client sent.
func TestAttributeChecks(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ship := f.newType(t, "Ship", nil)
	for _, c := range []struct {
		payload map[string]any
		field   string
	}{
		{map[string]any{"default": "x"}, "payload.default"},
		{map[string]any{"type": value.Enum, "options": []string{}}, "payload.options"},
		{map[string]any{"type": value.Enum, "options": []string{"calm", "calm"}, "default": "sad"}, "payload.options"},
	} {
		add, _ := newAttribute(ship, "Trust", value.Int, c.payload)
		_, err := f.apply(t, add)
		var invalid *commands.InvalidError
		if !errors.As(err, &invalid) || len(invalid.Problems) != 1 || invalid.Problems[0].Field != c.field {
			t.Errorf("%v was answered with %v, want one problem at %s", c.payload, err, c.field)
		}
	}
	raw := json.RawMessage(`{"id": "` + attributeID() + `", "component_type_id": "` + ship + `", "name": "Mood", ` +
		`"type": "enum", "options": ["calm", "\ud800"]}`)
	invalidAt(t, func() error { _, err := f.apply(t, cmd{component.CreateAttribute, raw}); return err }(),
		"payload.options")
}

// TestUndoLimit checks that a change of an enum's options or of a
// definition's type whose undo would take its batch's undo past the
// pipeline's bound is refused at the field that it changes, its undo counted
// as JSON encodes it for the operation log, escapes and all: one whose undo
// would hold more of the values that it loses than the bound leaves, one that
// loses no value but whose undo's old default takes it past, and one whose
// undo fits but would take its batch's past it, with the undos of the
// batch's commands before it; and that a command of another type whose undo
// would, as a description's, is refused at its payload.
func TestUndoLimit(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ship := f.newType(t, "Ship", nil)
	options := []string{"calm", "angry"}
	add, mood := newAttribute(ship, "Mood", value.Enum, map[string]any{"options": options})
	addTone, tone := newAttribute(ship, "Tone", value.Enum, map[string]any{"options": options})
	// The log escapes <, as \u003c, which then takes six bytes.
	marks := strings.Repeat("<", 20)
	addNotes, notes := newAttribute(ship, "Notes", value.Text, map[string]any{"default": marks})
	elara, elaraID := newComponent(ship, "Elara", map[string]any{"attributes": map[string]any{mood: "angry",
		tone: "angry"}})
	mira, miraID := newComponent(ship, "Mira", map[string]any{"attributes": map[string]any{mood: "angry"}})
	f.must(t, add, addTone, addNotes, elara, mira)
	// size is how many bytes the payload of the undo undo takes, with the
	// values that it gives back, as the operation log keeps it.
	size := func(undo, values map[string]any) int {
		t.Helper()
		p := maps.Clone(undo)
		if len(values) > 0 {
			p["values"] = values
		}
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		return len(raw)
	}
	try := func(bound int, c cmd) error {
		t.Helper()
		f.maxUndo(bound)
		defer f.maxUndo(0)
		_, err := f.apply(t, c)
		return err
	}
	optionsUndo := func(id string) map[string]any {
		return map[string]any{"id": id, "options": options, "default": "calm"}
	}
	// Each change loses both components' values, and its undo would hold one
	// of them, but not two.
	for _, c := range []struct {
		cmd   cmd
		undo  map[string]any
		field string
	}{
		{cmd{component.SetAttributeOptions, map[string]any{"id": mood, "options": []string{"calm"}}},
			optionsUndo(mood), "options"},
		{cmd{component.RetypeAttribute, map[string]any{"id": mood, "type": "int", "conversion": "reset"}},
			map[string]any{"id": mood, "type": "enum", "conversion": "convert", "options": options, "default": "calm"},
			"type"},
	} {
		if r := refused(t, try(size(c.undo, map[string]any{elaraID: "angry"}), c.cmd)); r.Field != c.field {
			t.Errorf("%v, whose undo would hold too much, was refused at %s, want %s", c.cmd, r.Field, c.field)
		}
	}
	// A retype of the text to a string loses none of its values, which hold
	// its default, but its undo holds the default.
	toString := cmd{component.RetypeAttribute, map[string]any{"id": notes, "type": "string", "conversion": "convert"}}
	stringUndo := map[string]any{"id": notes, "type": "text", "conversion": "convert", "default": marks}
	if r := refused(t, try(size(stringUndo, nil)-1, toString)); r.Field != "type" {
		t.Errorf("a retype whose undo's default takes it past the bound was refused at %s, want type", r.Field)
	}
	if err := try(size(stringUndo, nil), toString); err != nil {
		t.Fatalf("a retype whose undo takes the bound exactly: %v", err)
	}
	// A reset of the string loses both values, which its undo gives back,
	// escaped as the log keeps them.
	reset := cmd{component.RetypeAttribute, map[string]any{"id": notes, "type": "int", "conversion": "reset"}}
	resetUndo := map[string]any{"id": notes, "type": "string", "conversion": "convert", "default": ""}
	both := map[string]any{elaraID: marks, miraID: marks}
	if r := refused(t, try(size(resetUndo, both)-1, reset)); r.Field != "type" {
		t.Errorf("a reset whose undo's values take it past the bound was refused at %s, want type", r.Field)
	}
	if err := try(size(resetUndo, both), reset); err != nil {
		t.Errorf("a reset whose undo's values take the bound exactly: %v", err)
	}

	// Each change then loses Elara's value alone, whose undo fits, but not
	// both in one batch.
	f.must(t, cmd{component.SetAttribute, map[string]any{"id": miraID, "attribute_id": mood, "value": "calm"}})
	moodOptions := cmd{component.SetAttributeOptions, map[string]any{"id": mood, "options": []string{"calm"}}}
	toneOptions := cmd{component.SetAttributeOptions, map[string]any{"id": tone, "options": []string{"calm"}}}
	f.maxUndo(size(optionsUndo(mood), map[string]any{elaraID: "angry"}))
	if r := refused(t, func() error { _, err := f.apply(t, moodOptions, toneOptions); return err }()); r.Command != 1 ||
		r.Field != "options" {
		t.Errorf("a batch whose undo would hold too much was refused by command %d at %s, want 1 at options",
			r.Command, r.Field)
	}
	for _, c := range []cmd{moodOptions, toneOptions} {
		if _, err := f.apply(t, c); err != nil {
			t.Errorf("a change whose undo holds one value: %v", err)
		}
	}

	// The undo of a description holds the one that it replaces, which the
	// bound counts as it counts any other command's.
	f.maxUndo(0)
	text := strings.Repeat("Keeper of the light. ", 10)
	doc := func(text string) json.RawMessage {
		raw, err := json.Marshal(map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph",
			"content": []any{map[string]any{"type": "text", "text": text}}}}})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	describe := func(id, text string) cmd {
		return cmd{component.Describe, map[string]any{"id": id, "description": doc(text)}}
	}
	f.must(t, describe(elaraID, text), describe(miraID, text))
	f.maxUndo(len(doc(text)) + 100)
	if r := refused(t, func() error {
		_, err := f.apply(t, describe(elaraID, "Keeper"), describe(miraID, "Keeper"))
		return err
	}()); r.Command != 1 || r.Field != "" {
		t.Errorf("a batch of descriptions whose undo would hold too much was refused by command %d at %q, want 1 "+
			"at the payload", r.Command, r.Field)
	}
	if _, err := f.apply(t, describe(elaraID, "Keeper")); err != nil {
		t.Errorf("a description whose undo fits: %v", err)
	}
}

// TestStoredValues checks that a retype converts a value that the text rules
// that a command checks refuse, which a stored value may hold once the rules
// are tightened, with its characters as they are, which the list of what
// would not convert agrees with, and that the list and the retype alike fail
// on a value that is not one of its definition's type, which only a row that
// the pipeline did not write holds.
func TestStoredValues(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	ship := f.newType(t, "Ship", nil)
	add, notes := newAttribute(ship, "Notes", value.String, nil)
	elara, elaraID := newComponent(ship, "Elara", nil)
	f.must(t, add, elara)
	set := func(raw string) {
		t.Helper()
		if _, err := f.d.Admin(t).Exec(ctx, `UPDATE attribute_value SET value = $1::jsonb WHERE component_id = $2
			AND attribute_def_id = $3`, raw, decodeID(t, ids.Component, elaraID), decodeID(t, ids.AttributeDef,
			notes)); err != nil {
			t.Fatal(err)
		}
	}
	legacy, err := json.Marshal("a" + string(rune(0x2028)) + "b")
	if err != nil {
		t.Fatal(err)
	}
	set(string(legacy))
	unconvertible := func(to string) ([]component.Unconvertible, error) {
		return f.components.Unconvertible(ctx, f.actor(), f.project, decodeID(t, ids.ComponentType, ship),
			decodeID(t, ids.AttributeDef, notes), component.Target{Type: to}, ids.UUID{}, 10)
	}
	if list, err := unconvertible(value.Text); err != nil || len(list) != 0 {
		t.Errorf("the values that a retype to a text would not convert are %v, %v, want none", list, err)
	}
	f.must(t, cmd{component.RetypeAttribute, map[string]any{"id": notes, "type": "text", "conversion": "convert"}})
	if got := f.valueOf(t, elaraID, notes); got != string(legacy) {
		t.Errorf("after the retype, the component holds %s, want %s", got, legacy)
	}
	set("5")
	if _, err := unconvertible(value.String); err == nil {
		t.Errorf("a value of another type was listed")
	}
	var r *commands.RefusedError
	if _, err := f.apply(t, cmd{component.RetypeAttribute, map[string]any{"id": notes, "type": "string",
		"conversion": "convert"}}); err == nil || errors.As(err, &r) {
		t.Errorf("the retype of a value of another type was answered with %v", err)
	}
}

// TestUnconvertibleWhileChanged checks that the list of the values that a
// retype would not convert reads the definition and its values as of one
// moment: a change of the definition, and one of a value, that commit once
// the list has read the definition, before it reads the values, are not
// read, rather than failing on values of another type, and the read locks
// nothing that keeps them from committing meanwhile.
//
// Verifies: FR-COMP-01/AC2
func TestUnconvertibleWhileChanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	ship := f.newType(t, "Ship", nil)
	add, mood := newAttribute(ship, "Mood", value.String, nil)
	elara, elaraID := newComponent(ship, "Elara", map[string]any{"attributes": map[string]any{mood: "12"}})
	mira, miraID := newComponent(ship, "Mira", map[string]any{"attributes": map[string]any{mood: "calm"}})
	f.must(t, add, elara, mira)
	reads := 0
	db := beforeValues{DB: f.db, fn: func() {
		reads++
		// Both values become 1, which converts to a bool, where neither
		// string does, and then Elara's 0, which does too.
		f.must(t, cmd{component.RetypeAttribute, map[string]any{"id": mood, "type": "float",
			"conversion": "reset", "default": 1}})
		f.must(t, cmd{component.SetAttribute, map[string]any{"id": elaraID, "attribute_id": mood, "value": 0}})
	}}
	components := component.New(db, component.Options{Projects: f.projects})
	list, err := components.Unconvertible(ctx, f.actor(), f.project, decodeID(t, ids.ComponentType, ship),
		decodeID(t, ids.AttributeDef, mood), component.Target{Type: value.Bool}, ids.UUID{}, 10)
	var got []string
	for _, u := range list {
		got = append(got, u.ComponentID)
	}
	want := []string{elaraID, miraID}
	slices.Sort(want)
	if err != nil || !slices.Equal(got, want) || reads != 1 {
		t.Errorf("the values that a retype to a bool would not convert, read while the definition and a value "+
			"changed, are %v, %v, after %d reads of the definition, want %v after one", got, err, reads, want)
	}
}

// beforeValues is a component.DB whose transactions that read as of one
// moment call fn before their first read of an attribute definition's values
// for the list of what a retype would not convert, once they have read the
// definition, as a change that commits in between would.
type beforeValues struct {
	component.DB
	fn func()
}

func (d beforeValues) InSnapshot(ctx context.Context, scope database.Scope, fn func(tx pgx.Tx) error) error {
	return d.DB.InSnapshot(ctx, scope, func(tx pgx.Tx) error {
		return fn(&valuesTx{Tx: tx, fn: d.fn})
	})
}

// valuesTx is the transaction of beforeValues, which calls fn once, before
// its first read of the values.
type valuesTx struct {
	pgx.Tx
	fn func()
}

func (tx *valuesTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if tx.fn != nil && strings.HasPrefix(sql, "-- name: AttributeValueComponents ") {
		tx.fn()
		tx.fn = nil
	}
	return tx.Tx.Query(ctx, sql, args...)
}

// TestTypeTrash checks the trash of component types and attribute
// definitions: a type is deleted only once each of its components is in the
// trash, its definitions go with it, restoring a component restores its
// type, and that restore's undo deletes them again.
func TestTypeTrash(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ship := f.newType(t, "Ship", nil)
	add, trust := newAttribute(ship, "Trust", value.Int, nil)
	c, elara := newComponent(ship, "Elara", nil)
	f.must(t, add, c)
	if r := refused(t, func() error {
		_, err := f.apply(t, cmd{component.DeleteType, map[string]any{"id": ship}})
		return err
	}()); r.Field != "id" || !errors.Is(r.Reason, component.ErrTypeInUse) {
		t.Errorf("the deletion of a type in use: %v", r)
	}
	f.must(t, cmd{component.Delete, map[string]any{"id": elara}}, cmd{component.DeleteType, map[string]any{"id": ship}})
	if _, err := f.components.Type(context.Background(), f.actor(), f.project, decodeID(t, ids.ComponentType,
		ship)); !errors.Is(err, component.ErrTypeNotFound) {
		t.Errorf("a type in the trash: %v", err)
	}
	// A definition of a type in the trash is in the trash with it.
	for _, c := range []cmd{
		{component.RenameAttribute, map[string]any{"id": trust, "name": "Faith"}},
		{component.DeleteAttribute, map[string]any{"id": trust}},
		{component.RestoreAttribute, map[string]any{"id": trust}},
		{component.RenameType, map[string]any{"id": ship, "name": "Boat"}},
	} {
		if r := refused(t, func() error { _, err := f.apply(t, c); return err }()); r.Field != "id" {
			t.Errorf("%v was refused at %s", c, r.Field)
		}
	}
	// A new type takes the key, which a restore of the component then
	// refuses for, until the new one is renamed.
	other := f.newType(t, "Ship", nil)
	r := refused(t, func() error { _, err := f.apply(t, cmd{component.Restore, map[string]any{"id": elara}}); return err }())
	if r.Field != "id" || !errors.Is(r.Reason, component.ErrKeyTaken) {
		t.Errorf("the restore of a component whose type's key is taken: %v", r)
	}
	f.must(t, cmd{component.SetTypeKey, map[string]any{"id": other, "key": "boat"}})
	before := f.state(t, true)
	res := f.must(t, cmd{component.Restore, map[string]any{"id": elara}})
	if ct := f.getType(t, ship); len(ct.Attributes) != 1 || ct.Attributes[0].ID != trust {
		t.Errorf("the restored type is %v", ct)
	}
	if got := f.valueOf(t, elara, trust); got != "0" {
		t.Errorf("the restored component holds %s", got)
	}
	if err := f.undo(t, res.Seq); err != nil {
		t.Fatalf("undo the restore: %v", err)
	}
	if got := f.state(t, true); got != before {
		t.Errorf("undoing the restore left\n%s\nwant\n%s", got, before)
	}
}

// TestFolders checks that components go into folders of components, which a
// folder that holds one is not deleted, and that restoring a component
// restores the folders above it.
func TestFolders(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ship := f.newType(t, "Ship", nil)
	crew, boards := folderID(), folderID()
	f.must(t, cmd{board.CreateFolder, map[string]any{"id": crew, "kind": "component", "name": "Crew"}},
		cmd{board.CreateFolder, map[string]any{"id": boards, "kind": "board", "name": "Acts"}})
	wrong, _ := newComponent(ship, "Elara", map[string]any{"folder_id": boards})
	if r := refused(t, func() error { _, err := f.apply(t, wrong); return err }()); r.Field != "folder_id" {
		t.Errorf("a component in a folder of boards was refused at %s", r.Field)
	}
	c, elara := newComponent(ship, "Elara", map[string]any{"folder_id": crew})
	f.must(t, c)
	if r := refused(t, func() error {
		_, err := f.apply(t, cmd{board.DeleteFolder, map[string]any{"id": crew}})
		return err
	}()); !errors.Is(r.Reason, board.ErrFolderNotEmpty) {
		t.Errorf("the deletion of a folder that holds a component: %v", r)
	}
	f.must(t, cmd{component.Delete, map[string]any{"id": elara}}, cmd{board.DeleteFolder, map[string]any{"id": crew}})
	f.must(t, cmd{component.Restore, map[string]any{"id": elara}})
	if got := f.get(t, elara).FolderID; got != crew {
		t.Errorf("the restored component is in %s", got)
	}
	folders, err := board.New(f.db, board.Options{Projects: f.projects}).Folders(context.Background(), f.actor(),
		f.project, board.KindComponent, ids.UUID{}, 10)
	if err != nil || len(folders) != 1 || folders[0].ID != crew {
		t.Errorf("after the restore, the folders of components are %v, %v", folders, err)
	}
}

// TestUndo checks the inverse of every command of a type, an attribute
// definition, and a component: applying the inverse that the operation log
// keeps of a change leaves them as they were before it, and that of a
// creation deletes what it created.
func TestUndo(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ship := f.newType(t, "Ship", nil)
	add, trust := newAttribute(ship, "Trust", value.Float, map[string]any{"default": 0.5})
	mood, moodID := newAttribute(ship, "Mood", value.Enum, map[string]any{"options": []string{"calm", "angry"}})
	crew := folderID()
	c, elara := newComponent(ship, "Elara", map[string]any{"attributes": map[string]any{trust: 2.25}})
	gone, goneID := newComponent(ship, "Sol", nil)
	f.must(t, add, mood, c, gone, cmd{board.CreateFolder, map[string]any{"id": crew, "kind": "component",
		"name": "Crew"}})
	f.must(t, cmd{component.Delete, map[string]any{"id": goneID}})
	create, _ := newComponent(ship, "Mira", map[string]any{"folder_id": crew,
		"description": json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Pilot"}]}]}`)})
	addLater, _ := newAttribute(ship, "Notes", value.Text, map[string]any{"default": "none"})
	for _, c := range []cmd{
		{component.CreateType, map[string]any{"id": typeID(), "name": "Crew", "icon": "person"}},
		{component.RenameType, map[string]any{"id": ship, "name": "Boat"}},
		{component.SetTypeKey, map[string]any{"id": ship, "key": "boat"}},
		{component.SetTypeIcon, map[string]any{"id": ship, "icon": "flag"}},
		addLater,
		{component.RenameAttribute, map[string]any{"id": trust, "name": "Faith"}},
		{component.SetAttributeKey, map[string]any{"id": trust, "key": "faith"}},
		{component.SetAttributeDefault, map[string]any{"id": trust, "default": 9}},
		{component.SetAttributeRuntime, map[string]any{"id": trust, "runtime": true}},
		{component.SetAttributeOptions, map[string]any{"id": moodID, "options": []string{"angry", "sad"},
			"default": "sad"}},
		{component.RetypeAttribute, map[string]any{"id": trust, "type": "int", "conversion": "convert"}},
		{component.RetypeAttribute, map[string]any{"id": trust, "type": "string", "conversion": "reset"}},
		{component.MoveAttribute, map[string]any{"id": trust, "order": "a"}},
		{component.DeleteAttribute, map[string]any{"id": trust}},
		create,
		{component.Rename, map[string]any{"id": elara, "name": "Elara Vance"}},
		{component.SetKey, map[string]any{"id": elara, "key": "captain"}},
		{component.Describe, map[string]any{"id": elara, "description": json.RawMessage(
			`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Keeper"}]}]}`)}},
		{component.SetAttribute, map[string]any{"id": elara, "attribute_id": trust, "value": 7.5}},
		{component.Move, map[string]any{"id": elara, "folder_id": crew}},
		{component.Delete, map[string]any{"id": elara}},
		{component.Restore, map[string]any{"id": goneID}},
	} {
		deleted := c.typ != component.Create && c.typ != component.CreateType && c.typ != component.CreateAttribute
		before := f.state(t, deleted)
		res := f.must(t, c)
		if f.state(t, deleted) == before {
			t.Errorf("%s %v changed nothing", c.typ, c.payload)
		}
		if err := f.undo(t, res.Seq); err != nil {
			t.Fatalf("undo %s: %v", c.typ, err)
		}
		if got := f.state(t, deleted); got != before {
			t.Errorf("undoing %s left\n%s\nwant\n%s", c.typ, got, before)
		}
	}
	// A deletion of what is in the trash already, and a restoration of what
	// is not, change nothing, and are undone by nothing.
	for _, c := range []cmd{
		{component.Delete, map[string]any{"id": goneID}},
		{component.Restore, map[string]any{"id": elara}},
		{component.RestoreType, map[string]any{"id": ship}},
		{component.RestoreAttribute, map[string]any{"id": trust}},
	} {
		if res := f.must(t, c); len(res.Revisions) != 0 {
			t.Errorf("%s changed %v", c.typ, res.Revisions)
		}
	}
}

// TestRichText checks that a component's description is a rich-text document
// that the validator takes, stored in its canonical form, and that one it
// refuses is refused at the payload's description, joined with where in it
// the value that is wrong is. FR-TEXT-01 AC1 asks the same of each endpoint
// that stores rich text, of which plan item 36 adds the last.
func TestRichText(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ship := f.newType(t, "Ship", nil)
	c, elara := newComponent(ship, "Elara", map[string]any{"description": json.RawMessage(
		`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}]}`)})
	f.must(t, c)
	if got, err := json.Marshal(f.get(t, elara).Description); err != nil ||
		string(got) != `{"content":[{"content":[{"text":"ab","type":"text"}],"type":"paragraph"}],"type":"doc"}` {
		t.Errorf("the stored description is %s, %v", got, err)
	}
	// A list nested deeper than the validator takes, which the schema does
	// not refuse, is refused where it is.
	deep := `{"type":"paragraph"}`
	for range 6 {
		deep = `{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph"},` + deep + `]}]}`
	}
	for doc, field := range map[string]string{
		`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"x","marks":[{"type":"underline"}]}]}]}`: "payload.description",
		`{"type":"doc","content":[` + deep + `]}`: "payload.description.content[0].content[0].content[1].content[0]",
	} {
		_, err := f.apply(t, cmd{component.Describe, map[string]any{"id": elara, "description": json.RawMessage(doc)}})
		var invalid *commands.InvalidError
		if !errors.As(err, &invalid) || !slices.ContainsFunc(invalid.Problems, func(p commands.Problem) bool {
			return strings.HasPrefix(p.Field, field)
		}) {
			t.Errorf("the description %s: %v, want it refused at %s", doc, err, field)
		}
	}
}

// TestRevisionConflict checks that a batch that expects a stale revision of a
// component is answered with its current state, its values and its
// description among it, and that of a definition of a type in the trash with
// the type's deletion.
func TestRevisionConflict(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ship := f.newType(t, "Ship", nil)
	add, trust := newAttribute(ship, "Trust", value.Int, map[string]any{"default": 4})
	c, elara := newComponent(ship, "Elara", nil)
	// The definition added after the component changes it too.
	f.must(t, c, add)
	raw, _ := json.Marshal(map[string]any{"id": elara, "name": "Mira"})
	_, err := f.pipeline.Apply(context.Background(), f.actor(), f.project, []commands.Command{{ID: ids.New(),
		Type: component.Rename, Payload: raw, ExpectedRev: map[commands.Ref]int64{
			{Kind: ids.Component, ID: decodeID(t, ids.Component, elara)}: 1}}})
	var conflict *commands.RevisionConflictError
	if !errors.As(err, &conflict) || len(conflict.Conflicts) != 1 {
		t.Fatalf("a stale revision: %v", err)
	}
	var state struct {
		Rev         int64                      `json:"rev"`
		Attributes  map[string]json.RawMessage `json:"attributes"`
		Description json.RawMessage            `json:"description"`
	}
	if err := json.Unmarshal(conflict.Conflicts[0].State, &state); err != nil || state.Rev != 2 ||
		string(state.Attributes[trust]) != "4" || len(state.Description) == 0 {
		t.Errorf("the conflict holds %s, %v", conflict.Conflicts[0].State, err)
	}
	f.must(t, cmd{component.Delete, map[string]any{"id": elara}}, cmd{component.DeleteType, map[string]any{"id": ship}})
	raw, _ = json.Marshal(map[string]any{"id": trust, "name": "Faith"})
	_, err = f.pipeline.Apply(context.Background(), f.actor(), f.project, []commands.Command{{ID: ids.New(),
		Type: component.RenameAttribute, Payload: raw, ExpectedRev: map[commands.Ref]int64{
			{Kind: ids.AttributeDef, ID: decodeID(t, ids.AttributeDef, trust)}: 7}}})
	var attr struct {
		DeletedAt *time.Time `json:"deleted_at"`
	}
	if !errors.As(err, &conflict) || len(conflict.Conflicts) != 1 ||
		json.Unmarshal(conflict.Conflicts[0].State, &attr) != nil || attr.DeletedAt == nil {
		t.Errorf("the conflict of a definition of a type in the trash: %v", err)
	}
}

// TestNotFound checks that the components of a project are read only by
// those who may read the project, and that one in the trash is not found.
func TestNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	ship := f.newType(t, "Ship", nil)
	c, elara := newComponent(ship, "Elara", nil)
	f.must(t, c, cmd{component.Delete, map[string]any{"id": elara}})
	if _, err := f.components.Component(ctx, f.actor(), f.project, decodeID(t, ids.Component, elara)); !errors.Is(err,
		component.ErrNotFound) {
		t.Errorf("a component in the trash: %v", err)
	}
	stranger := commands.Actor{UserID: ids.New()}
	if _, err := f.components.Components(ctx, stranger, f.project, component.Filter{}, ids.UUID{}, 10); !errors.Is(err,
		project.ErrNotFound) {
		t.Errorf("a stranger's list: %v", err)
	}
	if _, err := f.components.Types(ctx, stranger, f.project, ids.UUID{}, 10); !errors.Is(err, project.ErrNotFound) {
		t.Errorf("a stranger's list of types: %v", err)
	}
}

// TestEnumerations checks that the database takes every icon of a type and
// every type of an attribute definition that the package lists, which the
// schemas take too, refuses as a key every reserved word, an enum's options
// that are not valid, a runtime text, a default that is not of its type, and
// a name of more than MaxNameLength characters, so that a list or a limit
// changed in one place and not in the database's checks fails.
//
// Verifies: FR-COMP-01/AC3
func TestEnumerations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	for _, icon := range component.Icons() {
		if ct := f.getType(t, f.newType(t, "Ship", map[string]any{"icon": icon})); ct.Icon != icon {
			t.Errorf("a type created with the icon %s has %s", icon, ct.Icon)
		}
	}
	ship := f.newType(t, "Crew", nil)
	for _, typ := range value.Types() {
		p := map[string]any{}
		if typ == value.Enum {
			p["options"] = []string{"a"}
		}
		add, _ := newAttribute(ship, "A "+typ, typ, p)
		f.must(t, add)
	}
	add, trust := newAttribute(ship, "Trust", value.Int, nil)
	c, elara := newComponent(ship, "Elara", nil)
	f.must(t, add, c)
	admin := f.d.Admin(t)
	check := func(sql string, want bool, args ...any) {
		t.Helper()
		_, err := admin.Exec(ctx, sql, args...)
		var pg *pgconn.PgError
		if refusedByCheck := errors.As(err, &pg) && pg.Code == "23514"; refusedByCheck != !want ||
			want && err != nil {
			t.Errorf("%s with %v: %v, want it taken: %t", sql, args, err, want)
		}
	}
	tr, el, ct := decodeID(t, ids.AttributeDef, trust), decodeID(t, ids.Component, elara),
		decodeID(t, ids.ComponentType, ship)
	for _, w := range ident.ReservedWords() {
		check("UPDATE attribute_def SET key = $1 WHERE id = $2", false, w, tr)
		check("UPDATE component SET key = $1 WHERE id = $2", false, w, el)
		check("UPDATE component_type SET key = $1 WHERE id = $2", false, w, ct)
	}
	for raw, want := range map[string]bool{"3": true, "3.5": false, `"3"`: false, "9007199254740992": false} {
		check("UPDATE attribute_def SET default_value = $1::jsonb WHERE id = $2", want, raw, tr)
	}
	check("UPDATE attribute_def SET type = 'enum', default_value = '\"a\"', enum_options = $1 WHERE id = $2", true,
		[]string{"a", "b"}, tr)
	for _, options := range [][]string{{}, {"a", "a"}, {""}, {strings.Repeat("a", 101)}} {
		check("UPDATE attribute_def SET enum_options = $1 WHERE id = $2", false, options, tr)
	}
	check("UPDATE attribute_def SET default_value = '\"c\"' WHERE id = $1", false, tr)
	check("UPDATE attribute_def SET type = 'text', enum_options = '{}', is_runtime = true WHERE id = $1", false, tr)
	check("UPDATE attribute_value SET value = $1::jsonb WHERE component_id = $2", false, "null", el)
	check("UPDATE attribute_value SET value = $1::jsonb WHERE component_id = $2", false, "1e400", el)
	for n, want := range map[int]bool{component.MaxNameLength: true, component.MaxNameLength + 1: false} {
		check("UPDATE component SET name = $1 WHERE id = $2", want, strings.Repeat("é", n), el)
		check("UPDATE component_type SET name = $1 WHERE id = $2", want, strings.Repeat("é", n), ct)
		check("UPDATE attribute_def SET name = $1 WHERE id = $2", want, strings.Repeat("é", n), tr)
	}
}

// TestPurge checks the trash's purge of components, attribute definitions,
// and types: a component and a definition go with their values once their
// times have passed, and a type once it holds no component either.
func TestPurge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	ship := f.newType(t, "Ship", nil)
	add, trust := newAttribute(ship, "Trust", value.Int, nil)
	gone, goneID := newAttribute(ship, "Gone", value.Int, nil)
	c, elara := newComponent(ship, "Elara", nil)
	keep, keepID := newComponent(ship, "Mira", nil)
	f.must(t, add, gone, c, keep)
	f.must(t, cmd{component.DeleteAttribute, map[string]any{"id": goneID}},
		cmd{component.Delete, map[string]any{"id": elara}}, cmd{component.Delete, map[string]any{"id": keepID}},
		cmd{component.DeleteType, map[string]any{"id": ship}})
	admin := f.d.Admin(t)
	// The type's time and Elara's have passed, Mira's has not.
	for table, id := range map[string]ids.UUID{"component_type": decodeID(t, ids.ComponentType, ship),
		"attribute_def": decodeID(t, ids.AttributeDef, goneID), "component": decodeID(t, ids.Component, elara)} {
		if _, err := admin.Exec(ctx, "UPDATE "+table+" SET deleted_at = now() - interval '31 days' WHERE id = $1",
			id); err != nil {
			t.Fatal(err)
		}
	}
	count := func(sql string, args ...any) int {
		t.Helper()
		var n int
		if err := admin.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if _, err := trash.Purge(ctx, f.db); err != nil {
		t.Fatal(err)
	}
	if n := count("SELECT count(*) FROM component WHERE project_id = $1", f.project); n != 1 {
		t.Errorf("after the purge, the project has %d components, want Mira's", n)
	}
	if n := count("SELECT count(*) FROM attribute_value WHERE attribute_def_id = $1",
		decodeID(t, ids.AttributeDef, goneID)); n != 0 {
		t.Errorf("the purged definition's values are left: %d", n)
	}
	if n := count("SELECT count(*) FROM component_type WHERE id = $1", decodeID(t, ids.ComponentType, ship)); n != 1 {
		t.Errorf("a type that holds a component in the trash was purged")
	}
	if _, err := admin.Exec(ctx, "UPDATE component SET deleted_at = now() - interval '31 days' WHERE id = $1",
		decodeID(t, ids.Component, keepID)); err != nil {
		t.Fatal(err)
	}
	if _, err := trash.Purge(ctx, f.db); err != nil {
		t.Fatal(err)
	}
	if n := count("SELECT count(*) FROM component_type WHERE id = $1", decodeID(t, ids.ComponentType, ship)); n != 0 {
		t.Errorf("a type that holds nothing once its time has passed was not purged")
	}
	if n := count("SELECT count(*) FROM attribute_def WHERE id = $1", decodeID(t, ids.AttributeDef, trust)); n != 0 {
		t.Errorf("a purged type's definitions are left")
	}
}

// TestComponentIsolation tries the package's queries from another
// workspace's transaction, where row-level security must keep them from
// reading or changing its components (NFR-SEC-01).
//
// Verifies: NFR-SEC-01
func TestComponentIsolation(t *testing.T) {
	t.Parallel()
	f := isolationtest.New(t, dbtest.New(t), isolationtest.Tables)
	uuid := func(id ids.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }
	one := func(id pgtype.UUID, err error) ([]ids.UUID, error) {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return []ids.UUID{id.Bytes}, err
	}
	for _, op := range []isolationtest.Op{
		{Name: "a component, in the trash or not", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			row, err := queries.New(tx).ComponentAny(ctx, queries.ComponentAnyParams{WorkspaceID: uuid(target.ID),
				ProjectID: uuid(target.Rows["project"]), ID: uuid(target.Rows["component"])})
			return one(row.Component.ID, err)
		}},
		{Name: "a type", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) ([]ids.UUID,
			error) {
			row, err := queries.New(tx).ComponentTypeAny(ctx, queries.ComponentTypeAnyParams{
				WorkspaceID: uuid(target.ID), ProjectID: uuid(target.Rows["project"]),
				ID: uuid(target.Rows["component_type"])})
			return one(row.ComponentType.ID, err)
		}},
		{Name: "an attribute definition", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			row, err := queries.New(tx).AttributeDefAny(ctx, queries.AttributeDefAnyParams{
				WorkspaceID: uuid(target.ID), ProjectID: uuid(target.Rows["project"]),
				ID: uuid(target.Rows["attribute_def"])})
			return one(row.AttributeDef.ID, err)
		}},
		{Name: "a definition's values", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			rows, err := queries.New(tx).AttributeValues(ctx, queries.AttributeValuesParams{
				WorkspaceID: uuid(target.ID), ProjectID: uuid(target.Rows["project"]),
				AttributeDefID: uuid(target.Rows["attribute_def"]), MaxRows: 10})
			var out []ids.UUID
			for _, r := range rows {
				out = append(out, r.ComponentID.Bytes)
			}
			return out, err
		}},
		{Name: "a definition's values with their components", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			rows, err := queries.New(tx).AttributeValueComponents(ctx, queries.AttributeValueComponentsParams{
				WorkspaceID: uuid(target.ID), ProjectID: uuid(target.Rows["project"]),
				AttributeDefID: uuid(target.Rows["attribute_def"]), MaxRows: 10})
			var out []ids.UUID
			for _, r := range rows {
				out = append(out, r.ComponentID.Bytes)
			}
			return out, err
		}},
		{Name: "a component's values", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			rows, err := queries.New(tx).ComponentValues(ctx, queries.ComponentValuesParams{
				WorkspaceID: uuid(target.ID), ProjectID: uuid(target.Rows["project"]),
				Ids: []pgtype.UUID{uuid(target.Rows["component"])}})
			var out []ids.UUID
			for _, r := range rows {
				out = append(out, r.ComponentID.Bytes)
			}
			return out, err
		}},
		{Name: "the component with a key", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			key := "elara_" + strings.ReplaceAll(target.Rows["component"].String(), "-", "")
			return one(queries.New(tx).ComponentWithKey(ctx, queries.ComponentWithKeyParams{
				WorkspaceID: uuid(target.ID), ProjectID: uuid(target.Rows["project"]), Key: key}))
		}},
		{Name: "rename a component", Run: func(ctx context.Context, tx pgx.Tx, target *isolationtest.Workspace) (
			[]ids.UUID, error) {
			id := uuid(target.Rows["component"])
			_, err := queries.New(tx).UpdateComponentName(ctx, queries.UpdateComponentNameParams{
				Name: "isolation " + time.Now().String(), WorkspaceID: uuid(target.ID),
				ProjectID: uuid(target.Rows["project"]), ID: id})
			return one(id, err)
		}},
		{Name: "touch a type's components", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			rows, err := queries.New(tx).TouchTypeComponents(ctx, queries.TouchTypeComponentsParams{
				WorkspaceID: uuid(target.ID), ProjectID: uuid(target.Rows["project"]),
				ComponentTypeID: uuid(target.Rows["component_type"])})
			var out []ids.UUID
			for _, r := range rows {
				out = append(out, r.ID.Bytes)
			}
			return out, err
		}},
		{Name: "set a definition's values", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			vals, _ := json.Marshal(map[string]int{target.Rows["component"].String(): 9})
			rows, err := queries.New(tx).SetAttributeValues(ctx, queries.SetAttributeValuesParams{Vals: vals,
				WorkspaceID: uuid(target.ID), ProjectID: uuid(target.Rows["project"]),
				AttributeDefID: uuid(target.Rows["attribute_def"])})
			var out []ids.UUID
			for _, r := range rows {
				out = append(out, r.Bytes)
			}
			return out, err
		}},
		{Name: "give a definition's values one value", Run: func(ctx context.Context, tx pgx.Tx,
			target *isolationtest.Workspace) ([]ids.UUID, error) {
			rows, err := queries.New(tx).SetAttributeValuesTo(ctx, queries.SetAttributeValuesToParams{
				Value: []byte("9"), WorkspaceID: uuid(target.ID), ProjectID: uuid(target.Rows["project"]),
				AttributeDefID: uuid(target.Rows["attribute_def"]),
				Ids:            []pgtype.UUID{uuid(target.Rows["component"])}})
			var out []ids.UUID
			for _, r := range rows {
				out = append(out, r.Bytes)
			}
			return out, err
		}},
	} {
		f.Check(t, op)
	}
}
