package diff

import (
	"bytes"
	"encoding/json"
)

// A scanner of the JSON of a board's file that finds each node's and each
// connection's bytes, and the ID of each, without reading what they hold, so
// that Compare reads into the published format's types only the nodes and
// the connections whose bytes differ between the two sides: a snapshot of
// the large fixture's 10,000 nodes is megabytes of JSON, which decoding
// whole would take most of NFR-PERF-13's two seconds. It reads valid JSON,
// as a snapshot that validates holds, and reports false where it finds
// something that no JSON is, rather than check every rule of JSON.

// space returns the index of the first byte of data from i that is not JSON's
// white space, or len(data).
func space(data []byte, i int) int {
	for i < len(data) {
		switch data[i] {
		case ' ', '\t', '\n', '\r':
			i++
		default:
			return i
		}
	}
	return i
}

// skipString returns the index after the string that starts at data[i], a
// quotation mark, and true, or false if the string does not end.
func skipString(data []byte, i int) (int, bool) {
	for i++; i < len(data); i++ {
		switch data[i] {
		case '\\':
			i++
		case '"':
			return i + 1, true
		}
	}
	return 0, false
}

// skipValue returns the index after the value that starts at data[i], and
// true, or false if no value starts there, or it does not end.
func skipValue(data []byte, i int) (int, bool) {
	if i >= len(data) {
		return 0, false
	}
	switch data[i] {
	case '"':
		return skipString(data, i)
	case '{', '[':
		depth := 0
		for i < len(data) {
			switch data[i] {
			case '"':
				end, ok := skipString(data, i)
				if !ok {
					return 0, false
				}
				i = end
				continue
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return i + 1, true
				}
			}
			i++
		}
		return 0, false
	case '}', ']', ',', ':':
		return 0, false
	}
	start := i
	for ; i < len(data); i++ {
		switch data[i] {
		case ',', '}', ']', ' ', '\t', '\r', '\n':
			return i, i > start
		}
	}
	return i, i > start
}

// eachMember calls f with the key and the value's bytes of each member of the
// JSON object data, in order, until f returns false, and reports whether data
// is an object as far as it read.
func eachMember(data []byte, f func(key string, value []byte) bool) bool {
	i := space(data, 0)
	if i >= len(data) || data[i] != '{' {
		return false
	}
	if i = space(data, i+1); i < len(data) && data[i] == '}' {
		return true
	}
	for {
		if i >= len(data) || data[i] != '"' {
			return false
		}
		end, ok := skipString(data, i)
		if !ok {
			return false
		}
		key, ok := unquote(data[i:end])
		if !ok {
			return false
		}
		if i = space(data, end); i >= len(data) || data[i] != ':' {
			return false
		}
		start := space(data, i+1)
		if end, ok = skipValue(data, start); !ok {
			return false
		}
		if !f(key, data[start:end]) {
			return true
		}
		if i = space(data, end); i >= len(data) {
			return false
		}
		switch data[i] {
		case ',':
			i = space(data, i+1)
		case '}':
			return true
		default:
			return false
		}
	}
}

// elementsOf returns the bytes of each element of the JSON array data, in
// order, and true, or false where data is no array.
func elementsOf(data []byte) ([][]byte, bool) {
	i := space(data, 0)
	if i >= len(data) || data[i] != '[' {
		return nil, false
	}
	var out [][]byte
	if i = space(data, i+1); i < len(data) && data[i] == ']' {
		return out, true
	}
	for {
		end, ok := skipValue(data, i)
		if !ok {
			return nil, false
		}
		out = append(out, data[i:end])
		if i = space(data, end); i >= len(data) {
			return nil, false
		}
		switch data[i] {
		case ',':
			i = space(data, i+1)
		case ']':
			return out, true
		default:
			return nil, false
		}
	}
}

// unquote returns the string that the JSON string raw is, and true, or false
// where it is no string.
func unquote(raw []byte) (string, bool) {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return "", false
	}
	if bytes.IndexByte(raw, '\\') < 0 {
		return string(raw[1 : len(raw)-1]), true
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

// idOf returns the ID of the JSON object raw, its member id's string, the
// last of an object that has it twice, as published.Decode reads a key, and
// true, or false where it has none.
func idOf(raw []byte) (string, bool) {
	var id string
	found := false
	eachMember(raw, func(key string, value []byte) bool {
		if key == "id" {
			id, found = unquote(value)
		}
		return true
	})
	return id, found
}
