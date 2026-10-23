package variable

import (
	"reflect"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/value"
)

// TestAttributeTypes checks that a variable's default of a type that only an
// attribute has, text or enum, which package value reads, is refused as no
// variable's type, however it is read, so that a payload that its schema did
// not hold to a variable's types is refused at its default rather than by the
// database's check.
func TestAttributeTypes(t *testing.T) {
	t.Parallel()
	for _, typ := range []string{value.Text, value.Enum} {
		for _, c := range []value.Checks{value.All, {Length: true}, {}} {
			if _, problem := of(value.FromJSON([]byte(`"calm"`)), typ, c); !strings.Contains(problem,
				"no variable's type") {
				t.Errorf("a default of the type %s, read with %+v, was answered with %q", typ, c, problem)
			}
		}
		if errs := checkDefault(typ, Value{}); len(errs) != 1 || errs[0].Field != "default" {
			t.Errorf("a variable's create of the type %s without a default was answered with %v", typ, errs)
		}
	}
	if def, problem := of(Value{}, TypeString, value.All); problem != "" || string(def.JSON()) != `""` {
		t.Errorf("a string's zero value is %s, %q", def.JSON(), problem)
	}
}

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
