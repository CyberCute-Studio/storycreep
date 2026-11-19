package serializer_test

import (
	"bytes"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/flow"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemagen"
	"github.com/CyberCute-Studio/storycreep/server/internal/serializer"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// readTree returns every file under dir, by its path from dir, with forward
// slashes.
func readTree(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)], err = os.ReadFile(p)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// decode reads the file at path of files into v with published.Decode, or
// fails the test.
func decode(t *testing.T, files map[string][]byte, path string, v any) {
	t.Helper()
	data, ok := files[path]
	if !ok {
		t.Fatalf("there is no %s", path)
	}
	if err := published.Decode(data, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// orderKey returns a key of package orderkey that orders the i-th of its
// siblings after the ones before it.
func orderKey(i int) string {
	return fmt.Sprintf("%04d1", i)
}

// docOf returns the rich text whose segments are segs, nil for none: one
// paragraph, or several, of texts, mentions, values, and breaks, as the
// conformance suite's texts are, or fails the test on a list's item, which
// none of them holds.
func docOf(t *testing.T, segs richtext.Segments) *richtext.Doc {
	t.Helper()
	if len(segs) == 0 {
		return nil
	}
	p := &richtext.Paragraph{}
	doc := &richtext.Doc{Blocks: []richtext.Block{p}}
	for _, s := range segs {
		marks := richtext.Marks{Link: s.Href}
		for _, m := range s.Marks {
			switch m {
			case "bold":
				marks.Bold = true
			case "code":
				marks.Code = true
			case "italic":
				marks.Italic = true
			case "strike":
				marks.Strike = true
			}
		}
		switch s.Kind {
		case richtext.SegmentText:
			p.Inlines = append(p.Inlines, &richtext.Text{Text: s.Value, Marks: marks})
		case richtext.SegmentMention:
			p.Inlines = append(p.Inlines, &richtext.Mention{Component: s.Ref, Marks: marks})
		case richtext.SegmentValue:
			p.Inlines = append(p.Inlines, &richtext.Value{Ref: s.Ref, Attribute: s.Attribute, Decimals: s.Decimals,
				Marks: marks})
		case richtext.SegmentBreak:
			p.Inlines = append(p.Inlines, &richtext.Break{})
		case richtext.SegmentParagraph:
			if s.Depth != 0 {
				t.Fatalf("docOf reads no list's paragraph")
			}
			p = &richtext.Paragraph{}
			doc.Blocks = append(doc.Blocks, p)
		default:
			t.Fatalf("docOf reads no segment of the kind %s", s.Kind)
		}
	}
	return doc
}

// expressionOf returns the expression of a published condition or script,
// its source and its AST, or nil for none.
func expressionOf(source string, ast expr.Node) *serializer.Expression {
	return &serializer.Expression{Source: source, AST: ast}
}

// draftOf returns the draft that the snapshot of files was written of, read
// whole: each entity as Load reads it, each order as a key that orders it,
// each expression with its AST, and each text a rich text of its segments.
func draftOf(t *testing.T, files map[string][]byte) *serializer.Draft {
	t.Helper()
	var m published.Manifest
	decode(t, files, published.ManifestPath, &m)
	var settings published.Settings
	decode(t, files, published.SettingsPath, &settings)
	d := &serializer.Draft{Project: serializer.Project{ID: m.Project.ID, Name: m.Project.Name, Slug: m.Project.Slug}}
	if settings.Entry != nil {
		d.Project.EntryBoard, d.Project.EntryNode = settings.Entry.BoardID, settings.Entry.NodeID
	}
	hundredths := func(x float64) int64 { return int64(math.Round(x * 100)) }
	for _, ib := range m.Boards {
		var f published.Board
		decode(t, files, ib.File, &f)
		d.Boards = append(d.Boards, serializer.Board{ID: ib.ID, Slug: ib.Slug, Name: ib.Name,
			EntryNode: ib.EntryNodeID})
		for _, n := range f.Nodes {
			switch n := n.(type) {
			case *published.Element:
				node := serializer.Node{ID: n.ID, Board: ib.ID, Type: published.NodeElement, Kind: n.Kind,
					Title: n.Title, Speaker: n.Speaker, Cover: n.Cover, Tags: n.Tags, X: hundredths(n.Position.X),
					Y: hundredths(n.Position.Y), IsEnding: n.IsEnding}
				if n.Text != nil {
					node.Text = docOf(t, n.Text.Segments)
				}
				if n.OnEnter != nil {
					node.OnEnter = expressionOf(n.OnEnter.Source, n.OnEnter.AST)
				}
				if n.OnExit != nil {
					node.OnExit = expressionOf(n.OnExit.Source, n.OnExit.AST)
				}
				d.Nodes = append(d.Nodes, node)
			case *published.Branch:
				node := serializer.Node{ID: n.ID, Board: ib.ID, Type: published.NodeBranch, Title: n.Title,
					X: hundredths(n.Position.X), Y: hundredths(n.Position.Y)}
				if n.OnEnter != nil {
					node.OnEnter = expressionOf(n.OnEnter.Source, n.OnEnter.AST)
				}
				if n.OnExit != nil {
					node.OnExit = expressionOf(n.OnExit.Source, n.OnExit.AST)
				}
				d.Nodes = append(d.Nodes, node)
				for _, o := range n.Outputs {
					out := serializer.Output{ID: o.ID, Node: n.ID, Kind: o.Kind}
					if o.Kind != published.OutputElse {
						out.Order = orderKey(int(o.Order))
						out.Condition = expressionOf(o.Condition.Source, o.Condition.AST)
					}
					d.Outputs = append(d.Outputs, out)
				}
			case *published.Jumper:
				node := serializer.Node{ID: n.ID, Board: ib.ID, Type: published.NodeJumper, Title: n.Title,
					X: hundredths(n.Position.X), Y: hundredths(n.Position.Y)}
				if n.Target != nil {
					node.TargetBoard, node.TargetNode = n.Target.BoardID, n.Target.NodeID
				}
				d.Nodes = append(d.Nodes, node)
			}
		}
		for _, c := range f.Connections {
			conn := serializer.Connection{ID: c.ID, Board: ib.ID, From: c.From, To: c.To, Order: orderKey(int(c.Order))}
			if c.FromPort != "out" {
				conn.FromOutput = c.FromPort
			}
			if c.Label != nil {
				conn.Label = docOf(t, c.Label.Segments)
			}
			if c.Condition != nil {
				conn.Condition = expressionOf(c.Condition.Source, c.Condition.AST)
			}
			if c.OnTraverse != nil {
				conn.OnTraverse = expressionOf(c.OnTraverse.Source, c.OnTraverse.AST)
			}
			d.Connections = append(d.Connections, conn)
		}
	}
	var variables published.Variables
	decode(t, files, published.VariablesPath, &variables)
	for _, v := range variables.Variables {
		d.Variables = append(d.Variables, serializer.Variable{ID: v.ID, Key: v.Key, Scope: v.Scope, Board: v.BoardID,
			Type: v.Type, Default: v.Default, Description: v.Description, ResetOnEnter: v.ResetOnEnter})
	}
	var types published.ComponentTypes
	decode(t, files, published.ComponentTypesPath, &types)
	for _, ct := range types.ComponentTypes {
		d.ComponentTypes = append(d.ComponentTypes, serializer.ComponentType{ID: ct.ID, Key: ct.Key, Name: ct.Name})
		for i, a := range ct.Attributes {
			d.Attributes = append(d.Attributes, serializer.Attribute{ID: a.ID, ComponentType: ct.ID,
				Order: orderKey(i), Key: a.Key, Name: a.Name, Type: a.Type, Options: a.Options, Default: a.Default,
				Runtime: a.Runtime})
		}
	}
	var components published.Components
	decode(t, files, published.ComponentsPath, &components)
	for _, c := range components.Components {
		d.Components = append(d.Components, serializer.Component{ID: c.ID, ComponentType: c.ComponentTypeID,
			Key: c.Key, Name: c.Name, Image: c.Image, Values: map[string]value.Value(c.Attributes)})
	}
	return d
}

// stampOf returns the stamp of the manifest of files: its generator, and
// its release, or its draft's moment.
func stampOf(t *testing.T, files map[string][]byte) serializer.Stamp {
	t.Helper()
	var m published.Manifest
	decode(t, files, published.ManifestPath, &m)
	s := serializer.Stamp{Generator: m.Generator}
	var at published.Timestamp
	switch snap := m.Snapshot.(type) {
	case *published.ReleaseSnapshot:
		s.Release = &serializer.Release{ID: snap.ID, Version: snap.Version, Note: snap.Note}
		at = snap.PublishedAt
	case *published.DraftSnapshot:
		at = snap.PulledAt
	}
	moment, err := parseTimestamp(at)
	if err != nil {
		t.Fatal(err)
	}
	s.At = moment
	return s
}

// schemaOf returns the path of the document of the published format of the
// file at path, as /schema holds it.
func schemaOf(p string) string {
	name := strings.TrimSuffix(path.Base(p), ".json")
	switch {
	case p == published.ManifestPath:
		name = "storycreep"
	case strings.HasPrefix(p, "boards/"):
		name = "board"
	}
	return published.PublishedDir + "/" + name + ".schema.json"
}

// schemas validates files against the published format's JSON Schemas, with
// a validator of JSON Schema 2020-12 that is not schemagen's, as a consumer
// that reads the schemas does.
type schemas struct {
	c *jsonschema.Compiler
}

func newSchemas(t *testing.T) *schemas {
	t.Helper()
	docs, err := published.Files(false)
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	for p, data := range docs {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if err := c.AddResource(schemagen.BaseURL+p, doc); err != nil {
			t.Fatal(err)
		}
	}
	return &schemas{c: c}
}

// check fails the test for each file of files that its schema refuses, and
// for a snapshot that flow.Load, the reference runtime's loader, refuses,
// which checks what a consumer checks beyond the schemas.
func (s *schemas) check(t *testing.T, files map[string][]byte) {
	t.Helper()
	for p, data := range files {
		sch, err := s.c.Compile(schemagen.BaseURL + schemaOf(p))
		if err != nil {
			t.Fatalf("%s: %v", schemaOf(p), err)
		}
		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("%s is not JSON: %v", p, err)
		}
		if err := sch.Validate(inst); err != nil {
			t.Errorf("%s does not validate against its schema: %v", p, err)
		}
	}
	if _, err := flow.Load(mapFS(files)); err != nil {
		t.Errorf("the reference runtime does not load the snapshot: %v", err)
	}
}

// mapFS returns files as a file system, as a game's res://storycreep/ holds
// them.
func mapFS(files map[string][]byte) fstest.MapFS {
	fsys := fstest.MapFS{}
	for p, data := range files {
		fsys[p] = &fstest.MapFile{Data: data}
	}
	return fsys
}

// loadSnapshot loads files in the reference runtime, as a game would.
func loadSnapshot(files map[string][]byte) (*flow.Snapshot, error) {
	return flow.Load(mapFS(files))
}
