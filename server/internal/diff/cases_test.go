package diff

import (
	"strconv"
	"strings"
)

// The cases of the diff corpus (schema/diff-corpus/cases.json) and of its
// word-level text diffs (words.json). Each expected diff is written from PRD
// §7.4 and schema/diff-corpus/README.md, not from what Compare gives, which
// TestCorpus holds to them. A case's diff leaves out what complete fills in:
// its from and to, as the sets' manifests have them, its schemaVersion, a
// list or a count that is empty or 0, and truncated, which no case is.

// corpusCase is a case of the corpus: its name, what it is about, the names of
// its from and to sets, and its expected diff, in which ids replaces each of
// the sets' IDs' placeholders, as <N1>, and refs each board's, as <@B1>.
type corpusCase struct {
	name, description, from, to, diff string
}

// ids replaces the placeholders of the expected diffs' IDs, and of their
// boards, with the IDs and the boards that the sets have.
var ids = strings.NewReplacer(
	`<@B1>`, `{"id": "<B1>", "name": "Harbour Intro\u2028Part one", "slug": "harbour-intro"}`,
	`<@B2>`, `{"id": "<B2>", "name": "Keeper's \"Gate\" \\ back", "slug": "keepers-gate"}`,
	`<@B3>`, `{"id": "<B3>", "name": "Empty", "slug": "empty"}`,
	`<@B4>`, `{"id": "<B4>", "name": "Lighthouse", "slug": "lighthouse"}`,
)

// refs replaces the placeholders of the IDs.
var refs = strings.NewReplacer(
	"<B1>", b1, "<B2>", b2, "<B3>", b3, "<B4>", b4,
	"<N1>", n1, "<N2>", n2, "<N3>", n3, "<N4>", n4, "<N5>", n5, "<N10>", n10, "<N11>", n11,
	"<N20>", n20, "<N21>", n21, "<N30>", n30,
	"<Q1>", q1, "<Q2>", q2, "<Q3>", q3, "<Q4>", q4,
	"<K1>", k1, "<K2>", k2, "<K3>", k3, "<K20>", k20,
	"<V1>", v1, "<V2>", v2, "<V3>", v3, "<V6>", v6, "<V7>", v7, "<V9>", v9, "<V10>", v10,
	"<T1>", t1, "<A1>", a1, "<A2>", a2, "<A3>", a3, "<C2>", c2, "<C3>", c3,
)

// expand returns the expected diff of c with its placeholders replaced.
func (c corpusCase) expand() string {
	return refs.Replace(ids.Replace(c.diff))
}

