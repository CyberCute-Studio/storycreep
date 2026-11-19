//go:build synthetic_schema

package schemaversion

// Register adds v to the versions that the server reads, as the newest, as
// package schemaversion/synthetic registers the test-only synthetic version
// (note N2). It exists only with the synthetic_schema build tag, so that a
// release build reads the real versions alone, and it panics unless v is the
// version after the newest, with a validator and a migration.
func Register(v Version) {
	register(v)
}
