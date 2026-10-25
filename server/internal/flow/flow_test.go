package flow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
)

// snapshotFS returns the files of the suite s as a file system, each file of
// path changed by the function of edits, which gets its JSON and returns the
// JSON to write, and the manifest's hashes of the files written again, so
// that Load reads past the hashes to what the edits change.
func snapshotFS(t *testing.T, s *suite, edits map[string]func(map[string]any)) fstest.MapFS {
	t.Helper()
	files, err := s.files()
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(files[published.ManifestPath], &manifest); err != nil {
		t.Fatal(err)
	}
	fsys := fstest.MapFS{}
	for path, data := range files {
		if edit := edits[path]; edit != nil && path != published.ManifestPath {
			var v map[string]any
			if err := json.Unmarshal(data, &v); err != nil {
				t.Fatal(err)
			}
			edit(v)
			if data, err = json.Marshal(v); err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(data)
			manifest["files"].(map[string]any)[path] = "sha256:" + hex.EncodeToString(sum[:])
		}
		fsys[path] = &fstest.MapFile{Data: data}
	}
	if edit := edits[published.ManifestPath]; edit != nil {
		edit(manifest)
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	fsys[published.ManifestPath] = &fstest.MapFile{Data: data}
	return fsys
}

// nodeOf returns the node of a board file's JSON whose ID is id.
func nodeOf(board map[string]any, id string) map[string]any {
	for _, n := range board["nodes"].([]any) {
		if n.(map[string]any)["id"] == id {
			return n.(map[string]any)
		}
	}
	panic("no node " + id)
}

// connectionOf returns the connection of a board file's JSON whose ID is id.
func connectionOf(board map[string]any, id string) map[string]any {
	for _, c := range board["connections"].([]any) {
		if c.(map[string]any)["id"] == id {
			return c.(map[string]any)
		}
	}
	panic("no connection " + id)
}

// TestLoad checks that Load refuses a snapshot that a runtime cannot run as
// the files say, at each of the checks that its doc lists, and loads every
// suite's snapshot as it is.
func TestLoad(t *testing.T) {
	t.Parallel()
	for _, s := range suites() {
		if _, err := Load(snapshotFS(t, s, nil)); err != nil {
			t.Errorf("%s: %v", s.name, err)
		}
	}
	main, roll, harbour := published.BoardPath("main"), published.BoardPath("roll"), published.BoardPath("harbour")
	for _, c := range []struct {
		name  string
		suite *suite
		path  string
		edit  func(map[string]any)
		want  string
	}{
		{"a newer astVersion", choicesSuite(), published.ManifestPath,
			func(m map[string]any) { m["astVersion"] = expr.ASTVersion + 1 }, "ast_unsupported"},
		{"a file's hash", choicesSuite(), published.ManifestPath, func(m map[string]any) {
			m["files"].(map[string]any)[main] = "sha256:" + strings.Repeat("0", 64)
		}, "its hash"},
		{"a file that the manifest does not list", choicesSuite(), published.ManifestPath, func(m map[string]any) {
			delete(m["files"].(map[string]any), main)
		}, "its hash"},
		{"a file that does not decode", choicesSuite(), main, func(b map[string]any) { b["schemaVersion"] = 2 },
			"schemaVersion"},
		{"a board's file of another board", choicesSuite(), main, func(b map[string]any) {
			b["board"].(map[string]any)["entryNodeId"] = nd("MARKET")
		}, "not the index's"},
		{"the settings' entry", choicesSuite(), published.SettingsPath, func(f map[string]any) {
			f["entry"].(map[string]any)["nodeId"] = nd("BYE")
		}, "not the settings'"},
		{"an entry of no node", choicesSuite(), published.ManifestPath, func(m map[string]any) {
			m["entry"].(map[string]any)["nodeId"] = nd("NADA")
		}, "not the settings'"},
		{"a connection to a node of no board", choicesSuite(), main, func(b map[string]any) {
			connectionOf(b, cn("PAY"))["to"] = nd("NADA")
		}, "does not join two nodes"},
		{"a connection of no port", choicesSuite(), main, func(b map[string]any) {
			connectionOf(b, cn("PAY"))["fromPort"] = bo("NADA")
		}, "no port of it"},
		{"a port's connections out of order", choicesSuite(), main, func(b map[string]any) {
			connectionOf(b, cn("PAY"))["order"] = 1
		}, "not ordered"},
		{"a branch's outputs out of order", branchesSuite(), roll, func(b map[string]any) {
			nodeOf(b, nd("GATE"))["outputs"].([]any)[0].(map[string]any)["order"] = 1
		}, "outputs are not ordered"},
		{"a jumper to a jumper", jumpersSuite(), harbour, func(b map[string]any) {
			nodeOf(b, nd("JSD"))["target"] = map[string]any{"boardId": bd("HRB"), "nodeId": nd("JBC")}
		}, "targets the jumper"},
		{"a jumper to a node of another board than its target's", jumpersSuite(), harbour, func(b map[string]any) {
			nodeOf(b, nd("JSD"))["target"] = map[string]any{"boardId": bd("BEACN"), "nodeId": nd("HSD")}
		}, "is not a node of its board"},
		{"an AST of a variable that the snapshot does not have", choicesSuite(), main, func(b map[string]any) {
			connectionOf(b, cn("DEPART"))["condition"].(map[string]any)["ast"].(map[string]any)["ref"] = vr("NADA")
		}, "condition of"},
		{"an AST that does not type-check", choicesSuite(), main, func(b map[string]any) {
			connectionOf(b, cn("DEPART"))["condition"].(map[string]any)["ast"].(map[string]any)["ref"] = vr("CASH")
		}, "type_mismatch"},
		{"another board's variable", jumpersSuite(), harbour, func(b map[string]any) {
			n := nodeOf(b, nd("HSD"))["onEnter"].(map[string]any)["ast"].(map[string]any)
			n["statements"].([]any)[0].(map[string]any)["target"].(map[string]any)["ref"] = vr("BTMP")
		}, "board_variable_out_of_scope"},
		{"a text's value of no variable", choicesSuite(), main, func(b map[string]any) {
			segs := nodeOf(b, nd("START"))["text"].(map[string]any)["segments"].([]any)
			segs[1].(map[string]any)["ref"] = vr("NADA")
		}, "no variable of the snapshot"},
		{"a label's value of an attribute that is not runtime", choicesSuite(), main, func(b map[string]any) {
			segs := nodeOf(b, nd("CHAT"))["text"].(map[string]any)["segments"].([]any)
			segs[2].(map[string]any)["attribute"] = at("TAG")
		}, "no runtime attribute"},
		{"a default of another type", choicesSuite(), published.VariablesPath, func(f map[string]any) {
			f["variables"].([]any)[0].(map[string]any)["default"] = "x"
		}, "variables.json"},
		{"a component without a value of a definition", choicesSuite(), published.ComponentsPath, func(f map[string]any) {
			delete(f["components"].([]any)[0].(map[string]any)["attributes"].(map[string]any), at("TAG"))
		}, "one of each"},
		{"a component of an enum's other option", choicesSuite(), published.ComponentsPath, func(f map[string]any) {
			f["components"].([]any)[0].(map[string]any)["attributes"].(map[string]any)[at("MD")] = "sad"
		}, "the value of"},
	} {
		_, err := Load(snapshotFS(t, c.suite, map[string]func(map[string]any){c.path: c.edit}))
		if !errors.Is(err, ErrLoad) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v; want an error of ErrLoad that says %q", c.name, err, c.want)
		}
	}
}

