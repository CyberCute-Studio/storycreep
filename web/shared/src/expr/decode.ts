// The reader of a published AST (§5.8.8, §6.5): the TypeScript twin of the
// Go package server/internal/expr's Decode.

import { type IdKind, isCanonicalId } from "../ids";
import {
  type AssignOp,
  type BinaryOp,
  type Context,
  isIdentifier,
  type Kind,
  MAX_ARGUMENTS,
  MAX_DEPTH,
  MAX_IDENTIFIER_LENGTH,
  MAX_NODES,
  MAX_STATEMENTS,
  NO_RANGE,
  type Node,
  type UnaryOp,
} from "./ast";
import { literalProblem } from "./encode";
import { checkContext } from "./error";
import { isJsonObject, JsonNumber, type JsonValue, readJSON } from "./json";
import {
  ASSIGN_OPS,
  BINARY_OPS,
  checkPlace,
  type Place,
  UNARY_OPS,
} from "./shape";
import { floatBeyondSchema, readValue } from "./value";

/** The error of JSON that is not the AST of a published expression. */
export class DecodeError extends Error {
  constructor(message: string) {
    super(`expr: not the JSON of a published AST: ${message}`);
    this.name = "DecodeError";
  }
}

const kinds = new Set<Kind>([
  "lit",
  "var",
  "attr",
  "node_ref",
  "unary",
  "binary",
  "call",
  "assign",
  "script",
]);

const attributeName =
  /^[A-Za-z_][A-Za-z0-9_]{0,63}\.[A-Za-z_][A-Za-z0-9_]{0,63}$/;

/**
 * Returns the AST that json holds as the published format holds one (§5.8.8,
 * §6.5): the JSON that encode writes of an AST whose names check has
 * resolved, of a condition when ctx is condition, an expression, and of a
 * script when ctx is script. json is the text, or a value that readJSON
 * returned, or one that JSON.parse returned, whose numbers are read as the
 * doubles that they are. Encode writes the AST that it returns as json's
 * canonical form. Its nodes have no source, and an empty range at 0.
 *
 * The AST's names are resolved as the JSON holds them: a variable is its key
 * and its ref, without the scope that its source qualified it with, and an
 * attribute its keys and its refs, without the cmp. that its source may have
 * named it with, so that what reads it against a project's declarations
 * reads its names by their refs (checkResolved), not anew by their keys
 * (check).
 *
 * It reads what the AST's schema takes, and nothing else, and ignores a key
 * that an object of the AST does not define, as a consumer does (§6.4), so
 * that it throws a DecodeError on: JSON that readJSON refuses; a node of an
 * unknown kind, or with an unknown operator or a literal of an unknown type;
 * a node at a place that its kind cannot be at; a field that is missing or of
 * another type than its kind's; a literal's value that is not of its type,
 * as an int that is not integral or is beyond ±(2^53 − 1), though one written
 * as 2.0 is the int 2 (§5.8.9), a float beyond ±1.7976931348623157e+308, as
 * it is written, or a string of more than 4,096 characters or that holds a
 * NUL; a variable's name or a call's function that is not an identifier of
 * at most 64 characters, an attribute's name that is not two such
 * identifiers joined by a dot, or a ref that is not an ID of its kind in its
 * canonical spelling; a call of more than MAX_ARGUMENTS arguments; a script
 * of more than MAX_STATEMENTS statements; and an AST of more than MAX_NODES
 * nodes, or deeper than MAX_DEPTH, its root 1 deep, so that decode never
 * recurses deeper than that. It throws a UsageError of a ctx that is neither
 * context.
 */
export function decode(json: string | JsonValue | unknown, ctx: Context): Node {
  checkContext(ctx);
  let tree: unknown = json;
  if (typeof json === "string") {
    try {
      tree = readJSON(json);
    } catch (e) {
      throw new DecodeError(`it ${(e as Error).message}`);
    }
  }
  const n = new Decoder().node(tree, "root", 1);
  if ((n.kind === "script") !== (ctx === "script")) {
    throw new DecodeError(
      `a ${ctx}'s AST is a script exactly when its context is`,
    );
  }
  return n;
}

