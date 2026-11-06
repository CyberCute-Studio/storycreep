// The panel of a component (PRD FR-COMP-02, FR-COMP-04): its name, its
// description, in rich text, its values of its type's attribute
// definitions, and its folder, which an editor changes in one batch, as a
// variable's panel does; its key, a refactor previewed first (rename.tsx);
// its image, an asset of the project that the user uploads; where it is
// used, as a speaker, in a mention, and by what reads and writes its
// attributes (usages.tsx); and its deletion to the trash, guarded by those
// usages (deletion.tsx). A component is listed without its description and
// its values, which the panel reads of it whole as it opens
// (ProjectDocument.readComponent).

import type {
  Attribute,
  Component,
  ComponentType,
  ProjectBody,
  RichTextDoc,
} from "@storycreep/shared/api";
import { useMutation, useQuery } from "@tanstack/react-query";
import {
  type ChangeEvent,
  type FormEvent,
  useCallback,
  useEffect,
  useId,
  useState,
} from "react";
import { useTranslation } from "react-i18next";
import { api, sessionQuery } from "../api";
import { refusalOf } from "../auth/refusal";
import { csrfHeaders } from "../auth/session";
import { DeleteContentDialog } from "../content/deletion";
import { ConflictNotice, SaveStatus, useEntityForm } from "../content/form";
import { RenameKeyDialog } from "../content/rename";
import { componentUsagesQuery, Usages } from "../content/usages";
import { ValueField, valueError } from "../content/ValueField";
import { parseValue, textOf, type Value } from "../content/values";
import type { ComponentEntry } from "../document/doc";
import { useProjectDocument } from "../document/react";
import { FolderField, folderTop, payloadField } from "../project/dialogs";
import { type Command, useCommands } from "../project/queries";
import type { Tree } from "../project/tree";
import type { RichTextValue } from "../richtext/convert";
import { RichTextEditor } from "../richtext/RichTextEditor";
import { Button } from "../ui/Button";
import type { Opener } from "../ui/Dialog";
import { TextField, useFocusRefusal } from "../ui/Field";
import { Alert } from "../ui/Page";
import { definitionsOf } from "./types";

/** The name of a component's field of a value of an attribute, which a refusal's location names as the payload's value. */
const attributeFieldOf = (id: string) => `attribute:${id}`;

export interface ComponentPanelProps {
  project: ProjectBody;
  component: ComponentEntry;
  type: ComponentType | undefined;
  tree: Tree<ComponentEntry>;
  editable: boolean;
  /** Tells whether the component's deletion is open, while which the page shows the component as it was. */
  onDeleting: (deleting: boolean) => void;
  /** Called once the component has been deleted, which closes the panel. */
  onDeleted: () => void;
}

/** The contents of a component's panel, under its heading. */
export function ComponentPanel(props: ComponentPanelProps) {
  const { t } = useTranslation();
  const { project, component, editable } = props;
  return (
    <div className="space-y-6">
      <p className="text-fg-muted">
        {t("component.of_type", {
          type: props.type?.name ?? t("component.unknown_type"),
        })}
      </p>
      <WholeComponent {...props} />
      <ComponentImage {...props} />
      {editable && <ComponentActions {...props} />}
      <Usages
        projectId={project.id}
        heading={t("component.usages")}
        lists={(["speaker", "mention", "read", "write"] as const).map(
          (use) => ({
            key: use,
            title: t("component.usage_list", { use }),
            empty: t("component.usage_empty", { use }),
            query: componentUsagesQuery(project.id, component.id, use),
          }),
        )}
      />
    </div>
  );
}

/**
 * The component's form, or what a reader sees of it, once the document
 * holds the component whole, which it reads as the panel opens, and again
 * once a read of the components' list has taken its description and its
 * values away, as of a newer revision.
 */
function WholeComponent(props: ComponentPanelProps) {
  const { t } = useTranslation();
  const { project, component, editable } = props;
  const doc = useProjectDocument(project.id);
  const whole = component.attributes !== undefined;
  const [failure, setFailure] = useState<unknown>();
  const readWhole = useCallback(() => {
    setFailure(undefined);
    doc.readComponent(component.id).catch(setFailure);
  }, [doc, component.id]);
  useEffect(() => {
    if (!whole) {
      readWhole();
    }
  }, [whole, readWhole]);
  if (!whole) {
    return failure === undefined ? (
      <p role="status" className="text-fg-muted">
        {t("component.loading")}
      </p>
    ) : (
      <div className="space-y-2">
        <Alert message={refusalOf(failure, t)?.message} />
        <Button onClick={readWhole}>{t("component.try_again")}</Button>
      </div>
    );
  }
  const read = component as Component;
  return editable ? (
    <ComponentForm {...props} component={read} />
  ) : (
    <ComponentView {...props} component={read} />
  );
}

