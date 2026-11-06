// The dialog that creates a variable (PRD FR-VAR-01): its key, and the
// fields that its panel has, in the folder that it was opened for.

import type { Board, Variable } from "@storycreep/shared/api";
import { newId } from "@storycreep/shared/ids";
import { Dialog } from "radix-ui";
import { type FormEvent, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { refusalOf } from "../auth/refusal";
import { valueError } from "../content/ValueField";
import { parseValue } from "../content/values";
import { payloadField } from "../project/dialogs";
import { useCommands } from "../project/queries";
import type { Tree } from "../project/tree";
import {
  closesBy,
  DialogFrame,
  FormButtons,
  focusBackUnlessCreated,
  type Opener,
} from "../ui/Dialog";
import { TextField, useFocusRefusal } from "../ui/Field";
import { Alert } from "../ui/Page";
import { typedOf, VariableFields } from "./fields";

const createField = payloadField(
  "key",
  "type",
  "default",
  "scope",
  "board_id",
  "reset_on_enter",
  "description",
  "folder_id",
);

export function CreateVariableDialog({
  projectId,
  boards,
  tree,
  parentId,
  opener,
  onClose,
  onCreated,
}: {
  projectId: string;
  boards: readonly Board[];
  tree: Tree<Variable>;
  /** The folder that the variable is created in, or undefined for the top. */
  parentId: string | undefined;
  opener: Opener;
  onClose: () => void;
  /** Called once the variable has been created, with its ID and its key. */
  onCreated: (id: string, key: string) => void;
}) {
  const { t } = useTranslation();
  const form = useRef<HTMLFormElement>(null);
  // The ID of the variable, the same for each attempt, so that an attempt
  // sent again once no answer came to it creates nothing more.
  const [id] = useState(() => newId("variable"));
  // Whether the variable has been created, whose panel, which the page opens
  // then, has the focus once the dialog has closed.
  const created = useRef(false);
  const [invalid, setInvalid] = useState<{ type: Variable["type"] }>();
  const commands = useCommands(projectId);
  const refusal = refusalOf(commands.error, t, createField, {
    key_taken: t("variable.error.key_taken"),
  });
  useFocusRefusal(form, invalid ?? commands.error);
  const errors: Record<string, string> = {
    ...refusal?.fields,
    ...(invalid === undefined ? {} : { default: valueError(t, invalid.type) }),
  };
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    // Without the white space around it, as the dialog that creates a
    // component reads its key.
    const key = String(data.get("key")).trim();
    const typed = typedOf(data);
    const value = parseValue(typed.type, typed.defaultText);
    if (value === undefined) {
      commands.reset();
      setInvalid({ type: typed.type });
      return;
    }
    setInvalid(undefined);
    commands.mutate(
      [
        {
          type: "variable.create",
          payload: {
            id,
            key,
            type: typed.type,
            default: value,
            scope: typed.scope,
            ...(typed.boardId === undefined ? {} : { board_id: typed.boardId }),
            ...(typed.reset ? { reset_on_enter: true } : {}),
            ...(typed.description === ""
              ? {}
              : { description: typed.description }),
            ...(typed.folderId === undefined
              ? {}
              : { folder_id: typed.folderId }),
          },
        },
      ],
      {
        onSuccess: () => {
          created.current = true;
          onCreated(id, key);
          onClose();
        },
      },
    );
  };
  return (
    <Dialog.Root open onOpenChange={closesBy(onClose, commands.isPending)}>
      <DialogFrame
        title={t("variable.create_title")}
        onCloseAutoFocus={focusBackUnlessCreated(opener, created)}
      >
        <form ref={form} className="space-y-4" onSubmit={submit} noValidate>
          <Alert message={refusal?.message} />
          <TextField
            label={t("variable.key")}
            hint={t("content.key_hint")}
            name="key"
            autoComplete="off"
            spellCheck={false}
            required
            maxLength={64}
            error={errors.key}
          />
          <VariableFields
            fresh
            shown={{
              type: "bool",
              default: false,
              scope: "global",
              reset_on_enter: false,
              description: "",
              folder_id: parentId,
            }}
            boards={boards}
            tree={tree}
            errors={errors}
          />
          <FormButtons
            cancel={t("boards.cancel")}
            pending={commands.isPending}
            submit={t("variable.create")}
          />
        </form>
      </DialogFrame>
    </Dialog.Root>
  );
}
