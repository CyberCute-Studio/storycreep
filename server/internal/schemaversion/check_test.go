package schemaversion_test

import (
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/schemacheck"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemaversion"
)

// withFile returns f with the file at p replaced.
func withFile(f schemaversion.Files, p, data string) schemaversion.Files {
	out := schemaversion.Files{}
	for k, v := range f {
		out[k] = v
	}
	out[p] = []byte(data)
	return out
}

// TestCheckHoldsAnUntrustedFile checks what Validate's schema check refuses
// before it validates, and that a key of a later version still passes, which
// the schemas' AnyValue takes.
//
// Verifies: NFR-SEC-13
func TestCheckHoldsAnUntrustedFile(t *testing.T) {
	t.Parallel()
	f := readTree(t, "schema/fixtures/v1")
	const board = "boards/harbour-intro.json"
	later := edited(t, f, board, func(b map[string]any) {
		nodeOf(t, b, "nd_000000000000000000000000N1")["later"] = map[string]any{"a": []any{1, "b", true}}
	})
	if err := schemaversion.Validate(later); err != nil {
		t.Errorf("a key of a later version: %v", err)
	}
	for name, c := range map[string]struct{ data, want string }{
		"a null in a key of a later version": {strings.Replace(string(f[board]), `"schemaVersion"`,
			`"later": {"a": [1, null]}, "schemaVersion"`, 1), "holds a null"},
		"a file nested too deeply": {`{"later":` + strings.Repeat("[", schemacheck.MaxDepth) +
			strings.Repeat("]", schemacheck.MaxDepth) + `}`, "nest"},
	} {
		err := schemaversion.Validate(withFile(f, board, c.data))
		if err == nil || !strings.Contains(err.Error(), "does not validate against its schema") ||
			!strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want a schema failure that says %q", name, err, c.want)
		}
	}
}

// TestCheckStopsAtTheFirstFailure checks, by its allocations, that a board
// whose every node fails does not cost an error of each, whether its nodes fail
// in an array or an AST's operands in an object, and that its failure is short.
//
// Verifies: NFR-SEC-13
func TestCheckStopsAtTheFirstFailure(t *testing.T) {
	f := readTree(t, "schema/fixtures/v1")
	const board = "boards/harbour-intro.json"
	numbers := make([]any, 20_000)
	for i := range numbers {
		numbers[i] = 0
	}
	var ast any = 0
	for range 12 {
		ast = map[string]any{"kind": "binary", "op": "add", "left": ast, "right": ast}
	}
	branch := map[string]any{"type": "branch", "id": "nd_00000000000000000000000001", "position": map[string]any{"x": 0, "y": 0},
		"outputs": []any{map[string]any{"id": "bo_00000000000000000000000001", "kind": "if", "order": "V",
			"condition": map[string]any{"source": "s", "ast": ast}}}}
	for name, c := range map[string]struct {
		nodes []any
		at    string
		most  float64
	}{
		"nodes that are numbers":        {numbers, "/nodes/0", 30_000},
		"an AST whose every leaf fails": {[]any{branch}, "/ast/", 60_000},
	} {
		bad := edited(t, f, board, func(b map[string]any) { b["nodes"] = c.nodes })
		var err error
		allocs := testing.AllocsPerRun(1, func() { err = schemaversion.Validate(bad) })
		if err == nil || !strings.Contains(err.Error(), "does not validate against its schema") ||
			!strings.Contains(err.Error(), c.at) {
			t.Fatalf("%s: the error is %v, want a schema failure at %s", name, err, c.at)
		}
		if len(err.Error()) > 1000 {
			t.Errorf("%s: the error has %d bytes", name, len(err.Error()))
		}
		// Parsing the file takes some allocations of each value, and an error
		// of each would take some 40 more.
		if allocs > c.most {
			t.Errorf("%s: it took %.0f allocations, want at most %.0f", name, allocs, c.most)
		}
		t.Logf("%s: %.0f allocations", name, allocs)
	}
}
