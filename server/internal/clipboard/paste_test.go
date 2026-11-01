package clipboard

import "testing"

// TestFields checks that a part's refusal is answered at the field of the
// paste's payload that the part's comes from: an ID at its new ID's, the board
// at the paste's, an order, which the paste gives, and the part as a whole at
// the clipboard's entity, which holds no order, and every other field at the
// entity's own of its name.
func TestFields(t *testing.T) {
	t.Parallel()
	fields := (&plan{}).fields("clipboard.connections[3]", "cn_old")
	for f, want := range map[string]string{
		"id":           "ids.cn_old",
		"board_id":     "board_id",
		"order":        "clipboard.connections[3]",
		"":             "clipboard.connections[3]",
		"label":        "clipboard.connections[3].label",
		"from_port":    "clipboard.connections[3].from_port",
		"condition":    "clipboard.connections[3].condition",
		"to_node_id":   "clipboard.connections[3].to_node_id",
		"from_node_id": "clipboard.connections[3].from_node_id",
	} {
		if got := fields(f); got != want {
			t.Errorf("fields(%q) = %q, want %q", f, got, want)
		}
	}
}
