// The panel of a component type (PRD FR-COMP-01): its name, its key, and its
// icon, which an editor changes in one batch (useEntityForm), its attribute
// definitions, in their order, which an editor adds, changes, retypes,
// moves up and down, and deletes (attributes.tsx), and its deletion, which
// the server allows only once each of its components is in the trash.

import type {
  Attribute,
  ComponentType,
  ProjectBody,
} from "@storycreep/shared/api";
import { type FormEvent, useState } from "react";
import { useTranslation } from "react-i18next";
import { refusalOf } from "../auth/refusal";
import {
  ConflictNotice,
  changedOf,
  SaveStatus,
  useEntityForm,
} from "../content/form";
import { typeName } from "../content/ValueField";
import { textOf } from "../content/values";
import { Confirm, payloadField, useExpected } from "../project/dialogs";
import { keyAfterSibling, keyBeforeSibling } from "../project/orderkey";
import { type Command, useCommands } from "../project/queries";
import { Button } from "../ui/Button";
import type { Opener } from "../ui/Dialog";
import { SelectField, TextField, useFocusRefusal } from "../ui/Field";
import { Menu, MenuItem, MenuSeparator } from "../ui/Menu";
import { Alert } from "../ui/Page";
import { useDeleting } from "../ui/Panel";
import {
  CreateAttributeDialog,
  DeleteAttributeDialog,
  EditAttributeDialog,
  RetypeAttributeDialog,
} from "./attributes";
import { definitionsOf } from "./types";

/** The icons that a type may have, each a name that the app shows a glyph for (PRD FR-COMP-01). */
export const typeIcons = [
  "none",
  "person",
  "group",
  "item",
  "location",
  "event",
  "book",
  "star",
  "heart",
  "key",
  "gem",
  "shield",
  "flag",
  "tag",
] as const satisfies readonly ComponentType["icon"][];

/** The glyph of each icon, which a type's name is shown with; its name says it for a screen reader. */
const glyphs: Readonly<Record<ComponentType["icon"], string>> = {
  none: "",
  person: "👤",
  group: "👥",
  item: "🎒",
  location: "📍",
  event: "📅",
  book: "📖",
  star: "⭐",
  heart: "❤",
  key: "🔑",
  gem: "💎",
  shield: "🛡",
  flag: "🚩",
  tag: "🏷",
};

/** The glyph of a type's icon, which is decoration: the type's name says what it is. */
export function TypeGlyph({ icon }: { icon: ComponentType["icon"] }) {
  const glyph = glyphs[icon];
  return glyph === "" ? null : (
    <span aria-hidden="true" className="me-1">
      {glyph}
    </span>
  );
}

const typeField = payloadField("name", "key", "icon");

export interface TypePanelProps {
  project: ProjectBody;
  type: ComponentType;
  editable: boolean;
  /** Tells whether the type's deletion is open, while which the page shows the type as it was. */
  onDeleting: (deleting: boolean) => void;
  /** Called once the type has been deleted, which closes the panel. */
  onDeleted: () => void;
}

/** The contents of a type's panel, under its heading. */
export function TypePanel(props: TypePanelProps) {
  const { t } = useTranslation();
  const { type, editable } = props;
  return (
    <div className="space-y-6">
      {editable ? (
        <TypeForm {...props} />
      ) : (
        <dl className="grid gap-x-6 gap-y-1 sm:grid-cols-[max-content_1fr]">
          <dt className="text-fg-muted">{t("component_type.key")}</dt>
          <dd className="font-mono break-all">{type.key}</dd>
          <dt className="text-fg-muted">{t("component_type.icon")}</dt>
          <dd>
            <TypeGlyph icon={type.icon} />
            {t("component_type.icon_name", { icon: type.icon })}
          </dd>
        </dl>
      )}
      <Definitions {...props} />
      {editable && <DeleteType {...props} />}
    </div>
  );
}

