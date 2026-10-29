// The canonical form of an AST (§5.8.8, §6.2).

import type { Lit, Node } from "./ast";
import { UsageError } from "./error";
import { len, MAX_STRING_LENGTH } from "./semantics";
import {
  ASSIGN_OPS,
  BINARY_OPS,
  checkPlace,
  type Place,
  UNARY_OPS,
} from "./shape";

/**
 * Returns the canonical form of the AST n (§5.8.8, §6.2), the same text for
 * the same AST, whatever source it was read from (FR-EXPR-03 AC3), as the Go
 * package's Encode writes it: its JSON without white space, each object's
 * keys sorted, an int as a plain decimal, a float as the shortest decimal
 * that reads back as it, as JSON.stringify writes one, never -0; and a
 * string with ", \, and the control characters escaped, as \b, \f, \n, \r,
 * \t, or \u00XX, and U+2028 and U+2029, as Go's encoding/json writes them,
 * and nothing else escaped. A field that is not set, as the ref of a name
 * that is not resolved, is left out.
 *
 * It throws a UsageError of the kind ast on a node that is not one of an
 * AST: what is no node of a kind, an int beyond ±(2^53 − 1), a float that is
 * not finite, a string literal of more than 4,096 characters, a string that
 * holds a NUL or a lone surrogate, which no text holds, a name, a node
 * reference, or a function that is empty, an operator or a literal's type
 * that the language does not have, a node that the AST holds twice, as each
 * node of a cycle is, or a node of a kind that its place cannot be of. It
 * writes an AST of any depth without recursing.
 */
export function encode(n: Node): string {
  // The parts that it has yet to write, the next on top: a node, at its
  // place, or text.
  const stack: ({ node: unknown; at: Place } | string)[] = [
    { node: n, at: "root" },
  ];
  const seen = new Set<unknown>();
  let out = "";
  // push adds parts, so that they are written next, in their order.
  const push = (...parts: ({ node: unknown; at: Place } | string)[]) => {
    for (let i = parts.length - 1; i >= 0; i--) {
      stack.push(parts[i]);
    }
  };
  // pushList adds the nodes, each at the place at, as the elements of an
  // array, separated by commas, which closing follows.
  const pushList = (nodes: readonly unknown[], at: Place, closing: string) => {
    stack.push(closing);
    for (let i = nodes.length - 1; i >= 0; i--) {
      stack.push({ node: nodes[i], at });
      if (i > 0) {
        stack.push(",");
      }
    }
  };
  for (let part = stack.pop(); part !== undefined; part = stack.pop()) {
    if (typeof part === "string") {
      out += part;
      continue;
    }
    const problem = checkPlace(part.node, part.at);
    if (problem !== undefined) {
      throw new UsageError("ast", problem);
    }
    if (seen.has(part.node)) {
      throw new UsageError("ast", "a node is in the AST twice");
    }
    seen.add(part.node);
    const node = part.node as Node;
    switch (node.kind) {
      case "lit":
        out += `{"kind":"lit","type":"${node.type}","value":${literalJSON(node)}}`;
        break;
      case "var":
        out += `{"kind":"var","name":${required(node.name)}${optional("ref", node.ref)}}`;
        break;
      case "attr":
        out += `{${node.attribute ? `"attribute":${quoted(node.attribute)},` : ""}"kind":"attr","name":${required(node.name)}${optional("ref", node.ref)}}`;
        break;
      case "node_ref":
        out += `{"kind":"node_ref","ref":${required(node.ref)}}`;
        break;
      case "unary":
        if (!UNARY_OPS.has(node.op)) {
          throw new UsageError(
            "ast",
            `${JSON.stringify(node.op)} is no unary operator`,
          );
        }
        out += `{"kind":"unary","op":"${node.op}","operand":`;
        push({ node: node.operand, at: "operand" }, "}");
        break;
      case "binary":
        if (!BINARY_OPS.has(node.op)) {
          throw new UsageError(
            "ast",
            `${JSON.stringify(node.op)} is no binary operator`,
          );
        }
        out += `{"kind":"binary","left":`;
        push(
          { node: node.left, at: "operand" },
          `,"op":"${node.op}","right":`,
          { node: node.right, at: "operand" },
          "}",
        );
        break;
      case "call":
        out += `{"args":[`;
        pushList(
          arrayOf(node.args),
          "operand",
          `],"function":${required(node.function)},"kind":"call"}`,
        );
        break;
      case "assign":
        if (!ASSIGN_OPS.has(node.op)) {
          throw new UsageError(
            "ast",
            `${JSON.stringify(node.op)} is no assignment operator`,
          );
        }
        out += `{"kind":"assign","op":"${node.op}","target":`;
        push(
          { node: node.target, at: "target" },
          `,"value":`,
          { node: node.value, at: "operand" },
          "}",
        );
        break;
      case "script":
        out += `{"kind":"script","statements":[`;
        pushList(arrayOf(node.statements), "statement", "]}");
        break;
    }
  }
  return out;
}

