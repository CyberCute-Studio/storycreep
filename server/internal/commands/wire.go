package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/validation"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// wireCommand is a command as a client sends it, in JSON (PRD FR-API-03):
// its ID, a UUID; its type; its payload; and the revisions that it expects,
// by the public IDs of their entities, as prj_…, each of which Parse decodes
// on its own, so that a problem of one is located at its key.
type wireCommand struct {
	CommandID   string                     `json:"command_id"`
	Type        Type                       `json:"type"`
	Payload     json.RawMessage            `json:"payload"`
	ExpectedRev map[string]json.RawMessage `json:"expected_rev"`
}

// Parse returns the batch of the commands that raw holds, each a JSON object
// as a client sends it (FR-API-03): {"command_id", "type", "payload",
// "expected_rev"}, where command_id is a UUID, and expected_rev maps the
// public ID of each entity that the command expects a revision of, as
// prj_01HQ8Z3XK4F7NAWR5TCQ9M2BVD, to the revision, for a batch to apply to
// the project projectID. It returns an *InvalidError if the batch has more
// than maxCommands commands, the most that a client's batch may hold, unless
// it is 0, and otherwise one that lists every problem of the batch, those
// that Apply would find before it reads the database included, if a command
// is not an object of exactly those fields (DecodeObject), its command_id is
// not a UUID, or expected_rev names an identifier that is not an entity's, or
// one entity twice, or a revision that is not an integer; otherwise Apply
// checks the rest, as it does of any batch. A problem of an entity of
// expected_rev, or of its revision, is located at its key as the client
// wrote it. A batch that Parse refuses counts in the pipeline's metrics as
// one that Apply refuses as invalid does.
//
// The commands that Parse returns are the client's, which it may send again:
// Apply remembers the answer to their batch in the batch's idempotency
// record, a refusal that the project's state decides, as one of stale
// revisions, included, where it would not of a batch of the server's own.
func (p *Pipeline) Parse(projectID ids.UUID, raw []json.RawMessage, maxCommands int) ([]Command, error) {
	start := time.Now()
	cmds, err := p.registry.parse(projectID, raw, maxCommands)
	if err != nil {
		p.metrics.observe(nil, err, false, time.Since(start))
	}
	return cmds, err
}

// parse is Parse.
func (r *Registry) parse(projectID ids.UUID, raw []json.RawMessage, maxCommands int) ([]Command, error) {
	if maxCommands > 0 && len(raw) > maxCommands {
		return nil, &InvalidError{Problems: []Problem{{Command: -1, Message: fmt.Sprintf("has more than %d commands",
			maxCommands)}}}
	}
	cmds := make([]Command, len(raw))
	var problems []Problem
	// skip are the commands that did not decode, of which nothing more can be
	// checked.
	skip := map[int]bool{}
	for i, data := range raw {
		add := func(field, msg string) { problems = append(problems, Problem{Command: i, Field: field, Message: msg}) }
		var w wireCommand
		if err := DecodeObject(data, &w); err != nil {
			d := err.(*DecodeError)
			add(d.Field, d.Message)
			skip[i] = true
			continue
		}
		c := Command{Type: w.Type, Payload: w.Payload, sent: true}
		if w.CommandID != "" {
			id, err := ids.ParseUUID(w.CommandID)
			switch {
			case err != nil:
				add("command_id", "is not a UUID in the form xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx")
			case id == (ids.UUID{}):
				add("command_id", "is the nil UUID, which is no command's ID")
			}
			if err != nil || id == (ids.UUID{}) {
				// An ID of its own, so that the check below neither finds it
				// missing nor takes it for another command's.
				id = ids.New()
			}
			c.ID = id
		}
		named := map[Ref]string{}
		for _, key := range slices.Sorted(maps.Keys(w.ExpectedRev)) {
			kind, id, err := ids.DecodeAny(key)
			if err != nil {
				add("expected_rev."+key, "is not an entity's ID: "+err.Error())
				continue
			}
			ref := Ref{Kind: kind, ID: id}
			if other, ok := named[ref]; ok {
				add("expected_rev."+key, "names the same entity as expected_rev."+other)
				continue
			}
			named[ref] = key
			// encoding/json would locate a revision of the wrong type at
			// expected_rev alone, without its key, and take null for 0.
			var rev *int64
			if err := json.Unmarshal(w.ExpectedRev[key], &rev); err != nil {
				add("expected_rev."+key, decodeError(err).Message)
				continue
			}
			if rev == nil {
				add("expected_rev."+key, "expected integer, got null")
				continue
			}
			if c.ExpectedRev == nil {
				c.ExpectedRev = map[Ref]int64{}
			}
			c.ExpectedRev[ref] = *rev
		}
		if len(named) > 0 {
			c.keys = named
		}
		cmds[i] = c
	}
	if len(problems) == 0 {
		return cmds, nil
	}
	_, more := r.check(cmds, skip, projectID)
	problems = append(problems, more...)
	// The problems of each command stay in the order that they were found
	// in: those of its JSON first.
	slices.SortStableFunc(problems, func(a, b Problem) int { return a.Command - b.Command })
	return nil, &InvalidError{Problems: problems}
}