/** A component as a role that does not edit the project reads it, or as it is while the project is archived. */
function ComponentView({
  component,
  type,
  tree,
}: Omit<ComponentPanelProps, "component"> & { component: Component }) {
  const { t } = useTranslation();
  const folder =
    component.folder_id === undefined
      ? undefined
      : tree.byId.get(component.folder_id);
  return (
    <div className="space-y-4">
      <RichTextEditor
        label={t("component.description")}
        defaultValue={component.description}
        editable={false}
      />
      <dl className="grid gap-x-6 gap-y-1 sm:grid-cols-[max-content_1fr]">
        {definitionsOf(type).map((attribute) => (
          <AttributeValue
            key={attribute.id}
            attribute={attribute}
            value={component.attributes[attribute.id] ?? attribute.default}
          />
        ))}
        <dt className="text-fg-muted">{t("content.folder")}</dt>
        <dd className="break-words">
          {folder?.kind === "folder" ? folder.folder.name : t("boards.top")}
        </dd>
      </dl>
    </div>
  );
}

function AttributeValue({
  attribute,
  value,
}: {
  attribute: Attribute;
  value: Value;
}) {
  return (
    <>
      <dt className="break-words text-fg-muted">{attribute.name}</dt>
      <dd className="font-mono break-all whitespace-pre-wrap">
        {textOf(value)}
      </dd>
    </>
  );
}

/** What the form of a component holds, as its submission reads it. */
interface ComponentTyped {
  name: string;
  /** The description as the editor gives it, or undefined if the user did not change it. */
  description: RichTextDoc | undefined;
  attributes: Record<string, Value>;
  folder_id: string | undefined;
}

