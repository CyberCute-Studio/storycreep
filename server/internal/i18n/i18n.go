// Package i18n localises the server's messages (PRD FR-I18N-01, FR-I18N-02,
// ADR-024): the display messages of the API's errors, and later its emails.
//
// Every message is in a catalog, one JSON file per locale in catalog/, with
// stable keys and ICU MessageFormat (messageformat.go says which part of it
// the server formats). English is the source locale, whose catalog has every
// message; another locale's catalog may lack some, which are then shown in
// English. Code names a message with K, whose argument is a string literal:
// catalog extraction (package extract) reads the keys from those calls, and
// keeps the catalogs to exactly the keys the code uses.
//
// A request's locale is negotiated from the user's preference, the request's
// Accept-Language, and the instance's default, in that order, falling back to
// English (FR-I18N-02); a locale the server has no catalog for is skipped.
package i18n

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/text/language"
)

// Source is the source locale, whose catalog has every message.
var Source = language.English

// Key names a message of the catalog. K makes one; the zero Key names none.
type Key struct {
	name string
}

// K returns the key of the message named name. Call it with a string
// literal, as in i18n.K("error.not_found"): catalog extraction reads the
// literal, and fails on a call with anything else, so that every key the
// server uses is in the catalog.
func K(name string) Key {
	return Key{name: name}
}

func (k Key) String() string {
	return k.name
}

// IsZero reports whether k names no message.
func (k Key) IsZero() bool {
	return k.name == ""
}

// Args are the arguments of a message, by name: strings and numbers.
type Args map[string]any

// Message is a message of the catalog and its arguments.
type Message struct {
	Key  Key
	Args Args
}

// IsZero reports whether m names no message.
func (m Message) IsZero() bool {
	return m.Key.IsZero()
}

// Localizer formats messages and numbers in one locale.
type Localizer struct {
	bundle *Bundle
	locale *locale
}

// Locale returns the localizer's locale.
func (l *Localizer) Locale() language.Tag {
	return l.locale.tag
}

// Format returns the message m in the localizer's locale, or in the source
// locale if the locale's catalog lacks it. A key that no catalog has is
// returned as it is, which catalog extraction rules out for the keys in the
// code. A message that cannot be formatted with m's arguments, as when one is
// missing, is returned as the catalog has it, as the web app's i18next does.
func (l *Localizer) Format(m Message) string {
	text, _ := l.Localize(m)
	return text
}

// Localize returns the message m as Format does, and the locale it is in:
// the localizer's, or the source locale's if it is shown in English, as a
// response's Content-Language names it.
func (l *Localizer) Localize(m Message) (string, language.Tag) {
	msg, tag := l.bundle.lookup(l.locale.tag, m.Key.name)
	if msg == nil {
		return m.Key.name, Source
	}
	loc := l.bundle.localizer(tag).locale
	var b strings.Builder
	if err := format(&b, loc, msg.nodes, m.Args, nil); err != nil {
		return msg.source, tag
	}
	return b.String(), tag
}

// Number returns the number v in the localizer's locale, as {v, number}
// formats it in a message: with the locale's separators and at most three
// fraction digits, and NaN and the infinities as Intl.NumberFormat writes
// them, as NaN, ∞, and -∞ in English. It returns a value that is not a number
// as fmt.Sprint does.
func (l *Localizer) Number(v any) string {
	f, ok := toFloat(v)
	if !ok {
		return fmt.Sprint(v)
	}
	return l.locale.format(v, f, 3)
}

type localizerKey struct{}

// WithLocalizer returns a context whose messages are formatted by l.
func WithLocalizer(ctx context.Context, l *Localizer) context.Context {
	return context.WithValue(ctx, localizerKey{}, l)
}

// FromContext returns the localizer of ctx, or one of the default bundle's
// source locale.
func FromContext(ctx context.Context) *Localizer {
	if l, ok := ctx.Value(localizerKey{}).(*Localizer); ok {
		return l
	}
	return Default().Localizer(Source)
}
