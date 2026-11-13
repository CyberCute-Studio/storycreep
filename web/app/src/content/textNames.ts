// What the rich text of a project's content names, and what its { and @
// offer (PRD FR-TEXT-02, FR-TEXT-03, plan note N73), as the project's
// document has them: a value reference shows its value's name as an
// expression names it, a variable's key or a component's key and its
// attribute's, and a mention its component's name, each read by the IDs
// that the text stores, so that a rename shows in every text at once,
// without a change of the texts; and { offers the values that an
// expression of the text's board reads (§5.8.3), its variables, the global
// ones, and the components' runtime attributes, and @ the components.

import type { TFunction } from "i18next";
import { useEffect, useMemo } from "react";
import { useTranslation } from "react-i18next";
import type { Doc } from "../document/doc";
import { useDocumentState, useProjectDocument } from "../document/react";
import type {
  MentionChoice,
  RichTextChoices,
  ValueChoice,
} from "../richtext/choices";
import type { RichTextNames } from "../richtext/extensions";

/** What the names of a text's references are read from: the project's document's variables, components, and component types. */
export type NameSources = Pick<
  Doc,
  "variables" | "components" | "componentTypes"
>;

/**
 * Returns the name that a value reference shows of what it refers to, as
 * an expression names it (§5.8.3): a variable's key, as gold, of a ref
 * without an attribute, or the key of the component of ref and its
 * attribute's, as elara.trust; or undefined, of what view does not have, as
 * an attribute that the component's type does not define.
 */
export function valueName(
  view: NameSources,
  ref: string,
  attribute: string | undefined,
): string | undefined {
  if (attribute === undefined) {
    return view.variables.get(ref)?.key;
  }
  const component = view.components.get(ref);
  const definition =
    component === undefined
      ? undefined
      : view.componentTypes
          .get(component.component_type_id)
          ?.attributes.find((a) => a.id === attribute);
  return component === undefined || definition === undefined
    ? undefined
    : `${component.key}.${definition.key}`;
}

/** Returns the names that a text's mentions and value references show, as view has them. */
export function textNames(view: NameSources): RichTextNames {
  return {
    mention: (id) => view.components.get(id)?.name,
    value: (ref, attribute) => valueName(view, ref, attribute),
  };
}

/** Compares two choices by their names, as the list orders them. */
function byName(a: { name: string }, b: { name: string }): number {
  return a.name.localeCompare(b.name);
}

/**
 * Returns what the { and @ of a text of the board of boardId, or of none,
 * as a component's description, offer of view: the global variables and
 * the board's own, which an expression of the board reads, and the runtime
 * attributes of each component, which expressions read as elara.trust
 * (FR-COMP-03), but not one that is not runtime, nor a variable of another
 * board, which a runtime does not load there, and which validation reports
 * (ref_dangling); and every component, to mention. Each list is in the
 * order of the names.
 */
export function textChoices(
  view: NameSources,
  boardId: string | undefined,
  t: TFunction,
): RichTextChoices {
  const values: ValueChoice[] = [];
  for (const variable of view.variables.values()) {
    if (variable.deleted_at !== undefined) {
      continue;
    }
    if (variable.scope === "global") {
      values.push({
        ref: variable.id,
        name: variable.key,
        detail: t("richtext.suggest.global"),
      });
    } else if (boardId !== undefined && variable.board_id === boardId) {
      values.push({
        ref: variable.id,
        name: variable.key,
        detail: t("richtext.suggest.board"),
      });
    }
  }
  const mentions: MentionChoice[] = [];
  for (const component of view.components.values()) {
    if (component.deleted_at !== undefined) {
      continue;
    }
    const type = view.componentTypes.get(component.component_type_id);
    mentions.push({
      id: component.id,
      name: component.name,
      detail: type?.name ?? "",
      terms: [component.key],
    });
    for (const attribute of type?.attributes ?? []) {
      if (attribute.runtime && attribute.deleted_at === undefined) {
        values.push({
          ref: component.id,
          attribute: attribute.id,
          name: `${component.key}.${attribute.key}`,
          detail: t("richtext.suggest.attribute", {
            attribute: attribute.name,
            component: component.name,
          }),
          terms: [`${component.name} ${attribute.name}`],
        });
      }
    }
  }
  return { values: values.sort(byName), mentions: mentions.sort(byName) };
}

/**
 * Returns the names that the rich text of a project's content shows, and
 * what its { and @ offer, of a text of the board of boardId, or of none, as
 * the project's document has them, each the same object until what it is
 * made of changes, as the editor shows its references again whenever its
 * names are another object. A page that has not read the project's
 * variables or its components, as the components' page has not read the
 * variables, has the document read them, so that its texts name what they
 * refer to; a read that failed, which the document records, leaves them
 * named as what they are.
 */
export function useTextNames(
  projectId: string,
  boardId?: string,
): { names: RichTextNames; choices: RichTextChoices } {
  const { t } = useTranslation();
  const doc = useProjectDocument(projectId);
  const variables = useDocumentState(doc, (s) => s.view.variables);
  const components = useDocumentState(doc, (s) => s.view.components);
  const componentTypes = useDocumentState(doc, (s) => s.view.componentTypes);
  useEffect(() => {
    for (const part of ["variables", "components"] as const) {
      if (!doc.holds(part)) {
        void doc.load(part).catch(() => undefined);
      }
    }
  }, [doc]);
  return useMemo(() => {
    const view = { variables, components, componentTypes };
    return { names: textNames(view), choices: textChoices(view, boardId, t) };
  }, [variables, components, componentTypes, boardId, t]);
}
