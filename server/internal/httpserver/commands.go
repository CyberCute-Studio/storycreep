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
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
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
	// CurrentRev and State are the entity's revision and the entity as it is
	// now, if it still exists.
	CurrentRev *int64          `json:"current_rev,omitempty"`
	State      json.RawMessage `json:"state,omitempty"`
}

// revisionConflictError returns the error that the API answers err with if
// err is a *commands.RevisionConflictError, or err itself otherwise: 409
// revision_conflict, whose details list as conflicts each entity of a
// command of the batch whose revision is not the one that the command
// expected, with the command, by its index in the batch, its command_id, and
// its type, the revision that it expected, and the entity's current revision
// and state if it still exists, or exists false if it does not (PRD FR-API-03
// AC3, FR-OFF-04 AC5). The error mapper of a route that applies commands
// calls it.
func revisionConflictError(err error) error {
	var conflict *commands.RevisionConflictError
	if !errors.As(err, &conflict) {
		return err
	}
	conflicts := make([]conflictBody, len(conflict.Conflicts))
	for i, c := range conflict.Conflicts {
		conflicts[i] = conflictBody{Command: c.Command, CommandID: c.CommandID.String(), Type: c.Type,
			ID: ids.Encode(c.Kind, c.ID), ExpectedRev: c.Expected, Exists: c.Exists}
		if c.Exists {
			current := c.Current
			conflicts[i].CurrentRev, conflicts[i].State = &current, c.State
		}
	}
	return &api.Error{Code: api.CodeRevisionConflict, Details: map[string]any{"conflicts": conflicts}}
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
// as it is decoded: one batch of commands, or a list of batches, each command
// as its JSON, which the pipeline reads (commands.Pipeline.Parse), so that a
// command that it cannot read refuses its own batch alone. Its schema in the
// OpenAPI document is commandsSchemas', which describes each type of
// command.
type commandsBody struct {
	Commands []json.RawMessage `json:"commands"`
	Batches  []batchBody       `json:"batches"`
}

// batchBody is a batch of a list of batches.
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
// type that registry holds (commands.Registry.Document), or a list of
// batches; and what one batch did, or what each batch of a list did.
func commandsSchemas(doc huma.Registry, registry *commands.Registry) (body, answer *huma.Schema) {
	const prefix = "#/components/schemas/"
	schemas := doc.Map()
	command := registry.Document(doc)
	schemas["CommandBody"] = command
	one := 1
	batch := &huma.Schema{
		Type:                 "object",
		Description:          "A batch of commands, which applies whole, in one transaction, or not at all.",
		AdditionalProperties: false,
		Required:             []string{"commands"},
		Properties: map[string]*huma.Schema{
			"commands": {Type: "array", MinItems: &one, MaxItems: ptr(maxBatchCommands),
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
			"batches": {Type: "array", MinItems: &one, MaxItems: ptr(maxBatches),
				Items:       &huma.Schema{Ref: prefix + "CommandBatchBody"},
				Description: fmt.Sprintf("The batches, at least one, and at most %d.", maxBatches)},
		},
	}
	schemas["CommandBatchesBody"] = batches
	for _, s := range []*huma.Schema{command, batch, batches} {
		s.PrecomputeMessages()
	}
	body = &huma.Schema{OneOf: []*huma.Schema{{Ref: prefix + "CommandBatchBody"}, {Ref: prefix + "CommandBatchesBody"}}}
	answer = &huma.Schema{OneOf: []*huma.Schema{
		doc.Schema(reflect.TypeFor[commandResultBody](), true, ""),
		doc.Schema(reflect.TypeFor[commandResultsBody](), true, ""),
	}}
	return body, answer
}

// ptr returns a pointer to v.
func ptr(v int) *int { return &v }

// registerCommands registers the command endpoint (PRD FR-API-03, §10.5),
// which applies batches of the commands of registry with pipeline.
func registerCommands(m *api.Mux, pipeline *commands.Pipeline, registry *commands.Registry) {
	body, answer := commandsSchemas(m.OpenAPI().Components.Schemas, registry)
	api.Register(m, api.ApplyCommands, huma.Operation{
		OperationID: "applyCommands",
		Method:      http.MethodPost,
		Path:        "/api/v1/projects/{project_id}/commands",
		Summary:     "Apply batches of commands to a project",
		Description: "Applies a batch of commands to a project, every change of whose content is a command of a " +
			"type that the body's schema describes, with its payload (PRD FR-API-03). The body is a batch, " +
			"{\"commands\": [...]}, or a list of batches, {\"batches\": [{\"commands\": [...]}, ...]}. A batch " +
			"applies whole, in one transaction, or not at all, and is one step of undo. Each command is authorised " +
			"by the signed-in user's role as its type's operation of the permission matrix needs (PRD §5.6), and " +
			"a role that does not allow one refuses its batch with 403 insufficient_role, or owner_required." +
			projectNotFoundDoc + tokenNotFoundDoc + " Every expected_rev of a batch is checked against the " +
			"entities as they are before the batch, and a stale one refuses it with 409 revision_conflict, whose " +
			"details hold the entities' current revisions and states. An archived project refuses a batch with " +
			"409 project_archived, unless each of its commands applies to an archived project, and a batch that " +
			"would take the workspace past a limit of its plan is refused with 409 limit_exceeded. A batch that " +
			"its schema, or its commands' own checks, refuse is answered with 422 validation_failed, whose " +
			"details locate each problem, as body.commands[2].payload.name." +
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
				"validation_failed, and a batch of more than %d commands is rejected so.", maxBatches,
				maxBatchCommands) +
			tokenChangeDoc(scope.ProjectWrite),
		Tags: []string{"Projects"},
		RequestBody: &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{
			"application/json": {Schema: body}}},
		// The pipeline checks each batch's commands, so that one that it
		// cannot apply refuses its own batch alone, with the location of each
		// problem, where huma would refuse the whole request, and a command
		// whose type's schema refuses it as one that matches none of the
		// types.
		SkipValidateBody: true,
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
		for i, b := range in.Body.Batches {
			res, err := applyBatch(ctx, pipeline, actor, id, b.Commands, fmt.Sprintf("body.batches[%d].commands", i))
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
}

// applyBatch applies the batch of the commands raw, as a client sent them at
// the location at of the request, as body.commands, to the project
// projectID, as the actor asks, and returns what it did, or the error that the
// API answers it with (commandError).
func applyBatch(ctx context.Context, pipeline *commands.Pipeline, actor commands.Actor, projectID ids.UUID,
	raw []json.RawMessage, at string) (commands.Result, error) {
	if len(raw) > maxBatchCommands {
		return commands.Result{}, bodyProblem(at, fmt.Sprintf("has more than %d commands", maxBatchCommands))
	}
	cmds, err := pipeline.Parse(raw)
	if err != nil {
		return commands.Result{}, commandError(err, at)
	}
	res, err := pipeline.Apply(ctx, actor, projectID, cmds)
	if err != nil {
		return commands.Result{}, commandError(err, at)
	}
	return res, nil
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
// body.commands[2].payload.name, and 422 idempotency_mismatch, whose details
// list as applied the command_ids of the batch that were applied.
func commandError(err error, at string) error {
	var invalid *commands.InvalidError
	var mismatch *commands.IdempotencyMismatchError
	var insufficient *scope.InsufficientError
	switch {
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
	case errors.Is(err, commands.ErrNotFound):
		return api.NewError(api.CodeNotFound)
	case errors.Is(err, commands.ErrInsufficientRole):
		return api.NewError(api.CodeInsufficientRole)
	case errors.Is(err, commands.ErrOwnerRequired):
		return api.NewError(api.CodeOwnerRequired)
	case errors.As(err, &insufficient):
		return insufficientScope(insufficient.Scope)
	case errors.Is(err, commands.ErrProjectArchived):
		return api.NewError(api.CodeProjectArchived)
	}
	return revisionConflictError(limitExceededError(err))
}
