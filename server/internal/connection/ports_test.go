package connection_test

import (
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/connection"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/node"
)

// newOutput returns the ID of a new conditional output of the branch, with
// the fields of extra.
func (f *fixture) newOutput(t *testing.T, branch string, extra payload) string {
	t.Helper()
	id := ids.Encode(ids.BranchOutput, ids.New())
	p := payload{"id": id, "node_id": branch, "condition": "true"}
	for k, v := range extra {
		p[k] = v
	}
	f.must(t, cmd{node.CreateOutput, p})
	return id
}

// TestPorts checks that a connection leaves a branch by one of its outputs,
// which it reads as its from_port, that one of another branch, one in the
// trash, or none is refused at from_port, that the connections that leave a
// port are ordered among themselves, after the port's last, that reconnecting
// a connection to another port, or to another node, places it after that
// port's last, that the undo sets its port back, and that a connection of an
// output in the trash, of a branch in the trash too, is refused with the
// branch named first, whose restoration the output's waits for.
func TestPorts(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	b := f.newBoard(t, "Harbour")
	branch, other := f.newNode(t, b, node.TypeBranch), f.newNode(t, b, node.TypeBranch)
	keeper, gull := f.newNode(t, b, node.TypeElement), f.newNode(t, b, node.TypeElement)
	yes, no := f.newOutput(t, branch, payload{"order": "M"}), f.newOutput(t, branch, payload{"order": "T"})
	elsewhere := f.newOutput(t, other, nil)

	first := f.connect(t, branch, keeper, payload{"from_port": yes})
	if c := f.get(t, first); c.FromPort != yes || c.FromNodeID != branch {
		t.Errorf("the connection out of the branch is %+v", c)
	}
	// Each port's connections are ordered among themselves.
	second := f.connect(t, branch, gull, payload{"from_port": no})
	if a, b := f.get(t, first), f.get(t, second); a.Order != b.Order {
		t.Errorf("the first connections of two ports have the orders %s and %s, want the first key of each", a.Order,
			b.Order)
	}
	for _, c := range []struct {
		name    string
		payload payload
	}{
		{"of another branch's output", payload{"from_port": elsewhere}},
		{"by out", payload{}},
		{"by out, named", payload{"from_port": connection.PortOut}},
	} {
		if r := f.refused(t, create(newID(), branch, keeper, c.payload)); r.Field != "from_port" {
			t.Errorf("a connection out of a branch %s is refused at %s: %s", c.name, r.Field, r.Message)
		}
	}
	if r := f.refused(t, create(newID(), keeper, gull, payload{"from_port": yes})); r.Field != "from_port" {
		t.Errorf("a connection out of an element by an output is refused at %s: %s", r.Field, r.Message)
	}
	if got := f.invalid(t, create(newID(), keeper, gull, payload{"from_port": "nd_" + ids.Encode(ids.Node,
		ids.New())[3:]})); len(got) != 1 || got[0] != "payload.from_port" {
		t.Errorf("a port that is neither out nor an output's ID is refused at %v", got)
	}

	// Reconnecting a connection to another port places it after that port's
	// last, and its undo sets it back.
	res := f.must(t, cmd{connection.Reconnect, payload{"id": first, "from_port": no}})
	if c, s := f.get(t, first), f.get(t, second); c.FromPort != no || c.Order <= s.Order {
		t.Errorf("the reconnected connection is %+v, after %+v", c, s)
	}
	f.undo(t, res.Seq)
	if c := f.get(t, first); c.FromPort != yes {
		t.Errorf("once the reconnect is undone, the connection leaves %s, want %s", c.FromPort, yes)
	}
	// Reconnecting it to an element leaves the element's out; to a branch,
	// one of its outputs, which a reconnect names.
	f.must(t, cmd{connection.Reconnect, payload{"id": first, "from_node_id": keeper}})
	if c := f.get(t, first); c.FromPort != connection.PortOut || c.FromNodeID != keeper {
		t.Errorf("the connection reconnected to an element is %+v", c)
	}
	if r := f.refused(t, cmd{connection.Reconnect, payload{"id": first, "from_node_id": branch}}); r.Field !=
		"from_port" {
		t.Errorf("a reconnect to a branch without a port is refused at %s: %s", r.Field, r.Message)
	}
	f.must(t, cmd{connection.Reconnect, payload{"id": first, "from_node_id": branch, "from_port": yes}})

	// An output in the trash takes no connection, and its connections are in
	// the trash with it.
	f.must(t, cmd{node.DeleteOutput, payload{"id": no, "confirm": true}})
	if f.live(t, second) {
		t.Error("the connection of a deleted output is live")
	}
	if r := f.refused(t, create(newID(), branch, keeper, payload{"from_port": no})); r.Field != "from_port" {
		t.Errorf("a connection out of an output in the trash is refused at %s: %s", r.Field, r.Message)
	}
	if r := f.refused(t, cmd{connection.Update, payload{"id": second, "condition": "true"}}); r.Field != "id" {
		t.Errorf("a change of a connection of an output in the trash is refused at %s: %s", r.Field, r.Message)
	}
	if r := f.refused(t, cmd{connection.Restore, payload{"id": second}}); r.Field != "id" ||
		!strings.Contains(r.Message, "restore it first") {
		t.Errorf("the restoration of a connection of an output in the trash is refused at %s: %s", r.Field,
			r.Message)
	}
	// Once the branch is in the trash too, whose output's restoration is
	// refused until it comes back, the refusal names the branch first.
	f.must(t, cmd{node.Delete, payload{"id": branch}})
	if r := f.refused(t, cmd{connection.Restore, payload{"id": second}}); r.Field != "id" ||
		!strings.Contains(r.Message, "only once its nodes do") {
		t.Errorf("the restoration of a connection of an output of a branch in the trash is refused at %s: %s",
			r.Field, r.Message)
	}
	if r := f.refused(t, cmd{connection.Delete, payload{"id": second}}); r.Field != "id" ||
		!strings.Contains(r.Message, "comes back with its nodes") {
		t.Errorf("the deletion of a connection of an output of a branch in the trash is refused at %s: %s",
			r.Field, r.Message)
	}
}
