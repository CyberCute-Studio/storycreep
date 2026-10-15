package i18n

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/feature/plural"
)

// The server's messages are in ICU MessageFormat, read as the web app's
// i18next-icu reads them (intl-messageformat with ignoreTag), so that one
// message renders the same on both sides; the vectors in schema/i18n hold
// both to it. The server supports a subset of the syntax, and Load refuses a
// message outside it:
//
//   - text, with ICU's apostrophe quoting: '' is an apostrophe, and an
//     apostrophe before {, }, <, >, or, in a plural, #, starts quoted text
//     that ends at the next lone apostrophe;
//   - {name}, which renders a string as it is and a number as JavaScript's
//     String does;
//   - {name, number} and {name, number, integer}, a number in the locale's
//     format, with at most three fraction digits or none;
//   - {name, plural, …} and {name, selectordinal, …}, with an optional
//     offset:n, exact matches such as =0, the locale's CLDR categories, and
//     # for the number less the offset;
//   - {name, select, …}.
//
// A plural or select must have an other option. Dates, times, and number
// skeletons or other styles are not supported yet.

// node is an element of a parsed message: a literal, pound, or *argument.
type node any

// literal is text, with its quoting resolved.
type literal string

// pound is # in an option of a plural or selectordinal: the number.
type pound struct{}

// argKind is the type of an argument.
type argKind int

const (
	argSimple argKind = iota
	argNumber
	argPlural
	argOrdinal // selectordinal
	argSelect
)

// argument is {name …}.
type argument struct {
	name string
	kind argKind
	// integer is set for {name, number, integer}.
	integer bool
	// offset is a plural's offset.
	offset int64
	// options are a plural's or select's options, in the order written.
	options []option
}

// option is one option of a plural or select: its selector, such as one,
// =0, or a select's value, and its message.
type option struct {
	selector string
	message  []node
}

// maxSafeInteger is the largest integer that intl-messageformat accepts as
// an offset or an exact match, JavaScript's Number.MAX_SAFE_INTEGER.
const maxSafeInteger = 1<<53 - 1

// parseMessage parses an ICU message, as intl-messageformat's parser does
// with ignoreTag and requiresOtherClause.
func parseMessage(src string) ([]node, error) {
	p := &parser{src: []rune(src)}
	nodes, err := p.message(0, argSimple)
	if err != nil {
		return nil, fmt.Errorf("at character %d: %w", p.pos+1, err)
	}
	return nodes, nil
}

type parser struct {
	src []rune
	pos int
}

func (p *parser) eof() bool { return p.pos >= len(p.src) }

// at returns the character at p.pos+i, or -1 past the end.
func (p *parser) at(i int) rune {
	if p.pos+i >= len(p.src) {
		return -1
	}
	return p.src[p.pos+i]
}

func (p *parser) skipSpace() {
	for !p.eof() && unicode.Is(unicode.Pattern_White_Space, p.at(0)) {
		p.pos++
	}
}

// accept consumes c if it is next.
func (p *parser) accept(c rune) bool {
	if p.at(0) != c {
		return false
	}
	p.pos++
	return true
}

// identifier reads an argument name, an argument type, or a selector:
// characters that are neither white space nor pattern syntax, which includes
// most ASCII punctuation, such as - and ., but not _.
func (p *parser) identifier() string {
	start := p.pos
	for !p.eof() {
		c := p.at(0)
		if unicode.Is(unicode.White_Space, c) || unicode.Is(unicode.Pattern_Syntax, c) {
			break
		}
		p.pos++
	}
	return string(p.src[start:p.pos])
}

// inPlural reports whether # is the number in a message of the kind of
// argument parent, which it is directly in a plural's or selectordinal's
// options only.
func inPlural(parent argKind) bool {
	return parent == argPlural || parent == argOrdinal
}

