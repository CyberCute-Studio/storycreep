package schemagen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// file is a document as Document writes it.
type file struct {
	Schema      string             `json:"$schema"`
	ID          string             `json:"$id"`
	Title       string             `json:"title"`
	Description string             `json:"description"`
	Ref         string             `json:"$ref"`
	Defs        map[string]*Schema `json:"$defs"`
	Examples    []json.RawMessage  `json:"examples"`
}

// readFile reads a document that Document wrote, and refuses a keyword that
// it does not write.
func readFile(data []byte) (*file, error) {
	var f file
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, err
	}
	if f.Schema != Dialect || RefName(f.Ref) == "" || f.Defs[RefName(f.Ref)] == nil {
		return nil, errors.New("the document is not of JSON Schema 2020-12, or its $ref is to none of its $defs")
	}
	return &f, nil
}

// TypeScript returns a TypeScript module of the types of the documents, as
// Document wrote them, which header starts: a type for each definition of
// their $defs, named after it, an interface for an object and a type alias
// for anything else, with each description as a JSDoc comment. A definition
// that two documents hold must be the same in both, as it is when one
// Generator wrote them.
//
// An object's type has its properties, an optional one with ?, and one that
// it may not have (False) as never, and nothing of the properties that it
// does not define, which TypeScript's structural types take anyway; a map is
// an index signature; and a value of a oneOf is a union. It fails, rather
// than write a type that is wrong, on a definition whose name is no
// identifier of TypeScript or is one of its reserved words, and on a schema
// that is neither of a type, a reference, a const, an enum, nor a oneOf.
func TypeScript(header string, documents ...[]byte) ([]byte, error) {
	defs := map[string]*Schema{}
	for i, data := range documents {
		f, err := readFile(data)
		if err != nil {
			return nil, fmt.Errorf("schemagen: document %d: %w", i, err)
		}
		for name, s := range f.Defs {
			if have, ok := defs[name]; ok {
				a, _ := have.MarshalJSON()
				b, _ := s.MarshalJSON()
				if !bytes.Equal(a, b) {
					return nil, fmt.Errorf("schemagen: two documents define %s differently", name)
				}
			}
			defs[name] = s
		}
	}
	var b strings.Builder
	b.WriteString(header)
	for _, name := range slices.Sorted(maps.Keys(defs)) {
		if !tsIdentifier.MatchString(name) || slices.Contains(tsReserved, name) {
			return nil, fmt.Errorf("schemagen: %s is not a name that a TypeScript type may have", name)
		}
		s := defs[name]
		t, err := tsType(s, 0)
		if err != nil {
			return nil, fmt.Errorf("schemagen: %s: %w", name, err)
		}
		b.WriteString("\n")
		tsComment(&b, 0, s.Description)
		if s.Type == "object" && len(s.Properties) > 0 {
			fmt.Fprintf(&b, "export interface %s %s\n", name, t)
		} else {
			fmt.Fprintf(&b, "export type %s = %s;\n", name, t)
		}
	}
	return []byte(b.String()), nil
}

// tsIdentifier matches the names that a TypeScript type may have.
var tsIdentifier = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// tsReserved are TypeScript's reserved words and predefined types, which no
// type may be named.
var tsReserved = []string{
	"any", "Array", "as", "bigint", "boolean", "break", "case", "catch", "class", "const", "continue",
	"debugger", "declare", "default", "delete", "do", "else", "enum", "export", "extends", "false", "finally",
	"for", "from", "function", "if", "implements", "import", "in", "instanceof", "interface", "let", "never",
	"new", "null", "number", "object", "package", "private", "protected", "public", "Record", "return",
	"static", "string", "super", "switch", "symbol", "this", "throw", "true", "try", "type", "typeof",
	"undefined", "unknown", "var", "void", "while", "with", "yield",
}

