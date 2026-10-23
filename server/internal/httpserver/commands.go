package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
	"golang.org/x/text/language"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/board"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/ident"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
	"github.com/CyberCute-Studio/storycreep/server/internal/slug"
	"github.com/CyberCute-Studio/storycreep/server/internal/variable"
)

// conflictBody is an entity of the details of an answer of
// revision_conflict, whose revision is not the one that a command of the
// batch expected.
type conflictBody struct {
	// Command is the command's index in the batch, and CommandID and Type
	// are its.
	Command     int           `json:"command"`
	CommandID   string        `json:"command_id"`
	Type        commands.Type `json:"type"`
	ID          string        `json:"id"`
	ExpectedRev int64         `json:"expected_rev"`
	Exists      bool          `json:"exists"`
	// CurrentRev is the entity's revision now, if it still exists, whose
	// state the details' states hold.
	CurrentRev *int64 `json:"current_rev,omitempty"`
}

// revisionConflictError returns the error that the API answers err with if
// err is a *commands.RevisionConflictError, or err itself otherwise: 409
// revision_conflict, whose details list as conflicts each entity of a
// command of the batch whose revision is not the one that the command
// expected, with the command, by its index in the batch, its command_id, and
// its type, the revision that it expected, and the entity's current revision
// if it still exists, or exists false if it does not, and as states the
// state of each of those entities that still exists, by its ID, once,
// however many commands expected a revision of it (PRD FR-API-03 AC3,
// FR-OFF-04 AC5), so that a batch of many commands that expect one entity's
// revision is not answered with the entity's state once for each. The error
// mapper of a route that applies commands calls it.
func revisionConflictError(err error) error {
	var conflict *commands.RevisionConflictError
	if !errors.As(err, &conflict) {
		return err
	}
	conflicts := make([]conflictBody, len(conflict.Conflicts))
	states := map[string]json.RawMessage{}
	for i, c := range conflict.Conflicts {
		id := ids.Encode(c.Kind, c.ID)
		conflicts[i] = conflictBody{Command: c.Command, CommandID: c.CommandID.String(), Type: c.Type, ID: id,
			ExpectedRev: c.Expected, Exists: c.Exists}
		if c.Exists {
			current := c.Current
			conflicts[i].CurrentRev = &current
			// Every conflict of an entity has its one state.
			states[id] = c.State
		}
	}
	return &api.Error{Code: api.CodeRevisionConflict, Details: map[string]any{"conflicts": conflicts,
		"states": states}}
}

// The most that a request of the command endpoint may hold (PRD FR-API-03),
// which the maintainer chose: each of its batches is a transaction of its
// own, which holds the project's lock, so they bound how long one request
// holds a worker and the project. The pipeline's other callers, such as the
// importer, are not bound by them.
const (
	maxBatches       = 100
	maxBatchCommands = 1000
)

// commandsBody is the body of POST /api/v1/projects/{project_id}/commands
// as huma decodes it, once it has validated it with commandsForm: one batch
// of commands, each command as its JSON, or a list of batches, each batch as
// its JSON, which the handler decodes (batchBody), and the pipeline reads
// each command of (commands.Pipeline.Parse), so that a batch, or a command,
// that cannot be read refuses its own batch alone. Its schema in the OpenAPI
// document is commandsSchemas', which describes each type of command.
type commandsBody struct {
	Commands []json.RawMessage `json:"commands"`
	Batches  []json.RawMessage `json:"batches"`
}

// commandsForm returns the schema that huma validates the command endpoint's
// body with, which holds it to the keys of the two forms, written exactly
// (PRD FR-API-01 AC2), and leaves its batches and their commands to the
// handler and the pipeline. huma refuses a body that is not JSON before it
// validates it, with 400 invalid_request, or 415 unsupported_media_type, as
// on every other route.
func commandsForm() *huma.Schema {
	// An empty schema takes any value.
	return &huma.Schema{Type: "object", AdditionalProperties: false, Properties: map[string]*huma.Schema{
		"commands": {Type: "array", Items: &huma.Schema{}}, "batches": {Type: "array", Items: &huma.Schema{}}}}
}

