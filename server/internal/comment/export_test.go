package comment

// TargetTypes returns the kinds of target, in the order of the enum of the
// API, for the test in comment_test that the API's enums of a target's kind
// list them.
func TargetTypes() []TargetType {
	out := make([]TargetType, len(targets))
	for i, t := range targets {
		out[i] = t.target
	}
	return out
}
