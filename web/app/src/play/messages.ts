// What play mode says of the flow (PRD FR-PLAY-01, §5.8.7, plan note N78):
// why it ended, each runtime error, and what the projection stood in for,
// each a message of the catalog, by a key that extraction reads.

import type { ExpressionField, RuntimeError } from "@storycreep/shared/flow";
import type { TFunction } from "i18next";
import type { ExpressionOwner, StandIn } from "./projection";
import type { PlayEnd } from "./session";

/** Returns why the flow ended, as play mode says it (§5.8.5), or that it had ended where a saved play state was saved (FR-PLAY-05). */
export function endedMessage(t: TFunction, reason: PlayEnd): string {
  switch (reason) {
    case "ending":
      return t("play.ended.ending");
    case "dead_end":
      return t("play.ended.dead_end");
    case "no_choices":
      return t("play.ended.no_choices");
    case "saved":
      return t("play.ended.saved");
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
export function fieldName(t: TFunction, field: ExpressionField): string {
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
 * Returns where an expression of field is, as the error panel says where a
 * runtime error is, of where, the name of the node whose it is, of the node
 * that its connection leaves, or of its output's branch, by owner, what it
 * is of: so that a choice's condition does not read as its node's.
 */
export function expressionPlace(
  t: TFunction,
  owner: ExpressionOwner | undefined,
  where: string,
  field: ExpressionField,
): string {
  const named = { where, field: fieldName(t, field) };
  switch (owner) {
    case "choice":
      return t("play.where_choice_field", named);
    case "connection":
      return t("play.where_connection_field", named);
    case "output":
      return t("play.where_output_field", named);
    case "node":
    case undefined:
      return t("play.where_field", named);
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
      if (s.field === "onTraverse") {
        return s.owner === "choice"
          ? t("play.stand_in.traverse", { where })
          : t("play.stand_in.connection_traverse", { where });
      }
      return t("play.stand_in.script", {
        where,
        field: fieldName(t, s.field),
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
