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
// identifiers and string literals, how deeply parentheses nest, and how many
// nodes an AST has.
//
// Check does the rest, against what a project's expressions can name
// (Project), at a board: it checks the AST against the other limits of the
// sandbox, resolves each name to its entity's ID, with the near matches of a
// name or a function that it cannot resolve, and checks the types (§5.8.3),
// the built-ins' calls (§5.8.4), and purity, which keeps signal out of
// conditions; it too returns the first error that it meets, an *Error.
// Compile parses a source and checks its AST. VersionOf returns the
// ast_version of the constructs that ASTs use, as a snapshot declares it
// (FR-EXPR-04).
//
// schema/expr-corpus/syntax.json holds the parser's cases, which
// TestSyntaxCorpus runs, and compile.json the checker's, with the project
// that they resolve names against, which TestCompileCorpus runs; the
// TypeScript parser and checker run them too, so that the two compile every
// source alike. The corpus's README describes the cases, the AST's JSON, and
// the codes of the errors.
package expr

import (
	"errors"
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
	// parentheses nest too, whether a group's, a call's, or a node
	// reference's, so that reading a source never recurses deeper than its
	// AST may be.
	MaxDepth = 64
	// MaxNodes is the AST node count of §5.8.6, which Parse counts as it
	// reads the tokens that make the nodes, so that a source of any length
	// costs no more than an AST of MaxNodes nodes.
	MaxNodes = 512
)

// ErrContext is Parse's error for a context that is neither ContextCondition
// nor ContextScript, which is its caller's mistake rather than the source's.
var ErrContext = errors.New("expr: no context of a source")

// Parse reads source as the source of a condition or a script, as ctx says,
// and returns its AST: a condition's expression, or a *Script. Its error is an
// *Error, the first problem that it meets reading the source from left to
// right, or, if ctx is neither ContextCondition nor ContextScript, one that
// wraps ErrContext.
//
// Parse never recurses deeper than parentheses nest, and its AST has at most
// MaxNodes nodes, so that a walk of it that recurses once per level recurses
// at most that deep, though the AST may be deeper than MaxDepth, as a chain of
// binary operators, which nest without parentheses, makes it, until Check
// checks the AST depth of §5.8.6 of it.
func Parse(source string, ctx Context) (Node, error) {
	if ctx != ContextCondition && ctx != ContextScript {
		return nil, fmt.Errorf("%w: %q", ErrContext, ctx)
	}
	p := &parser{ctx: ctx, lex: &lexer{src: source, ctx: ctx}}
	return p.parse()
}
