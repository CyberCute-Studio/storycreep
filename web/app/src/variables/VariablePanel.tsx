// The panel of a variable (PRD FR-VAR-01 to FR-VAR-04): its fields, which an
// editor changes in one batch, expecting the revision that the form shows,
// or reads; its rename, a refactor previewed first (rename.tsx); where it is
// used, its reads and its writes (usages.tsx); and its deletion to the
// trash, guarded by those usages (deletion.tsx). It shows the variable as
// the project's document has it, so a change that another session makes
// shows as it commits (FR-RT-02), but for what the user is editing, as the
// board's page does (useHeld).

import type { Board, ProjectBody, Variable } from "@storycreep/shared/api";
import { type FormEvent, useState } from "react";
import { useTranslation } from "react-i18next";
import { refusalOf } from "../auth/refusal";
import { DeleteContentDialog } from "../content/deletion";
import {
  ConflictNotice,
  changedOf,
  SaveStatus,
  useEntityForm,
} from "../content/form";
import { RenameKeyDialog } from "../content/rename";
import { Usages, variableUsagesQuery } from "../content/usages";
import { typeName, valueError, valueText } from "../content/ValueField";
import { parseValue, type Value } from "../content/values";
import { chosenFolder, payloadField } from "../project/dialogs";
import type { Command } from "../project/queries";
import type { Tree } from "../project/tree";
import { Button } from "../ui/Button";
import type { Opener } from "../ui/Dialog";
import { useFocusRefusal } from "../ui/Field";
import { Alert } from "../ui/Page";
import { persistenceOf, scopeName, typedOf, VariableFields } from "./fields";

/** The fields of a variable's commands' payloads that the form has, by their names in the payloads. */
const variableField = payloadField(
  "type",
  "default",
  "scope",
  "board_id",
  "reset_on_enter",
  "description",
  "folder_id",
);

/** What the panel shows of a variable, and what it does to it. */
export interface VariablePanelProps {
  project: ProjectBody;
  variable: Variable;
  boards: readonly Board[];
  tree: Tree<Variable>;
  editable: boolean;
  /** Tells whether the variable's deletion is open, while which the page shows the variable as it was. */
  onDeleting: (deleting: boolean) => void;
  /** Called once the variable has been deleted, which closes the panel. */
  onDeleted: () => void;
}

/** The contents of a variable's panel, under its heading. */
export function VariablePanel(props: VariablePanelProps) {
  const { t } = useTranslation();
  const { project, variable, editable } = props;
  return (
    <div className="space-y-6">
      {editable ? <VariableForm {...props} /> : <VariableView {...props} />}
      {editable && <VariableActions {...props} />}
      <Usages
        projectId={project.id}
        heading={t("variable.usages")}
        lists={[
          {
            key: "read",
            title: t("variable.reads"),
            empty: t("variable.reads_empty"),
            query: variableUsagesQuery(project.id, variable.id, "read"),
          },
          {
            key: "write",
            title: t("variable.writes"),
            empty: t("variable.writes_empty"),
            query: variableUsagesQuery(project.id, variable.id, "write"),
          },
        ]}
      />
    </div>
  );
}

/** Returns the name of the board of id of boards, or undefined for one that they do not have. */
function boardName(boards: readonly Board[], id: string | undefined) {
  return boards.find((b) => b.id === id)?.name;
}

/** A variable as a role that does not edit the project reads it, or as it is while the project is archived. */
function VariableView({ variable, boards, tree }: VariablePanelProps) {
  const { t } = useTranslation();
  const board = boardName(boards, variable.board_id);
  const folder =
    variable.folder_id === undefined
      ? undefined
      : tree.byId.get(variable.folder_id);
  return (
    <dl className="grid gap-x-6 gap-y-1 sm:grid-cols-[max-content_1fr]">
      <dt className="text-fg-muted">{t("variable.type")}</dt>
      <dd>{typeName(t, variable.type)}</dd>
      <dt className="text-fg-muted">{t("variable.default")}</dt>
      <dd className="font-mono break-all whitespace-pre-wrap">
        {valueText(t, variable.default)}
      </dd>
      <dt className="text-fg-muted">{t("variable.scope")}</dt>
      <dd>{scopeName(t, variable.scope, board)}</dd>
      <dt className="text-fg-muted">{t("variable.reset_on_enter_short")}</dt>
      <dd>
        {persistenceOf(t, variable.scope, variable.reset_on_enter, board)}
      </dd>
      <dt className="text-fg-muted">{t("variable.description")}</dt>
      <dd className="break-words whitespace-pre-line">
        {variable.description === ""
          ? t("board.no_description")
          : variable.description}
      </dd>
      <dt className="text-fg-muted">{t("content.folder")}</dt>
      <dd className="break-words">
        {folder?.kind === "folder" ? folder.folder.name : t("boards.top")}
      </dd>
    </dl>
  );
}

