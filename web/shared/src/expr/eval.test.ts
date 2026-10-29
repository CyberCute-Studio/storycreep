import { readFileSync } from "node:fs";
import { afterEach, describe, expect, test, vi } from "vitest";
import { type BinaryOp, type Context, NO_RANGE, type Node } from "./ast";
import { BUILTINS, builtins } from "./builtins";
import { compile } from "./check";
import { DecodeError, decode } from "./decode";
import { encode } from "./encode";
import {
  condition,
  type Env,
  type EvaluationError,
  evaluate,
  evaluations,
  MAX_STEPS,
  run,
} from "./eval";
import {
  decimalOf,
  isIntegral,
  isJsonObject,
  type JsonValue,
  plain,
  readJSON,
  readJSONBytes,
} from "./json";
import { Project } from "./project";
import { parseFloatText } from "./semantics";
import {
  type DeclaredType,
  equalValue,
  floatValue,
  intValue,
  jsonOf,
  NONE,
  readValue,
  sameValue,
  stringValue,
  type Value,
} from "./value";

// readCorpus returns the cases of a file of schema/expr-corpus, read with
// each number as it is written, so that a float is read as to_float reads
// it, correctly rounded, as the corpus's README says a runner reads one.
function readCorpus(name: string): { [key: string]: JsonValue }[] {
  const file = readJSON(
    readFileSync(
      new URL(`../../../../schema/expr-corpus/${name}`, import.meta.url),
      "utf8",
    ),
  );
  if (!isJsonObject(file) || !Array.isArray(file.cases)) {
    throw new Error(`${name} has no cases`);
  }
  return file.cases.filter(isJsonObject);
}

// typed returns a case's value, its type and its value, as its type reads
// it.
function typed(v: JsonValue): Value {
  if (!isJsonObject(v) || typeof v.type !== "string") {
    throw new Error(`${JSON.stringify(v)} is no typed value`);
  }
  const [read, problem] = readValue(v.value, v.type as DeclaredType);
  if (problem !== undefined) {
    throw new Error(`${JSON.stringify(v)} ${problem}`);
  }
  return read;
}

// literal returns the literal of the value v.
function literal(v: Value): Node {
  switch (v.type) {
    case "bool":
      return { kind: "lit", type: "bool", value: v.bool, src: NO_RANGE };
    case "int":
      return { kind: "lit", type: "int", value: v.int, src: NO_RANGE };
    case "float":
      return { kind: "lit", type: "float", value: v.float, src: NO_RANGE };
  }
  return { kind: "lit", type: "string", value: v.str, src: NO_RANGE };
}

const operators: Record<string, BinaryOp> = {
  "+": "add",
  "-": "sub",
  "*": "mul",
  "/": "div",
  "%": "mod",
  "==": "eq",
  "!=": "ne",
  "<": "lt",
  "<=": "lte",
  ">": "gt",
  ">=": "gte",
};

// A test's Env: no variables, attributes, or visits, and the runtime errors
// that it reports.
class TestEnv implements Env {
  readonly errors: EvaluationError[] = [];
  readonly signals: [string, Value | undefined][] = [];
  readonly vars = new Map<string, Value>();
  variable(id: string): Value {
    return this.vars.get(id) ?? NONE;
  }
  setVariable(id: string, v: Value): void {
    this.vars.set(id, v);
  }
  attribute(): Value {
    return NONE;
  }
  setAttribute(): void {}
  visits(): number {
    return 0;
  }
  signal(name: string, value: Value | undefined): void {
    this.signals.push([name, value]);
  }
  error(code: EvaluationError): void {
    this.errors.push(code);
  }
}

