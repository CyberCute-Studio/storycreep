// The appliers of the commands of component types, their attribute
// definitions, and components (PRD FR-COMP-01 to FR-COMP-03), as package
// component applies them. A definition lives in its type's attributes, but a
// change of one changes the definition's revision, not the type's, so their
// appliers record the definition's change alone (Draft.putQuietly); a change
// of a type's definitions that changes its components' values changes their
// revisions too, those of the components in the trash among them. A
// component that the document holds as the list answers it, without its
// description and its values, is changed as far as it holds it. A key or a
// key of order that the payload leaves out is the server's to pick, among
// keys and children that the trash holds too, so it is not sure; and a
// command that changes what expressions can name has the server compile the
// project's expressions that name it again (renames).

import type { Attribute, ComponentType } from "@storycreep/shared/api";
import { compareChildren } from "../../project/orderkey";
import { type Appliers, canonical } from "../apply";
import { type ComponentEntry, type Draft, typeOfAttribute } from "../doc";
import {
  folderChildren,
  idOrNone,
  keyAfter,
  placed,
  renames,
  textOf,
  withField,
} from "./node";
import { canonicalValue } from "./variable";

type Types =
  | `component_type.${"create" | "rename" | "set_key" | "set_icon" | "delete" | "restore"}`
  | `attribute.${"create" | "rename" | "set_key" | "set_default" | "set_runtime" | "set_options" | "retype" | "move" | "delete" | "restore"}`
  | `component.${"create" | "rename" | "set_key" | "describe" | "set_image" | "set_attribute" | "move" | "delete" | "restore"}`;

type Value = Attribute["default"];

type Values = Record<string, Value>;

/**
 * Returns the key that the server derives from a name, as far as the
 * document can tell (component/keys.go): its letters and digits in
 * lowercase ASCII, without their accents, each run of other characters an
 * underscore, after an underscore if it starts with a digit, or fallback
 * for none, which the server makes unique among the keys of its scope that
 * are not in the trash, so it is not sure.
 */
