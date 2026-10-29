# Unicode tables

The tables of Unicode that the expression language's `upper`, `lower`, and `trim` read (PRD §5.8.9, plan note N46), so that the Go, TypeScript, and GDScript evaluators case and trim a string alike, rather than each by its platform's tables, which are of other versions of Unicode and, as JavaScript's `toUpperCase` and `trim`, follow other rules. The tables are of Unicode 18.0.0, and are generated from the files of the Unicode Character Database (UCD) of that version, which [`ucd/`](ucd/) holds.

| File | Contents |
| --- | --- |
| [`tables.json`](tables.json) | The tables, generated from the files of `ucd/`. `server/internal/semantics` generates it, and its own copy of the tables, `tables.go`, and `go test ./...` fails when either is not what the files of `ucd/` generate; `go test ./internal/semantics -run TestTables -update`, in `server/`, rewrites both. The TypeScript evaluator, `web/shared/src/expr/semantics.ts`, reads it as it is, so that it has no copy to go out of date. |
| [`fold.json`](fold.json) | The case folding by which a runtime compares a node's tags, which are the same but for their case when they fold alike (`schema/published/README.md`): that of the Go runtime, `input.FoldCase` of `server/internal/input`, of `golang.org/x/text/cases`, and the code points that the normalization form C that it puts a tag in, of `golang.org/x/text/unicode/norm`, may change, where it inserts a CGJ, and the tables by which it decomposes, orders, and joins them, which are of the version of Unicode that its `unicodeVersion` states, rather than of `ucd/`. `server/internal/input` generates it, and `go test ./...` fails when it is not that folding and that form C, as when a newer `x/text`, or a newer Go, with which `x/text` builds the tables of another version of Unicode, changes them, and `go test -short ./...` when its `unicodeVersion` is not theirs; `go test ./internal/input -run TestFoldTable -update`, in `server/`, rewrites it. The TypeScript runtime's loader reads it as it is, by `foldCase` of `web/shared/src/flow/fold.ts`, and `pnpm test` checks it against `checks`. |
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
| `nonStarters` | The ranges of the code points that form C counts as non-starters at their start or at their end, each as an array of its first and its last code point, both included, how many it counts each of them as at its start, and how many at its end, in their order: U+0301 COMBINING ACUTE ACCENT as 1 and 1, U+00E9 LATIN SMALL LETTER E WITH ACUTE as 0 and 1, and U+0344 COMBINING GREEK DIALYTIKA TONOS as 2 and 2. It counts every other code point as none at either end, and each of these is one that `nfc` holds. A code point that it counts as none at its start is a starter. |
| `ccc` | The ranges of the code points of each canonical combining class other than 0, each as an array of its first and its last code point, both included, and the class, in their order, with ranges of one class that meet joined: U+0300 to U+0314 of 230. Every other code point is of class 0. |
| `decompositions` | Each code point that form D, the canonical decomposition, changes, but the Hangul syllables, U+AC00 to U+D7A3, which Unicode decomposes by an algorithm (§3.12), as an array of it and then the code points that it decomposes to, in their order, as form D gives them, every one of them decomposed: U+01D5 LATIN CAPITAL LETTER U WITH DIAERESIS AND MACRON to U+0055, U+0308, and U+0304. A code point that it does not hold decomposes to itself. |
| `compositions` | Each code point that form C joins two code points into, but the Hangul syllables, which Unicode joins by the algorithm, as an array of it, the first of the two, and the second, in the order of the code points: U+01D5 from U+00DC and U+0304. The second is the last code point of its decomposition, and the first what form C makes of the others, as form C joins the code points of a decomposition from its first. Form C joins no other two. |
| `checks` | The SHA-256 of what `input.FoldCase` returns of each of three strings, in UTF-8, as `sha256:` and 64 lowercase hexadecimal digits, by their names: `every`, every code point from U+0000 to U+10FFFF but the surrogates, in order; `beside`, each code point that `fold` or `nfc` holds, in order, nine times, followed in turn by `a`, U+0307, U+0345, U+03A3, U+03C2, U+0130, U+AC00, U+11A8, and nothing; and `streams`, each code point that `nfc` holds, in order, as `a`, 30 U+0301, it, and 30 U+0301, then `a`, 29 U+0301, and it, then `a`, 28 U+0301, and it. A runtime builds the three, folds them, and compares the hashes, so that it checks its folding of every code point, beside the code points whose neighbours a folding or form C might read, and after and before rows of non-starters, against the Go runtime's, rather than against a copy of its algorithm. |

