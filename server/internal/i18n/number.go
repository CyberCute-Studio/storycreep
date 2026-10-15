package i18n

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"golang.org/x/text/feature/plural"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"golang.org/x/text/number"
)

// locale formats for one locale.
type locale struct {
	tag     language.Tag
	printer *message.Printer
}

func newLocale(tag language.Tag) *locale {
	return &locale{tag: tag, printer: message.NewPrinter(tag)}
}

// number writes d in the locale's format, with its grouping and decimal
// separators, as Intl.NumberFormat does.
func (l *locale) number(d decimal) string {
	if d.negative && d.integer == "0" && d.fraction == "" {
		// Intl.NumberFormat shows a negative number that rounds to zero as
		// -0, and x/text as 0, so the sign is taken from -1 in the locale.
		one, zero := l.printer.Sprint(number.Decimal(1)), l.printer.Sprint(number.Decimal(0))
		return strings.Replace(l.printer.Sprint(number.Decimal(-1)), one, zero, 1)
	}
	if d.fraction == "" {
		if n, err := strconv.ParseInt(d.String(), 10, 64); err == nil {
			return l.printer.Sprint(number.Decimal(n))
		}
	}
	// d has at most three fraction digits, which x/text keeps, and
	// ParseFloat reads it back as the float whose shortest form it is, so
	// x/text, which would otherwise round half to even, does not round again.
	f, _ := strconv.ParseFloat(d.String(), 64)
	return l.printer.Sprint(number.Decimal(f, number.MaxFractionDigits(len(d.fraction))))
}

// decimal is a number as it is shown: its decimal digits, already rounded.
type decimal struct {
	negative bool
	integer  string // the digits before the point, without leading zeros, or "0"
	fraction string // the digits after it, without trailing zeros
}

func (d decimal) String() string {
	s := d.integer
	if d.fraction != "" {
		s += "." + d.fraction
	}
	if d.negative {
		s = "-" + s
	}
	return s
}

// newDecimal returns the number v, whose value is f, rounded to at most
// maxFraction fraction digits, as Intl.NumberFormat and Intl.PluralRules
// round it: an integer is exact, a float is read in its shortest decimal
// form, as 1.0005 rather than the 1.000499… that the float holds, and a half
// is rounded away from zero.
func newDecimal(v any, f float64, maxFraction int) (decimal, error) {
	var s string
	switch v := v.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		s = fmt.Sprint(v)
	case float32:
		s = strconv.FormatFloat(float64(v), 'f', -1, 32)
	default:
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return decimal{}, errors.New("the number is not finite")
		}
		s = strconv.FormatFloat(f, 'f', -1, 64)
	}
	var d decimal
	s, d.negative = strings.CutPrefix(s, "-")
	d.integer, d.fraction, _ = strings.Cut(s, ".")
	if len(d.fraction) > maxFraction {
		up := d.fraction[maxFraction] >= '5'
		d.fraction = d.fraction[:maxFraction]
		if up {
			digits := []byte(d.integer + d.fraction)
			i := len(digits) - 1
			for ; i >= 0 && digits[i] == '9'; i-- {
				digits[i] = '0'
			}
			if i >= 0 {
				digits[i]++
			} else {
				digits = append([]byte{'1'}, digits...)
			}
			cut := len(digits) - len(d.fraction)
			d.integer, d.fraction = string(digits[:cut]), string(digits[cut:])
		}
	}
	d.fraction = strings.TrimRight(d.fraction, "0")
	if d.integer = strings.TrimLeft(d.integer, "0"); d.integer == "" {
		d.integer = "0"
	}
	return d, nil
}

// pluralForm returns the plural form of d in the locale tag by rules, from
// the digits that are shown, as Intl.PluralRules does: 1 is one in English,
// and 1.5 is many in Czech.
func (d decimal) pluralForm(rules *plural.Rules, tag language.Tag) plural.Form {
	digits := []byte(d.integer + d.fraction)
	for i := range digits {
		digits[i] -= '0'
	}
	return rules.MatchDigits(tag, digits, len(d.integer), len(d.fraction))
}
