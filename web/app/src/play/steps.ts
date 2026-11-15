// Play mode's history (PRD FR-PLAY-04, plan note N79): what each step of the
// flow did, as the panel lists it, and the whole as text, for a bug report
// (AC2), with the values that the flow holds at its end; and what loading a
// saved play state could not keep, which the panel says, and the history
// with the start's step that loaded it (FR-PLAY-05 AC2, plan note N80).

import type { TFunction } from "i18next";
import { endedMessage, errorMessage, expressionPlace } from "./messages";
import type { LoadReport } from "./saved";
import type {
  PlayError,
  PlayedProjection,
  PlayFlow,
  PlayLoaded,
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
 * Returns what each step of flow's history did, in order: whether it
 * started or took a choice, the nodes that it passed and the one that it
 * came to, why the flow ended in it, the runtime errors that it reported,
 * and the values that the author set by hand after it, each followed by the
 * runtime errors of the choices built again of it, as they happened. It
 * reads the flow's runtime errors once, rather than once for each step.
 */
export function historyLines(
  t: TFunction,
  flow: PlayFlow,
  projection: PlayedProjection,
  where: Where,
  names: GroupNames,
): StepLines[] {
  // The runtime errors of each step's own, by the step's index, and of the
  // choices built again of each value set by hand after it, by the step's
  // index and the value's, in their order.
  const errors = new Map<string, PlayError[]>();
  for (const e of flow.errors) {
    const key = e.edit === undefined ? `${e.step}` : `${e.step} ${e.edit}`;
    const list = errors.get(key);
    if (list === undefined) {
      errors.set(key, [e]);
    } else {
      list.push(e);
    }
  }
  const errorLines = (key: string) =>
    (errors.get(key) ?? []).map((e) => errorLine(t, e, where));
  return flow.history.map((step, i) => {
    const heading =
      step.loaded !== undefined
        ? t("play.history.loaded", { name: step.loaded.name })
        : step.choice === undefined
          ? t("play.history.started")
          : step.choice.text === ""
            ? t("play.history.continued")
            : t("play.history.chose", { choice: step.choice.text });
    const lines: string[] =
      step.loaded === undefined
        ? []
        : reportLines(t, step.loaded.report, projection, where, names);
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
    lines.push(...errorLines(`${i}`));
    step.edits.forEach((edit, j) => {
      const target = projection.valueByKey.get(edit.key);
      lines.push(
        t("play.history.set", {
          name: target === undefined ? edit.key : valueLabel(t, target, names),
          value: shownValue(edit.value),
        }),
        ...errorLines(`${i} ${j}`),
      );
    });
    return { heading, lines };
  });
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
  historyLines(t, flow, projection, where, names).forEach(
    ({ heading, lines }, i) => {
      out.push(t("play.history.text_step", { number: i + 1, line: heading }));
      for (const line of lines) {
        out.push(`   ${line}`);
      }
    },
  );
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

/**
 * Returns what loading the saved play state loaded could not keep as it was
 * saved, as the panel and the history say it, one line for each value that
 * it dropped, defaulted, or retyped, in the order of the report, and one
 * of the visit counts that it dropped, and of where it started instead of
 * at its cursor; [] for a state that loaded whole.
 */
export function reportLines(
  t: TFunction,
  report: LoadReport,
  projection: PlayedProjection,
  where: Where,
  names: GroupNames,
): string[] {
  // name returns the name of the snapshot's value of the key, as the
  // inspector names it.
  const name = (key: string) => {
    const target = projection.valueByKey.get(key);
    return target === undefined ? key : valueLabel(t, target, names);
  };
  const lines = report.dropped.map((d) =>
    t("play.saved.dropped", { name: d.name ?? d.key }),
  );
  if (report.droppedVisits > 0) {
    lines.push(t("play.saved.dropped_visits", { count: report.droppedVisits }));
  }
  for (const key of report.defaulted) {
    lines.push(t("play.saved.defaulted", { name: name(key) }));
  }
  for (const key of report.retyped) {
    lines.push(t("play.saved.retyped", { name: name(key) }));
  }
  const c = report.cursor;
  if (c !== undefined) {
    lines.push(
      c.at === "board"
        ? t("play.saved.cursor_board", { board: where(c.board, undefined) })
        : c.at === "project"
          ? t("play.saved.cursor_project")
          : t("play.saved.cursor_none"),
    );
  }
  return lines;
}

/** Returns how many lines reportLines says of report. */
export function reportCount(report: LoadReport): number {
  return (
    report.dropped.length +
    (report.droppedVisits > 0 ? 1 : 0) +
    report.defaulted.length +
    report.retyped.length +
    (report.cursor === undefined ? 0 : 1)
  );
}

/** Returns the start's step's load of a saved play state, of flow, while it is the only step, or undefined. */
export function justLoaded(flow: PlayFlow): PlayLoaded | undefined {
  return flow.history.length === 1 ? flow.history[0].loaded : undefined;
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
