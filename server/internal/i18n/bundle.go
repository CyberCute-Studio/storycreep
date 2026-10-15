package i18n

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/text/language"
)

// catalogs are the server's catalogs, one per locale, named after it, as
// en.json.
//
//go:embed catalog/*.json
var catalogs embed.FS

// Default returns the bundle of the server's own catalogs. It panics if they
// are invalid, which a test rules out.
var Default = sync.OnceValue(func() *Bundle {
	sub, err := fs.Sub(catalogs, "catalog")
	if err != nil {
		panic(err)
	}
	b, err := Load(sub)
	if err != nil {
		panic("i18n: the server's catalogs are invalid: " + err.Error())
	}
	return b
})

// Bundle is a set of catalogs, one per locale, of which the source locale's
// has every message.
type Bundle struct {
	// locales are the locales that have a catalog, the source locale first.
	locales    []language.Tag
	catalogs   map[language.Tag]map[string]*compiled
	matcher    language.Matcher
	localizers map[language.Tag]*Localizer
}

// compiled is a message of a catalog, parsed.
type compiled struct {
	source string
	nodes  []node
}

// Load reads the catalogs in fsys: a file named after each locale's BCP 47
// tag, as en.json or cs.json, that holds a JSON object of messages by key,
// with the keys in any order. The source locale's catalog must have every key
// that another catalog has, and a message in another catalog may use only
// the arguments that the key's source message uses. A message that is empty
// in another catalog is missing there. Load reports every problem it finds,
// each with its file and key.
func Load(fsys fs.FS) (*Bundle, error) {
	files, err := fs.Glob(fsys, "*.json")
	if err != nil {
		return nil, err
	}
	b := &Bundle{catalogs: map[language.Tag]map[string]*compiled{}, localizers: map[language.Tag]*Localizer{}}
	var problems []error
	for _, file := range files {
		name := strings.TrimSuffix(file, ".json")
		tag, err := language.Parse(name)
		if err != nil || tag.String() != name {
			problems = append(problems, fmt.Errorf("%s: the name is not a locale's BCP 47 tag, such as en or cs", file))
			continue
		}
		src, err := fs.ReadFile(fsys, file)
		if err != nil {
			return nil, err
		}
		messages, err := ReadCatalog(src)
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", file, err))
			continue
		}
		catalog := map[string]*compiled{}
		for _, key := range slices.Sorted(maps.Keys(messages)) {
			msg := messages[key]
			if msg == "" && tag != Source {
				continue
			}
			nodes, err := parseMessage(msg)
			if err == nil && msg == "" {
				err = errors.New("the message is empty")
			}
			if err != nil {
				// The key stays, so that a translation of it is not also
				// reported as a key that the source catalog lacks.
				problems = append(problems, fmt.Errorf("%s: %s: %w", file, key, err))
			}
			catalog[key] = &compiled{source: msg, nodes: nodes}
		}
		b.catalogs[tag] = catalog
		b.locales = append(b.locales, tag)
	}
	source, ok := b.catalogs[Source]
	if !ok {
		problems = append(problems, fmt.Errorf("there is no catalog of the source locale, %s.json", Source))
	}
	for _, tag := range b.locales {
		if tag == Source || !ok {
			continue
		}
		for _, key := range slices.Sorted(maps.Keys(b.catalogs[tag])) {
			src := source[key]
			if src == nil {
				problems = append(problems, fmt.Errorf("%s.json: %s: the source catalog, %s.json, has no such key",
					tag, key, Source))
				continue
			}
			used, allowed := map[string]bool{}, map[string]bool{}
			arguments(b.catalogs[tag][key].nodes, used)
			arguments(src.nodes, allowed)
			for _, arg := range slices.Sorted(maps.Keys(used)) {
				if !allowed[arg] {
					problems = append(problems, fmt.Errorf("%s.json: %s: the argument %s is not in the source message",
						tag, key, arg))
				}
			}
		}
	}
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}
	// The source locale first: the matcher falls back to it.
	slices.SortFunc(b.locales, func(x, y language.Tag) int {
		switch {
		case x == Source:
			return -1
		case y == Source:
			return 1
		}
		return strings.Compare(x.String(), y.String())
	})
	b.matcher = language.NewMatcher(b.locales)
	for _, tag := range b.locales {
		b.localizers[tag] = &Localizer{bundle: b, locale: newLocale(tag)}
	}
	return b, nil
}