var corpusCases = []corpusCase{
	{"identical", "Two snapshots of the same content have no change, whatever their manifests say.", "base", "base",
		`{}`},
	{"draft", "The draft, as a draft pull fetches it, of the same content as a release has no change, and is named " +
		"as the draft, with its content hash.", "base", "draft", `{}`},
	{"renamed-variable", "A renamed variable is modified on its key, not removed and added, and each expression " +
		"and text that names it is modified, the expressions naming what they named (FR-SNAP-04 AC1).", "base",
		"renamed", `{
		"boards": [
			{"board": <@B1>, "changes": [
				{"branch": "<N2>", "change": "modified", "fields": [{"after": "met_lara and elara.trust >= 2 and elara.mood == \"tense\"", "before": "met_elara and elara.trust >= 2 and elara.mood == \"tense\"", "field": "condition", "refsChanged": false}], "id": "<Q2>", "kind": "branch_output"}
			]},
			{"board": <@B2>, "changes": [
				{"change": "modified", "fields": [{"after": "Say {gate_word}, met: {met_lara}", "before": "Say {gate_word}, met: {met_elara}", "field": "text", "words": [{"op": "equal", "text": "Say {gate_word}, met: "}, {"op": "delete", "text": "{met_elara}"}, {"op": "insert", "text": "{met_lara}"}]}], "id": "<N10>", "kind": "node", "preview": "Say {gate_word}, met: {met_lara}", "type": "element"}
			]}
		],
		"project": {"variables": [
			{"change": "modified", "fields": [{"after": "met_lara", "before": "met_elara", "field": "key"}], "id": "<V1>", "key": "met_lara", "kind": "variable", "type": "bool"}
		]},
		"summary": {"modified": 3, "kinds": {"branch_output": {"modified": 1}, "node": {"modified": 1}, "variable": {"modified": 1}}}
	}`},
	{"added", "Entities that only the to side has are added, named by what their kind shows of them, each node and " +
		"connection on its board.", "base", "added", `{
		"boards": [
			{"board": <@B3>, "changes": [
				{"change": "added", "id": "<N20>", "kind": "node", "preview": "Gulls cry.", "title": "Quay", "type": "element"},
				{"change": "added", "id": "<N21>", "kind": "node", "preview": "Silence.", "type": "element"},
				{"change": "added", "from": "<N20>", "id": "<K20>", "kind": "connection", "label": "Listen", "to": "<N21>"}
			]}
		],
		"project": {
			"components": [{"change": "added", "id": "<C3>", "key": "boatman", "kind": "component", "name": "Boatman"}],
			"variables": [{"change": "added", "id": "<V10>", "key": "tide", "kind": "variable", "type": "int"}]
		},
		"summary": {"added": 5, "kinds": {"component": {"added": 1}, "connection": {"added": 1}, "node": {"added": 2}, "variable": {"added": 1}}}
	}`},
	{"removed", "Entities that only the from side has are removed, named as the from side has them, each node and " +
		"connection on the board that it was on.", "added", "base", `{
		"boards": [
			{"board": <@B3>, "changes": [
				{"change": "removed", "id": "<N20>", "kind": "node", "preview": "Gulls cry.", "title": "Quay", "type": "element"},
				{"change": "removed", "id": "<N21>", "kind": "node", "preview": "Silence.", "type": "element"},
				{"change": "removed", "from": "<N20>", "id": "<K20>", "kind": "connection", "label": "Listen", "to": "<N21>"}
			]}
		],
		"project": {
			"components": [{"change": "removed", "id": "<C3>", "key": "boatman", "kind": "component", "name": "Boatman"}],
			"variables": [{"change": "removed", "id": "<V10>", "key": "tide", "kind": "variable", "type": "int"}]
		},
		"summary": {"removed": 5, "kinds": {"component": {"removed": 1}, "connection": {"removed": 1}, "node": {"removed": 2}, "variable": {"removed": 1}}}
	}`},
	{"moved", "Nodes and a connection that the two sides have on different boards are moved, with the board that " +
		"each was on and the one that it is on, listed on the latter, not removed and added; a moved node whose " +
		"fields changed too lists them (FR-SNAP-04 AC2).", "added", "moved", `{
		"boards": [
			{"board": <@B2>, "changes": [
				{"change": "moved", "fromBoard": <@B3>, "id": "<N20>", "kind": "node", "preview": "Gulls cry.", "title": "Quay", "toBoard": <@B2>, "type": "element"},
				{"change": "moved", "fields": [{"after": "Hush", "field": "title"}], "fromBoard": <@B3>, "id": "<N21>", "kind": "node", "preview": "Silence.", "title": "Hush", "toBoard": <@B2>, "type": "element"},
				{"change": "moved", "from": "<N20>", "fromBoard": <@B3>, "id": "<K20>", "kind": "connection", "label": "Listen", "to": "<N21>", "toBoard": <@B2>}
			]}
		],
		"summary": {"moved": 3, "kinds": {"connection": {"moved": 1}, "node": {"moved": 2}}}
	}`},
	{"rehomed", "A board that only one side has is added or removed, but a node or a connection of it that the " +
		"other side has on another board is moved, onto the added board or off the removed one, not added or " +
		"removed with it.", "added", "rehomed", `{
		"boards": [
			{"board": <@B3>, "change": "removed"},
			{"board": <@B4>, "change": "added", "changes": [
				{"change": "moved", "fromBoard": <@B3>, "id": "<N20>", "kind": "node", "preview": "Gulls cry.", "title": "Quay", "toBoard": <@B4>, "type": "element"},
				{"change": "moved", "fromBoard": <@B3>, "id": "<N21>", "kind": "node", "preview": "Silence.", "toBoard": <@B4>, "type": "element"},
				{"change": "moved", "from": "<N20>", "fromBoard": <@B3>, "id": "<K20>", "kind": "connection", "label": "Listen", "to": "<N21>", "toBoard": <@B4>}
			]}
		],
		"summary": {"added": 1, "moved": 3, "removed": 1, "kinds": {"board": {"added": 1, "removed": 1}, "connection": {"moved": 1}, "node": {"moved": 2}}}
	}`},
	{"cosmetic", "Nodes whose position alone changed are modified and cosmetic, and counted as cosmetic.", "base",
		"cosmetic", `{
		"boards": [
			{"board": <@B1>, "changes": [
				{"change": "modified", "cosmetic": true, "fields": [{"after": {"x": -999999000, "y": 40}, "before": {"x": -1000000000, "y": 0.01}, "field": "position"}], "id": "<N3>", "kind": "node", "preview": "The end <3 & \"bye\"", "type": "element"},
				{"change": "modified", "cosmetic": true, "fields": [{"after": {"x": 120.5, "y": 0}, "before": {"x": 0, "y": 0}, "field": "position"}], "id": "<N5>", "kind": "node", "type": "element"}
			]}
		],
		"summary": {"cosmetic": 2, "kinds": {"node": {"cosmetic": 2}}}
	}`},
	{"texts", "A changed rich text, an element's text or a connection's label, has its plain texts as before and " +
		"after, and their word-level diff.", "base", "texts", `{
		"boards": [
			{"board": <@B1>, "changes": [
				{"change": "modified", "fields": [{"after": "The very end <3 & \"farewell\"", "before": "The end <3 & \"bye\"", "field": "text", "words": [{"op": "equal", "text": "The "}, {"op": "insert", "text": "very "}, {"op": "equal", "text": "end <3 & "}, {"op": "delete", "text": "\"bye\""}, {"op": "insert", "text": "\"farewell\""}]}], "id": "<N3>", "kind": "node", "preview": "The very end <3 & \"farewell\"", "type": "element"},
				{"change": "modified", "fields": [{"after": "Leave quietly", "before": "Leave", "field": "label", "words": [{"op": "equal", "text": "Leave"}, {"op": "insert", "text": " quietly"}]}], "from": "<N1>", "id": "<K2>", "kind": "connection", "label": "Leave quietly", "to": "<N3>"}
			]}
		],
		"summary": {"modified": 2, "kinds": {"connection": {"modified": 1}, "node": {"modified": 1}}}
	}`},
	{"expressions", "A changed expression, added, removed, or rewritten, has its sources as before and after, and " +
		"whether the variables, attributes, and nodes that it names changed.", "base", "expressions", `{
		"boards": [
			{"board": <@B1>, "changes": [
				{"change": "modified", "fields": [{"after": "luck = luck * 2", "before": "luck = luck + 0.0000001", "field": "onEnter", "refsChanged": false}], "id": "<N2>", "kind": "node", "type": "branch"},
				{"branch": "<N2>", "change": "modified", "fields": [{"after": "coins > 0", "before": "luck > 0.5", "field": "condition", "refsChanged": true}], "id": "<Q1>", "kind": "branch_output"},
				{"change": "modified", "fields": [{"after": "visits_here < 5 and not visited(node(\"<N3>\"))", "before": "visits_here < 3 and not visited(node(\"<N3>\"))", "field": "condition", "refsChanged": false}], "from": "<N1>", "id": "<K1>", "kind": "connection", "label": "Go on", "to": "<N2>"},
				{"change": "modified", "fields": [{"before": "visits_here = 0\nsignal(\"bell\")", "field": "onTraverse", "refsChanged": true}], "from": "<N1>", "id": "<K2>", "kind": "connection", "label": "Leave", "to": "<N3>"},
				{"change": "modified", "fields": [{"after": "coins > 2", "field": "condition", "refsChanged": true}], "from": "<N1>", "id": "<K3>", "kind": "connection", "label": "Jump", "to": "<N4>"}
			]}
		],
		"summary": {"modified": 5, "kinds": {"branch_output": {"modified": 1}, "connection": {"modified": 3}, "node": {"modified": 1}}}
	}`},
	{"outputs", "A branch output added before others is added, and those whose places moved on are modified on " +
		"their order; the branch itself, whose own fields did not change, is not.", "base", "outputs", `{
		"boards": [
			{"board": <@B1>, "changes": [
				{"branch": "<N2>", "change": "modified", "fields": [{"after": 2, "before": 1, "field": "order"}], "id": "<Q1>", "kind": "branch_output"},
				{"branch": "<N2>", "change": "modified", "fields": [{"after": 3, "before": 2, "field": "order"}], "id": "<Q3>", "kind": "branch_output"},
				{"branch": "<N2>", "change": "added", "id": "<Q4>", "kind": "branch_output"}
			]}
		],
		"summary": {"added": 1, "modified": 2, "kinds": {"branch_output": {"added": 1, "modified": 2}}}
	}`},
	{"fields", "Changed fields of every kind of entity have their values as their files hold them, or no value " +
		"where a side does not have the field: a retyped variable, a description added, a resetOnEnter taken " +
		"away, names, options, an attribute's value, tags, a title, a board's name, and the project's.", "base",
		"fields", `{
		"boards": [
			{"board": <@B1>, "changes": [
				{"change": "modified", "fields": [{"after": ["finale"], "field": "tags"}], "id": "<N3>", "kind": "node", "preview": "The end <3 & \"bye\"", "type": "element"},
				{"change": "modified", "fields": [{"after": "To the keeper's gate", "before": "To the gate", "field": "title"}], "id": "<N4>", "kind": "node", "title": "To the keeper's gate", "type": "jumper"}
			]},
			{"board": {"id": "<B3>", "name": "Empty quay", "slug": "empty"}, "change": "modified", "fields": [{"after": "Empty quay", "before": "Empty", "field": "name"}]}
		],
		"project": {
			"attributes": [
				{"change": "modified", "componentType": "<T1>", "fields": [{"after": ["tense", "calm", "angry"], "before": ["tense", "calm"], "field": "options"}], "id": "<A2>", "key": "mood", "kind": "attribute", "name": "Mood"},
				{"change": "modified", "componentType": "<T1>", "fields": [{"after": "Life story", "before": "Biography", "field": "name"}], "id": "<A3>", "key": "bio", "kind": "attribute", "name": "Life story"}
			],
			"componentTypes": [
				{"change": "modified", "fields": [{"after": "Characters", "before": "Cast", "field": "name"}], "id": "<T1>", "key": "cast", "kind": "component_type", "name": "Characters"}
			],
			"components": [
				{"change": "modified", "fields": [{"after": 6, "attribute": "<A1>", "before": 5, "field": "attributes"}, {"after": "Mira the Pilot", "before": "Mira", "field": "name"}], "id": "<C2>", "key": "mira", "kind": "component", "name": "Mira the Pilot"}
			],
			"fields": [{"after": "Harbour Tales", "before": "Harbour <Tales> & \"Sea\" 🐚", "field": "name"}],
			"variables": [
				{"change": "modified", "fields": [{"after": "Fortune's favour.", "field": "description"}], "id": "<V2>", "key": "luck", "kind": "variable", "type": "float"},
				{"change": "modified", "fields": [{"after": 7, "before": 1e+21, "field": "default"}, {"after": "int", "before": "float", "field": "type"}], "id": "<V3>", "key": "big", "kind": "variable", "type": "int"},
				{"change": "modified", "fields": [{"before": true, "field": "resetOnEnter"}], "id": "<V7>", "key": "visits_here", "kind": "variable", "type": "int"}
			]
		},
		"summary": {"modified": 11, "kinds": {"attribute": {"modified": 2}, "board": {"modified": 1}, "component": {"modified": 1}, "component_type": {"modified": 1}, "node": {"modified": 2}, "project": {"modified": 1}, "variable": {"modified": 3}}}
	}`},
	{"boards", "A board that only one side has is added or removed with what it holds, and one whose slug, and so " +
		"its file's path, changed is modified, with nothing that it holds changed.", "base", "boards", `{
		"boards": [
			{"board": {"id": "<B2>", "name": "Keeper's \"Gate\" \\ back", "slug": "keepers-gate-east"}, "change": "modified", "fields": [{"after": "keepers-gate-east", "before": "keepers-gate", "field": "slug"}]},
			{"board": <@B3>, "change": "removed"},
			{"board": {"id": "<B4>", "name": "Lighthouse", "slug": "lighthouse"}, "change": "added", "changes": [
				{"change": "added", "id": "<N30>", "kind": "node", "preview": "The lamp burns.", "type": "element"}
			]}
		],
		"summary": {"added": 2, "modified": 1, "removed": 1, "kinds": {"board": {"added": 1, "modified": 1, "removed": 1}, "node": {"added": 1}}}
	}`},
	{"ignored", "Keys that the format does not define, which a consumer ignores, in an object of every kind of " +
		"entity and at any depth, one that differs from a key that the format defines only by its case among " +
		"them, and optional fields written with the value that the format leaves out, a board variable's " +
		"resetOnEnter of false and an element's tags of [], are no change.", "base", "ignored", `{}`},
}

