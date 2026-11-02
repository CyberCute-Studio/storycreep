// @vitest-environment node
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { Rolldown } from "vite";
import { describe, expect, it } from "vitest";
import config from "../vite.config";
import { checkHover, formatHoverProblem, hoverCheck } from "./hover";

/** The properties that checkHover reports in css, in order. */
function reported(css: string): string[] {
  return checkHover(css, "test.css").map((p) => p.property);
}

describe("checkHover", () => {
  // Verifies: FR-EDIT-09/AC2
  it("refuses a hover rule that can show, hide, move, or resize a control", () => {
    const cases: Record<string, string[]> = {
      ".menu:hover .actions { display: block }": ["display"],
      ".row:hover .delete { opacity: 1 }": ["opacity"],
      ".group:hover .x { visibility: visible; pointer-events: auto }": [
        "visibility",
        "pointer-events",
      ],
      // Tailwind's group-hover: and peer-hover: variants.
      ":is(:where(.group):hover *) { opacity: 1 }": ["opacity"],
      ":is(:where(.peer):hover ~ *) { display: flex }": ["display"],
      // Hidden unless hovered.
      ".a:not(:hover) .b { display: none }": ["display"],
      ".card:has(:hover) .b { transform: none }": ["transform"],
      "li:nth-child(2 of :hover) { height: auto }": ["height"],
      // Tailwind wraps its hover: variant in this query.
      "@media (hover: hover) { .a:hover { width: 10rem } }": ["width"],
      "@supports (display: grid) { @layer x { .a:hover { max-height: none } } }":
        ["max-height"],
      // A custom property may feed any other.
      ".a:hover { --reveal: 1 }": ["--reveal"],
      ".a:hover { opacity: var(--shown) }": ["opacity"],
      // Tailwind's hover:shadow-md, which sets one, as its ring utilities do.
      ".a:hover { --tw-shadow: 0 4px 6px -1px var(--tw-shadow-color, rgb(0 0 0 / 0.1)); box-shadow: var(--tw-ring-shadow), var(--tw-shadow) }":
        ["--tw-shadow"],
      // An image, as an icon, that only hover shows.
      ".row:hover .delete { background-image: url(trash.svg) }": [
        "background-image",
      ],
      ".a:hover { background: url(trash.svg) }": ["background"],
      ".a:hover { display: block !important }": ["display"],
      // Hover or focus is still hover on a touch screen, which has neither.
      ".a:hover, .a:focus-visible { opacity: 1 }": ["opacity"],
      // Nested rules apply on their parent's hover.
      ".a { &:hover { display: block } }": ["display"],
      ".a:hover { .b { display: block } }": ["display"],
      ".a:hover { @media (min-width: 1px) { position: static } }": ["position"],
      ".a:hover { color: red; scale: 1.1 }": ["scale"],
      // A scope that hover brings its rules into, or takes them out of.
      "@scope (.card:hover) { .actions { display: block } }": ["display"],
      "@scope (.a) to (.b:hover) { .c { display: none } }": ["display"],
      "@media (min-width: 1px) { @scope (:is(.a:hover)) { .b { opacity: 1 } } }":
        ["opacity"],
    };
    for (const [css, want] of Object.entries(cases)) {
      expect(reported(css), css).toEqual(want);
    }
  });

  it("allows a hover rule that changes only how a control looks", () => {
    for (const css of [
      ".a:hover { background-color: red; color: blue }",
      ".a:hover { border-color: var(--color-border); outline-color: red }",
      ".a:hover { text-decoration-line: underline; text-underline-offset: 2px }",
      ".a:hover { box-shadow: 0 0 0 1px red; cursor: pointer }",
      // Tailwind's hover:[box-shadow:…], a shadow without a custom property.
      ".hover\\:\\[box-shadow\\:0_4px_6px_-1px_rgb\\(0_0_0\\/0\\.1\\)\\]:hover { box-shadow: 0 4px 6px -1px rgb(0 0 0/0.1) }",
      "@media (hover: hover) { .a:hover { background-color: color-mix(in oklab, red 50%, transparent) } }",
      // A class named after Tailwind's variant is not the pseudo-class.
      ".hover\\:block { display: block }",
      ".group-hover\\:x { opacity: 0 }",
      // Other states than hover.
      ".a { opacity: 0 } .a:focus-visible { opacity: 1 } .a:active { display: block }",
      ".a:hover { color: red } .b { display: none }",
      // A scope without hover, and one with it that only colours.
      "@scope (.card) to (.b) { .actions { display: block } }",
      "@scope (.card:hover) { .actions { color: red } }",
      // A condition, which holds whether anything is hovered or not.
      "@supports selector(:hover) { .a { display: block } }",
    ]) {
      expect(reported(css), css).toEqual([]);
    }
  });

  // Verifies: FR-EDIT-09/AC2
  it("refuses CSS that it cannot read, where a rule that applies on hover would go unchecked", () => {
    for (const css of [
      // A selector that lightningcss cannot parse, whose rule it skips.
      ".a:hover >>> .b { display: block }",
      // An at-rule that it does not know, whose rules it does not read.
      "@future-rule { .a:hover { display: block } }",
    ]) {
      expect(checkHover(css, "test.css"), css).toEqual([
        expect.objectContaining({ unread: expect.any(String) }),
      ]);
    }
    const [problem] = checkHover(
      ".x { color: red }\n@future-rule { }",
      "assets/index.css",
    );
    expect(problem).toMatchObject({ file: "assets/index.css", line: 2 });
    expect(formatHoverProblem(problem)).toMatch(
      /^assets\/index\.css:2:\d+: the hover check cannot read this CSS \(Unknown at rule: @future-rule\)/,
    );
  });

  it("says where the rule is, and which selector", () => {
    const css = ".x { color: red }\n  .menu:hover .actions { display: block }";
    const [problem] = checkHover(css, "assets/index.css");
    expect(problem).toEqual({
      file: "assets/index.css",
      line: 2,
      column: 3,
      selector: ".menu:hover .actions",
      property: "display",
    });
    expect(formatHoverProblem(problem)).toMatch(
      /^assets\/index\.css:2:3: \.menu:hover \.actions applies on hover and sets display/,
    );
  });
});

