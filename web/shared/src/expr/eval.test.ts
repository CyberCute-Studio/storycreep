import { readFileSync } from "node:fs";
import { describe, expect, test } from "vitest";
import { type BinaryOp, NO_RANGE, type Node } from "./ast";
import { BUILTINS, builtins } from "./builtins";
import { compile } from "./check";
import { decode } from "./decode";
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
import { isJsonObject, type JsonValue, readJSON } from "./json";
import { Project } from "./project";
import {
  type DeclaredType,
  floatValue,
  intValue,
  NONE,
  readValue,
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

  test.each([
    ['{"kind":"lit","type":"int","value":2.5}', "condition"],
    ['{"kind":"lit","type":"int","value":4503599627370495.5}', "condition"],
    ['{"kind":"lit","type":"int","value":9007199254740992}', "condition"],
    [
      '{"kind":"lit","type":"float","value":1.7976931348623158e308}',
      "condition",
    ],
    ['{"kind":"lit","type":"float","value":0e1000}', "condition"],
    ['{"kind":"lit","type":"string","value":"\\ud800"}', "condition"],
    ['{"kind":"lit","type":"string","value":"\\u0000"}', "condition"],
    ['{"kind":"lit","type":"bool","value":null}', "condition"],
    ['{"kind":"lit","type":"bool","value":true}', "script"],
    ['{"kind":"script","statements":[]}', "condition"],
    [
      '{"kind":"script","statements":[{"kind":"lit","type":"bool","value":true}]}',
      "script",
    ],
    [
      '{"kind":"var","name":"x","ref":"var_01hq8zv01k7mwvnd2cqj4rt9xb"}',
      "condition",
    ],
    [
      '{"kind":"var","name":"1x","ref":"var_01HQ8ZV01K7MWVND2CQJ4RT9XB"}',
      "condition",
    ],
    [
      '{"kind":"attr","name":"a","ref":"cmp_01HQ8ZC01K7MWVND2CQJ4RT9XB","attribute":"atr_01HQ8ZR01K7MWVND2CQJ4RT9XB"}',
      "condition",
    ],
    [
      '{"kind":"unary","op":"inc","operand":{"kind":"lit","type":"int","value":1}}',
      "condition",
    ],
    ['{"kind":"call","function":"f","args":[1,2,3,4,5,6,7,8,9]}', "condition"],
    ['{"kind":"what"}', "condition"],
  ])("refuses %s as a %s", (text, ctx) => {
    expect(() => decode(text, ctx as "condition" | "script")).toThrow();
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
});
