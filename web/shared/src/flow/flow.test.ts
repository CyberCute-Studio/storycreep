import { createHash } from "node:crypto";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, test } from "vitest";
import ast from "../../../../schema/published/v1/ast.schema.json" with {
  type: "json",
};
import board from "../../../../schema/published/v1/board.schema.json" with {
  type: "json",
};
import componentTypes from "../../../../schema/published/v1/component_types.schema.json" with {
  type: "json",
};
import components from "../../../../schema/published/v1/components.schema.json" with {
  type: "json",
};
import settings from "../../../../schema/published/v1/settings.schema.json" with {
  type: "json",
};
import manifest from "../../../../schema/published/v1/storycreep.schema.json" with {
  type: "json",
};
import variables from "../../../../schema/published/v1/variables.schema.json" with {
  type: "json",
};
import saveState from "../../../../schema/save-state/v1/save-state.schema.json" with {
  type: "json",
};
import {
  boolValue,
  floatValue,
  intValue,
  readJSON,
  stringValue,
  type Value,
} from "../expr";
import type { SaveState } from "../save-state";
import { ASTUnsupportedError, foldCase, LoadError, load } from "./load";
import { Runtime, StateError, ValueError } from "./runtime";
import { compareNumbers, type Schema, validate } from "./schema";

const conformance = fileURLToPath(
  new URL("../../../../schema/conformance/", import.meta.url),
);

