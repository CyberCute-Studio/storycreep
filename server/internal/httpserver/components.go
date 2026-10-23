package httpserver

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/component"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
)

type componentTypesInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
	api.PageParams
}

type componentTypesOutput struct {
	Body api.Page[component.ComponentType]
}

type componentTypeInput struct {
	ProjectID       string `path:"project_id" doc:"The project's ID"`
	ComponentTypeID string `path:"component_type_id" doc:"The component type's ID"`
}

type componentTypeOutput struct {
	Body component.ComponentType
}

type conversionInput struct {
	ProjectID       string `path:"project_id" doc:"The project's ID"`
	ComponentTypeID string `path:"component_type_id" doc:"The component type's ID"`
	AttributeID     string `path:"attribute_id" doc:"The ID of the attribute definition, one of the type's"`
	Type            string `query:"type" required:"true" enum:"bool,int,float,string,text,enum" doc:"The type that the definition would be given, which is not its type already"`
	Options         string `query:"options" maxLength:"10099" doc:"The options of an enum, which type then is, one to a line, separated by line feeds (%0A), as Harbour%0APier: 1 to 100 options, each of 1 to 100 characters, not all of them blank, without white space around it, and without control characters, bidirectional controls, line separators, and U+FFFD, the replacement character, among them, no two alike. Another type has none."`
	api.PageParams
}

type conversionOutput struct {
	Body api.Page[component.Unconvertible]
}

type componentsInput struct {
	ProjectID       string `path:"project_id" doc:"The project's ID"`
	ComponentTypeID string `query:"component_type_id" doc:"The ID of a component type: only the components of that type are listed (PRD FR-COMP-02 AC3). Without it, those of every type."`
	Query           string `query:"query" maxLength:"100" doc:"What the components' names or keys hold, compared without regard to case, of at most 100 characters, without control characters: only those are listed (PRD FR-COMP-02 AC3). Without it, every component."`
	api.PageParams
}

type componentsOutput struct {
	Body api.Page[component.Summary]
}

type componentInput struct {
	ProjectID   string `path:"project_id" doc:"The project's ID"`
	ComponentID string `path:"component_id" doc:"The component's ID"`
}

type componentOutput struct {
	Body component.Component
}

// componentChangesDoc says, in the description of an operation that reads
// component types or components, how they are changed.
const componentChangesDoc = " They change through the project's command endpoint, POST " +
	"/api/v1/projects/{project_id}/commands, whose commands of the types component_type.*, attribute.*, and " +
	"component.* create, rename, rekey, retype, move, delete, and restore them, and change their values."