type JsonObject = { [key: string]: unknown };

// field returns the required field key of a node of the kind kind, or
// throws when it is missing.
function field(obj: JsonObject, kind: string, key: string): unknown {
  if (!Object.hasOwn(obj, key)) {
    throw new DecodeError(`a ${kind} node has no ${key}`);
  }
  return obj[key];
}

// text returns the required field key, a string.
function text(obj: JsonObject, kind: string, key: string): string {
  const v = field(obj, kind, key);
  if (typeof v !== "string") {
    throw new DecodeError(
      `the ${key} of a ${kind} node is of another type than its kind's`,
    );
  }
  return v;
}

// id returns the required field key, an ID of an entity of the kind k in its
// canonical spelling.
function id(obj: JsonObject, kind: string, key: string, k: IdKind): string {
  const s = text(obj, kind, key);
  if (!isCanonicalId(k, s)) {
    throw new DecodeError(
      `the ${key} of a ${kind} node is not the ID of a ${k} in its canonical spelling`,
    );
  }
  return s;
}

// Decoder reads an AST's nodes, counting them.
class Decoder {
  private nodes = 0;

  // node reads the node v, at the place at and the depth depth.
  node(v: unknown, at: Place, depth: number): Node {
    if (depth > MAX_DEPTH) {
      throw new DecodeError(`the AST is deeper than ${MAX_DEPTH}`);
    }
    if (++this.nodes > MAX_NODES) {
      throw new DecodeError(`the AST has more than ${MAX_NODES} nodes`);
    }
    if (!isJsonObject(v)) {
      throw new DecodeError("a node is not an object");
    }
    const kind = v.kind;
    if (typeof kind !== "string" || !Object.hasOwn(v, "kind")) {
      throw new DecodeError("a node has no kind, or one that is not a string");
    }
    if (!kinds.has(kind as Kind)) {
      throw new DecodeError(`${JSON.stringify(kind)} is no kind of node`);
    }
    const n = this.read(v, kind as Kind, depth);
    const problem = checkPlace(n, at);
    if (problem !== undefined) {
      throw new DecodeError(problem);
    }
    return n;
  }

  private read(v: JsonObject, kind: Kind, depth: number): Node {
    const src = NO_RANGE;
    switch (kind) {
      case "lit":
        return literal(v);
      case "var": {
        const ref = id(v, kind, "ref", "variable");
        const name = text(v, kind, "name");
        if (!isIdentifier(name)) {
          throw new DecodeError(
            `the name of a var node is not an identifier of at most ${MAX_IDENTIFIER_LENGTH} characters`,
          );
        }
        return { kind, name, scope: "", key: name, ref, src };
      }
      case "attr": {
        const ref = id(v, kind, "ref", "component");
        const attribute = id(v, kind, "attribute", "attribute_def");
        const name = text(v, kind, "name");
        if (!attributeName.test(name)) {
          throw new DecodeError(
            "the name of an attr node is not two keys joined by a dot",
          );
        }
        const dot = name.indexOf(".");
        return {
          kind,
          name,
          explicit: false,
          componentKey: name.slice(0, dot),
          attributeKey: name.slice(dot + 1),
          ref,
          attribute,
          src,
        };
      }
      case "node_ref":
        return { kind, ref: id(v, kind, "ref", "node"), src };
      case "unary": {
        const op = text(v, kind, "op");
        if (!UNARY_OPS.has(op)) {
          throw new DecodeError(`${JSON.stringify(op)} is no unary operator`);
        }
        const operand = this.operand(v, kind, "operand", "operand", depth);
        return { kind, op: op as UnaryOp, operand, src };
      }
      case "binary": {
        const op = text(v, kind, "op");
        if (!BINARY_OPS.has(op)) {
          throw new DecodeError(`${JSON.stringify(op)} is no binary operator`);
        }
        const left = this.operand(v, kind, "left", "operand", depth);
        const right = this.operand(v, kind, "right", "operand", depth);
        return { kind, op: op as BinaryOp, left, right, src };
      }
      case "call": {
        const fn = text(v, kind, "function");
        if (!isIdentifier(fn)) {
          throw new DecodeError(
            `the function of a call node is not an identifier of at most ${MAX_IDENTIFIER_LENGTH} characters`,
          );
        }
        const args = this.list(
          v,
          kind,
          "args",
          "operand",
          depth,
          MAX_ARGUMENTS,
        );
        return { kind, function: fn, args, src };
      }
      case "assign": {
        const op = text(v, kind, "op");
        if (!ASSIGN_OPS.has(op)) {
          throw new DecodeError(
            `${JSON.stringify(op)} is no assignment operator`,
          );
        }
        const target = this.operand(v, kind, "target", "target", depth);
        const value = this.operand(v, kind, "value", "operand", depth);
        return {
          kind,
          op: op as AssignOp,
          target,
          value,
          src,
        };
      }
      case "script":
        return {
          kind,
          statements: this.list(
            v,
            kind,
            "statements",
            "statement",
            depth,
            MAX_STATEMENTS,
          ),
          src,
        };
    }
  }

