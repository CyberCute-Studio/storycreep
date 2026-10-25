package flow

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/published"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// ErrState is LoadState's error for a state that it does not load.
var ErrState = errors.New("flow: a state that this runtime does not load")

// SaveState returns the runtime's state as a save state (PRD §8.5), as
// save_state() does: the snapshot's project, its release's ID and version, or
// neither of a draft, its schemaVersion, the cursor, unless the flow has not
// started, the value of every variable and of every component's runtime
// attribute, and the visit count of every node that the flow has entered.
// Neither a halt nor the current choices are part of it: LoadState builds the
// choices again. It panics where the flow is not at rest (Runtime), as when
// the listener calls it at an enter, whose on-enter has not run, which no
// cursor's phase says, or after a panic of the listener stopped a call there.
func (r *Runtime) SaveState() published.SaveState {
	if !r.atRest() {
		panic("flow: the state was saved where the flow is not at rest, between two nodes")
	}
	m := &r.s.manifest
	st := published.SaveState{
		Attributes:    make(map[string]published.AttributeValues),
		ProjectID:     m.Project.ID,
		SchemaVersion: m.SchemaVersion,
		StateVersion:  published.StateVersion,
		Variables:     make(map[string]value.Value),
		Visits:        make(published.Visits),
	}
	if rel, ok := m.Snapshot.(*published.ReleaseSnapshot); ok {
		st.SnapshotID, st.SnapshotVersion = rel.ID, rel.Version
	}
	if r.current != "" {
		st.Cursor = &published.Cursor{BoardID: r.s.nodes[r.current].board, NodeID: r.current, Phase: r.phase}
	}
	for _, id := range r.s.variables {
		st.Variables[id] = jsonOf(r.vars[id], r.s.declared[id].Type)
	}
	for _, k := range r.s.attributes {
		if st.Attributes[k.component] == nil {
			st.Attributes[k.component] = make(published.AttributeValues)
		}
		st.Attributes[k.component][k.attribute] = jsonOf(r.attrs[k], r.attrs[k].Type)
	}
	for id, n := range r.visits {
		st.Visits[id] = published.Int(n)
	}
	return st
}

// LoadState sets the runtime's state to st, a state saved against the
// runtime's snapshot, as load_state() does (§5.8.5): the values, the visit
// counts, and the cursor, without the on-exit of the node that was current,
// as no board entry, and without the visit count, the enter, or the on-enter
// of the cursor's node, whose choices it builds again when the cursor awaits
// one, from the conditions as they hold of st, and which it does not end when
// none holds. It clears a halt.
//
// It loads a state of the snapshot alone, whose entities are those of the
// snapshot, and returns an error that wraps ErrState, and changes nothing, of
// any other: of another project or snapshot, of another stateVersion, which
// published.Decode refuses, that lacks a variable or a runtime attribute of
// the snapshot or holds one that the snapshot does not have, or attributes
// of a component without any, which SaveState never writes, or a value that
// its declaration's type does not read (§5.8.9, valueOf), a visit count of a
// node that the snapshot does not have, or of a jumper, which the flow never
// enters, or below 1, or beyond published.MaxInt, at which the flow's counts
// stop, as a save state's schema bounds them, which a state built in Go need
// not be, or whose cursor is not of a node of its board, which is
// no jumper, in a phase that is neither awaiting_choice nor ended, which
// published.Decode refuses too, and awaits a choice only at an element that
// is no ending and that a connection leaves, as no flow awaits one
// elsewhere: it passes through a branch, and ends at an ending and at a dead
// end (§5.8.5).
// Loading a state saved against another snapshot, with what PRD §8.5 says of
// its entities that have changed, is the Godot runtime's and play mode's
// (FR-GODOT-07, FR-PLAY-05).
func (r *Runtime) LoadState(st published.SaveState) error {
	vars, attrs, visits, err := r.s.state(st)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrState, err)
	}
	r.call(true, func() {
		r.vars, r.attrs, r.visits = vars, attrs, visits
		r.current, r.phase, r.choices = "", "", nil
		if c := st.Cursor; c != nil {
			// The flow comes to rest, of the cursor's phase, once its
			// choices are built, whose conditions may report a runtime error,
			// at which it is not at rest.
			r.current = c.NodeID
			if c.Phase == published.PhaseAwaitingChoice {
				r.choices = r.hold(r.s.nodes[c.NodeID].ports[out])
			}
			r.phase = c.Phase
		}
	})
	return nil
}

