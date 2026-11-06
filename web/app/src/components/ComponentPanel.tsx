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
  useRef,
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
import {
  typeName,
  ValueField,
  valueError,
  valueText,
} from "../content/ValueField";
import { parseValue, textOf, type Value } from "../content/values";
import type { ComponentEntry } from "../document/doc";
import { useProjectDocument } from "../document/react";
import { equal } from "../document/reconcile";
import { chosenFolder, FolderField, payloadField } from "../project/dialogs";
import type { Command } from "../project/queries";
import { folderNameOf, type Tree } from "../project/tree";
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

/** What a read of a component whole ended with, where it ended without the component whole, as it kept changing while it was read. */
const unsettled = Symbol("unsettled");

/**
 * The component's form, or what a reader sees of it, once the document
 * holds the component whole, which it reads as the panel opens, and again
 * once a read of the components' list has taken its description and its
 * values away, as of a newer revision, while which the panel shows it as it
 * was last whole, rather than take the form away, with what the user typed
 * in it (FR-RT-02 AC3). A read that ended without it whole, or that failed,
 * is offered again.
 */
function WholeComponent(props: ComponentPanelProps) {
  const { t } = useTranslation();
  const { project, component, editable } = props;
  const doc = useProjectDocument(project.id);
  const whole = component.attributes !== undefined;
  const [failure, setFailure] = useState<unknown>();
  const readWhole = useCallback(() => {
    setFailure(undefined);
    doc.readComponent(component.id).then(() => {
      const now = doc.store.getState().view.components.get(component.id);
      if (now !== undefined && now.attributes === undefined) {
        setFailure(unsettled);
      }
    }, setFailure);
  }, [doc, component.id]);
  useEffect(() => {
    if (!whole) {
      readWhole();
    }
  }, [whole, readWhole]);
  const last = useRef<Component>(undefined);
  if (whole) {
    last.current = component as Component;
  }
  const read = last.current;
  const failed = failure !== undefined && (
    <div className="space-y-2">
      <Alert
        message={
          failure === unsettled
            ? t("component.unsettled")
            : refusalOf(failure, t)?.message
        }
      />
      <Button onClick={readWhole}>{t("component.try_again")}</Button>
    </div>
  );
  if (read === undefined) {
    return (
      failed || (
        <p role="status" className="text-fg-muted">
          {t("component.loading")}
        </p>
      )
    );
  }
  return (
    <>
      {!whole && failed}
      {editable ? (
        <ComponentForm {...props} component={read} />
      ) : (
        <ComponentView {...props} component={read} />
      )}
    </>
  );
}

