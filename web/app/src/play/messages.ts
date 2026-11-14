// What play mode says of the flow (PRD FR-PLAY-01, §5.8.7, plan note N78):
// why it ended, each runtime error, and what the projection stood in for,
// each a message of the catalog, by a key that extraction reads.

import type { Reason, RuntimeError } from "@storycreep/shared/flow";
import type { TFunction } from "i18next";
import type { StandIn } from "./projection";

/** Returns why the flow ended, as play mode says it (§5.8.5). */
export function endedMessage(t: TFunction, reason: Reason): string {
  switch (reason) {
    case "ending":
      return t("play.ended.ending");
    case "dead_end":
      return t("play.ended.dead_end");
    case "no_choices":
      return t("play.ended.no_choices");
  }
}

/** Returns what a runtime error of the code is, as the error panel says it (§5.8.7). */
export function errorMessage(t: TFunction, code: RuntimeError): string {
  switch (code) {
    case "division_by_zero":
      return t("play.error.division_by_zero");
    case "integer_overflow":
      return t("play.error.integer_overflow");
    case "float_not_finite":
      return t("play.error.float_not_finite");
    case "string_too_long":
      return t("play.error.string_too_long");
    case "conversion_failed":
      return t("play.error.conversion_failed");
    case "step_budget_exceeded":
      return t("play.error.step_budget_exceeded");
    case "jumper_broken":
      return t("play.error.jumper_broken");
    case "flow_loop":
      return t("play.error.flow_loop");
    case "unknown_node":
      return t("play.error.unknown_node");
    case "invalid_choice":
      return t("play.error.invalid_choice");
    case "cannot_advance":
      return t("play.error.cannot_advance");
    case "flow_halted":
      return t("play.error.flow_halted");
  }
}

/** Returns the name of an expression's field, as play mode names where an error or a stand-in is. */
export function fieldName(
  t: TFunction,
  field: "onEnter" | "onExit" | "onTraverse" | "condition",
): string {
  switch (field) {
    case "onEnter":
      return t("play.field.on_enter");
    case "onExit":
      return t("play.field.on_exit");
    case "onTraverse":
      return t("play.field.on_traverse");
    case "condition":
      return t("play.field.condition");
  }
}

/**
 * Returns what the projection stood in for, and how, as play mode lists it
 * (N78), of where, the name of the node, of the node that a connection
 * leaves, of an output's branch, or of the board, that it is of.
 */
export function standInMessage(
  t: TFunction,
  s: StandIn,
  where: string,
): string {
  switch (s.kind) {
    case "expression":
      if (s.code === "") {
        return t("play.stand_in.no_condition", { where });
      }
      if (s.field === "condition") {
        switch (s.owner) {
          case "choice":
            return t("play.stand_in.choice_condition", { where });
          case "connection":
            return t("play.stand_in.connection_condition", { where });
          default:
            return t("play.stand_in.output_condition", { where });
        }
      }
      if (s.field === "on_traverse") {
        return s.owner === "choice"
          ? t("play.stand_in.traverse", { where })
          : t("play.stand_in.connection_traverse", { where });
      }
      return t("play.stand_in.script", {
        where,
        field: fieldName(t, s.field === "on_enter" ? "onEnter" : "onExit"),
      });
    case "reference":
      return referenceMessage(t, s, where);
    case "text":
      return s.field === "text"
        ? t("play.stand_in.text", { where })
        : t("play.stand_in.label", { where });
    case "speaker":
      return t("play.stand_in.speaker", { where });
    case "target":
      return s.reason === "gone"
        ? t("play.stand_in.target", { where })
        : s.reason === "jumper"
          ? t("play.stand_in.target_jumper", { where })
          : t("play.stand_in.target_board", { where });
    case "board_entry":
      return t("play.stand_in.board_entry", { where });
    case "entry":
      return s.reason === "gone"
        ? t("play.stand_in.entry")
        : t("play.stand_in.entry_board");
    case "connection":
      return s.reason === "port"
        ? t("play.stand_in.connection", { where })
        : t("play.stand_in.connection_target", { where });
  }
}

// referenceMessage returns standInMessage's of a value reference or a
// mention left out of a text or a label, by why.
function referenceMessage(
  t: TFunction,
  s: Extract<StandIn, { kind: "reference" }>,
  where: string,
): string {
  const text = s.field === "text";
  switch (s.reason) {
    case "gone":
      return text
        ? t("play.stand_in.reference_text", { where })
        : t("play.stand_in.reference_label", { where });
    case "board":
      return text
        ? t("play.stand_in.board_variable_text", { where })
        : t("play.stand_in.board_variable_label", { where });
    case "runtime":
      return text
        ? t("play.stand_in.not_runtime_text", { where })
        : t("play.stand_in.not_runtime_label", { where });
  }
}