// message reads a message up to the end of the source or, nested in an
// option (nesting > 0), up to the "}" that closes it.
func (p *parser) message(nesting int, parent argKind) ([]node, error) {
	var out []node
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			out = append(out, literal(text.String()))
			text.Reset()
		}
	}
	for !p.eof() {
		switch c := p.at(0); {
		case c == '{':
			flush()
			arg, err := p.argument(nesting)
			if err != nil {
				return nil, err
			}
			out = append(out, arg)
		case c == '}' && nesting > 0:
			flush()
			return out, nil
		case c == '#' && inPlural(parent):
			flush()
			p.pos++
			out = append(out, pound{})
		case c == '\'':
			text.WriteString(p.quote(parent))
		default:
			// Everything else is text, "}" at the top level and "<" included:
			// tags are not parsed.
			text.WriteRune(c)
			p.pos++
		}
	}
	flush()
	return out, nil
}

// quote reads an apostrophe at p.pos and what it quotes: ” is an
// apostrophe, and an apostrophe before a character that would otherwise be
// syntax quotes the text up to the next lone apostrophe, or to the end. Any
// other apostrophe is itself.
func (p *parser) quote(parent argKind) string {
	switch p.at(1) {
	case '\'':
		p.pos += 2
		return "'"
	case '{', '}', '<', '>':
	case '#':
		if !inPlural(parent) {
			p.pos++
			return "'"
		}
	default:
		p.pos++
		return "'"
	}
	p.pos++ // the apostrophe
	var b strings.Builder
	b.WriteRune(p.at(0))
	p.pos++
	for !p.eof() {
		c := p.at(0)
		if c == '\'' {
			if p.at(1) != '\'' {
				p.pos++ // the closing apostrophe
				break
			}
			p.pos++ // the first of two
		}
		b.WriteRune(c)
		p.pos++
	}
	return b.String()
}

var (
	errUnclosedArgument = errors.New("an argument is not closed with }")
	errEmptyArgument    = errors.New("an argument has no name")
)

// argument reads an argument, from its "{" to its "}".
func (p *parser) argument(nesting int) (*argument, error) {
	p.pos++ // {
	p.skipSpace()
	if p.eof() {
		return nil, errUnclosedArgument
	}
	if p.at(0) == '}' {
		return nil, errEmptyArgument
	}
	name := p.identifier()
	if name == "" {
		return nil, fmt.Errorf("an argument name cannot start with %q", p.at(0))
	}
	p.skipSpace()
	switch {
	case p.eof():
		return nil, errUnclosedArgument
	case p.accept('}'):
		return &argument{name: name, kind: argSimple}, nil
	case !p.accept(','):
		return nil, fmt.Errorf("the argument %s is followed by %q, not , or }", name, p.at(0))
	}
	p.skipSpace()
	if p.eof() {
		return nil, errUnclosedArgument
	}
	typ := p.identifier()
	arg := &argument{name: name}
	switch typ {
	case "":
		return nil, fmt.Errorf("the argument %s has no type after its comma", name)
	case "number", "date", "time":
		p.skipSpace()
		style := ""
		if p.accept(',') {
			p.skipSpace()
			s, err := p.simpleStyle()
			if err != nil {
				return nil, err
			}
			if style = strings.TrimRightFunc(s, unicode.IsSpace); style == "" {
				return nil, fmt.Errorf("the argument %s has no style after its second comma", name)
			}
		}
		if !p.accept('}') {
			return nil, errUnclosedArgument
		}
		if typ != "number" {
			return nil, fmt.Errorf("the argument %s is a %s, which the server cannot format", name, typ)
		}
		switch style {
		case "":
		case "integer":
			arg.integer = true
		default:
			return nil, fmt.Errorf("the argument %s has the number style %q, which the server cannot format; "+
				"it formats {%s, number} and {%s, number, integer}", name, style, name, name)
		}
		arg.kind = argNumber
		return arg, nil
	case "plural", "selectordinal", "select":
		arg.kind = map[string]argKind{"plural": argPlural, "selectordinal": argOrdinal, "select": argSelect}[typ]
		p.skipSpace()
		if !p.accept(',') {
			return nil, fmt.Errorf("the %s %s has no comma before its options", typ, name)
		}
		p.skipSpace()
		selector := p.identifier()
		if arg.kind != argSelect && selector == "offset" {
			if !p.accept(':') {
				return nil, fmt.Errorf("the offset of %s has no colon", name)
			}
			p.skipSpace()
			n, err := p.integer()
			if err != nil {
				return nil, fmt.Errorf("the offset of %s: %w", name, err)
			}
			arg.offset = n
			p.skipSpace()
			selector = p.identifier()
		}
		if err := p.options(nesting, arg, selector); err != nil {
			return nil, err
		}
		if !p.accept('}') {
			return nil, errUnclosedArgument
		}
		return arg, nil
	default:
		return nil, fmt.Errorf("the argument %s has the type %q, which is not number, plural, selectordinal, "+
			"or select", name, typ)
	}
}

