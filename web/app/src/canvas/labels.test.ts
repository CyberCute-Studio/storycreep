import type { Node, RichTextDoc } from "@storycreep/shared/api";
import { contrast } from "@storycreep/shared/contrast";
import { describe, expect, it } from "vitest";
import { testNode } from "../test/canvas";
import { passOf, pathAt, statsOf } from "./bench";
import { canvasKey } from "./keyboard";
import {
  type LabelContext,
  labelsOf,
  namesDiffer,
  plainText,
  sameLabels,
} from "./labels";
import { nodeRect } from "./layout";
import { targetOf } from "./targets";
import { clip, graphemes, textTier, wrapLines } from "./text";
import {
  nodeColor,
  parseColor,
  readTheme,
  sameTheme,
  type Theme,
} from "./theme";

const context: LabelContext = {
  t: (key, args) =>
    args === undefined ? key : `${key}(${Object.values(args).join(",")})`,
  componentName: (id) => (id === "cmp_ada" ? "Ada" : undefined),
  variableKey: (id) => (id === "var_gold" ? "gold" : undefined),
  target: (node) =>
    targetOf(
      node,
      {
        nodes: new Map(
          [
            testNode("nd_dock", 0, 0, { title: "The dock" }),
            testNode("nd_blank", 0, 0, { title: "" }),
          ].map((n) => [n.id, n]),
        ),
        boards: new Map(),
      },
      undefined,
    ),
  boardName: (id) =>
    id === "bd_a" ? "Harbour" : id === "bd_b" ? "Lighthouse" : undefined,
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
    // A target without a title is untitled, as its own card says; one that
    // the document does not have, as of a board that the page has not
    // read, is named by its board; and one of a board that the project's
    // tree does not have either by its ID.
    const untitled = testNode("nd_u", 0, 0, {
      type: "jumper",
      target: { board_id: "bd_a", node_id: "nd_blank" },
    });
    expect(labelsOf(untitled, context).body).toBe(
      "canvas.node.jumper_to(canvas.node.untitled)",
    );
    const unread = testNode("nd_r", 0, 0, {
      type: "jumper",
      target: { board_id: "bd_b", node_id: "nd_far" },
    });
    expect(labelsOf(unread, context).body).toBe(
      "canvas.node.jumper_to_board(Lighthouse)",
    );
    const lost = testNode("nd_l", 0, 0, {
      type: "jumper",
      target: { board_id: "bd_gone", node_id: "nd_lost" },
    });
    expect(labelsOf(lost, context).body).toBe("canvas.node.jumper_to(nd_lost)");
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

  it("cuts a long text between whole characters, not within an emoji", () => {
    // The emoji's two code units are the 239th and the 240th.
    const text = plainText(
      {
        type: "doc",
        content: [
          {
            type: "paragraph",
            content: [
              { type: "text", text: `${"a".repeat(238)}🌊${"b".repeat(50)}` },
            ],
          },
        ],
      },
      context,
    );
    expect(text).toBe(`${"a".repeat(238)}…`);
  });

  it("tells whether two cards' labels show the same", () => {
    const labels = { title: "Dock", kind: "Jumper", body: "To: The dock" };
    expect(sameLabels(labels, { ...labels })).toBe(true);
    expect(sameLabels(labels, { ...labels, body: "To: Harbour" })).toBe(false);
    expect(sameLabels(labels, undefined)).toBe(false);
  });

  it("tells that a name that cards show changed, but not for another change of what has it", () => {
    const ada = { name: "Ada", description: "" };
    const ben = { name: "Ben", description: "" };
    const name = (c: { name: string }) => c.name;
    const before = new Map([
      ["cmp_ada", ada],
      ["cmp_ben", ben],
    ]);
    expect(namesDiffer(before, before, name)).toBe(false);
    // Another description, copied as the document writes it.
    const described = new Map(before).set("cmp_ada", {
      ...ada,
      description: "The keeper",
    });
    expect(namesDiffer(before, described, name)).toBe(false);
    const renamed = new Map(before).set("cmp_ada", { ...ada, name: "Adah" });
    expect(namesDiffer(before, renamed, name)).toBe(true);
    // One went, and another came.
    const swapped = new Map([
      ["cmp_ada", ada],
      ["cmp_cy", { name: "Cy", description: "" }],
    ]);
    expect(namesDiffer(before, swapped, name)).toBe(true);
    expect(namesDiffer(before, new Map([["cmp_ada", ada]]), name)).toBe(true);
  });
});

