package node_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/connection"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/node"
)

// newOutputID returns a new branch output's ID.
func newOutputID() string {
	return ids.Encode(ids.BranchOutput, ids.New())
}

// createOutput returns the command that creates the output id of the branch,
// with the fields of extra.
func createOutput(id, branch string, extra payload) cmd {
	p := payload{"id": id, "node_id": branch}
	for k, v := range extra {
		p[k] = v
	}
	return cmd{node.CreateOutput, p}
}

// outputs returns the outputs of the branch, as the API shows them, each as
// its ID and its kind, in their order.
func (f *fixture) outputs(t *testing.T, branch string) []string {
	t.Helper()
	var out []string
	for _, o := range f.get(t, branch).Outputs {
		out = append(out, o.ID+" "+o.Kind)
	}
	return out
}

// TestOutputs checks that a branch's outputs take the kinds of their places,
// its first conditional output its if and the others its elseifs, as they are
// created, reordered, deleted, and restored, which changes the kinds of
// those whose places change, and that an else comes after them, one at most,
// and only while the branch has a conditional output. The order's effect on
// the exported JSON is item 76's to check (FR-EDIT-04 AC2).
func TestOutputs(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	b := f.newBoard(t, f.project, "Harbour")
	branch := f.newNode(t, b, node.TypeBranch, nil)
	element := f.newNode(t, b, node.TypeElement, nil)
	first, second, otherwise := newOutputID(), newOutputID(), newOutputID()

	if r := f.refused(t, createOutput(otherwise, branch, payload{"else": true})); r.Field != "else" {
		t.Errorf("an else of a branch without a conditional output is refused at %s: %s", r.Field, r.Message)
	}
	if r := f.refused(t, createOutput(first, element, nil)); r.Field != "node_id" {
		t.Errorf("an output of an element is refused at %s: %s", r.Field, r.Message)
	}
	if got := f.invalid(t, createOutput(otherwise, branch, payload{"else": true, "condition": "true"})); !slices.Equal(
		got, []string{"payload.condition"}) {
		t.Errorf("an else with a condition is refused at %v", got)
	}

	f.must(t, createOutput(first, branch, payload{"condition": "true", "order": "M"}),
		createOutput(second, branch, payload{"condition": "false", "order": "T"}),
		createOutput(otherwise, branch, payload{"else": true}))
	want := []string{first + " if", second + " elseif", otherwise + " else"}
	if got := f.outputs(t, branch); !slices.Equal(got, want) {
		t.Errorf("the branch's outputs are %v, want %v", got, want)
	}
	if r := f.refused(t, createOutput(newOutputID(), branch, payload{"else": true})); r.Field != "else" {
		t.Errorf("a second else is refused at %s: %s", r.Field, r.Message)
	}
	for _, c := range []cmd{{node.UpdateOutput, payload{"id": otherwise, "condition": "true"}},
		{node.ReorderOutput, payload{"id": otherwise, "order": "A"}}} {
		if r := f.refused(t, c); r.Field != "condition" && r.Field != "id" {
			t.Errorf("%s of an else is refused at %s: %s", c.typ, r.Field, r.Message)
		}
	}

	// Placing the second before the first makes it the if, and changes both.
	res := f.must(t, cmd{node.ReorderOutput, payload{"id": second, "order": "F"}})
	if got, want := f.outputs(t, branch), []string{second + " if", first + " elseif", otherwise + " else"}; !slices.Equal(
		got, want) {
		t.Errorf("once reordered, the branch's outputs are %v, want %v", got, want)
	}
	for _, id := range []string{first, second} {
		if !slices.ContainsFunc(res.Revisions, func(r commands.Revision) bool {
			return r.Kind == ids.BranchOutput && ids.Encode(ids.BranchOutput, r.ID) == id
		}) {
			t.Errorf("the reorder changed the kind of %s, which its answer does not list: %v", id, res.Revisions)
		}
	}
	if err := f.undo(t, res.Seq); err != nil {
		t.Fatal(err)
	}
	if got := f.outputs(t, branch); !slices.Equal(got, want) {
		t.Errorf("once the reorder is undone, the branch's outputs are %v, want %v", got, want)
	}

	// The else needs a conditional output, so the last is deleted after it.
	f.must(t, cmd{node.DeleteOutput, payload{"id": first}})
	if got, want := f.outputs(t, branch), []string{second + " if", otherwise + " else"}; !slices.Equal(got, want) {
		t.Errorf("once the if is deleted, the branch's outputs are %v, want %v", got, want)
	}
	if r := f.refused(t, cmd{node.DeleteOutput, payload{"id": second}}); r.Field != "id" {
		t.Errorf("the deletion of the last conditional output of a branch with an else is refused at %s: %s",
			r.Field, r.Message)
	}
	res = f.must(t, cmd{node.DeleteOutput, payload{"id": otherwise}}, cmd{node.DeleteOutput, payload{"id": second}})
	if got := f.outputs(t, branch); len(got) != 0 {
		t.Errorf("the branch's outputs are %v, want none", got)
	}
	if r := f.refused(t, cmd{node.RestoreOutput, payload{"id": otherwise}}); r.Field != "id" {
		t.Errorf("the restoration of an else without a conditional output is refused at %s: %s", r.Field,
			r.Message)
	}
	if err := f.undo(t, res.Seq); err != nil {
		t.Fatal(err)
	}
	if got, want := f.outputs(t, branch), []string{second + " if", otherwise + " else"}; !slices.Equal(got, want) {
		t.Errorf("once the deletions are undone, the branch's outputs are %v, want %v", got, want)
	}
	// Restoring the first, which comes first, makes it the if again.
	f.must(t, cmd{node.RestoreOutput, payload{"id": first}})
	if got, want := f.outputs(t, branch), []string{first + " if", second + " elseif", otherwise + " else"}; !slices.Equal(
		got, want) {
		t.Errorf("once the first is restored, the branch's outputs are %v, want %v", got, want)
	}

	// An output of a branch in the trash is in the trash with it.
	f.must(t, cmd{node.Delete, payload{"id": branch}})
	for _, c := range []cmd{{node.UpdateOutput, payload{"id": first, "condition": "false"}},
		{node.DeleteOutput, payload{"id": first}}, {node.RestoreOutput, payload{"id": first}},
		createOutput(newOutputID(), branch, nil)} {
		f.refused(t, c)
	}
	f.must(t, cmd{node.Restore, payload{"id": branch}})
	if got, want := f.outputs(t, branch), []string{first + " if", second + " elseif", otherwise + " else"}; !slices.Equal(
		got, want) {
		t.Errorf("once the branch is restored, its outputs are %v, want %v", got, want)
	}
	if n := f.count(t, "SELECT count(*) FROM live_branch_output WHERE node_id = $1", decode(t, ids.Node, branch)); n != 3 {
		t.Errorf("the branch has %d live outputs, want 3", n)
	}
}