// simpleStyle reads the style of a number, date, or time argument, up to its
// closing "}": quoted text, and braces in pairs, are part of it.
func (p *parser) simpleStyle() (string, error) {
	start, depth := p.pos, 0
	for !p.eof() {
		switch p.at(0) {
		case '\'':
			p.pos++
			end := p.pos
			for end < len(p.src) && p.src[end] != '\'' {
				end++
			}
			if end == len(p.src) {
				return "", errors.New("an argument style has an apostrophe that is not closed")
			}
			p.pos = end + 1
			continue
		case '{':
			depth++
		case '}':
			if depth == 0 {
				return string(p.src[start:p.pos]), nil
			}
			depth--
		}
		p.pos++
	}
	return string(p.src[start:p.pos]), nil
}

// integer reads an integer with an optional sign, which must be a safe
// integer in JavaScript.
func (p *parser) integer() (int64, error) {
	start := p.pos
	if !p.accept('+') {
		p.accept('-')
	}
	digits := p.pos
	for !p.eof() && p.at(0) >= '0' && p.at(0) <= '9' {
		p.pos++
	}
	if p.pos == digits {
		return 0, errors.New("expected a number")
	}
	n, err := strconv.ParseInt(string(p.src[start:p.pos]), 10, 64)
	if err != nil || n > maxSafeInteger || n < -maxSafeInteger {
		return 0, fmt.Errorf("the number %s is too large", string(p.src[start:p.pos]))
	}
	return n, nil
}

// options reads the options of a plural, selectordinal, or select, whose
// first selector has been read.
func (p *parser) options(nesting int, arg *argument, selector string) error {
	seen := map[string]bool{}
	for {
		if selector == "" {
			if arg.kind == argSelect || p.at(0) != '=' {
				break
			}
			start := p.pos
			p.pos++
			if _, err := p.integer(); err != nil {
				return fmt.Errorf("an exact match of %s: %w", arg.name, err)
			}
			selector = string(p.src[start:p.pos])
		}
		if seen[selector] {
			return fmt.Errorf("%s has the option %s twice", arg.name, selector)
		}
		seen[selector] = true
		p.skipSpace()
		if !p.accept('{') {
			return fmt.Errorf("the option %s of %s has no message in braces", selector, arg.name)
		}
		msg, err := p.message(nesting+1, arg.kind)
		if err != nil {
			return err
		}
		if !p.accept('}') {
			return fmt.Errorf("the option %s of %s is not closed with }", selector, arg.name)
		}
		arg.options = append(arg.options, option{selector: selector, message: msg})
		p.skipSpace()
		selector = p.identifier()
	}
	if len(arg.options) == 0 {
		return fmt.Errorf("%s has no options", arg.name)
	}
	if !seen["other"] {
		return fmt.Errorf("%s has no other option", arg.name)
	}
	return nil
}

// arguments returns the names of the arguments that nodes use.
func arguments(nodes []node, into map[string]bool) {
	for _, n := range nodes {
		if a, ok := n.(*argument); ok {
			into[a.name] = true
			for _, o := range a.options {
				arguments(o.message, into)
			}
		}
	}
}

// format renders nodes in the locale l with args. value is the number that #
// stands for, in an option of a plural.
func format(b *strings.Builder, l *locale, nodes []node, args Args, value *decimal) error {
	for _, n := range nodes {
		switch n := n.(type) {
		case literal:
			b.WriteString(string(n))
		case pound:
			if value != nil {
				b.WriteString(l.number(*value))
			}
		case *argument:
			v, ok := args[n.name]
			if !ok {
				return fmt.Errorf("the argument %s is missing", n.name)
			}
			if err := formatArgument(b, l, n, v, args); err != nil {
				return err
			}
		}
	}
	return nil
}