describe("the evaluator", () => {
  // Every case of an operation of the corpus, but interpolate's, which the
  // flow's text runs, is an AST of its operator or built-in of literals,
  // whose value the evaluator gives as the Go evaluator does, a float by its
  // bits, with the runtime error that it reports (TestEvaluatorCorpus), so
  // that every built-in has a TypeScript implementation that the corpus
  // holds to the Go one's results.
  // Verifies: FR-EXPR-04/AC1
  test("gives each operation of the corpus its result and its error", () => {
    let ran = 0;
    const failures: string[] = [];
    for (const name of ["numbers.json", "strings.json", "formatting.json"]) {
      for (const c of readCorpus(name)) {
        if (c.op === "interpolate") {
          continue;
        }
        const where = `${name}: ${String(c.note)}`;
        const args = (c.args as JsonValue[]).map((a) => literal(typed(a)));
        const op = String(c.op);
        let n: Node;
        if (op === "-" && args.length === 1) {
          n = { kind: "unary", op: "neg", operand: args[0], src: NO_RANGE };
        } else if (Object.hasOwn(operators, op) && args.length === 2) {
          n = {
            kind: "binary",
            op: operators[op],
            left: args[0],
            right: args[1],
            src: NO_RANGE,
          };
        } else {
          n = { kind: "call", function: op, args, src: NO_RANGE };
        }
        const want = typed(c.result);
        const wantErrors = typeof c.error === "string" ? [c.error] : [];
        const env = new TestEnv();
        const g = evaluate(n, env);
        const same =
          g.type === want.type &&
          g.bool === want.bool &&
          g.int === want.int &&
          Object.is(g.float, want.float) &&
          g.str === want.str &&
          JSON.stringify(env.errors) === JSON.stringify(wantErrors);
        if (!same) {
          failures.push(
            `${where}: ${op} = ${JSON.stringify(g)} ${JSON.stringify(env.errors)}; want ${JSON.stringify(want)} ${JSON.stringify(wantErrors)}`,
          );
        }
        ran++;
      }
    }
    expect(failures).toEqual([]);
    expect(ran).toBeGreaterThan(0);
  });

  test("has an evaluation of each built-in, and of nothing else", () => {
    expect([...evaluations.keys()].sort()).toEqual(builtins());
    expect(builtins()).toEqual([...BUILTINS.keys()].sort());
  });

  test("reports step_budget_exceeded beyond MAX_STEPS, and a condition is false", () => {
    // A balanced sum of 2^14 ones, of more steps than the budget, which no
    // AST that check accepts is, compared with 0.
    const sum = (depth: number): Node =>
      depth === 0
        ? { kind: "lit", type: "int", value: 1, src: NO_RANGE }
        : {
            kind: "binary",
            op: "add",
            left: sum(depth - 1),
            right: sum(depth - 1),
            src: NO_RANGE,
          };
    const n: Node = {
      kind: "binary",
      op: "gt",
      left: sum(14),
      right: { kind: "lit", type: "int", value: 0, src: NO_RANGE },
      src: NO_RANGE,
    };
    expect(2 ** 15).toBeGreaterThan(MAX_STEPS);
    const env = new TestEnv();
    expect(condition(n, env)).toBe(false);
    expect(env.errors).toEqual(["step_budget_exceeded"]);
  });

  test("goes on after a runtime error with the zero value, and short-circuits", () => {
    const p = Project.of([], [], []);
    const env = new TestEnv();
    expect(condition(compile("1 / 0 + 5 == 5", "condition", p, ""), env)).toBe(
      true,
    );
    expect(env.errors).toEqual(["division_by_zero"]);
    const env2 = new TestEnv();
    expect(
      condition(compile("false && 1 / 0 == 0", "condition", p, ""), env2),
    ).toBe(false);
    expect(
      condition(compile("true || 1 / 0 == 0", "condition", p, ""), env2),
    ).toBe(true);
    expect(env2.errors).toEqual([]);
  });

  test("runs a script's assignments and signals in order", () => {
    const p = Project.of(
      [
        { id: "var_01HQ8ZV01K7MWVND2CQJ4RT9XB", key: "gold", type: "int" },
        { id: "var_01HQ8ZV02K7MWVND2CQJ4RT9XB", key: "rate", type: "float" },
      ],
      [],
      [],
    );
    const s = compile(
      'gold += 2; rate = gold; gold *= 3; signal("paid", gold); signal("done")',
      "script",
      p,
      "",
    );
    const env = new TestEnv();
    env.vars.set("var_01HQ8ZV01K7MWVND2CQJ4RT9XB", intValue(1));
    env.vars.set("var_01HQ8ZV02K7MWVND2CQJ4RT9XB", floatValue(0));
    if (s.kind !== "script") {
      throw new Error("not a script");
    }
    run(s, env);
    expect(env.vars.get("var_01HQ8ZV01K7MWVND2CQJ4RT9XB")).toEqual(intValue(9));
    expect(env.vars.get("var_01HQ8ZV02K7MWVND2CQJ4RT9XB")?.float).toBe(3);
    expect(env.vars.get("var_01HQ8ZV02K7MWVND2CQJ4RT9XB")?.type).toBe("float");
    expect(env.signals).toEqual([
      ["paid", intValue(9)],
      ["done", undefined],
    ]);
  });

  // An assignment of an op that is none of the language's, as a malformed
  // node's, assigns its value as = does, as the Go evaluator's map gives,
  // whatever the op's name: that of a member of Object.prototype too.
  test("assigns as = does of an op that is none of the language's", () => {
    const gold = "var_01HQ8ZV01K7MWVND2CQJ4RT9XB";
    const p = Project.of([{ id: gold, key: "gold", type: "int" }], [], []);
    for (const op of ["constructor", "toString", "__proto__", "valueOf"]) {
      const s = compile("gold = 3", "script", p, "");
      if (s.kind !== "script") {
        throw new Error("not a script");
      }
      (s.statements[0] as { op: string }).op = op;
      const env = new TestEnv();
      env.vars.set(gold, intValue(7));
      run(s, env);
      expect(env.vars.get(gold), op).toEqual(intValue(3));
      expect(env.errors, op).toEqual([]);
    }
  });
});

