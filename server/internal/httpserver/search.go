package httpserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
	"github.com/CyberCute-Studio/storycreep/server/internal/search"
)

type searchInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
	Query     string `query:"q" required:"true" minLength:"1" maxLength:"200" doc:"What to search for, of 1 to 200 characters, without control characters: each of its words must start a word of a result's title or body, compared without regard to case or diacritics, so that ela tru finds Elara trusts and zlut finds Žluťoučký. A query without a word, as one of punctuation alone, finds nothing."`
	Type      string `query:"type" enum:"node,connection,variable,component" doc:"A kind of entity: only the results of that kind are listed. Without it, those of every kind."`
	api.PageParams
}

type searchOutput struct {
	Body api.Page[search.Result]
}

// searchKey is the key of a list of results that its pages' cursors hold
// (search.Key): the rank, as the server read it, the kind, and the entity's
// UUID in its text form.
type searchKey struct {
	Rank float32 `json:"rank"`
	Type string  `json:"type"`
	ID   string  `json:"id"`
}

// registerSearch registers the route that searches a project's content (PRD
// FR-PROJ-03), from the index that the command endpoint keeps as it changes
// the content (registerCommands).
func registerSearch(m *api.Mux, searches *search.Service) {
	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "searchProject",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/search",
		Summary:     "Search a project",
		Description: "Lists the project's nodes, connections, variables, and components that are not in its trash " +
			"whose titles or bodies hold a word that starts with each word of the query, compared without regard " +
			"to case or diacritics (PRD FR-PROJ-03): a node's title and text, a connection's label, a variable's " +
			"key and description, and a component's name, key, description, and values of text, of the types " +
			"string, text, and enum. A text is searched as its author wrote it, without what its value " +
			"references show or its mentions name, which the usage lists find (GET " +
			"/api/v1/projects/{project_id}/variables/{variable_id}/usages and its twin for components), and a " +
			"body as far as its first 100,000 characters. Each result names its kind, its board and its node, by " +
			"which a client opens it, and its title and a snippet of its body's first 10,000 characters, each in " +
			"parts, with the words that match a word of the query marked (AC2). The results are in the order of " +
			"how well each word of the query matches them, a match in a title above one in a body, then of their " +
			"kinds and IDs." +
			projectNotFoundDoc + tokenNotFoundDoc + " A query that holds a control character is answered with 422 " +
			"validation_failed at query.q, once the project is found." + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Search"},
	}, func(ctx context.Context, in *searchInput) (*searchOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		after, err := searchCursor(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := searches.Search(ctx, actor, id, in.Query, search.Kind(in.Type), after, in.Fetch())
		if err != nil {
			return nil, searchError(err)
		}
		page, err := api.NewPage(in.PageParams, list, func(r search.Result) searchKey {
			k := r.Key()
			return searchKey{Rank: k.Rank, Type: string(k.Kind), ID: k.ID.String()}
		})
		if err != nil {
			return nil, err
		}
		return &searchOutput{Body: page}, nil
	}, api.TokenScope(scope.ProjectRead))
}

// searchCursor returns the key that cursor, a next_cursor of a list of
// results, holds, or the zero key, before every result, for the empty cursor
// of the first page. A cursor that holds no key, or not in the form that the
// server writes, a kind that has a document and a UUID in its canonical
// text, is answered with invalid_cursor.
func searchCursor(cursor string) (search.Key, error) {
	if cursor == "" {
		return search.Key{}, nil
	}
	key, err := api.DecodeCursor[searchKey](cursor)
	if err != nil {
		return search.Key{}, err
	}
	if !search.IsKind(search.Kind(key.Type)) {
		return search.Key{}, api.NewError(api.CodeInvalidCursor)
	}
	out := search.Key{Rank: key.Rank, Kind: search.Kind(key.Type)}
	if err := cursorUUIDs(cursorUUID{key.ID, &out.ID}); err != nil {
		return search.Key{}, err
	}
	return out, nil
}

// searchError returns the error that the API answers err with: a problem of
// the query at query.q, or an error of the project that it searches
// (projectError), or err itself if it is neither.
func searchError(err error) error {
	var fields input.FieldErrors
	if errors.As(err, &fields) {
		return queryFieldErrors(fields)
	}
	return projectError(err)
}
