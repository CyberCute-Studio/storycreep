# Unicode tables

The tables of Unicode that the expression language's `upper`, `lower`, and `trim` read (PRD §5.8.9, plan note N46), so that the Go, TypeScript, and GDScript evaluators case and trim a string alike, rather than each by its platform's tables, which are of other versions of Unicode and, as JavaScript's `toUpperCase` and `trim`, follow other rules. The tables are of Unicode 18.0.0, and are generated from the files of the Unicode Character Database (UCD) of that version, which [`ucd/`](ucd/) holds.

| File | Contents |
| --- | --- |
| [`tables.json`](tables.json) | The tables, generated from the files of `ucd/`. `server/internal/semantics` generates it, and its own copy of the tables, `tables.go`, and `go test ./...` fails when either is not what the files of `ucd/` generate; `go test ./internal/semantics -run TestTables -update`, in `server/`, rewrites both. The TypeScript evaluator, `web/shared/src/expr/semantics.ts`, reads it as it is, so that it has no copy to go out of date. |
| [`fold.json`](fold.json) | The case folding by which a runtime compares a node's tags, which are the same but for their case when they fold alike (`schema/published/README.md`): that of the Go runtime, `input.FoldCase` of `server/internal/input`, of `golang.org/x/text/cases`, and the code points that the normalization form C that it puts a tag in, of `golang.org/x/text/unicode/norm`, may change, whose tables are of the version of Unicode that its `unicodeVersion` states, rather than of `ucd/`. `server/internal/input` generates it, and `go test ./...` fails when it is not that folding and that form C, as when a newer `x/text`, or a newer Go, with which `x/text` builds the tables of another version of Unicode, changes them; `go test ./internal/input -run TestFoldTable -update`, in `server/`, rewrites it. The TypeScript runtime's loader, `web/shared/src/flow/load.ts`, reads it as it is (`foldCase`). |
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

## `fold.json`

Every code point of the file is a number, as in `tables.json`.

| Key | Contents |
| --- | --- |
| `unicodeVersion` | The version of Unicode of the tables of `golang.org/x/text`'s case folding and of its normalization form C, which `TestFoldTable` checks are of one version. |
| `fold` | Each code point that the folding changes, as an array of it and then the code points that it folds to, one or more, in the order of the code points. A code point that is not in it folds to itself. |
| `nfc` | The ranges of the code points that form C may change, reorder, or join with the code points beside them, each as its first and its last code point, both included, in their order. Form C leaves every other code point as it is, and joins it with none, as it does each code point that the version does not assign. |

A tag folds as `input.FoldCase` folds it: the tag in Unicode's normalization form C, each of its code points folded alone, as `fold` folds it, and the result in form C again, which `TestFoldTable` checks of `FoldCase`, of every code point beside its neighbours. So `ẞ` and `ß` fold to `ss`, `Σ` and the final `ς` to `σ`, and `I` to `i`, though `ı`, U+0131 LATIN SMALL LETTER DOTLESS I, which Unicode's case folding does not map, folds to itself, so that `kır` and `KIR` are two tags: JavaScript's `toUpperCase` and `toLowerCase`, which map `ı` to `I` and back to `i`, and which are of the platform's version of Unicode, fold otherwise.

A runtime puts a tag in Go's form C by putting each run of its code points that `nfc` holds in its platform's form C, and leaving every other code point as it is. Unicode never changes the form C of the code points that a version assigns, so a platform whose tables are of `unicodeVersion` or a later version gives Go's form C of each run, while its form C of the whole tag may join a code point that Unicode assigned since: Unicode 16.0's joins U+16D67 KIRAT RAI VOWEL SIGN E twice into U+16D68, which Go's leaves as two code points.

## Another version of Unicode

A new version of Unicode changes what `upper`, `lower`, and `trim` compute for the code points that it adds or maps, so it is a new `ast_version`, which applies to those three built-ins (PRD §5.8.9, FR-EXPR-04): a snapshot that uses any of them declares at least that `astVersion`, so that a runtime with older tables refuses it at load rather than computing other results. The tables of this version, Unicode 18.0.0, are those of the first `ast_version`. To move to another version:

1. Replace `ucd/UnicodeData.txt` and `ucd/PropList.txt` with that version's, from `https://www.unicode.org/Public/<version>/ucd/`, and `ucd/LICENSE.txt` with `https://www.unicode.org/license.txt`.
2. In `server/internal/semantics/tables_test.go`, set `ucdVersion` to the version and `ucdUnicodeData` and `ucdPropList` to the SHA-256 of the new files.
3. Run `go test ./internal/semantics -run TestTables -update` in `server/`, and change the version that this README, PRD §5.8.9, and plan item 38 state, each place of which the test checks (`versionStatements`).
4. Introduce the new `ast_version` for `upper`, `lower`, and `trim`, and add corpus cases of what the version changes.
