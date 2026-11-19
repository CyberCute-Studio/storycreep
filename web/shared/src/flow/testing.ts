// What the tests of the flow share, and the web app's tests of play mode's
// projection (plan item 71): they read the snapshots and the scenarios of
// the conformance suite from the disk, as Node does, which the browser's
// typecheck, of tsconfig.browser.json, leaves out with this file, and run
// the scenarios as the Go reference runtime's TestConformance does.

import { createHash } from "node:crypto";
import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { isDeepStrictEqual } from "node:util";
import {
  type DeclaredType,
  isJsonObject,
  JsonNumber,
  type JsonValue,
  plain,
  readJSON,
  readValue,
  sameValue,
  type Value,
} from "../expr";
import type { SaveState } from "../save-state";
import { attributeKey, type Snapshot } from "./load";
import { type Event, Runtime } from "./runtime";

/**
 * schema/conformance, whose README describes its suites and scenarios, which
 * the Go reference runtime runs too (TestConformance).
 */
export const conformance = join(
  import.meta.dirname,
  "../../../../schema/conformance/",
);

/**
 * Returns the files of the directory dir, by their paths under it, with
 * forward slashes.
 */
export function readTree(dir: string): Map<string, Uint8Array> {
  const files = new Map<string, Uint8Array>();
  const walk = (sub: string) => {
    for (const name of readdirSync(join(dir, sub))) {
      const path = sub === "" ? name : `${sub}/${name}`;
      if (statSync(join(dir, path)).isDirectory()) {
        walk(path);
      } else {
        files.set(path, new Uint8Array(readFileSync(join(dir, path))));
      }
    }
  };
  walk("");
  return files;
}

/**
 * Returns the fixture of the version n of the published format (PRD §6.4):
 * schema/fixtures/v<n> of a real version, one whose schemas schema/published
 * holds, and otherwise the synthetic version's of testdata/synthetic-schema
 * (plan note N2), as the Go tests' fixtureDir finds them.
 */
export function fixture(n: number): Map<string, Uint8Array> {
  const root = join(import.meta.dirname, "../../../..");
  return readTree(
    existsSync(join(root, `schema/published/v${n}`))
      ? join(root, `schema/fixtures/v${n}`)
      : join(root, `testdata/synthetic-schema/fixtures/v${n}`),
  );
}

/** A file's JSON, which a test edits as it likes. */
// biome-ignore lint/suspicious/noExplicitAny: a test edits any part of a file's JSON.
export type FileJSON = Record<string, any>;

/**
 * Returns files with each file at a path of edits changed as its edit changes
 * the file's JSON, and the manifest's hash of each file that they change, but
 * for the manifest, which its edit changes last, and which the loader reads
 * without a hash.
 */
export function edited(
  files: ReadonlyMap<string, Uint8Array>,
  edits: Readonly<Record<string, (f: FileJSON) => void>>,
): Map<string, Uint8Array> {
  const out = new Map(files);
  const json = (path: string) =>
    JSON.parse(new TextDecoder().decode(files.get(path))) as FileJSON;
  const m = json("storycreep.json");
  for (const [path, edit] of Object.entries(edits)) {
    if (path === "storycreep.json") {
      continue;
    }
    const f = json(path);
    edit(f);
    const bytes = new TextEncoder().encode(`${JSON.stringify(f, null, 2)}\n`);
    out.set(path, bytes);
    m.files[path] =
      `sha256:${createHash("sha256").update(bytes).digest("hex")}`;
  }
  edits["storycreep.json"]?.(m);
  out.set("storycreep.json", new TextEncoder().encode(JSON.stringify(m)));
  return out;
}

/** Returns files with their manifest's keys set as edit sets them. */
export function withManifest(
  files: ReadonlyMap<string, Uint8Array>,
  edit: Record<string, unknown>,
): Map<string, Uint8Array> {
  return edited(files, { "storycreep.json": (m) => Object.assign(m, edit) });
}

/** A scenario, or another object of a suite's files, read with each number as it is written. */
export type Raw = { [key: string]: JsonValue };

/**
 * Returns the scenarios of the suite of the directory dir, which holds its
 * snapshot/ and its scenarios.json, read with each number as it is written.
 */
export function readScenarios(dir: string): Raw[] {
  const file = readJSON(readFileSync(join(dir, "scenarios.json"), "utf8"));
  if (!isJsonObject(file) || !Array.isArray(file.scenarios)) {
    throw new Error(`${dir}: scenarios.json has no scenarios`);
  }
  return file.scenarios.filter(isJsonObject);
}

/**
 * Runs each of scenarios, of the suite name, on a new runtime of snapshot,
 * and returns what went otherwise than it says, [] for nothing: each step's
 * call emits the events of the step, in order, and leaves the view of the
 * step, and the state, where the step has one, as the Go reference runtime
 * does (FR-PLAY-06 AC1), and no two scenarios have one note.
 */
export function runScenarios(
  name: string,
  snapshot: Snapshot,
  scenarios: readonly Raw[],
): string[] {
  const failures: string[] = [];
  const notes = new Set<string>();
  for (const sc of scenarios) {
    const note = String(sc.note);
    if (notes.has(note)) {
      failures.push(`${name}: the note ${note} is another's`);
    }
    notes.add(note);
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
  return failures;
}

// typed returns the value of a variable or an attribute, as a scenario
// writes one, as its declaration types it, or throws.
export function typed(
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
      return r.loadState(textOf(input.state as JsonValue));
  }
  throw new Error(`${String(input.call)} is no call`);
}

// textOf returns the JSON text of v, each number as it is written, which
// loadState reads by the declarations, as the Go runner reads a state,
// rather than as the double that plain would round it to.
function textOf(v: JsonValue): string {
  if (v instanceof JsonNumber) {
    return v.raw;
  }
  if (Array.isArray(v)) {
    return `[${v.map(textOf).join(",")}]`;
  }
  if (isJsonObject(v)) {
    const members = Object.keys(v).map(
      (k) => `${JSON.stringify(k)}:${textOf(v[k])}`,
    );
    return `{${members.join(",")}}`;
  }
  return JSON.stringify(v);
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