describe("a card's text", () => {
  it("cuts a line to the card's width, ellipsis and all", () => {
    expect(clip("short", 10)).toBe("short");
    expect(clip("a longer title", 8)).toBe("a longe…");
    expect(clip("two\nlines", 10)).toBe("two…");
    // A first line that fills the width makes room for the ellipsis of the
    // lines after it.
    expect(clip("abcd\nef", 4)).toBe("abc…");
    expect(wrapLines("abcd efgh ijkl", 4, 2, false)).toBe("abcd\nefg…");
  });

  it("cuts by the width that the card's font draws each character at", () => {
    // Capitals twice a lowercase letter's width, as in a bold font.
    const measure = (ch: string) => (ch >= "A" && ch <= "Z" ? 2 : 1);
    expect(clip("LIGHTHOUSE", 10, measure)).toBe("LIGH…");
    expect(clip("lighthouse", 10, measure)).toBe("lighthouse");
    expect(wrapLines("THE KEEPER", 8, 2, false, measure)).toBe("THE\nKEE…");
  });

  it("wraps a text into the card's lines, at spaces", () => {
    expect(wrapLines("the quick brown fox jumps", 10, 2, false)).toBe(
      "the quick\nbrown fox…",
    );
    expect(wrapLines("one\ntwo\nthree", 10, 2, true)).toBe("one\ntwo");
    expect(wrapLines("anything", 10, 0, false)).toBe("");
  });

  it("spends no line on a paragraph's indentation", () => {
    // An indented word too long for the rest of the line is cut where the
    // line ends, not at a space of its indentation, which left a blank line.
    const indented = `intro\n${" ".repeat(10)}Antidisestablishmentarianism forever`;
    expect(wrapLines(indented, 20, 4, false)).toBe(
      `intro\n${" ".repeat(10)}Antidisest\nablishmentarianism\nforever`,
    );
    // Indentation as wide as the line is dropped.
    expect(wrapLines(`intro\n${" ".repeat(25)}word`, 20, 2, false)).toBe(
      "intro\nword",
    );
    // A short indented line keeps its indentation, and a paragraph of
    // spaces alone its line.
    expect(wrapLines(`intro\n${" ".repeat(4)}a word`, 20, 2, false)).toBe(
      `intro\n${" ".repeat(4)}a word`,
    );
    expect(wrapLines(`${" ".repeat(30)}\nend`, 20, 2, false)).toBe("\nend");
  });

  it("shows each of a branch's rows, however long the rows before it", () => {
    const rows = Array.from(
      { length: 8 },
      (_, i) => `if inventory.has_lantern && courage > ${i}`,
    );
    const shown = wrapLines(rows.join("\n"), 20, 8, true).split("\n");
    expect(shown).toHaveLength(8);
    expect(shown.every((row) => row === "if inventory.has_la…")).toBe(true);
  });

  it("counts a wide character, as of Japanese, as two of a line's columns", () => {
    expect(clip("灯台守の階段", 8)).toBe("灯台守…");
    expect(clip("灯台守", 6)).toBe("灯台守");
    expect(wrapLines("灯台守が階段を上って灯りをともす", 10, 2, false)).toBe(
      "灯台守が階\n段を上っ…",
    );
    // An emoji of two code units is cut whole, not in half.
    expect(clip("ab🌊🌊🌊", 4)).toBe("ab…");
  });

  it("cuts between characters as a reader sees them, not within an emoji of several, a flag, or a letter and its accent", () => {
    const zwj = String.fromCodePoint(0x200d);
    const family = `👨${zwj}👩${zwj}👧`;
    expect(clip(`ab${family}cd`, 5)).toBe(`ab${family}…`);
    expect(clip(`ab${family}cd`, 4)).toBe("ab…");
    // A flag is two regional indicators, and as wide as an emoji.
    const czech = String.fromCodePoint(0x1f1e8, 0x1f1ff);
    expect(clip(`Prague ${czech} trip`, 9)).toBe("Prague …");
    expect(clip(`Prague ${czech}`, 9)).toBe(`Prague ${czech}`);
    // An accent written after its letter takes none of the line's room.
    const cafe = `Cafe${String.fromCodePoint(0x301)}s`;
    expect(clip(cafe, 5)).toBe(cafe);
    expect(wrapLines(`${cafe} ${cafe}`, 5, 2, false)).toBe(`${cafe}\n${cafe}`);
    // A text's excerpt ends between whole characters too.
    const text = plainText(
      {
        type: "doc",
        content: [
          {
            type: "paragraph",
            content: [{ type: "text", text: `${"a".repeat(236)}${family}b` }],
          },
        ],
      },
      context,
    );
    expect(text).toBe(`${"a".repeat(236)}…`);
    // And so do the lines of a jumper's target, which no excerpt cut before
    // them, on a card large enough for the whole of what a card shows, with
    // the ellipsis that says that more of it follows.
    expect(wrapLines(`To ${"🌊".repeat(150)}`, 1000, 4, false)).toBe(
      `To ${"🌊".repeat(118)}…`,
    );
  });

  it("finds a text's characters as the browser's segmenter does, which PixiJS keys its glyphs by, of the marks that are letters too", () => {
    const segmenter = new Intl.Segmenter();
    for (const value of [
      // Thai's and Lao's vowel AM, after its consonant, of no other mark.
      "ทำงาน",
      "ກຳລັງ",
      // A syllable of Hangul written in its conjoining jamo.
      String.fromCodePoint(0x1100, 0x1161, 0x11a8, 0x1100, 0x1161),
      // Halfwidth katakana, with its voiced marks.
      String.fromCodePoint(0xff76, 0xff9e, 0xff77, 0xff9e, 0xff78),
      // A carriage return and the line feed after it.
      `one${String.fromCodePoint(0x0d, 0x0a)}two`,
      // Arabic's number sign, which joins the digits after it.
      String.fromCodePoint(0x600, 0x661, 0x662),
      // Malayalam's dot reph, before its consonant.
      String.fromCodePoint(0xd4e, 0xd30),
      "plain Latin text",
      "灯台守",
    ]) {
      expect([...graphemes(value)]).toEqual(
        Array.from(segmenter.segment(value), (s) => s.segment),
      );
    }
    // A cut keeps a consonant and its vowel together.
    expect(clip("ทำงาน", 2)).toBe("ทำ…");
    expect(clip("ทำงาน", 3)).toBe("ทำง…");
  });

  it("draws the glyphs sharper as the zoom grows", () => {
    expect(textTier(1)).toBe(1);
    expect(textTier(3)).toBe(2);
    expect(textTier(8)).toBe(4);
  });

  it("keeps the glyphs' resolution as the zoom falls a little below it, and raises it as soon as the zoom needs it", () => {
    // A tier's edge is where the title's 15 units take the glyphs' 28
    // pixels: a scale of 1.87 between 1 and 2, and of 3.73 between 2 and 4.
    expect(textTier(1.8)).toBe(1);
    expect(textTier(1.8, 2)).toBe(2);
    expect(textTier(1.6, 2)).toBe(1);
    expect(textTier(1.9, 1)).toBe(2);
    expect(textTier(3.6, 4)).toBe(4);
    expect(textTier(3, 4)).toBe(2);
    expect(textTier(1, 4)).toBe(1);
    expect(textTier(3.8, 2)).toBe(4);
  });
});

