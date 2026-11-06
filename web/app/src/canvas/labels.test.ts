import { describe, expect, it } from "vitest";
import { testNode } from "../test/canvas";
import { passOf, pathAt, statsOf } from "./bench";
import { keyAction } from "./keyboard";
import { type LabelContext, labelsOf, plainText } from "./labels";
import { clip, textTier, wrapLines } from "./text";

const context: LabelContext = {
  t: (key, args) =>
    args === undefined ? key : `${key}(${Object.values(args).join(",")})`,
  componentName: (id) => (id === "cmp_ada" ? "Ada" : undefined),
  variableKey: (id) => (id === "var_gold" ? "gold" : undefined),
  nodeTitle: (id) => (id === "nd_dock" ? "The dock" : undefined),
};

describe("a card's labels", () => {
  it("shows an element's kind, speaker, and text as plain words", () => {
    const node = testNode("nd_1", 0, 0, {
      title: "",
      kind: "dialogue",
      speaker_component_id: "cmp_ada",
      is_ending: true,
      text: {
        type: "doc",
        content: [
          {
            type: "paragraph",
            content: [
              { type: "text", text: "You have " },
              { type: "valueRef", attrs: { ref: "var_gold" } },
              { type: "text", text: " coins, " },
              { type: "mention", attrs: { id: "cmp_ada" } },
            ],
          },
          {
            type: "bulletList",
            content: [
              {
                type: "listItem",
                content: [
                  {
                    type: "paragraph",
                    content: [{ type: "text", text: "one" }],
                  },
                ],
              },
            ],
          },
        ],
      },
    });
    expect(labelsOf(node, context)).toEqual({
      title: "canvas.node.untitled",
      kind: "canvas.node.kind_ending(canvas.node.dialogue_by(Ada))",
      body: "You have {gold} coins, @Ada\none",
    });
  });

  it("shows a branch's outputs, one to a line, and a jumper's target", () => {
    const branch = testNode("nd_b", 0, 0, {
      type: "branch",
      outputs: [
        {
          id: "bo_1",
          node_id: "nd_b",
          kind: "if",
          condition: { source: "gold >\n 3", is_valid: true },
          order: "V",
          rev: 1,
          created_at: "",
          updated_at: "",
        },
        {
          id: "bo_2",
          node_id: "nd_b",
          kind: "else",
          order: "W",
          rev: 1,
          created_at: "",
          updated_at: "",
        },
      ],
    });
    expect(labelsOf(branch, context).body).toBe(
      "canvas.node.output_if(gold > 3)\ncanvas.node.output_else",
    );
    const jumper = testNode("nd_j", 0, 0, {
      type: "jumper",
      target: { board_id: "bd_a", node_id: "nd_dock" },
    });
    expect(labelsOf(jumper, context).body).toBe(
      "canvas.node.jumper_to(The dock)",
    );
  });

  it("cuts a long text short", () => {
    const text = plainText(
      {
        type: "doc",
        content: [
          {
            type: "paragraph",
            content: [{ type: "text", text: "a".repeat(500) }],
          },
        ],
      },
      context,
    );
    expect(text.length).toBe(240);
    expect(text.endsWith("…")).toBe(true);
  });
});

describe("a card's text", () => {
  it("cuts a line to the card's width", () => {
    expect(clip("short", 10)).toBe("short");
    expect(clip("a longer title", 8)).toBe("a longe…");
    expect(clip("two\nlines", 10)).toBe("two…");
  });

  it("wraps a text into the card's lines, at spaces", () => {
    expect(wrapLines("the quick brown fox jumps", 10, 2, false)).toBe(
      "the quick\nbrown fox…",
    );
    expect(wrapLines("one\ntwo\nthree", 10, 2, true)).toBe("one\ntwo");
    expect(wrapLines("anything", 10, 0, false)).toBe("");
  });

  it("draws the glyphs sharper as the zoom grows", () => {
    expect(textTier(1)).toBe(1);
    expect(textTier(3)).toBe(2);
    expect(textTier(8)).toBe(4);
  });
});

describe("the canvas's keys", () => {
  it("fits with Shift+1 and zooms to the selection with Shift+2, on any layout", () => {
    const key = {
      ctrlKey: false,
      metaKey: false,
      altKey: false,
      shiftKey: true,
    };
    expect(keyAction({ ...key, key: "!", code: "Digit1" })).toEqual({
      kind: "fit",
    });
    expect(keyAction({ ...key, key: '"', code: "Digit2" })).toEqual({
      kind: "selection",
    });
    expect(keyAction({ ...key, key: "Escape", code: "Escape" })).toEqual({
      kind: "deselect",
    });
  });
});

describe("the benchmark's path", () => {
  it("crosses the area at a steady speed, and swings the zoom for panzoom", () => {
    const area = { x: 0, y: 0, width: 9000, height: 1000 };
    const size = { width: 800, height: 600 };
    const a = pathAt("pan", 1, area, size);
    const b = pathAt("pan", 2, area, size);
    expect(a.zoom).toBe(1);
    expect(b.x - a.x).toBeCloseTo(900);
    expect(pathAt("panzoom", 0, area, size).zoom).toBeCloseTo(0.25);
    expect(pathAt("panzoom", 3, area, size).zoom).toBeCloseTo(1.5);
  });

  it("warms up for a pass of the area, row after row a canvas apart", () => {
    expect(
      passOf({ x: 0, y: 0, width: 9000, height: 1000 }, { height: 600 }),
    ).toBe(30);
  });

  it("summarises its frames", () => {
    expect(statsOf([4, 1, 3, 2])).toEqual({
      mean: 2.5,
      p50: 2,
      p95: 4,
      max: 4,
    });
  });
});