// ReadCatalog reads a catalog's JSON: an object whose values are strings, with
// no key twice. It does not parse the messages.
func ReadCatalog(src []byte) (map[string]string, error) {
	if !utf8.Valid(src) {
		return nil, errors.New("the file is not UTF-8")
	}
	dec := json.NewDecoder(bytes.NewReader(src))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, errors.New("the file is not a JSON object of messages by key")
	}
	messages := map[string]string{}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key := t.(string) // an object's keys are strings
		var msg any
		if err := dec.Decode(&msg); err != nil {
			return nil, err
		}
		s, ok := msg.(string)
		if !ok {
			return nil, fmt.Errorf("%s: the message is not a string", key)
		}
		if _, dup := messages[key]; dup {
			return nil, fmt.Errorf("%s: the key is in the file twice", key)
		}
		if key == "" {
			return nil, errors.New("a key is empty")
		}
		messages[key] = s
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("the file has more than one JSON value")
	}
	return messages, nil
}

// Locales returns the locales that have a catalog, the source locale first.
func (b *Bundle) Locales() []language.Tag {
	return slices.Clone(b.locales)
}

// Localizer returns the localizer of the locale that tag matches, or of the
// source locale if tag matches none.
func (b *Bundle) Localizer(tag language.Tag) *Localizer {
	if m, ok := b.Match(tag); ok {
		tag = m
	} else {
		tag = Source
	}
	return b.localizer(tag)
}

func (b *Bundle) localizer(tag language.Tag) *Localizer {
	return b.localizers[tag]
}

// lookup returns the message key in the locale tag, or in the source locale
// if tag's catalog lacks it, with the locale it is in, or nil if no catalog
// has it.
func (b *Bundle) lookup(tag language.Tag, key string) (*compiled, language.Tag) {
	if m := b.catalogs[tag][key]; m != nil {
		return m, tag
	}
	return b.catalogs[Source][key], Source
}

// Match returns the locale with a catalog that tag asks for: the same
// language, such as en for en-GB, in a variant that its speakers read. It
// reports false if no locale matches that closely, as for sk, which only
// resembles cs.
func (b *Bundle) Match(tag language.Tag) (language.Tag, bool) {
	if tag == language.Und {
		return language.Und, false
	}
	_, i, conf := b.matcher.Match(tag)
	if conf < language.High {
		return language.Und, false
	}
	return b.locales[i], true
}

// Negotiate returns the locale of a request (PRD FR-I18N-02): the first that
// matches a catalog of preference, the user's preferred locale as a BCP 47
// tag; acceptLanguage, the request's Accept-Language header, by quality; and
// instanceDefault; or else the source locale. A tag that is invalid or that
// no catalog matches is skipped, so an unknown locale falls back without an
// error.
func (b *Bundle) Negotiate(preference, acceptLanguage string, instanceDefault language.Tag) language.Tag {
	if preference != "" {
		if tag, err := language.Parse(preference); err == nil {
			if m, ok := b.Match(tag); ok {
				return m
			}
		}
	}
	if acceptLanguage != "" {
		// An invalid header is ignored as a whole, as x/text returns no tags
		// for it.
		tags, qs, _ := language.ParseAcceptLanguage(acceptLanguage)
		for i, tag := range tags {
			if qs[i] <= 0 {
				continue
			}
			if m, ok := b.Match(tag); ok {
				return m
			}
		}
	}
	if m, ok := b.Match(instanceDefault); ok {
		return m
	}
	return Source
}
