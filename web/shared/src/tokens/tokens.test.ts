import { readFileSync } from "node:fs";
import { expect, it } from "vitest";

// The colours of tokens.css that are shown together, which must contrast as
// PRD NFR-A11Y-06 asks, in the light mode and in the dark: text on its
// backgrounds by 4.5:1, and the boundaries of controls and the focus ring
// against what they are drawn on by 3:1 (WCAG 2.2 success criteria 1.4.3 and
// 1.4.11). A colour that a component shows on another joins these.
const text: readonly [string, string][] = [
  ["fg", "canvas"],
  ["fg", "surface"],
  ["fg", "surface-hover"],
  ["fg-muted", "canvas"],
  ["fg-muted", "surface"],
  ["fg-muted", "surface-hover"],
  ["accent", "canvas"],
  ["accent", "surface"],
  // A link under the pointer.
  ["accent-hover", "canvas"],
  ["accent-fg", "accent"],
  ["accent-fg", "accent-hover"],
  ["danger", "canvas"],
  ["danger", "surface"],
  ["danger-fg", "danger"],
];
const boundaries: readonly [string, string][] = [
  ["border", "canvas"],
  ["border", "surface"],
  ["focus", "canvas"],
  ["focus", "surface"],
  ["accent", "canvas"],
  ["accent", "surface"],
];

type Mode = "light" | "dark";

/** The colours of tokens.css, by name without --color-, in each mode. */
function readColours(): Record<string, Record<Mode, string>> {
  const css = readFileSync(new URL("tokens.css", import.meta.url), "utf8");
  const colours: Record<string, Record<Mode, string>> = {};
  const declaration =
    /--color-([a-z-]+):\s*light-dark\(\s*(#[0-9a-f]{6})\s*,\s*(#[0-9a-f]{6})\s*\)\s*;/g;
  for (const [, name, light, dark] of css.matchAll(declaration)) {
    colours[name] = { light, dark };
  }
  return colours;
}

/** The relative luminance of a colour #rrggbb, as WCAG 2.2 defines it. */
function luminance(hex: string): number {
  const channels = [1, 3, 5].map((i) => {
    const c = Number.parseInt(hex.slice(i, i + 2), 16) / 255;
    return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2];
}

/** The contrast ratio of two colours, as WCAG 2.2 defines it. */
function contrast(a: string, b: string): number {
  const [lighter, darker] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (lighter + 0.05) / (darker + 0.05);
}

// Verifies: NFR-A11Y-01, NFR-A11Y-06
it("the tokens' colours contrast enough in both modes", () => {
  const colours = readColours();
  const problems: string[] = [];
  for (const [pairs, least] of [
    [text, 4.5],
    [boundaries, 3],
  ] as const) {
    for (const [front, back] of pairs) {
      for (const mode of ["light", "dark"] as const) {
        const f = colours[front]?.[mode];
        const b = colours[back]?.[mode];
        if (f === undefined || b === undefined) {
          problems.push(`${front} or ${back} is not a colour of tokens.css`);
          continue;
        }
        const ratio = contrast(f, b);
        if (ratio < least) {
          problems.push(
            `${front} on ${back} in the ${mode} mode: ${ratio.toFixed(2)}:1, want at least ${least}:1`,
          );
        }
      }
    }
  }
  expect(problems).toEqual([]);
});

it("every colour of tokens.css has a light and a dark value", () => {
  const css = readFileSync(new URL("tokens.css", import.meta.url), "utf8");
  const named = [...css.matchAll(/--color-([a-z-]+):/g)].map((m) => m[1]);
  expect(named.length).toBeGreaterThan(0);
  expect(Object.keys(readColours()).sort()).toEqual(named.sort());
});
