// Package tsgen holds what the generators of the web app's TypeScript
// modules share: the names that a type may have, and how a property's name,
// a string, and a JSDoc comment are written. Package apicontract writes the
// REST API's types with it, and package schemagen the JSON Schemas' types.
package tsgen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Identifier matches the names that TypeScript takes as they are, as a
// type's or a property's.
var Identifier = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// reserved are TypeScript's reserved words and predefined types, which no
// type may be named.
var reserved = []string{
	"any", "Array", "as", "bigint", "boolean", "break", "case", "catch", "class", "const", "continue",
	"debugger", "declare", "default", "delete", "do", "else", "enum", "export", "extends", "false", "finally",
	"for", "from", "function", "if", "implements", "import", "in", "instanceof", "interface", "let", "never",
	"new", "null", "number", "object", "package", "private", "protected", "public", "Record", "return",
	"static", "string", "super", "switch", "symbol", "this", "throw", "true", "try", "type", "typeof",
	"undefined", "unknown", "var", "void", "while", "with", "yield",
}

// TypeName reports whether name is one that a type may have: an identifier
// that is none of TypeScript's reserved words and predefined types, nor of
// taken, the names that the module that holds the type has otherwise.
func TypeName(name string, taken ...string) bool {
	return Identifier.MatchString(name) && !slices.Contains(reserved, name) && !slices.Contains(taken, name)
}

// PropertyName returns name as a property's name: as it is if it is an
// identifier, and quoted otherwise.
func PropertyName(name string) string {
	if Identifier.MatchString(name) {
		return name
	}
	return String(name)
}

// String returns s as a string literal, which a JSON string is, without
// escaping HTML.
func String(s string) string {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

// Indent returns the indentation of n levels, two spaces each.
func Indent(n int) string {
	return strings.Repeat("  ", n)
}

// Comment writes text, and @deprecated if deprecated is set, to b as a JSDoc
// comment at the indentation n, on one line when it is one, and nothing when
// there is neither. A */ in text is escaped, so that it does not end the
// comment.
func Comment(b *strings.Builder, n int, text string, deprecated bool) {
	var lines []string
	if text = strings.TrimSpace(text); text != "" {
		lines = strings.Split(text, "\n")
	}
	if deprecated {
		lines = append(lines, "@deprecated")
	}
	if len(lines) == 0 {
		return
	}
	for i, l := range lines {
		lines[i] = strings.ReplaceAll(strings.TrimRight(l, " \t\r"), "*/", "*\\/")
	}
	if len(lines) == 1 {
		fmt.Fprintf(b, "%s/** %s */\n", Indent(n), lines[0])
		return
	}
	fmt.Fprintf(b, "%s/**\n", Indent(n))
	for _, l := range lines {
		if l == "" {
			fmt.Fprintf(b, "%s *\n", Indent(n))
		} else {
			fmt.Fprintf(b, "%s * %s\n", Indent(n), l)
		}
	}
	fmt.Fprintf(b, "%s */\n", Indent(n))
}
