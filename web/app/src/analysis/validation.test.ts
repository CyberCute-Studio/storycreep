import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, test } from "vitest";
import { World, withInline } from "../test/analysis";
import { expression, richText, testVariable, text } from "../test/play";
import { readDraft } from "./draft";
import { indexOf } from "./uses";
import {
  type AnalysisFinding,
  type Code,
  compareFindings,
  rules,
  validate,
} from "./validation";

// findings returns the findings of the world w.
function findings(w: World): AnalysisFinding[] {
  const { draft, index } = w.analyse();
  return validate(draft, index);
}

// found returns the IDs of the entities of the findings of the code in w.
function found(w: World, code: Code): string[] {
  return findings(w)
    .filter((f) => f.code === code)
    .map((f) => f.entity_id);
}

/** A rule's fixtures: a change of a clean world that the rule finds something in, the entity that it finds it of, and one that it finds nothing in, however near it is, as the server's rules' are (rules_test.go). */
interface RuleCase {
  positive: (w: World) => string;
  negative: (w: World) => void;
}

const label = (s: string) => richText(text(s));

const ruleCases: Record<Code, RuleCase> = {
  expr_invalid: {
    positive: (w) =>
      w.connect(w.start, w.end, { condition: expression("nope") }),
    negative: (w) => {
      w.connect(w.start, w.end, { condition: expression("true") });
    },
  },
  ref_dangling: {
    positive: (w) => {
      const start = w.nodeOf(w.start);
      start.text = withInline(start.text, {
        type: "valueRef",
        attrs: { ref: w.id("var") },
      });
      return w.start;
    },
    negative: (w) => {
      const v = w.variable(false, true);
      const start = w.nodeOf(w.start);
      start.text = withInline(start.text, {
        type: "valueRef",
        attrs: { ref: v },
      });
    },
  },
  jumper_broken: {
    positive: (w) => {
      const j = w.jumper(w.main, w.id("nd"));
      w.connect(w.start, j);
      return j;
    },
    negative: (w) => {
      w.connect(w.start, w.jumper(w.main, w.end));
    },
  },
  jumper_invalid: {
    positive: (w) => {
      const last = w.jumper(w.main, w.end);
      const first = w.jumper(w.main, last);
      w.connect(w.start, first);
      return first;
    },
    negative: (w) => {
      const last = w.jumper(w.main, w.end);
      w.connect(w.start, last);
      w.connect(w.start, w.jumper(w.main, w.end));
    },
  },
  branch_no_outputs: {
    positive: (w) => {
      const b = w.branch(w.main);
      w.connect(w.start, b);
      return b;
    },
    negative: (w) => {
      const b = w.branch(w.main);
      w.connect(w.start, b);
      w.connectOutput(b, w.output(b), w.end);
    },
  },
  port_unconnected: {
    positive: (w) => {
      const b = w.branch(w.main);
      w.connect(w.start, b);
      w.connectOutput(b, w.output(b), w.end);
      return w.output(b);
    },
    negative: (w) => {
      const b = w.branch(w.main);
      w.connect(w.start, b);
      w.connectOutput(b, w.output(b), w.end);
      w.connectOutput(b, w.output(b), w.end);
    },
  },
  var_read_never_written: {
    positive: (w) => w.variable(true, false),
    negative: (w) => {
      w.variable(true, true);
    },
  },
  var_written_never_read: {
    positive: (w) => w.variable(false, true),
    negative: (w) => {
      w.variable(true, true);
    },
  },
  node_unreachable: {
    // A root of a board with an entry node.
    positive: (w) => w.element(w.main, { is_ending: true }),
    negative: (w) => {
      // A node that a jumper reaches, and the roots of a board without an
      // entry node, each of which is where the flow may start.
      const other = w.board();
      const target = w.element(other, { is_ending: true });
      w.connect(w.start, w.jumper(w.main, target));
      w.element(other, { is_ending: true });
    },
  },
  node_empty: {
    positive: (w) => {
      const n = w.element(w.main, { text: richText(), is_ending: true });
      w.connect(w.start, n);
      return n;
    },
    negative: (w) => {
      const n = w.element(w.main, { text: richText() });
      w.connect(w.start, n);
      w.connect(n, w.end);
      // Text that holds an empty text node is no empty paragraph.
      const m = w.element(w.main, { text: label(""), is_ending: true });
      w.connect(w.start, m);
    },
  },
  choice_duplicate_label: {
    positive: (w) => {
      const x = w.variable(true, true);
      const key = w.variables.find((v) => v.id === x)?.key;
      w.connect(w.start, w.end, {
        label: label("Go"),
        condition: expression(`${key} > 1`),
      });
      return w.connect(w.start, w.end, {
        label: label("Go"),
        condition: expression(`${key} > 2`),
      });
    },
    negative: (w) => {
      // The same label and exclusive conditions, different labels, and an
      // ending's connections, which are no choices.
      const x = w.variable(true, true);
      const key = w.variables.find((v) => v.id === x)?.key;
      w.connect(w.start, w.end, {
        label: label("Go"),
        condition: expression(`${key} > 1`),
      });
      w.connect(w.start, w.end, {
        label: label("Go"),
        condition: expression(`!(${key} > 1)`),
      });
      w.connect(w.start, w.end, { label: label("Stay") });
      w.connect(w.end, w.start, { label: label("Again") });
      w.connect(w.end, w.start, { label: label("Again") });
    },
  },
  dead_end: {
    positive: (w) => {
      const n = w.element(w.main);
      w.connect(w.start, n);
      return n;
    },
    negative: (w) => {
      const n = w.element(w.main, { is_ending: true });
      w.connect(w.start, n);
      // A jumper is never a dead end, nor a branch an element.
      w.connect(w.start, w.jumper(w.main, w.end));
    },
  },
  speaker_missing: {
    positive: (w) => {
      const n = w.element(w.main, { kind: "dialogue", is_ending: true });
      w.connect(w.start, n);
      return n;
    },
    negative: (w) => {
      const cast = w.componentType("cast", []);
      const speaker = w.component(cast.id, "elara");
      const n = w.element(w.main, {
        kind: "dialogue",
        speaker_component_id: speaker,
        is_ending: true,
      });
      w.connect(w.start, n);
    },
  },
  board_entry_missing: {
    positive: (w) => {
      const b = w.board();
      w.element(b, { is_ending: true });
      w.element(b, { is_ending: true });
      return b;
    },
    negative: (w) => {
      const b = w.board();
      const a = w.element(b);
      w.connect(a, w.element(b, { is_ending: true }));
    },
  },
  connection_self: {
    positive: (w) => w.connect(w.start, w.start),
    negative: (w) => {
      w.connect(w.end, w.start);
    },
  },
  connection_parallel: {
    positive: (w) => w.connect(w.start, w.end, { label: label("Again") }),
    negative: (w) => {
      const other = w.element(w.main, { is_ending: true });
      w.connect(w.start, other);
    },
  },
};

