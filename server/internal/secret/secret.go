// Package secret holds values that must never reach a log, an API response,
// or a support bundle (PRD NFR-SEC-14).
package secret

import (
	"fmt"
	"io"
	"log/slog"
)

// Redacted is what every printed or encoded form of a Value shows.
const Redacted = "[REDACTED]"

// Value is a secret string, such as a password or a key. Printing it with fmt,
// logging it with slog, or encoding it as JSON, XML, or text always yields
// Redacted. Only Reveal returns the value, so every use of the real secret is
// a visible call at the place that needs it.
//
// The zero Value is an unset secret.
type Value struct {
	// p points at the secret instead of holding it. fmt prints a struct's
	// unexported fields by reflection, without calling their methods, so a
	// Value kept in an unexported field would otherwise print its contents.
	// Through a pointer, fmt prints an address.
	p *string
}

// New returns a Value holding s.
func New(s string) Value {
	return Value{p: &s}
}

// Reveal returns the secret. Call it only where the secret is used, never to
// log or display it.
func (v Value) Reveal() string {
	if v.p == nil {
		return ""
	}
	return *v.p
}

// IsSet reports whether the secret is non-empty.
func (v Value) IsSet() bool {
	return v.Reveal() != ""
}

// String returns Redacted.
func (v Value) String() string {
	return Redacted
}

// GoString returns Redacted, for the %#v verb.
func (v Value) GoString() string {
	return Redacted
}

// Format writes Redacted for every verb, including %x and %q, which would
// otherwise bypass String.
func (v Value) Format(f fmt.State, verb rune) {
	_, _ = io.WriteString(f, Redacted)
}

// LogValue returns Redacted, for slog.
func (v Value) LogValue() slog.Value {
	return slog.StringValue(Redacted)
}

// MarshalJSON returns Redacted as a JSON string.
func (v Value) MarshalJSON() ([]byte, error) {
	return []byte(`"` + Redacted + `"`), nil
}

// MarshalText returns Redacted, for encodings such as XML and for JSON map
// keys.
func (v Value) MarshalText() ([]byte, error) {
	return []byte(Redacted), nil
}