describe("decode", () => {
  // Every AST of the compile corpus, its names resolved, as the published
  // format holds one, decodes to an AST that encode writes as it was
  // written, in its canonical form (FR-EXPR-03 AC3).
  test("reads every AST of the compile corpus back as encode wrote it", () => {
    const corpus = JSON.parse(
      readFileSync(
        new URL("../../../../schema/expr-corpus/compile.json", import.meta.url),
        "utf8",
      ),
    );
    const p = Project.of(
      corpus.project.variables,
      corpus.project.components,
      corpus.project.nodes,
    );
    let n = 0;
    for (const c of corpus.cases) {
      if (c.ast === undefined) {
        continue;
      }
      const ast = compile(c.source, c.context, p, c.board ?? "");
      const text = encode(ast);
      expect(encode(decode(text, c.context))).toBe(text);
      expect(encode(decode(readJSON(text), c.context))).toBe(text);
      expect(encode(decode(JSON.parse(text), c.context))).toBe(text);
      n++;
    }
    expect(n).toBeGreaterThan(0);
  });

  test.each<[string, Context, RegExp]>([
    [
      '{"kind":"lit","type":"int","value":2.5}',
      "condition",
      /is not an integer/,
    ],
    [
      '{"kind":"lit","type":"int","value":4503599627370495.5}',
      "condition",
      /is not an integer/,
    ],
    [
      '{"kind":"lit","type":"int","value":9007199254740992}',
      "condition",
      /from -9007199254740991 to 9007199254740991/,
    ],
    [
      '{"kind":"lit","type":"float","value":1.7976931348623158e308}',
      "condition",
      /beyond ±1\.7976931348623157e\+308/,
    ],
    [
      '{"kind":"lit","type":"float","value":0e1000}',
      "condition",
      /exponent beyond ±999/,
    ],
    [
      '{"kind":"lit","type":"string","value":"\\ud800"}',
      "condition",
      /lone surrogate/,
    ],
    [
      '{"kind":"lit","type":"string","value":"\\u0000"}',
      "condition",
      /with a NUL/,
    ],
    ['{"kind":"lit","type":"bool","value":null}', "condition", /holds null/],
    [
      '{"kind":"lit","type":"bool","value":true}',
      "script",
      /a script's AST is a script exactly when/,
    ],
    [
      '{"kind":"script","statements":[]}',
      "condition",
      /a condition's AST is a script exactly when/,
    ],
    [
      '{"kind":"script","statements":[{"kind":"lit","type":"bool","value":true}]}',
      "script",
      /the kind lit cannot be/,
    ],
    [
      '{"kind":"var","name":"x","ref":"var_01hq8zv01k7mwvnd2cqj4rt9xb"}',
      "condition",
      /canonical spelling/,
    ],
    [
      '{"kind":"var","name":"1x","ref":"var_01HQ8ZV01K7MWVND2CQJ4RT9XB"}',
      "condition",
      /not an identifier/,
    ],
    [
      '{"kind":"attr","name":"a","ref":"cmp_01HQ8ZC01K7MWVND2CQJ4RT9XB","attribute":"atr_01HQ8ZR01K7MWVND2CQJ4RT9XB"}',
      "condition",
      /two keys joined by a dot/,
    ],
    [
      '{"kind":"unary","op":"inc","operand":{"kind":"lit","type":"int","value":1}}',
      "condition",
      /no unary operator/,
    ],
    [
      '{"kind":"call","function":"f","args":[{"kind":"lit","type":"int","value":1},{"kind":"lit","type":"int","value":1},{"kind":"lit","type":"int","value":1},{"kind":"lit","type":"int","value":1},{"kind":"lit","type":"int","value":1},{"kind":"lit","type":"int","value":1},{"kind":"lit","type":"int","value":1},{"kind":"lit","type":"int","value":1},{"kind":"lit","type":"int","value":1}]}',
      "condition",
      /more than 8 args/,
    ],
    ['{"kind":"what"}', "condition", /"what" is no kind of node/],
  ])("refuses %s as a %s, as a DecodeError", (text, ctx, problem) => {
    let err: unknown;
    try {
      decode(text, ctx);
    } catch (e) {
      err = e;
    }
    expect(err).toBeInstanceOf(DecodeError);
    expect((err as DecodeError).message).toMatch(problem);
  });

  test("refuses an AST deeper than 64 without recursing deeper", () => {
    const deep = (n: number) => {
      let text = '{"kind":"lit","type":"bool","value":true}';
      for (let i = 0; i < n; i++) {
        text = `{"kind":"unary","op":"not","operand":${text}}`;
      }
      return text;
    };
    expect(() => decode(deep(5_000), "condition")).toThrow(/deeper than 64/);
    expect(() => decode(JSON.parse(deep(5_000)), "condition")).toThrow(
      /deeper than 64/,
    );
    // JSON nested beyond what a reader of JSON reads.
    expect(() => decode(deep(100_000), "condition")).toThrow(
      /more than 10000 deep/,
    );
  });

  test("reads an int written as 2.0, as its type says", () => {
    expect(
      encode(decode('{"kind":"lit","type":"int","value":2.0}', "condition")),
    ).toBe('{"kind":"lit","type":"int","value":2}');
  });

  // Every decoded node's range is NO_RANGE, which is frozen, so that no
  // consumer changes the range of every decoded node by changing one's.
  test("gives each node the range of no source, which nothing changes", () => {
    const n = decode(
      '{"kind":"unary","op":"neg","operand":{"kind":"lit","type":"int","value":2}}',
      "condition",
    );
    expect(n.src).toBe(NO_RANGE);
    expect(() => {
      (n.src as { start: number }).start = 1;
    }).toThrow(TypeError);
    expect(NO_RANGE).toStrictEqual({ start: 0, end: 0 });
  });
});

