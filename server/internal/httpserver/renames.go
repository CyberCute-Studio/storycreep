package httpserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/component"
	"github.com/CyberCute-Studio/storycreep/server/internal/expression"
	"github.com/CyberCute-Studio/storycreep/server/internal/ident"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
	"github.com/CyberCute-Studio/storycreep/server/internal/variable"
)

type variableRenameInput struct {
	ProjectID  string `path:"project_id" doc:"The project's ID"`
	VariableID string `path:"variable_id" doc:"The variable's ID"`
	Key        string `query:"key" required:"true" maxLength:"64" doc:"The variable's new key, as a variable.rename command would give it"`
	Rewrite    string `query:"rewrite" required:"true" enum:"renamed,qualified" doc:"Which rewrites the list holds: those of the names of the variable itself, renamed, or those of the names of other variables and components' attributes that the new key would take, which the rename qualifies, qualified"`
	api.PageParams
}

type componentRenameInput struct {
	ProjectID   string `path:"project_id" doc:"The project's ID"`
	ComponentID string `path:"component_id" doc:"The component's ID"`
	Key         string `query:"key" required:"true" maxLength:"64" doc:"The component's new key, as a component.set_key command would give it"`
	Rewrite     string `query:"rewrite" required:"true" enum:"renamed,qualified" doc:"Which rewrites the list holds: those of the names of the component's attributes, renamed, or those of the names of other entities that the new key would take, qualified, of which a component's key takes none, so that the list is empty"`
	api.PageParams
}

type renamePreviewOutput struct {
	Body api.Page[expression.RenameSite]
}

// renameKey is the key of a list of a rename's preview that its pages'
// cursors hold (expression.PreviewKey), each UUID in its text form.
type renameKey struct {
	Board      string `json:"board"`
	Node       string `json:"node"`
	Expression string `json:"expression"`
}

// renamePreviewDoc says, in the description of an operation that previews a
// rename, what it lists, and how.
const renamePreviewDoc = " Each site is an expression of the project, in its trash or not, which the rename rewrites " +
	"alike, as in_trash says, with its board and node, by which a client opens it, its source, the source that the " +
	"rename writes in its place, with every name that it rewrites, and the names that it rewrites of the list's " +
	"kind, each with where it is in the source, as it is, what it becomes, and what it names. A name is rewritten " +
	"in the shortest form that names what it named from the expression's board (PRD FR-VAR-03): a variable's bare " +
	"key, unless a variable of the board has the key of a global one, which global.<key> names, and a " +
	"component's attribute as <component>.<attribute>, unless a variable of the board or a global one has the " +
	"component's key, which cmp.<component>.<attribute> names past; the other names stay as the source writes " +
	"them, and so does a name that names nothing, which the rename may make name what it renames. The sites are " +
	"in the order of their boards, their nodes, and the expressions themselves. The rename itself rewrites the " +
	"sources as the project stands when it applies, which another change may have changed meanwhile."

