// A validator of the published format's JSON Schemas (schema/published/v1,
// schema/save-state/v1), of the keywords that they use, which the flow's
// loader checks each file against before it reads it.

import {
  compareNumbers,
  isIntegral,
  isJsonObject,
  JsonNumber,
  semantics,
} from "../expr";

/** A JSON Schema, or a document of them, as the schema files hold them. */
export type Schema = boolean | { readonly [keyword: string]: unknown };

/**
 * The keywords that validate reads. A document that uses another, which a
 * later version of the generator might, is refused when it is first read
 * (keywordsOf), so that no keyword is ignored without anyone noticing.
 */
const keywords = new Set([
  "$defs",
  "$id",
  "$ref",
  "$schema",
  "additionalProperties",
  "const",
  "default",
  "dependentRequired",
  "description",
  "enum",
  "examples",
  "items",
  "maxItems",
  "maxLength",
  "maximum",
  "minItems",
  "minLength",
  "minimum",
  "oneOf",
  "pattern",
  "properties",
  "propertyNames",
  "required",
  "title",
  "type",
]);

/**
 * How deeply an instance may nest within what validate reads of it. No file
 * of the format nests deeper than its ASTs, which are at most 64 deep, each
 * level of which is at most two levels of JSON, as a call's array of
 * arguments; a key that its schema does not define is any value (AnyValue),
 * which validate does not descend into.
 */
const maxDepth = 256;

/**
 * The definition of a value that the format does not define: any JSON value
 * but null, which readJSON never returns, so that validate takes every value
 * that it is given there without reading it, however deep.
 */
const anyValue = "#/$defs/AnyValue";

/**
 * Returns why instance is not what the document's root schema takes, at the
 * JSON pointer of the value that it does not take, or undefined if it is.
 * instance is a value that readJSON returned, whose numbers are JsonNumbers
 * that it compares as they are written, or one whose numbers are
 * JavaScript's, as a schema's example, which it compares as String writes
 * them, and which may hold a null where the format takes any value but null
 * (AnyValue), which it takes there as it takes any. It throws an Error if
 * the document uses a keyword that it does not read.
 */
export function validate(
  document: Schema,
  instance: unknown,
): string | undefined {
  checkKeywords(document);
  return new Validator(document).valid(document, instance, "", 0);
}

// checked are the documents whose keywords checkKeywords has read.
const checked = new WeakSet<object>();

// checkKeywords throws if a schema of the document uses a keyword that
// validate does not read.
function checkKeywords(document: Schema): void {
  if (typeof document !== "object" || checked.has(document)) {
    return;
  }
  const stack: unknown[] = [document];
  for (let s = stack.pop(); s !== undefined; s = stack.pop()) {
    if (typeof s !== "object" || s === null) {
      continue;
    }
    for (const [k, v] of Object.entries(s)) {
      if (!keywords.has(k)) {
        throw new Error(
          `flow: a schema uses the keyword ${k}, which the validator does not read`,
        );
      }
      // dependentRequired's values are lists of keys, not schemas.
      if (k === "properties" || k === "$defs") {
        stack.push(...Object.values(v as object));
      } else if (k === "oneOf") {
        stack.push(...(v as unknown[]));
      } else if (
        k === "items" ||
        k === "additionalProperties" ||
        k === "propertyNames"
      ) {
        stack.push(v);
      }
    }
  }
  checked.add(document);
}

// rawOf returns the written form of a number, of readJSON's or of
// JavaScript's.
function rawOf(v: unknown): string | undefined {
  if (v instanceof JsonNumber) {
    return v.raw;
  }
  return typeof v === "number" && Number.isFinite(v) ? String(v) : undefined;
}

// typeOf returns the JSON type of v, as JSON Schema's type names it, but
// that an integral number is "integer" too (isOfType).
function isOfType(v: unknown, type: string): boolean {
  switch (type) {
    case "object":
      return isJsonObject(v);
    case "array":
      return Array.isArray(v);
    case "string":
      return typeof v === "string";
    case "boolean":
      return typeof v === "boolean";
    case "number":
      return rawOf(v) !== undefined;
    case "integer": {
      const raw = rawOf(v);
      return raw !== undefined && isIntegral(raw);
    }
  }
  return false;
}

