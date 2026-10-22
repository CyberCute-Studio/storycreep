package board

import (
	"reflect"
	"strings"
	"testing"
)

// TestSchemaEnums checks that the colours of a board and the kinds of folder
// that the package lists are those that the schemas of the commands'
// payloads and of the API's answers take, which their enum tags give, so
// that a colour or a kind added to the list and not to a schema, or to a
// schema and not to the list, fails. TestEnumerations checks the database's.
func TestSchemaEnums(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		typ   reflect.Type
		field string
		want  []string
	}{
		{reflect.TypeFor[createPayload](), "Color", Colors},
		{reflect.TypeFor[colorPayload](), "Color", Colors},
		{reflect.TypeFor[Board](), "Color", Colors},
		{reflect.TypeFor[createFolderPayload](), "Kind", Kinds},
		{reflect.TypeFor[Folder](), "Kind", Kinds},
	} {
		f, ok := c.typ.FieldByName(c.field)
		if got, want := f.Tag.Get("enum"), strings.Join(c.want, ","); !ok || got != want {
			t.Errorf("%s.%s takes %q, want %q", c.typ.Name(), c.field, got, want)
		}
	}
}
