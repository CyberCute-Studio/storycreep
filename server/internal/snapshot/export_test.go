package snapshot

// SetHooks sets what s's builds run, for the tests: validated once a build
// validated the draft, in the transaction that read it, and serialized with
// the files that the serializer wrote, before they are validated, which it
// may change.
func (s *Service) SetHooks(validated func(), serialized func(files map[string][]byte)) {
	s.hooks = hooks{validated: validated, serialized: serialized}
}
