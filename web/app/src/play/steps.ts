// Play mode's history (PRD FR-PLAY-04, plan note N79): what each step of the
// flow did, as the panel lists it, and the whole as text, for a bug report
// (AC2), with the values that the flow holds at its end.

import type { TFunction } from "i18next";
import { endedMessage, errorMessage, expressionPlace } from "./messages";
import type {
  PlayError,
  PlayedProjection,
  PlayFlow,
  PlayStep,
} from "./session";
import { type GroupNames, shownValue, valueGroups, valueLabel } from "./values";

/** What the history names places by: where a node is, by its title and its board's name, or a board alone. */
export type Where = (
  board: string | undefined,
  node: string | undefined,
) => string;

/** What a step did, as the history lists it: its heading, and each line under it. */
export interface StepLines {
  readonly heading: string;
  readonly lines: readonly string[];
}

/**
 * Returns what the step of the index i of flow's history did: whether it
 * started or took a choice, the nodes that it passed and the one that it
 * came to, why the flow ended in it, the runtime errors that it reported,
 * and the values that the author set by hand after it.
 */
export function stepLines(
  t: TFunction,
  flow: PlayFlow,
  i: number,
  projection: PlayedProjection,
  where: Where,
  names: GroupNames,
): StepLines {
  const step: PlayStep = flow.history[i];
  const heading =
    step.choice === undefined
      ? t("play.history.started")
      : step.choice.text === ""
        ? t("play.history.continued")
        : t("play.history.chose", { choice: step.choice.text });
  const lines: string[] = [];
  step.entered.forEach((at, j) => {
    const place = where(at.board, at.node);
    lines.push(
      j === step.entered.length - 1
        ? t("play.history.came_to", { where: place })
        : t("play.history.passed", { where: place }),
    );
  });
  if (step.ended !== undefined) {
    lines.push(endedMessage(t, step.ended));
  }
  for (const e of flow.errors) {
    if (e.step === i) {
      lines.push(errorLine(t, e, where));
    }
  }
  for (const edit of step.edits) {
    const target = projection.valueByKey.get(edit.key);
    lines.push(
      t("play.history.set", {
        name: target === undefined ? edit.key : valueLabel(t, target, names),
        value: shownValue(edit.value),
      }),
    );
  }
  return { heading, lines };
}

/**
 * Returns the history of flow as text, for a bug report (FR-PLAY-04 AC2):
 * its title, of the project, each step numbered, with what it did, and
 * then each value that the flow holds now, in the inspector's order.
 */
export function historyText(
  t: TFunction,
  project: string,
  flow: PlayFlow,
  projection: PlayedProjection,
  where: Where,
  names: GroupNames,
): string {
  const out = [t("play.history.text_title", { project }), ""];
  flow.history.forEach((_, i) => {
    const { heading, lines } = stepLines(t, flow, i, projection, where, names);
    out.push(t("play.history.text_step", { number: i + 1, line: heading }));
    for (const line of lines) {
      out.push(`   ${line}`);
    }
  });
  const groups = valueGroups(t, projection.values, names);
  if (groups.length > 0) {
    out.push("", t("play.history.text_values"));
    for (const g of groups) {
      out.push(`   ${g.heading}`);
      for (const v of g.values) {
        const value = flow.values.get(v.key);
        out.push(
          `      ${t("play.history.text_value", {
            name: v.name,
            value: value === undefined ? "" : shownValue(value),
          })}`,
        );
      }
    }
  }
  return `${out.join("\n")}\n`;
}

// errorLine returns a runtime error as the history says it: its code, what
// it means, and where it is, if it is of an expression, a jumper, or a
// branch.
function errorLine(t: TFunction, e: PlayError, where: Where): string {
  const message = errorMessage(t, e.code);
  if (e.board === undefined) {
    return t("play.history.error", { code: e.code, message });
  }
  const at = where(e.board, e.node);
  return t("play.history.error_at", {
    code: e.code,
    message,
    place:
      e.expression === "" ? at : expressionPlace(t, e.owner, at, e.expression),
  });
}
