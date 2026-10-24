package expr

import "fmt"

// Code is the stable code of an error of a source, which a client tells the
// error by, and words it from in its user's language.
type Code string

// The codes of the errors that Parse returns, in the order of the table in
// schema/expr-corpus/README.md.
const (
	// InvalidCharacter is a character that starts no token: outside a string
	// or a comment, one that is not ASCII, or ASCII that the language does
	// not use, as @ or a single &, and anywhere, a NUL, which no text holds,
	// or a byte that is not UTF-8.
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
	// dot and a key after it, or any reserved word after a dot.
	ReservedWord Code = "reserved_word"
	// InvalidName is a qualified name of more than two parts, but for
	// cmp.<component>.<attribute>, or global or board with more than one part
	// after it.
	InvalidName Code = "invalid_name"
	// InvalidNodeRef is a node(…) whose argument is not one string literal
	// that holds a node's ID.
	InvalidNodeRef Code = "invalid_node_ref"
	// InvalidStatement is a statement of a script that is neither an
	// assignment nor a call.
	InvalidStatement Code = "invalid_statement"
	// InvalidAssignmentTarget is an assignment to something other than a
	// variable or an attribute, or to one in parentheses.
	InvalidAssignmentTarget Code = "invalid_assignment_target"
	// ConditionMustBePure is an assignment operator in a condition
	// (FR-EXPR-02 AC3).
	ConditionMustBePure Code = "condition_must_be_pure"
	// ASTTooDeep is a parenthesis, of a group or of a call's arguments, that
	// opens one more than MaxDepth of them.
	ASTTooDeep Code = "ast_too_deep"
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
	InvalidStatement:        "a statement that is neither an assignment nor a call",
	InvalidAssignmentTarget: "an assignment to something other than a variable or an attribute",
	ConditionMustBePure:     "an assignment in a condition",
	ASTTooDeep:              fmt.Sprintf("parentheses nested more than %d deep", MaxDepth),
}

// Error is an error of a source: its code, and the characters that it covers,
// which are those of a token, of an expression, or, for UnexpectedEnd, none,
// at the end of the source.
type Error struct {
	Code Code `json:"code"`
	Range
}

func (e *Error) Error() string {
	return fmt.Sprintf("expr: %s, %s, at characters %d to %d", e.Code, problems[e.Code], e.Start, e.End)
}
