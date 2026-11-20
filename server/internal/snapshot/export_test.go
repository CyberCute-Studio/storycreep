package snapshot

import (
	"context"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/jobs"
)

// SetHooks sets what s's builds run, for the tests: validated once a build
// validated the draft, in the transaction that read it, and serialized with
// the files that the serializer wrote, before they are validated, which it
// may change.
func (s *Service) SetHooks(validated func(), serialized func(files map[string][]byte)) {
	s.hooks = hooks{validated: validated, serialized: serialized}
}

// Work runs the attempt attempt, of maxAttempts, at the job that builds the
// snapshot snapshotID of the workspace workspaceID, as the queue runs it.
func (s *Service) Work(ctx context.Context, workspaceID, snapshotID ids.UUID, attempt, maxAttempts int) error {
	return s.work(ctx, &jobs.Job[buildJob]{Attempt: attempt, MaxAttempts: maxAttempts,
		Args: buildJob{WorkspaceID: workspaceID.String(), SnapshotID: snapshotID.String()}})
}

// Render is render, for the tests.
var Render = render
