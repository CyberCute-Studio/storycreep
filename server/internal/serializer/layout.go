package serializer

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// Layout returns data, one JSON value, laid out as every file of the
// published format is (PRD §6.2): UTF-8, each object's keys sorted by their
// code points, as encoding/json sorts a map's, which compares their bytes,
// indented by two spaces, with ", \, and the control characters escaped, as
// \", \\, \b, \f, \n, \r, \t, or \u00XX with lowercase hexadecimal digits,
// and U+2028 and U+2029 as their \u escapes, as encoding/json escapes them,
// HTML's <, >, and & not, nor /, and a line feed at its end. It writes each
// number as data writes it, which the types of package published write as
// §6.2 says (Serialize), and of a key that an object has twice the last, as
// a consumer reads it (value.ReadJSON). It fails on data that
// value.CheckJSON refuses, as data that is not one JSON value, or that holds
// null, which no file holds.
func Layout(data []byte) ([]byte, error) {
	tree, err := value.ReadJSON(data)
	if err != nil {
		return nil, fmt.Errorf("serializer: a file is not laid out, since %w", err)
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(tree); err != nil {
		return nil, fmt.Errorf("serializer: a file is not laid out: %w", err)
	}
	return b.Bytes(), nil
}
