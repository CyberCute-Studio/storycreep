package published

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemagen"
)

// Board is a snapshot's file of one board, boards/<slug>.json (§6.3, §6.5):
// the board, its nodes, and its connections.
type Board struct {
	Board       BoardInfo    `json:"board"`
	Connections []Connection `json:"connections" doc:"The board's connections, sorted by ID."`
	Nodes       Nodes        `json:"nodes" doc:"The board's nodes, sorted by ID."`
	// SchemaVersion is SchemaVersion.
	SchemaVersion Int `json:"schemaVersion" const:"1" doc:"The version of the published format, 1."`
}

// MarshalJSON writes the board's file, its nodes and its connections [] when
// it has none, or fails on a node or a value that is not set.
func (b Board) MarshalJSON() ([]byte, error) {
	type plain Board
	return marshalFile("a board's file", plain(b))
}

// JSONSchemaDoc describes a board's file (schemagen.Documented).
func (Board) JSONSchemaDoc() string {
	return "A snapshot's file of one board, boards/<slug>.json: the board, its nodes, and its connections " +
		"(PRD §6.3, §6.5)."
}

// BoardInfo is a board, as its file holds it.
type BoardInfo struct {
	EntryNodeID string `json:"entryNodeId,omitempty" id:"node" doc:"The board's entry node. Without it, the board has none, which only a draft may lack."`
	ID          string `json:"id" id:"board"`
	Name        string `json:"name"`
	Slug        string `json:"slug" pattern:"^[a-z0-9]+(?:-[a-z0-9]+)*$" maxLength:"64"`
}

// JSONSchemaDoc describes a board (schemagen.Documented).
func (BoardInfo) JSONSchemaDoc() string {
	return "The board: its ID, name, slug, and entry node, as the manifest's index has them."
}

// The types of a node (FR-EDIT-02).
const (
	NodeElement = "element"
	NodeBranch  = "branch"
	NodeJumper  = "jumper"
)

// Node is a node of a board: an *Element, a *Branch, or a *Jumper.
type Node interface {
	nodeType() string
}

// Nodes are a board's nodes, of any type.
type Nodes []Node

// UnmarshalJSON reads a board's nodes, each by its type.
func (ns *Nodes) UnmarshalJSON(data []byte) error {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return err
	}
	*ns = make(Nodes, len(raws))
	for i, raw := range raws {
		n, err := unmarshalVariant(raw, "type", map[string]func() Node{
			NodeElement: func() Node { return &Element{} },
			NodeBranch:  func() Node { return &Branch{} },
			NodeJumper:  func() Node { return &Jumper{} },
		})
		if err != nil {
			return fmt.Errorf("node %d: %w", i, err)
		}
		(*ns)[i] = n
	}
	return nil
}

// Element is a line of dialogue or of narration (FR-EDIT-02).
type Element struct {
	Cover    string   `json:"cover,omitempty" id:"asset" doc:"The element's cover image. Its file is the snapshot's assets/<the ID's body>.<its extension> when the snapshot includes assets. Without it, the element has none."`
	ID       string   `json:"id" id:"node"`
	IsEnding bool     `json:"isEnding" doc:"Whether the flow ends at the element (PRD §5.8.5)."`
	Kind     string   `json:"kind" enum:"dialogue,narration" doc:"Dialogue, which its speaker says, or narration."`
	OnEnter  *Script  `json:"onEnter,omitempty" doc:"The script that runs when the flow enters the element. Without it, none."`
	OnExit   *Script  `json:"onExit,omitempty" doc:"The script that runs when the flow leaves the element, by a connection or by ending there. Without it, none."`
	Position Position `json:"position"`
	Speaker  string   `json:"speaker,omitempty" id:"component" doc:"The component that says a line of dialogue. Without it, the element has none."`
	Tags     []string `json:"tags,omitempty" maxItems:"32" minLength:"1" maxLength:"50" doc:"The element's tags, as their author wrote them, in their order, no two the same but for their case. Without them, none."`
	Text     *Text    `json:"text,omitempty" doc:"The element's text. Without it, the element's text is empty."`
	Title    string   `json:"title,omitempty" minLength:"1" maxLength:"200" doc:"The element's title. Without it, the element has none."`
}

