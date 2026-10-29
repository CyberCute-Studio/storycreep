import { createHash } from "node:crypto";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { afterEach, describe, expect, test, vi } from "vitest";
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
import foldTable from "../../../../schema/unicode/fold.json" with {
  type: "json",
};
import {
  boolValue,
  type Context,
  compareNumbers,
  DecodeError,
  decode,
  floatValue,
  intValue,
  isJsonObject,
  JsonNumber,
  type JsonValue,
  readJSON,
  stringValue,
  type Value,
} from "../expr";
import type { SaveState } from "../save-state";
import { foldCase, nfc } from "./fold";
import { ASTUnsupportedError, BOARD_SCHEMA, LoadError, load } from "./load";
import { Runtime, StateError, ValueError } from "./runtime";
import { type Schema, validate } from "./schema";
import { YIELD_EVERY } from "./sha256";
import { conformance, readTree } from "./testing";

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

// refuseResizable throws, as a browser's WebCrypto and TextDecoder do, of a
// view of a resizable ArrayBuffer.
function refuseResizable(data: unknown): void {
  if (
    ArrayBuffer.isView(data) &&
    data.buffer instanceof ArrayBuffer &&
    data.buffer.resizable
  ) {
    throw new TypeError("a view of a resizable ArrayBuffer");
  }
}

