# Message format vectors

`vectors.json` holds the test vectors of the message format (PRD FR-I18N-01). The catalogs' messages are in ICU MessageFormat, which the web app formats with i18next and i18next-icu (`web/shared/src/i18n`), and the server with its own formatter (`server/internal/i18n`). Both run every vector, so that a message renders the same wherever it is shown, and a translator writes one dialect of ICU for both.

The web app's formatter is the reference: each output is what i18next-icu, which reads messages with intl-messageformat and `ignoreTag`, renders. The server formats a subset of the syntax, which `server/internal/i18n/messageformat.go` lists; the vectors stay inside it.

## The file

| Key | Contents |
| --- | --- |
| `format` | A locale, a message, its arguments, and the text it renders. The Czech vectors check its plural rules (`one`, `few`, `many`, and `other`) and its number format, which groups digits with a no-break space (U+00A0) and writes a decimal comma. |
| `invalid` | Messages that both formatters refuse, such as a plural without an `other` option. Every plural and select must have one, which intl-messageformat checks only when it is asked to. |

Arguments are JSON values: strings, and numbers, which both sides read as 64-bit floats.

## Semantics the vectors pin

- **Plain arguments.** `{name}` shows a string as it is, and a number as JavaScript's `String` writes it, without grouping: `1234.5`, and `1e+21` from 10²¹.
- **Numbers.** `{n, number}` shows at most three fraction digits, and `{n, number, integer}` none. Both round the number's shortest decimal form, `1.0005` rather than the binary float's `1.000499…`, and round a half away from zero, so `2.5` is `3`.
- **Plurals.** An exact match such as `=0` is tried first, on the value itself. The category is that of the value less the offset, rounded to three fraction digits, so `1.0004` is `one` in English. `#` shows that number as `{n, number}` would, but only directly in a plural's or selectordinal's options: in a select nested in one, it is text.
- **Quoting.** `''` is an apostrophe. An apostrophe before `{`, `}`, `<`, `>`, or, in a plural, `#`, starts quoted text, which ends at the next lone apostrophe or at the end of the message. Any other apostrophe is itself, so `It's` needs no quoting.
- **Tags.** `<b>` and other tags are text.

One rule is outside the vectors, which hold one locale each: a message that a translation lacks is shown in English, and formatted with English plural rules and numbers, the locale that its options are written for, so a Czech reader sees `1,200 boards`. The web app passes i18next-icu the locale of the catalog that has the message, which i18next does not do by itself. Tests on both sides check it: `TestPlurals` in `server/internal/i18n`, and `i18n.test.ts` in `web/shared/src/i18n`.