// TestOutputConnections checks that an output's deletion that is not
// confirmed is refused while a connection that is not in the trash leaves it,
// with the connections that leave it as its reason, and that one that is
// deletes them with it, in its command, and its undo, the output's
// restoration, brings them back, but for one whose node at the other end is
// in the trash, which its answer lists.
func TestOutputConnections(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	b := f.newBoard(t, f.project, "Harbour")
	branch := f.newNode(t, b, node.TypeBranch, nil)
	keeper := f.newNode(t, b, node.TypeElement, nil)
	gull := f.newNode(t, b, node.TypeElement, nil)
	output := newOutputID()
	toKeeper, toGull := ids.Encode(ids.Connection, ids.New()), ids.Encode(ids.Connection, ids.New())
	f.must(t, createOutput(output, branch, nil),
		cmd{connection.Create, payload{"id": toKeeper, "from_node_id": branch, "from_port": output,
			"to_node_id": keeper}},
		cmd{connection.Create, payload{"id": toGull, "from_node_id": branch, "from_port": output, "to_node_id": gull}})

	r := f.refused(t, cmd{node.DeleteOutput, payload{"id": output}})
	var connected *node.ConnectedError
	if r.Field != "confirm" || !errors.As(r.Reason, &connected) || connected.Count != 2 ||
		len(connected.Connections) != 2 {
		t.Errorf("the deletion of an output that connections leave is refused at %s: %s, for %+v", r.Field,
			r.Message, r.Reason)
	}
	live := func(id string) int64 {
		return f.count(t, "SELECT count(*) FROM live_connection WHERE id = $1", decode(t, ids.Connection, id))
	}
	ops, _ := f.operations(t)
	f.must(t, cmd{node.DeleteOutput, payload{"id": output, "confirm": true}})
	if after, _ := f.operations(t); after != ops+1 || live(toKeeper)+live(toGull) != 0 {
		t.Errorf("the confirmed deletion made %d operations, and left %d connections", after-ops,
			live(toKeeper)+live(toGull))
	}

	f.must(t, cmd{node.Delete, payload{"id": gull}})
	res2, err := f.apply(t, f.project, cmd{node.RestoreOutput, payload{"id": output}})
	if err != nil {
		t.Fatal(err)
	}
	if live(toKeeper) != 1 || live(toGull) != 0 || len(res2.NotReconnected) != 1 ||
		ids.Encode(ids.Connection, res2.NotReconnected[0].ID) != toGull ||
		len(res2.NotReconnected[0].WaitingFor) != 1 ||
		ids.Encode(ids.Node, res2.NotReconnected[0].WaitingFor[0].ID) != gull {
		t.Errorf("the restored output reconnected %d and %d, and left %+v", live(toKeeper), live(toGull),
			res2.NotReconnected)
	}

	// The restoration of the node at the other end of a connection whose
	// output is in the trash lists it as waiting for the output.
	f.must(t, cmd{node.DeleteOutput, payload{"id": output, "confirm": true}})
	res2, err = f.apply(t, f.project, cmd{node.Restore, payload{"id": gull}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.NotReconnected) != 1 || ids.Encode(ids.Connection, res2.NotReconnected[0].ID) != toGull ||
		len(res2.NotReconnected[0].WaitingFor) != 1 ||
		res2.NotReconnected[0].WaitingFor[0].Kind != ids.BranchOutput {
		t.Errorf("the restored node left %+v, want its connection waiting for the output", res2.NotReconnected)
	}

	// A board's restoration lists the connections of its nodes that wait
	// for an output deleted on its own.
	f.must(t, cmd{board.Delete, payload{"id": b}})
	res2, err = f.apply(t, f.project, cmd{board.Restore, payload{"id": b}})
	if err != nil {
		t.Fatal(err)
	}
	var waiting []string
	for _, u := range res2.NotReconnected {
		waiting = append(waiting, ids.Encode(ids.Connection, u.ID))
	}
	slices.Sort(waiting)
	want := []string{toKeeper, toGull}
	slices.Sort(want)
	if !slices.Equal(waiting, want) {
		t.Errorf("the restored board left %v, want %v waiting for the output", waiting, want)
	}
}
