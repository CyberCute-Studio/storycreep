package clipboard

import (
	"strings"
	"testing"
)

// TestBounds checks that a copy that passes a clipboard's bounds, which a
// paste would refuse, is refused at node_ids, or at the place in node_ids of
// a branch of more outputs than a clipboard holds of one, those of its bytes
// with the new IDs that a paste of it gives, which its own JSON is within,
// among them, and that one within them is not.
func TestBounds(t *testing.T) {
	t.Parallel()
	place := func(j int) int { return 5 + j }
	for _, c := range []struct {
		name string
		c    Clipboard
		at   string
	}{
		{"a branch of too many outputs", Clipboard{Nodes: []ClipboardNode{{Type: typeElement},
			{Type: typeBranch, Outputs: make([]ClipboardOutput, MaxOutputs+1)}}}, "node_ids[6]"},
		{"too many connections", Clipboard{Nodes: []ClipboardNode{{}},
			Connections: make([]ClipboardConnection, MaxConnections+1)}, "node_ids"},
		{"too many variables", Clipboard{Nodes: []ClipboardNode{{}},
			Variables: make([]ClipboardVariable, MaxVariables+1)}, "node_ids"},
		{"too many attributes of a component", Clipboard{Nodes: []ClipboardNode{{}},
			Components: []ClipboardComponent{{Attributes: make([]ClipboardAttribute, MaxAttributes+1)}}},
			"node_ids"},
		{"too many bytes", Clipboard{Nodes: []ClipboardNode{{Text: Text(`"` + strings.Repeat("x", MaxBytes) +
			`"`)}}}, "node_ids"},
		{"too many bytes with the new IDs", Clipboard{Nodes: []ClipboardNode{{ID: strings.Repeat("n", 400),
			Text: Text(`"` + strings.Repeat("x", MaxBytes-1000) + `"`)}}}, "node_ids"},
		{"within them", Clipboard{Nodes: []ClipboardNode{{Text: Text(`"x"`)}}}, ""},
	} {
		if n, err := c.c.pasteBytes(); err != nil || c.name == "too many bytes with the new IDs" &&
			n-(2*400+len(`"":"",`)) > MaxBytes {
			t.Errorf("%s: the clipboard is %d bytes with its new IDs (%v), want its own JSON within them", c.name,
				n, err)
		}
		problems := c.c.bounds(place)
		switch {
		case c.at == "" && len(problems) > 0:
			t.Errorf("%s: %v, want none", c.name, problems)
		case c.at != "" && (len(problems) != 1 || problems[0].Field != c.at):
			t.Errorf("%s: %v, want one problem at %s", c.name, problems, c.at)
		}
	}
}