/** Returns the commands that change against, as the form showed it, to what the form holds, each expecting rev. */
function changesOf(
  against: Variable,
  typed: {
    type: Variable["type"];
    default: Value;
    scope: Variable["scope"];
    board_id: string | undefined;
    reset_on_enter: boolean;
    description: string;
    folder_id: string | undefined;
  },
  rev: number,
): Command[] {
  const id = against.id;
  const expected_rev = { [id]: rev };
  const changes: Command[] = [];
  // A variable whose reset_on_enter is set is a board's, so it is unset
  // before the variable becomes global, and set once it is a board's.
  if (against.reset_on_enter && !typed.reset_on_enter) {
    changes.push({
      type: "variable.set_reset_on_enter",
      payload: { id, reset_on_enter: false },
      expected_rev,
    });
  }
  if (
    typed.scope !== against.scope ||
    (typed.scope === "board" && typed.board_id !== against.board_id)
  ) {
    changes.push({
      type: "variable.set_scope",
      payload: {
        id,
        scope: typed.scope,
        ...(typed.board_id === undefined ? {} : { board_id: typed.board_id }),
      },
      expected_rev,
    });
  }
  if (typed.reset_on_enter && !against.reset_on_enter) {
    changes.push({
      type: "variable.set_reset_on_enter",
      payload: { id, reset_on_enter: true },
      expected_rev,
    });
  }
  if (typed.type !== against.type) {
    changes.push({
      type: "variable.retype",
      payload: { id, type: typed.type, default: typed.default },
      expected_rev,
    });
  } else if (typed.default !== against.default) {
    changes.push({
      type: "variable.set_default",
      payload: { id, default: typed.default },
      expected_rev,
    });
  }
  if (typed.description !== against.description) {
    changes.push({
      type: "variable.describe",
      payload: { id, description: typed.description },
      expected_rev,
    });
  }
  if (typed.folder_id !== against.folder_id) {
    changes.push({
      type: "variable.move",
      payload: {
        id,
        ...(typed.folder_id === undefined
          ? {}
          : { folder_id: typed.folder_id }),
      },
      expected_rev,
    });
  }
  return changes;
}

/**
 * The form of a variable's fields, which sends what of them changed in one
 * batch (useEntityForm), and refuses itself a default that is none of the
 * type that the form shows, before anything is sent.
 */
