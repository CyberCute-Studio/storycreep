import { createI18n } from "@storycreep/shared/i18n";
import { describe, expect, it } from "vitest";
import en from "../../../shared/i18n/en.json";
import { analyze } from "./analyze";
import type { Words } from "./messages";
import { type Part, plainOf } from "./plain";
import { englishWords, harbourScope, ids } from "./testdata";

// The plain-language rendering of an expression (PRD FR-EXPR-01 AC4, plan
// note N74): generated from the AST, not from its source, and in the user's
// language, from the catalog's phrases.

const scope = harbourScope();

/** Returns what the rendering of source says, names in backticks, a sentence a line. */
function said(
  source: string,
  context: "condition" | "script" = "condition",
  words: Words = englishWords(),
): string {
  const a = analyze(source, context, scope);
  if (a.ast === undefined) {
    throw new Error(`${source} does not compile: ${a.error?.code}`);
  }
  return plainOf(a.ast, words, scope.view, scope.boardId)
    .map((parts: Part[]) =>
      parts.map((p) => (p.kind === "code" ? `\`${p.text}\`` : p.text)).join(""),
    )
    .join("\n");
}

describe("the plain-language rendering of an expression", () => {
  // Verifies: FR-EXPR-01/AC4
  it("says a condition in words, from its AST, as the PRD's example has it", () => {
    expect(said("met_elara and elara.trust >= 3")).toBe(
      "`met_elara` is true and `elara.trust` is at least 3",
    );
    // Another source of the same AST says the same.
    expect(said("met_elara&&(cmp.elara.trust)>=3 // first")).toBe(
      said("met_elara and elara.trust >= 3"),
    );
    expect(said("not met_elara or gold < -2")).toBe(
      "`met_elara` is false or `gold` is less than -2",
    );
    expect(said('(met_elara or gold > 1) and keeper != "Ada"')).toBe(
      "(`met_elara` is true or `gold` is more than 1) and `keeper` is not “Ada”",
    );
    expect(
      said(
        `visited(node("${ids.quay}")) and visits(node("${ids.tower}")) == 2`,
      ),
    ).toBe(
      "“Quay” has been visited and the number of visits to “Tower” on Lighthouse is 2",
    );
    // Names as the board's expressions write them: Harbour's trust bare,
    // the global one qualified.
    expect(said("trust > global.trust")).toBe(
      "`trust` is more than `global.trust`",
    );
  });

  it("says each statement of a script as a sentence", () => {
    expect(
      said(
        'gold += 5\nkeeper += "!"\ntrust = min(trust, 3) * 2\nsignal("met", 1)',
        "script",
      ),
    ).toBe(
      [
        "Add 5 to `gold`.",
        "Add “!” to the end of `keeper`.",
        "Set `trust` to the smaller of `trust` and 3 times 2.",
        "Send the signal “met” with 1.",
      ].join("\n"),
    );
    expect(said("gold = (gold - 1) - 2", "script")).toBe(
      "Set `gold` to `gold` minus 1 minus 2.",
    );
    expect(said("gold = gold - (1 - 2)", "script")).toBe(
      "Set `gold` to `gold` minus (1 minus 2).",
    );
  });

  // Verifies: FR-EXPR-01/AC4
  it("is in the user's language, from the catalog's phrases, with the locale's numbers", () => {
    const cs = createI18n("cs", {
      catalogs: {
        en,
        cs: {
          "expr.plain.and": "{left} a {right}",
          "expr.plain.is_true": "{value} je pravda",
          "expr.plain.gte": "{left} je alespoň {right}",
        },
      },
    });
    const words: Words = { t: cs.t.bind(cs), locale: "cs" };
    expect(said("met_elara and elara.trust >= 2.5", "condition", words)).toBe(
      "`met_elara` je pravda a `elara.trust` je alespoň 2,5",
    );
  });

  it("marks no name of a text that the user wrote, whatever it holds", () => {
    expect(said('keeper == "0"')).toBe("`keeper` is “�0�”");
  });
});
