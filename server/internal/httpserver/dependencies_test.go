package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/dependency"
	"github.com/CyberCute-Studio/storycreep/server/internal/dependency/dependencytest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

func variableUsagesPath(project, v, access string) string {
	return variablePath(project, v) + "/usages?access=" + access
}

func componentUsagesPath(project, c, use string) string {
	return componentPath(project, c) + "/usages?use=" + use
}

// richText returns a rich-text document of one paragraph of the inline nodes,
// each a JSON object.
func richText(inlines ...string) json.RawMessage {
	return json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[` + strings.Join(inlines, ",") +
		`]}]}`)
}

func textInline(text string) string { return fmt.Sprintf(`{"type":"text","text":%q}`, text) }
func mentionInline(component string) string {
	return fmt.Sprintf(`{"type":"mention","attrs":{"id":%q}}`, component)
}
func valueInline(ref string) string {
	return fmt.Sprintf(`{"type":"valueRef","attrs":{"ref":%q}}`, ref)
}
func attributeInline(component, attribute string) string {
	return fmt.Sprintf(`{"type":"valueRef","attrs":{"ref":%q,"attribute":%q}}`, component, attribute)
}

// usageWorld is a project of two boards, Harbour and Lighthouse, whose content
// reads and writes its variables and its components' attributes, and
// mentions its components, as the usage queries' tests read them:
//
//   - the global variables met_elara and gold, and the components elara and
//     mira of the type Cast, with the runtime attributes trust and mood;
//   - on Harbour, the element keeper, whose speaker is elara, whose text shows
//     met_elara and mentions mira, whose on-enter script is elara.trust += 1
//     and whose on-exit script is elara.mood = "calm", and the element pier,
//     with the connection gate from keeper to pier, whose condition is
//     met_elara == true, whose label shows elara.trust, and whose on-traverse
//     script is gold = gold + 1;
//   - on Lighthouse, the branch lamp, whose output lit's condition is
//     mira.trust >= 3, and which leads to the element tower, whose on-enter
//     script is met_elara = true and gold -= 2, and whose on-exit script,
//     met_elara = nope, does not compile;
//   - and elara's description, which mentions mira and shows gold.
type usageWorld struct {
	s                                  *server
	admin                              *pgx.Conn
	owner                              signedIn
	project                            string
	harbour, lighthouse                string
	metElara, gold                     string
	cast, trust, mood                  string
	elara, mira                        string
	keeper, pier, gate, lamp, lit      string
	tower                              string
	keeperEnter, keeperExit            string
	gateCondition, gateTraverse        string
	litCondition, towerEnter, towerOut string
}

// newUsageWorld returns a new usageWorld, made in one batch, of a server of
// its own.
func newUsageWorld(t *testing.T) *usageWorld {
	t.Helper()
	s, d, owner, ws := inviteServer(t, "open", nil)
	w := &usageWorld{s: s, admin: d.Admin(t), owner: owner, project: createProject(t, s.main, owner, ws, "Harbour").ID,
		harbour: newBoardID(), lighthouse: newBoardID(), metElara: ids.Encode(ids.Variable, ids.New()),
		gold: ids.Encode(ids.Variable, ids.New()), cast: newComponentTypeID(), trust: newAttributeID(),
		mood: newAttributeID(), elara: newComponentID(), mira: newComponentID(), keeper: newNodeID(),
		pier: newNodeID(), gate: newConnectionID(), lamp: newNodeID(), lit: ids.Encode(ids.BranchOutput, ids.New()),
		tower: newNodeID()}
	pos := map[string]int{"x": 0, "y": 0}
	cmd := func(typ string, p map[string]any) wireCommand { return wireCommand{Type: typ, Payload: p} }
	res := call(s.main, http.MethodPost, commandsPath(w.project), commandBatch(t,
		cmd("board.create", map[string]any{"id": w.harbour, "name": "Harbour"}),
		cmd("board.create", map[string]any{"id": w.lighthouse, "name": "Lighthouse"}),
		cmd("variable.create", map[string]any{"id": w.metElara, "key": "met_elara", "type": "bool", "scope": "global"}),
		cmd("variable.create", map[string]any{"id": w.gold, "key": "gold", "type": "int", "scope": "global"}),
		cmd("component_type.create", map[string]any{"id": w.cast, "name": "Cast"}),
		cmd("attribute.create", map[string]any{"id": w.trust, "component_type_id": w.cast, "name": "Trust",
			"type": "int", "runtime": true}),
		cmd("attribute.create", map[string]any{"id": w.mood, "component_type_id": w.cast, "name": "Mood",
			"type": "string", "runtime": true}),
		cmd("component.create", map[string]any{"id": w.elara, "component_type_id": w.cast, "name": "Elara",
			"description": richText(mentionInline(w.mira), textInline(" owes "), valueInline(w.gold))}),
		cmd("component.create", map[string]any{"id": w.mira, "component_type_id": w.cast, "name": "Mira"}),
		cmd("node.create", map[string]any{"id": w.keeper, "board_id": w.harbour, "type": "element", "title": "Keeper",
			"position": pos, "speaker_component_id": w.elara,
			"text":     richText(textInline("Met: "), valueInline(w.metElara), textInline(" with "), mentionInline(w.mira)),
			"on_enter": "elara.trust += 1", "on_exit": `elara.mood = "calm"`}),
		cmd("node.create", map[string]any{"id": w.pier, "board_id": w.harbour, "type": "element", "title": "Pier",
			"position": pos}),
		cmd("connection.create", map[string]any{"id": w.gate, "from_node_id": w.keeper, "to_node_id": w.pier,
			"condition": "met_elara == true", "on_traverse": "gold = gold + 1",
			"label": richText(textInline("Trust "), attributeInline(w.elara, w.trust))}),
		cmd("node.create", map[string]any{"id": w.lamp, "board_id": w.lighthouse, "type": "branch", "title": "Lamp",
			"position": pos}),
		cmd("branch_output.create", map[string]any{"id": w.lit, "node_id": w.lamp, "condition": "mira.trust >= 3"}),
		cmd("node.create", map[string]any{"id": w.tower, "board_id": w.lighthouse, "type": "element",
			"title": "Tower", "position": pos, "on_enter": "met_elara = true\ngold -= 2", "on_exit": "met_elara = nope"}),
		cmd("connection.create", map[string]any{"id": newConnectionID(), "from_node_id": w.lamp, "from_port": w.lit,
			"to_node_id": w.tower}),
	), owner.header(true))
	if res.status != http.StatusOK {
		t.Fatalf("make the world: %d %s", res.status, res.body)
	}
	// The expressions' UUIDs, which the usages' keys order the sites by.
	for _, e := range []struct {
		id    *string
		owner string
		field string
	}{{&w.keeperEnter, w.keeper, "on_enter"}, {&w.keeperExit, w.keeper, "on_exit"},
		{&w.gateCondition, w.gate, "condition"}, {&w.gateTraverse, w.gate, "on_traverse"},
		{&w.litCondition, w.lit, "condition"}, {&w.towerEnter, w.tower, "on_enter"},
		{&w.towerOut, w.tower, "on_exit"}} {
		var id string
		if err := w.admin.QueryRow(context.Background(), `SELECT id::text FROM expression WHERE field = $1
			AND $2 IN (node_id, connection_id, branch_output_id)`, e.field, uuidOf(t, e.owner)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		*e.id = id
	}
	return w
}

// uuidOf returns the UUID of the public ID id, of any kind.
func uuidOf(t testing.TB, id string) ids.UUID {
	t.Helper()
	for _, k := range ids.Kinds() {
		if u, err := ids.Decode(k, id); err == nil {
			return u
		}
	}
	t.Fatalf("%s is no public ID", id)
	return ids.UUID{}
}

// usages returns every usage of the list at path, page by page, of limit
// items each, as si, and fails the test unless each page is 200.
func usages(t *testing.T, h http.Handler, si signedIn, path string, limit int) []dependency.Usage {
	t.Helper()
	var out []dependency.Usage
	for cursor := ""; ; {
		url := fmt.Sprintf("%s&limit=%d", path, limit)
		if cursor != "" {
			url += "&cursor=" + cursor
		}
		res := call(h, http.MethodGet, url, nil, si.header(false))
		var page api.Page[dependency.Usage]
		if res.status != http.StatusOK || json.Unmarshal(res.body, &page) != nil {
			t.Fatalf("GET %s: %d %s", url, res.status, res.body)
		}
		out = append(out, page.Items...)
		if page.NextCursor == nil {
			return out
		}
		cursor = *page.NextCursor
	}
}

// sites returns the usages as lines of their sites and what they name, in
// their order: the site, its board's and its node's names, and the ID of its
// connection, output, or component, and of its attribute, where it has one.
func sites(us []dependency.Usage) []string {
	out := make([]string, len(us))
	for i, u := range us {
		line := u.Site
		if u.BoardID != "" {
			line += " " + *u.BoardName + "/" + *u.NodeTitle
		}
		for _, id := range []string{u.ConnectionID, u.BranchOutputID, u.ComponentID, u.AttributeID} {
			if id != "" {
				line += " " + id
			}
		}
		out[i] = line
	}
	return out
}

// wantSites fails the test unless the usages are the sites want, in any
// order, and in the order of their keys.
func wantSites(t *testing.T, what string, got []dependency.Usage, want ...string) {
	t.Helper()
	if !slices.IsSortedFunc(got, func(a, b dependency.Usage) int { return compareKeys(a.Key(), b.Key()) }) {
		t.Errorf("%s are not in the order of their keys: %v", what, sites(got))
	}
	have := sites(got)
	slices.Sort(have)
	slices.Sort(want)
	if !slices.Equal(have, want) {
		t.Errorf("%s are\n%q\nwant\n%q", what, have, want)
	}
}

// compareKeys orders the keys of usages as their lists do.
func compareKeys(a, b dependency.Key) int {
	for _, p := range [][2]ids.UUID{{a.Board, b.Board}, {a.Node, b.Node}, {a.Site, b.Site},
		{a.Attribute, b.Attribute}} {
		if c := strings.Compare(p[0].String(), p[1].String()); c != 0 {
			return c
		}
	}
	return 0
}

// TestVariableUsages checks that a variable's usages list the sites that read
// it apart from those that write it, each with its board and its node: a
// condition and a text that read it, a script that assigns it, and one that
// updates it, as += and x = x + 1 do, in both lists; but not a script that
// does not compile, which names it by a key that resolves to nothing.
//
// Verifies: FR-DEP-01/AC2
func TestVariableUsages(t *testing.T) {
	t.Parallel()
	w := newUsageWorld(t)
	h, o, p := w.s.main, w.owner, w.project
	wantSites(t, "met_elara's reads", usages(t, h, o, variableUsagesPath(p, w.metElara, "read"), 50),
		"node_text Harbour/Keeper", "connection_condition Harbour/Keeper "+w.gate)
	wantSites(t, "met_elara's writes", usages(t, h, o, variableUsagesPath(p, w.metElara, "write"), 50),
		"node_on_enter Lighthouse/Tower")
	wantSites(t, "gold's reads", usages(t, h, o, variableUsagesPath(p, w.gold, "read"), 50),
		"connection_on_traverse Harbour/Keeper "+w.gate, "node_on_enter Lighthouse/Tower",
		"component_description "+w.elara)
	wantSites(t, "gold's writes", usages(t, h, o, variableUsagesPath(p, w.gold, "write"), 50),
		"connection_on_traverse Harbour/Keeper "+w.gate, "node_on_enter Lighthouse/Tower")

	for _, tt := range []struct {
		name, path string
		status     int
		code       api.Code
	}{
		{"no access", variablePath(p, w.gold) + "/usages", http.StatusUnprocessableEntity, api.CodeValidationFailed},
		{"another access", variableUsagesPath(p, w.gold, "mention"), http.StatusUnprocessableEntity,
			api.CodeValidationFailed},
		{"a variable that the project lacks", variableUsagesPath(p, ids.Encode(ids.Variable, ids.New()), "read"),
			http.StatusNotFound, api.CodeNotFound},
		{"a component's ID", variableUsagesPath(p, w.elara, "read"), http.StatusBadRequest, api.CodeInvalidID},
		{"a cursor of another form", variableUsagesPath(p, w.gold, "read") + "&cursor=" +
			mustCursor(t, idKey{ID: ids.New().String()}), http.StatusBadRequest, api.CodeInvalidCursor},
		{"a cursor of a UUID in upper case", variableUsagesPath(p, w.gold, "read") + "&cursor=" +
			mustCursor(t, usageKey{Board: strings.ToUpper(ids.New().String()), Node: ids.New().String(),
				Site: ids.New().String(), Attribute: ids.UUID{}.String()}), http.StatusBadRequest,
			api.CodeInvalidCursor},
	} {
		t.Run(tt.name, func(t *testing.T) {
			wantCode(t, tt.name, call(h, http.MethodGet, tt.path, nil, o.header(false)), tt.status, tt.code)
		})
	}
}

// mustCursor returns the cursor of the key k.
func mustCursor[K any](t *testing.T, k K) string {
	t.Helper()
	c, err := api.EncodeCursor(k)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestComponentUsagesByPair checks the fixture of FR-DEP-01 AC4: elara.trust
// is written on Harbour and mira.trust is read on Lighthouse, and elara.mood
// is written on Harbour too. Elara's usages list no site of Lighthouse, whose
// read of mira.trust is Mira's alone, and the write of elara.mood is listed
// with its attribute, mood, never as a usage of elara.trust: a component's
// attribute is the pair of the component and the definition.
//
// Verifies: FR-DEP-01/AC4
func TestComponentUsagesByPair(t *testing.T) {
	t.Parallel()
	w := newUsageWorld(t)
	h, o, p := w.s.main, w.owner, w.project
	var elara []dependency.Usage
	for _, use := range []string{"speaker", "mention", "read", "write"} {
		elara = append(elara, usages(t, h, o, componentUsagesPath(p, w.elara, use), 50)...)
	}
	for _, u := range elara {
		if u.BoardID == w.lighthouse {
			t.Errorf("Elara's usages list a site of Lighthouse: %v", sites([]dependency.Usage{u}))
		}
	}
	writes := usages(t, h, o, componentUsagesPath(p, w.elara, "write"), 50)
	wantSites(t, "Elara's writes", writes, "node_on_enter Harbour/Keeper "+w.trust,
		"node_on_exit Harbour/Keeper "+w.mood)
	var trust []string
	for _, u := range writes {
		if u.AttributeID == w.trust {
			trust = append(trust, u.Site)
		}
	}
	if !slices.Equal(trust, []string{"node_on_enter"}) {
		t.Errorf("the writes of elara.trust are %v, want the on-enter script's alone, not elara.mood's", trust)
	}
	// The update elara.trust += 1 reads the attribute too, and the label
	// shows it.
	wantSites(t, "Elara's reads", usages(t, h, o, componentUsagesPath(p, w.elara, "read"), 50),
		"node_on_enter Harbour/Keeper "+w.trust, "connection_label Harbour/Keeper "+w.gate+" "+w.trust)
	wantSites(t, "Elara's speakers", usages(t, h, o, componentUsagesPath(p, w.elara, "speaker"), 50),
		"node_speaker Harbour/Keeper")
	wantSites(t, "Mira's reads", usages(t, h, o, componentUsagesPath(p, w.mira, "read"), 50),
		"branch_output_condition Lighthouse/Lamp "+w.lit+" "+w.trust)
	wantSites(t, "Mira's writes", usages(t, h, o, componentUsagesPath(p, w.mira, "write"), 50))
	wantCode(t, "another use", call(h, http.MethodGet, componentUsagesPath(p, w.elara, "access"), nil,
		o.header(false)), http.StatusUnprocessableEntity, api.CodeValidationFailed)
	wantCode(t, "a component that the project lacks", call(h, http.MethodGet,
		componentUsagesPath(p, newComponentID(), "read"), nil, o.header(false)), http.StatusNotFound, api.CodeNotFound)
}

// TestMentionUsages checks that a component's mentions are its usages: each
// text that mentions it, a node's, a connection's label, and another
// component's description, and none that mentions another component.
//
// Verifies: FR-TEXT-03/AC1
func TestMentionUsages(t *testing.T) {
	t.Parallel()
	w := newUsageWorld(t)
	h, o, p := w.s.main, w.owner, w.project
	wantSites(t, "Mira's mentions", usages(t, h, o, componentUsagesPath(p, w.mira, "mention"), 50),
		"node_text Harbour/Keeper", "component_description "+w.elara)
	wantSites(t, "Elara's mentions", usages(t, h, o, componentUsagesPath(p, w.elara, "mention"), 50))
	res := call(h, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{Type: "connection.update",
		Payload: map[string]any{"id": w.gate, "label": richText(mentionInline(w.elara), textInline(" waits"))}}),
		o.header(true))
	if res.status != http.StatusOK {
		t.Fatalf("change the label: %d %s", res.status, res.body)
	}
	wantSites(t, "Elara's mentions once the label mentions her", usages(t, h, o,
		componentUsagesPath(p, w.elara, "mention"), 50), "connection_label Harbour/Keeper "+w.gate)
	// The label no longer shows elara.trust.
	wantSites(t, "Elara's reads once the label mentions her", usages(t, h, o,
		componentUsagesPath(p, w.elara, "read"), 50), "node_on_enter Harbour/Keeper "+w.trust)
}

// TestTextReferenceSurvivesRename checks that a variable's rename leaves the
// texts that show it as they are stored, byte for byte, since they name it by
// its ID, and that they are still its usages, while the source of a condition
// that names it by its old key no longer compiles until it is rewritten
// (FR-VAR-03, plan item 47), and is not.
//
// Verifies: FR-TEXT-02/AC1
func TestTextReferenceSurvivesRename(t *testing.T) {
	t.Parallel()
	w := newUsageWorld(t)
	h, o, p := w.s.main, w.owner, w.project
	ctx, admin := context.Background(), w.admin
	text := func() string {
		rows, err := admin.Query(ctx, "SELECT text::text FROM node WHERE id = $1", uuidOf(t, w.keeper))
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var s string
		for rows.Next() {
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
		}
		return s
	}
	before := text()
	res := call(h, http.MethodPost, commandsPath(p), commandBatch(t, wireCommand{Type: "variable.rename",
		Payload: map[string]any{"id": w.metElara, "key": "met_keeper"}}), o.header(true))
	if res.status != http.StatusOK {
		t.Fatalf("rename the variable: %d %s", res.status, res.body)
	}
	if after := text(); after != before || !strings.Contains(after, w.metElara) {
		t.Errorf("the rename changed the text that shows the variable from %s to %s", before, after)
	}
	wantSites(t, "the renamed variable's reads", usages(t, h, o, variableUsagesPath(p, w.metElara, "read"), 50),
		"node_text Harbour/Keeper")
}

// TestUsagesTrash checks that the usages leave out the sites in the
// project's trash, those of a node deleted to it, and of a board, and list
// them again once they are restored, and that the usages of a variable in
// the trash are answered with 404 not_found.
func TestUsagesTrash(t *testing.T) {
	t.Parallel()
	w := newUsageWorld(t)
	h, o, p := w.s.main, w.owner, w.project
	apply := func(cmds ...wireCommand) {
		t.Helper()
		if res := call(h, http.MethodPost, commandsPath(p), commandBatch(t, cmds...), o.header(true)); res.status !=
			http.StatusOK {
			t.Fatalf("%s: %d %s", undoTypes(cmds), res.status, res.body)
		}
	}
	apply(wireCommand{Type: "node.delete", Payload: map[string]any{"id": w.keeper}})
	wantSites(t, "met_elara's reads once Keeper is in the trash", usages(t, h, o,
		variableUsagesPath(p, w.metElara, "read"), 50))
	apply(wireCommand{Type: "board.delete", Payload: map[string]any{"id": w.lighthouse, "confirm": true}})
	wantSites(t, "gold's writes once Keeper and Lighthouse are in the trash", usages(t, h, o,
		variableUsagesPath(p, w.gold, "write"), 50))
	wantSites(t, "gold's reads once Keeper and Lighthouse are in the trash", usages(t, h, o,
		variableUsagesPath(p, w.gold, "read"), 50), "component_description "+w.elara)
	apply(wireCommand{Type: "node.restore", Payload: map[string]any{"id": w.keeper}},
		wireCommand{Type: "board.restore", Payload: map[string]any{"id": w.lighthouse}})
	wantSites(t, "gold's writes once both are restored", usages(t, h, o, variableUsagesPath(p, w.gold, "write"), 50),
		"connection_on_traverse Harbour/Keeper "+w.gate, "node_on_enter Lighthouse/Tower")
	apply(wireCommand{Type: "variable.delete", Payload: map[string]any{"id": w.gold}})
	wantCode(t, "a variable in the trash", call(h, http.MethodGet, variableUsagesPath(p, w.gold, "read"), nil,
		o.header(false)), http.StatusNotFound, api.CodeNotFound)
	wantDrift(t, w.admin, p)
}

// TestUsagesPaged checks that a list of usages is read page by page, each
// page after the last's key, and gives the same usages, in the same order,
// whatever the pages' size.
func TestUsagesPaged(t *testing.T) {
	t.Parallel()
	w := newUsageWorld(t)
	h, o, p := w.s.main, w.owner, w.project
	for _, path := range []string{variableUsagesPath(p, w.gold, "read"), componentUsagesPath(p, w.elara, "read"),
		componentUsagesPath(p, w.elara, "write")} {
		whole := usages(t, h, o, path, 200)
		if len(whole) < 2 {
			t.Fatalf("%s lists %d usages, too few to page", path, len(whole))
		}
		if paged := usages(t, h, o, path, 1); !slices.Equal(sites(paged), sites(whole)) {
			t.Errorf("%s, a usage a page, is %v, want %v", path, sites(paged), sites(whole))
		}
	}
	wantDrift(t, w.admin, p)
}

// wantDrift fails the test unless the index of what the project's content
// names is what a recomputation of it from scratch gives (FR-DEP-01 AC1).
func wantDrift(t *testing.T, conn dependencytest.Querier, project string) {
	t.Helper()
	if drift := indexDrift(t, conn, project); len(drift) > 0 {
		t.Errorf("the index of what the project names is not what its content names: %q", drift)
	}
}

// indexDrift returns how the index of what the project's content names
// differs from a recomputation of it from scratch (dependencytest.Drift).
func indexDrift(t testing.TB, conn dependencytest.Querier, project string) []string {
	t.Helper()
	drift, err := dependencytest.Drift(context.Background(), conn, decode(t, ids.Project, project))
	if err != nil {
		t.Fatal(err)
	}
	return drift
}