function VariableForm({ project, variable, boards, tree }: VariablePanelProps) {
  const { t } = useTranslation();
  const form = useEntityForm(project.id, variable, (view, id) =>
    view.variables.get(id),
  );
  const { commands, held, shown, against } = form;
  // A default that is none of its type, which the form refuses itself, with
  // a mark of its own for each attempt (useFocusRefusal).
  const [invalid, setInvalid] = useState<{ type: Variable["type"] }>();
  const refusal = refusalOf(commands.error, t, variableField, {
    key_taken: t("variable.error.key_taken_scope"),
  });
  useFocusRefusal(held.form, invalid ?? commands.error);
  const errors: Record<string, string> = {
    ...refusal?.fields,
    ...(invalid === undefined ? {} : { default: valueError(t, invalid.type) }),
  };
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const typed = typedOf(data);
    const value = parseValue(typed.type, typed.defaultText);
    if (value === undefined) {
      form.refused();
      setInvalid({ type: typed.type });
      return;
    }
    setInvalid(undefined);
    const values = {
      type: typed.type,
      default: value,
      scope: typed.scope,
      board_id: typed.boardId,
      reset_on_enter: typed.reset,
      description: typed.description,
      folder_id: chosenFolder(data, shown.folder_id),
    };
    form.save(
      changesOf(against, values, against.rev),
      changedOf(values, against),
    );
  };
  const fieldName = (field: string) => {
    switch (field) {
      case "type":
        return t("variable.type");
      case "default":
        return t("variable.default");
      case "scope":
        return t("variable.scope");
      case "board_id":
        return t("variable.board");
      case "reset_on_enter":
        return t("variable.reset_on_enter_short");
      case "description":
        return t("variable.description");
      case "key":
        return t("variable.key");
      // Its place in its folder, which a move changes with the folder.
      case "order":
        return t("content.place");
    }
    return t("content.folder");
  };
  const shownValue = (field: string, value: unknown) => {
    // A place in a folder is a key of the order, which reads as nothing.
    if (field === "order") {
      return undefined;
    }
    if (field === "folder_id") {
      const folder =
        typeof value === "string" ? tree.byId.get(value) : undefined;
      return folder?.kind === "folder" ? folder.folder.name : t("boards.top");
    }
    // A global variable is of no board, and a board that the list does not
    // have, as one in the trash, is named as the board's choice names it.
    if (field === "board_id") {
      return typeof value === "string"
        ? (boardName(boards, value) ?? t("boards.unknown_board"))
        : t("variable.scope_global");
    }
    if (field === "type") {
      return typeName(t, String(value));
    }
    // The scope and reset_on_enter as the form names its choices, rather
    // than as the API spells them.
    if (field === "scope") {
      return value === "global"
        ? t("variable.scope_global")
        : t("variable.scope_board");
    }
    if (field === "reset_on_enter") {
      return t("variable.reset_shown", {
        reset: value === true ? "yes" : "no",
      });
    }
    // A default as the view of the variable shows it, a bool's as its
    // choice names it.
    if (field === "default" && value !== undefined && value !== null) {
      return valueText(t, value as Value);
    }
    return value === undefined || value === null ? "" : String(value);
  };
  return (
    <div className="space-y-4">
      <Alert
        message={form.overlaps.length > 0 ? undefined : refusal?.message}
      />
      <ConflictNotice
        form={form}
        fieldName={fieldName}
        shownValue={shownValue}
      />
      <SaveStatus form={form} />
      {/* Shown anew once what it shows of the variable has changed, as once
          this form saved it, rather than showing what it was. */}
      <form
        key={held.key}
        {...held.props}
        className="space-y-4"
        onSubmit={submit}
        noValidate
      >
        <VariableFields
          shown={shown}
          boards={boards}
          tree={tree}
          errors={errors}
        />
        <Button type="submit" variant="primary" disabled={commands.isPending}>
          {t("variable.save")}
        </Button>
      </form>
    </div>
  );
}

/** A dialog of the variable's actions, with the control that opened it. */
type Open = { dialog: "rename" | "delete"; opener: Opener };

/** The variable's rename and its deletion, each in a dialog of its own. */
function VariableActions({
  project,
  variable,
  onDeleting,
  onDeleted,
}: VariablePanelProps) {
  const { t } = useTranslation();
  const [open, setOpen] = useState<Open>();
  return (
    <section className="space-y-2">
      <h3 className="text-lg font-semibold">{t("variable.key")}</h3>
      <p className="font-mono break-all">{variable.key}</p>
      <div className="flex flex-wrap gap-2">
        <Button
          aria-haspopup="dialog"
          onClick={(event) =>
            setOpen({ dialog: "rename", opener: event.currentTarget })
          }
        >
          {t("variable.rename")}
        </Button>
        <Button
          variant="danger"
          aria-haspopup="dialog"
          onClick={(event) =>
            setOpen({ dialog: "delete", opener: event.currentTarget })
          }
        >
          {t("variable.delete")}
        </Button>
      </div>
      {open?.dialog === "rename" && (
        <RenameKeyDialog
          projectId={project.id}
          renamed={{ kind: "variable", ...variable }}
          opener={open.opener}
          onClose={() => setOpen(undefined)}
        />
      )}
      {open?.dialog === "delete" && (
        <DeleteContentDialog
          project={project}
          deleted={{
            kind: "variable",
            id: variable.id,
            name: variable.key,
            rev: variable.rev,
          }}
          opener={open.opener}
          onClose={() => setOpen(undefined)}
          onDeleting={onDeleting}
          onDeleted={onDeleted}
        />
      )}
    </section>
  );
}
