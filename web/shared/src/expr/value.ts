// The values of the language as an evaluator holds them, and how a value of
// the published format, a save state, or the corpus is read by its
// declaration (PRD §5.8.9).

import type { ValueType } from "./ast";
import { textProblem } from "./encode";
import { JsonNumber, type JsonValue, MAX_EXPONENT } from "./json";
import {
  formatFloat,
  formatInt,
  len,
  MAX_INT,
  MAX_STRING_LENGTH,
  parseFloatText,
} from "./semantics";

/**
 * A value of the language as an evaluator holds it (PRD §5.8.3): a bool, an
 * int, a float, or a string, by its type, whose value is in bool, int,
 * float, or str. An int is within ±(2^53 − 1), a float finite and never -0,
 * and a string of code points, as every value of the language is (§5.8.9).
 * NONE is none, of no type, as the Go package's zero Value is.
 */
export interface Value {
  readonly type: ValueType | "";
  readonly bool: boolean;
  readonly int: number;
  readonly float: number;
  readonly str: string;
}

/** No value, of no type. */
export const NONE: Value = { type: "", bool: false, int: 0, float: 0, str: "" };

/** Returns the bool b. */
export function boolValue(b: boolean): Value {
  return { ...NONE, type: "bool", bool: b };
}

/** Returns the int n. */
export function intValue(n: number): Value {
  return { ...NONE, type: "int", int: n === 0 ? 0 : n };
}

/** Returns the float f. */
export function floatValue(f: number): Value {
  return { ...NONE, type: "float", float: f === 0 ? 0 : f };
}

/** Returns the string s. */
export function stringValue(s: string): Value {
  return { ...NONE, type: "string", str: s };
}

/** Reports whether a and b are one value, of one type. */
export function sameValue(a: Value, b: Value): boolean {
  return (
    a.type === b.type &&
    a.bool === b.bool &&
    a.int === b.int &&
    Object.is(a.float, b.float) &&
    a.str === b.str
  );
}

/**
 * Returns v as to_string writes it (§5.8.9): an int as a plain decimal, a
 * float as its shortest round-tripping decimal, a bool as true or false, and
 * a string as it is.
 */
export function valueString(v: Value): string {
  switch (v.type) {
    case "bool":
      return v.bool ? "true" : "false";
    case "int":
      return formatInt(v.int);
    case "float":
      return formatFloat(v.float);
  }
  return v.str;
}

/** Returns v, a number, as a float: an int widened exactly. */
export function asFloat(v: Value): number {
  return v.type === "int" ? v.int : v.float;
}

/** The most characters that a text attribute's value may have. */
export const MAX_TEXT_LENGTH = 100_000;

/** The types of a value that a declaration types: a variable's or an attribute's. */
export type DeclaredType = ValueType | "text" | "enum";

/**
 * Returns json, a value of the published format, of a save state, or of the
 * corpus, as a value of the declared type typ, an enum's one of options, as
 * a value is typed by its declaration, never by its JSON form (§5.8.9), or
 * says why it is none, as the Go runtime's valueOf does: a bool is true or
 * false; an int is a number that is an integer within ±(2^53 − 1), however it
 * is written, as 3, 3.0, or 3e0, read as a decimal, exactly, so that one that
 * rounds to an integer as a double, as 4503599627370495.5, is none; a float a
 * number that a double holds, read as the double nearest it, as to_float
 * reads it, -0 as 0; and a string, a text, or an enum's option a string of
 * at most 4,096 code points, or 100,000 for a text, without a NUL or a lone
 * surrogate, an enum's one of its options. A number is written with an
 * exponent within ±999, if with one. A JavaScript number is read as String
 * writes it, which every finite number's is.
 */
export function readValue(
  json: JsonValue | number | undefined,
  typ: DeclaredType,
  options: readonly string[] = [],
): [Value, string | undefined] {
  if (json === undefined) {
    return [NONE, "is missing"];
  }
  const v = typeof json === "number" ? new JsonNumber(String(json)) : json;
  const kind =
    typeof v === "boolean"
      ? "boolean"
      : typeof v === "string"
        ? "string"
        : v instanceof JsonNumber
          ? "number"
          : "";
  if (kind === "") {
    return [NONE, "is neither a boolean, a number, nor a string"];
  }
  if ((typ === "int" || typ === "float") && v instanceof JsonNumber) {
    return readNumber(v.raw, typ);
  }
  if (typ === "bool" && typeof v === "boolean") {
    return [boolValue(v), undefined];
  }
  if (
    (typ === "string" || typ === "text" || typ === "enum") &&
    typeof v === "string"
  ) {
    if (typ === "enum" && !options.includes(v)) {
      return [NONE, "is not one of the enum's options"];
    }
    const bound = typ === "text" ? MAX_TEXT_LENGTH : MAX_STRING_LENGTH;
    if (textProblem(v) !== undefined || len(v) > bound) {
      return [
        NONE,
        `is a value of the type ${typ} of more than ${bound} characters, or with a NUL or a lone surrogate, which no value is`,
      ];
    }
    return [stringValue(v), undefined];
  }
  return [NONE, `is a ${kind}, not a value of the type ${typ}`];
}

