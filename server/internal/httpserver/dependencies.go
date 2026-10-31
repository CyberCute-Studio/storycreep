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

type dependencyGraphInput struct {
	ProjectID   string `path:"project_id" doc:"The project's ID"`
	Kind        string `query:"kind" enum:"variable,attribute,jumper,visit" doc:"A kind of edge: only the edges of that kind are kept. Without it, those of every kind."`
	BoardID     string `query:"board_id" doc:"The ID of a board: only the edges that leave it or enter it are kept, and every board is still listed"`
	VariableID  string `query:"variable_id" doc:"The ID of a variable: only its contributions are kept, and so only the edges of the kind variable that it contributes to"`
	ComponentID string `query:"component_id" doc:"The ID of a component: only the contributions of its attributes are kept, and so only the edges of the kind attribute that they contribute to"`
	AttributeID string `query:"attribute_id" doc:"The ID of an attribute's definition: only the contributions of the attributes of that definition, of any component, are kept, and so only the edges of the kind attribute that they contribute to"`
}

type dependencyGraphOutput struct {
	Body *dependency.Graph
}

type contributionsInput struct {
	ProjectID   string `path:"project_id" doc:"The project's ID"`
	FromBoardID string `path:"from_board_id" doc:"The ID of the board that the edge leaves, which the other depends on"`
	ToBoardID   string `path:"to_board_id" doc:"The ID of the board that the edge enters"`
	Kind        string `query:"kind" required:"true" enum:"variable,attribute,jumper,visit" doc:"The edge's kind, whose contributions the list holds"`
	VariableID  string `query:"variable_id" doc:"The ID of a variable: only its contributions are listed, which only an edge of the kind variable has"`
	ComponentID string `query:"component_id" doc:"The ID of a component: only the contributions of its attributes are listed, which only an edge of the kind attribute has"`
	AttributeID string `query:"attribute_id" doc:"The ID of an attribute's definition: only the contributions of the attributes of that definition, of any component, are listed, which only an edge of the kind attribute has"`
	api.PageParams
}

type contributionsOutput struct {
	Body api.Page[dependency.Contribution]
}

// contributionKey is the key of a list of contributions that its pages'
// cursors hold (dependency.ContributionKey), each UUID in its text form, the
// zero UUID where a contribution has none.
type contributionKey struct {
	Target    string `json:"target"`
	Attribute string `json:"attribute"`
	FromNode  string `json:"from_node"`
	FromSite  string `json:"from_site"`
	ToNode    string `json:"to_node"`
	ToSite    string `json:"to_site"`
}

// graphDoc says, in the descriptions of the graph's operations, what an
// edge is, and which content it is of.
const graphDoc = " An edge of the kind variable or attribute leaves a board that writes a variable, or a component's " +
	"attribute, for another board that reads it; one of the kind jumper leaves a board that has a jumper for " +
	"another that its target is on; and one of the kind visit leaves a board for another one of whose " +
	"expressions reads whether a node of the first was visited, by visited() or visits(), as playing the first " +
	"changes what the other allows. A component's attribute is the pair of the component and its definition, " +
	"so that a board that writes elara.trust and another that reads mira.trust have no edge between them. The " +
	"edges are those of the sites, the nodes, and the boards that are not in the project's trash, and none joins " +
	"a board to itself, as a dependency within a board is none between boards. Each filter keeps the " +
	"contributions that it names, and so the edges that have any, and filters that name nothing in common keep " +
	"nothing; a filter by an ID that the project does not have keeps nothing either."

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

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "getDependencyGraph",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/dependencies",
		Summary:     "Get the dependencies between a project's boards",
		Description: "Returns the graph of the dependencies between the project's boards (PRD FR-DEP-02), as of one " +
			"moment: its boards that are not in its trash, each with how many other boards it depends on and how " +
			"many depend on it, which its badge shows, whatever the kinds of the edges between them, and its " +
			"edges, each from the board that another depends on to that other board, with its kind and how many " +
			"contributions it has, which GET /api/v1/projects/{project_id}/dependencies/{from_board_id}/" +
			"{to_board_id} lists." + graphDoc + " The badges count the edges that the filters keep, and each edge " +
			"the contributions that they keep, and every board is listed whatever they keep. The graph is one " +
			"document, which the server computes anew for each request, rather than a list of pages, so that its " +
			"boards, its edges, and their counts are all of one state of the draft, which its seq names." +
			projectNotFoundDoc + tokenNotFoundDoc + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Dependencies"},
	}, func(ctx context.Context, in *dependencyGraphInput) (*dependencyGraphOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		f := dependency.Filter{Kind: dependency.EdgeKind(in.Kind)}
		if err := graphFilter(&f, in.VariableID, in.ComponentID, in.AttributeID); err != nil {
			return nil, err
		}
		if f.Board, err = optionalID(ids.Board, in.BoardID); err != nil {
			return nil, err
		}
		g, err := dependencies.Graph(ctx, actor, id, f)
		if err != nil {
			return nil, dependencyError(err)
		}
		return &dependencyGraphOutput{Body: g}, nil
	}, api.TokenScope(scope.ProjectRead))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "listDependencyContributions",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/dependencies/{from_board_id}/{to_board_id}",
		Summary:     "List the contributions to an edge between two boards",
		Description: "Lists the contributions to the edge of the kind from the board from_board_id to the board " +
			"to_board_id, which the filters keep, as the graph of GET /api/v1/projects/{project_id}/dependencies " +
			"counts them (PRD FR-DEP-02 AC2): of variable and attribute, the triples of a variable, or a " +
			"component's attribute, a site of the board that the edge leaves that writes it, and a site of the " +
			"board that it enters that reads it, each such pair of sites once; of jumper, the jumpers of the one " +
			"whose targets are nodes of the other, each with its target; and of visit, the pairs of a node of the " +
			"one and an expression of the other that reads whether it was visited. Each end of a contribution " +
			"names its site, or the node itself, and its node, by which a client opens it, as a usage does: a " +
			"node's script or text is of the node, a connection's condition, script, or label of the node that " +
			"the connection leaves, and an output's condition of its branch. The contributions are in the order " +
			"of their variables, or their components and their attributes' definitions, or the nodes visited, " +
			"and then of the nodes and the sites of their ends, the one on the board that the edge leaves first, " +
			"and a jumper's in the order of the jumpers." + graphDoc + " The list of a board to itself is empty." +
			projectNotFoundDoc + tokenNotFoundDoc + " A board that the project does not have, or that is in its " +
			"trash, is answered with 404 not_found." + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Dependencies"},
	}, func(ctx context.Context, in *contributionsInput) (*contributionsOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		from, err := ids.Decode(ids.Board, in.FromBoardID)
		if err != nil {
			return nil, err
		}
		to, err := ids.Decode(ids.Board, in.ToBoardID)
		if err != nil {
			return nil, err
		}
		f := dependency.Filter{Kind: dependency.EdgeKind(in.Kind)}
		if err := graphFilter(&f, in.VariableID, in.ComponentID, in.AttributeID); err != nil {
			return nil, err
		}
		after, err := contributionCursor(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := dependencies.Contributions(ctx, actor, id, from, to, f, after, in.Fetch())
		if err != nil {
			return nil, dependencyError(err)
		}
		page, err := api.NewPage(in.PageParams, list, func(c dependency.Contribution) contributionKey {
			k := c.Key()
			return contributionKey{Target: k.Target.String(), Attribute: k.Attribute.String(),
				FromNode: k.FromNode.String(), FromSite: k.FromSite.String(), ToNode: k.ToNode.String(),
				ToSite: k.ToSite.String()}
		})
		if err != nil {
			return nil, err
		}
		return &contributionsOutput{Body: page}, nil
	}, api.TokenScope(scope.ProjectRead))
}