function derivedKey(name: string, fallback: string): string {
  const key = name
    .normalize("NFKD")
    .replace(/\p{M}/gu, "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "");
  if (key === "") {
    return fallback;
  }
  return (/^[0-9]/.test(key) ? `_${key}` : key).slice(0, 64).replace(/_+$/, "");
}

/** Returns the zero value of a definition of type, with options, which the server gives one that is created or retyped without a default. */
function zeroOf(type: Attribute["type"], options: readonly string[]): Value {
  switch (type) {
    case "bool":
      return false;
    case "int":
    case "float":
      return 0;
    case "enum":
      return options[0] ?? "";
  }
  return "";
}

/** Returns a payload's values by the canonical IDs of what they are values of. */
function valuesOf(values: Values | undefined): Values {
  return Object.fromEntries(
    Object.entries(values ?? {}).map(([id, value]) => [
      canonical(id),
      canonicalValue(value),
    ]),
  );
}

/** Changes the component type of id with change, if draft holds it. */
function changeType(
  draft: Draft,
  id: string,
  change: (type: ComponentType) => ComponentType,
): void {
  const type = draft.get("componentTypes", canonical(id));
  if (type !== undefined) {
    draft.put("componentTypes", { ...change(type), updated_at: draft.time });
  }
}

/**
 * Puts type in draft with its attribute definitions attributes, in their
 * order, without recording a change of the type, whose revision a change of
 * its definitions does not change.
 */
function putAttributes(
  draft: Draft,
  type: ComponentType,
  attributes: readonly Attribute[],
): void {
  draft.putQuietly("componentTypes", {
    ...type,
    attributes: [...attributes].sort(compareChildren),
  });
}

/**
 * Changes the attribute definition of id with change, if draft holds it,
 * and records its change, with whether it is sure. It returns the
 * definition's type, as it was, and the definition, as it is.
 */
function changeAttribute(
  draft: Draft,
  id: string,
  change: (attribute: Attribute) => Attribute,
  sure = true,
): { type: ComponentType; attribute: Attribute } | undefined {
  const attributeId = canonical(id);
  const type = typeOfAttribute(draft.all("componentTypes"), attributeId);
  const held = type?.attributes.find((a) => a.id === attributeId);
  if (type === undefined || held === undefined) {
    return undefined;
  }
  const attribute = { ...change(held), updated_at: draft.time };
  putAttributes(
    draft,
    type,
    type.attributes.map((a) => (a.id === attributeId ? attribute : a)),
  );
  draft.changed(attributeId, sure);
  return { type, attribute };
}

/**
 * Changes the values that the components of the type of typeId that draft
 * holds hold, with change, which returns what a component's values become,
 * or undefined where they do not change. A component that the document holds
 * without its values is changed with every, whose revision the command
 * changes whatever its values, and left as it is otherwise, as the store
 * reads it if the server changed it.
 */
function changeValues(
  draft: Draft,
  typeId: string,
  change: (values: Values) => Values | undefined,
  every = false,
): void {
  for (const component of [...draft.all("components").values()]) {
    if (component.component_type_id !== typeId) {
      continue;
    }
    const values =
      component.attributes === undefined
        ? undefined
        : change(component.attributes);
    if (values !== undefined) {
      draft.put("components", {
        ...component,
        attributes: values,
        updated_at: draft.time,
      });
    } else if (every) {
      draft.put("components", { ...component, updated_at: draft.time });
    }
  }
}

/** Returns values with the value of the definition of id set to value, or undefined if it holds that one already. */
function withValue(
  values: Values,
  id: string,
  value: Value,
): Values | undefined {
  return values[id] === value ? undefined : { ...values, [id]: value };
}

/** Changes the component of id with change, if draft holds it. */
function changeComponent(
  draft: Draft,
  id: string,
  change: (component: ComponentEntry) => ComponentEntry,
  sure = true,
): void {
  const component = draft.get("components", canonical(id));
  if (component !== undefined) {
    draft.put(
      "components",
      { ...change(component), updated_at: draft.time },
      sure,
    );
  }
}

export const componentAppliers: Appliers<Types> = {
  "component_type.create": (draft, p) => {
    draft.put(
      "componentTypes",
      {
        id: canonical(p.id),
        project_id: draft.project?.id ?? "",
        key: p.key ?? derivedKey(p.name, "type"),
        name: p.name,
        icon: p.icon ?? "none",
        attributes: [],
        rev: 1,
        created_at: draft.time,
        updated_at: draft.time,
      },
      p.key !== undefined,
    );
  },
  "component_type.rename": (draft, p) =>
    changeType(draft, p.id, (t) => ({ ...t, name: p.name })),
  // Expressions name the attributes of the type's components by the
  // components' keys, never by the type's.
  "component_type.set_key": (draft, p) =>
    changeType(draft, p.id, (t) => ({ ...t, key: p.key })),
  "component_type.set_icon": (draft, p) =>
    changeType(draft, p.id, (t) => ({ ...t, icon: p.icon })),
  // A type in use is not deleted, so its components are all in the trash
  // already; its definitions go with it, in its attributes.
  "component_type.delete": (draft, p) => {
    draft.remove("componentTypes", canonical(p.id));
    renames(draft);
  },
  // The type in the trash is none that the document holds: the store reads
  // it, as its operation's revisions name it.
  "component_type.restore": (draft) => renames(draft),
  "attribute.create": (draft, p) => {
    const type = draft.get("componentTypes", canonical(p.component_type_id));
    if (type === undefined) {
      return;
    }
    const id = canonical(p.id);
    const options = p.options ?? [];
    const attribute: Attribute = {
      id,
      component_type_id: type.id,
      order: p.order ?? keyAfter(type.attributes.map((a) => a.order)),
      key: p.key ?? derivedKey(p.name, "attribute"),
      name: p.name,
      type: p.type,
      ...(p.type === "enum" ? { options } : {}),
      default: canonicalValue(p.default ?? zeroOf(p.type, options)),
      runtime: p.runtime ?? false,
      rev: 1,
      created_at: draft.time,
      updated_at: draft.time,
    };
    putAttributes(draft, type, [...type.attributes, attribute]);
    draft.changed(id, p.key !== undefined && p.order !== undefined);
    // Every component of the type is given the default, and changes.
    changeValues(
      draft,
      type.id,
      (values) => ({ ...values, [id]: attribute.default }),
      true,
    );
    renames(draft);
  },
  "attribute.rename": (draft, p) => {
    changeAttribute(draft, p.id, (a) => ({ ...a, name: p.name }));
  },
  // No expression is written anew: those that name the old key no longer
  // compile.
  "attribute.set_key": (draft, p) => {
    changeAttribute(draft, p.id, (a) => ({ ...a, key: p.key }));
    renames(draft);
  },
  // The components' values stay as they are.
  "attribute.set_default": (draft, p) => {
    changeAttribute(draft, p.id, (a) => ({
      ...a,
      default: canonicalValue(p.default),
    }));
  },
  "attribute.set_runtime": (draft, p) => {
    changeAttribute(draft, p.id, (a) => ({ ...a, runtime: p.runtime }));
    renames(draft);
  },
  "attribute.set_options": (draft, p) => {
    const options = p.options ?? [];
    const changed = changeAttribute(draft, p.id, (a) => ({
      ...a,
      options,
      default: canonicalValue(p.default ?? a.default),
    }));
    renames(draft);
    if (changed === undefined) {
      return;
    }
    const { type, attribute } = changed;
    const given = valuesOf(p.values);
    // A component's value is the one that the payload gives it, or the one
    // that it holds, if it is one of the new options, or else the default.
    for (const component of [...draft.all("components").values()]) {
      const held = component.attributes?.[attribute.id];
      if (component.component_type_id !== type.id || held === undefined) {
        continue;
      }
      const value =
        given[component.id] ??
        (options.includes(held as string) ? held : attribute.default);
      const values = withValue(component.attributes ?? {}, attribute.id, value);
      if (values !== undefined) {
        draft.put("components", {
          ...component,
          attributes: values,
          updated_at: draft.time,
        });
      }
    }
  },
  "attribute.retype": (draft, p) => {
    const options = p.options ?? [];
    const changed = changeAttribute(draft, p.id, (a) =>
      withField(
        {
          ...a,
          type: p.type,
          default: canonicalValue(p.default ?? zeroOf(p.type, options)),
        },
        "options",
        p.type === "enum" ? options : undefined,
      ),
    );
    renames(draft);
    if (changed === undefined) {
      return;
    }
    const { type, attribute } = changed;
    const given = valuesOf(p.values);
    // A value that the payload does not give is converted, as the
    // expression language converts it, unless the conversion resets it:
    // the store reads the component of a converted one, if the server
    // changed it.
    for (const component of [...draft.all("components").values()]) {
      const held = component.attributes?.[attribute.id];
      if (component.component_type_id !== type.id || held === undefined) {
        continue;
      }
      const value =
        given[component.id] ??
        (p.conversion === "reset" ? attribute.default : undefined);
      const values =
        value === undefined
          ? undefined
          : withValue(component.attributes ?? {}, attribute.id, value);
      if (values !== undefined) {
        draft.put("components", {
          ...component,
          attributes: values,
          updated_at: draft.time,
        });
      }
    }
  },
  "attribute.move": (draft, p) => {
    const attributeId = canonical(p.id);
    const type = typeOfAttribute(draft.all("componentTypes"), attributeId);
    const held = type?.attributes.find((a) => a.id === attributeId);
    if (type === undefined || held === undefined) {
      return;
    }
    const { order, sure } =
      p.order === undefined
        ? placed(held, type.attributes)
        : { order: p.order, sure: true };
    if (order !== held.order) {
      changeAttribute(draft, attributeId, (a) => ({ ...a, order }), sure);
    }
  },
  "attribute.delete": (draft, p) => {
    const attributeId = canonical(p.id);
    const type = typeOfAttribute(draft.all("componentTypes"), attributeId);
    if (type === undefined) {
      return;
    }
    putAttributes(
      draft,
      type,
      type.attributes.filter((a) => a.id !== attributeId),
    );
    draft.changed(attributeId);
    // Every component of the type changes, and no longer shows its value,
    // which the server keeps for the definition's restoration.
    changeValues(
      draft,
      type.id,
      ({ [attributeId]: _, ...values }) => values,
      true,
    );
    renames(draft);
  },
  // The definition in the trash is none that the document holds, and the
  // payload does not tell its type, nor the values that come back with it:
  // the store reads the components, as its operation's revisions name them,
  // and the types' list again, which brings the definition back in its type,
  // as a definition has no read of its own, and its revision does not
  // change its type's.
  "attribute.restore": (draft) => {
    draft.reread("components");
    renames(draft);
  },
  "component.create": (draft, p) => {
    const type = draft.get("componentTypes", canonical(p.component_type_id));
    const given = valuesOf(p.attributes);
    const description = textOf(p.description ?? undefined);
    const folderId = idOrNone(p.folder_id);
    const image = idOrNone(p.image_asset_id);
    draft.put(
      "components",
      {
        id: canonical(p.id),
        project_id: draft.project?.id ?? "",
        component_type_id: canonical(p.component_type_id),
        ...(folderId === undefined ? {} : { folder_id: folderId }),
        order:
          p.order ??
          keyAfter(
            folderChildren(draft, "component", folderId).map((c) => c.order),
          ),
        key: p.key ?? derivedKey(p.name, "component"),
        name: p.name,
        ...(image === undefined ? {} : { image_asset_id: image }),
        description: description.text,
        // Each definition of the type is given the payload's value of it,
        // or its default.
        attributes:
          type === undefined
            ? given
            : Object.fromEntries(
                type.attributes.map((a) => [a.id, given[a.id] ?? a.default]),
              ),
        rev: 1,
        created_at: draft.time,
        updated_at: draft.time,
      },
      p.key !== undefined &&
        p.order !== undefined &&
        type !== undefined &&
        description.sure,
    );
    renames(draft);
  },
  "component.rename": (draft, p) =>
    changeComponent(draft, p.id, (c) => ({ ...c, name: p.name })),
  // The expressions that name the component's attributes are written anew,
  // which the store reads, as its operation's revisions name them.
  "component.set_key": (draft, p) => {
    changeComponent(draft, p.id, (c) => ({ ...c, key: p.key }));
    renames(draft);
  },
  "component.describe": (draft, p) => {
    const description = textOf(p.description);
    changeComponent(
      draft,
      p.id,
      (c) => ({ ...c, description: description.text }),
      description.sure,
    );
  },
  "component.set_image": (draft, p) =>
    changeComponent(draft, p.id, (c) =>
      withField(c, "image_asset_id", idOrNone(p.image_asset_id)),
    ),
  // A component that the document holds without its values is given the
  // one, but not the others, which the store reads.
  "component.set_attribute": (draft, p) => {
    const component = draft.get("components", canonical(p.id));
    const attributeId = canonical(p.attribute_id);
    const value = canonicalValue(p.value);
    if (
      component === undefined ||
      component.attributes?.[attributeId] === value
    ) {
      return;
    }
    draft.put(
      "components",
      {
        ...component,
        attributes: { ...component.attributes, [attributeId]: value },
        updated_at: draft.time,
      },
      component.attributes !== undefined,
    );
  },
  "component.move": (draft, p) => {
    const component = draft.get("components", canonical(p.id));
    if (component === undefined) {
      return;
    }
    const folderId = idOrNone(p.folder_id);
    const { order, sure } =
      p.order !== undefined
        ? { order: p.order, sure: true }
        : component.folder_id === folderId
          ? placed(component, folderChildren(draft, "component", folderId))
          : {
              order: keyAfter(
                folderChildren(draft, "component", folderId).map(
                  (c) => c.order,
                ),
              ),
              sure: false,
            };
    draft.put(
      "components",
      {
        ...withField(component, "folder_id", folderId),
        order,
        updated_at: draft.time,
      },
      sure,
    );
  },
  "component.delete": (draft, p) => {
    draft.remove("components", canonical(p.id));
    renames(draft);
  },
  // The component in the trash is none that the document holds, nor are
  // its values, which may have changed there, and its restoration brings
  // back its type and its folders: the store reads them, as its operation's
  // revisions name them.
  "component.restore": (draft) => renames(draft),
};
