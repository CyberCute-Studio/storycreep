package clipboard

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/node"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// TestCheck checks that Check holds a clipboard's nodes to what node.create
// would refuse of them, as a size without a height, or a title with white
// space around it, which a new node's may not have, and its declarations,
// which a paste does not write, only to what reading them takes, so that a
// declaration of what a project holds that a rule has changed for since it
// was written, as a description with white space around it, or a string
// default with a control character, is taken all the same, but one of
// another type than its own is not.
func TestCheck(t *testing.T) {
	t.Parallel()
	raw := func(s string) value.Value {
		var v value.Value
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	keeper := ids.Encode(ids.Node, ids.New())
	clip := func() Clipboard {
		var n ClipboardNode
		if err := json.Unmarshal([]byte(`{"id":"`+keeper+`","type":"element","title":"Keeper",`+
			`"position":{"x":10,"y":20}}`), &n); err != nil {
			t.Fatal(err)
		}
		return Clipboard{Format: Format, Version: Version, ProjectID: ids.Encode(ids.Project, ids.New()),
			BoardID: ids.Encode(ids.Board, ids.New()), Nodes: []ClipboardNode{n},
			Variables: []ClipboardVariable{{ID: ids.Encode(ids.Variable, ids.New()), Key: "mood", Scope: "global",
				Type: "string", Default: raw(`"calm\u0001"`), Description: "How the keeper feels "}},
			Components: []ClipboardComponent{{ID: ids.Encode(ids.Component, ids.New()), Key: "elara", Name: "Elara",
				Type: ClipboardComponentType{ID: ids.Encode(ids.ComponentType, ids.New()), Key: "hero", Name: "Hero"},
				Attributes: []ClipboardAttribute{{ID: ids.Encode(ids.AttributeDef, ids.New()), Key: "fear",
					Name: "Fear ", Type: "string", Default: raw(`"\u0001"`)}}}}}
	}
	if problems := Check(clip()); len(problems) > 0 {
		t.Errorf("a clipboard of declarations that a rule changed since: %v, want none", problems)
	}
	for _, c := range []struct {
		name   string
		change func(c *Clipboard)
		at     string
	}{
		{"a size without a height", func(c *Clipboard) {
			c.Nodes[0].Size = &node.NodeSize{}
			if err := json.Unmarshal([]byte(`{"width":5}`), c.Nodes[0].Size); err != nil {
				t.Fatal(err)
			}
		}, "nodes[0].size.height"},
		{"a title with white space around it", func(c *Clipboard) { c.Nodes[0].Title = " Keeper" }, "nodes[0].title"},
		{"a tag twice", func(c *Clipboard) { c.Nodes[0].Tags = []string{"Quest", "quest"} }, "nodes[0].tags[1]"},
		{"a target of an element", func(c *Clipboard) { c.Nodes[0].Target = &node.NodeTarget{} },
			"nodes[0].target"},
		{"outputs of an element", func(c *Clipboard) { c.Nodes[0].Outputs = []ClipboardOutput{{}} },
			"nodes[0].outputs"},
		{"a variable's default of another type", func(c *Clipboard) { c.Variables[0].Default = raw(`3`) },
			"variables[0].default"},
		{"an attribute's default of another type", func(c *Clipboard) {
			c.Components[0].Attributes[0].Default = raw(`true`)
		}, "components[0].attributes[0].default"},
	} {
		bad := clip()
		c.change(&bad)
		problems := Check(bad)
		if len(problems) == 0 || problems[0].Field != c.at {
			t.Errorf("%s: %v, want a problem at %s first", c.name, problems, c.at)
		}
		if c.at == "nodes[0].title" && !strings.Contains(problems[0].Message, "white space") {
			t.Errorf("%s: %s, want node.create's own message", c.name, problems[0].Message)
		}
	}
}
