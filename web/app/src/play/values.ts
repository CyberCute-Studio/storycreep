// How play mode shows a value of the flow's state (PRD FR-PLAY-02,
// FR-PLAY-03, FR-PLAY-04, plan note N79), in its inspector, its author view
// of hidden choices, and its history's text, and how its inspector reads
// what the author typed as a value of a variable's or an attribute's type:
// of what the board's page loads at once, which the evaluator is not.

import {
  boolValue,
  floatValue,
  intValue,
  stringValue,
  type Value,
  valueString,
} from "@storycreep/shared/expr/compile";
import type { TFunction } from "i18next";
import { collator } from "../content/textNames";
import { parseValue } from "../content/values";
import type { ProjectedValue } from "./projection";

/** A group of the inspector's values: its heading, and its values in the order that it lists them. */
export interface ValueGroup {
  /** The board's or the component's ID, or "" for the global variables. */
  readonly id: string;
  /** The board's or the component's name, which the filter matches too, or "" for the global variables. */
  readonly name: string;
  readonly heading: string;
  readonly values: readonly ProjectedValue[];
}

/** What names the groups of values: a board's name, and a component's, by their IDs. */
export interface GroupNames {
  board(id: string): string;
  component(id: string): string;
}

/**
 * Returns values in the groups that the inspector lists them in, and the
 * history's text writes them in: the global variables, by their names;
 * then each board's variables, by the boards' names, and the variables'
 * within; then each component's runtime attributes, by the components'
 * names, in the order of their type's definitions. Names are compared by
 * one collation, the page's (collator), the groups' as the values', and
 * those that it holds equal by their code units, and then by their IDs. A
 * group without values is left out.
 */
export function valueGroups(
  t: TFunction,
  values: readonly ProjectedValue[],
  names: GroupNames,
): ValueGroup[] {
  const byCodeUnits = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0);
  const byName = (a: ProjectedValue, b: ProjectedValue) =>
    collator.compare(a.name, b.name) ||
    byCodeUnits(a.name, b.name) ||
    byCodeUnits(a.key, b.key);
  const globals: ProjectedValue[] = [];
  const boards = new Map<string, ProjectedValue[]>();
  const components = new Map<string, ProjectedValue[]>();
  const into = (
    m: Map<string, ProjectedValue[]>,
    id: string,
    v: ProjectedValue,
  ) => {
    const list = m.get(id);
    if (list === undefined) {
      m.set(id, [v]);
    } else {
      list.push(v);
    }
  };
  for (const v of values) {
    if (v.kind === "attribute") {
      into(components, v.component, v);
    } else if (v.board === undefined) {
      globals.push(v);
    } else {
      into(boards, v.board, v);
    }
  }
  const named = (
    m: Map<string, ProjectedValue[]>,
    name: (id: string) => string,
    heading: (name: string) => string,
    sorted: boolean,
  ): ValueGroup[] =>
    [...m]
      .map(([id, list]) => ({ id, name: name(id), list }))
      .sort(
        (a, b) =>
          collator.compare(a.name, b.name) ||
          byCodeUnits(a.name, b.name) ||
          byCodeUnits(a.id, b.id),
      )
      .map(({ id, name, list }) => ({
        id,
        name,
        heading: heading(name),
        values: sorted ? [...list].sort(byName) : list,
      }));
  return [
    ...(globals.length === 0
      ? []
      : [
          {
            id: "",
            name: "",
            heading: t("play.inspector.globals"),
            values: globals.sort(byName),
          },
        ]),
    ...named(
      boards,
      names.board,
      (board) => t("play.inspector.board", { board }),
      true,
    ),
    ...named(
      components,
      names.component,
      (component) => t("play.inspector.component", { component }),
      false,
    ),
  ];
}

/**
 * Returns the name of the value target as play shows it apart from its
 * group, as the history does: its name, as an expression names it, and of
 * a board's variable, its board's name too, as two boards' variables may
 * share a key.
 */
export function valueLabel(
  t: TFunction,
  target: ProjectedValue,
  names: GroupNames,
): string {
  return target.kind === "variable" && target.board !== undefined
    ? t("play.value_of_board", {
        name: target.name,
        board: names.board(target.board),
      })
    : target.name;
}

/**
 * Returns v as the author view and the history's text show a value: a
 * string between double quotes, with JSON's escapes, so that one of spaces,
 * or none, shows, and any other value as to_string writes it (§5.8.9).
 */
export function shownValue(v: Value): string {
  return v.type === "string" ? JSON.stringify(v.str) : valueString(v);
}

/**
 * Returns the value of the type of target that text spells, as a field of
 * a variable's default reads one (parseValue), or undefined for none: true
 * or false, a whole number within ±(2^53 − 1), a finite number, one of an
 * enum's options, or any string, whose length and characters the runtime
 * checks as it sets it.
 */
export function typedValue(
  target: Pick<ProjectedValue, "type" | "options">,
  text: string,
): Value | undefined {
  const v = parseValue(target.type, text, target.options);
  if (v === undefined) {
    return undefined;
  }
  switch (target.type) {
    case "bool":
      return boolValue(v as boolean);
    case "int":
      return intValue(v as number);
    case "float":
      return floatValue(v as number);
  }
  return stringValue(String(v));
}