// wordsCase is a case of the word-level text diff: two plain texts, and the
// runs that their diff is.
type wordsCase struct {
	name, before, after string
	words               []WordRun
}

// run returns a run of the operation op of the text.
func run(op, text string) WordRun { return WordRun{Op: op, Text: text} }

// wordsCases are the cases of words.json, of each step of the diff (Words).
var wordsCases = []wordsCase{
	{"same", "The tide turns.", "The tide turns.", []WordRun{run(Equal, "The tide turns.")}},
	{"from-nothing", "", "Gulls cry.", []WordRun{run(Insert, "Gulls cry.")}},
	{"to-nothing", "Gulls cry.", "", []WordRun{run(Delete, "Gulls cry.")}},
	{"appended", "Leave", "Leave quietly", []WordRun{run(Equal, "Leave"), run(Insert, " quietly")}},
	{"word-replaced", "the cat sat", "the dog sat",
		[]WordRun{run(Equal, "the "), run(Delete, "cat"), run(Insert, "dog"), run(Equal, " sat")}},
	{"words-inserted", `The end <3 & "bye"`, `The very end <3 & "farewell"`, []WordRun{run(Equal, "The "),
		run(Insert, "very "), run(Equal, "end <3 & "), run(Delete, `"bye"`), run(Insert, `"farewell"`)}},
	{"white-space-changed", "a  b", "a b", []WordRun{run(Equal, "a"), run(Delete, "  "), run(Insert, " "),
		run(Equal, "b")}},
	{"phrase-rewritten", "one two three", "uno dos three",
		[]WordRun{run(Delete, "one two"), run(Insert, "uno dos"), run(Equal, " three")}},
	{"swapped", "a b", "b a", []WordRun{run(Delete, "a "), run(Equal, "b"), run(Insert, " a")}},
	{"repeated", "a b a", "a", []WordRun{run(Equal, "a"), run(Delete, " b a")}},
	{"lines", "First line\nsecond line", "First line\nthird line",
		[]WordRun{run(Equal, "First line\n"), run(Delete, "second"), run(Insert, "third"), run(Equal, " line")}},
	{"unicode-white-space", "a\u00a0b\u3000c", "a\u00a0x\u3000c",
		[]WordRun{run(Equal, "a\u00a0"), run(Delete, "b"), run(Insert, "x"), run(Equal, "\u3000c")}},
	{"no-break-is-no-space", "a\ufeffb", "a\ufeffc", []WordRun{run(Delete, "a\ufeffb"), run(Insert, "a\ufeffc")}},
	{"too-many-edits", "start " + numbered("a", 300) + " end", "start " + numbered("b", 300) + " end",
		[]WordRun{run(Equal, "start "), run(Delete, numbered("a", 300)), run(Insert, numbered("b", 300)),
			run(Equal, " end")}},
}

// numbered returns n words, each the prefix and a number from 0 to n − 1,
// separated by spaces, which two prefixes tell apart by more than MaxEdits
// tokens deleted and inserted for n of 300: 600, though the texts share
// every space.
func numbered(prefix string, n int) string {
	words := make([]string, n)
	for i := range words {
		words[i] = prefix + strconv.Itoa(i)
	}
	return strings.Join(words, " ")
}
