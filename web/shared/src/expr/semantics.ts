// The numeric and string semantics of the expression language (PRD §5.8.9),
// which every evaluator of it shares, so that TypeScript, Go, and GDScript
// give the same result for the same expression where each language's own
// operations differ: an int is an integer within ±MAX_INT, which nothing
// wraps; a float is a finite double, never -0; a string converts to a number
// by an exact grammar, and to the nearest double; a number is written the
// same way by every runtime; and a string is a sequence of code points,
// cased and trimmed by the tables of one version of Unicode, which are those
// of schema/unicode/tables.json, read here as they are.
//
// This is the TypeScript twin of the Go package server/internal/semantics.
// Both run the cases of schema/expr-corpus, whose README states the rules. A
// JavaScript number holds every int exactly, so no operation needs a BigInt,
// but none uses an operation whose result JavaScript computes otherwise:
// Math.round, which rounds half toward +∞, a string's length or its
// comparison by UTF-16 code units, or toUpperCase, toLowerCase, and trim,
// which follow other tables and other rules.
//
// An operation that fails reports a runtime error (§5.8.7) and yields the
// zero value of its result's type, 0, 0.0, or "", which evaluation goes on
// with.

import tables from "../../../../schema/unicode/tables.json" with {
  type: "json",
};

/**
 * The greatest int, and -MAX_INT the least: 2^53 − 1, which a double holds
 * exactly, so that every int widens to a float exactly (PRD §5.8.1, §5.8.9).
 */
export const MAX_INT = 9007199254740991;

/**
 * The most code points that a string may have, as a string literal may (PRD
 * §5.8.1), and as a string result may, which a longer one is string_too_long
 * (concat).
 */
export const MAX_STRING_LENGTH = 4096;

/**
 * The most digits after the point that an interpolation format specifier
 * shows a number with (FR-TEXT-02), as a text value reference's decimals do
 * (formatFixed).
 */
export const MAX_DECIMALS = 6;

/**
 * The version of Unicode whose simple case mappings and White_Space upper,
 * lower, trim, and isWhiteSpace read (PRD §5.8.9), that of
 * schema/unicode/tables.json. A new version of Unicode is a new ast_version,
 * of upper, lower, and trim.
 */
export const UNICODE_VERSION: string = tables.unicodeVersion;

/** The runtime errors that the operations of this module report (§5.8.7). */
export type SemanticsError =
  /** An int result beyond ±MAX_INT. */
  | "integer_overflow"
  /** A float result that is NaN or an infinity. */
  | "float_not_finite"
  /** A division by zero, of an int or a float, or an int's modulo by zero. */
  | "division_by_zero"
  /** A string that to_int or to_float does not read. */
  | "conversion_failed"
  /** A string result of more than MAX_STRING_LENGTH code points. */
  | "string_too_long";

/**
 * The result of an operation: its value, and the runtime error that it
 * reports, if any, in which case the value is its type's zero value.
 */
export type Result<T> = readonly [value: T, error: SemanticsError | undefined];

// ok returns the result of an operation that reports no error.
function ok<T>(value: T): Result<T> {
  return [value, undefined];
}

// zero returns n, or 0 for -0, which no int or float of the language is.
function zero(n: number): number {
  return n === 0 ? 0 : n;
}

// isInt reports whether n is an integer within ±MAX_INT, as every int is.
function isInt(n: number): boolean {
  return Number.isInteger(n) && -MAX_INT <= n && n <= MAX_INT;
}

/** Returns n as an int, or integer_overflow and 0 when it is beyond ±MAX_INT. */
export function checkInt(n: number): Result<number> {
  return isInt(n) ? ok(zero(n)) : [0, "integer_overflow"];
}

/**
 * Returns a + b. The exact sum of operands within ±MAX_INT, if it is within
 * ±MAX_INT, is a double, which the addition returns exactly, and one beyond
 * rounds to 2^53 or beyond, so the bound is checked on the result (§5.8.9).
 */