// sameJSON reports whether v is the JSON value c, a schema's const.
function sameJSON(v: unknown, c: unknown): boolean {
  if (typeof c === "number") {
    const raw = rawOf(v);
    return raw !== undefined && compareNumbers(raw, String(c)) === 0;
  }
  return v === c;
}

const patterns = new Map<string, RegExp>();

function pattern(p: string): RegExp {
  let re = patterns.get(p);
  if (re === undefined) {
    re = new RegExp(p, "u");
    patterns.set(p, re);
  }
  return re;
}

// at returns the JSON pointer of the key under the pointer path.
function at(path: string, key: string | number): string {
  return `${path}/${String(key).replace(/~/g, "~0").replace(/\//g, "~1")}`;
}

class Validator {
  constructor(private readonly document: Schema) {}

  // resolve returns the definition that ref names, #/$defs/<name>.
  private resolve(ref: string): Schema {
    const name = ref.startsWith("#/$defs/")
      ? ref.slice("#/$defs/".length)
      : undefined;
    const defs =
      typeof this.document === "object"
        ? (this.document.$defs as Record<string, Schema>)
        : undefined;
    if (
      name === undefined ||
      defs === undefined ||
      !Object.hasOwn(defs, name)
    ) {
      throw new Error(
        `flow: a schema refers to ${ref}, which its document does not define`,
      );
    }
    return defs[name];
  }

  // valid returns why v, at the pointer path, depth deep, is not what s
  // takes, or undefined.
  valid(
    s: Schema,
    v: unknown,
    path: string,
    depth: number,
  ): string | undefined {
    if (s === true) {
      return undefined;
    }
    if (s === false) {
      return `${path || "/"} is there, where it may not be`;
    }
    if (depth > maxDepth) {
      return `${path} nests deeper than any file of the format`;
    }
    const where = path || "/";
    if (typeof s.$ref === "string") {
      if (s.$ref === anyValue) {
        return undefined;
      }
      const problem = this.valid(this.resolve(s.$ref), v, path, depth);
      if (problem !== undefined) {
        return problem;
      }
    }
    if (typeof s.type === "string" && !isOfType(v, s.type)) {
      return `${where} is not of the type ${s.type}`;
    }
    if (Object.hasOwn(s, "const") && !sameJSON(v, s.const)) {
      return `${where} is not ${JSON.stringify(s.const)}`;
    }
    if (Array.isArray(s.enum) && !s.enum.some((c) => sameJSON(v, c))) {
      return `${where} is none of the values ${JSON.stringify(s.enum)}`;
    }
    if (Array.isArray(s.oneOf)) {
      const problem = this.oneOf(s.oneOf as Schema[], v, path, depth);
      if (problem !== undefined) {
        return problem;
      }
    }
    if (typeof v === "string") {
      // JSON Schema counts a string's length in code points.
      const n = semantics.len(v);
      if (typeof s.minLength === "number" && n < s.minLength) {
        return `${where} is shorter than ${s.minLength} characters`;
      }
      if (typeof s.maxLength === "number" && n > s.maxLength) {
        return `${where} is longer than ${s.maxLength} characters`;
      }
      if (typeof s.pattern === "string" && !pattern(s.pattern).test(v)) {
        return `${where} is not of the pattern ${s.pattern}`;
      }
    }
    const raw = rawOf(v);
    if (raw !== undefined) {
      if (
        typeof s.minimum === "number" &&
        compareNumbers(raw, String(s.minimum)) < 0
      ) {
        return `${where} is less than ${s.minimum}`;
      }
      if (
        typeof s.maximum === "number" &&
        compareNumbers(raw, String(s.maximum)) > 0
      ) {
        return `${where} is greater than ${s.maximum}`;
      }
    }
    if (Array.isArray(v)) {
      return this.array(s, v, path, depth);
    }
    if (isJsonObject(v)) {
      return this.object(s, v, path, depth);
    }
    return undefined;
  }

