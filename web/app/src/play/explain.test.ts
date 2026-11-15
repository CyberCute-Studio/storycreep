import {
  boolValue,
  compile,
  intValue,
  NONE,
  Project,
  stringValue,
  type Value,
} from "@storycreep/shared/expr/compile";
import { describe, expect, test } from "vitest";
import { failedParts } from "./explain";

// Why a hidden choice is hidden (PRD FR-PLAY-03, plan note N79): the parts
// of its condition that failed, each with the values that it reads.

const id = (prefix: string, body: string) =>
  `${prefix}_${body.padStart(26, "0")}`;
const board = id("bd", "1");
const a = { id: id("var", "1"), key: "a", type: "bool" as const };
const b = { id: id("var", "2"), key: "b", type: "int" as const };
const name = { id: id("var", "3"), key: "name", type: "string" as const };
const project = Project.of([a, b, name], [], []);

// explain returns the failed parts of source, a condition, of the values.
function explain(source: string, values: Record<string, Value>) {
  const tree = compile(source, "condition", project, board);
  return failedParts(tree, source, {
    variable: (v) => values[v] ?? NONE,
    attribute: () => NONE,
    visits: () => 0,
  });
}

describe("the failed parts of a condition", () => {
  // Verifies: FR-PLAY-03/AC1
  test("are the operand of && that failed", () => {
    const source = "a == true && b > 2";
    expect(
      explain(source, { [a.id]: boolValue(true), [b.id]: intValue(1) }),
    ).toEqual([{ source: "b > 2", reads: [{ name: "b", value: "1" }] }]);
    // The left operand fails first, and the evaluation stops at it, as the
    // runtime's does, whatever the right one is.
    expect(
      explain(source, { [a.id]: boolValue(false), [b.id]: intValue(1) }),
    ).toEqual([
      { source: "a == true", reads: [{ name: "a", value: "false" }] },
    ]);
  });

  test("are each operand of an || that failed, and the conjuncts within", () => {
    expect(
      explain('(a && b > 2) || name == "Elara"', {
        [a.id]: boolValue(true),
        [b.id]: intValue(0),
        [name.id]: stringValue("Ann"),
      }),
    ).toEqual([
      { source: "b > 2", reads: [{ name: "b", value: "0" }] },
      { source: 'name == "Elara"', reads: [{ name: "name", value: '"Ann"' }] },
    ]);
  });

  test("are a negation whole, and a value read once however often it is read", () => {
    expect(explain("!(b > 1 && b < 5)", { [b.id]: intValue(3) })).toEqual([
      { source: "!(b > 1 && b < 5)", reads: [{ name: "b", value: "3" }] },
    ]);
  });

  test("are of the source's code points, not its UTF-16 code units", () => {
    expect(
      explain('name == "😀é" && b > 2', {
        [name.id]: stringValue("😀é"),
        [b.id]: intValue(2),
      }),
    ).toEqual([{ source: "b > 2", reads: [{ name: "b", value: "2" }] }]);
    expect(
      explain('b > 2 || name == "😀é"', {
        [name.id]: stringValue(""),
        [b.id]: intValue(2),
      }),
    ).toEqual([
      { source: "b > 2", reads: [{ name: "b", value: "2" }] },
      { source: 'name == "😀é"', reads: [{ name: "name", value: '""' }] },
    ]);
  });

  test("are none of a condition that holds", () => {
    expect(explain("a", { [a.id]: boolValue(true) })).toEqual([]);
  });
});