// stubBrowserDecoder has TextDecoder refuse a view of a resizable
// ArrayBuffer, as a browser's does, until vi.unstubAllGlobals.
function stubBrowserDecoder(): void {
  const Decoder = globalThis.TextDecoder;
  vi.stubGlobal(
    "TextDecoder",
    class extends Decoder {
      override decode(
        ...args: Parameters<InstanceType<typeof Decoder>["decode"]>
      ): string {
        refuseResizable(args[0]);
        return super.decode(...args);
      }
    },
  );
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

  // A page that is no secure context, as a self-hosted instance's served
  // over plain HTTP, has no WebCrypto, crypto.subtle (PRD FR-OFF-01 AC4).
  describe("in a page without WebCrypto", () => {
    afterEach(() => {
      vi.unstubAllGlobals();
    });

    test("hashes the files with its own SHA-256", async () => {
      vi.stubGlobal("crypto", {});
      expect(globalThis.crypto.subtle).toBeUndefined();
      await expect(load(suite("choices"))).resolves.toBeDefined();
      await expect(
        load(
          suite("choices", {
            "storycreep.json": (m) => {
              m.files[main] = zeros;
            },
          }),
        ),
      ).rejects.toThrow(/its hash/);
    });

    // It copies a large file once, so that it hashes the bytes as they were
    // given: as it takes a view over a resizable buffer, which it hashes as
    // its own copy, or as it hashes a view over a fixed-length one.
    test("copies a large file once", async () => {
      vi.stubGlobal("crypto", {});
      const asset = `assets/${"0".repeat(23)}CVR.png`;
      const big = new Uint8Array(2 * YIELD_EVERY + 1).fill(7);
      const files = suite("choices", {
        "storycreep.json": (m) => {
          m.files[asset] =
            `sha256:${createHash("sha256").update(big).digest("hex")}`;
        },
      });
      const resizable = new Uint8Array(
        new ArrayBuffer(big.length, { maxByteLength: big.length + 1 }),
      );
      resizable.set(big);
      const slice = vi.spyOn(
        Object.getPrototypeOf(Uint8Array.prototype),
        "slice",
      );
      try {
        for (const given of [big, resizable]) {
          files.set(asset, given);
          slice.mockClear();
          await expect(load(files)).resolves.toBeDefined();
          const copies = slice.mock.contexts.filter(
            (c) => (c as Uint8Array).length === big.length,
          );
          // Compared by identity, as toEqual reads each of their bytes.
          expect(copies).toHaveLength(1);
          expect(copies[0] === given).toBe(true);
        }
      } finally {
        slice.mockRestore();
      }
    });
  });

  // As the Go runtime's Load does, it hashes each file as it reads it, in
  // the order in which it reads them, the files that it does not read last.
  describe("as it hashes the files", () => {
    afterEach(() => {
      vi.unstubAllGlobals();
    });

    // stubDigest has WebCrypto's digest call then with each view that it
    // hashes, once it has hashed it.
    const stubDigest = (then: (data: Uint8Array) => void) => {
      const subtle = globalThis.crypto.subtle;
      vi.stubGlobal("crypto", {
        subtle: {
          digest: async (algorithm: string, data: Uint8Array) => {
            const sum = await subtle.digest(algorithm, data.slice());
            then(data);
            return sum;
          },
        },
      });
    };

    // A browser's WebCrypto reads no view of a resizable ArrayBuffer, nor
    // does its TextDecoder, as WebIDL's BufferSource and
    // AllowSharedBufferSource take none, though Node's TextDecoder reads
    // one; and a page may give the loader one.
    test("reads and hashes a file over a resizable buffer", async () => {
      const subtle = globalThis.crypto.subtle;
      vi.stubGlobal("crypto", {
        subtle: {
          digest: async (algorithm: string, data: Uint8Array) => {
            refuseResizable(data);
            return subtle.digest(algorithm, data.slice());
          },
        },
      });
      stubBrowserDecoder();
      const files = new Map<string, Uint8Array>();
      for (const [path, bytes] of suite("choices")) {
        const view = new Uint8Array(
          new ArrayBuffer(bytes.length, { maxByteLength: bytes.length + 1 }),
        );
        view.set(bytes);
        files.set(path, view);
      }
      await expect(load(files)).resolves.toBeDefined();
    });

    test("hashes no file after the first that it refuses", async () => {
      let hashed = 0;
      stubDigest(() => {
        hashed++;
      });
      const files = suite("choices", {
        "project/settings.json": (f) => {
          f.signals = "asking";
        },
      });
      await expect(load(files)).rejects.toThrow(/project\/settings\.json/);
      expect(hashed).toBe(1);
    });

    test("reads the bytes that it hashed, whatever becomes of them meanwhile", async () => {
      // The bytes are blanked once they are hashed.
      stubDigest((data) => {
        data.fill(0x20);
      });
      await expect(load(suite("choices"))).resolves.toBeDefined();
      vi.unstubAllGlobals();
      // The map gives other bytes of a path once it has given its own.
      const given = new Set<string>();
      const files = new (class extends Map<string, Uint8Array> {
        override get(path: string): Uint8Array | undefined {
          const bytes = super.get(path);
          if (bytes !== undefined && given.has(path)) {
            return new TextEncoder().encode("{}");
          }
          given.add(path);
          return bytes;
        }
      })(suite("choices"));
      await expect(load(files)).resolves.toBeDefined();
    });
  });

  // As the Go runtime's Load opens each file of its fs.FS as it comes to
  // it, load takes each file from a function once, in the order in which
  // it reads them, an asset, which it hashes but does not read, last, so
  // that a page may fetch each file only once load asks for it.
  describe("from a function", () => {
    // An asset, which the manifest lists, of its bytes.
    const asset = `assets/${"0".repeat(23)}CVR.png`;
    const png = new Uint8Array([
      0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
    ]);
    // withAsset returns the files of the suite choices and the asset, which
    // the manifest lists with the hash.
    const withAsset = (hash: string) => {
      const files = suite("choices", {
        "storycreep.json": (m) => {
          m.files[asset] = hash;
        },
      });
      files.set(asset, png);
      return files;
    };
    const pngHash = `sha256:${createHash("sha256").update(png).digest("hex")}`;

    test("takes each file once, in the order in which it reads them", async () => {
      const files = withAsset(pngHash);
      const taken: string[] = [];
      await expect(
        load(async (path) => {
          taken.push(path);
          return files.get(path);
        }),
      ).resolves.toBeDefined();
      expect(taken).toEqual([
        "storycreep.json",
        "project/settings.json",
        "project/variables.json",
        "project/component_types.json",
        "project/components.json",
        main,
        asset,
      ]);
    });

    test("refuses an asset of another hash, and a file that it does not give", async () => {
      const files = withAsset(zeros);
      await expect(load(async (path) => files.get(path))).rejects.toThrow(
        `${asset}: its hash`,
      );
      await expect(
        load(async (path) =>
          path === "project/variables.json" ? undefined : files.get(path),
        ),
      ).rejects.toThrow("there is no project/variables.json");
    });

    test("stops once its signal is aborted", async () => {
      const files = withAsset(pngHash);
      const c = new AbortController();
      const taken: string[] = [];
      await expect(
        load(
          async (path, signal) => {
            expect(signal).toBe(c.signal);
            taken.push(path);
            if (taken.length === 2) {
              c.abort(new Error("stopped"));
            }
            return files.get(path);
          },
          { signal: c.signal },
        ),
      ).rejects.toThrow("stopped");
      expect(taken).toHaveLength(2);
      taken.length = 0;
      await expect(
        load(
          async (path) => {
            taken.push(path);
            return files.get(path);
          },
          { signal: AbortSignal.abort(new Error("stopped before")) },
        ),
      ).rejects.toThrow("stopped before");
      expect(taken).toEqual([]);
    });
  });

  // ı folds to itself, as the Go runtime folds it, rather than to i, as
  // JavaScript's toUpperCase and toLowerCase map it; the Go runtime's form
  // C, of Unicode 15.0, leaves U+16D67 KIRAT RAI VOWEL SIGN E twice as it
  // is, though a platform's form C of Unicode 16.0 or later joins the two
  // into U+16D68; and it inserts a CGJ after 30 non-starters in a row, so
  // that it does not put marks after the 30th before those up to it, though
  // a platform's form C does.
  test("takes two tags that the Go runtime's case folding tells apart", async () => {
    const below = String.fromCodePoint(0x323).repeat(16);
    const acute = String.fromCodePoint(0x301).repeat(16);
    for (const tags of [
      ["kır", "KIR"],
      [String.fromCodePoint(0x16d68), String.fromCodePoint(0x16d67, 0x16d67)],
      [`a${below}${acute}`, `a${acute}${below}`],
    ]) {
      await expect(
        load(
          suite("choices", {
            [main]: (b) => {
              nodeOf(b, nd("START")).tags = tags;
            },
          }),
        ),
      ).resolves.toBeDefined();
    }
  });

  // A platform whose Unicode is older than the Go runtime's, as a browser's
  // on a system that has not been updated, of Unicode 14.0, takes U+1E4EF
  // NAG MUNDARI SIGN SUTUH, a mark of Unicode 15.0, for an unassigned
  // starter, which its form C neither orders nor joins across; the Go
  // runtime's form C puts U+0323 COMBINING DOT BELOW before it, which joins
  // a into ạ, and U+0345 COMBINING GREEK YPOGEGRAMMENI after it, which folds
  // to ι. The loader compares tags by the tables of fold.json alone, and
  // never calls the platform's form C, of whatever version of Unicode.
  test("compares tags as the Go runtime does without the platform's form C", async () => {
    const mark = String.fromCodePoint(0x1e4ef);
    vi.spyOn(String.prototype, "normalize").mockImplementation(() => {
      throw new Error("the loader calls the platform's form C");
    });
    try {
      await expect(
        load(
          suite("choices", {
            [main]: (b) => {
              nodeOf(b, nd("START")).tags = [
                `a\u0345${mark}`,
                `a\u03b9${mark}`,
              ];
            },
          }),
        ),
      ).resolves.toBeDefined();
      await expect(
        load(
          suite("choices", {
            [main]: (b) => {
              nodeOf(b, nd("START")).tags = [`\u1ea1${mark}`, `a${mark}\u0323`];
            },
          }),
        ),
      ).rejects.toThrow(/the same but for their case/);
    } finally {
      vi.restoreAllMocks();
    }
  });

  // An AST that is one level deeper than an AST may be, which no schema
  // refuses, but decode does.
  let deep: Obj = { kind: "lit", type: "bool", value: true };
  for (let i = 0; i < 64; i++) {
    deep = { kind: "unary", op: "not", operand: deep };
  }
  // A script that is deeper still.
  const deepScript = {
    kind: "script",
    statements: [{ kind: "call", function: "signal", args: [deep] }],
  };

  // As the Go runtime's reader of a file, published.Decode, decodes a
  // board's ASTs as it reads the file, one that it cannot decode is refused
  // before what the loader checks once it has read every file, as a signal
  // that is there twice.
  test("refuses an AST that it cannot decode as it reads its board's file", async () => {
    const files = suite("choices", {
      "project/settings.json": (f) => {
        f.signals = ["asking", "asking"];
      },
      [main]: (b) => {
        for (const c of b.connections) {
          if (c.condition !== undefined) {
            c.condition.ast = deep;
            return;
          }
        }
        throw new Error("no condition");
      },
    });
    await expect(load(files)).rejects.toThrow(
      /boards\/main\.json: the condition of .*deeper than 64/,
    );
  });

  // Go's published.Decode reads a file's keys sorted, whatever order the
  // file writes them in, so that it decodes the connections' ASTs before the
  // nodes', and a node's onEnter before its onExit.
  test("decodes a board's ASTs in the order of their keys sorted", async () => {
    const files = (edit: (b: Obj) => void) =>
      suite("branches", {
        [roll]: (b) => {
          edit(b);
          // The nodes, then the connections, which the file writes last.
          const connections = b.connections;
          delete b.connections;
          b.connections = connections;
        },
      });
    await expect(
      load(
        files((b) => {
          nodeOf(b, nd("GATE")).onExit.ast = deepScript;
          connectionOf(b, cn("AF1")).condition.ast = deep;
        }),
      ),
    ).rejects.toThrow(/the condition of cn_0+AF1/);
    await expect(
      load(
        files((b) => {
          const gate = nodeOf(b, nd("GATE"));
          const onEnter = gate.onEnter;
          delete gate.onEnter;
          gate.onEnter = onEnter;
          gate.onEnter.ast = deepScript;
          gate.onExit.ast = deepScript;
        }),
      ),
    ).rejects.toThrow(/the onEnter of nd_0+GATE/);
  });

  // A key that the format defines as an expression of one type is no
  // expression of another type that does not define it, and the Go
  // runtime's reader of a file ignores it there, whatever it holds, as it
  // ignores any key that it does not know (§6.4).
  test("ignores the key of an expression where its type does not define it", async () => {
    const strange = (o: Obj, ...keys: string[]) => {
      for (const key of keys) {
        o[key] = key === "outputs" ? { a: 1 } : { ast: deep, of: "none" };
      }
    };
    await expect(
      load(
        suite("branches", {
          [roll]: (b) => {
            for (const c of b.connections) {
              strange(c, "onEnter", "onExit", "outputs");
            }
            for (const n of b.nodes) {
              strange(n, "condition", "onTraverse");
              for (const o of n.outputs ?? []) {
                strange(o, "onEnter", "onExit", "onTraverse", "outputs");
              }
            }
          },
        }),
      ),
    ).resolves.toBeDefined();
    await expect(
      load(
        suite("jumpers", {
          [harbour]: (b) => {
            for (const n of b.nodes) {
              strange(n, "condition", "onTraverse");
            }
          },
        }),
      ),
    ).resolves.toBeDefined();
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
      // Which decode refuses, as its schema would, which load leaves an AST
      // to.
      /boards\/main\.json: the condition of cn_\w+: .*the value of a lit node of the type int/,
    ],
    [
      "an AST of a node of no kind",
      "choices",
      {
        [main]: (b) => {
          nodeOf(b, nd("GATED")).onExit.ast.statements[0].kind = "loop";
        },
      },
      /boards\/main\.json: the onExit of nd_\w+: .*"loop" is no kind of node/,
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

  test("is what loadState loads again from its JSON, each number as it is written", async () => {
    const s = await load(suite("choices"));
    const r = new Runtime(s, false);
    r.start("", "");
    r.choose(cn("ASK"));
    const st = r.saveState();
    const again = new Runtime(s, false);
    again.loadState(JSON.stringify(st));
    expect(again.saveState()).toEqual(st);
    expect(again.current()).toEqual(r.current());
    // A visit count, a version, and an int written with a fraction of
    // zeros are integers.
    const written = JSON.stringify({
      ...st,
      schemaVersion: "SV",
      visits: { [nd("START")]: "V" },
    })
      .replace('"SV"', `${st.schemaVersion}.0`)
      .replace('"V"', "1.00")
      .replace(/"stateVersion":1/, '"stateVersion":1e0');
    again.loadState(written);
    expect(again.visits(nd("START"))).toBe(1);
    // An int's 2.0000000000000001, which JSON.parse reads as 2, is no
    // integer, as the Go runtime reads it.
    const text = JSON.stringify({
      ...st,
      variables: { ...st.variables, [vr("CASH")]: "CASH" },
    }).replace('"CASH"', "2.0000000000000001");
    expect(() => again.loadState(text)).toThrow(StateError);
    expect(() => again.loadState(text)).toThrow(/not an integer/);
    expect(() => again.loadState(`${JSON.stringify(st)} x`)).toThrow(
      /not one JSON value/,
    );
  });

  test.each<[string, (st: Obj) => void, RegExp]>([
    [
      "holding a null where the format takes any value but null",
      (st) => {
        st.later = { of: [null] };
      },
      /holds null/,
    ],
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
  // Each refused with a ValueError that says why.
  test.each<[string, string, Value, RegExp]>([
    [
      "a value of another type",
      vr("CASH"),
      stringValue("1"),
      /no value of the type int/,
    ],
    [
      "a string of more than 4,096 characters",
      vr("NAME"),
      stringValue("a".repeat(4097)),
      /more than 4096 characters/,
    ],
    ["a string with a NUL", vr("NAME"), stringValue("a\0b"), /with a NUL/],
    [
      "a string with a lone surrogate",
      vr("NAME"),
      stringValue("a\uD800"),
      /a string with a lone surrogate/,
    ],
    [
      "an int beyond ±(2^53 − 1)",
      vr("CASH"),
      intValue(2 ** 53),
      /an int beyond ±9007199254740991/,
    ],
    ["no variable", vr("NADA"), boolValue(true), /no variable/],
  ])("refuse %s", async (_, variable, v, why) => {
    const r = new Runtime(await load(suite("choices")), false);
    expect(() => r.setVariable(variable, v)).toThrow(ValueError);
    expect(() => r.setVariable(variable, v)).toThrow(why);
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

  // As the Go runtime compares the value that a save would read back with
  // the one that it sets, which of a -0 is 0.
  test("set a float's -0 as 0, and refuse a float that is not finite", async () => {
    const s = await load(suite("values"));
    const r = new Runtime(s, false);
    const float = [...s.declared.values()].find((d) => d.type === "float");
    if (float === undefined) {
      throw new Error("no float variable");
    }
    r.setVariable(float.id, { ...floatValue(1), float: -0 });
    expect(Object.is(r.variable(float.id).float, 0)).toBe(true);
    for (const f of [Number.NaN, Number.POSITIVE_INFINITY]) {
      expect(() =>
        r.setVariable(float.id, { ...floatValue(1), float: f }),
      ).toThrow(ValueError);
    }
    expect(Object.is(r.variable(float.id).float, 0)).toBe(true);
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

// load checks a board's file against its schema but for its expressions'
// ASTs, which it decodes, so that it reads each AST once.
describe("a board's ASTs", () => {
  // reached returns the names of the definitions that the document's root
  // refers to, at any remove.
  const reached = (document: Schema): Set<string> => {
    const defs = (document as { $defs: Record<string, Schema> }).$defs;
    const names = new Set<string>();
    const { $defs: _, ...root } = document as Record<string, unknown>;
    const stack: unknown[] = [root];
    for (let s = stack.pop(); s !== undefined; s = stack.pop()) {
      if (Array.isArray(s)) {
        stack.push(...s);
      } else if (typeof s === "object" && s !== null) {
        for (const [k, v] of Object.entries(s)) {
          if (k === "$ref" && typeof v === "string") {
            const name = v.slice("#/$defs/".length);
            if (!names.has(name)) {
              names.add(name);
              stack.push(defs[name]);
            }
          } else if (!["const", "default", "enum", "examples"].includes(k)) {
            stack.push(v);
          }
        }
      }
    }
    return names;
  };
  const isAST = (name: string) => name.startsWith("AST");

  test("load's schema of a board reads none of them", () => {
    expect([...reached(board as Schema)].filter(isAST)).toContain(
      "ASTExpression",
    );
    expect([...reached(BOARD_SCHEMA)].filter(isAST)).toEqual([]);
  });

  // As the Go test TestDecodeAgreesWithSchema checks of the Go decoder,
  // decode reads exactly the JSON that the board's schema of an AST takes,
  // of mutations of the compile corpus's ASTs, whose names are resolved:
  // each of their values replaced by one of another kind or of another
  // value, and a key that no node defines added to each of their objects,
  // which both ignore; so that load, which leaves an AST to decode, refuses
  // what the schema refuses.
  test("decode refuses what their schema refuses", () => {
    const corpus = readJSON(
      readFileSync(
        new URL("../../../../schema/expr-corpus/compile.json", import.meta.url),
        "utf8",
      ),
    );
    const cases = (
      isJsonObject(corpus) && Array.isArray(corpus.cases) ? corpus.cases : []
    )
      .filter(isJsonObject)
      .filter((c) => c.ast !== undefined);
    expect(cases.length).toBeGreaterThanOrEqual(50);
    const schemaOf = (ctx: Context): Schema => ({
      $defs: (board as { $defs: unknown }).$defs,
      $ref: ctx === "script" ? "#/$defs/ASTScript" : "#/$defs/ASTExpression",
    });
    // readJSON never reads a null, so neither the loader nor this test
    // gives one.
    const replacements: JsonValue[] = [
      "x",
      "a\u0000b",
      new JsonNumber("1.5"),
      new JsonNumber("9007199254740992"),
      true,
      Object.create(null),
      [],
      "var_01HQ8ZV01K7MWVND2CQJ4RT9XB",
      "nd_01hq8zn01k7mwvnd2cqj4rt9xb",
      "script",
      "+=",
      new JsonNumber("1.7976931348623157e308"),
      new JsonNumber("-1.7976931348623158e308"),
    ];
    // The shortest ASTs that together use every kind of node, operator, and
    // literal's type that the corpus's ASTs use, in each context, which
    // cover what every AST holds at a fraction of the cost.
    const sized = cases
      .map((c) => ({ c, size: JSON.stringify(c.ast).length }))
      .sort((a, b) => a.size - b.size);
    const covered = new Set<string>();
    const failures: string[] = [];
    let checked = 0;
    for (const { c } of sized) {
      const ctx = c.context as Context;
      let adds = false;
      const collect = (v: JsonValue) => {
        if (Array.isArray(v)) {
          v.forEach(collect);
        } else if (isJsonObject(v)) {
          for (const [k, x] of Object.entries(v)) {
            if (typeof x === "string" && ["kind", "op", "type"].includes(k)) {
              const token = `${ctx}:${k}=${x}`;
              adds ||= !covered.has(token);
              covered.add(token);
            }
            collect(x);
          }
        }
      };
      const tree = c.ast as JsonValue;
      collect(tree);
      if (!adds) {
        continue;
      }
      const attempt = (at: string) => {
        checked++;
        const problem = validate(schemaOf(ctx), tree);
        let decoded: unknown;
        try {
          decode(tree, ctx);
        } catch (e) {
          if (!(e instanceof DecodeError)) {
            throw e;
          }
          decoded = e;
        }
        if ((problem === undefined) !== (decoded === undefined)) {
          failures.push(
            `${String(c.source)} with ${at}: the schema: ${problem ?? "takes it"}; decode: ${decoded ?? "reads it"}`,
          );
        }
      };
      const mutate = (v: JsonValue, path: string) => {
        if (Array.isArray(v)) {
          v.forEach((x, i) => {
            mutate(x, `${path}[${i}]`);
          });
        } else if (isJsonObject(v)) {
          const o = v as { [key: string]: JsonValue };
          o.future = readJSON('{"a":[1]}');
          attempt(`${path}+future`);
          delete o.future;
          for (const [k, x] of Object.entries(o)) {
            for (const r of replacements) {
              o[k] = r;
              attempt(`${path}.${k} = ${JSON.stringify(r)}`);
            }
            o[k] = x;
            mutate(x, `${path}.${k}`);
          }
        }
      };
      mutate(tree, "$");
    }
    expect(failures).toEqual([]);
    expect(checked).toBeGreaterThanOrEqual(500);
  });
});

// Each tag and what the Go runtime's input.FoldCase returns of it.
test("foldCase folds a tag's case as the Go runtime does", () => {
  for (const [tag, folded] of [
    ["Quest", "quest"],
    ["QUEST", "quest"],
    ["Straße", "strasse"],
    ["STRASSE", "strasse"],
    ["ẞ", "ss"],
    ["ΟΔΟΣ", "οδοσ"],
    ["οδος", "οδοσ"],
    ["KIR", "kir"],
    ["kır", "kır"],
    ["İ", "i\u0307"],
    ["ΐ", "ΐ"],
    ["\u03aa\u0301", "ΐ"],
    ["é", "é"],
    ["e\u0301", "é"],
  ]) {
    expect(foldCase(tag), tag).toBe(folded);
  }
  // Go's form C joins the code points that its version of Unicode assigns,
  // a Hangul syllable and a final jamo too, and leaves those that it does
  // not assign as they are, as the platform's form C of a later version
  // does not.
  for (const [tag, folded] of [
    [[0xac00, 0x11a8], [0xac01]],
    [[0x41, 0x30a, 0x301], [0x1fb]],
    [[0x16d68], [0x16d68]],
    [
      [0x16d67, 0x16d67],
      [0x16d67, 0x16d67],
    ],
    [
      [0x61, 0x16d67, 0x301],
      [0x61, 0x16d67, 0x301],
    ],
  ]) {
    expect(foldCase(String.fromCodePoint(...tag)), tag.join(" ")).toBe(
      String.fromCodePoint(...folded),
    );
  }
});

// Each tag and what the Go runtime's input.FoldCase returns of it, whatever
// the platform's form C gives, which foldCase never calls: U+1E4EF NAG
// MUNDARI SIGN SUTUH is a mark of Unicode 15.0, of class 230, which a
// platform of Unicode 14.0 takes for an unassigned starter.
test("foldCase folds a tag as the Go runtime does without the platform's form C", () => {
  const spy = vi.spyOn(String.prototype, "normalize").mockImplementation(() => {
    throw new Error("foldCase calls the platform's form C");
  });
  try {
    for (const [tag, folded] of [
      [
        [0x1ea1, 0x1e4ef],
        [0x1ea1, 0x1e4ef],
      ],
      [
        [0x61, 0x1e4ef, 0x323],
        [0x1ea1, 0x1e4ef],
      ],
      [
        [0x61, 0x345, 0x1e4ef],
        [0x61, 0x1e4ef, 0x3b9],
      ],
      [
        [0x61, 0x3b9, 0x1e4ef],
        [0x61, 0x3b9, 0x1e4ef],
      ],
      [[0x1e9e], [0x73, 0x73]],
      [[0x3aa, 0x301], [0x390]],
      [[0x1100, 0x1161, 0x11a8], [0xac01]],
      [
        [0xac00, 0x11a7],
        [0xac00, 0x11a7],
      ],
      [
        [0x16d67, 0x16d67],
        [0x16d67, 0x16d67],
      ],
    ]) {
      expect(foldCase(String.fromCodePoint(...tag)), tag.join(" ")).toBe(
        String.fromCodePoint(...folded),
      );
    }
    expect(spy).not.toHaveBeenCalled();
  } finally {
    vi.restoreAllMocks();
  }
});

// fold.json's tables of form C give the platform's form C of strings of the
// code points that their version of Unicode assigns, as long as the
// platform's version is that one or a later one, since Unicode never changes
// the form C of those: each pair that they join, and it beside marks that
// may block it; each code point that they decompose, alone and beside marks;
// each mark beside marks of lower and higher classes and of its own; and the
// Hangul jamo and syllables: each leading consonant, and the code point
// after the last, beside each vowel, and the code points before the first
// and after the last; each syllable of no trailing consonant beside each
// trailing one, the two code points before the first, and the one after the
// last; and each other syllable alone, and beside a trailing consonant and a
// mark.
test("nfc gives the platform's form C of the code points of fold.json's version", () => {
  expect(
    Number.parseFloat(process.versions.unicode ?? "0"),
  ).toBeGreaterThanOrEqual(Number.parseFloat(foldTable.unicodeVersion));
  const s = (...cps: number[]) => String.fromCodePoint(...cps);
  const inputs: string[] = [];
  for (const [c, first, second] of foldTable.compositions) {
    expect(nfc(s(first, second)), `${first} ${second}`).toBe(s(c));
    inputs.push(
      s(first, second),
      s(first, 0x334, second),
      s(first, 0x323, second),
      s(first, 0x301, second),
      s(first, 0x345, second),
      s(first, 0x61, second),
    );
  }
  for (const [c] of foldTable.decompositions) {
    inputs.push(s(c), s(c, 0x301), s(0x61, c, 0x334), s(c, 0x323, 0x345));
  }
  for (const [first, last] of foldTable.ccc) {
    for (let c = first; c <= last; c++) {
      inputs.push(
        s(0x61, c, 0x323, 0x301, c, 0x334, 0x345),
        s(c, 0x61, c),
        s(0x61, 0x345, c, c, 0x334),
      );
    }
  }
  for (let l = 0x1100; l <= 0x1113; l++) {
    for (let v = 0x1160; v <= 0x1176; v++) {
      inputs.push(s(l, v), s(l, v, 0x11a8), s(l, 0x301, v));
    }
  }
  for (let syllable = 0xac00; syllable <= 0xd7a3; syllable++) {
    if ((syllable - 0xac00) % 28 === 0) {
      for (let t = 0x11a6; t <= 0x11c3; t++) {
        inputs.push(s(syllable, t));
      }
    } else {
      inputs.push(s(syllable), s(syllable, 0x11a8), s(syllable, 0x301));
    }
  }
  for (const input of inputs) {
    expect(nfc(input), [...input].map((c) => c.codePointAt(0)).join(" ")).toBe(
      input.normalize("NFC"),
    );
  }
});

// fold.json's checks are the SHA-256 of what the Go runtime's
// input.FoldCase returns of strings that a runtime builds from the table, as
// schema/unicode/README.md says: every code point; each that the folding
// changes or that is not inert in form C beside letters and marks; and each
// that is not inert after and before rows of acutes, where Go's form C
// inserts a CGJ.
test("foldCase folds every code point as the Go runtime does", {
  timeout: 30_000,
}, () => {
  const folding = new Set(foldTable.fold.map(([c]) => c));
  const composing = new Set(
    foldTable.nfc.flatMap(([first, last]) =>
      Array.from({ length: last - first + 1 }, (_, k) => first + k),
    ),
  );
  const neighbours = [0x61, 0x307, 0x345, 0x3a3, 0x3c2, 0x130, 0xac00, 0x11a8]
    .map((c) => String.fromCodePoint(c))
    .concat("");
  const acutes = (n: number) => String.fromCodePoint(0x301).repeat(n);
  const every: string[] = [];
  const beside: string[] = [];
  const streams: string[] = [];
  for (const c of composing) {
    const s = String.fromCodePoint(c);
    streams.push(`a${acutes(30)}${s}${acutes(30)}`);
    streams.push(`a${acutes(29)}${s}`, `a${acutes(28)}${s}`);
  }
  for (let c = 0; c <= 0x10ffff; c++) {
    if (c >= 0xd800 && c <= 0xdfff) {
      continue;
    }
    const s = String.fromCodePoint(c);
    every.push(s);
    if (folding.has(c) || composing.has(c)) {
      for (const n of neighbours) {
        beside.push(s, n);
      }
    }
  }
  for (const [name, input] of [
    ["every", every],
    ["beside", beside],
    ["streams", streams],
  ] as const) {
    const sum = createHash("sha256")
      .update(foldCase(input.join("")), "utf8")
      .digest("hex");
    expect(`sha256:${sum}`, name).toBe(foldTable.checks[name]);
  }
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
