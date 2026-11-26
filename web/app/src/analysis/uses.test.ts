import type { Usage } from "@storycreep/shared/api";
import { compile, Project } from "@storycreep/shared/expr/compile";
import { describe, expect, test } from "vitest";
import { World, withInline } from "../test/analysis";
import { expression, richText, text } from "../test/play";
import {
  componentUsages,
  referencesOf,
  textUsesOf,
  usageOrder,
  usesOf,
  variableUsages,
} from "./uses";

const gold = "var_00000000000000000000000001";
const seen = "var_00000000000000000000000002";
const elara = "cmp_00000000000000000000000003";
const trust = "atr_00000000000000000000000004";
const node = "nd_00000000000000000000000005";
const names = Project.of(
  [
    { id: gold, key: "gold", type: "int" },
    { id: seen, key: "seen", type: "bool" },
  ],
  [
    {
      id: elara,
      key: "elara",
      attributes: [{ id: trust, key: "trust", type: "int", runtime: true }],
    },
  ],
  [{ id: node }],
);

describe("usesOf", () => {
  test("a condition reads every name that it holds, each once", () => {
    const n = compile(
      `gold > 1 and elara.trust < gold and visited(node("${node}"))`,
      "condition",
      names,
      "",
    );
    expect(usesOf(n)).toEqual([
      { kind: "attribute", target: elara, attribute: trust, access: "read" },
      { kind: "node", target: node, access: "read" },
      { kind: "variable", target: gold, access: "read" },
    ]);
  });

  test("a script writes what it assigns, and reads what it updates", () => {
    const n = compile(
      'gold = 1; elara.trust += 2; signal("x")',
      "script",
      names,
      "",
    );
    expect(usesOf(n)).toEqual([
      { kind: "attribute", target: elara, attribute: trust, access: "read" },
      { kind: "attribute", target: elara, attribute: trust, access: "write" },
      { kind: "variable", target: gold, access: "write" },
    ]);
    // The value assigned, and a call's arguments, are read.
    expect(usesOf(compile("seen = gold > 2", "script", names, ""))).toEqual([
      { kind: "variable", target: gold, access: "read" },
      { kind: "variable", target: seen, access: "write" },
    ]);
  });
});

describe("textUsesOf", () => {
  test("a text reads its value references and the components that it mentions, each once", () => {
    const doc = richText(
      text("Hi "),
      { type: "valueRef", attrs: { ref: gold } },
      { type: "valueRef", attrs: { ref: gold, decimals: 2 } },
      { type: "valueRef", attrs: { ref: elara, attribute: trust } },
      { type: "mention", attrs: { id: elara } },
      {
        type: "memberMention",
        attrs: { id: "usr_00000000000000000000000006" },
      },
    );
    expect(textUsesOf(doc)).toEqual([
      { kind: "attribute", target: elara, attribute: trust, access: "read" },
      { kind: "component", target: elara, access: "read" },
      { kind: "variable", target: gold, access: "read" },
    ]);
    expect(referencesOf(doc)).toHaveLength(4);
  });

  test("a list's items are read too", () => {
    const doc = {
      type: "doc",
      content: [
        {
          type: "bulletList",
          content: [
            {
              type: "listItem",
              content: [
                {
                  type: "paragraph",
                  content: [{ type: "mention", attrs: { id: elara } }],
                },
              ],
            },
          ],
        },
      ],
    };
    expect(textUsesOf(doc)).toEqual([
      { kind: "component", target: elara, access: "read" },
    ]);
  });
});

