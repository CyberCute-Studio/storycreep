// The string semantics of the expression language that read the tables of
// one version of Unicode (PRD §5.8.9): upper, lower, and trim, which case
// and trim a string by those of schema/unicode/tables.json, read here as
// they are, so that there is no copy of them to go out of date, rather than
// by JavaScript's toUpperCase, toLowerCase, and trim, which follow other
// tables and other rules. With semantics.ts, it is the TypeScript twin of
// the Go package server/internal/semantics, apart from it so that what
// parses and checks an expression, which reads semantics.ts and never
// cases a string, as the editor's instant feedback, imports none of the
// tables.

import tables from "../../../../schema/unicode/tables.json" with {
  type: "json",
};

/**
 * The version of Unicode whose simple case mappings and White_Space upper,
 * lower, trim, and isWhiteSpace read (PRD §5.8.9), that of
 * schema/unicode/tables.json. A new version of Unicode is a new ast_version,
 * of upper, lower, and trim.
 */
export const UNICODE_VERSION: string = tables.unicodeVersion;

// caseTables are the simple case mappings of the tables, by code point,
// which caseMappings builds the first time that upper or lower is called, so
// that a page that parses and checks expressions, and calls neither, builds
// none of them.
let caseTables:
  | { upper: ReadonlyMap<number, number>; lower: ReadonlyMap<number, number> }
  | undefined;

// caseMappings returns the simple mappings of the tables to the case, upper
// or lower, by code point.
function caseMappings(c: "upper" | "lower"): ReadonlyMap<number, number> {
  caseTables ??= {
    upper: new Map(tables.upper.map(([from, to]) => [from, to])),
    lower: new Map(tables.lower.map(([from, to]) => [from, to])),
  };
  return caseTables[c];
}

// The ranges of White_Space, in their order, as the tables hold them.
const whiteSpace: readonly (readonly number[])[] = tables.whiteSpace;

/**
 * Returns s with each code point mapped to its simple uppercase mapping, as
 * upper does (§5.8.4, §5.8.9), from the tables of UNICODE_VERSION rather than
 * JavaScript's, which are of another version of Unicode and use the full
 * mappings. A simple mapping maps one code point to one, so the result has
 * as many code points as s: upper("ß") is "ß", where toUpperCase gives "SS".
 * It maps no code point by locale: upper("i") is "I", in Turkish too.
 */
export function upper(s: string): string {
  return mapCase(s, caseMappings("upper"));
}

/**
 * Returns s with each code point mapped to its simple lowercase mapping, as
 * lower does, as upper says: lower("İ") is "i", one code point, and
 * lower("Σ") is "σ", wherever it is in a word.
 */
export function lower(s: string): string {
  return mapCase(s, caseMappings("lower"));
}

// mapCase returns s with each code point that table maps mapped. A lone
// surrogate, which no string of the language holds, is kept as it is.
function mapCase(s: string, table: ReadonlyMap<number, number>): string {
  let out = "";
  for (const c of s) {
    const to = table.get(c.codePointAt(0) ?? 0);
    out += to === undefined ? c : String.fromCodePoint(to);
  }
  return out;
}

/**
 * Returns s without the code points of White_Space, of the tables of
 * UNICODE_VERSION, that it starts or ends with, as trim does (§5.8.4,
 * §5.8.9). White_Space holds no U+FEFF ZERO WIDTH NO-BREAK SPACE, which
 * JavaScript's trim removes, no U+200B ZERO WIDTH SPACE, and no U+180E
 * MONGOLIAN VOWEL SEPARATOR. Every code point of White_Space is one code
 * unit, which no surrogate is.
 */
export function trim(s: string): string {
  let start = 0;
  let end = s.length;
  while (start < end && isWhiteSpace(s.charCodeAt(start))) {
    start++;
  }
  while (end > start && isWhiteSpace(s.charCodeAt(end - 1))) {
    end--;
  }
  return s.slice(start, end);
}

/**
 * Reports whether c is a code point of White_Space, of the tables of
 * UNICODE_VERSION. The ranges are in their order, so it stops at the first
 * that ends at c or after it.
 */
export function isWhiteSpace(c: number): boolean {
  for (const [first, last] of whiteSpace) {
    if (c <= last) {
      return first <= c;
    }
  }
  return false;
}