// loaded returns the snapshot of the suite s.
func loaded(t *testing.T, s *suite) *Snapshot {
	t.Helper()
	snap, err := Load(snapshotFS(t, s, nil))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// TestLoadStateRefuses checks that LoadState refuses, and changes nothing
// of, a state that is not of the runtime's snapshot, or whose cursor no flow
// can be at.
func TestLoadStateRefuses(t *testing.T) {
	t.Parallel()
	s := choicesSuite()
	snap := loaded(t, s)
	good := func() published.SaveState {
		return s.state("PAGE", "CHAT", awaitingChoice, map[string]any{"CASH": 3}, nil, map[string]int{"CHAT": 1})
	}
	r := New(snap, false, nil)
	if err := r.LoadState(good()); err != nil {
		t.Fatalf("a state of the snapshot: %v", err)
	}
	r.Start("", "")
	before := r.SaveState()
	for name, edit := range map[string]func(*published.SaveState){
		"of another project":  func(st *published.SaveState) { st.ProjectID = mnemonicID(ids.Project, "C9") },
		"of a draft":          func(st *published.SaveState) { st.SnapshotID, st.SnapshotVersion = "", "" },
		"of another version":  func(st *published.SaveState) { st.StateVersion = 2 },
		"without a variable":  func(st *published.SaveState) { delete(st.Variables, vr("CASH")) },
		"of another variable": func(st *published.SaveState) { st.Variables[vr("NADA")] = raw(1) },
		"of a value of another type": func(st *published.SaveState) {
			st.Variables[vr("CASH")] = raw("x")
		},
		"of an enum's other option": func(st *published.SaveState) {
			st.Attributes[cm("KEEPER")][at("MD")] = raw("sad")
		},
		"without an attribute": func(st *published.SaveState) {
			delete(st.Attributes[cm("KEEPER")], at("MD"))
		},
		"of an attribute that is not runtime": func(st *published.SaveState) {
			st.Attributes[cm("KEEPER")][at("TAG")] = raw("x")
		},
		"of a visit of no node":        func(st *published.SaveState) { st.Visits[nd("NADA")] = 1 },
		"of a visit count below 1":     func(st *published.SaveState) { st.Visits[nd("CHAT")] = 0 },
		"of a cursor of no node":       func(st *published.SaveState) { st.Cursor.NodeID = nd("NADA") },
		"of a cursor of another board": func(st *published.SaveState) { st.Cursor.BoardID = bd("NADA") },
	} {
		st := good()
		edit(&st)
		if err := r.LoadState(st); !errors.Is(err, ErrState) {
			t.Errorf("a state %s: %v; want an error of ErrState", name, err)
		}
		if after := r.SaveState(); sameState(snap, after, before) != nil {
			t.Errorf("a state %s changed the runtime's", name)
		}
	}
	b := branchesSuite()
	r = New(loaded(t, b), false, nil)
	if err := r.LoadState(b.state("RANK", "GATE", awaitingChoice, nil, nil, nil)); !errors.Is(err, ErrState) {
		t.Errorf("a cursor that awaits a choice at a branch: %v; want an error of ErrState", err)
	}
	j := jumpersSuite()
	r = New(loaded(t, j), false, nil)
	if err := r.LoadState(j.state("HRB", "JSD", endedPhase, nil, nil, nil)); !errors.Is(err, ErrState) {
		t.Errorf("a cursor at a jumper: %v; want an error of ErrState", err)
	}
}

// TestSetValues checks that SetVariable and SetAttribute widen an int for a
// float and refuse a value of another type, an enum's other option, and an ID
// that the snapshot does not have, changing nothing.
func TestSetValues(t *testing.T) {
	t.Parallel()
	r := New(loaded(t, valuesSuite()), false, nil)
	if err := r.SetVariable(vr("F1"), expr.IntValue(2)); err != nil || r.Variable(vr("F1")) != expr.FloatValue(2) {
		t.Errorf("SetVariable of an int for a float: %v, %v", err, r.Variable(vr("F1")))
	}
	for name, err := range map[string]error{
		"a string for an int": r.SetVariable(vr("N1"), expr.StringValue("1")),
		"a float for an int":  r.SetVariable(vr("N1"), expr.FloatValue(1)),
		"a variable of no ID": r.SetVariable(vr("NADA"), expr.IntValue(1)),
		"none for a bool":     r.SetVariable(vr("B1"), expr.Value{}),
		"a value of no ID's":  r.SetAttribute(cm("NADA"), at("NADA"), expr.IntValue(1)),
	} {
		if err == nil {
			t.Errorf("SetVariable or SetAttribute of %s is not refused", name)
		}
	}
	if r.Variable(vr("N1")) != expr.IntValue(0) || r.Variable(vr("B1")) != expr.BoolValue(false) {
		t.Error("a refused value changed the variable")
	}
	c := New(loaded(t, choicesSuite()), false, nil)
	if err := c.SetAttribute(cm("KEEPER"), at("MD"), expr.StringValue("sad")); err == nil {
		t.Error("SetAttribute of an enum's other option is not refused")
	}
	if err := c.SetAttribute(cm("KEEPER"), at("TRST"), expr.IntValue(9)); err != nil ||
		c.Attribute(cm("KEEPER"), at("TRST")) != expr.IntValue(9) {
		t.Errorf("SetAttribute of an int: %v", err)
	}
	if err := c.SetAttribute(cm("KEEPER"), at("TAG"), expr.StringValue("x")); err == nil {
		t.Error("SetAttribute of an attribute that is not runtime is not refused")
	}
}

// TestInterpolate runs every interpolate case of the expression corpus's
// formatting.json, which a text's value is rather than an expression,
// through Interpolate, as the corpus's README says each runtime does.
func TestInterpolate(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "schema", "expr-corpus", "formatting.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []struct {
			Note     string `json:"note"`
			Op       string `json:"op"`
			Args     []typedJSON
			Decimals *int `json:"decimals"`
			Result   typedJSON
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range file.Cases {
		if c.Op != "interpolate" {
			continue
		}
		v, err := valueOf(c.Args[0].Value, c.Args[0].Type, nil)
		if err != nil {
			t.Fatalf("%s: %v", c.Note, err)
		}
		want, err := valueOf(c.Result.Value, c.Result.Type, nil)
		if err != nil {
			t.Fatalf("%s: %v", c.Note, err)
		}
		if got := Interpolate(v, c.Decimals); got != want.Str {
			t.Errorf("%s: Interpolate = %q; want %q", c.Note, got, want.Str)
		}
		n++
	}
	if n == 0 {
		t.Error("formatting.json has no interpolate case")
	}
}
