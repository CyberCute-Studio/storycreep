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
      "(`met_elara` is false) or `gold` is less than -2",
    );
    expect(said('(met_elara or gold > 1) and keeper != "Ada"')).toBe(
      "(`met_elara` is true or `gold` is more than 1) and `keeper` is not “Ada”",
    );
    expect(
      said(
        `visited(node("${ids.quay}")) and visits(node("${ids.tower}")) == 2`,
      ),
    ).toBe(
      "(“Quay” has been visited) and (the number of visits to “Tower” on Lighthouse) is 2",
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
        "Set `trust` to ((the smaller of `trust` and 3) times 2).",
        "Send the signal “met” with 1.",
      ].join("\n"),
    );
    expect(said("gold = (gold - 1) - 2", "script")).toBe(
      "Set `gold` to ((`gold` minus 1) minus 2).",
    );
    expect(said("gold = gold - (1 - 2)", "script")).toBe(
      "Set `gold` to (`gold` minus (1 minus 2)).",
    );
    // A condition assigned, as a comparison's operand is said.
    expect(said("met_elara = gold > 3\nmet_elara = true", "script")).toBe(
      [
        "Set `met_elara` to whether (`gold` is more than 3).",
        "Set `met_elara` to true.",
      ].join("\n"),
    );
    expect(said("gold += trust * 2", "script")).toBe(
      "Add (`trust` times 2) to `gold`.",
    );
  });

  // Verifies: FR-EXPR-01/AC4
  it("groups each operand, argument, and condition of a not but a name, a literal, a negative number, or a node, and a comparison within an and or an or, so that no two ASTs of different meanings say the same", () => {
    // What a not is of.
    const notOfOr = said("not (met_elara or gold > 1) and trust > 2");
    expect(notOfOr).toBe(
      "(it is not so that (`met_elara` is true or `gold` is more than 1)) and `trust` is more than 2",
    );
    expect(
      new Set([
        notOfOr,
        said("not (met_elara or (gold > 1 and trust > 2))"),
        said("not ((met_elara or gold > 1) and trust > 2)"),
      ]).size,
    ).toBe(3);
    expect(said("not (gold > 1) and met_elara")).toBe(
      "(it is not so that (`gold` is more than 1)) and `met_elara` is true",
    );
    expect(said("not (gold > 1 and met_elara)")).toBe(
      "it is not so that (`gold` is more than 1 and `met_elara` is true)",
    );
    // A comparison of an and, within an and, whose own operands are
    // grouped, so that what follows it is of none of them.
    const ofAnd = said("met_elara == (gold > 1 and trust > 2) and trust < 5");
    expect(ofAnd).toBe(
      "`met_elara` is whether (`gold` is more than 1 and `trust` is more than 2) and `trust` is less than 5",
    );
    expect(
      new Set([
        ofAnd,
        said("met_elara == ((gold > 1 and trust > 2) and trust < 5)"),
        said("met_elara == (gold > 1 and (trust > 2 and trust < 5))"),
      ]).size,
    ).toBe(3);
    // The arguments of a call, and a call within arithmetic.
    expect(said("abs(gold - 3) > 1")).toBe(
      "((`gold` minus 3) without its sign) is more than 1",
    );
    expect(said("gold - abs(3) > 1")).toBe(
      "(`gold` minus (3 without its sign)) is more than 1",
    );
    expect(said("abs(min(gold, trust)) > min(gold, abs(trust))")).toBe(
      "((the smaller of `gold` and `trust`) without its sign) is more than (the smaller of `gold` and (`trust` without its sign))",
    );
    expect(said("-abs(gold) < abs(-gold)")).toBe(
      "(minus (`gold` without its sign)) is less than ((minus `gold`) without its sign)",
    );
    expect(said("abs(-2) == clamp(gold + 1, 0, 5)")).toBe(
      "(-2 without its sign) is ((`gold` plus 1) kept between 0 and 5)",
    );
    // Arithmetic of one precedence, of either order, and a float that is
    // whole, which divides otherwise than an int.
    expect(said("(gold % 3) / 2 > 0")).toBe(
      "((the remainder of `gold` divided by 3) divided by 2) is more than 0",
    );
    expect(said("(gold / 3) % 2 > 0")).toBe(
      "(the remainder of (`gold` divided by 3) divided by 2) is more than 0",
    );
    expect(said("gold / 2 > 0")).toBe("(`gold` divided by 2) is more than 0");
    expect(said("gold / 2.0 > -1.0")).toBe(
      "(`gold` divided by 2.0) is more than -1.0",
    );
    // An and of an and, of either order, and an or of an and of
    // comparisons, or an and of an or of them.
    expect(said("met_elara and (gold > 1 and trust > 2)")).not.toBe(
      said("(met_elara and gold > 1) and trust > 2"),
    );
    const orOfAnd = said("(gold == 1 and trust == 2) or met_elara");
    const andOfOr = said("gold == 1 and (trust == 2 or met_elara)");
    expect(orOfAnd).toBe(
      "(`gold` is 1 and `trust` is 2) or `met_elara` is true",
    );
    expect(andOfOr).toBe(
      "`gold` is 1 and (`trust` is 2 or `met_elara` is true)",
    );
    expect(orOfAnd).not.toBe(andOfOr);
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
    // An int's digits as to_string writes them, ungrouped, as a float's.
    expect(said("gold >= 12500 and gold >= 12500.5", "condition", words)).toBe(
      "`gold` je alespoň 12500 a `gold` je alespoň 12500,5",
    );
  });

  it("marks no name of a text that the user wrote, whatever it holds", () => {
    expect(said('keeper == "0"')).toBe("`keeper` is “�0�”");
  });
});
