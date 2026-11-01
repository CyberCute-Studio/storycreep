package clipboard

import (
	"errors"
	"fmt"
	"strings"

	"github.com/CyberCute-Studio/storycreep/server/internal/expr"
	"github.com/CyberCute-Studio/storycreep/server/internal/ident"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/node"
	"github.com/CyberCute-Studio/storycreep/server/internal/richtext"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// The node types, as package node names them.
const (
	typeElement = node.TypeElement
	typeBranch  = node.TypeBranch
	typeJumper  = node.TypeJumper
)

// portOut is the port of an element, which a connection that names none
// leaves.
const portOut = "out"

// Check returns what is wrong with the clipboard c that its schema cannot say,
// each problem at its field, as nodes[2].text, once its schema has taken it,
// which refuses a newer version than Version (Clipboard.Version): an ID of the
// wrong kind, or one that two of its entities of a kind share; what node.create
// would refuse of a node, as a field that its type does not have, a size
// without a height, or a title or a tag that a new node's may not be
// (node.CheckCreated), or that a branch's outputs are set on a node of another
// type; a text that is no document that Parse takes, at where in it the value
// that is wrong is; a branch's outputs out of their order, or an else that the
// branch has no conditional output for; a connection between nodes that it does
// not hold, out of a jumper, or out of a port that its node does not have; a
// key that is no identifier, a scope without its board, or a default of another
// type than its variable's or its attribute definition's; and declarations that
// no project could have, as two attributes of one definition that say different
// things of it (expr.ErrProject). What a paste writes it holds to the rules
// that the commands that write it hold it to, but for what the board that it is
// pasted onto makes of it, as a source that its names make longer than a source
// may be once they are written there (plan.source), and what it does not write,
// as a declaration's default and description, which what it resolves to keeps,
// only to what reading it takes, as a value that the database holds is read
// (value.Read), so that a clipboard of what a project holds, which a rule that
// has changed since it was written may no longer take, pastes all the same,
// though creating one in its place may be refused. The variables and components
// that the content names, and the nodes that its jumpers target, need not be
// declared, nor held: a paste keeps or leaves out what they name as it is
// (package doc).
func Check(c Clipboard) input.FieldErrors {
	var errs input.FieldErrors
	add := func(at, msg string) { errs = append(errs, input.FieldError{Field: at, Message: msg}) }
	errs = append(errs, entityID(ids.Project, "project_id", c.ProjectID)...)
	errs = append(errs, entityID(ids.Board, "board_id", c.BoardID)...)
	seen := map[ids.UUID]string{}
	unique := func(kind ids.Kind, at, id string) bool {
		u, err := ids.Decode(kind, id)
		if err != nil {
			add(at, fmt.Sprintf("is not a %s's ID: %v", kind, err))
			return false
		}
		if first, dup := seen[u]; dup {
			add(at, "is the ID of "+first+" too")
			return false
		}
		seen[u] = at
		return true
	}
	nodes := map[string]ClipboardNode{}
	outputs := map[string]string{} // an output's ID, by the node's that it is of
	for i, n := range c.Nodes {
		at := fmt.Sprintf("nodes[%d]", i)
		if unique(ids.Node, at+".id", n.ID) {
			nodes[n.ID] = n
		}
		errs = append(errs, checkNode(at, n)...)
		for j, o := range n.Outputs {
			oat := fmt.Sprintf("%s.outputs[%d]", at, j)
			if unique(ids.BranchOutput, oat+".id", o.ID) {
				outputs[o.ID] = n.ID
			}
		}
	}
	for i, cn := range c.Connections {
		at := fmt.Sprintf("connections[%d]", i)
		unique(ids.Connection, at+".id", cn.ID)
		from, ok := nodes[cn.FromNodeID]
		switch {
		case !ok:
			add(at+".from_node_id", "is not the ID of a node of the clipboard")
		case from.Type == typeJumper:
			add(at+".from_node_id", "is a jumper's, which has no connections out (PRD §5.8.5)")
		case from.Type == typeBranch && outputs[cn.FromPort] != cn.FromNodeID:
			add(at+".from_port", "is not the ID of an output of the branch that the connection leaves, as a "+
				"branch's connection's port is")
		case from.Type == typeElement && cn.FromPort != "" && cn.FromPort != portOut:
			add(at+".from_port", "is not out, an element's one port")
		}
		if _, ok := nodes[cn.ToNodeID]; !ok {
			add(at+".to_node_id", "is not the ID of a node of the clipboard")
		}
		errs = append(errs, checkText(at+".label", cn.Label)...)
	}
	for i, v := range c.Variables {
		at := fmt.Sprintf("variables[%d]", i)
		unique(ids.Variable, at+".id", v.ID)
		errs = append(errs, checkKey(at+".key", v.Key)...)
		switch {
		case v.Scope == "board" && v.BoardID == "":
			add(at+".board_id", "is missing, which a board's variable has")
		case v.Scope == "board":
			errs = append(errs, entityID(ids.Board, at+".board_id", v.BoardID)...)
		case v.BoardID != "":
			add(at+".board_id", "is set on a global variable, which has no board")
		case v.ResetOnEnter:
			add(at+".reset_on_enter", "is set on a global variable, which only a board's variable may be")
		}
		if _, problem := v.Default.Read(v.Type, value.Checks{}); problem != "" {
			add(at+".default", problem)
		}
	}
	for i, k := range c.Components {
		at := fmt.Sprintf("components[%d]", i)
		unique(ids.Component, at+".id", k.ID)
		errs = append(errs, checkKey(at+".key", k.Key)...)
		errs = append(errs, entityID(ids.ComponentType, at+".type.id", k.Type.ID)...)
		errs = append(errs, checkKey(at+".type.key", k.Type.Key)...)
		// The attribute definitions of a type are shared by its components,
		// so one may be declared of several components, which say the same
		// of it (expr.NewProject), but of one component once.
		mine := map[string]bool{}
		for j, a := range k.Attributes {
			aat := fmt.Sprintf("%s.attributes[%d]", at, j)
			if _, err := ids.Decode(ids.AttributeDef, a.ID); err != nil {
				add(aat+".id", fmt.Sprintf("is not an attribute definition's ID: %v", err))
			} else if mine[a.ID] {
				add(aat+".id", "is the ID of another attribute definition of the component too")
			}
			mine[a.ID] = true
			errs = append(errs, checkKey(aat+".key", a.Key)...)
			if _, problem := a.Default.Read(a.Type, value.Checks{}, a.Options...); problem != "" {
				add(aat+".default", problem)
			}
		}
	}
	if len(errs) > 0 {
		return errs
	}
	if _, err := c.names(); err != nil {
		add("", "declares what no project has: "+strings.TrimPrefix(err.Error(), "expr: "))
	}
	return errs
}

// checkNode returns what is wrong with the node n of a clipboard, at at, but
// for its outputs' IDs: what node.create would refuse of it, but for its ID
// and its board (node.CheckCreated), outputs set on a node that is not a
// branch, its text, and the order of a branch's outputs.
func checkNode(at string, n ClipboardNode) input.FieldErrors {
	var errs input.FieldErrors
	add := func(field, msg string) { errs = append(errs, input.FieldError{Field: at + "." + field, Message: msg}) }
	for _, e := range node.CheckCreated(node.Created{Type: n.Type, Kind: n.Kind, Title: n.Title,
		Text: len(n.Text) > 0, SpeakerComponentID: n.SpeakerComponentID, CoverAssetID: n.CoverAssetID, Tags: n.Tags,
		Position: n.Position, Size: n.Size, IsEnding: n.IsEnding, Target: n.Target, OnEnter: n.OnEnter,
		OnExit: n.OnExit}) {
		add(e.Field, e.Message)
	}
	if n.Type != typeBranch && len(n.Outputs) > 0 {
		add("outputs", fmt.Sprintf("is set on a node of the type %s, which has none: only a node of the type %s "+
			"has them", n.Type, typeBranch))
	}
	errs = append(errs, checkText(at+".text", n.Text)...)
	for j, o := range n.Outputs {
		oat := fmt.Sprintf("outputs[%d]", j)
		switch {
		case o.Else && j != len(n.Outputs)-1:
			add(oat+".else", "is set on an output before the branch's last, but a branch's else comes after its "+
				"conditional outputs")
		case o.Else && j == 0:
			add(oat+".else", "is set on the branch's first output, but a branch has an else only while it has a "+
				"conditional output, which its else comes after")
		case o.Else && o.Condition != "":
			add(oat+".condition", "is set on an else, which has none")
		}
	}
	return errs
}

// checkText returns what is wrong with the text t at at: a document that
// richtext.Parse does not take, at where in it the value that is wrong is.
func checkText(at string, t Text) input.FieldErrors {
	if len(t) == 0 {
		return nil
	}
	if _, err := richtext.Parse(t); err != nil {
		var doc *richtext.Error
		if errors.As(err, &doc) {
			at += doc.Location()
		}
		return input.FieldErrors{{Field: at, Message: err.Error()}}
	}
	return nil
}

// checkKey returns what is wrong with the key at at: a key that is no
// identifier of the expression language (PRD §5.8.1).
func checkKey(at, key string) input.FieldErrors {
	if err := ident.Check(key); err != nil {
		return input.FieldErrors{{Field: at, Message: err.Error()}}
	}
	return nil
}

// entityID returns what is wrong with the ID id of the kind at at, as the
// packages of the commands that a paste applies say it.
func entityID(kind ids.Kind, at, id string) input.FieldErrors {
	if _, err := ids.Decode(kind, id); err != nil {
		return input.FieldErrors{{Field: at, Message: fmt.Sprintf("is not a %s's ID: %v", kind, err)}}
	}
	return nil
}

// names returns what the expressions of the clipboard's board could name
// where it was copied, of what it declares: its variables, each of its board,
// and its components, each with its attribute definitions, by which a paste
// reads the names of its expressions (expr.Relocate). An attribute of a
// component's type that the clipboard does not declare of it is not there,
// and a name of it names the component's attribute of its key wherever it is
// pasted, as cmp.<component>.<attribute> would.
func (c Clipboard) names() (*expr.Project, error) {
	variables := make([]expr.Variable, len(c.Variables))
	for i, v := range c.Variables {
		variables[i] = expr.Variable{ID: canonical(ids.Variable, v.ID), Key: v.Key, Type: v.Type}
		if v.Scope == "board" {
			variables[i].Board = canonical(ids.Board, v.BoardID)
		}
	}
	components := make([]expr.Component, len(c.Components))
	for i, k := range c.Components {
		components[i] = expr.Component{ID: canonical(ids.Component, k.ID), Key: k.Key,
			Attributes: make([]expr.Attribute, len(k.Attributes))}
		for j, a := range k.Attributes {
			components[i].Attributes[j] = expr.Attribute{ID: canonical(ids.AttributeDef, a.ID), Key: a.Key,
				Type: a.Type, Runtime: a.Runtime, Options: a.Options}
		}
	}
	return expr.NewProject(variables, components, nil)
}

// canonical returns the ID id of the kind in its canonical spelling, as
// ids.Encode writes it, or id itself if it is not one, which Check refuses.
func canonical(kind ids.Kind, id string) string {
	u, err := ids.Decode(kind, id)
	if err != nil {
		return id
	}
	return ids.Encode(kind, u)
}

// clipboardField returns the field of a payload, or of a request's body, that
// holds a clipboard at clipboard, of the clipboard's field f, as Check names
// it: clipboard.<f>, or clipboard itself for "", the whole clipboard.
func clipboardField(f string) string {
	return strings.TrimSuffix("clipboard."+f, ".")
}