/** The form of a type's name, key, and icon. */
function TypeForm({ project, type }: TypePanelProps) {
  const { t } = useTranslation();
  const form = useEntityForm(project.id, type, (view, id) =>
    view.componentTypes.get(id),
  );
  const { commands, held, shown, against } = form;
  const refusal = refusalOf(commands.error, t, typeField, {
    key_taken: t("component_type.error.key_taken"),
  });
  useFocusRefusal(held.form, commands.error);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const typed = {
      name: String(data.get("name")),
      // As the dialog that creates a type reads it.
      key: String(data.get("key")).trim(),
      icon: String(data.get("icon")) as ComponentType["icon"],
    };
    const id = against.id;
    const expected_rev = { [id]: against.rev };
    const changes: Command[] = [
      ...(typed.name === against.name
        ? []
        : [
            {
              type: "component_type.rename" as const,
              payload: { id, name: typed.name },
              expected_rev,
            },
          ]),
      ...(typed.key === against.key
        ? []
        : [
            {
              type: "component_type.set_key" as const,
              payload: { id, key: typed.key },
              expected_rev,
            },
          ]),
      ...(typed.icon === against.icon
        ? []
        : [
            {
              type: "component_type.set_icon" as const,
              payload: { id, icon: typed.icon },
              expected_rev,
            },
          ]),
    ];
    form.save(changes, changedOf(typed, against));
  };
  const fieldName = (field: string) =>
    field === "name"
      ? t("component_type.name")
      : field === "key"
        ? t("component_type.key")
        : t("component_type.icon");
  const shownValue = (field: string, value: unknown) =>
    field === "icon"
      ? t("component_type.icon_name", { icon: String(value) })
      : String(value ?? "");
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
      <form
        key={held.key}
        {...held.props}
        className="space-y-4"
        onSubmit={submit}
        noValidate
      >
        <TextField
          label={t("component_type.name")}
          name="name"
          autoComplete="off"
          required
          maxLength={100}
          defaultValue={shown.name}
          error={refusal?.fields.name}
        />
        <TextField
          label={t("component_type.key")}
          hint={t("component_type.key_hint")}
          name="key"
          autoComplete="off"
          spellCheck={false}
          required
          maxLength={64}
          defaultValue={shown.key}
          error={refusal?.fields.key}
        />
        <SelectField
          label={t("component_type.icon")}
          name="icon"
          defaultValue={shown.icon}
          error={refusal?.fields.icon}
        >
          {typeIcons.map((icon) => (
            <option key={icon} value={icon}>
              {t("component_type.icon_name", { icon })}
            </option>
          ))}
        </SelectField>
        <Button type="submit" variant="primary" disabled={commands.isPending}>
          {t("component_type.save")}
        </Button>
      </form>
    </div>
  );
}

/** A dialog of a definition, or the one that adds one, with the control that opened it. */
type Open =
  | { dialog: "create"; opener: Opener }
  | {
      dialog: "edit" | "retype" | "delete";
      attribute: Attribute;
      opener: Opener;
    };

