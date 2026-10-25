package expr

import (
	"maps"
	"slices"

	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/schemagen"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// The names of the definitions of an AST's schema.
const (
	schemaExpression = "ASTExpression"
	schemaScript     = "ASTScript"
)

// The patterns of the names that an AST writes: a variable's key, which is
// an identifier (§5.8.1), of at most MaxIdentifierLength characters, which
// its schema bounds with maxLength; and an attribute's, its component's key
// and its own joined by a dot, each of at most MaxIdentifierLength
// characters, which the pattern bounds itself (TestAttributePattern).
const (
	identifierPattern = `^[A-Za-z_][A-Za-z0-9_]*$`
	attributePattern  = `^[A-Za-z_][A-Za-z0-9_]{0,63}\.[A-Za-z_][A-Za-z0-9_]{0,63}$`
)

// ExpressionSchema returns a reference to the definition of the AST of a
// condition, an expression, as the published format holds it (§5.8.8): a
// node of any kind but assign and script, whose names are resolved to the
// IDs of what they name, which it defines with the definitions that it
// refers to. Every AST that Check accepts of a condition, Encode writes as
// JSON that it takes.
func ExpressionSchema(g *schemagen.Generator) *schemagen.Schema {
	return g.Define(schemaExpression, func() *schemagen.Schema {
		self := schemagen.Ref(schemaExpression)
		node := func(name, kind, description string, props map[string]*schemagen.Schema) *schemagen.Schema {
			return g.Define(name, func() *schemagen.Schema {
				p := maps.Clone(props)
				p["kind"] = &schemagen.Schema{Const: kind}
				return schemagen.Describe(g.Object(p, slices.Collect(maps.Keys(p))...), description)
			})
		}
		literal := func(name, typ string) *schemagen.Schema {
			return node(name, string(KindLit), "A literal of the type "+typ+".", map[string]*schemagen.Schema{
				"type":  {Const: typ},
				"value": value.Schema(typ),
			})
		}
		operators := func(versions any) *schemagen.Schema {
			var ops []any
			switch v := versions.(type) {
			case map[UnaryOp]int:
				for _, op := range slices.Sorted(maps.Keys(v)) {
					ops = append(ops, string(op))
				}
			case map[BinaryOp]int:
				for _, op := range slices.Sorted(maps.Keys(v)) {
					ops = append(ops, string(op))
				}
			}
			return &schemagen.Schema{Type: "string", Enum: ops}
		}
		return schemagen.Describe(&schemagen.Schema{OneOf: []*schemagen.Schema{
			literal("ASTBool", value.Bool),
			literal("ASTInt", value.Int),
			literal("ASTFloat", value.Float),
			literal("ASTString", value.String),
			variableSchema(g),
			attributeSchema(g),
			node("ASTNodeRef", string(KindNodeRef), "A node reference literal, node(\"nd_…\") (§5.8.4).",
				map[string]*schemagen.Schema{"ref": g.ID(ids.Node)}),
			node("ASTUnary", string(KindUnary), "A unary operator, not (!) or neg (-), applied to its operand.",
				map[string]*schemagen.Schema{"op": operators(unaryVersions), "operand": self}),
			node("ASTBinary", string(KindBinary), "A binary operator applied to its operands, left and right.",
				map[string]*schemagen.Schema{"left": self, "op": operators(binaryVersions), "right": self}),
			callSchema(g),
		}}, "The AST of an expression: a condition's, an operand, a call's argument, or an assignment's value.")
	})
}

// ScriptSchema returns a reference to the definition of the AST of a script,
// as the published format holds it (§5.8.8), which it defines with the
// definitions that it refers to: a script node of the script's statements,
// each an assignment or a call. Every AST that Check accepts of a script,
// Encode writes as JSON that it takes.
func ScriptSchema(g *schemagen.Generator) *schemagen.Schema {
	return g.Define(schemaScript, func() *schemagen.Schema {
		var ops []any
		for _, op := range slices.Sorted(maps.Keys(assignVersions)) {
			ops = append(ops, string(op))
		}
		target := g.Define("ASTTarget", func() *schemagen.Schema {
			return schemagen.Describe(&schemagen.Schema{OneOf: []*schemagen.Schema{variableSchema(g), attributeSchema(g)}},
				"What an assignment assigns to: a variable or a component's runtime attribute.")
		})
		assign := g.Define("ASTAssign", func() *schemagen.Schema {
			return schemagen.Describe(g.Object(map[string]*schemagen.Schema{
				"kind":   {Const: string(KindAssign)},
				"op":     {Type: "string", Enum: ops},
				"target": target,
				"value":  ExpressionSchema(g),
			}, "kind", "op", "target", "value"), "An assignment, with its operator as its source writes it.")
		})
		statement := g.Define("ASTStatement", func() *schemagen.Schema {
			return schemagen.Describe(&schemagen.Schema{OneOf: []*schemagen.Schema{assign, callSchema(g)}},
				"A statement of a script: an assignment or a call.")
		})
		return schemagen.Describe(g.Object(map[string]*schemagen.Schema{
			"kind": {Const: string(KindScript)},
			"statements": {Type: "array", Items: statement, MaxItems: schemagen.Int(MaxStatements),
				Description: "The script's statements, in order, [] for none."},
		}, "kind", "statements"), "The AST of a script: its statements, in order.")
	})
}

// variableSchema returns a reference to the definition of a variable's node.
func variableSchema(g *schemagen.Generator) *schemagen.Schema {
	return g.Define("ASTVar", func() *schemagen.Schema {
		return schemagen.Describe(g.Object(map[string]*schemagen.Schema{
			"kind": {Const: string(KindVar)},
			"name": {Type: "string", Pattern: identifierPattern, MaxLength: schemagen.Int(MaxIdentifierLength),
				Description: "The variable's key, for readability: the AST names the variable by its ref."},
			"ref": g.ID(ids.Variable),
		}, "kind", "name", "ref"), "A variable.")
	})
}

// attributeSchema returns a reference to the definition of an attribute's
// node.
func attributeSchema(g *schemagen.Generator) *schemagen.Schema {
	return g.Define("ASTAttr", func() *schemagen.Schema {
		return schemagen.Describe(g.Object(map[string]*schemagen.Schema{
			"attribute": g.ID(ids.AttributeDef),
			"kind":      {Const: string(KindAttr)},
			"name": {Type: "string", Pattern: attributePattern,
				Description: "<component_key>.<attribute_key>, for readability: the AST names the attribute by " +
					"its ref, the component's ID, and its attribute, the attribute definition's."},
			"ref": g.ID(ids.Component),
		}, "attribute", "kind", "name", "ref"), "A component's runtime attribute.")
	})
}

// callSchema returns a reference to the definition of a call's node.
func callSchema(g *schemagen.Generator) *schemagen.Schema {
	return g.Define("ASTCall", func() *schemagen.Schema {
		return schemagen.Describe(g.Object(map[string]*schemagen.Schema{
			"args": {Type: "array", Items: ExpressionSchema(g), MaxItems: schemagen.Int(MaxArguments),
				Description: "The arguments, in order, [] for none."},
			"function": {Type: "string", Pattern: identifierPattern, MaxLength: schemagen.Int(MaxIdentifierLength),
				Description: "The function's name. A runtime refuses at load a name that it does not know (PRD §6.4)."},
			"kind": {Const: string(KindCall)},
		}, "args", "function", "kind"), "A call of a function.")
	})
}
