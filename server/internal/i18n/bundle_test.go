package i18n

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"golang.org/x/text/language"
)

// newTestBundle returns a bundle with an English and a Czech catalog. The
// Czech one lacks board.count, which is then shown in English.
func newTestBundle(t *testing.T) *Bundle {
	t.Helper()
	b, err := Load(fstest.MapFS{
		"en.json": {Data: []byte(`{
  "files.count": "{count, plural, one {# file} other {# files}}",
  "board.count": "{count, plural, one {# board} other {# boards}}",
  "greeting": "Hello, {name}!"
}`)},
		"cs.json": {Data: []byte(`{
  "files.count": "{count, plural, one {# soubor} few {# soubory} many {# souboru} other {# souborů}}",
  "greeting": "Ahoj, {name}!"
}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestDefault checks that the server's own catalogs load: every message
// parses, in the syntax the server formats.
func TestDefault(t *testing.T) {
	b := Default()
	if got := b.Locales(); len(got) == 0 || got[0] != Source {
		t.Fatalf("Locales() = %v, want the source locale first", got)
	}
	if got := b.Localizer(Source).Format(Message{Key: K("error.not_found")}); got != "The resource does not exist." {
		t.Errorf("error.not_found = %q", got)
	}
}

// TestPlurals formats a catalog's messages with the plural rules of English
// and Czech, and their numbers' formats.
//
// Verifies: FR-I18N-01/AC2
func TestPlurals(t *testing.T) {
	b := newTestBundle(t)
	en, cs := b.Localizer(language.English), b.Localizer(language.Czech)
	files := func(n any) Message { return Message{Key: K("files.count"), Args: Args{"count": n}} }
	for _, tt := range []struct {
		l    *Localizer
		m    Message
		want string
	}{
		{en, files(1), "1 file"},
		{en, files(2), "2 files"},
		{en, files(0), "0 files"},
		{en, files(1.5), "1.5 files"},
		{en, files(1200), "1,200 files"},
		{cs, files(1), "1 soubor"},
		{cs, files(3), "3 soubory"},
		{cs, files(5), "5 souborů"},
		{cs, files(1.5), "1,5 souboru"},
		{cs, files(1200), "1" + nbsp + "200 souborů"},
		{cs, Message{Key: K("greeting"), Args: Args{"name": "Ada"}}, "Ahoj, Ada!"},
		// A message that the Czech catalog lacks is in English, with English
		// plural rules.
		{cs, Message{Key: K("board.count"), Args: Args{"count": 3}}, "3 boards"},
	} {
		if got := tt.l.Format(tt.m); got != tt.want {
			t.Errorf("%s in %s = %q, want %q", tt.m.Key, tt.l.Locale(), got, tt.want)
		}
	}
}

func TestFormatFallbacks(t *testing.T) {
	en := newTestBundle(t).Localizer(language.English)
	// A key that no catalog has is shown as it is.
	if got := en.Format(Message{Key: K("no.such.key")}); got != "no.such.key" {
		t.Errorf("an unknown key = %q", got)
	}
	// A message whose argument is missing is shown as the catalog has it.
	if got := en.Format(Message{Key: K("greeting")}); got != "Hello, {name}!" {
		t.Errorf("a message without its argument = %q", got)
	}
}

func TestNegotiate(t *testing.T) {
	b := newTestBundle(t)
	en, cs := language.English, language.Czech
	tests := []struct {
		name               string
		preference, accept string
		instanceDefault    language.Tag
		want               language.Tag
	}{
		{"nothing given", "", "", language.Und, en},
		{"the preference first", "cs", "en", en, cs},
		{"a regional preference", "cs-CZ", "", en, cs},
		{"Accept-Language", "", "cs", en, cs},
		{"Accept-Language by quality", "", "en;q=0.5, cs;q=0.9", en, cs},
		{"Accept-Language skips what has no catalog", "", "de, cs;q=0.8, en;q=0.5", en, cs},
		{"Accept-Language skips quality 0", "", "cs;q=0, en;q=0.1", cs, en},
		{"the instance default last", "", "", cs, cs},
		{"a regional variant of English", "", "en-GB", cs, en},
		{"Slovak only resembles Czech", "", "sk", en, en},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := b.Negotiate(tt.preference, tt.accept, tt.instanceDefault); got != tt.want {
				t.Errorf("Negotiate(%q, %q, %s) = %s, want %s", tt.preference, tt.accept, tt.instanceDefault, got, tt.want)
			}
		})
	}
}

// TestUnknownLocale checks that a locale the server has no catalog for, or a
// tag that is not one, in any step of the negotiation, falls back to English,
// whose messages are then shown, without an error.
//
// Verifies: FR-I18N-02/AC2
func TestUnknownLocale(t *testing.T) {
	b := newTestBundle(t)
	for _, tt := range []struct {
		preference, accept string
		instanceDefault    language.Tag
	}{
		{"fr", "", language.Und},
		{"xx-YY", "", language.Und},
		{"not a tag!", "", language.Und},
		{"", "fr-CA, de;q=0.8", language.Und},
		{"", "*", language.Und},
		{"", "en;q=nonsense,,;", language.Und},
		{"", strings.Repeat("x-", 1000), language.Und},
		{"", "", language.French},
		{"fr", "de", language.Spanish},
	} {
		got := b.Negotiate(tt.preference, tt.accept, tt.instanceDefault)
		if got != language.English {
			t.Errorf("Negotiate(%q, %q, %s) = %s, want en", tt.preference, tt.accept, tt.instanceDefault, got)
		}
		if msg := b.Localizer(got).Format(Message{Key: K("greeting"), Args: Args{"name": "Ada"}}); msg != "Hello, Ada!" {
			t.Errorf("the greeting in %s = %q", got, msg)
		}
	}
	// A localizer asked for directly by a tag without a catalog is English
	// too.
	if got := b.Localizer(language.French).Locale(); got != language.English {
		t.Errorf("Localizer(fr) is in %s, want en", got)
	}
}

func TestLoadProblems(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"no source catalog", map[string]string{"cs.json": `{}`}, []string{"no catalog of the source locale, en.json"}},
		{"a file name that is not a tag", map[string]string{"en.json": `{}`, "english.json": `{}`},
			[]string{"english.json: the name is not a locale's BCP 47 tag"}},
		{"a tag that is not canonical", map[string]string{"en.json": `{}`, "cs-cz.json": `{}`},
			[]string{"cs-cz.json: the name is not a locale's BCP 47 tag"}},
		{"not an object", map[string]string{"en.json": `["a"]`}, []string{"en.json: the file is not a JSON object"}},
		{"a value that is not a string", map[string]string{"en.json": `{"a": 1}`},
			[]string{"en.json: a: the message is not a string"}},
		{"a key twice", map[string]string{"en.json": `{"a": "x", "a": "y"}`},
			[]string{"en.json: a: the key is in the file twice"}},
		{"two values", map[string]string{"en.json": `{} {}`}, []string{"en.json: the file has more than one JSON value"}},
		{"an empty source message", map[string]string{"en.json": `{"a": ""}`},
			[]string{"en.json: a: the message is empty"}},
		{"a message that does not parse", map[string]string{"en.json": `{"a": "{n, plural, one {#}}"}`},
			[]string{"en.json: a: at character", "n has no other option"}},
		{"a translation of a key the source lacks", map[string]string{"en.json": `{}`, "cs.json": `{"a": "x"}`},
			[]string{"cs.json: a: the source catalog, en.json, has no such key"}},
		{"a translation with an argument the source lacks", map[string]string{
			"en.json": `{"a": "Hello, {name}!"}`, "cs.json": `{"a": "Ahoj, {nick}!"}`},
			[]string{"cs.json: a: the argument nick is not in the source message"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := fstest.MapFS{}
			for name, data := range tt.files {
				fsys[name] = &fstest.MapFile{Data: []byte(data)}
			}
			_, err := Load(fsys)
			if err == nil {
				t.Fatal("Load succeeded, want an error")
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Load: %v, want it to say %q", err, want)
				}
			}
		})
	}
}

// TestEmptyTranslation checks that a message left empty in a translation is
// missing there, so that the English one is shown.
func TestEmptyTranslation(t *testing.T) {
	b, err := Load(fstest.MapFS{
		"en.json": {Data: []byte(`{"a": "Saved."}`)},
		"cs.json": {Data: []byte(`{"a": ""}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := b.Localizer(language.Czech).Format(Message{Key: K("a")}); got != "Saved." {
		t.Errorf("an empty translation = %q, want the English message", got)
	}
}

func TestContext(t *testing.T) {
	if got := FromContext(context.Background()).Locale(); got != Source {
		t.Errorf("FromContext without a localizer is in %s, want %s", got, Source)
	}
	cs := newTestBundle(t).Localizer(language.Czech)
	if got := FromContext(WithLocalizer(context.Background(), cs)); got != cs {
		t.Errorf("FromContext = %v, want the context's localizer", got)
	}
}
