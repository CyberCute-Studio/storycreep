package httpserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/playstate"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
)

type playStatesInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
	api.PageParams
}

type playStatesOutput struct {
	Body api.Page[playstate.PlayStateSummary]
}

type playStateInput struct {
	ProjectID   string `path:"project_id" doc:"The project's ID"`
	PlayStateID string `path:"play_state_id" doc:"The play state's ID"`
}

type playStateOutput struct {
	Body playstate.PlayStateBody
}

// playStateChangesDoc says, in the description of an operation that reads
// play states, how they are changed.
const playStateChangesDoc = " They change through the project's command endpoint, POST " +
	"/api/v1/projects/{project_id}/commands, whose commands of the type play_state.* save, rename, replace the " +
	"state of, delete, and restore them, which an owner, an admin, or an editor of the project may send."

// registerPlayStates registers the routes that read a project's saved play
// states (PRD FR-PLAY-05), which change through the command endpoint
// (registerCommands).
func registerPlayStates(m *api.Mux, playStates *playstate.Service) {
	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "listPlayStates",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/play-states",
		Summary:     "List a project's saved play states",
		Description: "Lists the project's saved play states that are not in its trash, each without its state, " +
			"which GET /api/v1/projects/{project_id}/play-states/{play_state_id} returns, in the order of their IDs, " +
			"oldest first (PRD FR-PLAY-05). Every member who reads the project sees them all, whoever saved them." +
			playStateChangesDoc + projectNotFoundDoc + tokenNotFoundDoc + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Play states"},
	}, func(ctx context.Context, in *playStatesInput) (*playStatesOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		after, err := cursorID(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := playStates.List(ctx, actor, id, after, in.Fetch())
		if err != nil {
			return nil, playStateError(err)
		}
		page, err := api.NewPage(in.PageParams, list, func(s playstate.PlayStateSummary) idKey {
			return uuidKey(ids.PlayState, s.ID)
		})
		if err != nil {
			return nil, err
		}
		return &playStatesOutput{Body: page}, nil
	}, api.TokenScope(scope.ProjectRead))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "getPlayState",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/play-states/{play_state_id}",
		Summary:     "Get a saved play state",
		Description: "Returns a saved play state of the project, with its state, a save state of the project's flow " +
			"(PRD §8.5), as it was saved, each number written in its shortest form, as a runtime writes it, which " +
			"play mode loads as a starting point (PRD FR-PLAY-05)." +
			playStateChangesDoc + projectNotFoundDoc + tokenNotFoundDoc + " A play state that the project does not " +
			"have, or that is in its trash, is answered with 404 not_found." + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Play states"},
	}, func(ctx context.Context, in *playStateInput) (*playStateOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		playStateID, err := ids.Decode(ids.PlayState, in.PlayStateID)
		if err != nil {
			return nil, err
		}
		s, err := playStates.Get(ctx, actor, id, playStateID)
		if err != nil {
			return nil, playStateError(err)
		}
		return &playStateOutput{Body: s}, nil
	}, api.TokenScope(scope.ProjectRead))
}

// playStateError returns the error that the API answers err with, an error of
// package playstate or of the project that it reads the play states of
// (projectError), or err itself if it is none of them.
func playStateError(err error) error {
	if errors.Is(err, playstate.ErrNotFound) {
		return api.NewError(api.CodeNotFound)
	}
	return projectError(err)
}