describe("hoverCheck", () => {
  // The context of a hook, whose error fails the build.
  const context = {
    error(message: string): never {
      throw new Error(message);
    },
  };

  // The build fails with the problems of every stylesheet of the bundle, and
  // of the app's public/, in root, if it is given.
  function build(
    bundle: Record<string, string>,
    root?: string,
  ): string | undefined {
    const plugin = hoverCheck();
    if (root !== undefined) {
      const resolved = plugin.configResolved as (config: unknown) => void;
      resolved({ root, publicDir: join(root, "public") });
    }
    const outputs = Object.fromEntries(
      Object.entries(bundle).map(([fileName, source]) => [
        fileName,
        { type: "asset", fileName, source },
      ]),
    ) as unknown as Rolldown.OutputBundle;
    const generate = plugin.generateBundle as (
      this: { error(message: string): never },
      options: unknown,
      bundle: Rolldown.OutputBundle,
    ) => void;
    try {
      generate.call(context, {}, outputs);
      return undefined;
    } catch (error) {
      return (error as Error).message;
    }
  }

  it("fails the build on a stylesheet with a hover rule that it refuses", () => {
    const message = build({
      "assets/a.css": ".a:hover { color: red }",
      "assets/b.css": ".b:hover .c { opacity: 1 }",
      "assets/index.js": ".b:hover .c { opacity: 1 }",
    });
    expect(message).toMatch(/^assets\/b\.css:1:1: \.b:hover \.c applies/);
    expect(message?.split("\n")).toHaveLength(1);
    expect(build({ "assets/a.css": ".a:hover { color: red }" })).toBe(
      undefined,
    );
  });

  it("fails the build on a hover rule that it refuses in a page's <style>, or in a stylesheet of public/", () => {
    const page =
      '<!doctype html>\n<html><head><style nonce="n">.a:hover{color:red}</style>\n' +
      "  <style>\n    .row:hover .delete { opacity: 1 }\n  </style></head></html>";
    expect(build({ "index.html": page })).toMatch(
      /^index\.html:4:5: \.row:hover \.delete applies on hover and sets opacity/,
    );
    expect(
      build({ "index.html": "<style>.a:hover { color: red }</style>" }),
    ).toBe(undefined);

    // Vite copies public/ to the build as it is, outside the bundle.
    const root = mkdtempSync(join(tmpdir(), "storycreep-hover-"));
    try {
      mkdirSync(join(root, "public", "styles"), { recursive: true });
      writeFileSync(
        join(root, "public", "styles", "ok.css"),
        ".a:hover{color:red}",
      );
      writeFileSync(join(root, "public", "icon.svg"), "<svg></svg>");
      expect(build({}, root)).toBe(undefined);
      writeFileSync(
        join(root, "public", "styles", "menu.css"),
        ".menu:hover .actions { display: block }",
      );
      expect(build({}, root)).toMatch(
        /^public\/styles\/menu\.css:1:1: \.menu:hover \.actions applies on hover and sets display/,
      );
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it("fails the build on CSS that a script imports as a string, where the check cannot read it", () => {
    const transform = hoverCheck().transform as (
      this: typeof context,
      code: string,
      id: string,
    ) => void;
    for (const id of [
      "/app/src/menu.css?inline",
      "/app/src/menu.module.scss?inline",
      "/app/src/menu.css?raw",
      "/app/src/menu.css?used&inline",
    ]) {
      expect(() => transform.call(context, "", id), id).toThrow(
        /imports as a string/,
      );
    }
    for (const id of [
      "/app/src/menu.css",
      "/app/src/menu.css?url",
      "/app/src/main.tsx",
      "/app/src/notes.txt?raw",
      "/app/index.html?html-proxy&inline-css&index=0.css",
    ]) {
      expect(() => transform.call(context, "", id), id).not.toThrow();
    }
  });

  it("runs when Vite builds the app", () => {
    const names = (config.plugins ?? [])
      .flat()
      .map((p) => (p && typeof p === "object" && "name" in p ? p.name : ""));
    expect(names).toContain("storycreep:hover-check");
  });
});
