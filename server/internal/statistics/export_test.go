package statistics

import (
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
)

// SetCountRows sets how many texts the job of the statistics counts in a
// transaction at most, until the test ends.
func SetCountRows(t interface{ Cleanup(func()) }, n int32) {
	old := countRows
	countRows = n
	t.Cleanup(func() { countRows = old })
}

// SetCountLockWait sets how long the job of the statistics waits for a
// project's lock at most, until the test ends.
func SetCountLockWait(t interface{ Cleanup(func()) }, d time.Duration) {
	old := countLockWait
	countLockWait = d
	t.Cleanup(func() { countLockWait = old })
}

// NameNeutral returns the types of command whose changes of what texts name
// the derivation passes over (nameNeutral), for TestNameNeutral.
func NameNeutral() []commands.Type { return nameNeutral }