describe("readJSON", () => {
  // plain gives what readJSON read as JSON.parse reads the text: each key in
  // its order, and __proto__ as a key of the object's own, not its prototype.
  test("is read by plain as JSON.parse reads it", () => {
    const text = '{"b":1,"__proto__":{"x":[2,{"y":3.5}]},"a":"z"}';
    const got = plain(readJSON(text)) as Record<string, unknown>;
    expect(got).toStrictEqual(JSON.parse(text));
    expect(Object.keys(got)).toStrictEqual(["b", "__proto__", "a"]);
    expect(Object.getPrototypeOf(got)).toBe(Object.prototype);
    expect(Object.hasOwn(got, "__proto__")).toBe(true);
  });

  // As encoding/json, which counts each array and object that it opens,
  // empty or not, against its depth of 10,000.
  test("reads arrays and objects nested 10,000 deep, and no deeper", () => {
    const nested = (n: number) => "[".repeat(n) + "]".repeat(n);
    expect(() => readJSON(nested(10_000))).not.toThrow();
    expect(() => readJSON(`{"a":${nested(9_999)}}`)).not.toThrow();
    expect(() => readJSON(nested(10_001))).toThrow(/more than 10000 deep/);
    expect(() => readJSON(`{"a":${nested(10_000)}}`)).toThrow(
      /more than 10000 deep/,
    );
    expect(() =>
      readJSON(`${"[".repeat(10_000)}{}${"]".repeat(10_000)}`),
    ).toThrow(/more than 10000 deep/);
  });
});

