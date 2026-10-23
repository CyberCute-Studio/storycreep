package component

// SetDefinitionRead has s.Unconvertible call fn once it has read the
// attribute definition, before it reads its values, for the test in
// component_test of a change that commits in between, which calls it on a
// Service of its own before any read.
func SetDefinitionRead(s *Service, fn func()) {
	s.definitionRead = fn
}