describe("the index", () => {
  test("holds each site that names something, and none that does not compile", () => {
    const w = new World();
    const cast = w.componentType("cast", [
      { key: "trust", type: "int", runtime: true },
    ]);
    const c = w.component(cast.id, "elara", richText(text("Elara")));
    const v = w.variable(false, false);
    const key = w.variables[0].key;
    const start = w.nodeOf(w.start);
    start.on_enter = expression(`${key} += 1`);
    start.on_exit = expression("nope = 1");
    start.text = withInline(start.text, { type: "mention", attrs: { id: c } });
    const b = w.branch(w.main);
    w.connect(w.start, b, {
      label: richText({ type: "valueRef", attrs: { ref: v } }),
      condition: expression(`elara.trust > ${key}`),
    });
    const o = w.output(b, `${key} > 2`);
    w.connectOutput(b, o, w.end);
    // A description that names nothing is no site.
    w.component(cast.id, "mira", richText(text("Mira")));
    w.component(
      cast.id,
      "ana",
      richText({ type: "valueRef", attrs: { ref: v } }),
    );
    const { index } = w.analyse();
    expect(
      index.sites.map((s) => [s.type, s.owner, s.board, s.node, s.uses.length]),
    ).toEqual([
      ["node_on_enter", w.start, w.main, w.start, 2],
      ["branch_output_condition", o, w.main, b, 1],
      ["connection_condition", w.connections[1].id, w.main, w.start, 2],
      ["node_text", w.start, w.main, w.start, 1],
      ["connection_label", w.connections[1].id, w.main, w.start, 1],
      ["component_description", w.components[2].id, undefined, undefined, 1],
    ]);
  });

  test("answers the usages of a variable and of a component", () => {
    const w = new World();
    const cast = w.componentType("cast", [
      { key: "trust", type: "int", runtime: true },
      { key: "mood", type: "int", runtime: true },
    ]);
    const [trustDef, moodDef] = cast.attributes;
    const c = w.component(cast.id, "elara");
    // end's text shows v, and end's on-enter script assigns it.
    const v = w.variable(true, true);
    const start = w.nodeOf(w.start);
    start.speaker_component_id = c;
    start.kind = "dialogue";
    start.on_enter = expression("elara.trust = elara.mood");
    start.text = withInline(start.text, { type: "mention", attrs: { id: c } });
    const { draft, index } = w.analyse();
    expect(variableUsages(draft, index, v, "write")).toEqual([
      {
        site: "node_on_enter",
        board_id: w.main,
        board_name: w.boardOf(w.main).name,
        node_id: w.end,
        node_title: "",
      },
    ]);
    expect(variableUsages(draft, index, v, "read")?.map((u) => u.site)).toEqual(
      ["node_text"],
    );
    expect(componentUsages(draft, index, c, "speaker")).toEqual([
      {
        site: "node_speaker",
        board_id: w.main,
        board_name: w.boardOf(w.main).name,
        node_id: w.start,
        node_title: "Start",
      },
    ]);
    expect(
      componentUsages(draft, index, c, "mention")?.map((u) => u.site),
    ).toEqual(["node_text"]);
    expect(
      componentUsages(draft, index, c, "write")?.map((u) => u.attribute_id),
    ).toEqual([trustDef]);
    expect(
      componentUsages(draft, index, c, "read")?.map((u) => u.attribute_id),
    ).toEqual([moodDef]);
  });

  test("answers none of a variable or a component that the draft does not hold", () => {
    const w = new World();
    const gone = "2026-11-02T10:00:00Z";
    const cast = w.componentType("cast", [
      { key: "trust", type: "int", runtime: true },
    ]);
    const c = w.component(cast.id, "ghost", richText(), { deleted_at: gone });
    // end's text shows v, and start's text mentions c, whose speaker it
    // is, as they do once a forced deletion has taken each to the trash.
    const v = w.variable(true, false, { deleted_at: gone });
    const start = w.nodeOf(w.start);
    start.kind = "dialogue";
    start.speaker_component_id = c;
    start.text = withInline(start.text, { type: "mention", attrs: { id: c } });
    const { draft, index } = w.analyse();
    expect(index.sites.flatMap((s) => s.uses.map((u) => u.target))).toEqual(
      expect.arrayContaining([v, c]),
    );
    for (const access of ["read", "write"] as const) {
      expect(variableUsages(draft, index, v, access)).toBeUndefined();
    }
    for (const use of ["speaker", "mention", "read", "write"] as const) {
      expect(componentUsages(draft, index, c, use)).toBeUndefined();
    }
  });

  test("orders usages by their boards, nodes, sites, and attributes", () => {
    const usages: Usage[] = [
      {
        site: "connection_label",
        board_id: "bd_2",
        node_id: "nd_1",
        connection_id: "cn_1",
      },
      { site: "node_text", board_id: "bd_2", node_id: "nd_1" },
      { site: "node_on_enter", board_id: "bd_2", node_id: "nd_1" },
      { site: "node_text", board_id: "bd_1", node_id: "nd_9" },
      { site: "component_description", component_id: "cmp_1" },
    ];
    expect(
      [...usages].sort(usageOrder).map((u) => `${u.site} ${u.board_id ?? ""}`),
    ).toEqual([
      "component_description ",
      "node_text bd_1",
      "node_on_enter bd_2",
      "node_text bd_2",
      "connection_label bd_2",
    ]);
  });
});
