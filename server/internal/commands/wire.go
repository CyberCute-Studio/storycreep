package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"

	"github.com/danielgtaylor/huma/v2"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// wireCommand is a command as a client sends it, in JSON (PRD FR-API-03):
// its ID, a UUID; its type; its payload; and the revisions that it expects,
// by the public IDs of their entities, as prj_….
type wireCommand struct {
	CommandID   string           `json:"command_id"`
	Type        Type             `json:"type"`
	Payload     json.RawMessage  `json:"payload"`
	ExpectedRev map[string]int64 `json:"expected_rev"`
}

// Parse returns the batch of the commands that raw holds, each a JSON object
// as a client sends it (FR-API-03): {"command_id", "type", "payload",
// "expected_rev"}, where command_id is a UUID, and expected_rev maps the
// public ID of each entity that the command expects a revision of, as
// prj_01HQ8Z3XK4F7NAWR5TCQ9M2BVD, to the revision. It returns an
// *InvalidError that lists every problem of the batch, those that Apply would
// find included, if a command is not an object of those fields, its
// command_id is not a UUID, or expected_rev names an identifier that is not an
// entity's, or one entity twice; otherwise Apply checks the rest, as it does
// of any batch.
func (p *Pipeline) Parse(raw []json.RawMessage) ([]Command, error) {
	return p.registry.parse(raw)
}

// parse is Parse.
func (r *Registry) parse(raw []json.RawMessage) ([]Command, error) {
	cmds := make([]Command, len(raw))
	var problems []Problem
	// skip are the commands that did not decode, of which nothing more can be
	// checked.
	skip := map[int]bool{}
	for i, data := range raw {
		add := func(field, msg string) { problems = append(problems, Problem{Command: i, Field: field, Message: msg}) }
		var w wireCommand
		d := json.NewDecoder(bytes.NewReader(data))
		d.DisallowUnknownFields()
		if err := d.Decode(&w); err != nil {
			add("", "does not decode: "+strings.TrimPrefix(err.Error(), "json: "))
			skip[i] = true
			continue
		}
		c := Command{Type: w.Type, Payload: w.Payload}
		if w.CommandID != "" {
			id, err := ids.ParseUUID(w.CommandID)
			if err != nil {
				add("command_id", "is not a UUID in the form xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx")
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
			if c.ExpectedRev == nil {
				c.ExpectedRev = map[Ref]int64{}
			}
			c.ExpectedRev[ref] = w.ExpectedRev[key]
		}
		cmds[i] = c
	}
	if len(problems) == 0 {
		return cmds, nil
	}
	_, more := r.check(cmds, skip)
	problems = append(problems, more...)
	// The problems of each command stay in the order that they were found
	// in: those of its JSON first.
	slices.SortStableFunc(problems, func(a, b Problem) int { return a.Command - b.Command })
	return nil, &InvalidError{Problems: problems}
}

// schemaPrefix is where the API's OpenAPI document keeps its named schemas,
// which a reference to one starts with.
const schemaPrefix = "#/components/schemas/"

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
		return schemaPrefix + name
	}
	union := &huma.Schema{Description: "A command of the batch: its ID, its type, its payload, as the type's schema " +
		"says, and the revisions that it expects (PRD FR-API-03)."}
	for _, typ := range r.Types() {
		s := r.specs[typ]
		name := typeSchemaName(typ)
		payload := huma.SchemaFromType(reg, s.payload)
		payload.Description = fmt.Sprintf("The payload of a command of the type %s.", typ)
		one := 1.0
		cmd := &huma.Schema{
			Type:                 "object",
			Description:          fmt.Sprintf("A command of the type %s.", typ),
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
