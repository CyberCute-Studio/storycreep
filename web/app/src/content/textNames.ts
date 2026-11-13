// What the rich text of a project's content names, and what its { and @
// offer (PRD FR-TEXT-02, FR-TEXT-03, plan note N73), as the project's
// document has them: a value reference shows its value's name as an
// expression of the text's board names it, a variable's key or a
// component's key and its attribute's, and a mention its component's name,
// each read by the IDs that the text stores, so that a rename shows in every
// text at once, without a change of the texts; and { offers the values that
// an expression of the text's board reads (§5.8.3), its variables, the
// global ones, and the components' runtime attributes, and @ the
// components. What reads them from the document for a page is
// useTextNames.ts; what is here reads nothing but what it is given, as the
// canvas's cards and tree do.

import type { Variable } from "@storycreep/shared/api";
import type { TFunction } from "i18next";
import type { Doc } from "../document/doc";
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

/** What names the value that a value reference of ref and attribute refers to, as the text shows it, or undefined, of what the project's document does not have. */
export type ValueNamer = (
  ref: string,
  attribute: string | undefined,
) => string | undefined;

/**
 * The keys that an expression of a board, or of none, reads as variables
 * by their bare keys (§5.8.3): those of the board's variables, which a
 * global one of the same key is named global. and its key beside, and those
 * of the global ones too, which a component of the same key is named cmp.,
 * its key, and its attribute's beside. A variable in the trash is no
 * variable that an expression reads, and its key is free (FR-VAR-01).
 */
interface Shadows {
  board: ReadonlySet<string>;
  read: ReadonlySet<string>;
}

/** Returns the keys that an expression of the board of boardId, or of none, reads as variables of view. */
function shadowsOf(view: NameSources, boardId: string | undefined): Shadows {
  const board = new Set<string>();
  const read = new Set<string>();
  for (const variable of view.variables.values()) {
    if (variable.deleted_at !== undefined) {
      continue;
    }
    if (variable.scope === "global") {
      read.add(variable.key);
    } else if (boardId !== undefined && variable.board_id === boardId) {
      board.add(variable.key);
      read.add(variable.key);
    }
  }
  return { board, read };
}

/** Returns the name of variable as an expression of the board of shadows names it: its key, or global. and its key, of a global one whose key the board's variable has too. */
function variableName(variable: Variable, shadows: Shadows): string {
  return variable.scope === "global" && shadows.board.has(variable.key)
    ? `global.${variable.key}`
    : variable.key;
}

/** Returns the name of a component's attribute, of their keys, as an expression of the board of shadows names it: the two keys, joined by a dot, or cmp. and them, of a component whose key a variable that the board reads has too. */
function attributeName(
  component: string,
  attribute: string,
  shadows: Shadows,
): string {
  const name = `${component}.${attribute}`;
  return shadows.read.has(component) ? `cmp.${name}` : name;
}

/**
 * Returns what names the value that a value reference of a text of the
 * board of boardId, or of none, as a component's description, refers to,
 * as an expression of the board names it (§5.8.3), of view: a variable by
 * its key, as gold, or, of a global one whose key a variable of the board
 * has too, which that key names there, as global.gold; and a component's
 * attribute by the component's key and the attribute's, as elara.trust, or,
 * of a component whose key a variable that the board reads has too, which
 * that key names there, as cmp.elara.trust; or undefined, of what view does
 * not have, as an attribute that the component's type does not define.
 */
export function valueNamer(
  view: NameSources,
  boardId: string | undefined,
): ValueNamer {
  const shadows = shadowsOf(view, boardId);
  return (ref, attribute) => {
    if (attribute === undefined) {
      const variable = view.variables.get(ref);
      return variable === undefined
        ? undefined
        : variableName(variable, shadows);
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
      : attributeName(component.key, definition.key, shadows);
  };
}

/** Returns the names that the mentions and value references of a text of the board of boardId, or of none, show, as view has them. */
export function textNames(
  view: NameSources,
  boardId: string | undefined,
): RichTextNames {
  return {
    mention: (id) => view.components.get(id)?.name,
    value: valueNamer(view, boardId),
  };
}

/** What orders the choices by their names, as the list does, and the expression editor's offers and its picker's choices: one collator for all of them, rather than one for each comparison, as localeCompare makes. */
export const collator = new Intl.Collator();

/** Compares two choices by their names, as the list orders them. */
export function byName(a: { name: string }, b: { name: string }): number {
  return collator.compare(a.name, b.name);
}

/** Returns a node's title as what names the node shows it, as a usage's site and the expression editor's offers, names, and rendering do: what an untitled one is called, for one without. */
export function nodeTitleText(t: TFunction, title: string): string {
  return title === "" ? t("boards.untitled_node") : title;
}

/**
 * Returns what the { and @ of a text of the board of boardId, or of none,
 * as a component's description, offer of view: the global variables and
 * the board's own, which an expression of the board reads, and the runtime
 * attributes of each component, which expressions read as elara.trust
 * (FR-COMP-03), but not one that is not runtime, nor a variable of another
 * board, which a runtime does not load there, and which validation reports
 * (ref_dangling); and every component, to mention. Each value is named as
 * its reference shows it (valueNamer), and each list is in the order of
 * the names.
 */
export function textChoices(
  view: NameSources,
  boardId: string | undefined,
  t: TFunction,
): RichTextChoices {
  const shadows = shadowsOf(view, boardId);
  const values: ValueChoice[] = [];
  for (const variable of view.variables.values()) {
    if (variable.deleted_at !== undefined) {
      continue;
    }
    if (variable.scope === "global") {
      values.push({
        ref: variable.id,
        name: variableName(variable, shadows),
        detail: t("richtext.suggest.global"),
      });
    } else if (boardId !== undefined && variable.board_id === boardId) {
      values.push({
        ref: variable.id,
        name: variableName(variable, shadows),
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
          name: attributeName(component.key, attribute.key, shadows),
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
