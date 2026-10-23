package variable

import (
	"slices"

	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// The types of a variable (PRD FR-VAR-01, §5.8.3), the types of a value
// (package value) but an attribute's text and enum.
const (
	TypeBool   = value.Bool
	TypeInt    = value.Int
	TypeFloat  = value.Float
	TypeString = value.String
)

// types are the types of a variable.
var types = []string{TypeBool, TypeInt, TypeFloat, TypeString}

// Types returns the types of a variable, in the order of the database's
// check and of the schemas' enums, as a copy that the caller may change.
func Types() []string {
	return slices.Clone(types)
}

// Limits of a variable's default (§5.8.1, §5.8.9), which are a value's.
const (
	MaxInt          = value.MaxInt
	MaxStringLength = value.MaxStringLength
)

// Value is a variable's default, as JSON, which the variable's type says how
// to read (value.Value.Of). The zero Value is none, as a payload that leaves
// its default out has, which stands for the type's zero value.
type Value = value.Value

// Zero returns the zero value of the type typ, a variable's type: false, 0,
// 0.0, which is written as 0, or the empty string.
func Zero(typ string) Value {
	return value.Zero(typ)
}
