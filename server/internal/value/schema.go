package value

import (
	"fmt"
	"math"

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
// 2.0 (§5.8.9); a float a number that a double holds; a string a string of at
// most MaxStringLength characters; a text one of at most MaxTextLength; and an
// enum one of its attribute's options, of 1 to MaxOptionLength characters. It
// panics on another type.
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
		return &schemagen.Schema{Type: "string", MaxLength: schemagen.Int(MaxStringLength)}
	case Text:
		return &schemagen.Schema{Type: "string", MaxLength: schemagen.Int(MaxTextLength)}
	case Enum:
		return &schemagen.Schema{Type: "string", MinLength: schemagen.Int(1),
			MaxLength: schemagen.Int(MaxOptionLength)}
	}
	panic(fmt.Sprintf("value: %q is no type of a value", typ))
}