// batchBody is a batch of a list of batches, which the handler decodes with
// its keys written exactly as its schema names them (commands.DecodeObject),
// as huma's validation has the body's.
type batchBody struct {
	Commands []json.RawMessage `json:"commands"`
}

type commandsInput struct {
	SessionCSRFInput
	ProjectID string `path:"project_id" doc:"The project's ID"`
	Body      commandsBody
}

// entityRevisionBody is an entity's revision.
type entityRevisionBody struct {
	ID  string `json:"id" doc:"The entity's ID, as prj_…"`
	Rev int64  `json:"rev" doc:"The entity's revision"`
}

// commandResultBody is what a batch of commands did.
type commandResultBody struct {
	Status    string               `json:"status" enum:"applied,already_applied" doc:"applied for a batch that applied, or that was sent before, while its idempotency record is kept, for at least 24 hours, which is answered as it was then; already_applied for a batch whose commands were all applied before, once that record is pruned, or by batches grouped otherwise"`
	Seq       int64                `json:"seq" doc:"The project's sequence number after the batch: the seq of its operation in the project's operation log. That of a batch already applied is the latest seq of the batches that applied its commands."`
	Revisions []entityRevisionBody `json:"revisions" doc:"The entities that the batch changed, each with its revision after the batch, in the order of their first change. Those of a batch already applied are the entities that the batches that applied its commands changed, each with its current revision, without those that no longer exist."`
}

// commandOutcomeBody is what a batch of a list of batches did, or why it
// was refused.
type commandOutcomeBody struct {
	Status    string                `json:"status" enum:"applied,already_applied,rejected" doc:"applied or already_applied, as for a request of one batch, or rejected for a batch that was refused, and applied nothing"`
	Seq       int64                 `json:"seq,omitempty" doc:"The project's sequence number after the batch, as for a request of one batch; not for a batch that was rejected"`
	Revisions *[]entityRevisionBody `json:"revisions,omitempty" doc:"The entities that the batch changed, as for a request of one batch; not for a batch that was rejected"`
	Error     *api.ErrorBody        `json:"error,omitempty" doc:"Why the batch was rejected: the error that a request of the batch alone would be answered with, as the error envelope holds it"`
}

// commandResultsBody is what the batches of a list of batches did.
type commandResultsBody struct {
	Results []commandOutcomeBody `json:"results" doc:"A result for each batch of the request, in the order of the batches"`
}

type commandsOutput struct {
	// ContentLanguage and Vary are set for an answer that holds the message
	// of a batch's error (api.ErrorBodyOf).
	ContentLanguage string   `header:"Content-Language" doc:"The language of the messages of the batches' errors, for an answer of several batches with one that was rejected"`
	Vary            []string `header:"Vary" doc:"Accept-Language, for an answer whose messages are in the language that it chose"`
	// Body is a commandResultBody, or a commandResultsBody for a request of
	// a list of batches.
	Body any
}

// newCommandResult returns the body of res.
func newCommandResult(res commands.Result) commandResultBody {
	body := commandResultBody{Status: string(res.Status), Seq: res.Seq,
		Revisions: make([]entityRevisionBody, len(res.Revisions))}
	for i, r := range res.Revisions {
		body.Revisions[i] = entityRevisionBody{ID: ids.Encode(r.Kind, r.ID), Rev: r.Rev}
	}
	return body
}