export function addInt(a: number, b: number): Result<number> {
  if (!isInt(a) || !isInt(b)) {
    return [0, "integer_overflow"];
  }
  return checkInt(a + b);
}

/** Returns a - b, as addInt says. */
export function subInt(a: number, b: number): Result<number> {
  if (!isInt(a) || !isInt(b)) {
    return [0, "integer_overflow"];
  }
  return checkInt(a - b);
}

/** Returns -a, as addInt says, which is an int whenever a is. */
export function negInt(a: number): Result<number> {
  return checkInt(-a);
}

/**
 * Returns a * b, checked before it multiplies (§5.8.9): the product overflows
 * exactly when a ≠ 0 and |b| > floor(MAX_INT / |a|), so that no product is
 * computed that a double would round.
 */
export function mulInt(a: number, b: number): Result<number> {
  if (
    !isInt(a) ||
    !isInt(b) ||
    (a !== 0 && Math.abs(b) > floorDiv(MAX_INT, Math.abs(a)))
  ) {
    return [0, "integer_overflow"];
  }
  return ok(zero(a * b));
}

// floorDiv returns floor(n / d) of two positive ints, exactly: the quotient
// of the doubles, which may round to the next integer, corrected by the
// products, which are exact when they are within ±MAX_INT and otherwise
// beyond it as doubles too, so that each comparison is exact.
function floorDiv(n: number, d: number): number {
  let q = Math.floor(n / d);
  while (q * d > n) {
    q--;
  }
  while ((q + 1) * d <= n) {
    q++;
  }
  return q;
}

/**
 * Returns a / b truncated toward zero, or division_by_zero and 0 when b is 0.
 * The quotient of two ints within ±MAX_INT never rounds to the next integer
 * as a double, so truncating it is exact, and it never overflows: -MAX_INT /
 * -1 is MAX_INT.
 */
export function divInt(a: number, b: number): Result<number> {
  if (!isInt(a) || !isInt(b)) {
    return [0, "integer_overflow"];
  }
  if (b === 0) {
    return [0, "division_by_zero"];
  }
  return ok(zero(Math.trunc(a / b)));
}

/**
 * Returns a % b, which takes the sign of the dividend, as divInt says. The
 * remainder of two doubles is exact.
 */
export function modInt(a: number, b: number): Result<number> {
  if (!isInt(a) || !isInt(b)) {
    return [0, "integer_overflow"];
  }
  if (b === 0) {
    return [0, "division_by_zero"];
  }
  return ok(zero(a % b));
}

/**
 * Returns f as a float (§5.8.9): -0 as 0, and NaN or an infinity as
 * float_not_finite and 0. A result that underflows, to a subnormal or to
 * zero, is a float, without an error.
 */
export function checkFloat(f: number): Result<number> {
  if (!Number.isFinite(f)) {
    return [0, "float_not_finite"];
  }
  return ok(zero(f));
}

/**
 * Returns a + b, rounded to the nearest double once, as every operation of
 * JavaScript's is, which never fuses two (§5.8.9), and checked as checkFloat
 * checks it.
 */
export function addFloat(a: number, b: number): Result<number> {
  return checkFloat(a + b);
}

/** Returns a - b, as addFloat says. */
export function subFloat(a: number, b: number): Result<number> {
  return checkFloat(a - b);
}

/** Returns a * b, as addFloat says. */
export function mulFloat(a: number, b: number): Result<number> {
  return checkFloat(a * b);
}

/** Returns -a, as addFloat says, so -0.0 is 0.0. */
export function negFloat(a: number): Result<number> {
  return checkFloat(-a);
}

/**
 * Returns a / b, as addFloat says, or division_by_zero and 0 when b is 0, as
 * an int's division by zero is (§5.8.3), rather than the infinity or the NaN
 * that IEEE 754 gives.
 */