// DecodeError is why DecodeObject could not decode a JSON object, as a
// client reads it: the key of the field whose value is not of the field's
// type, or that is no field, or "" for the value itself, and what is wrong
// with it, in the words of the API's validation of its other bodies, as
// "expected string, got number", without the Go types that encoding/json
// names.
type DecodeError struct {
	Field   string
	Message string
}

func (e *DecodeError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return e.Field + ": " + e.Message
}

// DecodeObject decodes data, a JSON object, into v, a pointer to a struct
// without embedded fields, as encoding/json does, but takes a key only if it
// names one of the struct's fields exactly, as its json tag does (PRD
// FR-API-01 AC2), and returns a *DecodeError if it cannot. encoding/json also
// takes a key that differs from a field's name only in case for the field,
// and keeps the last of two such keys, so that
// {"type": "project.rename", "Type": "project.delete"} would decode as a
// deletion, and takes null for an empty object. huma's validation refuses
// both in the API's other bodies, but the command endpoint decodes its
// batches and their commands without it, batch by batch, with this. A key
// that is no field's exactly is refused as an unexpected property, and null
// as a value that is not an object, as huma's validation refuses them.
func DecodeObject(data []byte, v any) error {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return decodeError(err)
	}
	// null leaves the map nil, where an object, even an empty one, makes it.
	if keys == nil {
		return &DecodeError{Message: "expected object, got null"}
	}
	t := reflect.TypeOf(v).Elem()
	names := make(map[string]bool, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		switch {
		case !f.IsExported() || name == "-":
			continue
		case name == "":
			name = f.Name
		}
		names[name] = true
	}
	for _, key := range slices.Sorted(maps.Keys(keys)) {
		if !names[key] {
			return &DecodeError{Field: key, Message: validation.MsgUnexpectedProperty}
		}
	}
	// Every key is a field's, so none is unknown.
	if err := json.Unmarshal(data, v); err != nil {
		return decodeError(err)
	}
	return nil
}

// decodeError returns the *DecodeError that says why encoding/json could not
// decode a value: of the wrong JSON type, or not JSON at all.
func decodeError(err error) *DecodeError {
	var typ *json.UnmarshalTypeError
	if !errors.As(err, &typ) {
		return &DecodeError{Message: "is not valid JSON"}
	}
	got, _, _ := strings.Cut(typ.Value, " ")
	if got == "bool" {
		got = "boolean"
	}
	return &DecodeError{Field: typ.Field, Message: fmt.Sprintf("expected %s, got %s", jsonType(typ.Type), got)}
}

// jsonType returns the JSON type that encoding/json decodes into a value of
// the Go type t, as the API's schemas name it.
func jsonType(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8,
		reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "array"
	}
	return "object"
}

// SchemaPrefix is where the API's OpenAPI document keeps its named schemas,
// which a reference to one starts with.
const SchemaPrefix = "#/components/schemas/"