func formatArgument(b *strings.Builder, l *locale, a *argument, v any, args Args) error {
	if s, ok := v.(string); ok && (a.kind == argSimple || a.kind == argSelect) {
		if a.kind == argSimple {
			b.WriteString(s)
			return nil
		}
		return format(b, l, a.choose(s, ""), args, nil)
	}
	f, ok := toFloat(v)
	if !ok {
		if a.kind == argSimple || a.kind == argSelect {
			return fmt.Errorf("the argument %s is a %T, not a string or a number", a.name, v)
		}
		return fmt.Errorf("the argument %s is a %T, not a number", a.name, v)
	}
	switch a.kind {
	case argSimple:
		b.WriteString(jsString(v, f))
		return nil
	case argSelect:
		return format(b, l, a.choose(jsString(v, f), ""), args, nil)
	case argNumber:
		maxFraction := 3
		if a.integer {
			maxFraction = 0
		}
		d, err := newDecimal(v, f, maxFraction)
		if err != nil {
			return fmt.Errorf("the argument %s: %w", a.name, err)
		}
		b.WriteString(l.number(d))
		return nil
	}
	// A plural or selectordinal: an exact match on the value, or else the
	// category of the value less the offset, which # shows.
	var d decimal
	var err error
	if a.offset == 0 {
		d, err = newDecimal(v, f, 3)
	} else {
		less := f - float64(a.offset)
		d, err = newDecimal(less, less, 3)
	}
	if err != nil {
		return fmt.Errorf("the argument %s: %w", a.name, err)
	}
	rules := plural.Cardinal
	if a.kind == argOrdinal {
		rules = plural.Ordinal
	}
	category := categories[d.pluralForm(rules, l.tag)]
	return format(b, l, a.choose("="+jsString(v, f), category), args, &d)
}

// choose returns the message of the option that selector names, or else of
// the one that fallback names, if fallback is set, or else of other, which
// every plural and select has.
func (a *argument) choose(selector, fallback string) []node {
	named := func(s string) func(option) bool { return func(o option) bool { return o.selector == s } }
	i := slices.IndexFunc(a.options, named(selector))
	if i < 0 && fallback != "" {
		i = slices.IndexFunc(a.options, named(fallback))
	}
	if i < 0 {
		i = slices.IndexFunc(a.options, named("other"))
	}
	return a.options[i].message
}

// categories names the plural forms of x/text as CLDR does.
var categories = map[plural.Form]string{
	plural.Other: "other",
	plural.Zero:  "zero",
	plural.One:   "one",
	plural.Two:   "two",
	plural.Few:   "few",
	plural.Many:  "many",
}

// toFloat returns v as a float64 if it is a number.
func toFloat(v any) (float64, bool) {
	switch v := v.(type) {
	case int:
		return float64(v), true
	case int8:
		return float64(v), true
	case int16:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint8:
		return float64(v), true
	case uint16:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	case float32:
		return float64(v), true
	case float64:
		return v, true
	}
	return 0, false
}

// jsString returns the number v, whose value is f, as JavaScript's String
// writes it: an integer exactly, and a float in its shortest form, with an
// exponent below 1e-6 and from 1e21.
func jsString(v any, f float64) string {
	switch v := v.(type) {
	case int, int8, int16, int32, int64:
		return fmt.Sprint(v)
	case uint, uint8, uint16, uint32, uint64:
		return fmt.Sprint(v)
	case float32:
		// A float32 is shown in its own shortest form, as 0.1 rather than
		// 0.10000000149011612.
		f, _ = strconv.ParseFloat(strconv.FormatFloat(float64(v), 'g', -1, 32), 64)
	}
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == 0:
		return "0" // and -0 too
	}
	if a := math.Abs(f); a >= 1e21 || a < 1e-6 {
		s := strconv.FormatFloat(f, 'e', -1, 64)
		// Go writes the exponent with at least two digits, as 1e-07, and
		// JavaScript with as few as it needs.
		mantissa, exp, _ := strings.Cut(s, "e")
		sign, digits := exp[:1], strings.TrimLeft(exp[1:], "0")
		return mantissa + "e" + sign + digits
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}