export function divFloat(a: number, b: number): Result<number> {
  if (b === 0) {
    return [0, "division_by_zero"];
  }
  return checkFloat(a / b);
}

/**
 * Returns f truncated toward zero, as to_int converts a float, as an int, or
 * integer_overflow and 0 beyond ±MAX_INT, which the rounded value is compared
 * with before it is an int (§5.8.9).
 */
export function trunc(f: number): Result<number> {
  return toInt(Math.trunc(f));
}

/** Returns f rounded down, as trunc says. */
export function floor(f: number): Result<number> {
  return toInt(Math.floor(f));
}

/** Returns f rounded up, as trunc says. */
export function ceil(f: number): Result<number> {
  return toInt(Math.ceil(f));
}

/**
 * Returns f rounded half away from zero, as trunc says: round(-2.5) is -3,
 * and round(0.49999999999999994) is 0, which Math.round(x), and adding 0.5
 * and rounding down, would make otherwise. f minus its truncation is exact,
 * since a double of a magnitude of 2^52 or more is integral.
 */
export function round(f: number): Result<number> {
  const t = Math.trunc(f);
  return toInt(Math.abs(f - t) >= 0.5 ? t + Math.sign(f) : t);
}

// toInt returns t, an integral double, as an int, or integer_overflow and 0
// when it is beyond ±MAX_INT, or is not finite, which no float is.
function toInt(t: number): Result<number> {
  if (!(Math.abs(t) <= MAX_INT)) {
    return [0, "integer_overflow"];
  }
  return ok(zero(t));
}

/**
 * Returns the int that s writes, as to_int reads a string (§5.8.4, §5.8.9):
 * s is exactly [+-]?[0-9]+, with ASCII digits and nothing else, no white
 * space among it, or parseInt reports conversion_failed and 0, and its value
 * is within ±MAX_INT, however many leading zeros it has, or parseInt reports
 * integer_overflow and 0. "-0" is 0.
 */
export function parseIntText(s: string): Result<number> {
  const [body, negative] = cutSign(s);
  if (!isDigits(body)) {
    return [0, "conversion_failed"];
  }
  const digits = trimLeadingZeros(body);
  // MAX_INT has 16 digits, and every number of 16 digits beyond it reads as
  // a double of 2^53 or more.
  if (digits.length > 16) {
    return [0, "integer_overflow"];
  }
  const n = digits === "" ? 0 : Number(digits);
  if (n > MAX_INT) {
    return [0, "integer_overflow"];
  }
  return ok(zero(negative ? -n : n));
}

// cutSign returns s without the sign that it starts with, if any, and
// whether that sign is -.
function cutSign(s: string): [string, boolean] {
  if (s.startsWith("+") || s.startsWith("-")) {
    return [s.slice(1), s.startsWith("-")];
  }
  return [s, false];
}

// isDigits reports whether s is one or more ASCII digits.
function isDigits(s: string): boolean {
  return /^[0-9]+$/.test(s);
}

// trimLeadingZeros returns s without the zeros that it starts with.
function trimLeadingZeros(s: string): string {
  return s.replace(/^0+/, "");
}

// splitExponent returns s's mantissa and its exponent, what follows its
// first e or E, or undefined when it has none.
function splitExponent(s: string): [string, string | undefined] {
  const i = s.search(/[eE]/);
  return i < 0 ? [s, undefined] : [s.slice(0, i), s.slice(i + 1)];
}

// splitPoint returns s's digits before its point and after it, or undefined
// after it when it has none.
function splitPoint(s: string): [string, string | undefined] {
  const i = s.indexOf(".");
  return i < 0 ? [s, undefined] : [s.slice(0, i), s.slice(i + 1)];
}