// readTree returns the files of the directory dir, by their paths under it,
// with forward slashes.
function readTree(dir: string): Map<string, Uint8Array> {
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

// The IDs of the suites' entities, by their mnemonics, as the Go suites name
// them: the body is the mnemonic after zeros.
const id = (prefix: string, m: string) => `${prefix}_${m.padStart(26, "0")}`;
const nd = (m: string) => id("nd", m);
const cn = (m: string) => id("cn", m);
const vr = (m: string) => id("var", m);
const cm = (m: string) => id("cmp", m);
const at = (m: string) => id("atr", m);
const bo = (m: string) => id("bo", m);

// A file's JSON, which a test edits as it likes.
// biome-ignore lint/suspicious/noExplicitAny: a test edits any part of a file's JSON.
type Obj = Record<string, any>;

// suite returns the files of a suite's snapshot, each edited by edits, which
// change a file's JSON, and the manifest's hash of each file that they
// change, but for the manifest, which its edit changes last.
function suite(
  name: string,
  edits: Record<string, (f: Obj) => void> = {},
): Map<string, Uint8Array> {
  const files = readTree(join(conformance, name, "snapshot"));
  const m = JSON.parse(
    new TextDecoder().decode(files.get("storycreep.json")),
  ) as Obj;
  for (const [path, edit] of Object.entries(edits)) {
    if (path === "storycreep.json") {
      continue;
    }
    const f = JSON.parse(new TextDecoder().decode(files.get(path))) as Obj;
    edit(f);
    const bytes = new TextEncoder().encode(`${JSON.stringify(f, null, 2)}\n`);
    files.set(path, bytes);
    m.files[path] =
      `sha256:${createHash("sha256").update(bytes).digest("hex")}`;
  }
  edits["storycreep.json"]?.(m);
  files.set("storycreep.json", new TextEncoder().encode(JSON.stringify(m)));
  return files;
}

const nodeOf = (b: Obj, nid: string): Obj =>
  b.nodes.find((n: Obj) => n.id === nid);
const connectionOf = (b: Obj, cid: string): Obj =>
  b.connections.find((c: Obj) => c.id === cid);
const main = "boards/main.json";
const roll = "boards/roll.json";
const harbour = "boards/harbour.json";
const zeros = `sha256:${"0".repeat(64)}`;

describe("load", () => {
  test("loads every suite's snapshot", async () => {
    for (const name of readdirSync(conformance)) {
      if (!name.includes(".")) {
        await expect(load(suite(name))).resolves.toBeDefined();
      }
    }
  });

  test("ignores a key that the format does not define", async () => {
    await expect(
      load(
        suite("choices", {
          [main]: (b) => {
            nodeOf(b, nd("START")).later = { of: [1, "a", true, { deep: [] }] };
          },
        }),
      ),
    ).resolves.toBeDefined();
  });

  // A snapshot of a newer astVersion than the runtime's is refused as
  // ast_unsupported, which a newer runtime may run, before anything else is
  // read (FR-EXPR-04 AC4, the TypeScript runtime's half).
  test("refuses a newer astVersion as ast_unsupported", async () => {
    const files = suite("choices", {
      "storycreep.json": (m) => {
        m.astVersion = 2;
      },
    });
    await expect(load(files)).rejects.toBeInstanceOf(ASTUnsupportedError);
    await expect(load(files)).rejects.toThrow(/ast_unsupported/);
  });

  test.each<[string, string, Record<string, (f: Obj) => void>, RegExp]>([
    [
      "a file's hash",
      "choices",
      {
        "storycreep.json": (m) => {
          m.files[main] = zeros;
        },
      },
      /its hash/,
    ],
    [
      "a file that the manifest does not list",
      "choices",
      {
        "storycreep.json": (m) => {
          delete m.files[main];
        },
      },
      /not among the manifest's files/,
    ],
    [
      "a path of no file of a snapshot",
      "choices",
      {
        "storycreep.json": (m) => {
          m.files["notes.txt"] = zeros;
        },
      },
      /storycreep\.json/,
    ],
    [
      "a board's path of a name that Windows reserves",
      "choices",
      {
        "storycreep.json": (m) => {
          m.files["boards/con.json"] = zeros;
        },
      },
      /none of the paths/,
    ],
    [
      "an understated astVersion",
      "choices",
      {
        "storycreep.json": (m) => {
          m.astVersion = 0;
        },
      },
      /storycreep\.json/,
    ],
    [
      "the manifest's counts",
      "choices",
      {
        "storycreep.json": (m) => {
          m.counts.nodes = 99;
        },
      },
      /counts/,
    ],
    [
      "an index's board of another name than its file's",
      "choices",
      {
        "storycreep.json": (m) => {
          m.boards[0].name = "Other";
        },
      },
      /not the index's/,
    ],
    [
      "an index's board whose file is not of its slug",
      "choices",
      {
        "storycreep.json": (m) => {
          m.boards[0].slug = "other";
        },
      },
      /not boards\/ and its slug/,
    ],
    [
      "a signal that is there twice",
      "choices",
      {
        "project/settings.json": (f) => {
          f.signals = ["asking", "asking"];
        },
      },
      /signal is there twice/,
    ],
    [
      "two definitions of one key",
      "choices",
      {
        "project/component_types.json": (f) => {
          f.componentTypes[0].attributes[1].key = "trust";
        },
      },
      /have the key trust/,
    ],
    [
      "an enum's default of no option",
      "choices",
      {
        "project/component_types.json": (f) => {
          f.componentTypes[0].attributes[1].default = "sad";
        },
      },
      /the default of the definition/,
    ],
    [
      "a connection that is there twice",
      "choices",
      {
        [main]: (b) => {
          b.connections.push(connectionOf(b, cn("PAY")));
        },
      },
      /two of its entities/,
    ],
    [
      "a speaker of no component",
      "choices",
      {
        [main]: (b) => {
          nodeOf(b, nd("START")).speaker = cm("NADA");
        },
      },
      /no component of the snapshot/,
    ],
    [
      "two tags that are the same but for their case",
      "choices",
      {
        [main]: (b) => {
          nodeOf(b, nd("START")).tags = ["Quest", "quest"];
        },
      },
      /the same but for their case/,
    ],
    [
      "a segment's marks out of order",
      "choices",
      {
        [main]: (b) => {
          nodeOf(b, nd("CHAT")).text.segments[0].marks = ["italic", "bold"];
        },
      },
      /alphabetical order/,
    ],
    [
      "a segment's link without an href",
      "choices",
      {
        [main]: (b) => {
          nodeOf(b, nd("CHAT")).text.segments[0].marks = ["link"];
        },
      },
      /the link mark/,
    ],
    [
      "a text's value of another board's variable",
      "jumpers",
      {
        [harbour]: (b) => {
          nodeOf(b, nd("HENTRY")).text.segments[1].ref = vr("BTMP");
        },
      },
      /board_variable_out_of_scope/,
    ],
    [
      "a string default of more than 4,096 characters",
      "choices",
      {
        "project/variables.json": (f) => {
          f.variables[2].default = "a".repeat(4097);
        },
      },
      /longer than 4096 characters/,
    ],
    [
      "a label's value of an attribute that is not runtime",
      "choices",
      {
        [main]: (b) => {
          connectionOf(b, cn("ASK")).label.segments[1] = {
            kind: "value",
            marks: [],
            name: "elara.title",
            ref: cm("KEEPER"),
            attribute: at("TAG"),
          };
        },
      },
      /a value is of/,
    ],
    [
      "a file of another schemaVersion",
      "choices",
      {
        [main]: (b) => {
          b.schemaVersion = 2;
        },
      },
      /schemaVersion/,
    ],
    [
      "a board's file of another board",
      "choices",
      {
        [main]: (b) => {
          b.board.entryNodeId = nd("MARKET");
        },
      },
      /not the index's/,
    ],
    [
      "the settings' entry",
      "choices",
      {
        "project/settings.json": (f) => {
          f.entry.nodeId = nd("BYE");
        },
      },
      /not the settings'/,
    ],
    [
      "a connection to a node of no board",
      "choices",
      {
        [main]: (b) => {
          connectionOf(b, cn("PAY")).to = nd("NADA");
        },
      },
      /does not join two nodes/,
    ],
    [
      "a connection of no port",
      "choices",
      {
        [main]: (b) => {
          connectionOf(b, cn("PAY")).fromPort = bo("NADA");
        },
      },
      /no port of it/,
    ],
    [
      "a port's connections out of order",
      "choices",
      {
        [main]: (b) => {
          connectionOf(b, cn("PAY")).order = 1;
        },
      },
      /not ordered/,
    ],
    [
      "a branch's outputs out of order",
      "branches",
      {
        [roll]: (b) => {
          nodeOf(b, nd("GATE")).outputs[0].order = 1;
        },
      },
      /outputs are not ordered/,
    ],
    [
      "a branch with a second if",
      "branches",
      {
        [roll]: (b) => {
          const o = nodeOf(b, nd("GATE")).outputs[1];
          o.kind = "if";
          o.condition ??= {
            source: "true",
            ast: { kind: "lit", type: "bool", value: true },
          };
        },
      },
      /not one if/,
    ],
    [
      "a release's jumper without a target",
      "jumpers",
      {
        "storycreep.json": (m) => {
          m.snapshot = {
            channel: "release",
            contentHash: zeros,
            id: id("snp", "C3"),
            note: "",
            publishedAt: "2026-10-25T00:00:00Z",
            version: "1.0.0",
          };
        },
      },
      /only a draft's may lack/,
    ],
    [
      "a jumper with text, which only an element has",
      "jumpers",
      {
        [harbour]: (b) => {
          nodeOf(b, nd("JSD")).text = { preview: "x", segments: [] };
        },
      },
      /boards\/harbour\.json/,
    ],
    [
      "an AST that names no variable of the snapshot",
      "choices",
      {
        [main]: (b) => {
          for (const c of b.connections) {
            if (c.condition !== undefined) {
              c.condition.ast = { kind: "var", name: "met", ref: vr("NADA") };
              return;
            }
          }
          throw new Error("no condition");
        },
      },
      /unknown_identifier/,
    ],
    [
      "an int literal that is not integral",
      "choices",
      {
        [main]: (b) => {
          for (const c of b.connections) {
            if (c.condition !== undefined) {
              c.condition.ast = {
                kind: "binary",
                op: "eq",
                left: { kind: "lit", type: "int", value: 2.5 },
                right: { kind: "lit", type: "int", value: 2 },
              };
              return;
            }
          }
        },
      },
      /boards\/main\.json/,
    ],
  ])("refuses %s", async (_, name, edits, problem) => {
    const files = suite(name, edits);
    await expect(load(files)).rejects.toBeInstanceOf(LoadError);
    await expect(load(files)).rejects.toThrow(problem);
  });

  test.each<[string, (b: Uint8Array) => Uint8Array, RegExp]>([
    [
      "null anywhere",
      (b) => replace(b, '"isEnding": false', '"isEnding": null'),
      /holds null/,
    ],
    [
      "a number with an exponent beyond ±999",
      (b) => replace(b, '"order": 0', '"order": 0e1000'),
      /exponent/,
    ],
    [
      "bytes that are not UTF-8",
      (b) => new Uint8Array([...b.slice(0, 10), 0xff, ...b.slice(10)]),
      /not UTF-8/,
    ],
    [
      "a byte order mark",
      (b) => new Uint8Array([0xef, 0xbb, 0xbf, ...b]),
      /not one JSON value/,
    ],
    [
      "an escape of a lone surrogate",
      (b) => replace(b, '"Hello, "', '"Hello, \\ud800"'),
      /lone surrogate/,
    ],
  ])("refuses a file that holds %s", async (_, edit, problem) => {
    const files = suite("choices");
    const bytes = edit(files.get(main) as Uint8Array);
    files.set(main, bytes);
    const m = JSON.parse(
      new TextDecoder().decode(files.get("storycreep.json")),
    );
    m.files[main] =
      `sha256:${createHash("sha256").update(bytes).digest("hex")}`;
    files.set("storycreep.json", new TextEncoder().encode(JSON.stringify(m)));
    await expect(load(files)).rejects.toThrow(problem);
  });
});

// replace returns the bytes with the first occurrence of from replaced.
function replace(bytes: Uint8Array, from: string, to: string): Uint8Array {
  const text = new TextDecoder().decode(bytes);
  if (!text.includes(from)) {
    throw new Error(`no ${from}`);
  }
  return new TextEncoder().encode(text.replace(from, to));
}

describe("the state", () => {
  test("is what loadState loads again, through JSON too", async () => {
    const s = await load(suite("choices"));
    const r = new Runtime(s, false);
    r.start("", "");
    r.choose(cn("ASK"));
    const st = r.saveState();
    const again = new Runtime(s, false);
    again.loadState(JSON.parse(JSON.stringify(st)) as SaveState);
    expect(again.saveState()).toEqual(st);
    expect(again.current()).toEqual(r.current());
    expect(again.choices()).toEqual(r.choices());
  });

  test.each<[string, (st: Obj) => void, RegExp]>([
    [
      "of another project",
      (st) => {
        st.projectId = id("prj", "C9");
      },
      /another snapshot/,
    ],
    [
      "of another stateVersion",
      (st) => {
        st.stateVersion = 2;
      },
      /stateVersion/,
    ],
    [
      "without a variable",
      (st) => {
        delete st.variables[vr("MET")];
      },
      /variables are not/,
    ],
    [
      "of a variable of another type",
      (st) => {
        st.variables[vr("CASH")] = "1";
      },
      /variable/,
    ],
    [
      "of an int that is not integral",
      (st) => {
        st.variables[vr("CASH")] = 1.5;
      },
      /not an integer/,
    ],
    [
      "of an enum's value of no option",
      (st) => {
        st.attributes[cm("KEEPER")][at("MD")] = "sad";
      },
      /options/,
    ],
    [
      "without an attribute",
      (st) => {
        delete st.attributes[cm("KEEPER")][at("MD")];
      },
      /lacks a runtime attribute/,
    ],
    [
      "of a visit count below 1",
      (st) => {
        st.visits[nd("START")] = 0;
      },
      /visits/,
    ],
    [
      "of a visit count of no node",
      (st) => {
        st.visits[nd("NADA")] = 1;
      },
      /visit count/,
    ],
    [
      "of a cursor of another board",
      (st) => {
        st.cursor.boardId = id("bd", "NADA");
      },
      /cursor/,
    ],
    [
      "of a cursor of no phase",
      (st) => {
        st.cursor.phase = "entering";
      },
      /phase/,
    ],
    [
      "of a cursor that awaits a choice at an ending",
      (st) => {
        st.cursor.nodeId = nd("BYE");
      },
      /ending or a dead end/,
    ],
  ])("refuses a state %s", async (_, edit, problem) => {
    const s = await load(suite("choices"));
    const r = new Runtime(s, false);
    r.start("", "");
    const st = JSON.parse(JSON.stringify(r.saveState())) as Obj;
    edit(st);
    expect(() => r.loadState(st as SaveState)).toThrow(StateError);
    expect(() => r.loadState(st as SaveState)).toThrow(problem);
    // It changes nothing.
    expect(r.current().node).toBe(nd("START"));
  });
});

describe("setVariable and setAttribute", () => {
  test.each<[string, string, Value]>([
    ["a value of another type", vr("CASH"), stringValue("1")],
    [
      "a string of more than 4,096 characters",
      vr("NAME"),
      stringValue("a".repeat(4097)),
    ],
    ["a string with a NUL", vr("NAME"), stringValue("a\0b")],
    ["a string with a lone surrogate", vr("NAME"), stringValue("a\uD800")],
    ["an int beyond ±(2^53 − 1)", vr("CASH"), intValue(2 ** 53)],
    ["no variable", vr("NADA"), boolValue(true)],
  ])("refuse %s", async (_, variable, v) => {
    const r = new Runtime(await load(suite("choices")), false);
    expect(() => r.setVariable(variable, v)).toThrow(ValueError);
  });

  test("widen an int for a float, and refuse an enum's value of no option", async () => {
    const s = await load(suite("values"));
    const r = new Runtime(s, false);
    const float = [...s.declared.values()].find((d) => d.type === "float");
    if (float === undefined) {
      throw new Error("no float variable");
    }
    r.setVariable(float.id, intValue(2));
    expect(r.variable(float.id)).toEqual(floatValue(2));
    const choices = new Runtime(await load(suite("choices")), false);
    expect(() =>
      choices.setAttribute(cm("KEEPER"), at("MD"), stringValue("sad")),
    ).toThrow(ValueError);
    choices.setAttribute(cm("KEEPER"), at("MD"), stringValue("wary"));
    expect(choices.attribute(cm("KEEPER"), at("MD"))).toEqual(
      stringValue("wary"),
    );
  });
});

describe("the schema validator", () => {
  const documents: [string, Schema][] = [
    ["ast", ast as Schema],
    ["board", board as Schema],
    ["component_types", componentTypes as Schema],
    ["components", components as Schema],
    ["settings", settings as Schema],
    ["storycreep", manifest as Schema],
    ["variables", variables as Schema],
    ["save-state", saveState as Schema],
  ];

  // Each document's examples, read as JSON again with each number as it is
  // written, are what it takes.
  test.each(documents)("takes the examples of %s", (_, document) => {
    const examples = (document as { examples: unknown[] }).examples;
    expect(examples.length).toBeGreaterThan(0);
    for (const e of examples) {
      expect(validate(document, readJSON(JSON.stringify(e)))).toBeUndefined();
      expect(validate(document, e)).toBeUndefined();
    }
  });

  // validate reads a value of a key that the format does not define as any
  // value, which readJSON never reads as null, without descending into it,
  // which holds only while each document defines AnyValue so.
  test.each(documents)(
    "reads %s's AnyValue as any value but null",
    (_, document) => {
      expect(
        (document as { $defs: Record<string, unknown> }).$defs.AnyValue,
      ).toEqual({
        description: expect.any(String),
        oneOf: [
          { type: "boolean" },
          { type: "number" },
          { type: "string" },
          { items: { $ref: "#/$defs/AnyValue" }, type: "array" },
          {
            additionalProperties: { $ref: "#/$defs/AnyValue" },
            type: "object",
          },
        ],
      });
    },
  );

  test("refuses what the schemas refuse", () => {
    const v = (text: string) => validate(variables as Schema, readJSON(text));
    const ok =
      '{"schemaVersion":1,"variables":[{"id":"var_01HQ8ZV01K7MWVND2CQJ4RT9XB","key":"x","scope":"global","type":"int","default":2.0}]}';
    expect(v(ok)).toBeUndefined();
    expect(v(ok.replace("2.0", "2.5"))).toBeDefined();
    expect(v(ok.replace("2.0", "9007199254740992"))).toBeDefined();
    expect(v(ok.replace('"global"', '"board"'))).toBeDefined();
    expect(v(ok.replace('"x"', '"1x"'))).toBeDefined();
    expect(
      v(
        ok.replace(
          '"scope"',
          '"boardId":"bd_01HQ8ZA01K7MWVND2CQJ4RT9XB","scope"',
        ),
      ),
    ).toBeDefined();
    expect(v(ok.replace("var_01", "var_01").replace("XB", "xb"))).toBeDefined();
    expect(
      v(ok.replace('"schemaVersion":1', '"schemaVersion":1.0')),
    ).toBeUndefined();
  });

  test("compares numbers as they are written", () => {
    expect(
      compareNumbers("1.7976931348623158e308", "1.7976931348623157e+308"),
    ).toBe(1);
    expect(
      compareNumbers("1.7976931348623157e308", "1.7976931348623157e+308"),
    ).toBe(0);
    expect(compareNumbers("-0", "0")).toBe(0);
    expect(compareNumbers("100", "1e2")).toBe(0);
    expect(compareNumbers("-2", "1")).toBe(-1);
    expect(compareNumbers("-2", "-10")).toBe(1);
    expect(compareNumbers("0.001", "1e-3")).toBe(0);
    expect(compareNumbers("9007199254740991.0000001", "9007199254740991")).toBe(
      1,
    );
  });
});

test("foldCase folds a tag's case as far as the platform's mappings do", () => {
  expect(foldCase("Quest")).toBe(foldCase("QUEST"));
  expect(foldCase("Straße")).toBe(foldCase("STRASSE"));
  expect(foldCase("ẞ")).toBe(foldCase("ss"));
  expect(foldCase("ΟΔΟΣ")).toBe(foldCase("οδος"));
  expect(foldCase("é")).toBe(foldCase("e\u0301"));
  expect(foldCase("a")).not.toBe(foldCase("b"));
});

// The suites' files hold what their manifests list, read as JSON with each
// number as it is written.
test("reads every file of the suites as JSON", () => {
  for (const name of readdirSync(conformance)) {
    if (name.includes(".")) {
      continue;
    }
    for (const [path, bytes] of readTree(join(conformance, name, "snapshot"))) {
      expect(
        () => readJSON(new TextDecoder().decode(bytes)),
        path,
      ).not.toThrow();
    }
  }
});
