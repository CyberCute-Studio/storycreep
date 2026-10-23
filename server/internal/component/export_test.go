package component

// SetMaxUndoValues sets the most bytes of values that the undo of a change of
// an attribute definition's options or type holds to n, for the test in
// component_test of the refusal of a change whose undo would hold more, which
// is not parallel, so that no other test's change reads it, and returns what
// sets it back.
func SetMaxUndoValues(n int) (restore func()) {
	old := maxUndoValues
	maxUndoValues = n
	return func() { maxUndoValues = old }
}