// readNumber returns raw, a JSON number, as an int or a float.
function readNumber(
  raw: string,
  typ: "int" | "float",
): [Value, string | undefined] {
  const form = formProblem(raw);
  if (form !== undefined) {
    return [NONE, form];
  }
  if (typ === "int") {
    const [n, problem] = integerOf(raw);
    return problem === undefined ? [intValue(n), undefined] : [NONE, problem];
  }
  // Every JSON number is of the grammar that to_float reads.
  const [f, problem] = parseFloatText(raw);
  if (problem !== undefined) {
    return [
      NONE,
      "is beyond the range of a float, a double, which holds no number greater than about 1.8e308",
    ];
  }
  return [floatValue(f), undefined];
}

/**
 * Says why raw, a JSON number, is written with an exponent beyond
 * ±MAX_EXPONENT, whatever its value, or returns undefined if it is not. The
 * exponent is read without its leading zeros, however many.
 */
export function formProblem(raw: string): string | undefined {
  const e = raw.search(/[eE]/);
  if (e < 0) {
    return undefined;
  }
  const exponent = raw
    .slice(e + 1)
    .replace(/^[+-]+/, "")
    .replace(/^0+/, "");
  if (
    exponent !== "" &&
    (exponent.length > 3 || Number(exponent) > MAX_EXPONENT)
  ) {
    return `is written with an exponent beyond ±${MAX_EXPONENT}, which no int or float needs to be`;
  }
  return undefined;
}

/**
 * Returns the value of raw, a JSON number whose exponent is within
 * ±MAX_EXPONENT, if it is an integer within ±MAX_INT, or says why it is not
 * one. It reads raw as a decimal, exactly, however long it is.
 */
export function integerOf(raw: string): [number, string | undefined] {
  const notInteger = "is not an integer, which an int is";
  const tooLarge = `is not an integer from -${MAX_INT} to ${MAX_INT}, which every runtime holds exactly`;
  const negative = raw.startsWith("-");
  const s = negative ? raw.slice(1) : raw;
  const e = s.search(/[eE]/);
  const mantissa = e < 0 ? s : s.slice(0, e);
  const exponent = e < 0 ? "" : s.slice(e + 1);
  const point = mantissa.indexOf(".");
  const whole = point < 0 ? mantissa : mantissa.slice(0, point);
  const fraction = point < 0 ? "" : mantissa.slice(point + 1);
  let digits = (whole + fraction).replace(/^0+/, "");
  if (digits === "") {
    return [0, undefined];
  }
  // The value is digits times ten to the power of shift, which the
  // exponent's bound bounds.
  let shift = (exponent === "" ? 0 : Number(exponent)) - fraction.length;
  while (digits.endsWith("0")) {
    digits = digits.slice(0, -1);
    shift++;
  }
  if (shift < 0) {
    return [0, notInteger];
  }
  if (digits.length + shift > String(MAX_INT).length) {
    return [0, tooLarge];
  }
  const n = Number(digits + "0".repeat(shift));
  if (n > MAX_INT) {
    return [0, tooLarge];
  }
  return [negative ? -n || 0 : n, undefined];
}

/**
 * Returns v, of the type typ, as the published format and a save state hold
 * a value: a boolean, a number, or a string, or undefined of a v that is not
 * of a type that typ reads, an int for a float being one.
 */
export function jsonOf(
  v: Value,
  typ: DeclaredType,
): boolean | number | string | undefined {
  let json: boolean | number | string | undefined;
  switch (v.type) {
    case "bool":
      json = v.bool;
      break;
    case "int":
      json = v.int;
      break;
    case "float":
      json = v.float;
      break;
    case "string":
      json = v.str;
      break;
  }
  const [, problem] = readValue(json, typ);
  return problem === undefined ? json : undefined;
}

// The significant digits of the greatest double, as its shortest form writes
// them, 1.7976931348623157e+308.
const maxDoubleDigits = "17976931348623157";

/**
 * Reports whether raw, a JSON number that readValue takes as a float, is
 * beyond the bounds that the schemas give a float, ±1.7976931348623157e+308,
 * with which JSON Schema compares the number as it is written, though a
 * double, and so readValue, reads every number from there to half a unit in
 * the last place beyond the greatest double as the greatest double, as
 * 1.7976931348623158e308, which no number that the server writes is.
 */
export function floatBeyondSchema(raw: string): boolean {
  if (Math.abs(Number(raw)) !== Number.MAX_VALUE) {
    // Within the bounds, or beyond a double's range, which readValue refuses.
    return false;
  }
  // raw is within half a unit in the last place of the greatest double, so
  // that its first significant digit is of the same power of ten as the
  // bound's, and it is beyond the bound exactly when its significant digits,
  // read as a fraction, are greater than the bound's.
  let mantissa = raw.replace(/^[+-]/, "");
  const e = mantissa.search(/[eE]/);
  if (e >= 0) {
    mantissa = mantissa.slice(0, e);
  }
  const digits = mantissa
    .replace(".", "")
    .replace(/^0+/, "")
    .replace(/0+$/, "");
  return digits > maxDoubleDigits;
}
