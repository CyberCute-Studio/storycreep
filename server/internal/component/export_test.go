package component

// SetMaxUndoValues sets the most bytes that the values that the undo of a
// change of an attribute definition's options or type holds take in its
// payload to n, for the test in component_test of the refusal of a change
// whose undo would hold more, which is not parallel, so that no other test's
// change reads it, and returns what sets it back.
func SetMaxUndoValues(n int) (restore func()) {
	old := maxUndoValues
	maxUndoValues = n
	return func() { maxUndoValues = old }
}

// SetDefinitionRead has Service.Unconvertible call fn once it has read the
// attribute definition, before it reads its values, for the test in
// component_test of a change of the definition that commits in between,
// which is not parallel, so that no other test's read calls it, and returns
// what sets it back.
func SetDefinitionRead(fn func()) (restore func()) {
	old := definitionRead
	definitionRead = fn
	return func() { definitionRead = old }
}