describe("a node of a type that the canvas does not know", () => {
  it("is laid out and labelled as a card of its title, as a newer server's may send to a page loaded before it", () => {
    const node = {
      ...testNode("nd_1", 40, 80, { title: "The dock" }),
      type: "hub",
    } as unknown as Node;
    expect(nodeRect(node)).toEqual({ x: 40, y: 80, width: 240, height: 128 });
    expect(labelsOf(node, context)).toEqual({
      title: "The dock",
      kind: "",
      body: "",
    });
  });

  it("shows the text of an element without the blocks of a type that the canvas does not know, as a newer server's may send", () => {
    const node = testNode("nd_1", 0, 0, {
      text: {
        type: "doc",
        content: [
          { type: "heading", content: [{ type: "text", text: "Night" }] },
          { type: "horizontalRule" },
          { type: "paragraph", content: [{ type: "text", text: "The tide" }] },
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
                  { type: "codeBlock" },
                ],
              },
            ],
          },
        ],
      } as unknown as RichTextDoc,
    });
    expect(labelsOf(node, context).body).toBe("The tide\none");
  });
});

describe("a node's colour", () => {
  // The light and the dark theme's tokens (web/shared/src/tokens).
  const light: Theme = {
    canvas: 0xffffff,
    surface: 0xf3f4f6,
    border: 0x7c8494,
    fg: 0x15171c,
    muted: 0x4b5260,
    accent: 0x2b50c8,
    danger: 0xb3261e,
    dark: false,
  };
  const dark: Theme = {
    canvas: 0x121419,
    surface: 0x1c1f26,
    border: 0x727b8c,
    fg: 0xeceef2,
    muted: 0xa8afbc,
    accent: 0x8fa8ff,
    danger: 0xff8b80,
    dark: true,
  };
  const colors = [
    "red",
    "orange",
    "yellow",
    "green",
    "teal",
    "blue",
    "purple",
    "pink",
    "gray",
  ] as const;

  it("marks a node by its palette's colour, in the light theme or the dark, each 3:1 or more against the card and the board, and one of a colour that the palette does not have by the theme's edge", () => {
    for (const theme of [light, dark]) {
      for (const color of colors) {
        const mark = nodeColor(color, theme);
        expect(mark, color).not.toBe(theme.border);
        expect(contrast(mark, theme.surface)).toBeGreaterThanOrEqual(3);
        expect(contrast(mark, theme.canvas)).toBeGreaterThanOrEqual(3);
      }
    }
    expect(nodeColor("red", light)).toBe(0xc62828);
    expect(nodeColor("red", dark)).toBe(0xff7b72);
    expect(nodeColor("none", light)).toBe(light.border);
    // As a newer server's palette may send, to a page loaded before it.
    expect(nodeColor("crimson" as Node["color"], light)).toBe(light.border);
  });

  it("marks a node by the theme's edge where its colour would stand out of the card by less than 3:1, as on a contrast theme's Canvas of a middle grey", () => {
    // The system's colours of a contrast theme of the user's own.
    const grey: Theme = {
      canvas: 0x767676,
      surface: 0x767676,
      border: 0xffffff,
      fg: 0xffffff,
      muted: 0xffffff,
      accent: 0xffff00,
      danger: 0xffffff,
      dark: true,
    };
    for (const color of colors) {
      expect(nodeColor(color, grey), color).toBe(grey.border);
    }
  });

  it("tells a theme read again of the same colours from one of others", () => {
    expect(sameTheme(light, { ...light })).toBe(true);
    expect(sameTheme(light, { ...light, accent: 0x000000 })).toBe(false);
    expect(sameTheme(light, dark)).toBe(false);
  });
});

