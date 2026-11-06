// What the pages of a project's components read of their types (PRD
// FR-COMP-01): the types that are not in the trash, and a type's attribute
// definitions, in their order.

import type { Attribute, ComponentType } from "@storycreep/shared/api";
import { compareChildren } from "../project/orderkey";

/** Returns the types of types that are not in the trash, by their names. */
export function liveTypes(
  types: ReadonlyMap<string, ComponentType>,
): ComponentType[] {
  return [...types.values()]
    .filter((type) => type.deleted_at === undefined)
    .sort((a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id));
}

/** Returns the attribute definitions of type that are not in the trash, in their order. */
export function definitionsOf(type: ComponentType | undefined): Attribute[] {
  return (type?.attributes ?? [])
    .filter((attribute) => attribute.deleted_at === undefined)
    .sort(compareChildren);
}