// Branch picks the first of its outputs whose condition holds (FR-EDIT-04).
type Branch struct {
	ID       string         `json:"id" id:"node"`
	OnEnter  *Script        `json:"onEnter,omitempty" doc:"The script that runs when the flow enters the branch. Without it, none."`
	OnExit   *Script        `json:"onExit,omitempty" doc:"The script that runs when the flow leaves the branch. Without it, none."`
	Outputs  []BranchOutput `json:"outputs" doc:"The branch's outputs, in their author's order, which is the order in which their conditions are evaluated, [] for none."`
	Position Position       `json:"position"`
	Title    string         `json:"title,omitempty" minLength:"1" maxLength:"200" doc:"The branch's title. Without it, the branch has none."`
}

// Jumper continues the flow at its target, as if the flow had reached the
// target itself (§5.8.5).
type Jumper struct {
	ID       string        `json:"id" id:"node"`
	Position Position      `json:"position"`
	Target   *JumperTarget `json:"target,omitempty" doc:"The node that the jumper continues the flow at. Without it, the jumper has none, which only a draft may lack."`
	Title    string        `json:"title,omitempty" minLength:"1" maxLength:"200" doc:"The jumper's title. Without it, the jumper has none."`
}

func (*Element) nodeType() string { return NodeElement }
func (*Branch) nodeType() string  { return NodeBranch }
func (*Jumper) nodeType() string  { return NodeJumper }

// JSONSchemaDoc describes an element (schemagen.Documented).
func (Element) JSONSchemaDoc() string {
	return "An element: a line of dialogue or of narration (FR-EDIT-02)."
}

// JSONSchemaDoc describes a branch (schemagen.Documented).
func (Branch) JSONSchemaDoc() string {
	return "A branch, which picks the first of its outputs whose condition holds (FR-EDIT-04)."
}

// JSONSchemaDoc describes a jumper (schemagen.Documented).
func (Jumper) JSONSchemaDoc() string {
	return "A jumper, which continues the flow at its target, as if the flow had reached the target itself " +
		"(PRD §5.8.5)."
}

// MarshalJSON writes the element with its type.
func (n Element) MarshalJSON() ([]byte, error) {
	type plain Element
	return marshalVariant("type", NodeElement, plain(n))
}

// MarshalJSON writes the branch with its type.
func (n Branch) MarshalJSON() ([]byte, error) {
	type plain Branch
	return marshalVariant("type", NodeBranch, plain(n))
}

// MarshalJSON writes the jumper with its type.
func (n Jumper) MarshalJSON() ([]byte, error) {
	type plain Jumper
	return marshalVariant("type", NodeJumper, plain(n))
}

// Position is where a node is on its board's canvas, to 2 decimal places
// (§6.2).
type Position struct {
	X float64 `json:"x" minimum:"-1000000000" maximum:"1000000000" doc:"Rightward, in units of the canvas."`
	Y float64 `json:"y" minimum:"-1000000000" maximum:"1000000000" doc:"Downward, in units of the canvas."`
}

// JSONSchemaDoc describes a position (schemagen.Documented).
func (Position) JSONSchemaDoc() string {
	return "Where the node is on its board's canvas, to 2 decimal places (PRD §6.2)."
}

// JumperTarget is the node that a jumper continues the flow at, and its
// board.
type JumperTarget struct {
	BoardID string `json:"boardId" id:"board" doc:"The node's board, which may be the jumper's own."`
	NodeID  string `json:"nodeId" id:"node" doc:"A node of the board, which is no jumper."`
}

// JSONSchemaDoc describes a jumper's target (schemagen.Documented).
func (JumperTarget) JSONSchemaDoc() string {
	return "The node that a jumper continues the flow at, and its board."
}

// The kinds of a branch's output (FR-EDIT-04).
const (
	OutputIf     = "if"
	OutputElseIf = "elseif"
	OutputElse   = "else"
)

// BranchOutput is an output of a branch: if or elseif, with a condition, or
// else, without one (FR-EDIT-04). The connections that leave it name it as
// their port.
type BranchOutput struct {
	Condition *Condition `json:"condition,omitempty" when:"kind=if|elseif" required:"true" doc:"The condition that picks the output."`
	ID        string     `json:"id" id:"branch_output"`
	Kind      string     `json:"kind" enum:"if,elseif,else"`
	Order     Int        `json:"order" minimum:"0" maximum:"9007199254740991" doc:"The output's place among its branch's, from 0."`
}

