// Package expr reads the source of the expression language (PRD §5.8): a
// connection's or a branch output's condition, which is one expression, and a
// node's or a connection's script, which is a sequence of statements
// (§5.8.5). Parse splits a source into tokens by the lexical rules of §5.8.1,
// reads them by the grammar of §5.8.2, and returns its AST (§5.8.8), or the
// first error that it meets reading the source from left to right, with a
// stable code and the characters that it covers, counted in code points
// (Error). Encode writes an AST in its canonical form, so that the same AST is
// always the same bytes (FR-EXPR-03 AC3).
//
// Parse names a variable or a component's attribute as its source writes it,
// and leaves which entity that is to name resolution (§5.8.3), which adds the
// entity's ID. It does not check types, nor purity beyond an assignment in a
// condition, nor the sandbox's limits (§5.8.6) beyond the lengths of
// identifiers and string literals and how deeply parentheses nest.
//
// schema/expr-corpus/syntax.json holds the parser's cases, which
// TestSyntaxCorpus runs, and which the TypeScript parser runs too, so that the
// two read every source alike; the corpus's README describes the cases, the
// AST's JSON, and the codes of the errors.
package expr

import (
	"fmt"

	"github.com/CyberCute-Studio/storycreep/server/internal/ident"
	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// Context is what a source is the source of, which decides how it is read
// (§5.8.5): a condition is one expression, of which line breaks are white
// space, and a script is a sequence of statements, separated by ; or by line
// breaks.
type Context string

// The contexts of a source, as the expression entity's context names them
// (§5.3).
const (
	ContextCondition Context = "condition"
	ContextScript    Context = "script"
)

// The limits that Parse checks (§5.8.1, §5.8.6).
const (
	// MaxIdentifierLength is the most characters of an identifier.
	MaxIdentifierLength = ident.MaxLength
	// MaxStringLength is the most characters, counted as code points, of the
	// value of a string literal, as of any string value.
	MaxStringLength = value.MaxStringLength
	// MaxDepth is the AST depth of §5.8.6, which bounds how deeply
	// parentheses nest too, whether a group's or a call's, so that reading a
	// source never recurses deeper than its AST may be.
	MaxDepth = 64
)

// Parse reads source as the source of a condition or a script, as ctx says,
// and returns its AST: a condition's expression, or a *Script. Its error is
// always an *Error: the first problem that it meets reading the source from
// left to right. It panics if ctx is neither ContextCondition nor
// ContextScript.
func Parse(source string, ctx Context) (Node, error) {
	if ctx != ContextCondition && ctx != ContextScript {
		panic(fmt.Sprintf("expr: %q is no context of a source", ctx))
	}
	p := &parser{ctx: ctx, toks: lex(source, ctx)}
	return p.parse()
}
