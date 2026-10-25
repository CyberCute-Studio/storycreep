package value

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/CyberCute-Studio/storycreep/server/internal/schemagen"
)

// JSONSchema gives the published schemas the definition of a value whose type
// the schema does not know, as a component's value of an attribute, which
// its attribute definition types (schemagen.Provider): a boolean, a number,
// or a string, never null.
func (Value) JSONSchema(*schemagen.Generator) *schemagen.Schema {
	return &schemagen.Schema{
		Description: "A value, which its declaration's type says how to read, never its JSON form (PRD §5.8.9): " +
			"a boolean, a number, or a string.",
		OneOf: []*schemagen.Schema{{Type: "boolean"}, {Type: "number"}, {Type: "string"}},
	}
}

// JSONSchemaOf gives the published schemas the schema of a value of the type
// typ, one of Types, as a variable's default is of its variable's type
// (schemagen.TypedProvider).
func (Value) JSONSchemaOf(_ *schemagen.Generator, typ string) *schemagen.Schema {
	return Schema(typ)
}

// Schema returns the JSON Schema of a value of the type typ, one of Types,
// as the published format holds it: a bool a boolean; an int an integer
// within ±MaxInt, which a consumer reads as an int whatever its JSON form, as
// 2.0 (§5.8.9); a float a number that a double holds, within the shortest
// form of the greatest double (FloatBeyondSchema); a string a string of at
// most MaxStringLength characters, and a text one of at most MaxTextLength,
// neither of which holds a NUL, which PostgreSQL's text cannot hold, nor a
// string literal of an expression (noNUL); and an enum one of its attribute's
// options, of 1 to MaxOptionLength characters. It panics on another type.
func Schema(typ string) *schemagen.Schema {
	switch typ {
	case Bool:
		return &schemagen.Schema{Type: "boolean"}
	case Int:
		return &schemagen.Schema{Type: "integer", Minimum: schemagen.Float(-MaxInt), Maximum: schemagen.Float(MaxInt)}
	case Float:
		return &schemagen.Schema{Type: "number", Minimum: schemagen.Float(-math.MaxFloat64),
			Maximum: schemagen.Float(math.MaxFloat64)}
	case String:
		return &schemagen.Schema{Type: "string", Pattern: noNUL, MaxLength: schemagen.Int(MaxStringLength)}
	case Text:
		return &schemagen.Schema{Type: "string", Pattern: noNUL, MaxLength: schemagen.Int(MaxTextLength)}
	case Enum:
		return &schemagen.Schema{Type: "string", MinLength: schemagen.Int(1),
			MaxLength: schemagen.Int(MaxOptionLength)}
	}
	panic(fmt.Sprintf("value: %q is no type of a value", typ))
}

// noNUL is the pattern of a string without a NUL, which Go's regular
// expressions and ECMAScript's read alike.
const noNUL = `^[^\x00]*$`

// maxDigits are the significant digits of the bound of a float that Schema
// gives, as JSON Schema compares a number with it: math.MaxFloat64 as a
// schema writes it, its shortest form, 1.7976931348623157e+308, which is a
// little less than the double itself.
var maxDigits = strings.Replace(strings.SplitN(strconv.FormatFloat(math.MaxFloat64, 'e', -1, 64), "e", 2)[0],
	".", "", 1)

// FloatBeyondSchema reports whether raw, a JSON number that Read takes as a
// float, is beyond the bounds that Schema gives a float, ±1.7976931348623157e+308,
// with which JSON Schema compares the number as it is written, by its value,
// though a double, and so Read, reads every number from there to half a unit
// in the last place beyond math.MaxFloat64, as 1.7976931348623158e308, as
// math.MaxFloat64, which no number that the server writes is. It compares
// the number's digits, as it is written, with the bound's, in time linear in
// its length, however many digits it has.
func FloatBeyondSchema(raw []byte) bool {
	f, err := strconv.ParseFloat(string(raw), 64)
	if err != nil || math.Abs(f) != math.MaxFloat64 {
		// Within the bounds, or beyond a double's range, which Read refuses.
		return false
	}
	// raw is within half a unit in the last place of math.MaxFloat64, so that
	// its first significant digit is of the same power of ten as the bound's,
	// and it is beyond the bound exactly when its significant digits, read
	// as a fraction, are greater than the bound's, as a string compares them
	// once neither has a zero at its end.
	mantissa := strings.TrimLeft(string(raw), "+-")
	if i := strings.IndexAny(mantissa, "eE"); i >= 0 {
		mantissa = mantissa[:i]
	}
	digits := strings.Trim(strings.Replace(mantissa, ".", "", 1), "0")
	return digits > maxDigits
}
