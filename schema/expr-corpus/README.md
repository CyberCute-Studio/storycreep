# Expression corpus

Cases of the expression language that every evaluator of it runs, Go, TypeScript, and GDScript, so that the three give the same result for the same expression (PRD §5.8.9, plan note N46). Where a platform's own operation differs, as JavaScript's `Math.round`, `toFixed`, `.length`, and `<` on strings, GDScript's `String.to_float()`, or an int64 product that wraps, a case says what the language computes instead.

The Go package `server/internal/semantics` runs every case (`TestCorpus`), and so will the TypeScript engine (plan item 44) and the GDScript evaluator (item 103). The cases of the parser, an expression's source with the AST or the error that it gives, arrive with the parser (item 39), in a format of their own.

| File | Cases of |
| --- | --- |
| [`numbers.json`](numbers.json) | `int` and `float` arithmetic, with their overflow, `float_not_finite`, and division by zero; the conversion of a float to an int, `to_int`, `floor`, `ceil`, and `round`; and `to_int` and `to_float` of a string. |
| [`strings.json`](strings.json) | `len`, comparison, concatenation, `contains`, `starts_with`, and `ends_with`, all of code points, and `upper`, `lower`, and `trim` by the tables of [`../unicode/`](../unicode/). |
| [`formatting.json`](formatting.json) | `to_string`, and how a text value reference shows a value, with and without a format specifier (FR-TEXT-02). |

## A case

Each file is an object whose `cases` hold one operation each, applied to values:

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
