import { describe, expect, test } from "vitest";
import {
  MAX_OFFERS,
  offersOf,
  type RichTextChoices,
  valueQuery,
} from "./choices";

const choices: RichTextChoices = {
  values: [
    { ref: "var_big_gold", name: "big_gold", detail: "Global variable" },
    {
      ref: "cmp_elara",
      attribute: "atr_trust",
      name: "elara.trust",
      detail: "Trust of Elara",
      terms: ["Elara Trust"],
    },
    { ref: "var_gold", name: "gold", detail: "Global variable" },
  ],
  mentions: [
    {
      id: "cmp_old_man",
      name: "Old Man",
      detail: "Character",
      terms: ["sage"],
    },
    { id: "cmp_elara", name: "Élara", detail: "Character", terms: ["elara"] },
  ],
};

/** Returns the names of what open offers. */
function offered(trigger: "{" | "@", query: string): string[] {
  return offersOf(choices, { trigger, query }).offers.map(
    (offer) => offer.choice.name,
  );
}

describe("what a text's { and @ offer", () => {
  test("reads the digits after a last colon of what was typed after { as the format of the value chosen, from 0 to 6", () => {
    expect(valueQuery("gold")).toEqual({ name: "gold" });
    expect(valueQuery("gold:")).toEqual({ name: "gold" });
    expect(valueQuery("gold:2")).toEqual({ name: "gold", decimals: 2 });
    expect(valueQuery("gold:0")).toEqual({ name: "gold", decimals: 0 });
    expect(valueQuery(":6")).toEqual({ name: "", decimals: 6 });
    // A colon followed by anything else is part of the name, which no
    // value has.
    expect(valueQuery("gold:7")).toEqual({ name: "gold:7" });
    expect(valueQuery("gold:22")).toEqual({ name: "gold:22" });
    expect(offered("{", "gold:7")).toEqual([]);
  });

  test("offers the values whose names have a word that starts as each word typed does, those whose names start so first", () => {
    expect(offered("{", "")).toEqual(["big_gold", "elara.trust", "gold"]);
    expect(offered("{", "go")).toEqual(["gold", "big_gold"]);
    expect(offered("{", "elara.tr")).toEqual(["elara.trust"]);
    // By what else names it, as its component's name and its own.
    expect(offered("{", "trust")).toEqual(["elara.trust"]);
    const two = offersOf(choices, { trigger: "{", query: "gold:2" });
    expect(two.offers.map((o) => o.kind === "value" && o.decimals)).toEqual([
      2, 2,
    ]);
  });

  test("offers the components by their names, whatever their case and diacritics, and by their keys", () => {
    expect(offered("@", "")).toEqual(["Old Man", "Élara"]);
    expect(offered("@", "ela")).toEqual(["Élara"]);
    expect(offered("@", "man")).toEqual(["Old Man"]);
    expect(offered("@", "sage")).toEqual(["Old Man"]);
    expect(offered("@", "zz")).toEqual([]);
  });

  test("offers at most MAX_OFFERS, and says how many match in all", () => {
    const many: RichTextChoices = {
      values: Array.from({ length: MAX_OFFERS + 5 }, (_, i) => ({
        ref: `var_${i}`,
        name: `gold_${String(i).padStart(2, "0")}`,
        detail: "",
      })),
      mentions: [],
    };
    const { offers, total } = offersOf(many, { trigger: "{", query: "gold" });
    expect(offers).toHaveLength(MAX_OFFERS);
    expect(total).toBe(MAX_OFFERS + 5);
  });
});
