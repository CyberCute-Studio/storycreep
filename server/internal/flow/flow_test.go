package flow

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
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
			manifest["files"].(map[string]any)[path] = hashOf(data)
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

// attributeOf returns the attribute definition of a component_types.json's
// JSON at the index i of its first type's.
func attributeOf(f map[string]any, i int) map[string]any {
	return f["componentTypes"].([]any)[0].(map[string]any)["attributes"].([]any)[i].(map[string]any)
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
		}, "not among the manifest's files"},
		{"a path of no file of a snapshot", choicesSuite(), published.ManifestPath, func(m map[string]any) {
			m["files"].(map[string]any)["notes.txt"] = "sha256:" + strings.Repeat("0", 64)
		}, "none of the paths"},
		{"a board's path of a name that Windows reserves", choicesSuite(), published.ManifestPath,
			func(m map[string]any) {
				m["files"].(map[string]any)[published.BoardPath("con")] = "sha256:" + strings.Repeat("0", 64)
			}, "none of the paths"},
		{"an understated astVersion", choicesSuite(), published.ManifestPath,
			func(m map[string]any) { m["astVersion"] = 0 }, "though its expressions use"},
		{"the manifest's counts", choicesSuite(), published.ManifestPath, func(m map[string]any) {
			m["counts"].(map[string]any)["nodes"] = 99
		}, "counts"},
		{"an index's board of another name than its file's", choicesSuite(), published.ManifestPath,
			func(m map[string]any) { m["boards"].([]any)[0].(map[string]any)["name"] = "Other" }, "not the index's"},
		{"an index's board whose file is not of its slug", choicesSuite(), published.ManifestPath,
			func(m map[string]any) { m["boards"].([]any)[0].(map[string]any)["slug"] = "other" },
			"not boards/ and its slug"},
		{"a signal that is there twice", choicesSuite(), published.SettingsPath, func(f map[string]any) {
			f["signals"] = []any{"asking", "asking"}
		}, "signal is there twice"},
		{"a component type that is there twice", choicesSuite(), published.ComponentTypesPath,
			func(f map[string]any) {
				ts := f["componentTypes"].([]any)
				f["componentTypes"] = append(ts, ts[0])
			}, "two of its entities"},
		{"two component types of one key", choicesSuite(), published.ComponentTypesPath, func(f map[string]any) {
			f["componentTypes"] = append(f["componentTypes"].([]any), map[string]any{"id": ct("PRP"),
				"key": "character", "name": "Other", "attributes": []any{}})
		}, "two component types have the key"},
		{"two definitions of one key", choicesSuite(), published.ComponentTypesPath, func(f map[string]any) {
			attributeOf(f, 1)["key"] = "trust"
		}, "have the key trust"},
		{"a text's definition that is runtime", choicesSuite(), published.ComponentTypesPath,
			func(f map[string]any) {
				a := attributeOf(f, 2)
				a["type"], a["runtime"] = "text", true
			}, "never runtime"},
		{"an enum's option that is there twice", choicesSuite(), published.ComponentTypesPath,
			func(f map[string]any) { attributeOf(f, 1)["options"] = []any{"calm", "calm", "wary", "angry"} },
			"option twice"},
		{"an enum's default of no option", choicesSuite(), published.ComponentTypesPath,
			func(f map[string]any) { attributeOf(f, 1)["default"] = "sad" }, "the default of the definition"},
		{"a branch's outputs of one ID", branchesSuite(), roll, func(b map[string]any) {
			outputs := nodeOf(b, nd("GATE"))["outputs"].([]any)
			outputs[1].(map[string]any)["id"] = outputs[0].(map[string]any)["id"]
		}, "two of its entities"},
		{"a connection that is there twice", choicesSuite(), main, func(b map[string]any) {
			b["connections"] = append(b["connections"].([]any), connectionOf(b, cn("PAY")))
		}, "two of its entities"},
		{"a speaker of no component", choicesSuite(), main, func(b map[string]any) {
			nodeOf(b, nd("START"))["speaker"] = cm("NADA")
		}, "no component of the snapshot"},
		{"two tags that are the same but for their case", choicesSuite(), main, func(b map[string]any) {
			nodeOf(b, nd("START"))["tags"] = []any{"Quest", "quest"}
		}, "the same but for their case"},
		{"a mention of no component", choicesSuite(), main, func(b map[string]any) {
			nodeOf(b, nd("CHAT"))["text"].(map[string]any)["segments"].([]any)[0].(map[string]any)["ref"] = cm("NADA")
		}, "a mention is of"},
		{"a segment's marks out of order", choicesSuite(), main, func(b map[string]any) {
			nodeOf(b, nd("CHAT"))["text"].(map[string]any)["segments"].([]any)[0].(map[string]any)["marks"] =
				[]any{"italic", "bold"}
		}, "alphabetical order"},
		{"a segment's link without an href", choicesSuite(), main, func(b map[string]any) {
			nodeOf(b, nd("CHAT"))["text"].(map[string]any)["segments"].([]any)[0].(map[string]any)["marks"] =
				[]any{"link"}
		}, "the link mark"},
		{"a text's value of another board's variable", jumpersSuite(), harbour, func(b map[string]any) {
			segs := nodeOf(b, nd("HENTRY"))["text"].(map[string]any)["segments"].([]any)
			segs[1].(map[string]any)["ref"] = vr("BTMP")
		}, "board_variable_out_of_scope"},
		{"a string default of more than 4,096 characters", choicesSuite(), published.VariablesPath,
			func(f map[string]any) {
				f["variables"].([]any)[2].(map[string]any)["default"] = strings.Repeat("a", 4097)
			}, "more than 4096 characters"},
		{"a label's value of an attribute that is not runtime", choicesSuite(), main, func(b map[string]any) {
			connectionOf(b, cn("ASK"))["label"].(map[string]any)["segments"].([]any)[1] = map[string]any{
				"kind": "value", "marks": []any{}, "name": "elara.title", "ref": cm("KEEPER"), "attribute": at("TAG")}
		}, "the label of " + cn("ASK") + ": a value is of"},
		{"a file that does not decode", choicesSuite(), main, func(b map[string]any) { b["schemaVersion"] = 2 },
			"schemaVersion"},
		{"a board's file of another board", choicesSuite(), main, func(b map[string]any) {
			b["board"].(map[string]any)["entryNodeId"] = nd("MARKET")
		}, "not the index's"},
		{"the settings' entry", choicesSuite(), published.SettingsPath, func(f map[string]any) {
			f["entry"].(map[string]any)["nodeId"] = nd("BYE")
		}, "not the settings'"},
		{"the manifest's entry", choicesSuite(), published.ManifestPath, func(m map[string]any) {
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
		{"a branch whose first output is no if", branchesSuite(), roll, func(b map[string]any) {
			nodeOf(b, nd("GATE"))["outputs"].([]any)[0].(map[string]any)["kind"] = published.OutputElseIf
		}, "not one if"},
		{"a branch with a second if", branchesSuite(), roll, func(b map[string]any) {
			nodeOf(b, nd("GATE"))["outputs"].([]any)[1].(map[string]any)["kind"] = published.OutputIf
		}, "not one if"},
		{"a branch whose else is not its last output", branchesSuite(), roll, func(b map[string]any) {
			outputs := nodeOf(b, nd("GATE"))["outputs"].([]any)
			outputs[1], outputs[2] = outputs[2], outputs[1]
			outputs[1].(map[string]any)["order"], outputs[2].(map[string]any)["order"] = 1, 2
		}, "not one if"},
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
		{"a text's value of an attribute that is not runtime", choicesSuite(), main, func(b map[string]any) {
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
		// A newer astVersion alone is ErrASTUnsupported too, which a newer
		// runtime may run.
		if errors.Is(err, ErrASTUnsupported) != (c.want == "ast_unsupported") {
			t.Errorf("%s: %v; want an error of ErrASTUnsupported only of a newer astVersion", c.name, err)
		}
	}
	// The checks of what the files agree on, which an edit of one file alone
	// fails first.
	entry := func(f map[string]any) { f["entry"].(map[string]any)["nodeId"] = nd("NADA") }
	boardEntry := func(f map[string]any) {
		for _, b := range f["boards"].([]any) {
			b.(map[string]any)["entryNodeId"] = nd("NADA")
		}
	}
	for _, c := range []struct {
		name  string
		edits map[string]func(map[string]any)
		want  string
	}{
		{"an entry of no node", map[string]func(map[string]any){published.ManifestPath: entry,
			published.SettingsPath: entry}, "is not a node of its board"},
		{"a board's entry node of no node", map[string]func(map[string]any){published.ManifestPath: boardEntry,
			main: func(b map[string]any) { b["board"].(map[string]any)["entryNodeId"] = nd("NADA") }},
			"is not one of its nodes"},
	} {
		_, err := Load(snapshotFS(t, choicesSuite(), c.edits))
		if !errors.Is(err, ErrLoad) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v; want an error of ErrLoad that says %q", c.name, err, c.want)
		}
	}
	// A file that Load lists but does not decode, an asset, is hashed too.
	asset := "assets/" + strings.TrimPrefix(mnemonicID(ids.Asset, "CVR"), ids.Asset.Prefix()+"_") + ".png"
	data := []byte("an image")
	for _, c := range []struct {
		hash string
		ok   bool
	}{{"sha256:" + strings.Repeat("0", 64), false}, {string(hashOf(data)), true}} {
		fsys := snapshotFS(t, choicesSuite(), map[string]func(map[string]any){
			published.ManifestPath: func(m map[string]any) { m["files"].(map[string]any)[asset] = c.hash },
		})
		fsys[asset] = &fstest.MapFile{Data: data}
		_, err := Load(fsys)
		if (err == nil) != c.ok || err != nil && !strings.Contains(err.Error(), asset+": its hash") {
			t.Errorf("an asset of the hash %s: %v", c.hash, err)
		}
	}
}

// hashOf returns the hash of data, as a manifest lists a file's (fileHash).
func hashOf(data []byte) published.Hash {
	sum := sha256.Sum256(data)
	return fileHash(sum[:])
}

// TestLoadOrder checks that Load reports the same error of a snapshot that
// is wrong in several ways, each time it loads it: of the first file that it
// reads, the settings, though variables.json is wrong too.
func TestLoadOrder(t *testing.T) {
	t.Parallel()
	hash := func(path string) func(map[string]any) {
		return func(m map[string]any) { m["files"].(map[string]any)[path] = "sha256:" + strings.Repeat("0", 64) }
	}
	for range 20 {
		_, err := Load(snapshotFS(t, choicesSuite(), map[string]func(map[string]any){
			published.ManifestPath: func(m map[string]any) {
				hash(published.SettingsPath)(m)
				hash(published.VariablesPath)(m)
			},
		}))
		if err == nil || !strings.Contains(err.Error(), published.SettingsPath) {
			t.Fatalf("Load = %v; want the error of %s", err, published.SettingsPath)
		}
	}
	// Of two nodes whose IDs are not in their canonical spelling, it is the
	// error of the first in its board's file, whatever order a map of the
	// nodes would give.
	var first string
	lower := func(b map[string]any) {
		nodes := b["nodes"].([]any)
		for _, n := range []any{nodes[0], nodes[len(nodes)-1]} {
			n := n.(map[string]any)
			n["id"] = strings.ToLower(n["id"].(string))
		}
		first = nodes[0].(map[string]any)["id"].(string)
	}
	for range 20 {
		_, err := Load(snapshotFS(t, choicesSuite(), map[string]func(map[string]any){
			published.BoardPath("main"): lower,
		}))
		if err == nil || !strings.Contains(err.Error(), strconv.Quote(first)) {
			t.Fatalf("Load = %v; want the error of %s", err, first)
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
	r := New(snap, false)
	if _, err := r.LoadState(good()); err != nil {
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
		"of attributes of no component": func(st *published.SaveState) {
			st.Attributes[cm("NADA")] = published.AttributeValues{}
		},
		"of a string of more than 4,096 characters": func(st *published.SaveState) {
			st.Variables[vr("NAME")] = raw(strings.Repeat("a", 4097))
		},
		"of a string with a NUL":   func(st *published.SaveState) { st.Variables[vr("NAME")] = raw("a\x00b") },
		"of a visit of no node":    func(st *published.SaveState) { st.Visits[nd("NADA")] = 1 },
		"of a visit count below 1": func(st *published.SaveState) { st.Visits[nd("CHAT")] = 0 },
		"of a visit count beyond 2^53 − 1": func(st *published.SaveState) {
			st.Visits[nd("CHAT")] = published.MaxInt + 1
		},
		"of a cursor of no node":       func(st *published.SaveState) { st.Cursor.NodeID = nd("NADA") },
		"of a cursor of another board": func(st *published.SaveState) { st.Cursor.BoardID = bd("NADA") },
		"of a cursor of no phase":      func(st *published.SaveState) { st.Cursor.Phase = "" },
		"of a cursor of another phase": func(st *published.SaveState) { st.Cursor.Phase = "waiting" },
	} {
		st := good()
		edit(&st)
		if _, err := r.LoadState(st); !errors.Is(err, ErrState) {
			t.Errorf("a state %s: %v; want an error of ErrState", name, err)
		}
		if after := r.SaveState(); sameState(snap, after, before) != nil {
			t.Errorf("a state %s changed the runtime's", name)
		}
	}
	b := branchesSuite()
	r = New(loaded(t, b), false)
	if _, err := r.LoadState(b.state("RANK", "GATE", awaitingChoice, nil, nil, nil)); !errors.Is(err, ErrState) {
		t.Errorf("a cursor that awaits a choice at a branch: %v; want an error of ErrState", err)
	}
	j := jumpersSuite()
	r = New(loaded(t, j), false)
	if _, err := r.LoadState(j.state("HRB", "JSD", endedPhase, nil, nil, nil)); !errors.Is(err, ErrState) {
		t.Errorf("a cursor at a jumper: %v; want an error of ErrState", err)
	}
	if _, err := r.LoadState(j.state("HRB", "HENTRY", awaitingChoice, nil, nil,
		map[string]int{"HENTRY": 1, "JSD": 1})); !errors.Is(err, ErrState) {
		t.Errorf("a visit count of a jumper: %v; want an error of ErrState", err)
	}
	r = New(snap, false)
	for _, n := range []string{"BYE", "MARKET"} {
		if _, err := r.LoadState(s.state("PAGE", n, awaitingChoice, nil, nil, nil)); !errors.Is(err, ErrState) {
			t.Errorf("a cursor that awaits a choice at %s, an ending or a dead end: %v; want an error of ErrState", n,
				err)
		}
		if _, err := r.LoadState(s.state("PAGE", n, endedPhase, nil, nil, nil)); err != nil {
			t.Errorf("a cursor that ended at %s: %v", n, err)
		}
	}
}

// TestSameState checks that sameState, which TestConformance compares each
// step's state with, tells two states apart by their keys, as the README
// says a runner compares them: a state of another component's attributes is
// not the state, though the component holds none of them.
func TestSameState(t *testing.T) {
	t.Parallel()
	s := choicesSuite()
	snap := loaded(t, s)
	state := func() published.SaveState {
		return s.state("PAGE", "START", awaitingChoice, nil, nil, map[string]int{"START": 1})
	}
	if err := sameState(snap, state(), state()); err != nil {
		t.Fatalf("a state is not itself: %v", err)
	}
	other := state()
	other.Attributes = map[string]published.AttributeValues{cm("NADA"): {}}
	if err := sameState(snap, state(), other); err == nil {
		t.Error("a state of another component's attributes, though none of them, is the state")
	}
}

// TestSetValues checks that SetVariable and SetAttribute widen an int for a
// float and refuse a value of another type, an enum's other option, and an ID
// that the snapshot does not have, changing nothing.
func TestSetValues(t *testing.T) {
	t.Parallel()
	r := New(loaded(t, valuesSuite()), false)
	if err := r.SetVariable(vr("F1"), expr.IntValue(2)); err != nil || r.Variable(vr("F1")) != expr.FloatValue(2) {
		t.Errorf("SetVariable of an int for a float: %v, %v", err, r.Variable(vr("F1")))
	}
	for name, err := range map[string]error{
		"a string for an int":        r.SetVariable(vr("N1"), expr.StringValue("1")),
		"a float for an int":         r.SetVariable(vr("N1"), expr.FloatValue(1)),
		"a variable of no ID":        r.SetVariable(vr("NADA"), expr.IntValue(1)),
		"none for a bool":            r.SetVariable(vr("B1"), expr.Value{}),
		"a value of no ID's":         r.SetAttribute(cm("NADA"), at("NADA"), expr.IntValue(1)),
		"an int beyond 2^53 − 1":     r.SetVariable(vr("N1"), expr.IntValue(1<<53)),
		"an int beyond −(2^53 − 1)":  r.SetVariable(vr("N1"), expr.IntValue(-1<<53)),
		"an infinity":                r.SetVariable(vr("F1"), expr.FloatValue(math.Inf(1))),
		"a NaN":                      r.SetVariable(vr("F1"), expr.FloatValue(math.NaN())),
		"a string that is not UTF-8": r.SetVariable(vr("S1"), expr.StringValue("\xff")),
		"a string of more than 4,096 characters": r.SetVariable(vr("S1"),
			expr.StringValue(strings.Repeat("é", 4097))),
		"a string with a NUL": r.SetVariable(vr("S1"), expr.StringValue("a\x00b")),
	} {
		if err == nil {
			t.Errorf("SetVariable or SetAttribute of %s is not refused", name)
		}
	}
	for _, s := range []string{strings.Repeat("é", 4096), "a\x01b"} {
		if err := r.SetVariable(vr("S1"), expr.StringValue(s)); err != nil || r.Variable(vr("S1")).Str != s {
			t.Errorf("SetVariable of a string of %d characters: %v", len([]rune(s)), err)
		}
	}
	if err := r.SetVariable(vr("S1"), expr.StringValue("")); err != nil {
		t.Fatal(err)
	}
	if r.Variable(vr("N1")) != expr.IntValue(0) || r.Variable(vr("B1")) != expr.BoolValue(false) ||
		r.Variable(vr("F1")) != expr.FloatValue(2) || r.Variable(vr("S1")) != expr.StringValue("") {
		t.Error("a refused value changed the variable")
	}
	if err := r.SetVariable(vr("F1"), expr.FloatValue(math.Copysign(0, -1))); err != nil ||
		math.Signbit(r.Variable(vr("F1")).Float) {
		t.Errorf("SetVariable of -0: %v, %v; want 0", err, r.Variable(vr("F1")))
	}
	if err := r.SetVariable(vr("N1"), expr.IntValue(-(1<<53 - 1))); err != nil {
		t.Errorf("SetVariable of −(2^53 − 1): %v", err)
	}
	c := New(loaded(t, choicesSuite()), false)
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
	// An enum's option is set as LoadState reads it back, by the enum's
	// declaration, so that one that a string's check would refuse, with a
	// NUL, which Load and LoadState take of an enum, is set, saved, and
	// loaded too.
	option := "a\x00b"
	snap, err := Load(snapshotFS(t, choicesSuite(), map[string]func(map[string]any){
		published.ComponentTypesPath: func(f map[string]any) {
			attributeOf(f, 1)["options"] = []any{"calm", "wary", "angry", option}
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	c = New(snap, false)
	if err := c.SetAttribute(cm("KEEPER"), at("MD"), expr.StringValue(option)); err != nil {
		t.Fatalf("SetAttribute of an enum's option with a NUL: %v", err)
	}
	if _, err := c.LoadState(c.SaveState()); err != nil || c.Attribute(cm("KEEPER"), at("MD")).Str != option {
		t.Errorf("LoadState of the state of an enum's option with a NUL: %v", err)
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

// TestResetWithoutEntry checks that Reset of a snapshot without an entry
// reports unknown_node, as Start("", "") does, and changes nothing: neither
// the values, nor the visit counts, nor where the flow is.
func TestResetWithoutEntry(t *testing.T) {
	t.Parallel()
	noEntry := func(f map[string]any) { delete(f, "entry") }
	snap, err := Load(snapshotFS(t, choicesSuite(), map[string]func(map[string]any){
		published.ManifestPath: noEntry, published.SettingsPath: noEntry,
	}))
	if err != nil {
		t.Fatal(err)
	}
	r := New(snap, false)
	r.Start(bd("PAGE"), "")
	if err := r.SetVariable(vr("CASH"), expr.IntValue(9)); err != nil {
		t.Fatal(err)
	}
	before := r.SaveState()
	events := r.Reset()
	if len(events) != 1 || events[0].Kind != EventError || events[0].Code != UnknownNode {
		t.Errorf("Reset reports %+v; want unknown_node alone", events)
	}
	if err := sameState(snap, r.SaveState(), before); err != nil {
		t.Errorf("Reset changed the state: %v", err)
	}
	if node, phase := r.Current(); node != nd("START") || phase != awaitingChoice || len(r.Choices()) == 0 {
		t.Errorf("Reset moved the flow to %s, %s", node, phase)
	}
}

// TestResetBrokenEntry checks that Reset of a snapshot whose entry is a
// jumper without a target reports jumper_broken, as Start("", "") does, and
// changes nothing, since it resolves the entry before anything else (PRD
// §5.8.5): neither the values, nor the visit counts, nor where the flow is.
func TestResetBrokenEntry(t *testing.T) {
	t.Parallel()
	broken := func(f map[string]any) { f["entry"].(map[string]any)["nodeId"] = nd("JBRK") }
	snap, err := Load(snapshotFS(t, jumpersSuite(), map[string]func(map[string]any){
		published.ManifestPath: broken, published.SettingsPath: broken,
	}))
	if err != nil {
		t.Fatal(err)
	}
	r := New(snap, false)
	r.Start(bd("HRB"), "")
	if err := r.SetVariable(vr("TRPS"), expr.IntValue(9)); err != nil {
		t.Fatal(err)
	}
	before := r.SaveState()
	events := r.Reset()
	if len(events) != 1 || events[0].Kind != EventError || events[0].Code != JumperBroken ||
		events[0].At != nd("JBRK") {
		t.Errorf("Reset reports %+v; want jumper_broken of JBRK alone", events)
	}
	if err := sameState(snap, r.SaveState(), before); err != nil {
		t.Errorf("Reset changed the state: %v", err)
	}
	if node, phase := r.Current(); node != nd("HENTRY") || phase != awaitingChoice || len(r.Choices()) == 0 {
		t.Errorf("Reset moved the flow to %s, %s", node, phase)
	}
}
