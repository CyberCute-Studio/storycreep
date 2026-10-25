package expr

import (
	"fmt"
	"strings"
)

// Code is the stable code of an error of a source, which a client tells the
// error by, and words it from in its user's language.
type Code string

// The codes of the errors that Parse returns, in the order of the table of
// syntax errors in schema/expr-corpus/README.md. Check returns ASTTooDeep,
// ASTTooLarge, InvalidStatement, and ConditionMustBePure too, of an AST, and
// the codes after them, as the table of compile errors there lists them.
const (
	// InvalidCharacter is a character that starts no token: outside a string
	// or a comment, one that is not ASCII, or ASCII that the language does
	// not use, as @ or a single &, and anywhere, a NUL, which no text holds,
	// or a byte that is not UTF-8, right after a string's backslash too,
	// though one in place of a \u's hexadecimal digits ends them, so that the
	// \u is InvalidEscape first.
	InvalidCharacter Code = "invalid_character"
	// UnterminatedString is a string literal whose closing quote does not
	// come before the end of its line or of the source.
	UnterminatedString Code = "unterminated_string"
	// InvalidEscape is a backslash in a string literal that \", \\, \n, \t,
	// \r, or \uXXXX does not follow, or a \uXXXX of U+0000 or of a surrogate
	// that is not a high one followed at once by a low one's \uXXXX.
	InvalidEscape Code = "invalid_escape"
	// StringTooLong is a string literal whose value has more than
	// MaxStringLength code points.
	StringTooLong Code = "string_too_long"
	// InvalidNumber is a number that is of neither literal's grammar, as 1.,
	// 1e5, or 3abc.
	InvalidNumber Code = "invalid_number"
	// IntegerOutOfRange is an integer literal beyond 2^53 − 1.
	IntegerOutOfRange Code = "integer_out_of_range"
	// FloatOutOfRange is a float literal beyond a double's range.
	FloatOutOfRange Code = "float_out_of_range"
	// IdentifierTooLong is an identifier of more than MaxIdentifierLength
	// characters.
	IdentifierTooLong Code = "identifier_too_long"
	// UnexpectedToken is a token that nothing before it can be followed by.
	UnexpectedToken Code = "unexpected_token"
	// UnexpectedEnd is the end of a source that is not complete.
	UnexpectedEnd Code = "unexpected_end"
	// ReservedWord is a reserved word where a name is read: null, if, else,
	// or elseif anywhere that an operand may be, global or board without a
	// dot after it, or any reserved word after a dot.
	ReservedWord Code = "reserved_word"
	// InvalidName is a qualified name of more than two parts, but for
	// cmp.<component>.<attribute>, or global or board with more than one part
	// after it.
	InvalidName Code = "invalid_name"
	// InvalidNodeRef is a node(…) whose argument is not one string literal
	// that holds a node's ID.
	InvalidNodeRef Code = "invalid_node_ref"
	// InvalidStatement is a statement of a script that is neither an
	// assignment nor a call, once the token after it ends it, or, as Check
	// finds, a call of a function that has no effect, as upper(x) alone,
	// which does nothing.
	InvalidStatement Code = "invalid_statement"
	// InvalidAssignmentTarget is an assignment to something other than a
	// variable or an attribute, or to one in parentheses.
	InvalidAssignmentTarget Code = "invalid_assignment_target"
	// ConditionMustBePure is an assignment operator in a condition, wherever
	// the parser stops at it (FR-EXPR-02 AC3), or, as Check finds, a call of
	// signal in a condition (FR-EXPR-04 AC3).
	ConditionMustBePure Code = "condition_must_be_pure"
	// ASTTooDeep is an opening parenthesis that the parser reads as a group's,
	// a call's, or a node reference's, and that opens one more than MaxDepth
	// of them; a ( that nothing before it can be followed by opens none. Of
	// an AST, Check gives it to the first node more than MaxDepth deep.
	ASTTooDeep Code = "ast_too_deep"
	// ASTTooLarge is the token that makes an AST's node beyond MaxNodes: a
	// literal, the identifier or the qualifier that a name, a call, or a node
	// reference starts with, a unary or a binary operator, or a statement's
	// assignment operator, each counted as the parser reaches it, after a
	// script's own node, which counts before its first token. A token that
	// the lexer could not read makes no node, nor does a reserved word that no
	// rule reads, so that each is its own error there. Of an AST that Parse
	// did not return, Check gives it to the node beyond MaxNodes.
	ASTTooLarge Code = "ast_too_large"

	// TooManyStatements is a script's statement beyond MaxStatements.
	TooManyStatements Code = "too_many_statements"
	// TooManyArguments is a call's argument beyond MaxArguments.
	TooManyArguments Code = "too_many_arguments"
	// TooManySignals is a script's call of signal beyond MaxSignals.
	TooManySignals Code = "too_many_signals"
	// UnknownIdentifier is a name whose variable, or whose component, nothing
	// at its place has, with the near matches that it might be.
	UnknownIdentifier Code = "unknown_identifier"
	// BoardVariableOutOfScope is a variable of another board than the one
	// whose expression it is in, which only its own board's expressions read
	// (FR-VAR-02 AC2).
	BoardVariableOutOfScope Code = "board_variable_out_of_scope"
	// VariableNotComponent is <a>.<b> whose <a> is a variable, which has no
	// attributes and shadows a component of its key, which cmp.<a>.<b>
	// names.
	VariableNotComponent Code = "variable_not_component"
	// ComponentNotValue is a component's key alone, where a value is read,
	// which a component is not.
	ComponentNotValue Code = "component_not_value"
	// UnknownAttribute is an attribute that its component does not have, with
	// the near matches that it might be.
	UnknownAttribute Code = "unknown_attribute"
	// AttributeNotRuntime is an attribute that is no part of the game's
	// state, which an expression neither reads nor writes (FR-COMP-03 AC2).
	AttributeNotRuntime Code = "attribute_not_runtime"
	// UnknownNode is a node reference to a node that the project does not
	// have, or has in the trash.
	UnknownNode Code = "unknown_node"
	// NodeRefJumper is a node reference to a jumper, whose visits are always
	// none (PRD §5.8.5).
	NodeRefJumper Code = "node_ref_jumper"
	// UnknownFunction is a call of a function that is not a built-in, with
	// the near matches that it might be (FR-EXPR-04 AC2).
	UnknownFunction Code = "unknown_function"
	// SignalNotStatement is a call of signal where a value is read, which it
	// has none of: anywhere but as a script's statement.
	SignalNotStatement Code = "signal_not_statement"
	// WrongArgumentCount is a call of a built-in with more or fewer
	// arguments than it takes.
	WrongArgumentCount Code = "wrong_argument_count"
	// InvalidSignalName is a signal's name that is not a string literal, or
	// is the empty one.
	InvalidSignalName Code = "invalid_signal_name"
	// TypeMismatch is an operand of a type that its place does not take,
	// with its type and those that its place takes (FR-EXPR-02 AC4).
	TypeMismatch Code = "type_mismatch"
	// InvalidEnumValue is an assignment to an enum attribute other than = of
	// a string literal that is one of its options.
	InvalidEnumValue Code = "invalid_enum_value"
)

