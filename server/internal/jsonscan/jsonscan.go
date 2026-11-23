// Package jsonscan walks a JSON document in memory by its punctuation, building
// nothing of it, so that what a parser or a validator would build of an
// untrusted document can be bounded before it is built. Every function but
// Values takes a document that json.Valid has accepted.
package jsonscan

// Values counts the values that a parser would hold of the JSON document data:
// the document, each object and array, and each item after a comma. It
// allocates nothing, and reads a document that is not valid as far as it can,
// which the parser then refuses.
func Values(data []byte) int {
	n := 1
	inString := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inString {
			switch c {
			case '\\':
				i++
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{', '[', ',':
			n++
		}
	}
	return n
}

// HasNull says whether the document holds a null, the only place outside a
// string where an n is valid.
func HasNull(data []byte) bool {
	for i := 0; i < len(data); i++ {
		switch data[i] {
		case '"':
			for i++; i < len(data) && data[i] != '"'; i++ {
				if data[i] == '\\' {
					i++
				}
			}
		case 'n':
			return true
		}
	}
	return false
}

// Depth returns how deeply the values of the document nest.
func Depth(data []byte) int {
	depth, open := 0, 0
	for i := 0; i < len(data); i++ {
		switch data[i] {
		case '"':
			for i++; i < len(data) && data[i] != '"'; i++ {
				if data[i] == '\\' {
					i++
				}
			}
		case '{', '[':
			open++
			depth = max(depth, open)
		case '}', ']':
			open--
		}
	}
	return depth
}

// Keys counts the members of the document's objects: outside a string, a colon
// is a member's and nothing else's.
func Keys(data []byte) int {
	n := 0
	for i := 0; i < len(data); i++ {
		switch data[i] {
		case '"':
			for i++; i < len(data) && data[i] != '"'; i++ {
				if data[i] == '\\' {
					i++
				}
			}
		case ':':
			n++
		}
	}
	return n
}

// SkipSpace returns the offset of the first byte of data from i that is not
// JSON's white space.
func SkipSpace(data []byte, i int) int {
	for i < len(data) && (data[i] == ' ' || data[i] == '\n' || data[i] == '\r' || data[i] == '\t') {
		i++
	}
	return i
}

// ValueEnd returns the offset just past the value that starts at i.
func ValueEnd(data []byte, i int) int {
	if c := data[i]; c != '{' && c != '[' && c != '"' {
		for i < len(data) && !endsScalar(data[i]) {
			i++
		}
		return i
	}
	depth := 0
	for ; ; i++ {
		switch data[i] {
		case '"':
			for i++; data[i] != '"'; i++ {
				if data[i] == '\\' {
					i++
				}
			}
			if depth == 0 {
				return i + 1
			}
		case '{', '[':
			depth++
		case '}', ']':
			if depth--; depth == 0 {
				return i + 1
			}
		}
	}
}

// endsScalar says whether c is a byte that a number, true, false, or null
// cannot hold.
func endsScalar(c byte) bool {
	switch c {
	case ',', ']', '}', ' ', '\n', '\r', '\t':
		return true
	}
	return false
}

// Members calls fn with the key, as it is spelled with its quotes, and the
// value of each member of the object, in their order, until fn fails.
func Members(object []byte, fn func(key, value []byte) error) error {
	i := SkipSpace(object, SkipSpace(object, 0)+1)
	for object[i] != '}' {
		keyEnd := ValueEnd(object, i)
		start := SkipSpace(object, SkipSpace(object, keyEnd)+1)
		end := ValueEnd(object, start)
		if err := fn(object[i:keyEnd], object[start:end]); err != nil {
			return err
		}
		if i = SkipSpace(object, end); object[i] == ',' {
			i = SkipSpace(object, i+1)
		}
	}
	return nil
}

// Elements calls fn with the index and the value of each element of the array,
// in their order, until fn fails.
func Elements(array []byte, fn func(n int, value []byte) error) error {
	i := SkipSpace(array, SkipSpace(array, 0)+1)
	for n := 0; array[i] != ']'; n++ {
		end := ValueEnd(array, i)
		if err := fn(n, array[i:end]); err != nil {
			return err
		}
		if i = SkipSpace(array, end); array[i] == ',' {
			i = SkipSpace(array, i+1)
		}
	}
	return nil
}