// registerComponents registers the routes that read a project's component
// types and components (PRD FR-COMP-01, FR-COMP-02), which change through the
// command endpoint (registerCommands).
func registerComponents(m *api.Mux, components *component.Service) {
	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "listComponentTypes",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/component-types",
		Summary:     "List a project's component types",
		Description: "Lists the project's component types that are not in its trash, each with its attribute " +
			"definitions that are not, in the order of their IDs, oldest first. A project is created with the types " +
			"Character, Item, Location, Faction, and Custom, without attribute definitions, named in its default " +
			"locale." + componentChangesDoc + projectNotFoundDoc + tokenNotFoundDoc + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Components"},
	}, func(ctx context.Context, in *componentTypesInput) (*componentTypesOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		after, err := cursorID(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := components.Types(ctx, actor, id, after, in.Fetch())
		if err != nil {
			return nil, componentError(err)
		}
		page, err := api.NewPage(in.PageParams, list, func(t component.ComponentType) idKey {
			return uuidKey(ids.ComponentType, t.ID)
		})
		if err != nil {
			return nil, err
		}
		return &componentTypesOutput{Body: page}, nil
	}, api.TokenScope(scope.ProjectRead))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "getComponentType",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/component-types/{component_type_id}",
		Summary:     "Get a component type",
		Description: "Returns a component type of the project, with its attribute definitions that are not in the " +
			"project's trash." + componentChangesDoc + projectNotFoundDoc + tokenNotFoundDoc + " A type that the " +
			"project does not have, or that is in its trash, is answered with 404 not_found." +
			tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Components"},
	}, func(ctx context.Context, in *componentTypeInput) (*componentTypeOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		typeID, err := ids.Decode(ids.ComponentType, in.ComponentTypeID)
		if err != nil {
			return nil, err
		}
		t, err := components.Type(ctx, actor, id, typeID)
		if err != nil {
			return nil, componentError(err)
		}
		return &componentTypeOutput{Body: t}, nil
	}, api.TokenScope(scope.ProjectRead))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "listUnconvertibleValues",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/component-types/{component_type_id}/attributes/{attribute_id}/conversion",
		Summary:     "List the values that a retype would not convert",
		Description: "Lists the components of the type, those in the project's trash included, whose values of the " +
			"attribute definition do not convert to the type that the query names, in the order of their IDs, " +
			"oldest first: those that the command attribute.retype with the conversion convert gives the new " +
			"default, which a client shows before it applies one (PRD FR-COMP-01 AC2). A value converts as the " +
			"expression language's to_int, to_float, and to_string convert it (PRD §5.8.4, §5.8.9), a bool as 0 " +
			"or 1, an int or a float that is 0 or 1 as a bool, a string that is true or false as a bool, and a value " +
			"whose string is one of an enum's options as that option." + projectNotFoundDoc + tokenNotFoundDoc +
			" A type that the project does not have, or a definition that the type does not have, or either in " +
			"the project's trash, is answered with 404 not_found, and a type of the query that is the definition's " +
			"own, or options that are not an enum's, with 422 validation_failed." + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Components"},
	}, func(ctx context.Context, in *conversionInput) (*conversionOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		typeID, err := ids.Decode(ids.ComponentType, in.ComponentTypeID)
		if err != nil {
			return nil, err
		}
		attributeID, err := ids.Decode(ids.AttributeDef, in.AttributeID)
		if err != nil {
			return nil, err
		}
		after, err := cursorID(in.Cursor)
		if err != nil {
			return nil, err
		}
		to := component.Target{Type: in.Type}
		if in.Options != "" {
			to.Options = strings.Split(in.Options, "\n")
		}
		list, err := components.Unconvertible(ctx, actor, id, typeID, attributeID, to, after, in.Fetch())
		if err != nil {
			return nil, componentError(err)
		}
		page, err := api.NewPage(in.PageParams, list, func(u component.Unconvertible) idKey {
			return uuidKey(ids.Component, u.ComponentID)
		})
		if err != nil {
			return nil, err
		}
		return &conversionOutput{Body: page}, nil
	}, api.TokenScope(scope.ProjectRead))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "listComponents",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/components",
		Summary:     "List a project's components",
		Description: "Lists the project's components that are not in its trash, of a type, or whose names or keys " +
			"hold a text, if the query says so, in the order of their IDs, oldest first (PRD FR-COMP-02 AC3). " +
			"Each names its type, its folder, and its place among the folder's children, from which a client " +
			"builds the tree of folders of components (GET /api/v1/projects/{project_id}/folders?kind=component), " +
			"but not its description or its values, which the component's own read returns." + componentChangesDoc +
			projectNotFoundDoc + tokenNotFoundDoc + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Components"},
	}, func(ctx context.Context, in *componentsInput) (*componentsOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		var f component.Filter
		if in.ComponentTypeID != "" {
			if f.TypeID, err = ids.Decode(ids.ComponentType, in.ComponentTypeID); err != nil {
				return nil, err
			}
		}
		f.Query = in.Query
		after, err := cursorID(in.Cursor)
		if err != nil {
			return nil, err
		}
		list, err := components.Components(ctx, actor, id, f, after, in.Fetch())
		if err != nil {
			return nil, componentError(err)
		}
		page, err := api.NewPage(in.PageParams, list, func(c component.Summary) idKey {
			return uuidKey(ids.Component, c.ID)
		})
		if err != nil {
			return nil, err
		}
		return &componentsOutput{Body: page}, nil
	}, api.TokenScope(scope.ProjectRead))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "getComponent",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{project_id}/components/{component_id}",
		Summary:     "Get a component",
		Description: "Returns a component of the project, with its description and the values that it holds of its " +
			"type's attribute definitions that are not in the project's trash." + componentChangesDoc +
			projectNotFoundDoc + tokenNotFoundDoc + " A component that the project does not have, or that is in its " +
			"trash, is answered with 404 not_found." + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Components"},
	}, func(ctx context.Context, in *componentInput) (*componentOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		componentID, err := ids.Decode(ids.Component, in.ComponentID)
		if err != nil {
			return nil, err
		}
		c, err := components.Component(ctx, actor, id, componentID)
		if err != nil {
			return nil, componentError(err)
		}
		return &componentOutput{Body: c}, nil
	}, api.TokenScope(scope.ProjectRead))
}

// componentError returns the error that the API answers err with, an error of
// package component or of the project that it reads the components of
// (projectError), or err itself if it is none of them. A problem of a query's
// parameter is answered at the query.
func componentError(err error) error {
	var fields input.FieldErrors
	switch {
	case errors.Is(err, component.ErrNotFound), errors.Is(err, component.ErrTypeNotFound),
		errors.Is(err, component.ErrAttributeNotFound):
		return api.NewError(api.CodeNotFound)
	case errors.As(err, &fields):
		problems := make([]api.Problem, len(fields))
		for i, f := range fields {
			problems[i] = api.Problem{Location: "query." + f.Field, Message: f.Message}
		}
		return &api.Error{Code: api.CodeValidationFailed, Details: map[string]any{"errors": problems}}
	}
	return projectError(err)
}
