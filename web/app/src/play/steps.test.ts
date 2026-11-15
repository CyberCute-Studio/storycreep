import { intValue, type Value } from "@storycreep/shared/expr/compile";
import { expect, test } from "vitest";
import { createAppI18n } from "../i18n";
import type { ProjectedValue } from "./projection";
import type {
  PlayError,
  PlayedProjection,
  PlayFlow,
  PlayStep,
} from "./session";
import { historyLines, historyText, type Where } from "./steps";
import type { GroupNames } from "./values";

// Play mode's history as the panel lists it and as its text writes it (PRD
// FR-PLAY-04, plan note N79), of a flow and a projection made by hand, with
// the page's messages.

const t = createAppI18n(["en"], document.documentElement).t;

// A variable of the projection, global, or of the board.
const variable = (
  key: string,
  name: string,
  board?: string,
): ProjectedValue => ({
  kind: "variable",
  key,
  variable: key,
  name,
  type: "int",
  options: [],
  ...(board === undefined ? {} : { board }),
});

// What a projection holds of values, which is all that the history reads of
// it.
function projectionOf(values: readonly ProjectedValue[]): PlayedProjection {
  return {
    values,
    valueByKey: new Map(values.map((v) => [v.key, v])),
  } as unknown as PlayedProjection;
}

// A flow of history and errors, its values those given.
function flowOf(
  history: readonly PlayStep[],
  errors: readonly PlayError[],
  values: ReadonlyMap<string, Value> = new Map(),
): PlayFlow {
  return { history, errors, values } as unknown as PlayFlow;
}

const where: Where = (board, node) =>
  node === undefined ? (board ?? "") : `${node} on ${board}`;
const names: GroupNames = { board: (id) => id, component: (id) => id };

// A runtime error of the choices built again of a value set by hand is
// listed after that value, which caused it, and a step's own before the
// values set after it, as they happened.
test("lists a runtime error of a value set by hand after that value", () => {
  const gold = variable("var_gold", "gold");
  const division = (step: number, edit?: number): PlayError => ({
    code: "division_by_zero",
    at: "cn_1",
    expression: "condition",
    board: "Harbour",
    node: "Quay",
    owner: "choice",
    step,
    ...(edit === undefined ? {} : { edit }),
  });
  const flow = flowOf(
    [
      {
        entered: [{ board: "Harbour", node: "Quay" }],
        edits: [
          { key: gold.key, value: intValue(2) },
          { key: gold.key, value: intValue(0) },
        ],
      },
    ],
    [division(0), division(0, 1)],
  );
  const [step] = historyLines(t, flow, projectionOf([gold]), where, names);
  expect(step.lines).toEqual([
    "Came to Quay on Harbour",
    expect.stringMatching(/^Runtime error division_by_zero at /),
    "Set gold to 2 by hand",
    "Set gold to 0 by hand",
    expect.stringMatching(/^Runtime error division_by_zero at /),
  ]);
});

// The history's text writes the values in the inspector's order, the
// groups' names and the values' compared by one collation, so that a name
// in capitals sorts among the others either way.
test("writes the values in groups and names of one order", () => {
  const values = [
    variable("var_1", "Zeal"),
    variable("var_2", "apple"),
    variable("var_3", "Zeal", "Zoo"),
    variable("var_4", "apple", "Zoo"),
    variable("var_5", "x", "ark"),
  ];
  const text = historyText(
    t,
    "Tides",
    flowOf([], [], new Map(values.map((v, i) => [v.key, intValue(i)]))),
    projectionOf(values),
    where,
    names,
  );
  expect(text.split("\n").slice(3)).toEqual([
    "Values now:",
    "   Global variables",
    "      apple = 1",
    "      Zeal = 0",
    "   Variables of ark",
    "      x = 4",
    "   Variables of Zoo",
    "      apple = 3",
    "      Zeal = 2",
    "",
  ]);
});
