// What a project's document has yet to read for the whole of the project:
// every board's content, and every component whole, with its description
// and its values, which the list of the components leaves out, of a type,
// neither in the trash. Play mode reads them before it projects the draft
// (play/session.ts), and the device's copy of a pinned project is written
// once the document holds them (offline/keeper.ts, PRD FR-OFF-02), so that
// play runs on the copy offline.

import type { ComponentEntry, Doc, Part } from "./doc";
import { boardPart } from "./doc";
import type { ProjectDocument } from "./store";

/** Reports whether doc has yet to read the content of board, one outside the trash. */
export function unreadBoard(
  doc: ProjectDocument,
  board: { readonly id: string; readonly deleted_at?: string },
): boolean {
  return board.deleted_at === undefined && !doc.hasRead(boardPart(board.id));
}

/** Reports whether view holds component without its description and its values, of a type, neither in the trash, that view holds. */
export function unreadComponent(view: Doc, component: ComponentEntry): boolean {
  const type = view.componentTypes.get(component.component_type_id);
  return (
    component.deleted_at === undefined &&
    component.attributes === undefined &&
    type !== undefined &&
    type.deleted_at === undefined
  );
}

/**
 * Returns what doc has yet to read of the project for its whole, of what
 * view, a document of it, holds: the parts of the tree, the variables, and
 * the components, each board's content, and the IDs of the components to
 * read whole.
 */
export function unreadOf(
  doc: ProjectDocument,
  view: Doc,
): { parts: Part[]; components: string[] } {
  const parts: Part[] = (["tree", "variables", "components"] as const).filter(
    (part) => !doc.hasRead(part),
  );
  for (const board of view.boards.values()) {
    if (unreadBoard(doc, board)) {
      parts.push(boardPart(board.id));
    }
  }
  const components: string[] = [];
  for (const component of view.components.values()) {
    if (unreadComponent(view, component)) {
      components.push(component.id);
    }
  }
  return { parts, components };
}
