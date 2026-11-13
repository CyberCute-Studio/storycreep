import type {
  Attribute,
  ComponentType,
  Variable,
} from "@storycreep/shared/api";
import type { TFunction } from "i18next";
import { describe, expect, test } from "vitest";
import type { ComponentEntry } from "../document/doc";
import { type NameSources, textChoices, valueNamer } from "./textNames";

const at = "2026-11-01T10:00:00Z";

function variable(
  id: string,
  key: string,
  more: Partial<Variable> = {},
): Variable {
  return {
    id,
    project_id: "prj_harbour",
    key,
    type: "int",
    default: 0,
    scope: "global",
    reset_on_enter: false,
    description: "",
    order: id,
    rev: 1,
    created_at: at,
    updated_at: at,
    ...more,
  };
}

function attribute(
  id: string,
  key: string,
  more: Partial<Attribute> = {},
): Attribute {
  return {
    id,
    component_type_id: "ct_character",
    key,
    name: key[0].toUpperCase() + key.slice(1),
    type: "int",
    default: 0,
    runtime: true,
    order: id,
    rev: 1,
    created_at: at,
    updated_at: at,
    ...more,
  };
}

const character: ComponentType = {
  id: "ct_character",
  project_id: "prj_harbour",
  key: "character",
  name: "Character",
  icon: "person",
  attributes: [
    attribute("atr_trust", "trust"),
    attribute("atr_mood", "mood", { runtime: false }),
  ],
  rev: 1,
  created_at: at,
  updated_at: at,
};

function component(
  id: string,
  key: string,
  name: string,
  more: Partial<ComponentEntry> = {},
): ComponentEntry {
  return {
    id,
    project_id: "prj_harbour",
    component_type_id: character.id,
    key,
    name,
    order: id,
    rev: 1,
    created_at: at,
    updated_at: at,
    ...more,
  };
}

const t = ((key: string, args?: Record<string, unknown>) =>
  args === undefined
    ? key
    : `${key}(${Object.values(args).join(",")})`) as unknown as TFunction;

/** Returns the names' sources of variables and components, of the type character. */
function sources(
  variables: Variable[],
  components: ComponentEntry[],
): NameSources {
  return {
    variables: new Map(variables.map((v) => [v.id, v])),
    components: new Map(components.map((c) => [c.id, c])),
    componentTypes: new Map([[character.id, character]]),
  };
}

describe("the names of a text's references", () => {
  test("name a variable by its key, and a component's attribute by the component's key and the attribute's, as an expression does", () => {
    const view = sources(
      [variable("var_gold", "gold")],
      [component("cmp_elara", "elara", "Elara")],
    );
    const valueName = valueNamer(view, "bd_a");
    expect(valueName("var_gold", undefined)).toBe("gold");
    expect(valueName("cmp_elara", "atr_trust")).toBe("elara.trust");
    // What the document does not have, as an attribute that the type does
    // not define, has no name.
    expect(valueName("var_gone", undefined)).toBeUndefined();
    expect(valueName("cmp_elara", "atr_gone")).toBeUndefined();
    expect(valueName("cmp_gone", "atr_trust")).toBeUndefined();
  });

  test("name a global variable and a component that a variable of the text's board shadows as an expression of the board names them, by global. and cmp.", () => {
    const view = sources(
      [
        variable("var_gold", "gold"),
        variable("var_gold_a", "gold", { scope: "board", board_id: "bd_a" }),
        variable("var_bram", "bram", { scope: "board", board_id: "bd_a" }),
        variable("var_elara", "elara"),
        variable("var_tide", "tide"),
        variable("var_tide_a", "tide", {
          scope: "board",
          board_id: "bd_a",
          deleted_at: at,
        }),
      ],
      [
        component("cmp_elara", "elara", "Elara"),
        component("cmp_bram", "bram", "Bram"),
      ],
    );
    const onBoard = valueNamer(view, "bd_a");
    // The bare key names the board's variable there.
    expect(onBoard("var_gold_a", undefined)).toBe("gold");
    expect(onBoard("var_gold", undefined)).toBe("global.gold");
    // A component whose key a variable that the board reads has, the
    // board's or a global one.
    expect(onBoard("cmp_bram", "atr_trust")).toBe("cmp.bram.trust");
    expect(onBoard("cmp_elara", "atr_trust")).toBe("cmp.elara.trust");
    // A variable in the trash shadows nothing.
    expect(onBoard("var_tide", undefined)).toBe("tide");
    // Of another board, or of none, as a component's description, the
    // board's variables shadow nothing, but the global ones still do.
    for (const elsewhere of [
      valueNamer(view, "bd_b"),
      valueNamer(view, undefined),
    ]) {
      expect(elsewhere("var_gold", undefined)).toBe("gold");
      expect(elsewhere("cmp_bram", "atr_trust")).toBe("bram.trust");
      expect(elsewhere("cmp_elara", "atr_trust")).toBe("cmp.elara.trust");
    }
    // What { offers is named as the references show it.
    expect(
      textChoices(view, "bd_a", t).values.map((v) => [v.name, v.ref]),
    ).toEqual([
      ["bram", "var_bram"],
      ["cmp.bram.trust", "cmp_bram"],
      ["cmp.elara.trust", "cmp_elara"],
      ["elara", "var_elara"],
      ["global.gold", "var_gold"],
      ["gold", "var_gold_a"],
      ["tide", "var_tide"],
    ]);
  });

  test("{ offers the global variables, the board's own, and the components' runtime attributes, and @ every component, each in the order of the names", () => {
    const view = sources(
      [
        variable("var_gold", "gold"),
        variable("var_ale", "ale"),
        variable("var_tide", "tide", { scope: "board", board_id: "bd_a" }),
        variable("var_fog", "fog", { scope: "board", board_id: "bd_b" }),
        variable("var_old", "old", { deleted_at: at }),
      ],
      [
        component("cmp_elara", "elara", "Elara"),
        component("cmp_bram", "bram", "Bram"),
        component("cmp_gone", "gone", "Gone", { deleted_at: at }),
      ],
    );
    const onBoard = textChoices(view, "bd_a", t);
    expect(
      onBoard.values.map((v) => [v.name, v.ref, v.attribute, v.detail]),
    ).toEqual([
      ["ale", "var_ale", undefined, "richtext.suggest.global"],
      [
        "bram.trust",
        "cmp_bram",
        "atr_trust",
        "richtext.suggest.attribute(Trust,Bram)",
      ],
      [
        "elara.trust",
        "cmp_elara",
        "atr_trust",
        "richtext.suggest.attribute(Trust,Elara)",
      ],
      ["gold", "var_gold", undefined, "richtext.suggest.global"],
      ["tide", "var_tide", undefined, "richtext.suggest.board"],
    ]);
    expect(onBoard.values[2].terms).toEqual(["Elara Trust"]);
    expect(
      onBoard.mentions.map((m) => [m.name, m.id, m.detail, m.terms]),
    ).toEqual([
      ["Bram", "cmp_bram", "Character", ["bram"]],
      ["Elara", "cmp_elara", "Character", ["elara"]],
    ]);
    // A text of no board, as a component's description, reads no board's
    // variables.
    expect(textChoices(view, undefined, t).values.map((v) => v.name)).toEqual([
      "ale",
      "bram.trust",
      "elara.trust",
      "gold",
    ]);
  });
});
