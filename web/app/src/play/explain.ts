// Why a choice is hidden (PRD FR-PLAY-03, plan note N79): the parts of its
// condition that made it false, as play mode's author view shows them, each
// with the values that it reads. A condition is pure (§5.8.5), so that its
// parts are evaluated again, apart, against the runtime as it was when the
// flow built the choices (the runtime's Observer), with no effect on the
// runtime, whose runtime errors the flow reported already.

import {
  type Env,
  evaluate,
  type Node,
  type Range,
  type Value,
  walk,
} from "@storycreep/shared/expr";
import { shownValue } from "./values";

/** What explaining a condition reads of the runtime: its values and its visit counts. */
export interface Reader {
  variable(id: string): Value;
  attribute(component: string, attribute: string): Value;
  visits(node: string): number;
}

/** A value that a failed part of a condition reads, as the author view names it. */
export interface PartRead {
  /** The variable or the attribute as the condition's source writes it, as global.gold or elara.trust. */
  readonly name: string;
  /** Its value, as shownValue writes it. */
  readonly value: string;
}

/** A part of a condition that failed: its source, and the values that it reads, each once, in the order of the source. */
export interface FailedPart {
  readonly source: string;
  readonly reads: readonly PartRead[];
}

/**
 * Returns the parts of the condition n, of source, that made it false as r
 * holds its values, each with what it reads: of a && b, those of the first
 * operand that is false, as the evaluation stops at it, so that of a ==
 * true && b > 2 it is the one operand that failed (AC1); of a || b, those of
 * each, as both are false; and of any other expression, the expression
 * itself, as a comparison, a call, or a negation. A condition that holds of
 * r, as none whose choice is hidden does, has none.
 */
export function failedParts(
  n: Node,
  source: string,
  r: Reader,
): readonly FailedPart[] {
  const env = readOnly(r);
  const points = Array.from(source);
  const text = (at: Range) => points.slice(at.start, at.end).join("");
  if (evaluate(n, env).bool) {
    return [];
  }
  const parts: FailedPart[] = [];
  const visit = (m: Node) => {
    if (m.kind === "binary" && m.op === "and") {
      visit(evaluate(m.left, env).bool ? m.right : m.left);
      return;
    }
    if (m.kind === "binary" && m.op === "or") {
      visit(m.left);
      visit(m.right);
      return;
    }
    const reads: PartRead[] = [];
    const seen = new Set<string>();
    walk(m, (x) => {
      if (x.kind !== "var" && x.kind !== "attr") {
        return;
      }
      const key = x.kind === "var" ? x.ref : `${x.ref}/${x.attribute}`;
      if (seen.has(key)) {
        return;
      }
      seen.add(key);
      reads.push({ name: text(x.src), value: shownValue(evaluate(x, env)) });
    });
    parts.push({ source: text(m.src), reads });
  };
  visit(n);
  return parts;
}

// readOnly returns an Env of what r reads, which changes nothing and
// reports nothing: a condition assigns nothing and emits no signal, and the
// flow reported its runtime errors as it evaluated it.
function readOnly(r: Reader): Env {
  return {
    variable: (id) => r.variable(id),
    attribute: (c, a) => r.attribute(c, a),
    visits: (node) => r.visits(node),
    setVariable: () => {},
    setAttribute: () => {},
    signal: () => {},
    error: () => {},
  };
}