// commandsSchemas adds to doc the schemas of the body of the command endpoint
// and of its answer of 200, and returns them: one batch of commands, of any
// type that pipeline applies (commands.Pipeline.Document), or a list of
// batches; and what one batch did, or what each batch of a list did.
func commandsSchemas(doc huma.Registry, pipeline *commands.Pipeline) (body, answer *huma.Schema) {
	const prefix = commands.SchemaPrefix
	schemas := doc.Map()
	command := pipeline.Document(doc)
	schemas["CommandBody"] = command
	one := 1
	batch := &huma.Schema{
		Type:                 "object",
		Description:          "A batch of commands, which applies whole, in one transaction, or not at all.",
		AdditionalProperties: false,
		Required:             []string{"commands"},
		Properties: map[string]*huma.Schema{
			"commands": {Type: "array", MinItems: &one, MaxItems: new(maxBatchCommands),
				Items: &huma.Schema{Ref: prefix + "CommandBody"},
				Description: fmt.Sprintf("The batch's commands, in the order to apply them: at least one, and at "+
					"most %d. Every command_id of the batch is its own.", maxBatchCommands)},
		},
	}
	schemas["CommandBatchBody"] = batch
	batches := &huma.Schema{
		Type: "object",
		Description: "A list of batches, each of which applies whole, in its own transaction, or not at all, in the " +
			"order of the list, whatever the batches before it did.",
		AdditionalProperties: false,
		Required:             []string{"batches"},
		Properties: map[string]*huma.Schema{
			"batches": {Type: "array", MinItems: &one, MaxItems: new(maxBatches),
				Items:       &huma.Schema{Ref: prefix + "CommandBatchBody"},
				Description: fmt.Sprintf("The batches, at least one, and at most %d.", maxBatches)},
		},
	}
	schemas["CommandBatchesBody"] = batches
	// Document has computed command's messages.
	for _, s := range []*huma.Schema{batch, batches} {
		s.PrecomputeMessages()
	}
	body = &huma.Schema{OneOf: []*huma.Schema{{Ref: prefix + "CommandBatchBody"}, {Ref: prefix + "CommandBatchesBody"}}}
	answer = &huma.Schema{OneOf: []*huma.Schema{
		doc.Schema(reflect.TypeFor[commandResultBody](), true, ""),
		doc.Schema(reflect.TypeFor[commandResultsBody](), true, ""),
	}}
	return body, answer
}

