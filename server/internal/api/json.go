package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
)

// maxJSONDepth is how deeply the values of a request body may nest (PRD
// NFR-SEC-07). It leaves room for the deepest payload the API takes, an
// expression's AST of depth 64 (§5.8.6) inside a command batch, while it
// bounds the recursion of everything that walks a body.
const maxJSONDepth = 256

// jsonFormat is huma's format for application/json. Bodies are decoded
// strictly (PRD FR-API-01 AC2): a body must be one JSON value, in UTF-8, with
// no key twice in an object, nested at most maxJSONDepth deep, and decoding
// into the operation's input type rejects a field that the type does not
// have. huma validates the body against the input type's schema, which
// disallows additional properties, before it decodes it, and that is what
// reports an unknown field with its location. It matches field names exactly
// (init), as encoding/json, which ignores their case, does not.
var jsonFormat = huma.Format{
	Marshal:   huma.DefaultJSONFormat.Marshal,
	Unmarshal: unmarshalStrict,
}

// unmarshalStrict decodes the JSON value data into v, or explains why data
// is not a JSON value that the API accepts.
func unmarshalStrict(data []byte, v any) error {
	if err := checkJSON(data); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	return d.Decode(v)
}

// jsonFrame is an object or array that checkJSON is inside.
type jsonFrame struct {
	object bool
	keys   map[string]bool
	// key is the object's current key, and index the array's current
	// element, -1 before the first.
	key   string
	index int
	// inValue is set in an object between a key and the end of its value.
	inValue bool
}

// checkJSON explains why data is not a single JSON value in UTF-8 whose
// objects have unique keys and whose values nest at most maxJSONDepth deep.
// encoding/json would accept such data: it replaces invalid UTF-8, keeps the
// last of two equal keys, and nests up to 10000 deep. The explanation starts
// with the location of the problem, such as body.items[2].name.
func checkJSON(data []byte) error {
	if !utf8.Valid(data) {
		return errors.New("the body is not valid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var path []*jsonFrame
	location := func() string {
		var b strings.Builder
		b.WriteString("body")
		for _, f := range path {
			switch {
			case f.object && f.inValue:
				b.WriteString("." + f.key)
			case !f.object && f.index >= 0:
				b.WriteString("[" + strconv.Itoa(f.index) + "]")
			}
		}
		return b.String()
	}
	// ended is called when a value ends, to leave the key it was the value
	// of.
	ended := func() {
		if n := len(path); n > 0 && path[n-1].object {
			path[n-1].inValue = false
		}
	}
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return errors.New("the body is not valid JSON: it ends before its JSON value does")
		}
		if err != nil {
			return fmt.Errorf("the body is not valid JSON: %w", err)
		}
		var top *jsonFrame
		if n := len(path); n > 0 {
			top = path[n-1]
		}
		switch {
		case top != nil && top.object && !top.inValue && tok != json.Delim('}'):
			// The decoder has checked that a key is a string.
			key, _ := tok.(string)
			top.key, top.inValue = key, true
			if top.keys[key] {
				return fmt.Errorf("%s: the key %s appears more than once in its object", location(), strconv.Quote(key))
			}
			top.keys[key] = true
		case tok == json.Delim('}') || tok == json.Delim(']'):
			path = path[:len(path)-1]
			ended()
		default:
			if top != nil && !top.object {
				top.index++
			}
			if tok == json.Delim('{') || tok == json.Delim('[') {
				if len(path) == maxJSONDepth {
					return fmt.Errorf("%s: the values nest more than %d deep", location(), maxJSONDepth)
				}
				path = append(path, &jsonFrame{object: tok == json.Delim('{'), keys: map[string]bool{}, index: -1})
			} else {
				ended()
			}
		}
		if len(path) == 0 {
			break
		}
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("the body has more after its JSON value")
	}
	return nil
}