describe("the canvas's theme", () => {
  it("draws in the system's colours where the system forces its own, as under a contrast theme, rather than tokens that would each read as the text's colour", () => {
    // Each system colour as the page resolves it.
    const system = (name: string) => {
      const probe = document.createElement("span");
      probe.style.color = name;
      document.body.append(probe);
      const color = parseColor(getComputedStyle(probe).color);
      probe.remove();
      return color;
    };
    const host = document.createElement("div");
    document.body.append(host);
    try {
      const forced = readTheme(host, true);
      expect(forced).toEqual({
        canvas: system("Canvas"),
        surface: system("Canvas"),
        border: system("CanvasText"),
        fg: system("CanvasText"),
        muted: system("CanvasText"),
        accent: system("Highlight"),
        danger: system("CanvasText"),
        dark: false,
      });
      expect(forced.canvas).not.toBe(forced.fg);
      // Without forced colours, the tokens, which jsdom's page has none of.
      expect(readTheme(host).fg).toBe(0x15171c);
      expect(host.childElementCount).toBe(0);
    } finally {
      host.remove();
    }
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
    expect(canvasKey({ ...key, key: "!", code: "Digit1" }, "linux")).toBe(
      "canvas.fit",
    );
    expect(canvasKey({ ...key, key: '"', code: "Digit2" }, "linux")).toBe(
      "canvas.zoom_selection",
    );
    expect(
      canvasKey(
        { ...key, shiftKey: false, key: "Escape", code: "Escape" },
        "linux",
      ),
    ).toBe("canvas.deselect");
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

  it("moves panzoom's path as fast on the screen at 25% as at 150%", () => {
    const area = { x: 0, y: 0, width: 90_000, height: 1000 };
    const size = { width: 800, height: 600 };
    // The board's point at the canvas's centre, and its speed on the
    // screen, in CSS pixels a second, over a hundredth of a second.
    const centre = (s: number) => {
      const v = pathAt("panzoom", s, area, size);
      return v.x + size.width / 2 / v.zoom;
    };
    const onScreen = (s: number) =>
      ((centre(s + 0.01) - centre(s)) / 0.01) *
      pathAt("panzoom", s, area, size).zoom;
    for (const s of [0, 1.5, 3, 7.5, 9]) {
      expect(onScreen(s)).toBeCloseTo(900, -1);
    }
  });

  it("warms up for a pass of the area, row after row a canvas apart", () => {
    const area = { x: 0, y: 0, width: 9000, height: 1000 };
    expect(passOf("pan", area, { height: 600 })).toBe(30);
    // Panzoom's path, at the zoom of each moment, gets to the end of its
    // third row, the area's bottom right, as the pass ends.
    const pass = passOf("panzoom", area, { height: 600 });
    const end = pathAt("panzoom", pass - 1e-6, area, {
      width: 800,
      height: 600,
    });
    expect(end.x + 400 / end.zoom).toBeCloseTo(9000, 0);
    expect(end.y + 300 / end.zoom).toBeCloseTo(1000, 0);
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