// state returns the values and the visit counts of st, a state of the
// snapshot, or why it is none (LoadState).
func (s *Snapshot) state(st published.SaveState) (map[string]expr.Value, map[attributeKey]expr.Value,
	map[string]int64, error) {
	m := &s.manifest
	var id, version string
	if rel, ok := m.Snapshot.(*published.ReleaseSnapshot); ok {
		id, version = rel.ID, rel.Version
	}
	switch {
	case st.StateVersion != published.StateVersion:
		return nil, nil, nil, fmt.Errorf("its stateVersion is %d", st.StateVersion)
	case st.ProjectID != m.Project.ID || st.SnapshotID != id || st.SnapshotVersion != version ||
		st.SchemaVersion != m.SchemaVersion:
		return nil, nil, nil, errors.New("it was saved against another snapshot")
	}
	vars := make(map[string]expr.Value)
	if !slices.Equal(slices.Sorted(maps.Keys(st.Variables)), slices.Sorted(slices.Values(s.variables))) {
		return nil, nil, nil, errors.New("its variables are not the snapshot's")
	}
	for _, vid := range slices.Sorted(maps.Keys(st.Variables)) {
		read, err := valueOf(st.Variables[vid], s.declared[vid].Type, nil)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("the value of the variable %s %w", vid, err)
		}
		vars[vid] = read
	}
	attrs := make(map[attributeKey]expr.Value)
	n := 0
	for _, c := range slices.Sorted(maps.Keys(st.Attributes)) {
		values := st.Attributes[c]
		if len(values) == 0 {
			return nil, nil, nil, fmt.Errorf("it holds attributes of %s, but none of its values", c)
		}
		for _, a := range slices.Sorted(maps.Keys(values)) {
			k := attributeKey{c, a}
			typ, ok := s.declaredTypes[k]
			if !ok {
				return nil, nil, nil, fmt.Errorf("%s of %s is no runtime attribute of the snapshot", a, c)
			}
			read, err := valueOf(values[a], typ, s.options[k])
			if err != nil {
				return nil, nil, nil, fmt.Errorf("the value of %s of %s %w", a, c, err)
			}
			attrs[k] = read
			n++
		}
	}
	if n != len(s.attributes) {
		return nil, nil, nil, errors.New("it lacks a runtime attribute of the snapshot")
	}
	visits := make(map[string]int64)
	for _, nid := range slices.Sorted(maps.Keys(st.Visits)) {
		count, n := st.Visits[nid], s.nodes[nid]
		if n == nil || count < 1 || count > published.MaxInt {
			return nil, nil, nil, fmt.Errorf("its visit count of %s is not of a node of the snapshot, or below 1, or "+
				"beyond %d", nid, int64(published.MaxInt))
		}
		if _, jumper := n.node.(*published.Jumper); jumper {
			return nil, nil, nil, fmt.Errorf("it has a visit count of the jumper %s, which the flow never enters", nid)
		}
		visits[nid] = int64(count)
	}
	if c := st.Cursor; c != nil {
		n := s.nodes[c.NodeID]
		if n == nil || n.board != c.BoardID {
			return nil, nil, nil, fmt.Errorf("its cursor's node %s is not a node of its board %s", c.NodeID,
				c.BoardID)
		}
		if c.Phase != published.PhaseAwaitingChoice && c.Phase != published.PhaseEnded {
			return nil, nil, nil, fmt.Errorf("its cursor's phase is %q, neither %s nor %s", c.Phase,
				published.PhaseAwaitingChoice, published.PhaseEnded)
		}
		awaits := c.Phase == published.PhaseAwaitingChoice
		switch e := n.node.(type) {
		case *published.Jumper:
			return nil, nil, nil, fmt.Errorf("its cursor is at the jumper %s", c.NodeID)
		case *published.Branch:
			if awaits {
				return nil, nil, nil, fmt.Errorf("its cursor awaits a choice at the branch %s", c.NodeID)
			}
		case *published.Element:
			if awaits && (e.IsEnding || len(n.ports[out]) == 0) {
				return nil, nil, nil, fmt.Errorf("its cursor awaits a choice at %s, an ending or a dead end",
					c.NodeID)
			}
		}
	}
	return vars, attrs, visits, nil
}
