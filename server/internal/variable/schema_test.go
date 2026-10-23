package variable

import (
	"reflect"
	"strings"
	"testing"
)

// TestSchemaEnums checks that the types and the scopes of a variable that the
// package lists are those that the schemas of the commands' payloads and of
// the API's answers take, which their enum tags give, so that a type or a
// scope added to the list and not to a schema, or to a schema and not to the
// list, fails. TestEnumerations checks the database's.
func TestSchemaEnums(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		typ   reflect.Type
		field string
		want  []string
	}{
		{reflect.TypeFor[createPayload](), "Type", Types()},
		{reflect.TypeFor[retypePayload](), "Type", Types()},
		{reflect.TypeFor[Variable](), "Type", Types()},
		{reflect.TypeFor[createPayload](), "Scope", Scopes()},
		{reflect.TypeFor[scopePayload](), "Scope", Scopes()},
		{reflect.TypeFor[Variable](), "Scope", Scopes()},
	} {
		f, ok := c.typ.FieldByName(c.field)
		if got, want := f.Tag.Get("enum"), strings.Join(c.want, ","); !ok || got != want {
			t.Errorf("%s.%s takes %q, want %q", c.typ.Name(), c.field, got, want)
		}
	}
}
