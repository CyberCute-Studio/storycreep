package secret

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

const plain = "hunter2-correct-horse"

type exported struct {
	Name   string
	Secret Value
}

type unexported struct {
	name   string
	secret Value
}

type pointer struct {
	Secret *Value
}

// TestValueNeverPrints covers NFR-SEC-14: no way of printing, logging, or
// encoding a Value shows the secret, including inside other values.
func TestValueNeverPrints(t *testing.T) {
	v := New(plain)
	in := exported{Name: "smtp", Secret: v}
	hidden := unexported{name: "smtp", secret: v}

	outputs := map[string]string{}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%10s", "%T"} {
		outputs["Sprintf "+verb] = fmt.Sprintf(verb, v)
		outputs["Sprintf pointer "+verb] = fmt.Sprintf(verb, &v)
		outputs["Sprintf struct "+verb] = fmt.Sprintf(verb, in)
		outputs["Sprintf unexported field "+verb] = fmt.Sprintf(verb, hidden)
		outputs["Sprintf slice "+verb] = fmt.Sprintf(verb, []Value{v})
		outputs["Sprintf map "+verb] = fmt.Sprintf(verb, map[string]Value{"k": v})
		outputs["Sprintf pointer field "+verb] = fmt.Sprintf(verb, pointer{Secret: &v})
	}
	outputs["Sprint"] = fmt.Sprint(v, in)
	outputs["Sprintln"] = fmt.Sprintln(v, in)
	outputs["error"] = fmt.Errorf("connect: %w: %v", errors.New("refused"), v).Error()

	for name, x := range map[string]any{
		"value": v, "struct": in, "unexported field": hidden, "pointer": &v,
		"map value": map[string]Value{"k": v}, "map key": map[Value]int{v: 1},
	} {
		b, err := json.Marshal(x)
		if err != nil {
			t.Fatalf("json %s: %v", name, err)
		}
		outputs["json "+name] = string(b)
	}
	b, err := xml.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	outputs["xml"] = string(b)

	for name, h := range map[string]func(*bytes.Buffer) slog.Handler{
		"json": func(w *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(w, nil) },
		"text": func(w *bytes.Buffer) slog.Handler { return slog.NewTextHandler(w, nil) },
	} {
		var buf bytes.Buffer
		log := slog.New(h(&buf))
		log.Info("value", "secret", v)
		log.Info("struct", "config", in)
		log.Info("unexported", "config", hidden)
		log.Info("group", slog.Group("smtp", "password", v))
		log.With("secret", v).Info("with")
		log.Info("any", slog.Any("secret", v))
		outputs["slog "+name] = buf.String()
	}

	for name, out := range outputs {
		if strings.Contains(out, plain) {
			t.Errorf("%s shows the secret: %s", name, out)
		}
	}
	for _, name := range []string{"Sprintf %v", "json value", "slog json", "slog text"} {
		if !strings.Contains(outputs[name], Redacted) {
			t.Errorf("%s = %q, want it to contain %q", name, outputs[name], Redacted)
		}
	}
}

func TestReveal(t *testing.T) {
	if got := New(plain).Reveal(); got != plain {
		t.Errorf("Reveal() = %q, want %q", got, plain)
	}
	var zero Value
	if zero.Reveal() != "" || zero.IsSet() {
		t.Errorf("the zero Value should be unset and reveal the empty string")
	}
	if New("").IsSet() {
		t.Errorf("an empty secret should be unset")
	}
	if !New(plain).IsSet() {
		t.Errorf("a non-empty secret should be set")
	}
	if got := zero.String(); got != Redacted {
		t.Errorf("an unset secret prints %q, want %q", got, Redacted)
	}
}