// JSONSchemaDoc describes a branch's output (schemagen.Documented).
func (BranchOutput) JSONSchemaDoc() string {
	return "An output of a branch: if or elseif, with a condition, or else, without one (FR-EDIT-04). The " +
		"connections that leave it name it as their fromPort."
}

// Connection is a connection of a board, from a node's port to a node of the
// board (FR-EDIT-03).
type Connection struct {
	Condition  *Condition `json:"condition,omitempty" doc:"The condition that makes the connection a choice. Without it, the connection always is."`
	From       string     `json:"from" id:"node" doc:"The node that the connection leaves: an element, or a branch by one of its outputs."`
	FromPort   string     `json:"fromPort" pattern:"^(?:out|bo_[01234567][0123456789ABCDEFGHJKMNPQRSTVWXYZ]{25})$" doc:"The port that the connection leaves its node by: an element's out, or the ID of a branch's output."`
	ID         string     `json:"id" id:"connection"`
	Label      *Label     `json:"label,omitempty" doc:"The connection's label, the text of its choice. Without it, the connection has none."`
	OnTraverse *Script    `json:"onTraverse,omitempty" doc:"The script that runs when the flow traverses the connection. Without it, none."`
	Order      Int        `json:"order" minimum:"0" maximum:"9007199254740991" doc:"The connection's place among those that leave its port, from 0, the order of its choice."`
	To         string     `json:"to" id:"node" doc:"The node of the board that the connection leads to."`
}

// JSONSchemaDoc describes a connection (schemagen.Documented).
func (Connection) JSONSchemaDoc() string {
	return "A connection of a board, from a node's port to a node of the board (FR-EDIT-03)."
}

// Text is an element's text: its segments and its preview (§6.5).
type Text struct {
	// Preview is the text's plain text as one line, cut to 100 characters
	// (richtext.Preview).
	Preview  string            `json:"preview"`
	Segments richtext.Segments `json:"segments"`
}

// MarshalJSON writes the text, or fails on one without segments, which a file
// leaves out (atLeastOne).
func (t Text) MarshalJSON() ([]byte, error) {
	if len(t.Segments) == 0 {
		return nil, errEmptyText
	}
	type plain Text
	return schemagen.Marshal(plain(t))
}

// errEmptyText is the error of writing a text or a label without segments.
var errEmptyText = errors.New("published: a text or a label has no segments, where an empty one is left out")

// JSONSchemaDoc describes a text (schemagen.Documented).
func (Text) JSONSchemaDoc() string {
	return "An element's text: its segments, at least one, and its preview (PRD §6.5)."
}

// JSONSchema gives the schemas the definition of a text, whose segments are
// at least one (schemagen.Provider).
func (Text) JSONSchema(g *schemagen.Generator) *schemagen.Schema {
	return g.Object(map[string]*schemagen.Schema{
		"preview": {Type: "string", Description: "The text's plain text as one line, cut after 100 characters " +
			"with an ellipsis, as schema/rich-text/README.md says."},
		"segments": atLeastOne(g),
	}, "preview", "segments")
}

// Label is a connection's label, the text of its choice: its segments.
type Label struct {
	Segments richtext.Segments `json:"segments"`
}

// MarshalJSON writes the label, or fails on one without segments, which a
// file leaves out (atLeastOne).
func (l Label) MarshalJSON() ([]byte, error) {
	if len(l.Segments) == 0 {
		return nil, errEmptyText
	}
	type plain Label
	return schemagen.Marshal(plain(l))
}

// JSONSchemaDoc describes a label (schemagen.Documented).
func (Label) JSONSchemaDoc() string {
	return "A connection's label, the text of its choice: its segments, at least one."
}

// JSONSchema gives the schemas the definition of a label, whose segments are
// at least one (schemagen.Provider).
func (Label) JSONSchema(g *schemagen.Generator) *schemagen.Schema {
	return g.Object(map[string]*schemagen.Schema{"segments": atLeastOne(g)}, "segments")
}

// atLeastOne returns the schema of a rich text's segments, of which a text
// or a label that is there has at least one: an empty one is left out.
func atLeastOne(g *schemagen.Generator) *schemagen.Schema {
	return &schemagen.Schema{Type: "array", Items: g.Ref(segmentType), MinItems: schemagen.Int(1),
		Description: "The rich text's segments, in order, at least one: an empty rich text is left out."}
}