// registerCommands registers the command endpoint (PRD FR-API-03, §10.5),
// which applies batches of commands with pipeline, whose registry the
// document describes them from.
func registerCommands(m *api.Mux, pipeline *commands.Pipeline) {
	const path = "/api/v1/projects/{project_id}/commands"
	body, answer := commandsSchemas(m.OpenAPI().Components.Schemas, pipeline)
	api.Register(m, api.ApplyCommands, huma.Operation{
		OperationID: "applyCommands",
		Method:      http.MethodPost,
		Path:        path,
		Summary:     "Apply batches of commands to a project",
		Description: "Applies a batch of commands to a project, every change of whose content is a command of a " +
			"type that the body's schema describes, with its payload (PRD FR-API-03). The body is a batch, " +
			"{\"commands\": [...]}, or a list of batches, {\"batches\": [{\"commands\": [...]}, ...]}. A batch " +
			"applies whole, in one transaction, or not at all, and is one step of undo. Each command is authorised " +
			"by the signed-in user's role as its type's operation of the permission matrix needs (PRD §5.6), and " +
			"a role that does not allow one refuses its batch with 403 insufficient_role, or owner_required. A " +
			"batch with a command that only a session may apply, as deleting or restoring the project, which an " +
			"API token sends is refused with 403 session_required, whatever the token's scopes (PRD FR-AUTH-04)." +
			projectNotFoundDoc + tokenNotFoundDoc + " Every expected_rev of a batch is checked against the " +
			"entities as they are before the batch, and a stale one refuses it with 409 revision_conflict, whose " +
			"details hold the entities' current revisions and states. An archived project refuses a batch with " +
			"409 project_archived, unless each of its commands applies to an archived project, and a batch that " +
			"would take the workspace past a limit of its plan is refused with 409 limit_exceeded. A batch that " +
			"its schema, or its commands' own checks, refuse is answered with 422 validation_failed, whose " +
			"details locate each problem, as body.commands[2].payload.name. A command may also refuse its batch " +
			"for what the project's state makes of its payload, at the payload's field, as " +
			"body.commands[0].payload.folder_id: with 422 validation_failed for a value that the state does not " +
			"take, as the ID of a folder that the project does not have, or that is deleted, or with the code that " +
			"the field's description names, as 422 slug_invalid for a slug that is not one, 409 slug_taken for a " +
			"slug that another board has, 409 folder_not_empty for a folder to delete that holds folders, boards, " +
			"or variables, 422 key_invalid for a variable's key that is not an identifier, or 409 key_taken for a " +
			"key that another variable of its scope has." +
			"\n\nA command_id is the client's, unique within the project, and makes the batch idempotent. A batch " +
			"sent again, as when its answer was lost, is answered as it was the first time and applies nothing " +
			"again, before the project's state and the workspace's limits are checked: the same batch, with the " +
			"same commands in the same order, for at least 24 hours, whether it applied or was refused with " +
			"revision_conflict, project_archived, or limit_exceeded; after that, a batch whose commands applied " +
			"is answered with the status already_applied, the seq that applied them, and the current revisions " +
			"of the entities that they changed, and one that was refused is checked anew. So is any other batch " +
			"whose commands were all applied before, however they are grouped. A batch with commands that were " +
			"applied and commands that were not is refused with 422 idempotency_mismatch, which lists the " +
			"applied ones, and applies nothing. A command's payload is not compared with the one that its ID was " +
			"applied with. Any other refusal, as of the role, is checked anew when the batch is sent again." +
			fmt.Sprintf("\n\nA request of a list of batches applies each, in its own transaction, in the order of "+
				"the list, and is answered with 200 and a result of each, applied or rejected with its error, so "+
				"that a rejected batch, of any of the refusals above, never keeps the later ones from applying. "+
				"A body that is neither form, or has more than %d batches, is answered with 422 "+
				"validation_failed, and a batch of more than %d commands is rejected so. A body that is not one "+
				"strict JSON value, as one with a key twice in an object, anywhere in it, is answered with 400 "+
				"invalid_request, as on every route. The body is at most "+
				"%d MB, as every route's, and a larger one is answered with 413 request_too_large, so that a "+
				"request of many large batches may need to be split.", maxBatches, maxBatchCommands,
				api.DefaultMaxBodyBytes>>20) +
			tokenChangeDoc(scope.ProjectWrite),
		Tags: []string{"Projects"},
		// huma validates the body with the schema that the operation is
		// registered with, commandsForm, which it keeps, and the document
		// describes the body with body, which replaces it below: the pipeline
		// checks each batch's commands, so that one that it cannot apply
		// refuses its own batch alone, with the location of each problem,
		// where huma, validating with body, would refuse the whole request,
		// and a command whose type's schema refuses it as one that matches
		// none of the types.
		RequestBody: &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{
			"application/json": {Schema: commandsForm()}}},
		Responses: map[string]*huma.Response{"200": {Description: "OK", Content: map[string]*huma.MediaType{
			"application/json": {Schema: answer}}}},
	}, func(ctx context.Context, in *commandsInput) (*commandsOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		switch {
		case in.Body.Commands != nil && in.Body.Batches != nil:
			return nil, bodyProblem("body", "has both commands and batches; send one batch as commands, or a "+
				"list of batches as batches")
		case in.Body.Batches == nil && in.Body.Commands == nil:
			return nil, bodyProblem("body", "has neither commands nor batches")
		case in.Body.Commands != nil:
			res, err := applyBatch(ctx, pipeline, actor, id, in.Body.Commands, "body.commands")
			if err != nil {
				return nil, err
			}
			return &commandsOutput{Body: newCommandResult(res)}, nil
		case len(in.Body.Batches) == 0:
			return nil, bodyProblem("body.batches", "has no batches")
		case len(in.Body.Batches) > maxBatches:
			return nil, bodyProblem("body.batches", fmt.Sprintf("has more than %d batches", maxBatches))
		}
		out := &commandsOutput{}
		results := commandResultsBody{Results: make([]commandOutcomeBody, len(in.Body.Batches))}
		for i, raw := range in.Body.Batches {
			res, err := applyListed(ctx, pipeline, actor, id, raw, fmt.Sprintf("body.batches[%d]", i))
			if err != nil {
				if ctx.Err() != nil {
					// The client went away, and reads no answer; the batches
					// that it did not learn of it sends again.
					return nil, ctx.Err()
				}
				var locale language.Tag
				outcome := commandOutcomeBody{Status: "rejected", Error: &api.ErrorBody{}}
				*outcome.Error, locale = api.ErrorBodyOf(ctx, err)
				out.ContentLanguage, out.Vary = locale.String(), []string{"Accept-Language"}
				results.Results[i] = outcome
				continue
			}
			body := newCommandResult(res)
			results.Results[i] = commandOutcomeBody{Status: body.Status, Seq: body.Seq, Revisions: &body.Revisions}
		}
		out.Body = results
		return out, nil
	}, api.TokenScope(scope.ProjectWrite))
	// huma took commandsForm to validate with when the operation was
	// registered, so the document's schema changes the document alone.
	m.OpenAPI().Paths[path].Post.RequestBody.Content["application/json"].Schema = body
}