// isFloatForm reports whether s is of the grammar of a string that to_float
// reads (§5.8.9), [+-]?[0-9]+(\.[0-9]+)?([eE][+-]?[0-9]+)?, over the whole of
// it: no white space, no inf or nan, no hexadecimal, no _ between digits, no
// point without digits on both sides of it, and nothing after it.
function isFloatForm(s: string): boolean {
  const [body] = cutSign(s);
  const [mantissa, exponent] = splitExponent(body);
  if (exponent !== undefined && !isDigits(cutSign(exponent)[0])) {
    return false;
  }
  const [whole, fraction] = splitPoint(mantissa);
  return isDigits(whole) && (fraction === undefined || isDigits(fraction));
}

/**
 * Returns the float that s writes, as to_float reads a string (§5.8.4,
 * §5.8.9): s is of the grammar [+-]?[0-9]+(\.[0-9]+)?([eE][+-]?[0-9]+)?, or
 * parseFloat reports conversion_failed and 0, and its value is the double
 * nearest it, correctly rounded, half to even, however many digits it has
 * and however large its exponent, or parseFloat reports float_not_finite and
 * 0 when it is beyond a double's range. A value too small for a double is 0,
 * or the subnormal nearest it, and "-0" is 0. The grammar is checked before
 * anything converts, so that no leniency of JavaScript's Number() is reached,
 * which reads white space, hexadecimal, and "Infinity".
 */
export function parseFloatText(s: string): Result<number> {
  if (!isFloatForm(s)) {
    return [0, "conversion_failed"];
  }
  const [f, finite] = nearest(s);
  if (!finite) {
    return [0, "float_not_finite"];
  }
  return checkFloat(f);
}

// hugeExponent stands for an exponent of more than 9 digits, either way,
// which no digits that a string holds bring back within a double's range.
const hugeExponent = 1_000_000_000;

// nearest returns s, a string of the grammar that isFloatForm checks, as the
// double nearest it, 0 for one too small for a double, and whether it is
// within a double's range, however many digits s has, and however large its
// exponent. It writes s's digits without the zeros around them as 0.ddd,
// with the exponent that gives s's value, which Number() reads correctly
// rounded, once it has told that exponent from one beyond a double's range,
// either way. It writes at most maxDigits of the digits, and 1 after them for
// the rest when any of the rest is not 0 (significantDigits), which reads as
// the same double, so that it costs no more for a string of a million digits
// than for one of a thousand.
function nearest(s: string): [number, boolean] {
  const [body, negative] = cutSign(s);
  const [mantissa, exponent] = splitExponent(body);
  const [whole, fraction = ""] = splitPoint(mantissa);
  // The digits of whole and of fraction, from the first that is not 0, are
  // lead and then tail.
  const lead = trimLeadingZeros(whole);
  const tail = lead === "" ? trimLeadingZeros(fraction) : fraction;
  const digits = lead.length + tail.length;
  const significant = significantDigits(lead, tail);
  if (significant === "") {
    return [0, true];
  }
  let e = 0;
  if (exponent !== undefined) {
    const [sign, negativeExponent] = cutSign(exponent);
    const magnitude = trimLeadingZeros(sign);
    if (magnitude.length > 9) {
      e = hugeExponent;
    } else if (magnitude !== "") {
      e = Number(magnitude);
    }
    if (negativeExponent) {
      e = -e;
    }
  }
  // The value is 0.significant times ten to the power of point: at least
  // 1e310, past a double's range, if point is more than 310, and less than
  // 1e-400, which rounds to 0, if it is less than -400.
  const point = digits - fraction.length + e;
  if (point > 310) {
    return [0, false];
  }
  if (point < -400) {
    return [0, true];
  }
  const f = Number(`0.${significant}e${point}`);
  if (!Number.isFinite(f)) {
    return [0, false];
  }
  return [negative ? -f : f, true];
}

// maxDigits is how many of a number's significant digits nearest writes:
// more than the 768 that the exact value of a double, or of the point halfway
// between two, has at most, so that two numbers whose first maxDigits
// significant digits are the same, and whose digits after them are 0, or are
// not all 0, are each on the same side of every double and of every point
// halfway between two, and so have the same nearest double.
const maxDigits = 800;

