package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/component"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

func componentTypesPath(project string) string   { return projectPath(project) + "/component-types" }
func componentTypePath(project, t string) string { return componentTypesPath(project) + "/" + t }
func conversionPath(project, t, a string) string {
	return componentTypePath(project, t) + "/attributes/" + a + "/conversion"
}
func componentsPath(project string) string           { return projectPath(project) + "/components" }
func componentPath(project, c string) string         { return componentsPath(project) + "/" + c }
func newComponentTypeID() string                     { return ids.Encode(ids.ComponentType, ids.New()) }
func newAttributeID() string                         { return ids.Encode(ids.AttributeDef, ids.New()) }
func newComponentID() string                         { return ids.Encode(ids.Component, ids.New()) }
func componentCommand(typ string, p any) wireCommand { return wireCommand{Type: typ, Payload: p} }

// componentType adds a component type to the project, as the superuser, and
// returns its ID.
func (f *rolesFixture) componentType(t *testing.T, project string) string {
	t.Helper()
	id := ids.New()
	if _, err := f.admin.Exec(context.Background(), `INSERT INTO component_type (id, workspace_id, project_id, key,
		name) VALUES ($1, $2, $3, $4, 'Character')`, id, decode(t, ids.Workspace, f.ws), decode(t, ids.Project, project),
		"character_"+strings.ReplaceAll(id.String(), "-", "")); err != nil {
		t.Fatal(err)
	}
	return ids.Encode(ids.ComponentType, id)
}

// attribute adds an int attribute definition to the type typ of the project,
// as the superuser, and returns its ID.
func (f *rolesFixture) attribute(t *testing.T, project, typ string) string {
	t.Helper()
	id := ids.New()
	if _, err := f.admin.Exec(context.Background(), `INSERT INTO attribute_def (id, workspace_id, project_id,
		component_type_id, "order", key, name, type, default_value) VALUES ($1, $2, $3, $4, 'V', 'trust', 'Trust',
		'int', '0')`, id, decode(t, ids.Workspace, f.ws), decode(t, ids.Project, project),
		decode(t, ids.ComponentType, typ)); err != nil {
		t.Fatal(err)
	}
	return ids.Encode(ids.AttributeDef, id)
}

// component adds a component of the type typ to the project, as the
// superuser, and returns its ID.
func (f *rolesFixture) component(t *testing.T, project, typ string) string {
	t.Helper()
	id := ids.New()
	if _, err := f.admin.Exec(context.Background(), `INSERT INTO component (id, workspace_id, project_id,
		component_type_id, "order", key, name) VALUES ($1, $2, $3, $4, 'V', $5, 'Elara')`, id,
		decode(t, ids.Workspace, f.ws), decode(t, ids.Project, project), decode(t, ids.ComponentType, typ),
		"elara_"+strings.ReplaceAll(id.String(), "-", "")); err != nil {
		t.Fatal(err)
	}
	return ids.Encode(ids.Component, id)
}

