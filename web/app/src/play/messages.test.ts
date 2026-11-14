import type { TFunction } from "i18next";
import { expect, test } from "vitest";
import { standInMessage } from "./messages";
import type { StandIn } from "./projection";

// A t that returns the key of the message that it is asked for.
const t = ((key: string) => key) as unknown as TFunction;

// Each stand-in says why the projection stood in for it, by what it is of
// and why it is left out, so that the author is told what to fix (N78).
test.each<[StandIn, string]>([
  [
    {
      kind: "expression",
      entity: "cn_1",
      owner: "choice",
      field: "condition",
      source: "x >",
      code: "unexpected_end",
    },
    "play.stand_in.choice_condition",
  ],
  [
    {
      kind: "expression",
      entity: "cn_1",
      owner: "connection",
      field: "condition",
      source: "x >",
      code: "unexpected_end",
    },
    "play.stand_in.connection_condition",
  ],
  [
    {
      kind: "expression",
      entity: "bo_1",
      owner: "output",
      field: "condition",
      source: "x >",
      code: "unexpected_end",
    },
    "play.stand_in.output_condition",
  ],
  [
    {
      kind: "expression",
      entity: "bo_1",
      owner: "output",
      field: "condition",
      source: "",
      code: "",
    },
    "play.stand_in.no_condition",
  ],
  [
    {
      kind: "expression",
      entity: "cn_1",
      owner: "choice",
      field: "on_traverse",
      source: "x +=",
      code: "unexpected_end",
    },
    "play.stand_in.traverse",
  ],
  [
    {
      kind: "expression",
      entity: "cn_1",
      owner: "connection",
      field: "on_traverse",
      source: "x +=",
      code: "unexpected_end",
    },
    "play.stand_in.connection_traverse",
  ],
  [
    {
      kind: "reference",
      entity: "nd_1",
      field: "text",
      ref: "var_1",
      reason: "gone",
    },
    "play.stand_in.reference_text",
  ],
  [
    {
      kind: "reference",
      entity: "cn_1",
      field: "label",
      ref: "var_1",
      reason: "gone",
    },
    "play.stand_in.reference_label",
  ],
  [
    {
      kind: "reference",
      entity: "nd_1",
      field: "text",
      ref: "var_1",
      reason: "board",
    },
    "play.stand_in.board_variable_text",
  ],
  [
    {
      kind: "reference",
      entity: "cn_1",
      field: "label",
      ref: "var_1",
      reason: "board",
    },
    "play.stand_in.board_variable_label",
  ],
  [
    {
      kind: "reference",
      entity: "nd_1",
      field: "text",
      ref: "cmp_1",
      reason: "runtime",
    },
    "play.stand_in.not_runtime_text",
  ],
  [
    {
      kind: "reference",
      entity: "cn_1",
      field: "label",
      ref: "cmp_1",
      reason: "runtime",
    },
    "play.stand_in.not_runtime_label",
  ],
  [
    { kind: "connection", entity: "cn_1", reason: "port" },
    "play.stand_in.connection",
  ],
  [
    { kind: "connection", entity: "cn_1", reason: "target" },
    "play.stand_in.connection_target",
  ],
  [
    { kind: "target", entity: "nd_1", ref: "nd_2", reason: "gone" },
    "play.stand_in.target",
  ],
  [
    { kind: "target", entity: "nd_1", ref: "nd_2", reason: "jumper" },
    "play.stand_in.target_jumper",
  ],
  [
    { kind: "target", entity: "nd_1", ref: "nd_2", reason: "board" },
    "play.stand_in.target_board",
  ],
  [
    { kind: "entry", entity: "prj_1", ref: "nd_2", reason: "gone" },
    "play.stand_in.entry",
  ],
  [
    { kind: "entry", entity: "prj_1", ref: "nd_2", reason: "board" },
    "play.stand_in.entry_board",
  ],
])("%j says %s", (s, key) => {
  expect(standInMessage(t, s, "where")).toBe(key);
});