// significantDigits returns the digits of lead and then tail, the first of
// which is not 0, without the zeros after the last that is not, or "" if
// there is none: at most maxDigits of them, and then 1, standing for the
// rest, when any of the rest is not 0, which reads as the same double as all
// of them would (maxDigits).
function significantDigits(lead: string, tail: string): string {
  let kept = "";
  let rest = false;
  for (const part of [lead, tail]) {
    const n = Math.min(part.length, maxDigits - kept.length);
    kept += part.slice(0, n);
    rest = rest || trimLeadingZeros(part.slice(n)) !== "";
  }
  if (rest) {
    return `${kept}1`;
  }
  return kept.replace(/0+$/, "");
}

/**
 * Returns n as to_string writes an int (§5.8.9): a plain decimal, with a
 * leading - when it is negative.
 */
export function formatInt(n: number): string {
  return String(zero(n));
}

/**
 * Returns f, a finite double, as to_string writes a float (§5.8.9), as
 * interpolation shows one without a format specifier: the shortest decimal
 * that reads as f again, with . as its point, whatever the locale, in
 * positional notation for a magnitude from 1e-6 to less than 1e21, with one
 * digit after the point for an integral value, as 2.0, and otherwise as the
 * shortest digits, with a point after the first only when there are more, e,
 * the exponent's sign, and the exponent without leading zeros, as 1e+21 or
 * 1.5e-7, as JavaScript's String writes them, which it is. 0 and -0 are 0.0.
 */
export function formatFloat(f: number): string {
  const a = Math.abs(f);
  if (a === 0 || (a >= 1e-6 && a < 1e21)) {
    const s = String(zero(f));
    return s.includes(".") ? s : `${s}.0`;
  }
  return String(f);
}

// shortest returns the shortest digits of a, a finite double that is not
// negative, that read as a again, the nearest to it of those, as String
// picks them, and the power of ten that their first stands for, as 1.005 is
// "1005" and 0, and 0 is "0" and 0.
function shortest(a: number): [string, number] {
  const s = String(a);
  const [mantissa, exponent] = splitExponent(s);
  if (exponent !== undefined) {
    return [mantissa.replace(".", ""), Number(exponent)];
  }
  const [whole, fraction = ""] = splitPoint(s);
  if (whole !== "0") {
    return [whole + fraction, whole.length - 1];
  }
  const digits = trimLeadingZeros(fraction);
  if (digits === "") {
    return ["0", 0];
  }
  return [digits, digits.length - fraction.length - 1];
}

/**
 * Returns f, a finite double, as a text value reference whose format
 * specifier is decimals digits shows a number (FR-TEXT-02, §5.8.9): the
 * decimal that formatFloat writes, rounded half away from zero to decimals
 * digits after the point, written in positional notation however large or
 * small it is, with exactly decimals digits after the point, without a point
 * when decimals is 0, and without a sign when it rounds to zero. It rounds
 * that decimal rather than the double's exact value, so that 1.005, whose
 * double is a little less than 1.005, shows as 1.01 with 2 digits, where
 * toFixed shows 1.00. An int is shown as the float that it widens to,
 * exactly: 5 with 2 digits is 5.00. A decimals beyond 0 to MAX_DECIMALS,
 * which no format specifier has, is the nearer of the two.
 */
