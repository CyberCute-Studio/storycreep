// The fields of a variable (PRD FR-VAR-01, FR-VAR-02), which its panel's form
// and the dialog that creates one share: its type, its default, of the type
// that the form shows, its scope, global or a board's, its reset_on_enter,
// beside which the form says in plain words what becomes of its value as
// the story goes on (FR-VAR-02 AC5), its description, and its folder.

import type { Board, Variable } from "@storycreep/shared/api";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { typeName, ValueField } from "../content/ValueField";
import {
  convertedTo,
  type Value,
  variableTypes,
  zeroOf,
} from "../content/values";
import { FolderField, folderTop } from "../project/dialogs";
import type { Tree } from "../project/tree";
import {
  CheckboxField,
  GroupField,
  RadioField,
  SelectField,
  TextAreaField,
} from "../ui/Field";

/** What the fields of a variable start with. */
export interface VariableShown {
  type: Variable["type"];
  default: Value;
  scope: Variable["scope"];
  board_id?: string;
  reset_on_enter: boolean;
  description: string;
  folder_id?: string;
}

/** The plain words of what becomes of a variable's value as the story goes on (PRD FR-VAR-02 AC5). */
export function persistenceOf(
  t: ReturnType<typeof useTranslation>["t"],
  scope: Variable["scope"],
  reset: boolean,
  board: string | undefined,
): string {
  if (scope === "global") {
    return t("variable.persistence_global");
  }
  const named = board ?? t("variable.its_board");
  return reset
    ? t("variable.persistence_reset", { board: named })
    : t("variable.persistence_kept", { board: named });
}

export function VariableFields({
  shown,
  fresh = false,
  boards,
  tree,
  errors,
}: {
  shown: VariableShown;
  /**
   * Whether the fields are of a variable to create, whose default of
   * another type is that type's zero value, rather than the default as it
   * converts, as of a variable that has one already.
   */
  fresh?: boolean;
  /** The project's boards, which a board's variable is of one of, in the order of the tree. */
  boards: readonly Board[];
  tree: Tree<Variable>;
  /** What the form says at each of its fields, by the field's name. */
  errors: Readonly<Record<string, string>>;
}) {
  const { t } = useTranslation();
  const [type, setType] = useState(shown.type);
  const [scope, setScope] = useState(shown.scope);
  // The board that the user chose, or that the variable is of, or else the
  // first, as the list has it now, as once a board was made elsewhere of a
  // project that had none as the fields were shown.
  const [chosenBoard, setBoardId] = useState(shown.board_id ?? boards[0]?.id);
  const boardId = chosenBoard ?? boards[0]?.id ?? "";
  const [reset, setReset] = useState(shown.reset_on_enter);
  const board = boards.find((b) => b.id === boardId);
  // A board that the list does not have, as one that went to the trash
  // elsewhere, is shown as it is, as a select shows a value that none of its
  // options has, until the user chooses another.
  const known = boards.some((b) => b.id === boardId);
  return (
    <>
      <SelectField
        label={t("variable.type")}
        name="type"
        value={type}
        onChange={(event) =>
          setType(event.currentTarget.value as Variable["type"])
        }
        error={errors.type}
      >
        {variableTypes.map((choice) => (
          <option key={choice} value={choice}>
            {typeName(t, choice)}
          </option>
        ))}
      </SelectField>
      <ValueField
        // Another type shows the default as that type has it.
        key={type}
        label={t("variable.default")}
        hint={
          type === shown.type || fresh
            ? undefined
            : t("variable.default_retyped")
        }
        name="default"
        type={type}
        defaultValue={
          type === shown.type
            ? shown.default
            : fresh
              ? zeroOf(type)
              : convertedTo(type, shown.default)
        }
        error={errors.default}
      />
      <GroupField legend={t("variable.scope")} error={errors.scope}>
        <RadioField
          label={t("variable.scope_global")}
          name="scope"
          value="global"
          checked={scope === "global"}
          onChange={() => setScope("global")}
        />
        <RadioField
          label={t("variable.scope_board")}
          name="scope"
          value="board"
          checked={scope === "board"}
          disabled={boards.length === 0 && shown.scope !== "board"}
          onChange={() => setScope("board")}
        />
      </GroupField>
      {scope === "board" && (
        <SelectField
          label={t("variable.board")}
          name="board"
          value={boardId}
          onChange={(event) => setBoardId(event.currentTarget.value)}
          error={errors.board_id}
        >
          {!known && (
            <option value={boardId}>{t("boards.unknown_board")}</option>
          )}
          {boards.map((choice) => (
            <option key={choice.id} value={choice.id}>
              {choice.name}
            </option>
          ))}
        </SelectField>
      )}
      <CheckboxField
        label={t("variable.reset_on_enter")}
        hint={persistenceOf(t, scope, scope === "board" && reset, board?.name)}
        name="reset_on_enter"
        checked={scope === "board" && reset}
        disabled={scope !== "board"}
        onChange={(event) => setReset(event.currentTarget.checked)}
      />
      <TextAreaField
        label={t("variable.description")}
        name="description"
        defaultValue={shown.description}
        error={errors.description}
      />
      <FolderField
        tree={tree}
        label={t("content.folder")}
        initial={shown.folder_id}
        error={errors.folder_id}
      />
    </>
  );
}

/** What a form of a variable's fields holds, as its submission reads it. */
export interface VariableTyped {
  type: Variable["type"];
  /** The default as typed, which parseValue reads as the type. */
  defaultText: string;
  scope: Variable["scope"];
  boardId: string | undefined;
  reset: boolean;
  description: string;
  folderId: string | undefined;
}

/** Returns what the fields of a variable of data, a form's, hold. */
export function typedOf(data: FormData): VariableTyped {
  const scope = String(data.get("scope")) === "board" ? "board" : "global";
  const folder = String(data.get("folder") ?? folderTop);
  return {
    type: String(data.get("type")) as Variable["type"],
    defaultText: String(data.get("default") ?? ""),
    scope,
    boardId: scope === "board" ? String(data.get("board")) : undefined,
    reset: scope === "board" && data.get("reset_on_enter") !== null,
    description: String(data.get("description") ?? ""),
    folderId: folder === folderTop ? undefined : folder,
  };
}
