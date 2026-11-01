// @vitest-environment node
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
    ]) {
      expect(reported(css), css).toEqual([]);
    }
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
  // The build fails with the problems of every stylesheet of the bundle.
  function build(bundle: Record<string, string>): string | undefined {
    const plugin = hoverCheck();
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
      generate.call(
        {
          error(message: string): never {
            throw new Error(message);
          },
        },
        {},
        outputs,
      );
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

  it("runs when Vite builds the app", () => {
    const names = (config.plugins ?? [])
      .flat()
      .map((p) => (p && typeof p === "object" && "name" in p ? p.name : ""));
    expect(names).toContain("storycreep:hover-check");
  });
});