// graphFilter sets the filters of f by a variable, a component, and an
// attribute's definition from their IDs, each of which may be empty, for a
// filter that keeps everything.
func graphFilter(f *dependency.Filter, variable, component, attribute string) error {
	var err error
	if f.Variable, err = optionalID(ids.Variable, variable); err != nil {
		return err
	}
	if f.Component, err = optionalID(ids.Component, component); err != nil {
		return err
	}
	f.Attribute, err = optionalID(ids.AttributeDef, attribute)
	return err
}

// optionalID returns the UUID of s, an ID of the kind, or zero for the empty
// s of a filter that a request leaves out.
func optionalID(k ids.Kind, s string) (ids.UUID, error) {
	if s == "" {
		return ids.UUID{}, nil
	}
	return ids.Decode(k, s)
}

// contributionCursor returns the key that cursor, a next_cursor of a list of
// contributions, holds, or the zero key, before every contribution, for the
// empty cursor of the first page. A cursor that holds no key, or not in the
// form that the server writes, is answered with invalid_cursor.
func contributionCursor(cursor string) (dependency.ContributionKey, error) {
	if cursor == "" {
		return dependency.ContributionKey{}, nil
	}
	key, err := api.DecodeCursor[contributionKey](cursor)
	if err != nil {
		return dependency.ContributionKey{}, err
	}
	var out dependency.ContributionKey
	if err := cursorUUIDs(cursorUUID{key.Target, &out.Target}, cursorUUID{key.Attribute, &out.Attribute},
		cursorUUID{key.FromNode, &out.FromNode}, cursorUUID{key.FromSite, &out.FromSite},
		cursorUUID{key.ToNode, &out.ToNode}, cursorUUID{key.ToSite, &out.ToSite}); err != nil {
		return dependency.ContributionKey{}, err
	}
	return out, nil
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
	if err := cursorUUIDs(cursorUUID{key.Board, &out.Board}, cursorUUID{key.Node, &out.Node},
		cursorUUID{key.Site, &out.Site}, cursorUUID{key.Attribute, &out.Attribute}); err != nil {
		return dependency.Key{}, err
	}
	return out, nil
}

// cursorUUID is a UUID of the key that a cursor holds, in its text form, and
// where to set it.
type cursorUUID struct {
	text string
	id   *ids.UUID
}

// cursorUUIDs sets each UUID of a cursor's key from its text form, or returns
// the error of invalid_cursor for one that is not a UUID in the form that the
// server writes, its canonical text.
func cursorUUIDs(uuids ...cursorUUID) error {
	for _, u := range uuids {
		id, err := ids.ParseUUID(u.text)
		if err != nil || id.String() != u.text {
			return api.NewError(api.CodeInvalidCursor)
		}
		*u.id = id
	}
	return nil
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
