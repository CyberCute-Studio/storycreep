package httpserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/clipboard"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
)

type clipboardCopyInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
	Body      struct {
		NodeIDs []string `json:"node_ids" minItems:"1" maxItems:"1000" nullable:"false" doc:"The IDs of the nodes to copy, at most 1000, each once, of one board of the project, none of them in its trash, itself or with its board"`
	}
}

type clipboardCopyOutput struct {
	Body clipboard.Clipboard
}

type clipboardResolveInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
	Body      struct {
		BoardID   string              `json:"board_id" doc:"The ID of the board that the clipboard would be pasted onto, a board of the project that is not in its trash"`
		Clipboard clipboard.Clipboard `json:"clipboard" doc:"The clipboard (PRD FR-EDIT-07 AC3), as POST /api/v1/projects/{project_id}/clipboard/copy answers with it, of this project or of another of the workspace's"`
	}
}

type clipboardResolveOutput struct {
	Body clipboard.ClipboardReport
}

// registerClipboard registers the routes that copy a project's nodes as a
// clipboard and report what of a clipboard a paste onto a board would
// resolve to nothing (PRD FR-EDIT-07), which the command clipboard.paste
// pastes. Each is a POST, as the nodes and the clipboard that it reads do not
// fit in a URL, but reads, and changes nothing, so a role that reads the
// project's content may use it.
func registerClipboard(m *api.Mux, clipboards *clipboard.Service) {
	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "copyNodes",
		Method:      http.MethodPost,
		Path:        "/api/v1/projects/{project_id}/clipboard/copy",
		Summary:     "Copy nodes as a clipboard",
		Description: "Answers with the clipboard of the nodes, a documented JSON structure of a version of its own " +
			"(PRD FR-EDIT-07 AC3, schema/clipboard): the nodes, with a branch's outputs, the connections between " +
			"them, in the order of their ports and of each port's choices, and a declaration of each variable, " +
			"component, and attribute definition, not in the project's trash, that their texts, expressions, and " +
			"speakers name, by which the command clipboard.paste resolves them where it pastes the clipboard, " +
			"onto a board of this project or of another of the workspace's. It reads, and changes nothing." +
			projectNotFoundDoc + tokenNotFoundDoc + " A node that the project does not have, or has in its " +
			"trash, or of another board than the others, is answered with 422 validation_failed at its place in " +
			"body.node_ids, and nodes whose clipboard would pass a clipboard's bounds, which a paste could not " +
			"take, as more than 10000 connections between them, or more than 4 MiB (4194304 bytes) of JSON, " +
			"which a paste's batch must fit in a request's body with, at body.node_ids, or at its place in it for " +
			"a branch of more than 1000 outputs, once the project is found." + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Clipboard"},
	}, func(ctx context.Context, in *clipboardCopyInput) (*clipboardCopyOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		c, err := clipboards.Copy(ctx, actor, id, in.Body.NodeIDs)
		if err != nil {
			return nil, clipboardError(err)
		}
		return &clipboardCopyOutput{Body: c}, nil
	}, api.TokenScope(scope.ProjectRead))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "resolveClipboard",
		Method:      http.MethodPost,
		Path:        "/api/v1/projects/{project_id}/clipboard/resolve",
		Summary:     "Report what a paste would not resolve",
		Description: "Answers with what of the clipboard a paste onto the board, by the command clipboard.paste, " +
			"would resolve to nothing, as the project is now (PRD FR-EDIT-07 AC1): the variables and the " +
			"components that it declares, as it declares them, with what creating one in their place takes, the " +
			"attribute definitions that the components that it resolves to lack, the speakers that it does not " +
			"declare that the project lacks, in its trash or not, the nodes outside it that its jumpers target or " +
			"its expressions name that the project lacks, and the cover images that are not the project's. A " +
			"client may create the variables and the components in the batch of the paste, before it, so that " +
			"the paste resolves to them. A paste resolves a variable to the same variable, where the board " +
			"reaches it, global or of the board, and else to the one of its key and scope, a global one or one " +
			"of the board; a component to the same component, and else to the one of its key; and an attribute " +
			"definition of each component to the same one, of the type of the component that its component " +
			"resolves to, and else to the one of its key of that type; none of them in the project's trash. It " +
			"reads, and changes nothing." + projectNotFoundDoc + tokenNotFoundDoc + " A board that the project " +
			"does not have, or has in its trash, is answered with 422 validation_failed at body.board_id, and a " +
			"clipboard that the server does not read, as one of a newer version, at its field of " +
			"body.clipboard, once the project is found." + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Clipboard"},
	}, func(ctx context.Context, in *clipboardResolveInput) (*clipboardResolveOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		r, err := clipboards.Resolve(ctx, actor, id, in.Body.BoardID, in.Body.Clipboard)
		if err != nil {
			return nil, clipboardError(err)
		}
		return &clipboardResolveOutput{Body: r}, nil
	}, api.TokenScope(scope.ProjectRead))
}

// clipboardError returns the error that the API answers err with: a problem
// of the request's body at its field, or an error of the project that it
// reads (projectError), or err itself if it is neither.
func clipboardError(err error) error {
	var fields input.FieldErrors
	if errors.As(err, &fields) {
		return fieldErrors(fields)
	}
	return projectError(err)
}
