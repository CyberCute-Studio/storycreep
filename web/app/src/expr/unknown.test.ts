import type { Context } from "@storycreep/shared/expr/compile";
import { describe, expect, it } from "vitest";
import { analyze } from "./analyze";
import { harbourScope } from "./testdata";
import { unknownOf } from "./unknown";

// The variable that an unknown name of a source would be (PRD FR-EXPR-01,
// plan note N74): its key, the scope that it is written in, and the type
// that its place suggests, which the editor offers to create it with.

const scope = harbourScope();

/** Returns what the unknown name of source, of context, would be. */
function unknown(source: string, context: Context = "condition") {
  return unknownOf(analyze(source, context, scope), scope);
}

describe("an unknown name's variable", () => {
  it("is of the type of a literal that it is compared with, assigned, or added to, and a bool alone or of and, or, and not", () => {
    expect(unknown("met_mira")).toEqual({ key: "met_mira", type: "bool" });
    expect(unknown("not met_mira")?.type).toBe("bool");
    expect(unknown("gold > 1 and met_mira")?.type).toBe("bool");
    expect(unknown('mood == "calm"')?.type).toBe("string");
    expect(unknown("drift < 0.5")?.type).toBe("float");
    expect(unknown("drift = 2", "script")?.type).toBe("int");
    expect(unknown('motto += "!"', "script")?.type).toBe("string");
    expect(unknown("board.seen")).toEqual({
      key: "seen",
      scope: "board",
      type: "bool",
    });
  });

  it("is a float where a float is its value, or arithmetic's other operand, a minus before it too, and else an int", () => {
    expect(unknown("drift = -0.5", "script")?.type).toBe("float");
    expect(unknown("drift = -2", "script")?.type).toBe("int");
    expect(unknown("drift += 0.5", "script")?.type).toBe("float");
    expect(unknown("drift -= 1", "script")?.type).toBe("int");
    expect(unknown("drift * 1.5 > 2")?.type).toBe("float");
    expect(unknown("drift * 2 > 2")?.type).toBe("int");
    expect(unknown("drift > -1.5")?.type).toBe("float");
  });

  it("is of the type of a name of the board that it is assigned to, compared with, or added to, as the checker resolves the name", () => {
    // keeper is a string; trust, a bare key, the board's whole number, and
    // global.trust a float; elara.trust a whole number of the component.
    expect(unknown("keeper = greeting", "script")?.type).toBe("string");
    expect(unknown("keeper += greeting", "script")?.type).toBe("string");
    expect(unknown("global.trust = drift", "script")?.type).toBe("float");
    expect(unknown("trust *= drift", "script")?.type).toBe("int");
    expect(unknown("greeting = keeper", "script")?.type).toBe("string");
    expect(unknown("mood == keeper")?.type).toBe("string");
    expect(unknown("met_elara or seen")?.type).toBe("bool");
    expect(unknown("drift < global.trust")?.type).toBe("float");
    expect(unknown("elara.trust > drift")?.type).toBe("int");
    // Without the scope, a name says nothing of its type.
    expect(unknownOf(analyze("keeper = greeting", "script", scope))?.type).toBe(
      "int",
    );
  });
});