test("the rules are the server's, in its order", () => {
  // The API's codes of a finding, in the order of the server's rules, and
  // their severities, which the OpenAPI document writes of them: a
  // warning's code is one that a publication acknowledges, an info's one
  // that a mute silences and no publication acknowledges, and an error's
  // one that neither names.
  type Codes = { properties: { code: { enum: string[] } } };
  const api = JSON.parse(
    readFileSync(
      join(import.meta.dirname, "../../../../schema/api/v1/openapi.json"),
      "utf8",
    ),
  ) as {
    components: {
      schemas: {
        Finding: Codes;
        Acknowledgement: Codes;
        ValidationMutePayload: Codes;
      };
    };
  };
  const { Finding, Acknowledgement, ValidationMutePayload } =
    api.components.schemas;
  const warnings = new Set(Acknowledgement.properties.code.enum);
  const mutable = new Set(ValidationMutePayload.properties.code.enum);
  expect(rules.map((r) => [r.code, r.severity])).toEqual(
    Finding.properties.code.enum.map((code) => [
      code,
      warnings.has(code) ? "warning" : mutable.has(code) ? "info" : "error",
    ]),
  );
});

// Verifies: FR-DEP-03/AC1
test("the clean world has no finding", () => {
  expect(findings(new World())).toEqual([]);
});

