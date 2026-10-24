# Expression corpus

Cases of the expression language that every implementation of it runs, so that the evaluators, Go, TypeScript, and GDScript, give the same result for the same expression (PRD §5.8.9, plan note N46), and the parsers, Go and TypeScript, read every source alike (FR-EXPR-02, plan note N47). Where a platform's own operation differs, as JavaScript's `Math.round`, `toFixed`, `.length`, and `<` on strings, GDScript's `String.to_float()`, or an int64 product that wraps, a case says what the language computes instead.

The Go package `server/internal/semantics` runs every case of an operation (`TestCorpus`), and so will the TypeScript engine (plan item 44) and the GDScript evaluator (item 103). The Go package `server/internal/expr` runs every case of the parser (`TestSyntaxCorpus`), and so will the TypeScript parser (item 44).

| File | Cases of |
| --- | --- |
| [`numbers.json`](numbers.json) | `int` and `float` arithmetic, with their overflow, `float_not_finite`, and division by zero; the conversion of a float to an int, `to_int`, `floor`, `ceil`, and `round`; and `to_int` and `to_float` of a string. |
| [`strings.json`](strings.json) | `len`, comparison, concatenation, `contains`, `starts_with`, and `ends_with`, all of code points, and `upper`, `lower`, and `trim` by the tables of [`../unicode/`](../unicode/). |
| [`formatting.json`](formatting.json) | `to_string`, and how a text value reference shows a value, with and without a format specifier (FR-TEXT-02). |
| [`syntax.json`](syntax.json) | The parser: a condition's or a script's source, and the AST or the error that it gives, of each token, operator, and form of the grammar, and of each error (§5.8.1, §5.8.2, §5.8.8). Its format is [its own](#a-case-of-the-parser). |

## A case

Each file of operations is an object whose `cases` hold one operation each, applied to values:

```json
{
  "note": "PRD §5.8.9: -MAX * 2 overflows",
  "op": "*",
  "args": [
    { "type": "int", "value": -9007199254740991 },
    { "type": "int", "value": 2 }
  ],
  "result": { "type": "int", "value": 0 },
  "error": "integer_overflow"
}
```

| Key | Contents |
| --- | --- |
| `note` | What the case checks, unique within its file. |
| `op` | The operation: an operator or a built-in function, below. |
| `args` | The values that it is applied to, in order. |
| `decimals` | Only with `interpolate`: the format specifier's digits after the point, from 0 to 6. |
| `result` | The value that it yields. |
| `error` | The runtime error that it reports, or absent for none. A case with an error yields the zero value of its result's type: `0`, `0.0`, `""`, or `false` (§5.8.7). |

A value is its `type`, `bool`, `int`, `float`, or `string`, and its `value`, which the type says how to read, never the JSON's form, as the published format's values are read (§5.8.9): an `int` is a JSON integer from -9007199254740991 to 9007199254740991, written without a point or an exponent; a `float` is a JSON number, read as the double nearest it, finite, and never -0, whether or not it is written with a point, as `2.0` or `1e300`; a `string` is a JSON string of code points; and a `bool` is `true` or `false`. A runner reads a `float` as its own `to_float` reads a string, correctly rounded, since a platform's JSON parser may not be, and compares a result with `result` by its type and its value exactly, a `float` by its bits, so that a -0 that should be 0 fails.

## Operations

An operation takes the types that the language's type checker gives it (§5.8.3): an `int` beside a `float` is widened to a `float`, exactly, and the operation is the `float`'s.

| `op` | Arguments | Result |
| --- | --- | --- |
| `+`, `-`, `*`, `/` | two numbers | an `int` of two `int`s, otherwise a `float`. `/` of two `int`s truncates toward zero. |
| `+` | two strings | their concatenation |
| `%` | two `int`s | the remainder, with the sign of the dividend |
| `-` | one number | its negation |
| `==`, `!=` | two values of one type after widening | a `bool` |
| `<`, `<=`, `>`, `>=` | two numbers, or two strings, compared by code point | a `bool` |
| `to_int` | a `float`, truncated toward zero, or a `string` | an `int` |
| `to_float` | an `int`, widened, or a `string` | a `float` |
| `to_string` | any value | a `string` |
| `floor`, `ceil`, `round` | a `float` | an `int`; `round` rounds half away from zero |
| `len` | a `string` | its number of code points, an `int` |
| `contains`, `starts_with`, `ends_with` | two `string`s | a `bool` |
| `upper`, `lower`, `trim` | a `string` | a `string` |
| `interpolate` | any value, and `decimals` | a `string`: the value as a text value reference shows it, as `to_string` writes it without `decimals`, and with them, when it is a number, the decimal that `to_string` writes rounded half away from zero to that many digits after the point, in positional notation, without a sign when it rounds to zero. A value that is not a number is shown as `to_string` writes it, whatever `decimals` is. |

## Runtime errors

| Error | When |
| --- | --- |
| `integer_overflow` | An `int` result is beyond ±(2^53 − 1), or `to_int`, `floor`, `ceil`, or `round` of a `float`, or `to_int` of a string, is. |
| `float_not_finite` | A `float` result overflows a double, or `to_float` of a string does. |
| `division_by_zero` | `/` by `0` or `0.0`, or `%` by `0`. |
| `conversion_failed` | `to_int` or `to_float` of a string outside its grammar. |

## A case of the parser

`syntax.json` is an object whose `cases` hold one source each, with the AST that the parser gives it, or the error:

```json
{
  "note": "PRD FR-EXPR-02 AC3: an assignment in a condition is condition_must_be_pure, at its operator",
  "context": "condition",
  "source": "met_elara = true",
  "error": { "code": "condition_must_be_pure", "start": 10, "end": 11 }
}
```

| Key | Contents |
| --- | --- |
| `note` | What the case checks, unique within the file. |
| `context` | How the source is read (PRD §5.8.5): `condition`, as one expression, or `script`, as statements. |
| `source` | The source, a JSON string. It writes each character as itself or escapes it, but never as a surrogate's escape, which one JSON decoder reads as U+FFFD and another keeps when it is not one of a pair. |
| `ast` | The AST that the parser gives the source (below). |
| `error` | Instead of `ast`, the error that the parser gives the source: its `code` (below), and its range, `start` and `end`. |

A runner compares a case's `ast` with its parser's by value, each object's keys in any order, and each number as the double nearest it, as every runtime reads an AST's numbers (PRD §5.8.9), so that Biome, which formats the file, may write `1e+21` as `1e21`. The Go runner compares the canonical form that it writes (PRD §5.8.8) with the case's AST so written.

A range counts characters as code points, from 0: `start` is the offset of the error's first character, and `end` the offset of the character after its last, so that an error at the end of the source, as `unexpected_end`, has an empty range there. A character beyond U+FFFF, as 😀, is one character, not the two UTF-16 code units that JavaScript's `.length` counts, nor the four bytes of its UTF-8. An editor converts a range to its own units.

### The AST

PRD §5.8.8 gives the AST's node kinds, their fields, their operators' names, and its canonical form. The parser names a variable or a component's attribute as its source writes it, in `name`, as `met_elara`, `global.met_elara`, `elara.trust`, or `cmp.elara.trust`, and leaves out the `ref` and `attribute` of the entity that it is, which name resolution adds (PRD §5.8.3, plan item 40), so that no case of this file has them.

### The first error

The parser reads a source from left to right, and stops with the first error that it meets, though a later token has one too. So that two parsers give the same error for every source, each meets them as the Go parser does:

- A token that cannot be read, as a string with a bad escape, is the token's error once the parser reaches the token, and a token that the parser reaches but cannot read on from is `unexpected_token`, or `unexpected_end` at the end of the source. A string literal is read to its end before its length is checked, so that a bad escape or a forbidden character in it comes first.
- In a script, a statement is read as an expression first, and then is `invalid_statement` when it is neither an assignment nor a call, and `invalid_assignment_target` when an assignment's operator follows what is not a variable or an attribute, or one in parentheses, over the whole expression, its parentheses included.
- In a condition, an assignment's operator is `condition_must_be_pure` wherever the parser stops at it, as in `a and (x = 1)`.
- A qualified name is read to its last part before its parts are counted, so that a reserved word among them, as in `a.b.c.null`, is `reserved_word` rather than `invalid_name`.
- `node(` is read to its closing parenthesis, and its argument's error comes first. One string literal that holds no node's ID is `invalid_node_ref` over the string; any other argument, or none, over the whole `node(…)`.

### Syntax errors

| Error | When | Range |
| --- | --- | --- |
| `invalid_character` | A character that starts no token: outside a string literal or a comment, one that is not ASCII, or ASCII that the language does not use, as `@`, `'`, or a single `&` or `\|`; and anywhere, a NUL, which no text holds, or a byte that is not UTF-8. White space is a space, a tab, a CR, or an LF, and nothing else, not a no-break space. | The character. |
| `unterminated_string` | A string literal that its line or the source ends in before its closing quote, as right after a backslash. | From the opening quote to the end. |
| `invalid_escape` | A backslash in a string literal that `\"`, `\\`, `\n`, `\t`, `\r`, or `\u` and four hexadecimal digits does not follow, or a `\u` of U+0000, or of a surrogate other than a high one followed at once by a low one's `\u`, which are one character together. | The backslash and the character after it, or after `\u` the hexadecimal digits that follow it, up to four. |
| `string_too_long` | A string literal whose value has more than 4,096 characters, an escape counted as the one that it stands for. | The string literal. |
| `invalid_number` | A number that is neither an integer literal, `[0-9]+`, nor a float literal, `[0-9]+\.[0-9]+([eE][+-]?[0-9]+)?`, as `1.`, `1e5`, `3abc`, `1_000`, or `0x1F`. A number is the run of letters, digits, `_`, and `.` that starts with a digit, with a sign after an `e` or `E` of it. | The number. |
| `integer_out_of_range` | An integer literal beyond 9,007,199,254,740,991, 2^53 − 1. A negative number is `-` applied to a literal, so that `-9007199254740992` is one too. | The literal, without a `-`. |
| `float_out_of_range` | A float literal beyond a double's range. One too small for a double is `0.0`, or the subnormal nearest it. | The literal. |
| `identifier_too_long` | An identifier, `[A-Za-z_][A-Za-z0-9_]*`, of more than 64 characters. | The identifier. |
| `unexpected_token` | A token that nothing before it can be followed by, as the second `-` of `--a`, a `;` in a condition, or, in a script, an operator that starts a line after a statement that the line break ended, as the `and` of `x = a` and `and b` on two lines. | The token. |
| `unexpected_end` | The end of a source that is not complete, as of `a +`, `(a`, or an empty condition. | Empty, at the end. |
| `reserved_word` | A reserved word where a name is read: `null`, `if`, `else`, or `elseif` as an operand or a function's name, `global` or `board` without a dot after it, or any reserved word after a dot. | The word. |
| `invalid_name` | A qualified name of three parts that does not start with `cmp`, of more than three, or `global` or `board` with more than one part after it. | The name. |
| `invalid_node_ref` | A `node(…)` that does not hold one string literal of a node's ID, as `node("bd_…")` or `node(x)`. | The string literal, or the whole `node(…)`. |
| `invalid_statement` | A statement of a script that is neither an assignment nor a call, as `x == 1`, or one in parentheses. | The statement. |
| `invalid_assignment_target` | An assignment to something other than a variable or an attribute, as `1 = x` or `f(x) = 1`, or to one in parentheses. | The target. |
| `condition_must_be_pure` | An assignment's operator in a condition (FR-EXPR-02 AC3). | The operator. |
| `ast_too_deep` | A parenthesis, of a group or of a call's arguments, that opens one more than 64 of them (PRD §5.8.6). | The parenthesis. |
