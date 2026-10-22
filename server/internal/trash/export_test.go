package trash

import (
	"context"

	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/sweep"
)

// PurgeWorkspace purges the trash of the projects of the workspace
// workspaceID alone, as a run of Purge does once it reaches the workspace,
// for the test in trash_test that the purge of a workspace deletes nothing
// of another's, even what has been in its trash for Retention.
func PurgeWorkspace(ctx context.Context, db sweep.DB, workspaceID ids.UUID) (int64, error) {
	return purgeWorkspace(ctx, db, func() bool { return true }, workspaceID, database.Interval(Retention))
}