describe.each(rules.map((r) => [r.code]))("the rule %s", (code) => {
  // Verifies: FR-DEP-03/AC1
  test("finds the entity of its positive fixture, once", () => {
    const w = new World();
    const want = ruleCases[code].positive(w);
    expect(found(w, code)).toEqual([want]);
  });

  // Verifies: FR-DEP-03/AC1
  test("finds nothing in its negative fixture", () => {
    const w = new World();
    ruleCases[code].negative(w);
    expect(found(w, code)).toEqual([]);
  });
});

describe("a finding", () => {
  test("names what a client opens it by, and what it repeats", () => {
    const w = new World();
    const b = w.branch(w.main, { title: "Fork" });
    w.connect(w.start, b);
    w.connectOutput(b, w.output(b), w.end);
    const o = w.output(b);
    const again = w.connect(w.start, w.end, { label: label("Again") });
    const v = w.variable(true, false);
    const all = findings(w);
    const board = w.boardOf(w.main);
    expect(all.find((f) => f.code === "port_unconnected")).toEqual({
      code: "port_unconnected",
      severity: "warning",
      entity_type: "branch_output",
      entity_id: o,
      board_id: w.main,
      board_name: board.name,
      node_id: b,
      node_title: "Fork",
      args: {},
    });
    expect(all.find((f) => f.code === "connection_parallel")).toEqual({
      code: "connection_parallel",
      severity: "warning",
      entity_type: "connection",
      entity_id: again,
      board_id: w.main,
      board_name: board.name,
      node_id: w.start,
      node_title: "Start",
      target: { type: "connection", id: w.connections[0].id },
      args: {},
    });
    // A global variable has no board, and its key is its message's.
    expect(all.find((f) => f.code === "var_read_never_written")).toEqual({
      code: "var_read_never_written",
      severity: "warning",
      entity_type: "variable",
      entity_id: v,
      args: { key: w.variables[0].key },
    });
  });

  test("of an expression names its field and its error", () => {
    const w = new World();
    const start = w.nodeOf(w.start);
    start.on_exit = expression("x = ");
    const b = w.branch(w.main);
    w.connect(w.start, b);
    const o = w.output(b, "");
    w.connectOutput(b, o, w.end);
    const invalid = findings(w).filter((f) => f.code === "expr_invalid");
    expect(invalid.map((f) => [f.entity_id, f.field])).toEqual([
      [w.start, "on_exit"],
      [o, "condition"],
    ]);
    for (const f of invalid) {
      expect(typeof f.args.error).toBe("string");
      expect(f.args.error).not.toBe("");
    }
  });

  test("is in the order of the server's report", () => {
    const w = new World();
    // An error, warnings of two rules, and an info, each added in an order
    // that is not the report's.
    w.element(w.main, { kind: "dialogue", is_ending: true });
    w.variable(true, false);
    w.connect(w.start, w.start, { label: label("Loop") });
    w.connect(w.start, w.end, {
      label: label("Nope"),
      condition: expression("nope"),
    });
    const all = findings(w);
    expect(all.map((f) => f.code)).toEqual([
      "expr_invalid",
      "var_read_never_written",
      "node_unreachable",
      "connection_self",
      "connection_parallel",
      "speaker_missing",
    ]);
    expect([...all].sort(compareFindings)).toEqual(all);
  });
});

