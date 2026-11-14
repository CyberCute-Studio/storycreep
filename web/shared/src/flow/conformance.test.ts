import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { beforeAll, describe, expect, test } from "vitest";
import {
  builtins,
  type DeclaredType,
  isJsonObject,
  type JsonValue,
  type Node,
  plain,
  readJSON,
  walk,
} from "../expr";
import { load, type Snapshot } from "./load";
import { interpolate } from "./runtime";
import {
  conformance,
  type Raw,
  readScenarios,
  readTree,
  runScenarios,
  typed,
} from "./testing";

// The suites, each its snapshot, loaded, and its scenarios, read with each
// number as it is written.
const suites = new Map<string, { snapshot: Snapshot; scenarios: Raw[] }>();

beforeAll(async () => {
  for (const name of readdirSync(conformance).sort()) {
    if (!statSync(join(conformance, name)).isDirectory()) {
      continue;
    }
    const snapshot = await load(readTree(join(conformance, name, "snapshot")));
    suites.set(name, {
      snapshot,
      scenarios: readScenarios(join(conformance, name)),
    });
  }
});

describe("the conformance suite", () => {
  // Each scenario of each suite runs on a new runtime of the suite's
  // snapshot, and each step's call emits the events of the step, in order,
  // and leaves the view of the step, and the state, where the step has one,
  // as the Go reference runtime does (FR-PLAY-06 AC1), so that play mode's
  // engine runs the published suite (AC3) with the same semantics.
  // Verifies: FR-PLAY-06/AC1, FR-PLAY-06/AC3
  test("runs every scenario as the reference runtime does", () => {
    const failures: string[] = [];
    let n = 0;
    for (const [name, { snapshot, scenarios }] of suites) {
      failures.push(...runScenarios(name, snapshot, scenarios));
      n += scenarios.length;
    }
    expect(failures).toEqual([]);
    expect(n).toBeGreaterThanOrEqual(30);
  });

  // Every built-in is called by an expression of a snapshot of the suite, so
  // that each has conformance cases (FR-EXPR-04 AC1), beside its cases in
  // the expression corpus: by a call of an AST that load decoded, which a
  // runtime evaluates, however its file is formatted.
  test("calls every built-in", () => {
    const called = new Set<string>();
    const calls = (n: Node | undefined) => {
      if (n !== undefined) {
        walk(n, (c) => {
          if (c.kind === "call") {
            called.add(c.function);
          }
        });
      }
    };
    for (const { snapshot } of suites.values()) {
      for (const n of snapshot.nodes.values()) {
        if (n.type !== "jumper") {
          calls(n.onEnter);
          calls(n.onExit);
        }
        if (n.type === "branch") {
          for (const o of n.outputs) {
            calls(o.condition);
          }
        }
        for (const connections of n.ports.values()) {
          for (const c of connections) {
            calls(c.condition);
            calls(c.onTraverse);
          }
        }
      }
    }
    expect(suites.size).toBeGreaterThan(0);
    expect(builtins().filter((b) => !called.has(b))).toEqual([]);
  });
});

// Each interpolate case of the corpus's formatting.json shows its value as a
// text value reference shows it, with or without a format specifier, as the
// Go runtime's Interpolate does (TestInterpolate).
test("interpolate shows each value of the formatting corpus as it says", () => {
  const file = readJSON(
    readFileSync(
      new URL(
        "../../../../schema/expr-corpus/formatting.json",
        import.meta.url,
      ),
      "utf8",
    ),
  );
  const cases =
    isJsonObject(file) && Array.isArray(file.cases)
      ? file.cases.filter(isJsonObject)
      : [];
  let n = 0;
  for (const c of cases) {
    if (c.op !== "interpolate") {
      continue;
    }
    const arg = (c.args as JsonValue[])[0] as Raw;
    const v = typed(arg.value, arg.type as DeclaredType);
    const result = c.result as Raw;
    const decimals =
      c.decimals === undefined ? undefined : (plain(c.decimals) as number);
    expect(interpolate(v, decimals), String(c.note)).toBe(result.value);
    n++;
  }
  expect(n).toBeGreaterThan(0);
});
