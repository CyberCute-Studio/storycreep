package diff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// Compare returns the diff of the snapshot whose files are from into the one
// whose files are to, each a snapshot's files by their paths, of version
// published.SchemaVersion of the format, as a game's res://storycreep/ holds
// them (§6.3), which it reads with published.Decode, and does not change. It
// compares the entities of the two by their IDs (§7.4):
//
//   - The project's own fields, its manifest's name and slug and its
//     settings' entry and signals; each variable; each component type, and
//     each of its attribute definitions, with its component type and its place
//     among the type's, from 0, as fields of its own, componentType and order;
//     each component, with the value of each attribute as a field of its own;
//     each board, by its manifest's entryNodeId, name, and slug; each node;
//     each branch output, with its branch as a field, branch; and each
//     connection: each by every field of the format that its file holds it
//     with but its ID, a branch's outputs, a component type's attribute
//     definitions, and a component's attributes, which are entities and
//     fields of their own.
//   - An entity that only to has was added, and one that only from has
//     removed. One that both have was moved, if it is a node or a connection
//     on different boards; otherwise it was modified if a field differs, a
//     field that one of them has and the other does not among them, and is
//     cosmetic where only its position does.
//   - Two values of a field are the same where their JSON is, but that each
//     value of a declaration's type is read as that type (value.Value.Read),
//     so that two numbers are the same where they are one number, as 2 and
//     2.0 of an int. A field's value is written as its file holds it, but a
//     rich text's, an element's text or a connection's label, which is its
//     plain text (richtext.PlainText), with the Words of the two, and an
//     expression's, a condition or a script, which is its source, with
//     whether the variables, components' attributes, and nodes that its AST
//     names differ (refsChanged).
//
// A file whose bytes are the same on both sides holds nothing that changed,
// so Compare reads it on neither, as an entity that moved off a board's file,
// or onto it, changed it; and of the other boards' files it reads into the
// format's types only the nodes and connections whose bytes differ, or that
// one side alone has, or that are on different boards (boards), so that two
// releases of a large project that differ in a few hundred entities compare
// in a fraction of a second (NFR-PERF-13). It compares snapshots that
// validate (schemaversion.Validate), and fails where what it reads of either
// side is not of the format's version, as published.Decode refuses it, where
// its manifest's index names a board whose file it does not have, or where it
// has an entity's ID twice.
func Compare(from, to map[string][]byte) (*Diff, error) {
	d, err := compareFiles(from, to)
	if err != nil {
		return nil, err
	}
	d.fill()
	return d, nil
}

// CompareSized returns the diff of from into to as Compare does, sized for
// display as Truncate sizes it to at most limit changes, of which alone it
// computes the word-level diffs of their rich texts, so that a diff of many
// changes spends no time on the words of those that it leaves out.
func CompareSized(from, to map[string][]byte, limit int) (*Diff, error) {
	d, err := compareFiles(from, to)
	if err != nil {
		return nil, err
	}
	return d.Truncate(limit)
}

// compareFiles returns the diff of from into to as Compare does, but that a
// change of a rich text holds its two plain texts, and not yet their Words,
// which fill computes.
func compareFiles(from, to map[string][]byte) (*Diff, error) {
	f, err := read(from)
	if err != nil {
		return nil, fmt.Errorf("diff: the from side: %w", err)
	}
	t, err := read(to)
	if err != nil {
		return nil, fmt.Errorf("diff: the to side: %w", err)
	}
	c := &comparison{from: f, to: t, d: &Diff{
		Boards:        []BoardChanges{},
		From:          f.ref(),
		Project:       ProjectChanges{Attributes: []EntityChange{}, ComponentTypes: []EntityChange{}, Components: []EntityChange{}, Fields: []FieldChange{}, Variables: []EntityChange{}},
		SchemaVersion: published.SchemaVersion,
		To:            t.ref(),
	}}
	for _, step := range []func() error{c.project, c.variables, c.componentTypes, c.components, c.boards} {
		if err := step(); err != nil {
			return nil, err
		}
	}
	return c.d, nil
}

