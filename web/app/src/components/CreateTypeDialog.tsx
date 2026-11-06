// The dialog that creates a component type (PRD FR-COMP-01): its name, its
// key, which the server derives from the name unless the user gives one,
// and its icon. It has no attribute definitions yet.

import { newId } from "@storycreep/shared/ids";
import { Dialog } from "radix-ui";
import { type FormEvent, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { refusalOf } from "../auth/refusal";
import { payloadField } from "../project/dialogs";
import { useCommands } from "../project/queries";
import {
  closesBy,
  DialogFrame,
  FormButtons,
  focusBackUnlessCreated,
  type Opener,
} from "../ui/Dialog";
import { SelectField, TextField, useFocusRefusal } from "../ui/Field";
import { Alert } from "../ui/Page";
import { typeIcons } from "./TypePanel";

const createField = payloadField("name", "key", "icon");

export function CreateTypeDialog({
  projectId,
  opener,
  onClose,
  onCreated,
}: {
  projectId: string;
  opener: Opener;
  onClose: () => void;
  /** Called once the type has been created, with its ID and its name. */
  onCreated: (id: string, name: string) => void;
}) {
  const { t } = useTranslation();
  const form = useRef<HTMLFormElement>(null);
  const [id] = useState(() => newId("component_type"));
  // Whether the type has been created, whose panel, which the page opens
  // then, has the focus once the dialog has closed.
  const created = useRef(false);
  const commands = useCommands(projectId);
  const refusal = refusalOf(commands.error, t, createField, {
    key_taken: t("component_type.error.key_taken"),
  });
  useFocusRefusal(form, commands.error);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const name = String(data.get("name"));
    const key = String(data.get("key")).trim();
    commands.mutate(
      [
        {
          type: "component_type.create",
          payload: {
            id,
            name,
            icon: String(data.get("icon")) as (typeof typeIcons)[number],
            ...(key === "" ? {} : { key }),
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
        title={t("component_type.create_title")}
        onCloseAutoFocus={focusBackUnlessCreated(opener, created)}
      >
        <form ref={form} className="space-y-4" onSubmit={submit} noValidate>
          <Alert message={refusal?.message} />
          <TextField
            label={t("component_type.name")}
            name="name"
            autoComplete="off"
            required
            maxLength={100}
            error={refusal?.fields.name}
          />
          <TextField
            label={t("component.key_optional")}
            hint={t("component_type.key_derived_hint")}
            name="key"
            autoComplete="off"
            spellCheck={false}
            maxLength={64}
            error={refusal?.fields.key}
          />
          <SelectField
            label={t("component_type.icon")}
            name="icon"
            defaultValue="none"
            error={refusal?.fields.icon}
          >
            {typeIcons.map((icon) => (
              <option key={icon} value={icon}>
                {t("component_type.icon_name", { icon })}
              </option>
            ))}
          </SelectField>
          <FormButtons
            cancel={t("boards.cancel")}
            pending={commands.isPending}
            submit={t("component_type.create")}
          />
        </form>
      </DialogFrame>
    </Dialog.Root>
  );
}