// Verifies: FR-DEP-03/AC1, FR-TEXT-02/AC2
test("a dangling reference is found once for each thing that a field refers to", () => {
  const w = new World();
  const other = w.board("Other");
  const cast = w.componentType("cast", [
    { key: "trust", type: "int", runtime: true },
    { key: "bio", type: "text", runtime: false },
  ]);
  const elsewhere = w.componentType("prop", [
    { key: "weight", type: "int", runtime: true },
  ]);
  const elara = w.component(cast.id, "elara");
  const [trust, bio] = cast.attributes;
  const tide = w.variable(true, true, { scope: "board", board_id: w.main });
  const fog = w.id("var");
  w.variables.push(
    testVariable(fog, "fog", "int", 0, { scope: "board", board_id: other }),
  );
  const gone = w.id("var");
  const start = w.nodeOf(w.start);
  const refs = [
    { type: "valueRef", attrs: { ref: gone } },
    { type: "valueRef", attrs: { ref: gone } },
    { type: "valueRef", attrs: { ref: tide } },
    { type: "valueRef", attrs: { ref: fog } },
    { type: "valueRef", attrs: { ref: elara, attribute: trust } },
    { type: "valueRef", attrs: { ref: elara, attribute: bio } },
    {
      type: "valueRef",
      attrs: { ref: elara, attribute: elsewhere.attributes[0] },
    },
    { type: "mention", attrs: { id: elara } },
    { type: "mention", attrs: { id: w.id("cmp") } },
  ] as const;
  for (const r of refs) {
    start.text = withInline(start.text, r);
  }
  // A description, which no runtime shows, may show any variable, and an
  // attribute that is not runtime.
  w.component(
    cast.id,
    "mira",
    richText(
      { type: "valueRef", attrs: { ref: fog } },
      { type: "valueRef", attrs: { ref: elara, attribute: bio } },
      { type: "mention", attrs: { id: w.id("cmp") } },
    ),
  );
  w.nodeOf(w.end).speaker_component_id = w.id("cmp");
  w.nodeOf(w.end).kind = "dialogue";
  w.setEntry(other, w.id("nd"));
  w.entry = { board_id: w.id("bd"), node_id: w.id("nd") };
  const dangling = findings(w)
    .filter((f) => f.code === "ref_dangling")
    .map((f) => [f.entity_type, f.field, f.target?.type, f.args.reason]);
  expect(dangling.sort()).toEqual(
    [
      ["node", "text", "variable", "deleted"],
      ["node", "text", "variable", "scope"],
      ["node", "text", "attribute", "runtime"],
      ["node", "text", "attribute", "deleted"],
      ["node", "text", "component", "deleted"],
      ["component", "description", "component", "deleted"],
      ["node", "speaker_component_id", "component", "deleted"],
      ["board", "entry_node_id", "node", "deleted"],
      ["project", "entry", "board", "deleted"],
      ["project", "entry", "node", "deleted"],
    ].sort(),
  );
});

describe("the draft", () => {
  test("leaves out what is in the trash, with what went with it", () => {
    const w = new World();
    const other = w.board("Other", { deleted_at: "2026-11-02T10:00:00Z" });
    // A node of a board in the trash, which its board's deletion took with
    // it, though the document has not marked it.
    w.element(other);
    // A node in the trash, whose connections went with it.
    const gone = w.element(w.main, { deleted_at: "2026-11-02T10:00:00Z" });
    w.connect(w.start, gone);
    const b = w.branch(w.main);
    w.connect(w.start, b);
    const o = w.output(b);
    w.connectOutput(b, o, w.end);
    const n = w.nodeOf(b);
    n.outputs = n.outputs?.map((x) =>
      x.id === o ? { ...x, deleted_at: "2026-11-02T10:00:00Z" } : x,
    );
    const { draft } = w.analyse();
    expect([...draft.boards.keys()]).toEqual([w.main]);
    expect([...draft.nodes.keys()]).toEqual([w.start, w.end, b]);
    expect(
      draft.connections.map((c) => [c.from_node_id, c.to_node_id]),
    ).toEqual([
      [w.start, w.end],
      [w.start, b],
    ]);
    // The branch has no output, and no connection leaves it.
    expect(found(w, "branch_no_outputs")).toEqual([b]);
    expect(found(w, "connection_parallel")).toEqual([]);
  });

  test("orders the connections by their ports", () => {
    const w = new World();
    const b = w.branch(w.main);
    w.connect(w.start, b, { order: "b" });
    const [x, y] = [w.output(b), w.output(b)];
    const late = w.connectOutput(b, y, w.end);
    const early = w.connectOutput(b, x, w.end);
    const first = w.connect(w.start, w.end, { order: "0" });
    const { draft } = w.analyse();
    // The start's out, in its order, and then the branch's outputs.
    expect(draft.connections.map((c) => c.id)).toEqual([
      first,
      w.connections[1].id,
      w.connections[0].id,
      early,
      late,
    ]);
  });

  test("compiles each expression from its source, against the draft", () => {
    const w = new World();
    const v = w.variable(false, false);
    const key = w.variables[0].key;
    // The document holds the AST of an earlier source, as an expression
    // that the page changed does until the server answers.
    const c = w.connect(w.start, w.end, {
      condition: { source: `${key} > 1`, is_valid: false },
    });
    const { draft, index } = w.analyse();
    const e = draft.expressions.find((x) => x.id === c);
    expect(e?.ast?.kind).toBe("binary");
    expect(index.sites.flatMap((s) => s.uses.map((u) => u.target))).toContain(
      v,
    );
  });

  test("is not read of a document that has not read a component whole", () => {
    const w = new World();
    const cast = w.componentType("cast", []);
    w.component(cast.id, "elara", richText(), { attributes: undefined });
    expect(() => w.analyse()).toThrow(/has not been read whole/);
  });

  test("is not read of a document that has not read the project", () => {
    expect(() =>
      readDraft({
        seq: 0,
        project: undefined,
        boards: new Map(),
        nodes: new Map(),
        connections: new Map(),
        variables: new Map(),
        componentTypes: new Map(),
        components: new Map(),
      }),
    ).toThrow(/has not been read/);
  });
});