/** A component as a role that does not edit the project reads it, or as it is while the project is archived. */
function ComponentView({
  component,
  type,
  tree,
}: Omit<ComponentPanelProps, "component"> & { component: Component }) {
  const { t } = useTranslation();
  return (
    <div className="space-y-4">
      {/* Shown anew once the component changes, as its description does
          elsewhere, which the editor shows only as it starts. */}
      <RichTextEditor
        key={component.rev}
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
          {folderNameOf(tree, component.folder_id) ?? t("boards.top")}
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
  const { t } = useTranslation();
  return (
    <>
      <dt className="break-words text-fg-muted">{attribute.name}</dt>
      <dd className="font-mono break-all whitespace-pre-wrap">
        {valueText(t, value)}
      </dd>
    </>
  );
}

/** What the form of a component holds, as its submission reads it. */
interface ComponentTyped {
  name: string;
  /** The description as the editor holds it. */
  description: RichTextDoc;
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
  if (!equal(typed.description, against.description)) {
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
  // The component as the document has it now, once a save was refused for
  // a change made elsewhere; one that the document has without its values,
  // as once a read of the list took them away, is as the form last had it
  // whole, which the panel reads again, rather than gone, which would take
  // away what the user typed.
  const form = useEntityForm(project.id, component, (view, id) => {
    const now = view.components.get(id);
    return now === undefined
      ? undefined
      : now.attributes === undefined
        ? component
        : (now as Component);
  });
  const { commands, held, shown, against } = form;
  // What the editor of the description gave last, of the form shown now:
  // the document, or why the server would refuse it, or nothing while the
  // user has not changed it, which it holds as the form shows it. Only a
  // submission reads it, so a change of the text, at each key, does not
  // render the form, with each of its values, again.
  const description = useRef<{ key: string; value: RichTextValue }>(undefined);
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
    const typedDescription =
      description.current?.key === held.key
        ? description.current.value
        : undefined;
    const attributes: Record<string, Value> = {};
    const refused: Record<string, string> = {};
    for (const attribute of definitions) {
      const field = attributeFieldOf(attribute.id);
      const text = String(data.get(field) ?? "");
      const was = shown.attributes[attribute.id] ?? attribute.default;
      // A value that the user left as the form shows it is kept as it is,
      // though it is none of its definition's type or options now, as once
      // they changed elsewhere while the form held it, which the server
      // gave a value of their own, rather than refuse a save of another
      // field for it.
      if (text === textOf(was)) {
        attributes[attribute.id] = was;
        continue;
      }
      const value = parseValue(attribute.type, text, attribute.options ?? []);
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
    const typed: ComponentTyped = {
      name: String(data.get("name")),
      // The editor starts with the description as the form shows it, which
      // is what the user changed once a save was refused for a change made
      // elsewhere, which the form holds.
      description: typedDescription?.doc ?? shown.description,
      attributes,
      folder_id: chosenFolder(data, shown.folder_id),
    };
    const changes = changesOf(against, typed, definitions, against.rev);
    setLastSent(changes);
    // What the user changed, and nothing else of what the form shows, which
    // a save refused for a change made elsewhere holds over the component as
    // it is now: of its values, those that the batch sets.
    const changedValues: Record<string, Value> = Object.fromEntries(
      changes.flatMap((c) =>
        c.type === "component.set_attribute"
          ? [[c.payload.attribute_id, c.payload.value]]
          : [],
      ),
    );
    form.save(
      changes,
      {
        ...(typed.name === against.name ? {} : { name: typed.name }),
        ...(equal(typed.description, against.description)
          ? {}
          : { description: typed.description }),
        ...(Object.keys(changedValues).length === 0
          ? {}
          : { attributes: changedValues }),
        ...(typed.folder_id === against.folder_id
          ? {}
          : { folder_id: typed.folder_id }),
      },
      (now, changed) => ({
        ...now,
        ...changed,
        attributes: { ...now.attributes, ...changed.attributes },
      }),
    );
  };
  // The order is the component's place in its folder, which a move changes
  // with the folder.
  const fieldName = (field: string) =>
    field === "name"
      ? t("component.name")
      : field === "description"
        ? t("component.description")
        : field === "attributes"
          ? t("component.values")
          : field === "order"
            ? t("content.place")
            : t("content.folder");
  const shownValue = (field: string, value: unknown) => {
    if (field === "folder_id") {
      return folderNameOf(tree, value) ?? t("boards.top");
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
            description.current = { key: held.key, value };
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
                  type: typeName(t, attribute.type),
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

/**
 * The component's image: its thumbnail, and, for an editor, its upload and
 * its removal, each a change of its own, expecting the revision that the
 * panel showed as the user asked for it.
 */
function ComponentImage({ project, component, editable }: ComponentPanelProps) {
  const { t } = useTranslation();
  const inputId = useId();
  const session = useQuery(sessionQuery).data;
  const doc = useProjectDocument(project.id);
  // An upload and the command that sets the image to what it uploaded are
  // one request of the page's, which goes on to set the image once the
  // panel has gone, as once the user opened another component meanwhile,
  // rather than leave the asset uploaded and the component without it. The
  // removal, without a file, is the command alone. Each attempt says what
  // became of it alone, rather than what became of the one before it.
  const image = useMutation({
    mutationFn: async ({ file, rev }: { file?: File; rev: number }) => {
      const asset =
        file === undefined
          ? undefined
          : (
              await api.call("uploadAsset", {
                path: { project_id: project.id },
                headers: csrfHeaders(session),
                body: file,
              })
            ).body;
      return doc.send([
        {
          type: "component.set_image",
          payload: {
            id: component.id,
            ...(asset === undefined ? {} : { image_asset_id: asset.id }),
          },
          expected_rev: { [component.id]: rev },
        },
      ]);
    },
  });
  const chosen = (event: ChangeEvent<HTMLInputElement>) => {
    const input = event.currentTarget;
    const file = input.files?.[0];
    if (file === undefined) {
      return;
    }
    image.mutate(
      { file, rev: component.rev },
      {
        // The same file chosen again is a change of the input's own.
        onSettled: () => {
          input.value = "";
        },
      },
    );
  };
  const pending = image.isPending;
  const refusal = refusalOf(image.error, t);
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
            <Button
              disabled={pending}
              onClick={() => image.mutate({ rev: component.rev })}
            >
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
          onClick={(event) =>
            setOpen({ dialog: "delete", opener: event.currentTarget })
          }
        >
          {t("component.delete")}
        </Button>
      </div>
      {open?.dialog === "rename" && (
        <RenameKeyDialog
          projectId={project.id}
          renamed={{ kind: "component", ...component }}
          opener={open.opener}
          onClose={() => setOpen(undefined)}
        />
      )}
      {open?.dialog === "delete" && (
        <DeleteContentDialog
          project={project}
          deleted={{
            kind: "component",
            id: component.id,
            name: component.name,
            rev: component.rev,
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
