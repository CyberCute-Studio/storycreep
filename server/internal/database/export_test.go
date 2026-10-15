package database

// MigrationLock is the advisory lock key, for the tests in database_test,
// which hold the lock to see Migrate wait for it.
const MigrationLock = migrationLock

// ScramMatches is scramMatches, for the tests in database_test that check it
// against the verifiers PostgreSQL computes.
var ScramMatches = scramMatches

// QuoteLiteral is quoteLiteral, for the tests in database_test that have
// PostgreSQL compute a verifier from a password.
var QuoteLiteral = quoteLiteral

// CheckServerVersion is checkServerVersion, for the test in database_test of
// the versions that the test servers do not run.
var CheckServerVersion = checkServerVersion
