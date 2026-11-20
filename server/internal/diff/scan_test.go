package diff

import (
	"bytes"
	"encoding/json"
	"maps"
	"reflect"
	"testing"
)

// TestScan checks that the scanner finds the members of an object and the
// elements of an array as JSON has them, whatever their strings hold, and
// the ID of an object, and that it refuses what is no JSON of them.
func TestScan(t *testing.T) {
	obj := `{ "a": "x}]\"", "b" : [1, {"c": "]\\"}, true], "id": "nd_1", "n": -1.5e+3, "o": {"id": "inner"} }`
	got := map[string]string{}
	if !eachMember([]byte(obj), func(k string, v []byte) bool { got[k] = string(v); return true }) {
		t.Fatalf("eachMember refuses %s", obj)
	}
	want := map[string]string{"a": `"x}]\""`, "b": `[1, {"c": "]\\"}, true]`, "id": `"nd_1"`, "n": "-1.5e+3",
		"o": `{"id": "inner"}`}
	if !maps.Equal(got, want) {
		t.Errorf("the members of %s are %v, want %v", obj, got, want)
	}
	if id, ok := idOf([]byte(obj)); !ok || id != "nd_1" {
		t.Errorf("the ID of %s is %q, %v", obj, id, ok)
	}
	if id, ok := idOf([]byte(`{"id": "nd_\u0031"}`)); !ok || id != "nd_1" {
		t.Errorf("the ID of an escaped string is %q, %v", id, ok)
	}
	items, ok := elementsOf([]byte(` [ {"id": "a"}, "s,]", 3 , [ ] ] `))
	if !ok || !reflect.DeepEqual(items, [][]byte{[]byte(`{"id": "a"}`), []byte(`"s,]"`), []byte("3"),
		[]byte("[ ]")}) {
		t.Errorf("the elements are %q, %v", items, ok)
	}
	if items, ok := elementsOf([]byte("[]")); !ok || len(items) != 0 {
		t.Errorf("the elements of [] are %q, %v", items, ok)
	}
	for _, bad := range []string{``, ` `, `[`, `[1,`, `[1,]`, `[1 2]`, `"x"`, `{}`} {
		if _, ok := elementsOf([]byte(bad)); ok {
			t.Errorf("elementsOf takes %q", bad)
		}
	}
	for _, bad := range []string{``, `[]`, `{`, `{"a"}`, `{"a": }`, `{"a": 1,}`, `{"a": 1 "b": 2}`, `{"a": "x`,
		`{a: 1}`} {
		if eachMember([]byte(bad), func(string, []byte) bool { return true }) {
			t.Errorf("eachMember takes %q", bad)
		}
	}
	if _, ok := idOf([]byte(`{"key": "id"}`)); ok {
		t.Error("idOf finds an ID in an object without one")
	}
}

// TestScanTruncated checks that the scanner refuses a board's file cut short
// anywhere, without panicking, as it does a file that is no JSON.
func TestScanTruncated(t *testing.T) {
	data := corpusSetFiles(t, "base")["boards/harbour-intro.json"]
	for i := 0; i < len(data)-2; i++ {
		cut := data[:i]
		var nodes []byte
		ok := eachMember(cut, func(k string, v []byte) bool {
			if k == "nodes" {
				nodes = v
			}
			return true
		})
		if ok && nodes != nil {
			if items, ok := elementsOf(nodes); ok {
				for _, item := range items {
					idOf(item)
				}
			}
		}
	}
}

// relaid returns files with each JSON file written compactly, as no file of
// the serializer is, so that every node's and connection's bytes differ from
// the same one's in files.
func relaid(t *testing.T, files map[string][]byte) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for p, data := range files {
		var b bytes.Buffer
		if err := json.Compact(&b, data); err != nil {
			t.Fatal(err)
		}
		out[p] = b.Bytes()
	}
	return out
}

// TestCompareReadsValues checks that the diff of every case of the corpus is
// the same whether the files of its sides are laid out as the serializer lays
// them out or otherwise, as a hand-edited file may be, so that a node or a
// connection whose bytes differ, but not its values, is no change.
func TestCompareReadsValues(t *testing.T) {
	for _, c := range readCases(t) {
		from, to := corpusSetFiles(t, c.From), corpusSetFiles(t, c.To)
		for name, pair := range map[string][2]map[string][]byte{
			"from":         {relaid(t, from), to},
			"to":           {from, relaid(t, to)},
			"either":       {relaid(t, from), relaid(t, to)},
			"relaid alone": {from, relaid(t, from)},
		} {
			d, err := Compare(pair[0], pair[1])
			if err != nil {
				t.Fatalf("%s, %s relaid: %v", c.Name, name, err)
			}
			if name == "relaid alone" {
				if d.Summary != (DiffSummary{}) {
					t.Errorf("%s's from set relaid has the changes %+v from itself", c.Name, d.Summary)
				}
				continue
			}
			if got := layout(t, d); !sameJSON(t, got, c.Diff) {
				t.Errorf("%s, %s relaid: the diff is\n%s\nwant\n%s", c.Name, name, got, c.Diff)
			}
		}
	}
}
