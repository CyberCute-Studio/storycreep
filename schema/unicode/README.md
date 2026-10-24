# Unicode tables

The tables of Unicode that the expression language's `upper`, `lower`, and `trim` read (PRD §5.8.9, plan note N46), so that the Go, TypeScript, and GDScript evaluators case and trim a string alike, rather than each by its platform's tables, which are of other versions of Unicode and, as JavaScript's `toUpperCase` and `trim`, follow other rules. The tables are of Unicode 18.0.0, and are generated from the files of the Unicode Character Database (UCD) of that version, which [`ucd/`](ucd/) holds.

| File | Contents |
| --- | --- |
| [`tables.json`](tables.json) | The tables, generated from the files of `ucd/`. `server/internal/semantics` generates it, and its own copy of the tables, `tables.go`, and `go test ./...` fails when either is not what the files of `ucd/` generate; `go test ./internal/semantics -run TestTables -update`, in `server/`, rewrites both. |
| [`ucd/UnicodeData.txt`](ucd/UnicodeData.txt) | The UCD's `UnicodeData.txt`, as unicode.org publishes it at `https://www.unicode.org/Public/18.0.0/ucd/`, whose fields 12 and 13 are a code point's `Simple_Uppercase_Mapping` and `Simple_Lowercase_Mapping`. |
| [`ucd/PropList.txt`](ucd/PropList.txt) | The UCD's `PropList.txt`, from the same place, which lists the code points of `White_Space`. |
| [`ucd/LICENSE.txt`](ucd/LICENSE.txt) | The Unicode License v3, under which Unicode, Inc. publishes the UCD, and under which the files of `ucd/` and the tables generated from them are distributed here. |

The test checks the SHA-256 of each file of `ucd/` against that of the file that unicode.org publishes, so that a file that an editor or a checkout changed, as one whose line endings it changed, fails rather than generating other tables.

## `tables.json`

```json
{
  "unicodeVersion": "18.0.0",
  "lower": [[65, 97], [66, 98]],
  "upper": [[97, 65], [98, 66]],
  "whiteSpace": [[9, 13], [32, 32]]
}
```

Every code point is a number, its scalar value, as 65 for U+0041 LATIN CAPITAL LETTER A.

| Key | Contents |
| --- | --- |
| `unicodeVersion` | The version of Unicode of the tables, which is that of the files of `ucd/`. |
| `lower` | Each code point that has a simple lowercase mapping, and that mapping, as a pair, in the order of the code points. A code point that is not in it is its own lowercase. |
| `upper` | Each code point that has a simple uppercase mapping, and that mapping, as `lower` holds them. |
| `whiteSpace` | The ranges of the code points of `White_Space`, each as its first and its last code point, both included, in their order, with ranges that meet joined, as U+2028 and U+2029 are. |

A simple mapping maps one code point to one, so `upper` and `lower` never change a string's length: `upper("ß")` is `"ß"`, which has no simple uppercase mapping, though its full one is `SS`, and `lower("İ")`, U+0130, is `"i"`, one code point. Neither depends on a locale, so `upper("i")` is `"I"`, in Turkish too, nor on the code points around one, so `lower("Σ")` is `"σ"`, never the final `"ς"`. `White_Space` holds neither U+FEFF ZERO WIDTH NO-BREAK SPACE, which JavaScript's `trim` removes, nor U+200B ZERO WIDTH SPACE.

Code points that Unicode assigned after the version of a platform's tables have mappings here that the platform's do not have: Go 1.26's package `unicode` holds Unicode 15.0.0's, so `strings.ToUpper` leaves U+0264 LATIN SMALL LETTER RAMS HORN, which Unicode 16.0 maps to U+A7CB, which it assigned then. The expression corpus ([`../expr-corpus/`](../expr-corpus/)) has cases of mappings that Unicode 16.0, 17.0, and 18.0 introduced, so that an evaluator with older tables fails them.

## Another version of Unicode

A new version of Unicode changes what `upper`, `lower`, and `trim` compute for the code points that it adds or maps, so it is a new `ast_version`, which applies to those three built-ins (PRD §5.8.9, FR-EXPR-04): a snapshot that uses any of them declares at least that `astVersion`, so that a runtime with older tables refuses it at load rather than computing other results. The tables of this version, Unicode 18.0.0, are those of the first `ast_version`. To move to another version:

1. Replace `ucd/UnicodeData.txt` and `ucd/PropList.txt` with that version's, from `https://www.unicode.org/Public/<version>/ucd/`, and `ucd/LICENSE.txt` with `https://www.unicode.org/license.txt`.
2. In `server/internal/semantics/tables_test.go`, set `ucdVersion` to the version and `ucdUnicodeData` and `ucdPropList` to the SHA-256 of the new files.
3. Run `go test ./internal/semantics -run TestTables -update` in `server/`, and change the version that this README states.
4. Introduce the new `ast_version` for `upper`, `lower`, and `trim`, and add corpus cases of what the version changes.
