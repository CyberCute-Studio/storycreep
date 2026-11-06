// The typed values of a project's content (PRD FR-VAR-01, FR-COMP-01): a
// variable's default, and an attribute definition's default and its
// components' values, each of a type of the expression language, as the
// fields of a panel show and read them. A field reads what the user typed as
// the server would read it, or says that it is none of the type, which the
// field shows before anything is sent; the server, which reads every value
// again, is the authority (value package).

import type { Attribute, Variable } from "@storycreep/shared/api";

/** The type of an attribute definition, which a variable's is one of. */
export type ValueType = Attribute["type"];

/** A value of a type. */
export type Value = Attribute["default"];

/** The types of a variable (PRD FR-VAR-01). */
export const variableTypes = [
  "bool",
  "int",
  "float",
  "string",
] as const satisfies readonly Variable["type"][];

/** The types of an attribute definition (PRD FR-COMP-01). */
export const attributeTypes = [
  ...variableTypes,
  "text",
  "enum",
] as const satisfies readonly ValueType[];

/** The largest whole number that every runtime holds exactly, which an int is within (PRD §5.8.9). */
const intBound = 2 ** 53 - 1;

/** Returns the zero value of type, with options for an enum, which the server gives one created without a value. */
export function zeroOf(
  type: ValueType,
  options: readonly string[] = [],
): Value {
  switch (type) {
    case "bool":
      return false;
    case "int":
    case "float":
      return 0;
    case "enum":
      return options[0] ?? "";
  }
  return "";
}

/** Returns the text that a field shows of value. */
export function textOf(value: Value): string {
  return typeof value === "string" ? value : String(value);
}

/**
 * Returns the value of type that text spells, or undefined if it spells
 * none: true or false for a bool; a whole number within ±(2^53 − 1) for an
 * int, without a fraction or an exponent; a finite decimal number for a
 * float, with a point and an exponent if any, as 0.5 or 1e3, read as the
 * nearest double, as the server reads it; any text for a string or a text,
 * whose length and characters the server checks; and one of options for an
 * enum.
 */
export function parseValue(
  type: ValueType,
  text: string,
  options: readonly string[] = [],
): Value | undefined {
  switch (type) {
    case "bool":
      return text === "true" ? true : text === "false" ? false : undefined;
    case "int": {
      const spelled = text.trim();
      if (!/^[+-]?\d+$/.test(spelled)) {
        return undefined;
      }
      const n = Number(spelled);
      return Math.abs(n) <= intBound ? n : undefined;
    }
    case "float": {
      const spelled = text.trim();
      if (!/^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$/.test(spelled)) {
        return undefined;
      }
      const n = Number(spelled);
      return Number.isFinite(n) ? n : undefined;
    }
    case "enum":
      return options.includes(text) ? text : undefined;
  }
  return text;
}

/**
 * Returns what value becomes as its type changes to type, as a field shows
 * it once the user chooses another type: the same value where it spells one
 * of the new type, as 3 of an int as a float, or the new type's zero value.
 */
export function convertedTo(
  type: ValueType,
  value: Value,
  options: readonly string[] = [],
): Value {
  return parseValue(type, textOf(value), options) ?? zeroOf(type, options);
}

/**
 * Returns the options of an enum that text lists, one to a line, as a field
 * of them reads them: each without the white space around it, which an
 * option may not have, and without the blank lines.
 */
export function optionsOf(text: string): string[] {
  return text
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line !== "");
}