// tsType returns the TypeScript type of the values of s, whose lines after
// the first are indented by n.
func tsType(s *Schema, n int) (string, error) {
	switch {
	case s.False:
		return "never", nil
	case s.Ref != "":
		name := RefName(s.Ref)
		if name == "" {
			return "", fmt.Errorf("%s refers to no definition of the document", s.Ref)
		}
		return name, nil
	case len(s.OneOf) > 0:
		parts := make([]string, len(s.OneOf))
		for i, m := range s.OneOf {
			t, err := tsType(m, n)
			if err != nil {
				return "", err
			}
			parts[i] = t
		}
		return strings.Join(parts, " | "), nil
	case s.Const != nil:
		return tsLiteral(s.Const)
	case s.Enum != nil:
		parts := make([]string, len(s.Enum))
		for i, v := range s.Enum {
			lit, err := tsLiteral(v)
			if err != nil {
				return "", err
			}
			parts[i] = lit
		}
		return strings.Join(parts, " | "), nil
	}
	switch s.Type {
	case "string":
		return "string", nil
	case "integer", "number":
		return "number", nil
	case "boolean":
		return "boolean", nil
	case "array":
		if s.Items == nil {
			return "", errors.New("an array has no items")
		}
		items, err := tsType(s.Items, n)
		if err != nil {
			return "", err
		}
		return "Array<" + items + ">", nil
	case "object":
		return tsObject(s, n)
	}
	return "", fmt.Errorf("a schema is neither of a type, a reference, a const, an enum, nor a oneOf")
}

// tsObject returns the type of an object: its properties, or the index
// signature of a map.
func tsObject(s *Schema, n int) (string, error) {
	if len(s.Properties) == 0 {
		if s.AdditionalProperties == nil || s.AdditionalProperties.False {
			return "", errors.New("an object has neither properties nor a schema of its keys' values")
		}
		v, err := tsType(s.AdditionalProperties, n)
		if err != nil {
			return "", err
		}
		return "{ [key: string]: " + v + " }", nil
	}
	required := map[string]bool{}
	for _, r := range s.Required {
		required[r] = true
	}
	var b strings.Builder
	b.WriteString("{\n")
	for _, name := range slices.Sorted(maps.Keys(s.Properties)) {
		p := s.Properties[name]
		t, err := tsType(p, n+1)
		if err != nil {
			return "", fmt.Errorf("%s: %w", name, err)
		}
		tsComment(&b, n+1, p.Description)
		optional := "?"
		if required[name] && !p.False {
			optional = ""
		}
		fmt.Fprintf(&b, "%s%s%s: %s;\n", tsIndent(n+1), tsPropertyName(name), optional, t)
	}
	b.WriteString(tsIndent(n) + "}")
	return b.String(), nil
}

// tsLiteral returns the literal type of a const's or an enum's value.
func tsLiteral(v any) (string, error) {
	switch v := v.(type) {
	case string:
		return tsString(v), nil
	case bool:
		if v {
			return "true", nil
		}
		return "false", nil
	case json.Number:
		return v.String(), nil
	case float64:
		data, err := json.Marshal(v)
		return string(data), err
	}
	return "", fmt.Errorf("the const or enum value %v is not a string, a number, or a boolean", v)
}

// tsPropertyName returns name as a property's name: as it is if it is an
// identifier, and quoted otherwise.
func tsPropertyName(name string) string {
	if tsIdentifier.MatchString(name) {
		return name
	}
	return tsString(name)
}

// tsString returns s as a string literal, which a JSON string is.
func tsString(s string) string {
	data, _ := marshal(s)
	return string(data)
}

func tsIndent(n int) string {
	return strings.Repeat("  ", n)
}

// tsComment writes text to b as a JSDoc comment at the indentation n.
func tsComment(b *strings.Builder, n int, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = strings.ReplaceAll(strings.TrimRight(l, " \t\r"), "*/", "*\\/")
	}
	if len(lines) == 1 {
		fmt.Fprintf(b, "%s/** %s */\n", tsIndent(n), lines[0])
		return
	}
	fmt.Fprintf(b, "%s/**\n", tsIndent(n))
	for _, l := range lines {
		if l == "" {
			fmt.Fprintf(b, "%s *\n", tsIndent(n))
		} else {
			fmt.Fprintf(b, "%s * %s\n", tsIndent(n), l)
		}
	}
	fmt.Fprintf(b, "%s */\n", tsIndent(n))
}
