package clipboard

import (
	"strings"
	"testing"
)

// TestBounds checks that a copy that passes a clipboard's bounds, which a
// paste would refuse, is refused at node_ids, or at the place in node_ids of
// a branch of more outputs than a clipboard holds of one, and that one within
// them is not.
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
		{"within them", Clipboard{Nodes: []ClipboardNode{{Text: Text(`"x"`)}}}, ""},
	} {
		problems := c.c.bounds(place)
		switch {
		case c.at == "" && len(problems) > 0:
			t.Errorf("%s: %v, want none", c.name, problems)
		case c.at != "" && (len(problems) != 1 || problems[0].Field != c.at):
			t.Errorf("%s: %v, want one problem at %s", c.name, problems, c.at)
		}
	}
}
