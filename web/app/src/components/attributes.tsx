// The attribute definitions of a component type (PRD FR-COMP-01): the
// dialogs that add one, change one, change its type, and delete one, each
// of which sends a batch of commands (useCommands), expecting the revision
// of the definition that it opened with (useExpected), as the tree's
// dialogs do. Changing a definition's type is allowed only with a choice of
// what becomes of its components' values, convert or reset, and the dialog
// lists, before it applies, the components whose values would not convert,
// those in the trash among them, which convert gives the new default (AC2).

import type {
  Attribute,
  ComponentType,
  PageUnconvertible,
} from "@storycreep/shared/api";
import { newId } from "@storycreep/shared/ids";
import { useInfiniteQuery } from "@tanstack/react-query";
import { Dialog } from "radix-ui";
import { type FormEvent, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { api, pagedQuery } from "../api";
import { refusalOf } from "../auth/refusal";
import { typeName, ValueField, valueError } from "../content/ValueField";
import {
  attributeTypes,
  convertedTo,
  optionsOf,
  parseValue,
  type Value,
  type ValueType,
  zeroOf,
} from "../content/values";
import { Confirm, payloadField, useExpected } from "../project/dialogs";
import { type Command, useCommands } from "../project/queries";
import { MoreButton } from "../ui/Button";
import {
  closesBy,
  DialogFrame,
  FormButtons,
  focusBackTo,
  type Opener,
} from "../ui/Dialog";
import {
  CheckboxField,
  GroupField,
  RadioField,
  SelectField,
  TextAreaField,
  TextField,
  useFocusRefusal,
} from "../ui/Field";
import { Alert } from "../ui/Page";

const attributeField = payloadField(
  "name",
  "key",
  "type",
  "default",
  "options",
  "runtime",
  "conversion",
);

/**
 * The fields of an enum's options, one to a line, and of the default, whose
 * choice offers the options as the field lists them, which the form shows
 * anew as they change.
 */
function OptionsAndDefault({
  type,
  options,
  onOptions,
  defaultValue,
  errors,
}: {
  type: ValueType;
  /** The options as the field holds them, one to a line. */
  options: string;
  onOptions: (options: string) => void;
  defaultValue: Value;
  errors: Readonly<Record<string, string>>;
}) {
  const { t } = useTranslation();
  const listed = optionsOf(options);
  return (
    <>
      {type === "enum" && (
        <TextAreaField
          label={t("attribute.options")}
          hint={t("attribute.options_hint")}
          name="options"
          rows={4}
          value={options}
          onChange={(event) => onOptions(event.currentTarget.value)}
          error={errors.options}
        />
      )}
      <ValueField
        // Another type, or other options, show the default anew.
        key={type === "enum" ? `enum:${listed.join("\n")}` : type}
        label={t("attribute.default")}
        name="default"
        type={type}
        options={listed}
        defaultValue={convertedTo(type, defaultValue, listed)}
        error={errors.default}
      />
    </>
  );
}

/** The dialog that adds an attribute definition to a type, after its last. */
export function CreateAttributeDialog({
  projectId,
  type,
  opener,
  onClose,
}: {
  projectId: string;
  type: ComponentType;
  opener: Opener;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const form = useRef<HTMLFormElement>(null);
  const [id] = useState(() => newId("attribute_def"));
  const [valueType, setValueType] = useState<ValueType>("int");
  const [options, setOptions] = useState("");
  const [runtime, setRuntime] = useState(false);
  const [invalid, setInvalid] = useState<{ fields: Record<string, string> }>();
  const commands = useCommands(projectId);
  const refusal = refusalOf(commands.error, t, attributeField, {
    key_taken: t("attribute.error.key_taken"),
  });
  useFocusRefusal(form, invalid ?? commands.error);
  const errors: Record<string, string> = {
    ...refusal?.fields,
    ...invalid?.fields,
  };
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const listed = optionsOf(options);
    const value = parseValue(
      valueType,
      String(data.get("default") ?? ""),
      listed,
    );
    if (value === undefined) {
      commands.reset();
      setInvalid({
        fields:
          valueType === "enum" && listed.length === 0
            ? { options: t("attribute.error.no_options") }
            : { default: valueError(t, valueType) },
      });
      return;
    }
    setInvalid(undefined);
    const key = String(data.get("key")).trim();
    commands.mutate(
      [
        {
          type: "attribute.create",
          payload: {
            id,
            component_type_id: type.id,
            name: String(data.get("name")),
            type: valueType,
            default: value,
            ...(key === "" ? {} : { key }),
            ...(valueType === "enum" ? { options: listed } : {}),
            ...(runtime && valueType !== "text" ? { runtime: true } : {}),
          },
        },
      ],
      { onSuccess: onClose },
    );
  };
  return (
    <Dialog.Root open onOpenChange={closesBy(onClose, commands.isPending)}>
      <DialogFrame
        title={t("attribute.create_title", { type: type.name })}
        onCloseAutoFocus={focusBackTo(opener)}
      >
        <form ref={form} className="space-y-4" onSubmit={submit} noValidate>
          <Alert message={refusal?.message} />
          <TextField
            label={t("attribute.name")}
            name="name"
            autoComplete="off"
            required
            maxLength={100}
            error={errors.name}
          />
          <TextField
            label={t("attribute.key_optional")}
            hint={t("attribute.key_derived_hint")}
            name="key"
            autoComplete="off"
            spellCheck={false}
            maxLength={64}
            error={errors.key}
          />
          <SelectField
            label={t("attribute.type")}
            name="type"
            value={valueType}
            onChange={(event) =>
              setValueType(event.currentTarget.value as ValueType)
            }
            error={errors.type}
          >
            {attributeTypes.map((choice) => (
              <option key={choice} value={choice}>
                {typeName(t, choice)}
              </option>
            ))}
          </SelectField>
          <OptionsAndDefault
            type={valueType}
            options={options}
            onOptions={setOptions}
            defaultValue={zeroOf(valueType, optionsOf(options))}
            errors={errors}
          />
          <CheckboxField
            label={t("attribute.runtime")}
            hint={
              valueType === "text"
                ? t("attribute.runtime_text")
                : t("attribute.runtime_hint")
            }
            name="runtime"
            checked={runtime && valueType !== "text"}
            disabled={valueType === "text"}
            onChange={(event) => setRuntime(event.currentTarget.checked)}
          />
          <FormButtons
            cancel={t("boards.cancel")}
            pending={commands.isPending}
            submit={t("attribute.create")}
          />
        </form>
      </DialogFrame>
    </Dialog.Root>
  );
}

/** Returns the commands that change attribute, as the dialog opened with it, to what the form holds, each expecting rev. */
function editsOf(
  attribute: Attribute,
  typed: {
    name: string;
    key: string;
    options: string[] | undefined;
    default: Value;
    runtime: boolean;
  },
  rev: number,
): Command[] {
  const id = attribute.id;
  const expected_rev = { [id]: rev };
  const changes: Command[] = [];
  if (typed.name !== attribute.name) {
    changes.push({
      type: "attribute.rename",
      payload: { id, name: typed.name },
      expected_rev,
    });
  }
  if (typed.key !== attribute.key) {
    changes.push({
      type: "attribute.set_key",
      payload: { id, key: typed.key },
      expected_rev,
    });
  }
  const optionsChanged =
    typed.options !== undefined &&
    typed.options.join("\n") !== (attribute.options ?? []).join("\n");
  const defaultChanged = typed.default !== attribute.default;
  if (optionsChanged) {
    // The default, one of the new options, goes with them.
    changes.push({
      type: "attribute.set_options",
      payload: {
        id,
        options: typed.options ?? [],
        ...(defaultChanged ? { default: typed.default } : {}),
      },
      expected_rev,
    });
  } else if (defaultChanged) {
    changes.push({
      type: "attribute.set_default",
      payload: { id, default: typed.default },
      expected_rev,
    });
  }
  if (typed.runtime !== attribute.runtime) {
    changes.push({
      type: "attribute.set_runtime",
      payload: { id, runtime: typed.runtime },
      expected_rev,
    });
  }
  return changes;
}

/**
 * The dialog that changes an attribute definition: its name, its key, an
 * enum's options, its default, which the components created later are
 * given, and whether it is part of the game's state. A change of an enum's
 * options gives a value that is none of them the default. It sends what the
 * user changed of the definition as it opened with it, which its fields
 * start with, so that another session's change of a field that the user did
 * not change, made while it is open, is not changed back.
 */
export function EditAttributeDialog({
  projectId,
  attribute,
  opener,
  onClose,
}: {
  projectId: string;
  attribute: Attribute;
  opener: Opener;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const form = useRef<HTMLFormElement>(null);
  // The definition as the dialog opened with it, which its fields show.
  const [opened] = useState(attribute);
  const [options, setOptions] = useState((opened.options ?? []).join("\n"));
  const [runtime, setRuntime] = useState(opened.runtime);
  const [invalid, setInvalid] = useState<{ fields: Record<string, string> }>();
  const commands = useCommands(projectId);
  const rev = useExpected(attribute.rev, commands.error);
  const refusal = refusalOf(commands.error, t, attributeField, {
    key_taken: t("attribute.error.key_taken"),
  });
  useFocusRefusal(form, invalid ?? commands.error);
  const errors: Record<string, string> = {
    ...refusal?.fields,
    ...invalid?.fields,
  };
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const listed = attribute.type === "enum" ? optionsOf(options) : undefined;
    const value = parseValue(
      attribute.type,
      String(data.get("default") ?? ""),
      listed,
    );
    if (value === undefined) {
      commands.reset();
      setInvalid({
        fields:
          listed !== undefined && listed.length === 0
            ? { options: t("attribute.error.no_options") }
            : { default: valueError(t, attribute.type) },
      });
      return;
    }
    setInvalid(undefined);
    const changes = editsOf(
      opened,
      {
        name: String(data.get("name")),
        // As the dialog that adds a definition reads it.
        key: String(data.get("key")).trim(),
        options: listed,
        default: value,
        runtime: runtime && attribute.type !== "text",
      },
      rev,
    );
    if (changes.length === 0) {
      onClose();
      return;
    }
    commands.mutate(changes, { onSuccess: onClose });
  };
  return (
    <Dialog.Root open onOpenChange={closesBy(onClose, commands.isPending)}>
      <DialogFrame
        title={t("attribute.edit_title", { name: attribute.name })}
        description={t("attribute.edit_intro", {
          type: typeName(t, attribute.type),
        })}
        onCloseAutoFocus={focusBackTo(opener)}
      >
        <form ref={form} className="space-y-4" onSubmit={submit} noValidate>
          <Alert message={refusal?.message} />
          <TextField
            label={t("attribute.name")}
            name="name"
            autoComplete="off"
            required
            maxLength={100}
            defaultValue={opened.name}
            error={errors.name}
          />
          <TextField
            label={t("attribute.key")}
            hint={t("attribute.key_hint")}
            name="key"
            autoComplete="off"
            spellCheck={false}
            required
            maxLength={64}
            defaultValue={opened.key}
            error={errors.key}
          />
          <OptionsAndDefault
            type={attribute.type}
            options={options}
            onOptions={setOptions}
            defaultValue={opened.default}
            errors={errors}
          />
          <CheckboxField
            label={t("attribute.runtime")}
            hint={
              attribute.type === "text"
                ? t("attribute.runtime_text")
                : t("attribute.runtime_hint")
            }
            name="runtime"
            checked={runtime && attribute.type !== "text"}
            disabled={attribute.type === "text"}
            onChange={(event) => setRuntime(event.currentTarget.checked)}
          />
          <FormButtons
            cancel={t("boards.cancel")}
            pending={commands.isPending}
            submit={t("attribute.save")}
          />
        </form>
      </DialogFrame>
    </Dialog.Root>
  );
}

/** The components of a type whose values of a definition do not convert to type, with options, a page at a time. */
function unconvertibleQuery(
  projectId: string,
  attribute: Attribute,
  type: ValueType,
  options: readonly string[],
) {
  return pagedQuery(
    [
      "projects",
      projectId,
      "conversion",
      attribute.id,
      attribute.rev,
      type,
      options.join("\n"),
    ],
    async (query, signal): Promise<PageUnconvertible> =>
      (
        await api.call(
          "listUnconvertibleValues",
          {
            path: {
              project_id: projectId,
              component_type_id: attribute.component_type_id,
              attribute_id: attribute.id,
            },
            query: {
              ...query,
              type,
              ...(type === "enum" ? { options: options.join("\n") } : {}),
            },
          },
          { signal },
        )
      ).body,
    { meta: { showsRefusals: true } },
  );
}

/** The components whose values would not convert, which the retype gives the new default. */
function Unconvertible({
  projectId,
  attribute,
  type,
  options,
}: {
  projectId: string;
  attribute: Attribute;
  type: ValueType;
  options: readonly string[];
}) {
  const { t } = useTranslation();
  const list = useInfiniteQuery(
    unconvertibleQuery(projectId, attribute, type, options),
  );
  const items = list.data?.pages.flatMap((page) => page.items) ?? [];
  return (
    <section className="space-y-2" aria-live="polite">
      {list.isPending ? (
        <p className="text-fg-muted">{t("attribute.checking")}</p>
      ) : list.isError ? (
        <Alert message={refusalOf(list.error, t)?.message} />
      ) : items.length === 0 ? (
        <p>{t("attribute.all_convert")}</p>
      ) : (
        <>
          <p>{t("attribute.unconvertible_intro")}</p>
          <ul className="max-h-48 list-disc space-y-1 overflow-y-auto ps-5">
            {items.map((item) => (
              <li key={item.component_id} className="break-words">
                {item.deleted_at === undefined
                  ? t("attribute.unconvertible", {
                      name: item.name,
                      key: item.key,
                    })
                  : t("attribute.unconvertible_trash", {
                      name: item.name,
                      key: item.key,
                    })}
              </li>
            ))}
          </ul>
          <MoreButton list={list}>{t("usage.more")}</MoreButton>
        </>
      )}
    </section>
  );
}

/** How long the typing in a field pauses, in milliseconds, before what follows it reads what was typed (useSettled). */
const typingPause = 400;

/** Returns value once it has stayed the same for wait milliseconds, as what is typed in a field once the typing pauses. */
function useSettled<T>(value: T, wait: number): T {
  const [settled, setSettled] = useState(value);
  useEffect(() => {
    const timer = setTimeout(() => setSettled(value), wait);
    return () => clearTimeout(timer);
  }, [value, wait]);
  return settled;
}

/**
 * The dialog that changes the type of an attribute definition, with what
 * becomes of its components' values: converted, each that converts, and the
 * rest given the new default, which the dialog lists first, or every one
 * given the new default. A definition that is part of the game's state
 * cannot become a text, which is never part of it.
 */
export function RetypeAttributeDialog({
  projectId,
  attribute,
  opener,
  onClose,
}: {
  projectId: string;
  attribute: Attribute;
  opener: Opener;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const form = useRef<HTMLFormElement>(null);
  const choices = attributeTypes.filter(
    (type) =>
      type !== attribute.type && !(attribute.runtime && type === "text"),
  );
  // The type that the user chose, until it is none of the choices, as once
  // the attribute became it elsewhere: the choice shows the first then, and
  // the dialog sends what it shows.
  const [chosenType, setValueType] = useState<ValueType>(choices[0]);
  const valueType = choices.includes(chosenType) ? chosenType : choices[0];
  const [options, setOptions] = useState((attribute.options ?? []).join("\n"));
  const [conversion, setConversion] = useState<"convert" | "reset">("convert");
  const [invalid, setInvalid] = useState<{ fields: Record<string, string> }>();
  const commands = useCommands(projectId);
  const rev = useExpected(attribute.rev, commands.error);
  const refusal = refusalOf(commands.error, t, attributeField);
  useFocusRefusal(form, invalid ?? commands.error);
  const errors: Record<string, string> = {
    ...refusal?.fields,
    ...invalid?.fields,
  };
  // The list of what would not convert follows the options once they are
  // typed, rather than at every key.
  const listed = optionsOf(useSettled(options, typingPause));
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const newOptions = valueType === "enum" ? optionsOf(options) : undefined;
    const value = parseValue(
      valueType,
      String(data.get("default") ?? ""),
      newOptions,
    );
    if (value === undefined) {
      commands.reset();
      setInvalid({
        fields:
          newOptions !== undefined && newOptions.length === 0
            ? { options: t("attribute.error.no_options") }
            : { default: valueError(t, valueType) },
      });
      return;
    }
    setInvalid(undefined);
    commands.mutate(
      [
        {
          type: "attribute.retype",
          payload: {
            id: attribute.id,
            type: valueType,
            conversion,
            default: value,
            ...(newOptions === undefined ? {} : { options: newOptions }),
          },
          expected_rev: { [attribute.id]: rev },
        },
      ],
      { onSuccess: onClose },
    );
  };
  return (
    <Dialog.Root open onOpenChange={closesBy(onClose, commands.isPending)}>
      <DialogFrame
        title={t("attribute.retype_title", { name: attribute.name })}
        description={t("attribute.retype_intro", {
          type: typeName(t, attribute.type),
        })}
        onCloseAutoFocus={focusBackTo(opener)}
      >
        <form ref={form} className="space-y-4" onSubmit={submit} noValidate>
          <Alert message={refusal?.message} />
          <SelectField
            label={t("attribute.new_type")}
            hint={
              attribute.runtime ? t("attribute.runtime_no_text") : undefined
            }
            name="type"
            value={valueType}
            onChange={(event) =>
              setValueType(event.currentTarget.value as ValueType)
            }
            error={errors.type}
          >
            {choices.map((choice) => (
              <option key={choice} value={choice}>
                {typeName(t, choice)}
              </option>
            ))}
          </SelectField>
          <OptionsAndDefault
            type={valueType}
            options={options}
            onOptions={setOptions}
            defaultValue={attribute.default}
            errors={errors}
          />
          <GroupField
            legend={t("attribute.conversion")}
            error={errors.conversion}
          >
            <RadioField
              label={t("attribute.convert")}
              hint={t("attribute.convert_hint")}
              name="conversion"
              value="convert"
              checked={conversion === "convert"}
              onChange={() => setConversion("convert")}
            />
            <RadioField
              label={t("attribute.reset")}
              hint={t("attribute.reset_hint")}
              name="conversion"
              value="reset"
              checked={conversion === "reset"}
              onChange={() => setConversion("reset")}
            />
          </GroupField>
          {conversion === "convert" &&
            (valueType !== "enum" || listed.length > 0) && (
              <Unconvertible
                projectId={projectId}
                attribute={attribute}
                type={valueType}
                options={listed}
              />
            )}
          <FormButtons
            cancel={t("boards.cancel")}
            pending={commands.isPending}
            submit={t("attribute.retype")}
          />
        </form>
      </DialogFrame>
    </Dialog.Root>
  );
}

/** The confirmation that deletes an attribute definition, with its components' values, to the trash. */
export function DeleteAttributeDialog({
  projectId,
  attribute,
  opener,
  onClose,
}: {
  projectId: string;
  attribute: Attribute;
  opener: Opener;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const commands = useCommands(projectId);
  const rev = useExpected(attribute.rev, commands.error);
  return (
    <Confirm
      title={t("attribute.delete_title", { name: attribute.name })}
      description={t("attribute.delete_body")}
      confirm={t("attribute.delete")}
      opener={opener}
      pending={commands.isPending}
      onClose={onClose}
      onConfirm={() =>
        commands.mutate(
          [
            {
              type: "attribute.delete",
              payload: { id: attribute.id },
              expected_rev: { [attribute.id]: rev },
            },
          ],
          { onSuccess: onClose },
        )
      }
    >
      <Alert message={refusalOf(commands.error, t)?.message} />
    </Confirm>
  );
}