// TestComponents checks the routes that read a project's component types and
// components: a project is created with the default types, in their order;
// once its commands have made an attribute definition and components, the
// types are listed with their definitions, the components are listed page by
// page, by type, and by what their names or keys hold, without their
// descriptions and values, which a component's own read returns, and a
// component in the trash is not found.
//
// Verifies: FR-COMP-01/AC1, FR-COMP-02/AC3
func TestComponents(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	p := createProject(t, s.main, owner, ws, "Harbour")
	var types api.Page[component.ComponentType]
	if res := call(s.main, http.MethodGet, componentTypesPath(p.ID), nil, owner.header(false)); res.status !=
		http.StatusOK || json.Unmarshal(res.body, &types) != nil {
		t.Fatalf("list the types: %d %s", res.status, res.body)
	}
	var keys []string
	for _, ct := range types.Items {
		keys = append(keys, ct.Key)
	}
	if want := component.DefaultTypeKeys(); !slices.Equal(keys, want) || types.Items[0].Name != "Character" {
		t.Fatalf("a new project's types are %v, want %v", keys, want)
	}
	character, item := types.Items[0].ID, types.Items[1].ID
	trust, folder := newAttributeID(), newFolderID()
	elara, mira, sword := newComponentID(), newComponentID(), newComponentID()
	if res := call(s.main, http.MethodPost, commandsPath(p.ID), commandBatch(t,
		componentCommand("folder.create", map[string]string{"id": folder, "kind": "component", "name": "Crew"}),
		componentCommand("component.create", map[string]any{"id": elara, "component_type_id": character,
			"name": "Elara Vance", "folder_id": folder,
			"description": json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[` +
				`{"type":"text","text":"The keeper","marks":[{"type":"italic"},{"type":"bold"}]}]}]}`)}),
		componentCommand("component.create", map[string]any{"id": mira, "component_type_id": character,
			"name": "Mira"}),
		componentCommand("component.create", map[string]any{"id": sword, "component_type_id": item,
			"name": "Old sword", "key": "elara_sword"}),
		componentCommand("attribute.create", map[string]any{"id": trust, "component_type_id": character,
			"name": "Trust", "type": "int", "default": 3, "runtime": true}),
	), owner.header(true)); res.status != http.StatusOK {
		t.Fatalf("create the components: %d %s", res.status, res.body)
	}
	var ct component.ComponentType
	if res := call(s.main, http.MethodGet, componentTypePath(p.ID, character), nil, owner.header(false)); res.status !=
		http.StatusOK || json.Unmarshal(res.body, &ct) != nil || len(ct.Attributes) != 1 ||
		ct.Attributes[0].Key != "trust" || !ct.Attributes[0].Runtime {
		t.Errorf("the type is %d %s", res.status, res.body)
	}
	var c struct {
		component.Component
		Description json.RawMessage            `json:"description"`
		Attributes  map[string]json.RawMessage `json:"attributes"`
	}
	res := call(s.main, http.MethodGet, componentPath(p.ID, elara), nil, owner.header(false))
	if res.status != http.StatusOK || json.Unmarshal(res.body, &c) != nil {
		t.Fatalf("read the component: %d %s", res.status, res.body)
	}
	// The attribute definition added after the component gave it its
	// default (FR-COMP-01 AC1), and the description is in its canonical
	// form, its marks in order.
	if c.Key != "elara_vance" || c.Name != "Elara Vance" || c.ComponentTypeID != character || c.FolderID != folder ||
		string(c.Attributes[trust]) != "3" || c.Rev != 2 || !strings.Contains(string(c.Description),
		`"marks":[{"type":"bold"},{"type":"italic"}]`) {
		t.Errorf("the component is %s", res.body)
	}
	list := func(query string) []string {
		t.Helper()
		var out []string
		for cursor := ""; ; {
			var page api.Page[json.RawMessage]
			res := call(s.main, http.MethodGet, componentsPath(p.ID)+"?limit=1&cursor="+url.QueryEscape(cursor)+query, nil,
				owner.header(false))
			if res.status != http.StatusOK || json.Unmarshal(res.body, &page) != nil {
				t.Fatalf("list the components%s: %d %s", query, res.status, res.body)
			}
			for _, raw := range page.Items {
				var s component.Summary
				var fields map[string]json.RawMessage
				if json.Unmarshal(raw, &s) != nil || json.Unmarshal(raw, &fields) != nil {
					t.Fatalf("a component listed: %s", raw)
				}
				if _, ok := fields["description"]; ok {
					t.Errorf("a component is listed with its description: %s", raw)
				}
				out = append(out, s.ID)
			}
			if page.NextCursor == nil {
				break
			}
			cursor = *page.NextCursor
		}
		return out
	}
	sorted := func(ids ...string) []string {
		slices.Sort(ids)
		return ids
	}
	for query, want := range map[string][]string{
		"": sorted(elara, mira, sword),
		"&component_type_id=" + url.QueryEscape(character): sorted(elara, mira),
		"&component_type_id=" + url.QueryEscape(item):      {sword},
		"&query=ELARA":                   sorted(elara, sword),
		"&query=vance":                   {elara},
		"&query=" + url.QueryEscape("%"): nil,
		"&query=ra&component_type_id=" + url.QueryEscape(character): sorted(elara, mira),
	} {
		if got := list(query); !slices.Equal(got, want) {
			t.Errorf("the components listed%s are %v, want %v", query, got, want)
		}
	}
	wantCode(t, "a query of control characters", call(s.main, http.MethodGet, componentsPath(p.ID)+"?query=%07",
		nil, owner.header(false)), http.StatusUnprocessableEntity, api.CodeValidationFailed)
	if res := call(s.main, http.MethodPost, commandsPath(p.ID), commandBatch(t,
		componentCommand("component.delete", map[string]string{"id": mira})), owner.header(true)); res.status !=
		http.StatusOK {
		t.Fatalf("delete the component: %d %s", res.status, res.body)
	}
	wantCode(t, "a component in the trash", call(s.main, http.MethodGet, componentPath(p.ID, mira), nil,
		owner.header(false)), http.StatusNotFound, api.CodeNotFound)
	if got := list(""); !slices.Equal(got, sorted(elara, sword)) {
		t.Errorf("after the deletion, the components listed are %v", got)
	}
}

// TestConversion checks the route that lists the components whose values of
// an attribute definition would not convert to another type, those in the
// trash included, before a retype applies (FR-COMP-01 AC2), and that the
// retype then gives them the default.
//
// Verifies: FR-COMP-01/AC2
func TestConversion(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	p := createProject(t, s.main, owner, ws, "Harbour")
	ct, mood := newComponentTypeID(), newAttributeID()
	elara, mira, sol := newComponentID(), newComponentID(), newComponentID()
	if res := call(s.main, http.MethodPost, commandsPath(p.ID), commandBatch(t,
		componentCommand("component_type.create", map[string]string{"id": ct, "name": "Ship"}),
		componentCommand("attribute.create", map[string]any{"id": mood, "component_type_id": ct, "name": "Mood",
			"type": "string", "default": "7"}),
		componentCommand("component.create", map[string]any{"id": elara, "component_type_id": ct, "name": "Elara",
			"attributes": map[string]any{mood: "calm"}}),
		componentCommand("component.create", map[string]any{"id": mira, "component_type_id": ct, "name": "Mira",
			"attributes": map[string]any{mood: "12"}}),
		componentCommand("component.create", map[string]any{"id": sol, "component_type_id": ct, "name": "Sol",
			"attributes": map[string]any{mood: "angry"}}),
		componentCommand("component.delete", map[string]string{"id": sol}),
	), owner.header(true)); res.status != http.StatusOK {
		t.Fatalf("create the components: %d %s", res.status, res.body)
	}
	unconvertible := func(query string) []string {
		t.Helper()
		var page api.Page[component.Unconvertible]
		res := call(s.main, http.MethodGet, conversionPath(p.ID, ct, mood)+"?"+query, nil, owner.header(false))
		if res.status != http.StatusOK || json.Unmarshal(res.body, &page) != nil {
			t.Fatalf("the conversion %s: %d %s", query, res.status, res.body)
		}
		var out []string
		for _, u := range page.Items {
			out = append(out, u.Key)
			if (u.DeletedAt != nil) != (u.ComponentID == sol) {
				t.Errorf("%s is listed as in the trash: %v", u.Key, u.DeletedAt)
			}
		}
		slices.Sort(out)
		return out
	}
	for query, want := range map[string][]string{
		"type=int":                    {"elara", "sol"},
		"type=float":                  {"elara", "sol"},
		"type=bool":                   {"elara", "mira", "sol"},
		"type=text":                   nil,
		"type=enum&options=calm%0A12": {"sol"},
	} {
		if got := unconvertible(query); !slices.Equal(got, want) {
			t.Errorf("the values that %s would not convert are %v, want %v", query, got, want)
		}
	}
	for query, loc := range map[string]string{
		"type=string":             "query.type",
		"type=enum":               "query.options",
		"type=enum&options=a%0Aa": "query.options",
		"type=int&options=a":      "query.options",
	} {
		res := call(s.main, http.MethodGet, conversionPath(p.ID, ct, mood)+"?"+query, nil, owner.header(false))
		if code, locs, _ := problemsOf(t, res); res.status != http.StatusUnprocessableEntity ||
			code != api.CodeValidationFailed || !slices.Contains(locs, loc) {
			t.Errorf("the conversion %s: %d %s, want 422 at %s", query, res.status, res.body, loc)
		}
	}
	wantCode(t, "a definition of another type", call(s.main, http.MethodGet, conversionPath(p.ID, newComponentTypeID(),
		mood)+"?type=int", nil, owner.header(false)), http.StatusNotFound, api.CodeNotFound)

	// The retype converts what converts, and gives the default to what does
	// not, the value of the component in the trash among them.
	if res := call(s.main, http.MethodPost, commandsPath(p.ID), commandBatch(t,
		componentCommand("attribute.retype", map[string]any{"id": mood, "type": "int", "conversion": "convert",
			"default": -1})), owner.header(true)); res.status != http.StatusOK {
		t.Fatalf("retype the definition: %d %s", res.status, res.body)
	}
	var c struct {
		Attributes map[string]json.RawMessage `json:"attributes"`
	}
	for id, want := range map[string]string{elara: "-1", mira: "12"} {
		if res := call(s.main, http.MethodGet, componentPath(p.ID, id), nil, owner.header(false)); res.status !=
			http.StatusOK || json.Unmarshal(res.body, &c) != nil || string(c.Attributes[mood]) != want {
			t.Errorf("after the retype, the component %s is %d %s, want %s", id, res.status, res.body, want)
		}
	}
}

// TestComponentRefusals checks how the API answers a command of a component,
// a type, or an attribute definition that refuses its batch, at the location
// of the payload's field: a key that is not an identifier with 422
// key_invalid and the rule that it breaks, a key that another entity of its
// scope has with 409 key_taken, the deletion of a type that a component has
// with 409 component_type_in_use, and a description that the rich-text
// validator refuses at the field, joined with where in the document it is.
//
// Verifies: FR-COMP-01/AC3, FR-COMP-02/AC1
func TestComponentRefusals(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	p := createProject(t, s.main, owner, ws, "Harbour")
	ct, trust, elara := newComponentTypeID(), newAttributeID(), newComponentID()
	if res := call(s.main, http.MethodPost, commandsPath(p.ID), commandBatch(t,
		componentCommand("component_type.create", map[string]string{"id": ct, "name": "Ship", "key": "ship"}),
		componentCommand("attribute.create", map[string]any{"id": trust, "component_type_id": ct, "name": "Trust",
			"type": "int"}),
		componentCommand("component.create", map[string]any{"id": elara, "component_type_id": ct, "name": "Elara"}),
	), owner.header(true)); res.status != http.StatusOK {
		t.Fatalf("create the components: %d %s", res.status, res.body)
	}
	// A list nested deeper than the validator takes, which the schema does
	// not refuse.
	deep := `{"type":"paragraph"}`
	for range 6 {
		deep = `{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph"},` + deep + `]}]}`
	}
	deep = `{"type":"doc","content":[` + deep + `]}`
	for _, c := range []struct {
		cmd    wireCommand
		status int
		code   api.Code
		loc    string
		reason string
	}{
		{componentCommand("component.set_key", map[string]string{"id": elara, "key": "9lives"}),
			http.StatusUnprocessableEntity, api.CodeKeyInvalid, "body.commands[0].payload.key", "format"},
		{componentCommand("component.set_key", map[string]string{"id": elara, "key": "board"}),
			http.StatusUnprocessableEntity, api.CodeKeyInvalid, "body.commands[0].payload.key", "reserved"},
		{componentCommand("component.create", map[string]any{"id": newComponentID(), "component_type_id": ct,
			"name": "Elara", "key": "elara"}), http.StatusConflict, api.CodeKeyTaken, "body.commands[0].payload.key",
			""},
		{componentCommand("attribute.create", map[string]any{"id": newAttributeID(), "component_type_id": ct,
			"name": "Trust", "key": "trust", "type": "bool"}), http.StatusConflict, api.CodeKeyTaken,
			"body.commands[0].payload.key", ""},
		{componentCommand("attribute.set_key", map[string]string{"id": trust, "key": "a-b"}),
			http.StatusUnprocessableEntity, api.CodeKeyInvalid, "body.commands[0].payload.key", "format"},
		{componentCommand("component_type.create", map[string]string{"id": newComponentTypeID(), "name": "Ship",
			"key": "ship"}), http.StatusConflict, api.CodeKeyTaken, "body.commands[0].payload.key", ""},
		{componentCommand("component_type.delete", map[string]string{"id": ct}), http.StatusConflict,
			api.CodeComponentTypeInUse, "body.commands[0].payload.id", ""},
		{componentCommand("component.set_attribute", map[string]any{"id": elara, "attribute_id": trust,
			"value": "three"}), http.StatusUnprocessableEntity, api.CodeValidationFailed,
			"body.commands[0].payload.value", ""},
		{componentCommand("component.describe", map[string]any{"id": elara, "description": json.RawMessage(deep)}),
			http.StatusUnprocessableEntity, api.CodeValidationFailed,
			"body.commands[0].payload.description.content[0].content[0].content[1].content[0].content[1]." +
				"content[0].content[1].content[0].content[1].content[0].content[1]", ""},
	} {
		res := call(s.main, http.MethodPost, commandsPath(p.ID), commandBatch(t, c.cmd), owner.header(true))
		code, locs, details := problemsOf(t, res)
		if res.status != c.status || code != c.code || !slices.Contains(locs, c.loc) ||
			c.reason != "" && details["reason"] != c.reason {
			t.Errorf("%v: %d %s, want %d %s at %s", c.cmd, res.status, res.body, c.status, c.code, c.loc)
		}
	}
}