// applyBatch applies the batch of the commands raw, as a client sent them at
// the location at of the request, as body.commands, to the project
// projectID, as the actor asks, and returns what it did, or the error that the
// API answers it with (commandError).
func applyBatch(ctx context.Context, pipeline *commands.Pipeline, actor commands.Actor, projectID ids.UUID,
	raw []json.RawMessage, at string) (commands.Result, error) {
	cmds, err := pipeline.Parse(projectID, raw, maxBatchCommands)
	if err != nil {
		return commands.Result{}, commandError(err, at)
	}
	res, err := pipeline.Apply(ctx, actor, projectID, cmds)
	if err != nil {
		return commands.Result{}, commandError(err, at)
	}
	return res, nil
}

// applyListed applies the batch raw, a batch of a list of batches, at the
// location at of the request, as body.batches[2], as applyBatch does, once it
// has decoded it, or returns validation_failed, at its location, if it
// cannot, so that it refuses its own batch alone.
func applyListed(ctx context.Context, pipeline *commands.Pipeline, actor commands.Actor, projectID ids.UUID,
	raw json.RawMessage, at string) (commands.Result, error) {
	var b batchBody
	if err := commands.DecodeObject(raw, &b); err != nil {
		d := err.(*commands.DecodeError)
		if d.Field != "" {
			at += "." + d.Field
		}
		return commands.Result{}, bodyProblem(at, d.Message)
	}
	return applyBatch(ctx, pipeline, actor, projectID, b.Commands, at+".commands")
}

// bodyProblem returns validation_failed, with the one problem msg, at the
// location at of the request's body, as body.batches.
func bodyProblem(at, msg string) error {
	return &api.Error{Code: api.CodeValidationFailed, Details: map[string]any{"errors": []api.Problem{
		{Location: at, Message: msg}}}}
}

// commandError returns the error that the API answers err with, an error of
// the pipeline that applied a batch of commands sent at the location at of
// the request, as body.commands, or err itself if it is none of them: the
// problems of a batch that is not valid, each at its command's location, as
// body.commands[2].payload.name, a command's refusal of the batch at the
// location of its payload's field (refusalError), 422 idempotency_mismatch,
// whose details list as applied the command_ids of the batch that were
// applied, and the pipeline's other refusals as every route answers them
// (pipelineError).
func commandError(err error, at string) error {
	var invalid *commands.InvalidError
	var mismatch *commands.IdempotencyMismatchError
	var refused *commands.RefusedError
	switch {
	case errors.As(err, &refused):
		loc := at + fmt.Sprintf("[%d].payload", refused.Command)
		if refused.Field != "" {
			loc += "." + refused.Field
		}
		return refusalError(refused, loc)
	case errors.As(err, &invalid):
		problems := make([]api.Problem, len(invalid.Problems))
		for i, p := range invalid.Problems {
			loc := at
			if p.Command >= 0 {
				loc += fmt.Sprintf("[%d]", p.Command)
				if p.Field != "" {
					loc += "." + p.Field
				}
			}
			problems[i] = api.Problem{Location: loc, Message: p.Message}
		}
		return &api.Error{Code: api.CodeValidationFailed, Details: map[string]any{"errors": problems}}
	case errors.As(err, &mismatch):
		applied := make([]string, len(mismatch.Applied))
		for i, id := range mismatch.Applied {
			applied[i] = id.String()
		}
		return &api.Error{Code: api.CodeIdempotencyMismatch, Details: map[string]any{"applied": applied}}
	}
	return pipelineError(err)
}

