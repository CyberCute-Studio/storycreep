package httpserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/dependency"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
)

type variableUsagesInput struct {
	ProjectID  string `path:"project_id" doc:"The project's ID"`
	VariableID string `path:"variable_id" doc:"The variable's ID"`
	Access     string `query:"access" required:"true" enum:"read,write" doc:"Which usages the list holds: the sites that read the variable, or those that write it, which a script assigns"`
	api.PageParams
}

type componentUsagesInput struct {
	ProjectID   string `path:"project_id" doc:"The project's ID"`
	ComponentID string `path:"component_id" doc:"The component's ID"`
	Use         string `query:"use" required:"true" enum:"speaker,mention,read,write" doc:"Which usages the list holds: the nodes whose speaker the component is, the texts that mention it, or the sites that read its attributes, or those that write them"`
	api.PageParams
}

type usagesOutput struct {
	Body api.Page[dependency.Usage]
}

// usageKey is the key of a list of usages that its pages' cursors hold
// (dependency.Key), each UUID in its text form, the zero UUID where a usage
// has none.
type usageKey struct {
	Board     string `json:"board"`
	Node      string `json:"node"`
	Site      string `json:"site"`
	Attribute string `json:"attribute"`
}

// usagesDoc says, in the description of an operation that lists usages, how
// they are listed and found.
const usagesDoc = " Each usage names its site and the site's board and node, by which a client opens it: a node's " +
	"script, text, or speaker is of the node, a connection's condition, script, or label of the node that the " +
	"connection leaves, an output's condition of its branch, and a component's description of no board and no " +
	"node. The usages are those of the sites that are not in the project's trash, in the order of their boards, " +
	"their nodes, and the sites themselves. An expression that does not compile uses nothing, since it names " +
	"what it names by keys that resolve to nothing, and a text uses what its value references show, and the " +
	"components that it mentions, by their IDs, whatever becomes of their keys and names."

// registerDependencies registers the routes that read what uses a project's
// variables and components (PRD FR-DEP-01, FR-COMP-04, FR-TEXT-02,
// FR-TEXT-03), from the index that the command endpoint keeps as it changes
// the content (registerCommands).
func registerDependencies(m *api.Mux, dependencies *dependency.Service) {
	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "listVariableUsages",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/variables/{variable_id}/usages",
		Summary:     "List a variable's usages",
		Description: "Lists the sites of the project that read the variable, or those that write it, as access says " +
			"(PRD FR-DEP-01): the scripts and the conditions that read it or assign it, a script that updates it, " +
			"as with +=, both reading and writing it, and the texts whose value references show it, which read it." +
			usagesDoc + projectNotFoundDoc + tokenNotFoundDoc + " A variable that the project does not have, or " +
			"that is in its trash, as one of a board in the trash is, is answered with 404 not_found." +
			tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Variables"},
	}, func(ctx context.Context, in *variableUsagesInput) (*usagesOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		variableID, err := ids.Decode(ids.Variable, in.VariableID)
		if err != nil {
			return nil, err
		}
		after, err := usageCursor(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := dependencies.VariableUsages(ctx, actor, id, variableID, dependency.Access(in.Access), after,
			in.Fetch())
		if err != nil {
			return nil, dependencyError(err)
		}
		return usagePage(in.PageParams, list)
	}, api.TokenScope(scope.ProjectRead))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "listComponentUsages",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/components/{component_id}/usages",
		Summary:     "List a component's usages",
		Description: "Lists the sites of the project that use the component, as use says (PRD FR-COMP-04): the " +
			"nodes whose speaker it is, the texts that mention it (PRD FR-TEXT-03), or the sites that read its " +
			"attributes, or those that write them, each with the attribute that it reads or writes, and once for " +
			"each of them that it reads or writes: the scripts and the conditions that read or assign its runtime " +
			"attributes, and the texts whose value references show its attributes, runtime or not, as one that " +
			"is no longer runtime is. A site of another component's attribute of the same definition, as " +
			"mira.trust is of elara.trust's, is no usage of the component." + usagesDoc +
			projectNotFoundDoc + tokenNotFoundDoc + " A component that the project does not have, or that is in its " +
			"trash, is answered with 404 not_found." + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Components"},
	}, func(ctx context.Context, in *componentUsagesInput) (*usagesOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		componentID, err := ids.Decode(ids.Component, in.ComponentID)
		if err != nil {
			return nil, err
		}
		after, err := usageCursor(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := dependencies.ComponentUsages(ctx, actor, id, componentID, dependency.ComponentUse(in.Use), after,
			in.Fetch())
		if err != nil {
			return nil, dependencyError(err)
		}
		return usagePage(in.PageParams, list)
	}, api.TokenScope(scope.ProjectRead))
}

// usagePage returns the page of a list of usages, which a query read with
// p.Fetch() as its limit.
func usagePage(p api.PageParams, list []dependency.Usage) (*usagesOutput, error) {
	page, err := api.NewPage(p, list, func(u dependency.Usage) usageKey {
		k := u.Key()
		return usageKey{Board: k.Board.String(), Node: k.Node.String(), Site: k.Site.String(),
			Attribute: k.Attribute.String()}
	})
	if err != nil {
		return nil, err
	}
	return &usagesOutput{Body: page}, nil
}

// usageCursor returns the key that cursor, a next_cursor of a list of
// usages, holds, or the zero key, before every usage, for the empty cursor of
// the first page. A cursor that holds no key, or not in the form that the
// server writes, is answered with invalid_cursor.
func usageCursor(cursor string) (dependency.Key, error) {
	if cursor == "" {
		return dependency.Key{}, nil
	}
	key, err := api.DecodeCursor[usageKey](cursor)
	if err != nil {
		return dependency.Key{}, err
	}
	var out dependency.Key
	for _, f := range []struct {
		text string
		id   *ids.UUID
	}{{key.Board, &out.Board}, {key.Node, &out.Node}, {key.Site, &out.Site}, {key.Attribute, &out.Attribute}} {
		id, err := ids.ParseUUID(f.text)
		if err != nil || id.String() != f.text {
			return dependency.Key{}, api.NewError(api.CodeInvalidCursor)
		}
		*f.id = id
	}
	return out, nil
}

// dependencyError returns the error that the API answers err with, an error
// of package dependency or of the project that it reads the usages of
// (projectError), or err itself if it is none of them.
func dependencyError(err error) error {
	if errors.Is(err, dependency.ErrNotFound) {
		return api.NewError(api.CodeNotFound)
	}
	return projectError(err)
}
