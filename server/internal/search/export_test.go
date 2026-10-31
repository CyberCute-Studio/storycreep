package search

import "github.com/CyberCute-Studio/storycreep/server/internal/commands"

// SetReindexRows sets how many documents the job of the index writes in a
// transaction at most, until the test ends.
func SetReindexRows(t interface{ Cleanup(func()) }, n int32) {
	old := reindexRows
	reindexRows = n
	t.Cleanup(func() { reindexRows = old })
}

// Neutral returns the types of command that the derivation passes over
// (neutral), for TestNeutral.
func Neutral() []commands.Type { return neutral }