// pipelineError returns the error that the API answers err with if it is one
// of the command pipeline's refusals that every route that applies commands
// answers alike, or err itself otherwise: of a project that is not found, of
// the actor's role, or of their API token, of an archived project, of a limit
// of the workspace's plan, of stale revisions, and of a command, for a field
// of its payload, which a route that dispatches commands names as the field
// of its body that it came from, as body.slug (refusalError). The error
// mapper of each such route calls it once it has mapped what it answers its
// own way, as the problems of a batch that is not valid, so that a refusal
// that the pipeline adds is answered alike on every route.
func pipelineError(err error) error {
	var insufficient *scope.InsufficientError
	var refused *commands.RefusedError
	switch {
	case errors.As(err, &refused):
		loc := "body"
		if refused.Field != "" {
			loc += "." + refused.Field
		}
		return refusalError(refused, loc)
	case errors.Is(err, commands.ErrNotFound):
		return api.NewError(api.CodeNotFound)
	case errors.Is(err, commands.ErrInsufficientRole):
		return api.NewError(api.CodeInsufficientRole)
	case errors.Is(err, commands.ErrOwnerRequired):
		return api.NewError(api.CodeOwnerRequired)
	case errors.Is(err, commands.ErrSessionRequired):
		return api.NewError(api.CodeSessionRequired)
	case errors.As(err, &insufficient):
		// A route that takes a token checks the scope that it needs before
		// the pipeline does, which refuses only a batch with a command that
		// needs another.
		return insufficientScope(insufficient.Scope)
	case errors.Is(err, commands.ErrProjectArchived):
		return api.NewError(api.CodeProjectArchived)
	}
	return revisionConflictError(limitExceededError(err))
}

// refusalError returns the error that the API answers a command's refusal of
// its batch with (commands.Refuse), for the field of the request at the
// location loc, as body.commands[2].payload.slug: 422 slug_invalid, with the
// rule that the slug breaks as details.reason, for a slug that is not one,
// 409 slug_taken for a slug that another board has, 409 folder_not_empty for
// a folder to delete that holds something, 422 key_invalid, with the rule
// that the key breaks as details.reason, for a variable's key that is not an
// identifier, 409 key_taken for a key that another variable of its scope
// has, and 422 validation_failed for any other value that the project's
// state does not take, as an ID of a board that the project does not have,
// each with the field's problem in details.errors.
func refusalError(r *commands.RefusedError, loc string) *api.Error {
	problems := []api.Problem{{Location: loc, Message: r.Message}}
	var invalid *slug.Error
	var key *ident.Error
	switch {
	case errors.As(r.Reason, &invalid):
		return &api.Error{Code: api.CodeSlugInvalid, Details: map[string]any{"reason": invalid.Reason,
			"errors": problems}}
	case errors.Is(r.Reason, board.ErrSlugTaken):
		return &api.Error{Code: api.CodeSlugTaken, Details: map[string]any{"errors": problems}}
	case errors.Is(r.Reason, board.ErrFolderNotEmpty):
		return &api.Error{Code: api.CodeFolderNotEmpty, Details: map[string]any{"errors": problems}}
	case errors.As(r.Reason, &key):
		return &api.Error{Code: api.CodeKeyInvalid, Details: map[string]any{"reason": key.Reason,
			"errors": problems}}
	case errors.Is(r.Reason, variable.ErrKeyTaken):
		return &api.Error{Code: api.CodeKeyTaken, Details: map[string]any{"errors": problems}}
	}
	return &api.Error{Code: api.CodeValidationFailed, Details: map[string]any{"errors": problems}}
}