A tag folds as `input.FoldCase` folds it: the tag in Unicode's normalization form C, each of its code points folded alone, as `fold` folds it, and the result in form C again, which `TestFoldTable` checks of `FoldCase`, of every code point beside its neighbours. So `ẞ` and `ß` fold to `ss`, `Σ` and the final `ς` to `σ`, and `I` to `i`, though `ı`, U+0131 LATIN SMALL LETTER DOTLESS I, which Unicode's case folding does not map, folds to itself, so that `kır` and `KIR` are two tags: JavaScript's `toUpperCase` and `toLowerCase`, which map `ı` to `I` and back to `i`, and which are of the platform's version of Unicode, fold otherwise.

A runtime puts a tag in Go's form C by putting each run of its code points that `nfc` holds in form C by `ccc`, `decompositions`, and `compositions`, and the algorithm of the Hangul syllables, as UAX #15 and Unicode's §3.11 and §3.12 say: each code point decomposed, the non-starters of each row of them ordered by their classes, those of one class kept in their order, and each code point then joined, from the first, with the last starter before it, unless a code point between the two that is not joined is a starter or of its class or a greater one, which a starter joins only with the starter just before it. It leaves every other code point as it is. It never puts a tag in its platform's form C, whose tables are of the platform's version of Unicode: one of an older version, as a browser's on a system that has not been updated, takes a mark that a later version assigned, as U+1E4EF NAG MUNDARI SIGN SUTUH, of Unicode 15.0, for an unassigned starter, which it neither orders nor joins across, and one of a later version may join a code point that Unicode assigned since: Unicode 16.0's joins U+16D67 KIRAT RAI VOWEL SIGN E twice into U+16D68, which Go's leaves as two code points. `TestFoldTable` checks that the tables give x/text's form C of every code point that `nfc` holds, of each pair that `compositions` joins, and of every run of the checks' strings.

Go's form C also follows Unicode's Stream-Safe Text Format (UAX #15): before a code point that would make more than 30 non-starters in a row, it inserts U+034F COMBINING GRAPHEME JOINER, a starter, across which no mark is reordered, which a platform's form C does not. So, before it puts a run in form C, a runtime inserts one there too, counting as `nonStarters` counts: a code point that it counts as none at its start, or that `nfc` does not hold, ends the row, which is then as long as its count at its end; any other adds its count at its start to the row, unless that makes more than 30, where the runtime inserts a CGJ before it, and the row is as long as its count at its start. It then puts each part of the run between two CGJs in form C, as the tables do. So `a`, 16 U+0323 COMBINING DOT BELOW, and 16 U+0301, and `a`, 16 U+0301, and 16 U+0323, are two tags: Go's form C inserts a CGJ before the 31st mark of each, and orders the marks before it, but none across it, so that the first ends with two U+0301 and the second with two U+0323, though form C without a CGJ, as a platform's, which orders all 32, gives the two alike.

## Another version of Unicode

A new version of Unicode changes what `upper`, `lower`, and `trim` compute for the code points that it adds or maps, so it is a new `ast_version`, which applies to those three built-ins (PRD §5.8.9, FR-EXPR-04): a snapshot that uses any of them declares at least that `astVersion`, so that a runtime with older tables refuses it at load rather than computing other results. The tables of this version, Unicode 18.0.0, are those of the first `ast_version`. To move to another version:

1. Replace `ucd/UnicodeData.txt` and `ucd/PropList.txt` with that version's, from `https://www.unicode.org/Public/<version>/ucd/`, and `ucd/LICENSE.txt` with `https://www.unicode.org/license.txt`.
2. In `server/internal/semantics/tables_test.go`, set `ucdVersion` to the version and `ucdUnicodeData` and `ucdPropList` to the SHA-256 of the new files.
3. Run `go test ./internal/semantics -run TestTables -update` in `server/`, and change the version that this README, PRD §5.8.9, and plan item 38 state, each place of which the test checks (`versionStatements`).
4. Introduce the new `ast_version` for `upper`, `lower`, and `trim`, and add corpus cases of what the version changes.