// side is a side's files, its manifest, its boards by ID, and its settings,
// and its component types and their attribute definitions once it has read
// them (definitions).
type side struct {
	files    map[string][]byte
	manifest published.Manifest
	boards   map[string]published.ManifestBoard
	settings published.Settings
	types    map[string]published.ComponentType
	defs     map[string]published.AttributeDefinition
}

// read returns the side whose files are files, with its manifest and settings
// decoded.
func read(files map[string][]byte) (*side, error) {
	s := &side{files: files, boards: map[string]published.ManifestBoard{}}
	if err := s.decode(published.ManifestPath, &s.manifest); err != nil {
		return nil, err
	}
	if err := s.decode(published.SettingsPath, &s.settings); err != nil {
		return nil, err
	}
	for _, b := range s.manifest.Boards {
		if _, ok := files[b.File]; !ok {
			return nil, fmt.Errorf("the manifest names %s, which the snapshot does not have", b.File)
		}
		if _, ok := s.boards[b.ID]; ok {
			return nil, fmt.Errorf("the manifest has the board %s twice", b.ID)
		}
		s.boards[b.ID] = b
	}
	return s, nil
}

// decode reads the side's file at path into v.
func (s *side) decode(path string, v any) error {
	data, ok := s.files[path]
	if !ok {
		return fmt.Errorf("the snapshot has no %s", path)
	}
	if err := published.Decode(data, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// ref returns the side as a diff names it, as its manifest's snapshot has it.
func (s *side) ref() DiffSide {
	switch snap := s.manifest.Snapshot.(type) {
	case *published.ReleaseSnapshot:
		return DiffSide{Kind: SideSnapshot, ID: snap.ID, Version: snap.Version, ContentHash: string(snap.ContentHash)}
	case *published.DraftSnapshot:
		return DiffSide{Kind: SideDraft, ContentHash: string(snap.ContentHash)}
	}
	return DiffSide{}
}

// boardRef returns the board id of the side as a diff names it.
func (s *side) boardRef(id string) *BoardRef {
	b := s.boards[id]
	return &BoardRef{ID: b.ID, Name: b.Name, Slug: b.Slug}
}

// same reports whether the file at path is the same, byte for byte, on both
// sides, which holds nothing that changed.
func (c *comparison) same(fromPath, toPath string) bool {
	f, ok := c.from.files[fromPath]
	t, ok2 := c.to.files[toPath]
	return ok && ok2 && bytes.Equal(f, t)
}

// comparison is a diff that Compare is making of from into to.
type comparison struct {
	from, to *side
	d        *Diff
}

// field is a field of an entity: its JSON, by which two values of it are
// compared, and what a FieldChange writes of it, its JSON, a rich text's
// plain text, or an expression's source, with the plain text of a rich text
// and the references of an expression.
type field struct {
	raw  json.RawMessage
	show any
	text *string
	expr bool
	refs []string
}

// fields are an entity's fields, by name, the values of a component's
// attributes by the name attributes and the attribute's ID (attributeField).
type fields map[string]field

// attributePrefix is what the name in fields of the value of a component's
// attribute starts with, before the attribute definition's ID.
const attributePrefix = "attributes\x00"

// attributeField returns the name in fields of the value of the attribute
// definition id of a component.
func attributeField(id string) string {
	return attributePrefix + id
}

// entity is an entity of a side: its board, of a node, a branch output, or a
// connection, the fields that it is compared by, and what a change shows of
// it (EntityChange, without its change).
type entity struct {
	board  string
	fields fields
	show   EntityChange
}

// compare returns the change of the entity id of the kind kind from f into
// t, either of which may be missing, or false if it did not change.
func compare(kind, id string, f, t *entity) (EntityChange, bool) {
	switch {
	case f == nil:
		e := t.show
		e.Kind, e.ID, e.Change = kind, id, Added
		return e, true
	case t == nil:
		e := f.show
		e.Kind, e.ID, e.Change = kind, id, Removed
		return e, true
	}
	e := t.show
	e.Kind, e.ID = kind, id
	e.Fields = changes(f.fields, t.fields)
	switch {
	// A branch output is on its branch's board, whose move is the branch's:
	// the output itself is modified, if at all.
	case f.board != t.board && kind != KindBranchOutput:
		e.Change = Moved
	case len(e.Fields) == 0:
		return EntityChange{}, false
	default:
		e.Change = Modified
		e.Cosmetic = !slices.ContainsFunc(e.Fields, func(c FieldChange) bool { return c.Field != "position" })
	}
	return e, true
}

// changes returns the changes of the fields from into to, sorted by name, a
// component's attributes by their IDs, or nil for none.
func changes(from, to fields) []FieldChange {
	var out []FieldChange
	for _, n := range unionOf(from, to) {
		f, inFrom := from[n]
		t, inTo := to[n]
		if inFrom && inTo && bytes.Equal(f.raw, t.raw) {
			continue
		}
		c := FieldChange{Field: n}
		if attr, ok := cutAttribute(n); ok {
			c.Field, c.Attribute = "attributes", attr
		}
		if inFrom {
			c.Before = f.show
		}
		if inTo {
			c.After = t.show
		}
		if f.text != nil || t.text != nil {
			c.texts = textsOf(f.text, t.text)
		}
		if f.expr || t.expr {
			changed := !slices.Equal(f.refs, t.refs)
			c.RefsChanged = &changed
		}
		out = append(out, c)
	}
	return out
}

// cutAttribute returns the attribute definition's ID of name, a component's
// attribute's name in fields, and true, or false for another field's.
func cutAttribute(name string) (string, bool) {
	return strings.CutPrefix(name, attributePrefix)
}

// textsOf returns the plain texts before and after, either of which may be
// missing, as an empty text, whose Words a FieldChange computes when it is
// listed (FieldChange.fill).
func textsOf(before, after *string) *plainTexts {
	var p plainTexts
	if before != nil {
		p.before = *before
	}
	if after != nil {
		p.after = *after
	}
	return &p
}

// objectOf returns v's JSON as a JSON object's members, without those of the
// names drop: v, of the published format's types, as published.Decode read
// it, holds only the keys that the format defines, and encoding/json leaves
// out an optional field whose value is empty, as a resetOnEnter of false or
// tags of [], so that a field is one that the entity has as the format reads
// it (schema/diff-corpus/README.md).
func objectOf(v any, drop ...string) (fields, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	out := make(fields, len(m))
	for k, raw := range m {
		if !slices.Contains(drop, k) {
			out[k] = field{raw: raw, show: raw}
		}
	}
	return out, nil
}

// set sets the field name to v's JSON.
func (fs fields) set(name string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	fs[name] = field{raw: raw, show: json.RawMessage(raw)}
	return nil
}

// richText marks the field name, a rich text of the segments segs, if it is
// one of fs's, as one whose value is shown as its plain text, with the words
// of its change.
func (fs fields) richText(name string, segs richtext.Segments) {
	f, ok := fs[name]
	if !ok {
		return
	}
	plain := richtext.PlainText(segs)
	f.show, f.text = plain, &plain
	fs[name] = f
}

// expression marks the field name, an expression of the AST ast and the source
// source, if it is one of fs's, as one whose value is shown as its source,
// with whether its references changed.
func (fs fields) expression(name string, ast expr.Node, source string) {
	f, ok := fs[name]
	if !ok {
		return
	}
	f.show, f.expr, f.refs = source, true, refsOf(ast)
	fs[name] = f
}

// refsOf returns what the AST n names, each once, sorted: the ID of each
// variable and node that it names, and of each component's attribute, the
// component's ID, a period, and the attribute definition's.
func refsOf(n expr.Node) []string {
	set := map[string]bool{}
	expr.Walk(n, func(c expr.Node) {
		switch c := c.(type) {
		case *expr.Var:
			set[c.Ref] = true
		case *expr.Attr:
			set[c.Ref+"."+c.Attribute] = true
		case *expr.NodeRef:
			set[c.Ref] = true
		}
	})
	return slices.Sorted(maps.Keys(set))
}

// typed returns v, a value of the type typ, of an enum's options, as that
// type reads it, in its canonical form, by which two values of a declaration
// are compared, or v itself where it is not one, as a value of a declaration
// that the side does not have.
func typed(v value.Value, typ string, options []string) value.Value {
	if out, problem := v.Read(typ, value.Checks{}, options...); problem == "" {
		return out
	}
	return v
}

// project adds the changes of the project's own fields.
func (c *comparison) project() error {
	of := func(s *side) (fields, error) {
		fs := fields{}
		signals := s.settings.Signals
		if signals == nil {
			signals = []string{}
		}
		for name, v := range map[string]any{"name": s.manifest.Project.Name, "slug": s.manifest.Project.Slug,
			"signals": signals} {
			if err := fs.set(name, v); err != nil {
				return nil, err
			}
		}
		if s.settings.Entry != nil {
			if err := fs.set("entry", s.settings.Entry); err != nil {
				return nil, err
			}
		}
		return fs, nil
	}
	f, err := of(c.from)
	if err != nil {
		return err
	}
	t, err := of(c.to)
	if err != nil {
		return err
	}
	if ch := changes(f, t); ch != nil {
		c.d.Project.Fields = ch
		c.d.Summary.count(KindProject, Modified, false)
	}
	return nil
}

// list adds to *out the changes of the entities of the kind kind, from from
// into to, by their IDs, sorted by ID, and counts them.
func (c *comparison) list(out *[]EntityChange, kind string, from, to map[string]*entity) {
	for _, id := range unionOf(from, to) {
		if e, ok := compare(kind, id, from[id], to[id]); ok {
			*out = append(*out, e)
			c.d.Summary.count(kind, e.Change, e.Cosmetic)
		}
	}
}

// unionOf returns the IDs of a and b, each once, sorted.
func unionOf[V any](a, b map[string]V) []string {
	ids := slices.Collect(maps.Keys(a))
	for id := range b {
		if _, ok := a[id]; !ok {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

// variables adds the changes of the variables.
func (c *comparison) variables() error {
	if c.same(published.VariablesPath, published.VariablesPath) {
		return nil
	}
	of := func(s *side) (map[string]*entity, error) {
		var file published.Variables
		if err := s.decode(published.VariablesPath, &file); err != nil {
			return nil, err
		}
		out := map[string]*entity{}
		for _, v := range file.Variables {
			if _, ok := out[v.ID]; ok {
				return nil, fmt.Errorf("%s has the variable %s twice", published.VariablesPath, v.ID)
			}
			v.Default = typed(v.Default, v.Type, nil)
			fs, err := objectOf(v, "id")
			if err != nil {
				return nil, err
			}
			out[v.ID] = &entity{fields: fs, show: EntityChange{Key: v.Key, Type: v.Type}}
		}
		return out, nil
	}
	f, err := of(c.from)
	if err != nil {
		return err
	}
	t, err := of(c.to)
	if err != nil {
		return err
	}
	c.list(&c.d.Project.Variables, KindVariable, f, t)
	return nil
}

// definitions returns the side's component types, by ID, and the
// definitions of their attributes, by ID, as component_types.json holds them.
func (s *side) definitions() (map[string]published.ComponentType, map[string]published.AttributeDefinition, error) {
	if s.types != nil {
		return s.types, s.defs, nil
	}
	var file published.ComponentTypes
	if err := s.decode(published.ComponentTypesPath, &file); err != nil {
		return nil, nil, err
	}
	types := map[string]published.ComponentType{}
	defs := map[string]published.AttributeDefinition{}
	for _, t := range file.ComponentTypes {
		if _, ok := types[t.ID]; ok {
			return nil, nil, fmt.Errorf("%s has the component type %s twice", published.ComponentTypesPath, t.ID)
		}
		types[t.ID] = t
		for _, a := range t.Attributes {
			if _, ok := defs[a.ID]; ok {
				return nil, nil, fmt.Errorf("%s has the attribute definition %s twice", published.ComponentTypesPath,
					a.ID)
			}
			defs[a.ID] = a
		}
	}
	s.types, s.defs = types, defs
	return types, defs, nil
}

// componentTypes adds the changes of the component types and of their
// attribute definitions.
func (c *comparison) componentTypes() error {
	if c.same(published.ComponentTypesPath, published.ComponentTypesPath) {
		return nil
	}
	of := func(s *side) (types, attributes map[string]*entity, err error) {
		all, _, err := s.definitions()
		if err != nil {
			return nil, nil, err
		}
		types, attributes = map[string]*entity{}, map[string]*entity{}
		for id, t := range all {
			fs, err := objectOf(t, "id", "attributes")
			if err != nil {
				return nil, nil, err
			}
			types[id] = &entity{fields: fs, show: EntityChange{Key: t.Key, Name: t.Name}}
			for i, a := range t.Attributes {
				a.Default = typed(a.Default, a.Type, a.Options)
				fs, err := objectOf(a, "id")
				if err != nil {
					return nil, nil, err
				}
				if err := fs.set("componentType", t.ID); err != nil {
					return nil, nil, err
				}
				if err := fs.set("order", i); err != nil {
					return nil, nil, err
				}
				attributes[a.ID] = &entity{fields: fs, show: EntityChange{ComponentType: t.ID, Key: a.Key,
					Name: a.Name}}
			}
		}
		return types, attributes, nil
	}
	fTypes, fAttributes, err := of(c.from)
	if err != nil {
		return err
	}
	tTypes, tAttributes, err := of(c.to)
	if err != nil {
		return err
	}
	c.list(&c.d.Project.ComponentTypes, KindComponentType, fTypes, tTypes)
	c.list(&c.d.Project.Attributes, KindAttribute, fAttributes, tAttributes)
	return nil
}

// components adds the changes of the components, each attribute's value
// read as its definition's type.
func (c *comparison) components() error {
	if c.same(published.ComponentsPath, published.ComponentsPath) {
		return nil
	}
	of := func(s *side) (map[string]*entity, error) {
		_, defs, err := s.definitions()
		if err != nil {
			return nil, err
		}
		var file published.Components
		if err := s.decode(published.ComponentsPath, &file); err != nil {
			return nil, err
		}
		out := map[string]*entity{}
		for _, cmp := range file.Components {
			if _, ok := out[cmp.ID]; ok {
				return nil, fmt.Errorf("%s has the component %s twice", published.ComponentsPath, cmp.ID)
			}
			fs, err := objectOf(cmp, "id", "attributes")
			if err != nil {
				return nil, err
			}
			for id, v := range cmp.Attributes {
				d := defs[id]
				if err := fs.set(attributeField(id), typed(v, d.Type, d.Options)); err != nil {
					return nil, err
				}
			}
			out[cmp.ID] = &entity{fields: fs, show: EntityChange{Key: cmp.Key, Name: cmp.Name}}
		}
		return out, nil
	}
	f, err := of(c.from)
	if err != nil {
		return err
	}
	t, err := of(c.to)
	if err != nil {
		return err
	}
	c.list(&c.d.Project.Components, KindComponent, f, t)
	return nil
}

// element is a node or a connection of a side, as its board's file holds it:
// its board, and its JSON's bytes.
type element struct {
	board string
	raw   []byte
}

// contents are the nodes, branch outputs, and connections of a side that a
// comparison reads into the published format's types, by ID.
type contents struct {
	nodes, outputs, connections map[string]*entity
}

func newContents() contents {
	return contents{nodes: map[string]*entity{}, outputs: map[string]*entity{}, connections: map[string]*entity{}}
}

// boards adds the changes of the boards and of what they hold. It reads the
// boards' files whose bytes differ between the two sides, or that one side
// alone has, for the bytes of each node and connection (elements), and reads
// into the published format's types only those that one side alone has, that
// are on different boards, or whose bytes differ, and the branch outputs of
// those, since a node or a connection of the same bytes on the same board has
// the same fields, and a branch the same outputs, and every other has a
// change.
func (c *comparison) boards() error {
	var changed []string
	for _, id := range unionOf(c.from.boards, c.to.boards) {
		f, inFrom := c.from.boards[id]
		t, inTo := c.to.boards[id]
		if inFrom && inTo && c.same(f.File, t.File) {
			continue
		}
		changed = append(changed, id)
	}
	fromNodes, fromConnections, err := c.from.elements(changed)
	if err != nil {
		return fmt.Errorf("diff: the from side: %w", err)
	}
	toNodes, toConnections, err := c.to.elements(changed)
	if err != nil {
		return fmt.Errorf("diff: the to side: %w", err)
	}
	from, to := newContents(), newContents()
	for _, k := range []struct {
		from, to map[string]element
		read     func(e element, into contents) error
	}{
		{fromNodes, toNodes, readNode},
		{fromConnections, toConnections, readConnection},
	} {
		for _, id := range unionOf(k.from, k.to) {
			f, inFrom := k.from[id]
			t, inTo := k.to[id]
			if inFrom && inTo && f.board == t.board && bytes.Equal(f.raw, t.raw) {
				continue
			}
			if inFrom {
				if err := k.read(f, from); err != nil {
					return fmt.Errorf("diff: the from side: %w", err)
				}
			}
			if inTo {
				if err := k.read(t, to); err != nil {
					return fmt.Errorf("diff: the to side: %w", err)
				}
			}
		}
	}

	// A board's group names it as the to side has it, or as the from side
	// had it where the to side does not have it.
	groups := map[string]*BoardChanges{}
	group := func(board string) *BoardChanges {
		g, ok := groups[board]
		if !ok {
			s := c.to
			if _, inTo := c.to.boards[board]; !inTo {
				s = c.from
			}
			g = &BoardChanges{Board: *s.boardRef(board), Changes: []EntityChange{}}
			groups[board] = g
		}
		return g
	}
	for _, id := range changed {
		f, inFrom := c.from.boards[id]
		t, inTo := c.to.boards[id]
		var change string
		var fs []FieldChange
		switch {
		case !inFrom:
			change = Added
		case !inTo:
			change = Removed
		default:
			ff, err := objectOf(f, "id", "file")
			if err != nil {
				return err
			}
			tf, err := objectOf(t, "id", "file")
			if err != nil {
				return err
			}
			if fs = changes(ff, tf); fs != nil {
				change = Modified
			}
		}
		if change != "" {
			g := group(id)
			g.Change, g.Fields = change, fs
			c.d.Summary.count(KindBoard, change, false)
		}
	}
	for _, k := range []struct {
		kind     string
		from, to map[string]*entity
	}{
		{KindNode, from.nodes, to.nodes},
		{KindBranchOutput, from.outputs, to.outputs},
		{KindConnection, from.connections, to.connections},
	} {
		for _, id := range unionOf(k.from, k.to) {
			f, t := k.from[id], k.to[id]
			e, ok := compare(k.kind, id, f, t)
			if !ok {
				continue
			}
			board := ""
			if t != nil {
				board = t.board
			} else {
				board = f.board
			}
			if e.Change == Moved {
				e.FromBoard, e.ToBoard = c.from.boardRef(f.board), c.to.boardRef(t.board)
			}
			g := group(board)
			g.Changes = append(g.Changes, e)
			c.d.Summary.count(k.kind, e.Change, e.Cosmetic)
		}
	}
	for _, id := range slices.Sorted(maps.Keys(groups)) {
		c.d.Boards = append(c.d.Boards, *groups[id])
	}
	return nil
}

// elements returns the nodes and the connections of the side's boards of the
// IDs ids that it has, by their IDs, as their boards' files hold them.
func (s *side) elements(ids []string) (nodes, connections map[string]element, err error) {
	nodes, connections = map[string]element{}, map[string]element{}
	for _, id := range ids {
		mb, ok := s.boards[id]
		if !ok {
			continue
		}
		var lists map[string][]byte
		if !eachMember(s.files[mb.File], func(key string, value []byte) bool {
			if key == "nodes" || key == "connections" {
				if lists == nil {
					lists = map[string][]byte{}
				}
				lists[key] = value
			}
			return true
		}) {
			return nil, nil, fmt.Errorf("%s is no JSON object", mb.File)
		}
		for _, k := range []struct {
			key  string
			into map[string]element
		}{{"nodes", nodes}, {"connections", connections}} {
			items, ok := elementsOf(lists[k.key])
			if !ok {
				return nil, nil, fmt.Errorf("%s has no array of %s", mb.File, k.key)
			}
			for _, raw := range items {
				eid, ok := idOf(raw)
				if !ok {
					return nil, nil, fmt.Errorf("%s has one of its %s without an ID", mb.File, k.key)
				}
				if _, ok := k.into[eid]; ok {
					return nil, nil, fmt.Errorf("the snapshot has %s twice", eid)
				}
				k.into[eid] = element{board: id, raw: raw}
			}
		}
	}
	return nodes, connections, nil
}

// readNode reads the node e into the nodes of into, and a branch's outputs
// into its outputs.
func readNode(e element, into contents) error {
	var ns published.Nodes
	if err := published.Decode(slices.Concat([]byte("["), e.raw, []byte("]")), &ns); err != nil {
		return fmt.Errorf("a node of %s: %w", e.board, err)
	}
	n := ns[0]
	ent, err := nodeEntity(n)
	if err != nil {
		return err
	}
	ent.board = e.board
	into.nodes[n.NodeID()] = ent
	br, ok := n.(*published.Branch)
	if !ok {
		return nil
	}
	for _, o := range br.Outputs {
		if _, ok := into.outputs[o.ID]; ok {
			return fmt.Errorf("the snapshot has the branch output %s twice", o.ID)
		}
		fs, err := objectOf(o, "id")
		if err != nil {
			return err
		}
		if err := fs.set("branch", br.ID); err != nil {
			return err
		}
		if o.Condition != nil {
			fs.expression("condition", o.Condition.AST, o.Condition.Source)
		}
		into.outputs[o.ID] = &entity{board: e.board, fields: fs, show: EntityChange{Branch: br.ID}}
	}
	return nil
}

// readConnection reads the connection e into the connections of into.
func readConnection(e element, into contents) error {
	var cs []published.Connection
	if err := published.Decode(slices.Concat([]byte("["), e.raw, []byte("]")), &cs); err != nil {
		return fmt.Errorf("a connection of %s: %w", e.board, err)
	}
	cn := cs[0]
	fs, err := objectOf(cn, "id")
	if err != nil {
		return err
	}
	show := EntityChange{From: cn.From, To: cn.To}
	if cn.Label != nil {
		fs.richText("label", cn.Label.Segments)
		show.Label = richtext.PlainText(cn.Label.Segments)
	}
	if cn.Condition != nil {
		fs.expression("condition", cn.Condition.AST, cn.Condition.Source)
	}
	if cn.OnTraverse != nil {
		fs.expression("onTraverse", cn.OnTraverse.AST, cn.OnTraverse.Source)
	}
	into.connections[cn.ID] = &entity{board: e.board, fields: fs, show: show}
	return nil
}

// nodeEntity returns the node n as an entity, without its board, and without
// a branch's outputs, which are entities of their own.
func nodeEntity(n published.Node) (*entity, error) {
	fs, err := objectOf(n, "id", "outputs")
	if err != nil {
		return nil, err
	}
	var show EntityChange
	script := func(name string, s *published.Script) {
		if s != nil {
			fs.expression(name, s.AST, s.Source)
		}
	}
	switch n := n.(type) {
	case *published.Element:
		show = EntityChange{Type: published.NodeElement, Title: n.Title}
		if n.Text != nil {
			fs.richText("text", n.Text.Segments)
			show.Preview = n.Text.Preview
		}
		script("onEnter", n.OnEnter)
		script("onExit", n.OnExit)
	case *published.Branch:
		show = EntityChange{Type: published.NodeBranch, Title: n.Title}
		script("onEnter", n.OnEnter)
		script("onExit", n.OnExit)
	case *published.Jumper:
		show = EntityChange{Type: published.NodeJumper, Title: n.Title}
	}
	return &entity{fields: fs, show: show}, nil
}
