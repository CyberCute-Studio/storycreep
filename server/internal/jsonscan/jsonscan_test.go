package jsonscan

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestValuesCountsWhatAParserHolds checks the count of a document's values, and
// that what its strings spell is not counted.
func TestValuesCountsWhatAParserHolds(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]int{
		`1`:                  1,
		`[]`:                 2,
		`[1,2,3]`:            4,
		`{"a":1,"b":[{}]}`:   5,
		`"[,{,]"`:            1,
		`["a\",b",{"c,":1}]`: 4,
	} {
		if got := Values([]byte(in)); got != want {
			t.Errorf("Values(%s) = %d, want %d", in, got, want)
		}
	}
}

// TestHasNullAndDepth checks that a null and a bracket are found outside of
// strings alone.
func TestHasNullAndDepth(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		in    string
		null  bool
		depth int
	}{
		{`1`, false, 0},
		{`null`, true, 0},
		{`"null"`, false, 0},
		{`{"n":"nnn [[[ {{{"}`, false, 1},
		{`["\"",null]`, true, 1},
		{`[[{"a":[true,false]}]]`, false, 4},
		{`{"a\\":"\"[n"}`, false, 1},
	} {
		if got := HasNull([]byte(c.in)); got != c.null {
			t.Errorf("HasNull(%s) = %v", c.in, got)
		}
		if got := Depth([]byte(c.in)); got != c.depth {
			t.Errorf("Depth(%s) = %d, want %d", c.in, got, c.depth)
		}
	}
}

// TestMembersAndElements checks that a walk finds what a parser finds, of each
// kind of value, with and without white space.
func TestMembersAndElements(t *testing.T) {
	t.Parallel()
	const doc = ` { "a" : 1 , "b\"]" : "x,]}\\" , "c":[ 1, {"d":[2,"]"]} ,null, true ,-1.5e3,"" ] ,"e":{ } , "f" : [ ]
	} `
	if !json.Valid([]byte(doc)) {
		t.Fatal("the test's document is not JSON")
	}
	var want map[string]json.RawMessage
	if err := json.Unmarshal([]byte(doc), &want); err != nil {
		t.Fatal(err)
	}
	seen := 0
	err := Members([]byte(doc), func(key, value []byte) error {
		var name string
		if err := json.Unmarshal(key, &name); err != nil {
			t.Fatal(err)
		}
		if string(want[name]) != string(value) {
			t.Errorf("%s holds %s, want %s", name, value, want[name])
		}
		seen++
		return nil
	})
	if err != nil || seen != len(want) {
		t.Errorf("the walk saw %d members, %v, want %d", seen, err, len(want))
	}
	var elements []json.RawMessage
	if err := json.Unmarshal(want["c"], &elements); err != nil {
		t.Fatal(err)
	}
	seen = 0
	err = Elements(want["c"], func(n int, value []byte) error {
		if n != seen || strings.TrimSpace(string(elements[n])) != string(value) {
			t.Errorf("element %d is %s, want %s", n, value, elements[n])
		}
		seen++
		return nil
	})
	if err != nil || seen != len(elements) {
		t.Errorf("the walk saw %d elements, %v, want %d", seen, err, len(elements))
	}
	for _, empty := range []string{`{}`, ` { } `} {
		if err := Members([]byte(empty), func(_, _ []byte) error {
			t.Errorf("%s has a member", empty)
			return nil
		}); err != nil {
			t.Error(err)
		}
	}
	for _, empty := range []string{`[]`, ` [ ] `} {
		if err := Elements([]byte(empty), func(int, []byte) error {
			t.Errorf("%s has an element", empty)
			return nil
		}); err != nil {
			t.Error(err)
		}
	}
}