describe("readJSONBytes", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  // A browser's TextDecoder reads no view of a resizable ArrayBuffer, as
  // WebIDL's AllowSharedBufferSource takes none, though Node's reads one.
  test("reads bytes over a resizable buffer, as a browser's TextDecoder reads none", () => {
    const Decoder = globalThis.TextDecoder;
    vi.stubGlobal(
      "TextDecoder",
      class extends Decoder {
        override decode(
          ...args: Parameters<InstanceType<typeof Decoder>["decode"]>
        ): string {
          const [input] = args;
          if (
            ArrayBuffer.isView(input) &&
            input.buffer instanceof ArrayBuffer &&
            input.buffer.resizable
          ) {
            throw new TypeError("a view of a resizable ArrayBuffer");
          }
          return super.decode(...args);
        }
      },
    );
    const text = new TextEncoder().encode('{"a":[1,"b"]}');
    const view = new Uint8Array(
      new ArrayBuffer(text.length, { maxByteLength: text.length * 2 }),
    );
    view.set(text);
    expect(plain(readJSONBytes(view))).toStrictEqual({ a: [1, "b"] });
    expect(() => readJSONBytes(new Uint8Array([0xff]))).toThrow(/is not UTF-8/);
  });
});

describe("decimalOf", () => {
  // A run of zeros among a number's digits, which a pattern that matches
  // the zeros at its end, as /0+$/, matches in time quadratic in its
  // length, from each zero of the run, is counted in time linear in it.
  test("reads a number of a hundred thousand digits at once", () => {
    const zeros = "0".repeat(100_000);
    const start = performance.now();
    expect(isIntegral(`1${zeros}1`)).toBe(true);
    expect(isIntegral(`1.${zeros}1`)).toBe(false);
    expect(decimalOf(`-00${zeros}1${zeros}1${zeros}`)).toStrictEqual({
      negative: true,
      digits: `1${zeros}1`,
      exponent: 200_001,
    });
    expect(decimalOf(`+0.0${zeros}`)).toStrictEqual({
      negative: false,
      digits: "",
      exponent: -100_002,
    });
    // Linearly, in about a millisecond; quadratically, in some seconds.
    expect(performance.now() - start).toBeLessThan(1000);
  });
});

describe("parseFloatText", () => {
  // Beyond the corpus's cases: an exponent of more digits than a double
  // holds, either way, and more significant digits than nearest writes,
  // whose rest, which it writes as 1, is above the point halfway between
  // two doubles, or is 0, and leaves the number on it.
  test("reads any exponent, and any number of digits, to the nearest double", () => {
    const many = (d: string, n: number) => d.repeat(n);
    for (const [s, f, error] of [
      ["1e99999999999999999999", 0, "float_not_finite"],
      [`1e${many("1", 400)}`, 0, "float_not_finite"],
      ["-1e-99999999999999999999", 0, undefined],
      [`1e-${many("1", 400)}`, 0, undefined],
      ["0e99999999999999999999", 0, undefined],
      ["+1e+0000000000000000000002", 100, undefined],
      [`0.${many("0", 1000)}1e1001`, 1, undefined],
      [`9007199254740993${many("0", 900)}e-900`, 9007199254740992, undefined],
      [`9007199254740993${many("0", 900)}1e-901`, 9007199254740994, undefined],
    ] as const) {
      expect(parseFloatText(s), s.slice(0, 40)).toEqual([f, error]);
    }
  });
});

describe("readValue", () => {
  // No JSON writes a NaN or an infinity, so neither is a value of any type,
  // which jsonOf would otherwise write as an int.
  test("refuses a number that is not finite", () => {
    for (const n of [
      Number.NaN,
      Number.POSITIVE_INFINITY,
      Number.NEGATIVE_INFINITY,
    ]) {
      for (const typ of ["int", "float"] as const) {
        const [v, problem] = readValue(n, typ);
        expect(v, `${n} as ${typ}`).toEqual(NONE);
        expect(problem, `${n} as ${typ}`).toMatch(
          /not a number that JSON writes/,
        );
      }
      expect(jsonOf(intValue(n), "int")).toBeUndefined();
      expect(jsonOf(floatValue(n), "float")).toBeUndefined();
    }
  });
});

// sameValue compares floats by Object.is, and equalValue as the Go runtime
// compares two values, with ==.
test("sameValue and equalValue compare a float's -0 and a NaN apart", () => {
  const negativeZero = { ...floatValue(1), float: -0 };
  const nan = floatValue(Number.NaN);
  expect(sameValue(negativeZero, floatValue(0))).toBe(false);
  expect(equalValue(negativeZero, floatValue(0))).toBe(true);
  expect(sameValue(nan, nan)).toBe(true);
  expect(equalValue(nan, nan)).toBe(false);
  for (const same of [sameValue, equalValue]) {
    expect(same(intValue(1), floatValue(1))).toBe(false);
    expect(same(stringValue("a"), stringValue("a"))).toBe(true);
  }
});