// problems say what each error is, in English, for programs and logs. A client
// words an error from its code in its user's language.
var problems = map[Code]string{
	InvalidCharacter:        "a character that starts no token",
	UnterminatedString:      "a string literal without its closing quote on its line",
	InvalidEscape:           "an escape that is not \\\", \\\\, \\n, \\t, \\r, or \\u of a character other than NUL",
	StringTooLong:           fmt.Sprintf("a string literal of more than %d characters", MaxStringLength),
	InvalidNumber:           "a number that is neither an integer literal nor a float literal",
	IntegerOutOfRange:       "an integer literal beyond 9007199254740991",
	FloatOutOfRange:         "a float literal beyond a double's range",
	IdentifierTooLong:       fmt.Sprintf("an identifier of more than %d characters", MaxIdentifierLength),
	UnexpectedToken:         "a token that cannot follow what is before it",
	UnexpectedEnd:           "the end of a source that is not complete",
	ReservedWord:            "a reserved word where a name is read",
	InvalidName:             "a name of more parts than the forms of a variable's or an attribute's",
	InvalidNodeRef:          "a node(…) that does not hold one string literal of a node's ID",
	InvalidStatement:        "a statement that is neither an assignment nor a call of a function with an effect",
	InvalidAssignmentTarget: "an assignment to something other than a variable or an attribute",
	ConditionMustBePure:     "an assignment operator or a signal in a condition",
	ASTTooDeep:              fmt.Sprintf("parentheses nested, or an AST, more than %d deep", MaxDepth),
	ASTTooLarge:             fmt.Sprintf("an AST of more than %d nodes", MaxNodes),
	TooManyStatements:       fmt.Sprintf("a script of more than %d statements", MaxStatements),
	TooManyArguments:        fmt.Sprintf("a call of more than %d arguments", MaxArguments),
	TooManySignals:          fmt.Sprintf("a script of more than %d signals", MaxSignals),
	UnknownIdentifier:       "a name that no variable or component at its place has",
	BoardVariableOutOfScope: "a variable of another board",
	VariableNotComponent:    "a variable's key before an attribute's, where a component's is read",
	ComponentNotValue:       "a component's key alone, where a value is read",
	UnknownAttribute:        "an attribute that its component does not have",
	AttributeNotRuntime:     "an attribute that is no part of the game's state",
	UnknownNode:             "a node reference to a node that the project does not have",
	NodeRefJumper:           "a node reference to a jumper",
	UnknownFunction:         "a call of a function that is not a built-in",
	SignalNotStatement:      "a signal where a value is read",
	WrongArgumentCount:      "a call of more or fewer arguments than its function takes",
	InvalidSignalName:       "a signal's name that is not a string literal of a character or more",
	TypeMismatch:            "an operand of a type that its place does not take",
	InvalidEnumValue:        "an assignment to an enum attribute other than = of a string literal of one of its options",
}

// Error is an error of a source: its code, and the characters that it covers,
// which are those of a token, of an expression, or, for UnexpectedEnd, none,
// at the end of the source. A TypeMismatch names its operand's type, and the
// types that its place takes, and an unknown name or function the near
// matches that it might be.
type Error struct {
	Code Code `json:"code"`
	Range
	// Type is a TypeMismatch's operand's type: bool, int, float, string, or
	// node, a node reference's, and "" for any other error.
	Type string `json:"type,omitempty"`
	// Expected are the types that a TypeMismatch's operand's place takes, in
	// the order bool, int, float, string, node.
	Expected []string `json:"expected,omitempty"`
	// Suggestions are what might be written in place of the error's
	// characters, nearest first, of an UnknownIdentifier, an
	// UnknownAttribute, an UnknownFunction, or a VariableNotComponent, if
	// anything near is.
	Suggestions []string `json:"suggestions,omitempty"`
}

func (e *Error) Error() string {
	s := fmt.Sprintf("expr: %s, %s, at characters %d to %d", e.Code, problems[e.Code], e.Start, e.End)
	if e.Type != "" {
		s += fmt.Sprintf(": of the type %s, where %s is expected", e.Type, strings.Join(e.Expected, " or "))
	}
	if len(e.Suggestions) > 0 {
		s += "; perhaps " + strings.Join(e.Suggestions, " or ")
	}
	return s
}
