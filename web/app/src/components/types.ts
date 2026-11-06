// What the pages of a project's components read of their types (PRD
// FR-COMP-01): the types, by their names, and a type's attribute
// definitions, in their order. The project's document holds none of either
// that is in the trash: a deletion takes it away, and the lists, and the
// states that the server gives, leave the trash out.

import type { Attribute, ComponentType } from "@storycreep/shared/api";
import { compareChildren } from "../project/orderkey";

/** Returns the types of types by their names. */
export function typesByName(
  types: ReadonlyMap<string, ComponentType>,
): ComponentType[] {
  return [...types.values()].sort(
    (a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id),
  );
}

/** Returns the attribute definitions of type, in their order. */
export function definitionsOf(type: ComponentType | undefined): Attribute[] {
  return [...(type?.attributes ?? [])].sort(compareChildren);
}