  // operand reads the field key of a node of the kind kind, a node at the
  // place at, one deeper than the node.
  private operand(
    obj: JsonObject,
    kind: string,
    key: string,
    at: Place,
    depth: number,
  ): Node {
    return this.node(field(obj, kind, key), at, depth + 1);
  }

  // list reads the field key of a node of the kind kind, an array of at most
  // most nodes at the place at, one deeper than the node.
  private list(
    obj: JsonObject,
    kind: string,
    key: string,
    at: Place,
    depth: number,
    most: number,
  ): Node[] {
    const items = field(obj, kind, key);
    if (!Array.isArray(items)) {
      throw new DecodeError(
        `the ${key} of a ${kind} node is of another type than its kind's`,
      );
    }
    if (items.length > most) {
      throw new DecodeError(`a ${kind} node has more than ${most} ${key}`);
    }
    return items.map((item) => this.node(item, at, depth + 1));
  }
}

// literal reads a literal, whose value is read as its type says, never by its
// JSON form (§5.8.9).
function literal(obj: JsonObject): Node {
  const typ = text(obj, "lit", "type");
  const raw = field(obj, "lit", "value");
  if (typ !== "bool" && typ !== "int" && typ !== "float" && typ !== "string") {
    throw new DecodeError(`${JSON.stringify(typ)} is no type of a literal`);
  }
  const json =
    typeof raw === "number" ||
    typeof raw === "boolean" ||
    typeof raw === "string" ||
    raw instanceof JsonNumber
      ? (raw as JsonValue | number)
      : undefined;
  let [v, problem] = readValue(json, typ);
  if (json === undefined) {
    problem = "is not a boolean, a number, or a string";
  }
  if (problem === undefined && typ === "float") {
    const written = raw instanceof JsonNumber ? raw.raw : String(raw);
    if (floatBeyondSchema(written)) {
      problem =
        "is beyond ±1.7976931348623157e+308, the greatest double's shortest form, as it is written";
    }
  }
  if (problem !== undefined) {
    throw new DecodeError(
      `the value of a lit node of the type ${typ} ${problem}`,
    );
  }
  let n: Node;
  switch (typ) {
    case "bool":
      n = { kind: "lit", type: typ, value: v.bool, src: NO_RANGE };
      break;
    case "int":
      n = { kind: "lit", type: typ, value: v.int, src: NO_RANGE };
      break;
    case "float":
      n = { kind: "lit", type: typ, value: v.float, src: NO_RANGE };
      break;
    default:
      n = { kind: "lit", type: typ, value: v.str, src: NO_RANGE };
  }
  const invalid = literalProblem(n);
  if (invalid !== undefined) {
    throw new DecodeError(invalid);
  }
  return n;
}