/** Returns the commands that change against, as the form showed it, to typed, each expecting rev. */
function changesOf(
  against: Component,
  typed: ComponentTyped,
  definitions: readonly Attribute[],
  rev: number,
): Command[] {
  const id = against.id;
  const expected_rev = { [id]: rev };
  const changes: Command[] = [];
  if (typed.name !== against.name) {
    changes.push({
      type: "component.rename",
      payload: { id, name: typed.name },
      expected_rev,
    });
  }
  if (
    typed.description !== undefined &&
    JSON.stringify(typed.description) !== JSON.stringify(against.description)
  ) {
    changes.push({
      type: "component.describe",
      payload: { id, description: typed.description },
      expected_rev,
    });
  }
  for (const attribute of definitions) {
    const value = typed.attributes[attribute.id];
    if (
      value !== undefined &&
      value !== (against.attributes[attribute.id] ?? attribute.default)
    ) {
      changes.push({
        type: "component.set_attribute",
        payload: { id, attribute_id: attribute.id, value },
        expected_rev,
      });
    }
  }
  if (typed.folder_id !== against.folder_id) {
    changes.push({
      type: "component.move",
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
 * The form of a component, which sends what of it changed in one batch
 * (useEntityForm), and refuses itself a value that is none of its type, or
 * a description that the server would refuse, before anything is sent.
 */
function ComponentForm({
  project,
  component,
  type,
  tree,
}: Omit<ComponentPanelProps, "component"> & { component: Component }) {
  const { t } = useTranslation();
  const form = useEntityForm(project.id, component, (view, id) => {
    const now = view.components.get(id);
    return now?.attributes === undefined ? undefined : (now as Component);
  });
  const { commands, held, shown, against } = form;
  // What the editor of the description gave last, of the form shown now:
  // the document, or why the server would refuse it.
  const [description, setDescription] = useState<{
    key: string;
    value: RichTextValue;
  }>();
  const typedDescription =
    description?.key === held.key ? description.value : undefined;
  // The values that are none of their types, and a description that the
  // server would refuse, which the form refuses itself, with a mark of its
  // own for each attempt (useFocusRefusal).
  const [invalid, setInvalid] = useState<{ fields: Record<string, string> }>();
  const definitions = definitionsOf(type);
  // The batch that the form sent last, whose commands a refusal's location
  // names by their places in it.
  const [lastSent, setLastSent] = useState<Command[]>([]);
  const fieldOf = (location: string) => {
    const field = payloadField("name", "description", "folder_id")(location);
    if (field !== undefined) {
      return field;
    }
    // A value refused at the payload's value names the command's attribute.
    const at = /^body\.commands\[(\d+)\]\.payload\.value$/.exec(location);
    const sent = at === null ? undefined : lastSent[Number(at[1])];
    return sent?.type === "component.set_attribute"
      ? attributeFieldOf(sent.payload.attribute_id)
      : undefined;
  };
  const refusal = refusalOf(commands.error, t, fieldOf, {
    key_taken: t("component.error.key_taken"),
  });
  useFocusRefusal(held.form, invalid ?? commands.error);
  // A description that the server would refuse says why under it, and is
  // marked invalid, by the editor itself.
  const errors: Record<string, string> = {
    ...refusal?.fields,
    ...invalid?.fields,
  };
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const attributes: Record<string, Value> = {};
    const refused: Record<string, string> = {};
    for (const attribute of definitions) {
      const field = attributeFieldOf(attribute.id);
      const value = parseValue(
        attribute.type,
        String(data.get(field) ?? ""),
        attribute.options ?? [],
      );
      if (value === undefined) {
        refused[field] = valueError(t, attribute.type);
      } else {
        attributes[attribute.id] = value;
      }
    }
    if (
      Object.keys(refused).length > 0 ||
      typedDescription?.error !== undefined
    ) {
      form.refused();
      setInvalid({ fields: refused });
      return;
    }
    setInvalid(undefined);
    const folder = String(data.get("folder") ?? folderTop);
    const typed: ComponentTyped = {
      name: String(data.get("name")),
      description: typedDescription?.doc,
      attributes,
      folder_id: folder === folderTop ? undefined : folder,
    };
    const changes = changesOf(against, typed, definitions, against.rev);
    setLastSent(changes);
    form.save(changes, {
      name: typed.name,
      ...(typed.description === undefined
        ? {}
        : { description: typed.description }),
      attributes: { ...against.attributes, ...typed.attributes },
      folder_id: typed.folder_id,
    });
  };
  const fieldName = (field: string) =>
    field === "name"
      ? t("component.name")
      : field === "description"
        ? t("component.description")
        : field === "attributes"
          ? t("component.values")
          : t("content.folder");
  const shownValue = (field: string, value: unknown) => {
    if (field === "folder_id") {
      const folder =
        typeof value === "string" ? tree.byId.get(value) : undefined;
      return folder?.kind === "folder" ? folder.folder.name : t("boards.top");
    }
    if (field === "name") {
      return String(value ?? "");
    }
    // A description or the values do not read as a line of text.
    return undefined;
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
      <form
        key={held.key}
        {...held.props}
        className="space-y-4"
        onSubmit={submit}
        noValidate
      >
        <TextField
          label={t("component.name")}
          name="name"
          autoComplete="off"
          required
          maxLength={100}
          defaultValue={shown.name}
          error={errors.name}
        />
        <RichTextEditor
          label={t("component.description")}
          defaultValue={shown.description}
          error={errors.description}
          onChange={(value) => {
            setDescription({ key: held.key, value });
            held.props.onChange();
          }}
        />
        {definitions.length > 0 && (
          <fieldset className="space-y-4">
            <legend className="font-semibold">{t("component.values")}</legend>
            {definitions.map((attribute) => (
              <ValueField
                key={attribute.id}
                label={attribute.name}
                hint={t("component.value_hint", {
                  key: attribute.key,
                  type: t("value.type", { type: attribute.type }),
                })}
                name={attributeFieldOf(attribute.id)}
                type={attribute.type}
                options={attribute.options ?? []}
                defaultValue={
                  shown.attributes[attribute.id] ?? attribute.default
                }
                error={errors[attributeFieldOf(attribute.id)]}
              />
            ))}
          </fieldset>
        )}
        <FolderField
          tree={tree}
          label={t("content.folder")}
          initial={shown.folder_id}
          error={errors.folder_id}
        />
        <Button type="submit" variant="primary" disabled={commands.isPending}>
          {t("component.save")}
        </Button>
      </form>
    </div>
  );
}

/** The URL of an asset's thumbnail, which the browser reads with the page's session. */
function thumbnailOf(projectId: string, assetId: string): string {
  return `/api/v1/projects/${encodeURIComponent(projectId)}/assets/${encodeURIComponent(assetId)}/thumbnail`;
}

/** The types of image that the server stores (PRD FR-ASSET-02). */
const imageTypes = "image/png,image/jpeg,image/gif,image/webp,image/svg+xml";

/** The component's image: its thumbnail, and, for an editor, its upload and its removal, each a change of its own. */
function ComponentImage({ project, component, editable }: ComponentPanelProps) {
  const { t } = useTranslation();
  const inputId = useId();
  const session = useQuery(sessionQuery).data;
  const commands = useCommands(project.id);
  const upload = useMutation({
    mutationFn: async (file: File) =>
      (
        await api.call("uploadAsset", {
          path: { project_id: project.id },
          headers: csrfHeaders(session),
          body: file,
        })
      ).body,
  });
  const setImage = (assetId: string | undefined) =>
    commands.mutate([
      {
        type: "component.set_image",
        payload: {
          id: component.id,
          ...(assetId === undefined ? {} : { image_asset_id: assetId }),
        },
        expected_rev: { [component.id]: component.rev },
      },
    ]);
  const chosen = (event: ChangeEvent<HTMLInputElement>) => {
    const input = event.currentTarget;
    const file = input.files?.[0];
    if (file === undefined) {
      return;
    }
    commands.reset();
    upload.mutate(file, {
      onSuccess: (asset) => setImage(asset.id),
      // The same file chosen again is a change of the input's own.
      onSettled: () => {
        input.value = "";
      },
    });
  };
  const pending = upload.isPending || commands.isPending;
  const refusal = refusalOf(upload.error ?? commands.error, t);
  return (
    <section className="space-y-2">
      <h3 className="text-lg font-semibold">{t("component.image")}</h3>
      {component.image_asset_id === undefined ? (
        <p className="text-fg-muted">{t("component.no_image")}</p>
      ) : (
        <img
          src={thumbnailOf(project.id, component.image_asset_id)}
          alt={t("component.image_alt", { name: component.name })}
          className="max-h-32 max-w-full rounded-md border border-border"
        />
      )}
      <Alert message={refusal?.message} />
      {editable && (
        <div className="flex flex-wrap items-end gap-2">
          <div className="flex flex-col gap-1">
            <label htmlFor={inputId} className="font-medium">
              {component.image_asset_id === undefined
                ? t("component.upload_image")
                : t("component.replace_image")}
            </label>
            <input
              id={inputId}
              type="file"
              accept={imageTypes}
              disabled={pending}
              onChange={chosen}
              className="min-h-11 max-w-full file:me-2 file:min-h-11 file:cursor-pointer file:rounded-md file:border file:border-border file:bg-surface file:px-4 file:text-fg"
            />
          </div>
          {component.image_asset_id !== undefined && (
            <Button disabled={pending} onClick={() => setImage(undefined)}>
              {t("component.remove_image")}
            </Button>
          )}
        </div>
      )}
    </section>
  );
}

/** A dialog of the component's actions, with the control that opened it. */
type Open = { dialog: "rename" | "delete"; opener: Opener };

/** The component's key, which a refactor renames, and its deletion. */
function ComponentActions({
  project,
  component,
  onDeleting,
  onDeleted,
}: ComponentPanelProps) {
  const { t } = useTranslation();
  const [open, setOpen] = useState<Open>();
  return (
    <section className="space-y-2">
      <h3 className="text-lg font-semibold">{t("component.key")}</h3>
      <p className="font-mono break-all">{component.key}</p>
      <div className="flex flex-wrap gap-2">
        <Button
          aria-haspopup="dialog"
          onClick={(event) =>
            setOpen({ dialog: "rename", opener: event.currentTarget })
          }
        >
          {t("component.rename_key")}
        </Button>
        <Button
          variant="danger"
          aria-haspopup="dialog"
          onClick={(event) => {
            onDeleting(true);
            setOpen({ dialog: "delete", opener: event.currentTarget });
          }}
        >
          {t("component.delete")}
        </Button>
      </div>
      {open?.dialog === "rename" && (
        <RenameKeyDialog
          projectId={project.id}
          renamed={component}
          opener={open.opener}
          onClose={() => setOpen(undefined)}
        />
      )}
      {open?.dialog === "delete" && (
        <DeleteContentDialog
          project={project}
          deleted={{
            id: component.id,
            name: component.name,
            rev: component.rev,
          }}
          opener={open.opener}
          onClose={() => {
            setOpen(undefined);
            onDeleting(false);
          }}
          onDeleted={onDeleted}
        />
      )}
    </section>
  );
}