  // oneOf returns why v is not exactly one of the variants: of none, the
  // problem of the variant whose constants it has, if one is, and of two,
  // that it is.
  private oneOf(
    variants: Schema[],
    v: unknown,
    path: string,
    depth: number,
  ): string | undefined {
    let matches = 0;
    let closest: string | undefined;
    for (const variant of variants) {
      const problem = this.valid(variant, v, path, depth);
      if (problem === undefined) {
        matches++;
      } else if (closest === undefined && this.hasConstants(variant, v)) {
        closest = problem;
      }
    }
    if (matches === 1) {
      return undefined;
    }
    if (matches > 1) {
      return `${path || "/"} is more than one of its variants`;
    }
    return closest ?? `${path || "/"} is none of its variants`;
  }

  // hasConstants reports whether v has each property that the schema s, or
  // the definition that it refers to, gives a constant, of that constant,
  // as a variant's type, kind, or channel, and has at least one.
  private hasConstants(s: Schema, v: unknown): boolean {
    const schema =
      typeof s === "object" && typeof s.$ref === "string"
        ? this.resolve(s.$ref)
        : s;
    if (
      typeof schema !== "object" ||
      !isJsonObject(v) ||
      !isJsonObject(schema.properties)
    ) {
      return false;
    }
    let any = false;
    for (const [k, p] of Object.entries(
      schema.properties as Record<string, Schema>,
    )) {
      if (typeof p === "object" && Object.hasOwn(p, "const")) {
        if (!Object.hasOwn(v, k) || !sameJSON(v[k], p.const)) {
          return false;
        }
        any = true;
      }
    }
    return any;
  }

  private array(
    s: Schema & object,
    v: unknown[],
    path: string,
    depth: number,
  ): string | undefined {
    const where = path || "/";
    if (typeof s.minItems === "number" && v.length < s.minItems) {
      return `${where} has fewer than ${s.minItems} items`;
    }
    if (typeof s.maxItems === "number" && v.length > s.maxItems) {
      return `${where} has more than ${s.maxItems} items`;
    }
    if (s.items !== undefined) {
      for (let i = 0; i < v.length; i++) {
        const problem = this.valid(
          s.items as Schema,
          v[i],
          at(path, i),
          depth + 1,
        );
        if (problem !== undefined) {
          return problem;
        }
      }
    }
    return undefined;
  }

  // object checks an object's properties: first those whose schemas are
  // constants or false, which tell a variant from another, then the keys
  // that it requires, and then the rest, so that a variant that v is not
  // fails before reading the rest of v.
  private object(
    s: Schema & object,
    v: { [key: string]: unknown },
    path: string,
    depth: number,
  ): string | undefined {
    const where = path || "/";
    const properties = (
      isJsonObject(s.properties) ? s.properties : {}
    ) as Record<string, Schema>;
    const telling = (p: Schema) =>
      p === false || (typeof p === "object" && Object.hasOwn(p, "const"));
    for (const [k, p] of Object.entries(properties)) {
      if (telling(p) && Object.hasOwn(v, k)) {
        const problem = this.valid(p, v[k], at(path, k), depth + 1);
        if (problem !== undefined) {
          return problem;
        }
      }
    }
    if (Array.isArray(s.required)) {
      for (const k of s.required as string[]) {
        if (!Object.hasOwn(v, k)) {
          return `${where} has no ${k}`;
        }
      }
    }
    if (isJsonObject(s.dependentRequired)) {
      for (const [k, deps] of Object.entries(s.dependentRequired)) {
        if (Object.hasOwn(v, k)) {
          for (const d of deps as string[]) {
            if (!Object.hasOwn(v, d)) {
              return `${where} has ${k} without ${d}`;
            }
          }
        }
      }
    }
    for (const k of Object.keys(v)) {
      if (s.propertyNames !== undefined) {
        const problem = this.valid(
          s.propertyNames as Schema,
          k,
          at(path, k),
          depth + 1,
        );
        if (problem !== undefined) {
          return `the key ${JSON.stringify(k)} of ${where}: ${problem}`;
        }
      }
      let p: Schema | undefined;
      if (Object.hasOwn(properties, k)) {
        p = properties[k];
        if (telling(p)) {
          continue;
        }
      } else {
        p = s.additionalProperties as Schema | undefined;
      }
      if (p !== undefined) {
        const problem = this.valid(p, v[k], at(path, k), depth + 1);
        if (problem !== undefined) {
          return problem;
        }
      }
    }
    return undefined;
  }
}