// Document adds to reg, the registry of the schemas of the API's OpenAPI
// document (PRD FR-API-02), the schema of a command of each registered type,
// as a client sends it (Parse), and of its payload, each named after the
// type, as ProjectRenameCommand and ProjectRenamePayload for project.rename,
// and returns the schema of a command of any of them: one of them, which its
// type tells apart. A payload's schema is built from its type as the one that
// the pipeline validates it with is (Register), so that the document
// describes every payload that the pipeline accepts (plan note N30). The
// schemas of the structs that a payload holds are named in reg as reg names
// them. It panics if reg has a schema of one of the names already, as when it
// is called twice.
func (r *Registry) Document(reg huma.Registry) *huma.Schema {
	schemas := reg.Map()
	add := func(name string, s *huma.Schema) string {
		if _, ok := schemas[name]; ok {
			panic(fmt.Sprintf("commands: the API's document has a schema named %s already", name))
		}
		schemas[name] = s
		return SchemaPrefix + name
	}
	union := &huma.Schema{Description: "A command of the batch: its ID, its type, its payload, as the type's schema " +
		"says, and the revisions that it expects (PRD FR-API-03)."}
	for _, typ := range r.Types() {
		s := r.specs[typ]
		name := typeSchemaName(typ)
		payload := huma.SchemaFromType(reg, s.payload)
		payload.Description = fmt.Sprintf("The payload of a command of the type %s.", typ)
		one := 1.0
		description := fmt.Sprintf("A command of the type %s.", typ)
		if s.sessionOnly {
			description += " Only a session may apply it: a batch with it that an API token sends is refused " +
				"with 403 session_required, whatever the token's scopes (PRD FR-AUTH-04)."
		}
		cmd := &huma.Schema{
			Type:                 "object",
			Description:          description,
			AdditionalProperties: false,
			Required:             []string{"command_id", "type", "payload"},
			Properties: map[string]*huma.Schema{
				"command_id": {Type: "string", Format: "uuid", Description: "The command's ID, a UUID that the " +
					"client makes, unique within the project. A command whose ID the project's batches applied " +
					"already is answered as it was, and not applied again."},
				"type":    {Type: "string", Enum: []any{string(typ)}, Description: "The command's type."},
				"payload": {Ref: add(name+"Payload", payload)},
				"expected_rev": {Type: "object", AdditionalProperties: &huma.Schema{Type: "integer",
					Format: "int64", Minimum: &one}, Description: "The revision that the client read of each " +
					"entity that the command changes or refers to, by the entity's ID, as prj_…: the batch applies " +
					"only if every entity still has it before the batch, and is answered with 409 " +
					"revision_conflict otherwise. An entity that the batch creates has none. Without it, the " +
					"command expects no revision."},
			},
		}
		// A schema made by hand validates its properties and its messages
		// once they are computed, which huma does for the schemas that it
		// makes itself.
		cmd.PrecomputeMessages()
		cmd.Properties["expected_rev"].AdditionalProperties.(*huma.Schema).PrecomputeMessages()
		union.OneOf = append(union.OneOf, &huma.Schema{Ref: add(name+"Command", cmd)})
	}
	union.PrecomputeMessages()
	return union
}

// Document adds to reg the schemas of the commands that the pipeline applies,
// as Registry.Document does of the pipeline's registry, and returns the
// schema of a command of any of them, so that the API's document describes
// the commands that Parse and Apply take, and no others.
func (p *Pipeline) Document(reg huma.Registry) *huma.Schema {
	return p.registry.Document(reg)
}

// typeSchemaName returns the name of the schemas of a command of the type typ:
// its words, each capitalised, as ProjectUpdateSettings for
// project.update_settings.
func typeSchemaName(typ Type) string {
	var b strings.Builder
	for _, word := range strings.FieldsFunc(string(typ), func(r rune) bool { return r == '.' || r == '_' }) {
		b.WriteRune(unicode.ToUpper(rune(word[0])))
		b.WriteString(word[1:])
	}
	return b.String()
}
