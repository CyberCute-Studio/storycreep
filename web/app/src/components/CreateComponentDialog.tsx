// The dialog that creates a component (PRD FR-COMP-02): its name, its key,
// which the server derives from the name unless the user gives one, its
// type, which is fixed once it is created, and its folder. Its values are
// its type's defaults.

import type { ComponentType } from "@storycreep/shared/api";
import { newId } from "@storycreep/shared/ids";
import { Dialog } from "radix-ui";
import { type FormEvent, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { refusalOf } from "../auth/refusal";
import type { ComponentEntry } from "../document/doc";
import { FolderField, folderTop, payloadField } from "../project/dialogs";
import { useCommands } from "../project/queries";
import type { Tree } from "../project/tree";
import {
  closesBy,
  DialogFrame,
  FormButtons,
  focusBackUnlessCreated,
  type Opener,
} from "../ui/Dialog";
import { SelectField, TextField, useFocusRefusal } from "../ui/Field";
import { Alert } from "../ui/Page";

const createField = payloadField(
  "name",
  "key",
  "component_type_id",
  "folder_id",
);

export function CreateComponentDialog({
  projectId,
  types,
  tree,
  typeId,
  parentId,
  opener,
  onClose,
  onCreated,
}: {
  projectId: string;
  /** The project's component types, in the order that the choice lists them. */
  types: readonly ComponentType[];
  tree: Tree<ComponentEntry>;
  /** The type that the choice starts at, as the list's filter's, if any. */
  typeId: string | undefined;
  /** The folder that the component is created in, or undefined for the top. */
  parentId: string | undefined;
  opener: Opener;
  onClose: () => void;
  /** Called once the component has been created, with its ID and its name. */
  onCreated: (id: string, name: string) => void;
}) {
  const { t } = useTranslation();
  const form = useRef<HTMLFormElement>(null);
  const [id] = useState(() => newId("component"));
  // Whether the component has been created, whose panel, which the page opens
  // then, has the focus once the dialog has closed.
  const created = useRef(false);
  const commands = useCommands(projectId);
  const refusal = refusalOf(commands.error, t, createField, {
    key_taken: t("component.error.key_taken"),
  });
  useFocusRefusal(form, commands.error);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const name = String(data.get("name"));
    const key = String(data.get("key")).trim();
    const folder = String(data.get("folder") ?? folderTop);
    const type = data.get("type");
    if (type === null) {
      // The project has no type left, as once its last went to the trash
      // elsewhere while the dialog was open, which its field says.
      return;
    }
    commands.mutate(
      [
        {
          type: "component.create",
          payload: {
            id,
            name,
            component_type_id: String(type),
            ...(key === "" ? {} : { key }),
            ...(folder === folderTop ? {} : { folder_id: folder }),
          },
        },
      ],
      {
        onSuccess: () => {
          created.current = true;
          onCreated(id, name);
          onClose();
        },
      },
    );
  };
  return (
    <Dialog.Root open onOpenChange={closesBy(onClose, commands.isPending)}>
      <DialogFrame
        title={t("component.create_title")}
        onCloseAutoFocus={focusBackUnlessCreated(opener, created)}
      >
        <form ref={form} className="space-y-4" onSubmit={submit} noValidate>
          <Alert message={refusal?.message} />
          <TextField
            label={t("component.name")}
            name="name"
            autoComplete="off"
            required
            maxLength={100}
            error={refusal?.fields.name}
          />
          <TextField
            label={t("component.key_optional")}
            hint={t("component.key_derived_hint")}
            name="key"
            autoComplete="off"
            spellCheck={false}
            maxLength={64}
            error={refusal?.fields.key}
          />
          <SelectField
            label={t("component.type")}
            hint={t("component.type_fixed_hint")}
            name="type"
            defaultValue={typeId ?? types[0]?.id}
            error={
              types.length === 0
                ? t("components.no_types")
                : refusal?.fields.component_type_id
            }
          >
            {types.map((type) => (
              <option key={type.id} value={type.id}>
                {type.name}
              </option>
            ))}
          </SelectField>
          <FolderField
            tree={tree}
            label={t("content.folder")}
            initial={parentId}
            error={refusal?.fields.folder_id}
          />
          <FormButtons
            cancel={t("boards.cancel")}
            pending={commands.isPending}
            disabled={types.length === 0}
            submit={t("component.create")}
          />
        </form>
      </DialogFrame>
    </Dialog.Root>
  );
}