// registerRenames registers the routes that preview what renaming a
// project's variable, or changing a component's key, rewrites (PRD FR-VAR-03,
// FR-COMP-02 AC2), before the command that does it is sent to the command
// endpoint (registerCommands).
func registerRenames(m *api.Mux, variables *variable.Service, components *component.Service) {
	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "previewVariableRename",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/variables/{variable_id}/rename-preview",
		Summary:     "Preview a variable's rename",
		Description: "Lists the expressions that renaming the variable to the key would rewrite, as the command " +
			"variable.rename rewrites them (PRD FR-VAR-03): those whose names of the variable it rewrites, or, as " +
			"rewrite says, those whose names of other variables or components' attributes it qualifies, which a " +
			"variable of a board renamed to the key of a global one, or a variable renamed to a component's key, " +
			"would take, listed apart." + renamePreviewDoc + projectNotFoundDoc + tokenNotFoundDoc + " A variable " +
			"that the project does not have, or that is in its trash, as one of a board in the trash is, is answered " +
			"with 404 not_found, a key that is no identifier with 422 key_invalid, and one that another variable of " +
			"the variable's scope has with 409 key_taken, as the rename would be." + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Variables"},
	}, func(ctx context.Context, in *variableRenameInput) (*renamePreviewOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		variableID, err := ids.Decode(ids.Variable, in.VariableID)
		if err != nil {
			return nil, err
		}
		after, err := renameCursor(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := variables.RenamePreview(ctx, actor, id, variableID, in.Key, in.Rewrite == "qualified", after,
			in.Fetch())
		if err != nil {
			return nil, renameError(variableError(err))
		}
		return renamePage(in.PageParams, list)
	}, api.TokenScope(scope.ProjectRead))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "previewComponentRename",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/components/{component_id}/rename-preview",
		Summary:     "Preview a change of a component's key",
		Description: "Lists the expressions that changing the component's key to the key would rewrite, as the " +
			"command component.set_key rewrites them (PRD FR-COMP-02 AC2, FR-VAR-03): those whose names of the " +
			"component's attributes it rewrites, or, as rewrite says, those whose names of other entities it " +
			"qualifies, which a component's key takes from none, so that that list is always empty." +
			renamePreviewDoc + projectNotFoundDoc + tokenNotFoundDoc + " A component that the project does not " +
			"have, or that is in its trash, is answered with 404 not_found, a key that is no identifier with 422 " +
			"key_invalid, and one that another component of the project has with 409 key_taken, as the change would " +
			"be." + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Components"},
	}, func(ctx context.Context, in *componentRenameInput) (*renamePreviewOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		componentID, err := ids.Decode(ids.Component, in.ComponentID)
		if err != nil {
			return nil, err
		}
		after, err := renameCursor(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := components.RenamePreview(ctx, actor, id, componentID, in.Key, in.Rewrite == "qualified", after,
			in.Fetch())
		if err != nil {
			return nil, renameError(componentError(err))
		}
		return renamePage(in.PageParams, list)
	}, api.TokenScope(scope.ProjectRead))
}

// renamePage returns the page of a list of a rename's preview, which a read
// read with p.Fetch() as its limit.
func renamePage(p api.PageParams, list []expression.RenameSite) (*renamePreviewOutput, error) {
	page, err := api.NewPage(p, list, func(s expression.RenameSite) renameKey {
		k := s.Key()
		return renameKey{Board: k.Board.String(), Node: k.Node.String(), Expression: k.Expression.String()}
	})
	if err != nil {
		return nil, err
	}
	return &renamePreviewOutput{Body: page}, nil
}

// renameCursor returns the key that cursor, a next_cursor of a list of a
// rename's preview, holds, or the zero key, before every site, for the empty
// cursor of the first page. A cursor that holds no key, or not in the form
// that the server writes, is answered with invalid_cursor.
func renameCursor(cursor string) (expression.PreviewKey, error) {
	if cursor == "" {
		return expression.PreviewKey{}, nil
	}
	key, err := api.DecodeCursor[renameKey](cursor)
	if err != nil {
		return expression.PreviewKey{}, err
	}
	var out expression.PreviewKey
	for _, f := range []struct {
		text string
		id   *ids.UUID
	}{{key.Board, &out.Board}, {key.Node, &out.Node}, {key.Expression, &out.Expression}} {
		id, err := ids.ParseUUID(f.text)
		if err != nil || id.String() != f.text {
			return expression.PreviewKey{}, api.NewError(api.CodeInvalidCursor)
		}
		*f.id = id
	}
	return out, nil
}

// renameError returns the error that the API answers err with, as a rename's
// preview found it, once its entity's error mapper has mapped its own: 422
// key_invalid for a key that is no identifier, with the rule that it breaks
// as details.reason, and 409 key_taken for one that another entity of the
// scope has, each at the query's key, as the rename itself answers them.
func renameError(err error) error {
	problems := []api.Problem{{Location: "query.key", Message: err.Error()}}
	var key *ident.Error
	switch {
	case errors.As(err, &key):
		problems[0].Message = key.Problem()
		return &api.Error{Code: api.CodeKeyInvalid, Details: map[string]any{"reason": key.Reason,
			"errors": problems}}
	case errors.Is(err, ident.ErrTaken):
		problems[0].Message = "is the key of another entity of the scope, which is not in the project's trash"
		return &api.Error{Code: api.CodeKeyTaken, Details: map[string]any{"errors": problems}}
	}
	return err
}
