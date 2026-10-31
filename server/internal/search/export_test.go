package search

import (
	"context"
	"log/slog"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/sweep"
)

// Work runs the job of the index once, as its worker does, for as long as
// more reports that the run has time left, warning in log (work), or
// nowhere for nil.
func Work(ctx context.Context, db sweep.DB, more func() bool, log *slog.Logger) error {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return work(ctx, db, more, log)
}

// SetReindexRows sets how many documents the job of the index writes in a
// transaction at most, until the test ends.
func SetReindexRows(t interface{ Cleanup(func()) }, n int32) {
	old := reindexRows
	reindexRows = n
	t.Cleanup(func() { reindexRows = old })
}

// SetReindexLockWait sets how long the job of the index waits for a
// project's lock at most, until the test ends.
func SetReindexLockWait(t interface{ Cleanup(func()) }, d time.Duration) {
	old := reindexLockWait
	reindexLockWait = d
	t.Cleanup(func() { reindexLockWait = old })
}

// Neutral returns the types of command that the derivation passes over
// (neutral), for TestNeutral.
func Neutral() []commands.Type { return neutral }
