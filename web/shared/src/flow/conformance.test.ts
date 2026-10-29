import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { isDeepStrictEqual } from "node:util";
import { beforeAll, describe, expect, test } from "vitest";
import {
  builtins,
  type DeclaredType,
  isJsonObject,
  type JsonValue,
  plain,
  readJSON,
  readValue,
  sameValue,
  type Value,
} from "../expr";
import type { SaveState } from "../save-state";
import { attributeKey, load, type Snapshot } from "./load";
import { type Event, interpolate, Runtime } from "./runtime";
import { conformance, readTree } from "./testing";

type Raw = { [key: string]: JsonValue };

// The suites, each its snapshot, loaded, and its scenarios, read with each
// number as it is written.
const suites = new Map<string, { snapshot: Snapshot; scenarios: Raw[] }>();

beforeAll(async () => {
  for (const name of readdirSync(conformance).sort()) {
    if (!statSync(join(conformance, name)).isDirectory()) {
      continue;
    }
    const snapshot = await load(readTree(join(conformance, name, "snapshot")));
    const file = readJSON(
      readFileSync(join(conformance, name, "scenarios.json"), "utf8"),
    );
    if (!isJsonObject(file) || !Array.isArray(file.scenarios)) {
      throw new Error(`${name}: scenarios.json has no scenarios`);
    }
    suites.set(name, {
      snapshot,
      scenarios: file.scenarios.filter(isJsonObject),
    });
  }
});

// typed returns the value of a variable or an attribute, as a scenario
// writes one, as its declaration types it, or throws.
function typed(
  v: JsonValue | number | undefined,
  typ: DeclaredType,
  options: readonly string[] = [],
): Value {
  const [read, problem] = readValue(v, typ, options);
  if (problem !== undefined) {
    throw new Error(`a value ${problem}`);
  }
  return read;
}

// call makes the input's call of r, its values typed by their declarations
// in s, and returns the events that it emits, none of a set_variable or a
// set_attribute, which moves no flow.
function call(r: Runtime, s: Snapshot, input: Raw): Event[] {
  const str = (k: string) =>
    typeof input[k] === "string" ? (input[k] as string) : "";
  switch (input.call) {
    case "start":
      return r.start(str("board"), str("node"));
    case "goto":
      return r.goto(str("board"), str("node"));
    case "reset":
      return r.reset();
    case "choose":
      return r.choose(str("choice"));
    case "advance":
      return r.advance();
    case "set_variable": {
      const d = s.declared.get(str("variable"));
      if (d === undefined) {
        throw new Error(
          `set_variable of ${str("variable")}, which is no variable`,
        );
      }
      r.setVariable(d.id, typed(input.value, d.type));
      return [];
    }
    case "set_attribute": {
      const k = attributeKey(str("component"), str("attribute"));
      const typ = s.declaredTypes.get(k);
      if (typ === undefined) {
        throw new Error(`set_attribute of ${k}, which is no runtime attribute`);
      }
      r.setAttribute(
        str("component"),
        str("attribute"),
        typed(input.value, typ, s.options.get(k)),
      );
      return [];
    }
    case "load_state":
      return r.loadState(plain(input.state as JsonValue) as SaveState);
  }
  throw new Error(`${String(input.call)} is no call`);
}

// eventOf returns e as a scenario writes it, but a signal's value, which it
// leaves as it is, to be compared by its type.
function eventOf(e: Event): { [key: string]: unknown } {
  switch (e.kind) {
    case "enter":
    case "ready":
    case "exit":
      return { event: e.kind, node: e.node };
    case "end":
      return { event: e.kind, reason: e.reason };
    case "signal":
      return {
        event: e.kind,
        name: e.name,
        ...(e.value === undefined ? {} : { value: e.value }),
      };
    case "error":
      return {
        event: e.kind,
        code: e.code,
        ...(e.at === "" ? {} : { at: e.at }),
        ...(e.expression === "" ? {} : { expression: e.expression }),
      };
  }
}

// sameEvents reports whether got are want: the same kinds in the same order,
// each with the same fields, a signal's value compared by its type and the
// value that the type reads, so that 2 and 2.0 are one float.
function sameEvents(got: Event[], want: JsonValue[]): boolean {
  if (got.length !== want.length) {
    return false;
  }
  return got.every((g, i) => {
    const w = want[i];
    if (!isJsonObject(w)) {
      return false;
    }
    const { value: gv, ...gRest } = eventOf(g) as { value?: Value };
    const { value: wv, ...wRest } = plain(w) as { value?: unknown };
    if (!isDeepStrictEqual(gRest, wRest)) {
      return false;
    }
    if ((gv === undefined) !== (wv === undefined)) {
      return false;
    }
    if (gv === undefined) {
      return true;
    }
    const raw = w.value;
    if (!isJsonObject(raw) || raw.type !== gv.type) {
      return false;
    }
    return sameValue(gv, typed(raw.value, gv.type as DeclaredType));
  });
}