// Condition is a condition: its source and its AST, an expression (§5.8.5,
// §5.8.8).
type Condition struct {
	AST    expr.Node
	Source string
}

// Script is a script: its source and its AST, a script node (§5.8.5,
// §5.8.8).
type Script struct {
	AST    expr.Node
	Source string
}

// expressionJSON is a condition or a script as the published format holds
// it.
type expressionJSON struct {
	AST    json.RawMessage `json:"ast"`
	Source string          `json:"source"`
}

// JSONSchema gives the schemas the definition of a condition
// (schemagen.Provider).
func (Condition) JSONSchema(g *schemagen.Generator) *schemagen.Schema {
	return expressionSchema(g, expr.ExpressionSchema(g), "A condition: its source, and its AST, an expression, "+
		"whose names are resolved to the IDs of what they name (PRD §5.8.5, §5.8.8).")
}

// JSONSchema gives the schemas the definition of a script
// (schemagen.Provider).
func (Script) JSONSchema(g *schemagen.Generator) *schemagen.Schema {
	return expressionSchema(g, expr.ScriptSchema(g), "A script: its source, and its AST, a script node, whose "+
		"names are resolved to the IDs of what they name (PRD §5.8.5, §5.8.8).")
}

func expressionSchema(g *schemagen.Generator, ast *schemagen.Schema, description string) *schemagen.Schema {
	return schemagen.Describe(g.Object(map[string]*schemagen.Schema{
		"ast": ast,
		"source": {Type: "string", Description: "The source, as its author wrote it, for debugging: a runtime " +
			"evaluates the AST, and parses no source (PRD §6.1)."},
	}, "ast", "source"), description)
}

// MarshalJSON writes the condition, its AST as expr.Encode writes it, or
// fails on an AST that a file does not hold (encodeAST).
func (c Condition) MarshalJSON() ([]byte, error) {
	return marshalExpression(c.AST, c.Source, expr.ContextCondition)
}

// MarshalJSON writes the script, its AST as expr.Encode writes it, or fails
// on an AST that a file does not hold (encodeAST).
func (s Script) MarshalJSON() ([]byte, error) {
	return marshalExpression(s.AST, s.Source, expr.ContextScript)
}

func marshalExpression(ast expr.Node, source string, ctx expr.Context) ([]byte, error) {
	data, err := encodeAST(ast, ctx)
	if err != nil {
		return nil, err
	}
	return schemagen.Marshal(expressionJSON{AST: data, Source: source})
}

// encodeAST returns the JSON that expr.Encode writes of ast, the AST of a
// ctx, or fails on one that is not set, or that expr.Decode does not read
// back as a ctx's, as a file holds it: one whose names are not resolved, as
// expr.Parse returns it, or a script where ctx is a condition's.
func encodeAST(ast expr.Node, ctx expr.Context) (json.RawMessage, error) {
	if ast == nil {
		return nil, fmt.Errorf("published: the AST of a %s is not set", ctx)
	}
	data, err := expr.Encode(ast)
	if err != nil {
		return nil, err
	}
	if _, err := expr.Decode(data, ctx); err != nil {
		return nil, fmt.Errorf("published: the AST of a %s is not one that a file holds: %w", ctx, err)
	}
	return data, nil
}

// UnmarshalJSON reads a condition, its AST as expr.Decode reads it.
func (c *Condition) UnmarshalJSON(data []byte) error {
	ast, source, err := unmarshalExpression(data, expr.ContextCondition)
	*c = Condition{AST: ast, Source: source}
	return err
}

// UnmarshalJSON reads a script, its AST as expr.Decode reads it.
func (s *Script) UnmarshalJSON(data []byte) error {
	ast, source, err := unmarshalExpression(data, expr.ContextScript)
	*s = Script{AST: ast, Source: source}
	return err
}

func unmarshalExpression(data []byte, ctx expr.Context) (expr.Node, string, error) {
	var w expressionJSON
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, "", err
	}
	if w.AST == nil {
		return nil, "", fmt.Errorf("published: a %s has no ast", ctx)
	}
	ast, err := expr.Decode(w.AST, ctx)
	return ast, w.Source, err
}