export function formatFixed(f: number, decimals: number): string {
  const d = Math.min(Math.max(Math.trunc(decimals) || 0, 0), MAX_DECIMALS);
  const [digits, e] = shortest(Math.abs(f));
  // The digits down to 10^-d, which are |f| times 10^d as an integer,
  // rounded up when the first digit after them is 5 or more: a tie is
  // rounded away from zero, which for |f| is up.
  const keep = e + 1 + d;
  let kept = "";
  if (keep < 0) {
    // |f| is less than half of 10^-d.
  } else if (keep >= digits.length) {
    kept = digits + "0".repeat(keep - digits.length);
  } else if (digits[keep] >= "5") {
    kept = increment(digits.slice(0, keep));
  } else {
    kept = digits.slice(0, keep);
  }
  kept = trimLeadingZeros(kept);
  const sign = f < 0 && kept !== "" ? "-" : "";
  if (kept.length <= d) {
    kept = "0".repeat(d + 1 - kept.length) + kept;
  }
  const whole = kept.slice(0, kept.length - d);
  if (d === 0) {
    return sign + whole;
  }
  return `${sign}${whole}.${kept.slice(kept.length - d)}`;
}

// increment returns the decimal digits n, which may be none, plus one.
function increment(n: string): string {
  const b = n.split("");
  for (let i = b.length - 1; i >= 0; i--) {
    if (b[i] < "9") {
      b[i] = String(Number(b[i]) + 1);
      return b.join("");
    }
    b[i] = "0";
  }
  return `1${b.join("")}`;
}

/**
 * Returns the number of code points of s, as len counts them (§5.8.4,
 * §5.8.9), not its UTF-16 code units, as s.length does: len("😀") is 1.
 */
export function len(s: string): number {
  let n = 0;
  for (const _ of s) {
    n++;
  }
  return n;
}

/**
 * Returns a + b, the strings one after the other, as + of two strings does
 * (§5.8.3), or string_too_long and "" when that has more than
 * MAX_STRING_LENGTH code points, so that no script, however many of its
 * statements double a string, builds one longer than a string literal may
 * be. It counts code points, so that "😀", one code point and two UTF-16
 * code units, counts as one. Of strings of at most MAX_STRING_LENGTH code
 * units together, which have at most as many code points, it counts none.
 */
export function concat(a: string, b: string): Result<string> {
  if (
    a.length + b.length > MAX_STRING_LENGTH &&
    len(a) + len(b) > MAX_STRING_LENGTH
  ) {
    return ["", "string_too_long"];
  }
  return ok(a + b);
}

/**
 * Returns -1, 0, or +1 as a is before, equal to, or after b, in the
 * lexicographic order of their code points, as <, <=, >, and >= compare
 * strings (§5.8.3, §5.8.9): the string of U+FFFF is before "😀", U+1F600,
 * though JavaScript's < on UTF-16 code units puts "😀" first, comparing its
 * high surrogate, U+D83D, with U+FFFF. Neither string is normalised, so "é",
 * U+00E9, and e followed by U+0301 COMBINING ACUTE ACCENT are not equal.
 */
export function compare(a: string, b: string): number {
  const n = Math.min(a.length, b.length);
  for (let i = 0; i < n; i++) {
    if (a.charCodeAt(i) !== b.charCodeAt(i)) {
      // The code points at the first code unit that differs, which is the
      // first of each code point that differs, or the low surrogates of two
      // pairs whose high ones are alike, which compare as their code points
      // do.
      return (a.codePointAt(i) ?? 0) < (b.codePointAt(i) ?? 0) ? -1 : 1;
    }
  }
  return Math.sign(a.length - b.length);
}

/**
 * Reports whether s holds sub, as contains does (§5.8.4), comparing code
 * points: of strings of whole code points, as every string of the language
 * is, a match of the code units of one in the other's is a match of their
 * code points, since a high surrogate always starts one and a low one always
 * ends one. Neither string is normalised, as compare says.
 */
export function contains(s: string, sub: string): boolean {
  return s.includes(sub);
}

/** Reports whether s starts with prefix, as contains says. */
export function startsWith(s: string, prefix: string): boolean {
  return s.startsWith(prefix);
}

/** Reports whether s ends with suffix, as contains says. */
export function endsWith(s: string, suffix: string): boolean {
  return s.endsWith(suffix);
}

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
