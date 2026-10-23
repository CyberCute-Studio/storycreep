package component

// SetMaxUndoValues sets the most bytes that the undo of a batch may take once
// a change of an attribute definition's options or type adds the values that
// it loses to n, for the test in component_test of the refusal of a change
// whose undo would take it past that, which is not parallel, so that no other
// test's change reads it, and returns what sets it back.
func SetMaxUndoValues(n int) (restore func()) {
	old := maxUndoValues
	maxUndoValues = n
	return func() { maxUndoValues = old }
}

// SetDefinitionRead has s.Unconvertible call fn once it has read the
// attribute definition, before it reads its values, with whether it locked
// the definition, for the test in component_test of a change of the
// definition that commits in between, which calls it on a Service of its own
// before any read.
func SetDefinitionRead(s *Service, fn func(locked bool)) {
	s.definitionRead = fn
}