test("the rule of duplicate choices compares the choices of one element alone", () => {
  // The choices of another element, a branch's outputs' connections, and
  // the connections of an ending or of a jumper, whatever their labels, are
  // never compared; two choices of one label are.
  const w = new World();
  const other = w.element(w.main, { is_ending: true });
  w.connect(w.start, w.end, { label: label("Go") });
  const second = w.connect(w.start, other, { label: label("Go") });
  w.connect(w.start, w.end, { label: label("Stay") });
  w.connect(other, w.end, { label: label("Go") });
  const b = w.branch(w.main);
  w.connect(w.start, b, { label: label("Branch") });
  w.connectOutput(b, w.output(b), w.end);
  w.connectOutput(b, w.output(b), w.end);
  w.connect(w.end, w.start, { label: label("Again") });
  w.connect(w.end, w.start, { label: label("Again") });
  expect(found(w, "choice_duplicate_label")).toEqual([second]);
  const { draft } = w.analyse();
  expect(validate(draft, indexOf(draft))).toEqual(findings(w));
});

// Verifies: FR-DEP-03/AC1
test("the flow may start at a board's entry, a root, or the project's entry, and goes on by connections and jumpers", () => {
  // As the server's TestReachability: at a board's entry node, or at every
  // root of a board without one in the draft, and at the project's entry
  // node; by every connection, whatever its condition, but an ending's,
  // where the flow ends, and a jumper's, which it leaves for the jumper's
  // target at once; and from a jumper to its target.
  const w = new World();
  // A board whose entry node is not in the draft: its roots are where the
  // flow may start.
  const other = w.board("Other");
  w.setEntry(other, w.id("nd"));
  const root = w.element(other);
  w.connect(root, w.element(other, { is_ending: true }));
  // A root of main, which has an entry node, reached only by the project's
  // entry, and one reached by a jumper of the other board.
  const byEntry = w.element(w.main, { is_ending: true });
  w.entry = { board_id: w.main, node_id: byEntry };
  const byJumper = w.element(w.main, { is_ending: true });
  const j = w.jumper(other, byJumper);
  w.connect(root, j);
  // A root of main that nothing reaches, which leads on to another.
  const lost = w.element(w.main);
  const after = w.element(w.main, { is_ending: true });
  w.connect(lost, after);
  // A node that only the ending end leads to, and one that only the jumper
  // does, neither of which the flow takes.
  const beyond = w.element(w.main, { is_ending: true });
  w.connect(w.end, beyond);
  const past = w.element(other, { is_ending: true });
  w.connect(j, past);
  expect(found(w, "node_unreachable")).toEqual([lost, after, beyond, past]);
});