/** The type's attribute definitions, in their order, with what an editor does to each. */
function Definitions({ project, type, editable }: TypePanelProps) {
  const { t } = useTranslation();
  const definitions = definitionsOf(type);
  const [open, setOpen] = useState<Open>();
  const move = useCommands(project.id);
  const close = () => setOpen(undefined);
  // The definition that a dialog changes, as the type has it now, or, once
  // it has gone from it, as it was.
  const attribute =
    open === undefined || open.dialog === "create"
      ? undefined
      : (definitions.find((a) => a.id === open.attribute.id) ?? open.attribute);
  /**
   * Returns the key that moves the definition at i one place up, before the
   * one before it, or down, after the one after it, among those of the
   * type, or undefined for none.
   */
  const placeAt = (i: number, direction: "up" | "down"): string | undefined => {
    const rest = definitions.filter((_, n) => n !== i);
    return direction === "up"
      ? keyBeforeSibling(rest, i - 1)
      : keyAfterSibling(rest, i);
  };
  const moveTo = (attribute: Attribute, order: string) =>
    move.mutate([
      {
        type: "attribute.move",
        payload: { id: attribute.id, order },
        expected_rev: { [attribute.id]: attribute.rev },
      },
    ]);
  return (
    <section className="space-y-2">
      <div className="flex flex-wrap items-center gap-2">
        <h3 className="flex-1 text-lg font-semibold">
          {t("component_type.attributes")}
        </h3>
        {editable && (
          <Button
            aria-haspopup="dialog"
            disabled={definitions.length >= 100}
            onClick={(event) =>
              setOpen({ dialog: "create", opener: event.currentTarget })
            }
          >
            {t("component_type.add_attribute")}
          </Button>
        )}
      </div>
      <p className="text-fg-muted">{t("component_type.attributes_intro")}</p>
      <Alert
        message={
          move.error === null ? undefined : refusalOf(move.error, t)?.message
        }
      />
      {definitions.length === 0 ? (
        <p className="text-fg-muted">{t("component_type.no_attributes")}</p>
      ) : (
        <ol className="space-y-2">
          {definitions.map((definition, i) => {
            const up = i > 0 ? placeAt(i, "up") : undefined;
            const down =
              i < definitions.length - 1 ? placeAt(i, "down") : undefined;
            const opener = () =>
              document.querySelector<HTMLElement>(
                `[data-attribute-actions="${CSS.escape(definition.id)}"]`,
              );
            return (
              <li
                key={definition.id}
                className="flex items-start gap-2 rounded-md border border-border p-2"
              >
                <div className="min-w-0 flex-1">
                  <p className="break-words">
                    <span className="font-medium">{definition.name}</span>{" "}
                    <span className="font-mono break-all text-fg-muted">
                      {definition.key}
                    </span>
                  </p>
                  <p className="text-sm break-words text-fg-muted">
                    {t("component_type.attribute_detail", {
                      type: typeName(t, definition.type),
                      default: textOf(definition.default),
                      runtime: definition.runtime ? "yes" : "no",
                    })}
                  </p>
                </div>
                {editable && (
                  <Menu
                    trigger={
                      <Button
                        data-attribute-actions={definition.id}
                        aria-label={t("boards.actions", {
                          name: definition.name,
                        })}
                      >
                        <span aria-hidden="true">⋯</span>
                      </Button>
                    }
                  >
                    <MenuItem
                      onSelect={() =>
                        setOpen({
                          dialog: "edit",
                          attribute: definition,
                          opener,
                        })
                      }
                    >
                      {t("component_type.edit_attribute")}
                    </MenuItem>
                    <MenuItem
                      onSelect={() =>
                        setOpen({
                          dialog: "retype",
                          attribute: definition,
                          opener,
                        })
                      }
                    >
                      {t("component_type.retype_attribute")}
                    </MenuItem>
                    <MenuSeparator />
                    <MenuItem
                      disabled={up === undefined || move.isPending}
                      onSelect={() =>
                        up !== undefined && moveTo(definition, up)
                      }
                    >
                      {t("component_type.move_up")}
                    </MenuItem>
                    <MenuItem
                      disabled={down === undefined || move.isPending}
                      onSelect={() =>
                        down !== undefined && moveTo(definition, down)
                      }
                    >
                      {t("component_type.move_down")}
                    </MenuItem>
                    <MenuSeparator />
                    <MenuItem
                      danger
                      onSelect={() =>
                        setOpen({
                          dialog: "delete",
                          attribute: definition,
                          opener,
                        })
                      }
                    >
                      {t("boards.delete")}
                    </MenuItem>
                  </Menu>
                )}
              </li>
            );
          })}
        </ol>
      )}
      {open?.dialog === "create" && (
        <CreateAttributeDialog
          projectId={project.id}
          type={type}
          opener={open.opener}
          onClose={close}
        />
      )}
      {open?.dialog === "edit" && attribute !== undefined && (
        <EditAttributeDialog
          projectId={project.id}
          attribute={attribute}
          opener={open.opener}
          onClose={close}
        />
      )}
      {open?.dialog === "retype" && attribute !== undefined && (
        <RetypeAttributeDialog
          projectId={project.id}
          attribute={attribute}
          opener={open.opener}
          onClose={close}
        />
      )}
      {open?.dialog === "delete" && attribute !== undefined && (
        <DeleteAttributeDialog
          projectId={project.id}
          attribute={attribute}
          opener={open.opener}
          onClose={close}
        />
      )}
    </section>
  );
}

/** The deletion of the type, which the server refuses while a component of it is not in the trash. */
function DeleteType(props: TypePanelProps) {
  const { t } = useTranslation();
  const [opener, setOpener] = useState<HTMLElement>();
  return (
    <section className="space-y-2">
      <h3 className="text-lg font-semibold">
        {t("component_type.delete_heading")}
      </h3>
      <p>{t("component_type.delete_intro")}</p>
      <Button
        variant="danger"
        aria-haspopup="dialog"
        onClick={(event) => setOpener(event.currentTarget)}
      >
        {t("component_type.delete")}
      </Button>
      {opener !== undefined && (
        <DeleteTypeDialog
          {...props}
          opener={opener}
          onClose={() => setOpener(undefined)}
        />
      )}
    </section>
  );
}

/** The confirmation that deletes the type, expecting the revision that it opened with (useExpected). */
function DeleteTypeDialog({
  project,
  type,
  opener,
  onClose,
  onDeleting,
  onDeleted,
}: TypePanelProps & { opener: Opener; onClose: () => void }) {
  const { t } = useTranslation();
  useDeleting(onDeleting);
  const commands = useCommands(project.id);
  const rev = useExpected(type.rev, commands.error);
  return (
    <Confirm
      title={t("deletion.title", { name: type.name })}
      description={t("component_type.delete_body")}
      confirm={t("component_type.delete")}
      opener={opener}
      pending={commands.isPending}
      onClose={onClose}
      onConfirm={() =>
        commands.mutate(
          [
            {
              type: "component_type.delete",
              payload: { id: type.id },
              expected_rev: { [type.id]: rev },
            },
          ],
          { onSuccess: onDeleted },
        )
      }
    >
      <Alert message={refusalOf(commands.error, t)?.message} />
    </Confirm>
  );
}
