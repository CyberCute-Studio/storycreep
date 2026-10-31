package search

import (
	"context"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/sweep"
)

// Work runs the job of the index once, as its worker does, for as long as
// more reports that the run has time left (work).
func Work(ctx context.Context, db sweep.DB, more func() bool) error { return work(ctx, db, more) }

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
