// What the expressions of a board can name (PRD §5.8.3), as the project's
// document has it, which the expression editor checks a source against as
// it is typed, as the server will once it is saved (FR-EXPR-02): the
// project's variables, global and of each board, and its components, with
// their types' attributes, none of them in the trash, as a Project of the
// shared expression language; and its nodes, which the document holds of
// the boards that the page has read, and which a source's node references
// are looked up in as it is checked (nodesOf). What reads them for a page
// is useScope.ts.

import {
  type BoardNode,
  type Component,
  Project,
  UsageError,
  type Variable,
} from "@storycreep/shared/expr/compile";
import type { Doc } from "../document/doc";

/** What the names of a board's expressions are read from: the project's document's variables, components, component types, boards, and nodes. */
export type ScopeSources = Pick<
  Doc,
  "variables" | "components" | "componentTypes" | "boards" | "nodes" | "gone"
>;

/** What the expressions of a board can name, and what the editor shows of it. */
export interface Scope {
  /** The ID of the board whose expressions they are. */
  readonly boardId: string;
  readonly view: ScopeSources;
  /**
   * The Project of the document's variables and components, of no node,
   * or undefined where the document holds what no project has, as for a
   * moment two components of one key, one of which another session has
   * taken to the trash, or the key from, as the document has heard of one
   * change and not yet of the other: the editor then checks a source's
   * syntax alone, and the server, which is the authority, the rest.
   */
  readonly project: Project | undefined;
}

/** Returns the variables of view that expressions name, those that are not in the trash, as the expression language reads them. */
function variablesOf(view: ScopeSources): Variable[] {
  const out: Variable[] = [];
  for (const v of view.variables.values()) {
    if (v.deleted_at !== undefined) {
      continue;
    }
    out.push({
      id: v.id,
      key: v.key,
      type: v.type,
      ...(v.scope === "board" && v.board_id !== undefined
        ? { board: v.board_id }
        : {}),
    });
  }
  return out;
}

/** Returns the components of view that expressions name, those that are not in the trash, each with its type's attributes, as the expression language reads them. */
function componentsOf(view: ScopeSources): Component[] {
  const out: Component[] = [];
  for (const c of view.components.values()) {
    if (c.deleted_at !== undefined) {
      continue;
    }
    const type = view.componentTypes.get(c.component_type_id);
    const attributes =
      type === undefined || type.deleted_at !== undefined
        ? []
        : type.attributes
            .filter((a) => a.deleted_at === undefined)
            .map((a) => ({
              id: a.id,
              key: a.key,
              type: a.type,
              runtime: a.runtime,
              ...(a.type === "enum" ? { options: a.options ?? [] } : {}),
            }));
    out.push({ id: c.id, key: c.key, attributes });
  }
  return out;
}

/** Returns the Project of what view holds of the variables and the components, or undefined where it holds what no project has (Scope's project). */
export function projectOf(view: ScopeSources): Project | undefined {
  try {
    return Project.of(variablesOf(view), componentsOf(view), []);
  } catch (e) {
    if (e instanceof UsageError) {
      return undefined;
    }
    throw e;
  }
}

/** Returns the Scope of the board of boardId of view. */
export function scopeOf(view: ScopeSources, boardId: string): Scope {
  return { boardId, view, project: projectOf(view) };
}

/**
 * Returns the nodes of the IDs that a source's node references name, as a
 * Project adds them (Project.with): each that the document holds, and is
 * not in the trash, with whether it is a jumper; and each that it does not
 * hold, of a board that the page has not read, as a node that is no jumper,
 * since the server, which reads every board, tells of it once the source is
 * saved. One that the document holds in the trash, or knows went, is left
 * out, which the check then finds unknown (unknown_node), as the server
 * does.
 */
export function nodesOf(
  view: ScopeSources,
  ids: Iterable<string>,
): BoardNode[] {
  const out: BoardNode[] = [];
  const seen = new Set<string>();
  for (const id of ids) {
    if (seen.has(id)) {
      continue;
    }
    seen.add(id);
    const node = view.nodes.get(id);
    if (node !== undefined) {
      if (node.deleted_at === undefined) {
        out.push({ id, jumper: node.type === "jumper" });
      }
    } else if (!view.gone.has(id)) {
      out.push({ id });
    }
  }
  return out;
}