// viewOf returns the view of r's flow, as a scenario writes it.
function viewOf(r: Runtime): unknown {
  const { node, phase } = r.current();
  return {
    canAdvance: r.canAdvance(),
    choices: r.choices().map((c) => ({ id: c.id, text: c.text })),
    halted: r.halted(),
    ...(node === "" ? {} : { node }),
    ...(phase === "" ? {} : { phase }),
    text: r.text(),
  };
}

// sameState returns why got, a state of s, is not want, or undefined: each
// key, and each value compared as its declaration types it.
function sameState(s: Snapshot, got: SaveState, want: Raw): string | undefined {
  const w = plain(want) as SaveState;
  const plainOf = (st: SaveState) => ({
    cursor: st.cursor,
    projectId: st.projectId,
    schemaVersion: st.schemaVersion,
    snapshotId: st.snapshotId,
    snapshotVersion: st.snapshotVersion,
    stateVersion: st.stateVersion,
    visits: st.visits,
  });
  if (
    !isDeepStrictEqual(
      JSON.parse(JSON.stringify(plainOf(got))),
      JSON.parse(JSON.stringify(plainOf(w))),
    )
  ) {
    return `${JSON.stringify(plainOf(got))}; want ${JSON.stringify(plainOf(w))}`;
  }
  const keys = (o: object) => Object.keys(o).sort().join(" ");
  if (keys(got.variables) !== keys(w.variables)) {
    return "the variables differ";
  }
  const wantVariables = want.variables as Raw;
  for (const id of Object.keys(w.variables)) {
    const typ = s.declared.get(id)?.type ?? "string";
    if (
      !sameValue(typed(got.variables[id], typ), typed(wantVariables[id], typ))
    ) {
      return `the variable ${id} is ${JSON.stringify(got.variables[id])}`;
    }
  }
  if (keys(got.attributes) !== keys(w.attributes)) {
    return "the attributes' components differ";
  }
  const wantAttributes = want.attributes as { [key: string]: Raw };
  for (const c of Object.keys(w.attributes)) {
    if (keys(got.attributes[c]) !== keys(w.attributes[c])) {
      return `the attributes of ${c} differ`;
    }
    for (const a of Object.keys(w.attributes[c])) {
      const k = attributeKey(c, a);
      const typ = s.declaredTypes.get(k) ?? "string";
      const options = s.options.get(k) ?? [];
      if (
        !sameValue(
          typed(got.attributes[c][a], typ, options),
          typed(wantAttributes[c][a], typ, options),
        )
      ) {
        return `${a} of ${c} is ${JSON.stringify(got.attributes[c][a])}`;
      }
    }
  }
  return undefined;
}

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
      const notes = new Set<string>();
      for (const sc of scenarios) {
        const note = String(sc.note);
        expect(notes.has(note), `${name}: the note ${note} is another's`).toBe(
          false,
        );
        notes.add(note);
        n++;
        const r = new Runtime(snapshot, sc.strict === true);
        const steps = (sc.steps as JsonValue[]).filter(isJsonObject);
        for (let i = 0; i < steps.length; i++) {
          const st = steps[i];
          const input = st.input as Raw;
          const where = `${name}: ${note}: step ${i + 1} (${String(input.call)})`;
          let events: Event[];
          try {
            events = call(r, snapshot, input);
          } catch (e) {
            failures.push(`${where}: ${(e as Error).message}`);
            break;
          }
          if (!sameEvents(events, st.events as JsonValue[])) {
            failures.push(
              `${where}: events ${JSON.stringify(events.map(eventOf))}; want ${JSON.stringify(plain(st.events as JsonValue))}`,
            );
          }
          const view = viewOf(r);
          if (!isDeepStrictEqual(view, plain(st.view as JsonValue))) {
            failures.push(
              `${where}: view ${JSON.stringify(view)}; want ${JSON.stringify(plain(st.view as JsonValue))}`,
            );
          }
          if (st.state !== undefined) {
            const problem = sameState(snapshot, r.saveState(), st.state as Raw);
            if (problem !== undefined) {
              failures.push(`${where}: state: ${problem}`);
            }
          }
        }
      }
    }
    expect(failures).toEqual([]);
    expect(n).toBeGreaterThanOrEqual(30);
  });

  // Every built-in is called by an expression of a snapshot of the suite, so
  // that each has conformance cases (FR-EXPR-04 AC1), beside its cases in
  // the expression corpus.
  test("calls every built-in", () => {
    const called = new Set<string>();
    for (const name of readdirSync(conformance)) {
      const boards = join(conformance, name, "snapshot", "boards");
      if (!statSync(join(conformance, name)).isDirectory()) {
        continue;
      }
      for (const file of readdirSync(boards)) {
        const text = readFileSync(join(boards, file), "utf8");
        for (const m of text.matchAll(/"function": "([a-z_]+)"/g)) {
          called.add(m[1]);
        }
      }
    }
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