// arrayOf returns a, an AST's list, or throws if it is none.
function arrayOf(a: unknown): readonly unknown[] {
  if (!Array.isArray(a)) {
    throw new UsageError(
      "ast",
      "a call's arguments or a script's statements are not a list",
    );
  }
  return a;
}

/**
 * Returns the JSON of a literal's value, of its type, or throws a UsageError
 * of the kind ast of one that is no value of the language: a type that no
 * literal is of, a value of another type, an int beyond ±(2^53 − 1), a float
 * that is not finite, or a string that holds a NUL or a lone surrogate, or
 * that is longer than 4,096 characters. encode and check both refuse a
 * literal by it, so that every AST that check accepts, encode writes.
 */
export function literalJSON(n: Lit): string {
  const problem = literalProblem(n);
  if (problem !== undefined) {
    throw new UsageError("ast", problem);
  }
  if (n.type === "float" && n.value === 0) {
    // -0 is 0 (§5.8.9).
    return "0";
  }
  return n.type === "string" ? quoted(n.value) : JSON.stringify(n.value);
}

/** Returns why the literal n is no value of the language, or undefined. */
export function literalProblem(n: Lit): string | undefined {
  const v: unknown = n.value;
  switch (n.type) {
    case "bool":
      return typeof v === "boolean"
        ? undefined
        : "a bool literal's value is not a boolean";
    case "int":
      return typeof v === "number" && Number.isSafeInteger(v)
        ? undefined
        : `the int ${String(v)} is not an integer within ±9007199254740991`;
    case "float":
      return typeof v === "number" && Number.isFinite(v)
        ? undefined
        : `the float ${String(v)} is not finite`;
    case "string":
      if (typeof v !== "string") {
        return "a string literal's value is not a string";
      }
      return (
        textProblem(v) ??
        (len(v) > MAX_STRING_LENGTH
          ? `a string literal is longer than ${MAX_STRING_LENGTH} characters`
          : undefined)
      );
  }
  return `${JSON.stringify((n as { type: unknown }).type)} is no type of a literal`;
}

/**
 * Returns why s is no text, a string that holds a NUL, which no text holds,
 * or a lone surrogate, which is no character and which UTF-8 cannot write,
 * or undefined.
 */
export function textProblem(s: string): string | undefined {
  if (s.includes("\0")) {
    return "a string holds a NUL";
  }
  // As !s.isWellFormed() does, which Safari has only since 16.4, so that it
  // runs on iPadOS 16, which PRD §9.9 supports.
  if (/\p{Surrogate}/u.test(s)) {
    return "a string holds a lone surrogate, which is not UTF-8";
  }
  return undefined;
}

/**
 * Returns the JSON of the string s as Go's encoding/json writes it, without
 * escaping <, >, and &: as JSON.stringify writes it, but U+2028 and U+2029
 * escaped. It throws a UsageError of the kind ast of what is no text.
 */
export function quoted(s: string): string {
  if (typeof s !== "string") {
    throw new UsageError(
      "ast",
      "a name, a node reference, or a function is not a string",
    );
  }
  const problem = textProblem(s);
  if (problem !== undefined) {
    throw new UsageError("ast", problem);
  }
  return JSON.stringify(s).replace(/[\u2028\u2029]/g, (c) =>
    c === "\u2028" ? "\\u2028" : "\\u2029",
  );
}

// required returns the JSON of s, a field that a node needs, or throws if it
// is empty.
function required(s: string): string {
  if (s === "") {
    throw new UsageError(
      "ast",
      "a name, a node reference, or a function is empty",
    );
  }
  return quoted(s);
}

// optional returns the field key of the string s, after a comma, or nothing
// when s is "", a field that is not set.
function optional(key: string, s: string): string {
  return s === "" || s === undefined ? "" : `,"${key}":${quoted(s)}`;
}
